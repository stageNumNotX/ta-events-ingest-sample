package traffic

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"

	"ta-events-ingest/internal/scenarios/redirect"
)

// Handler — HTTP-вход traffic-runtime. Resolve/redirect.Build не имеют
// cloud-клиентов; запрос один раз захватывает current contract (или fixture).
type Handler struct {
	log         *slog.Logger
	current     atomic.Pointer[PreparedRelease]
	contract    atomic.Pointer[Contract]
	enqueue     Enqueuer
	mint        func() (string, error)
	contextMode string
	edgeToken   string
}

// NewHandler собирает хендлер. rel может быть nil: тогда /ready ложен и
// GET / отвечает 503 до загрузки валидного release.
func NewHandler(rel *PreparedRelease, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{log: log, mint: newClickID, contextMode: ContextModePrototype}
	if rel != nil {
		h.current.Store(rel)
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		h.onlyGET(w, r, h.handleHealth)
	case "/ready":
		h.onlyGET(w, r, h.handleReady)
	case "/contract":
		h.onlyGET(w, r, h.handleContract)
	case "/":
		h.onlyGET(w, r, h.handleRoot)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) onlyGET(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	next(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.loaded() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (h *Handler) loaded() bool {
	if h.contract.Load() != nil {
		return true
	}
	return h.current.Load().valid()
}

func (h *Handler) handleContract(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c := h.contract.Load()
	if c == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "contract not loaded")
		return
	}
	httpName := ""
	if rel := h.current.Load(); rel != nil {
		httpName = rel.Name
	}
	st, err := c.Status(httpName)
	if err != nil {
		h.log.Error("contract probe failed", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "contract probe failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(st)
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.contextMode == ContextModeQueryV1 {
		h.handleQueryV1Root(w, r)
		return
	}
	if c := h.contract.Load(); c != nil {
		h.handleResolvedRoot(w, r, c)
		return
	}
	rel := h.current.Load()
	if !rel.valid() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not ready")
		return
	}
	h.serveRedirect(w, r, rel.Target, func(clickID, loc, taSource, taSub string) {
		h.emitRedirect(clickID, loc, taSource, taSub, rel.Name)
	})
}

func (h *Handler) handleQueryV1Root(w http.ResponseWriter, r *http.Request) {
	parsed, err := ParseAllowlistedQuery(r.URL.RawQuery)
	if err != nil {
		status := queryErrorStatus(err)
		msg := queryErrorMessage(err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, msg)
		return
	}
	c := h.contract.Load()
	if c == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "not ready")
		return
	}
	clickID, err := h.mint()
	if err != nil {
		h.log.Error("click_id mint failed", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal error")
		return
	}
	src, srcReject := NormalizeQueryValue(parsed.TASource)
	sub, subReject := NormalizeQueryValue(parsed.TASub)
	campaign, campaignReject := NormalizeIDValue(parsed.CampaignID)
	zone, zoneReject := NormalizeIDValue(parsed.ZoneID)
	cost, costReject := NormalizeQueryValue(parsed.Cost)
	zone, campaign = FillPackedIDs(src, sub, zone, campaign)
	rr, err := c.ResolveRedirect(QueryV1Context(clickID, src, sub))
	if err != nil {
		h.log.Error("resolve redirect failed", "error", err.Error())
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "no executable redirect")
		return
	}
	loc, err := redirect.Build(rr.URL, redirect.Substitutions{
		ClickID:  clickID,
		TASource: src,
		TASub:    sub,
	})
	if err != nil {
		h.log.Error("redirect build failed", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal error")
		return
	}
	h.emitResolved(clickID, loc, clickEventAttrs{
		TASource:       src,
		TASub:          sub,
		CampaignID:     campaign,
		ZoneID:         zone,
		Cost:           cost,
		Country:        h.eventCountry(r),
		SourceReject:   srcReject,
		SubReject:      subReject,
		CampaignReject: campaignReject,
		ZoneReject:     zoneReject,
		CostReject:     costReject,
	}, rr, c, QueryV1ContextSource)
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusFound)
}

func (h *Handler) handleResolvedRoot(w http.ResponseWriter, r *http.Request, c *Contract) {
	rr, err := c.ResolveRedirect(PrototypeContext())
	if err != nil {
		h.log.Error("resolve redirect failed", "error", err.Error())
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "no executable redirect")
		return
	}
	h.serveRedirect(w, r, rr.URL, func(clickID, loc, taSource, taSub string) {
		h.emitResolved(clickID, loc, clickEventAttrs{TASource: taSource, TASub: taSub}, rr, c, PrototypeContextSource)
	})
}

func (h *Handler) serveRedirect(w http.ResponseWriter, r *http.Request, target string, emit func(clickID, loc, taSource, taSub string)) {
	clickID, err := h.mint()
	if err != nil {
		h.log.Error("click_id mint failed", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal error")
		return
	}
	q := r.URL.Query()
	src := q.Get("ta_source")
	sub := q.Get("ta_sub")
	loc, err := redirect.Build(target, redirect.Substitutions{
		ClickID:  clickID,
		TASource: src,
		TASub:    sub,
	})
	if err != nil {
		h.log.Error("redirect build failed", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal error")
		return
	}
	emit(clickID, loc, src, sub)
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusFound)
}

// StoreRelease атомарно подменяет current. Для P1 вызывается только на load.
func (h *Handler) StoreRelease(rel *PreparedRelease) {
	h.current.Store(rel)
}

// StoreContract атомарно подменяет verified traffic-runtime/v1 snapshot.
func (h *Handler) StoreContract(c *Contract) {
	h.contract.Store(c)
}

// CurrentContract возвращает захваченный snapshot. Request path должен
// сохранить этот указатель и не читать handler повторно.
func (h *Handler) CurrentContract() *Contract {
	if h == nil {
		return nil
	}
	return h.contract.Load()
}

// SwapIfNewer ставит contract только если pointerVersion строго больше
// уже загруженной. Stale completion N после N+1 не перетирает head (CAS).
func (h *Handler) SwapIfNewer(c *Contract) bool {
	if h == nil || c == nil {
		return false
	}
	for {
		old := h.contract.Load()
		if old != nil && c.pointerVersion <= old.pointerVersion {
			return false
		}
		if h.contract.CompareAndSwap(old, c) {
			if rel, err := c.HTTPRelease(); err == nil {
				h.current.Store(rel)
			}
			return true
		}
	}
}

func (h *Handler) SetEnqueue(q Enqueuer) {
	h.enqueue = q
}

func (h *Handler) SetContextMode(mode string) {
	if h == nil {
		return
	}
	if mode == "" {
		mode = ContextModePrototype
	}
	h.contextMode = mode
}

// SetEdgeToken stores the shared secret that proves the request came
// from the configured Worker. Empty token fails closed: event country
// stays "". This is not a TRAFFIC_CONTEXT_MODE.
func (h *Handler) SetEdgeToken(token string) {
	if h == nil {
		return
	}
	h.edgeToken = token
}

func (h *Handler) verifiedEdge(r *http.Request) bool {
	if h == nil || h.edgeToken == "" || r == nil {
		return false
	}
	got := []byte(r.Header.Get(edgeTokenHeader))
	want := []byte(h.edgeToken)
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (h *Handler) eventCountry(r *http.Request) string {
	if !h.verifiedEdge(r) {
		return ""
	}
	return EventCountryFromCF(r.Header.Get("CF-IPCountry"))
}
