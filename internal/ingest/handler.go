package ingest

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Publisher публикует канонические события (реализуется пакетом internal/pubsub).
// Объявлен на стороне потребителя, чтобы хендлер можно было тестировать с моком.
type Publisher interface {
	Publish(ev CanonicalEvent) error
}

// Metrics — базовые счётчики приёма (раздел 13 спеки).
type Metrics struct {
	BatchesAccepted  atomic.Int64
	EventsAccepted   atomic.Int64
	EventsPublished  atomic.Int64
	PublishErrors    atomic.Int64
	ValidationErrors atomic.Int64
	BadRequests      atomic.Int64
}

// Options конфигурирует хендлер.
type Options struct {
	Secret       string
	Salt         string
	MaxBatchSize int
	MaxBodyBytes int64
	Publisher    Publisher
	Logger       *slog.Logger
	Metrics      *Metrics
	// Now позволяет детерминированно подменить время в тестах; nil → time.Now.
	Now func() time.Time
}

const defaultMaxBodyBytes = 1 << 20 // 1 MiB

// Handler — HTTP-хендлер сервиса приёма событий.
type Handler struct {
	opts Options
}

// NewHandler собирает http.Handler с маршрутами /v1/events и /healthz.
func NewHandler(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Metrics == nil {
		opts.Metrics = &Metrics{}
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	h := &Handler{opts: opts}

	mux := http.NewServeMux()
	// /healthz — по спеке; работает локально и в k8s. На Cloud Run пути,
	// оканчивающиеся на "z" (/healthz, /livez, ...), зарезервированы Google Front
	// End и не доходят до контейнера, поэтому дублируем рабочим алиасом /health.
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /health", h.handleHealthz)
	mux.Handle("POST /v1/events", h.authMiddleware(http.HandlerFunc(h.handleEvents)))
	return mux
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

// authMiddleware проверяет заголовок Authorization: Bearer <EVENTS_INGEST_SECRET>.
// Сравнение в постоянном времени, чтобы не утекал секрет по таймингу.
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authz, prefix)), []byte(h.opts.Secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	m := h.opts.Metrics

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxBodyBytes))
	if err != nil {
		m.BadRequests.Add(1)
		h.opts.Logger.Warn("read body failed", "error", err.Error())
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	raws, err := splitEvents(body)
	if err != nil {
		m.BadRequests.Add(1)
		h.opts.Logger.Warn("malformed payload", "error", err.Error())
		writeError(w, http.StatusBadRequest, "malformed JSON payload")
		return
	}
	if len(raws) == 0 {
		m.BadRequests.Add(1)
		writeError(w, http.StatusBadRequest, "empty payload")
		return
	}
	if len(raws) > h.opts.MaxBatchSize {
		m.BadRequests.Add(1)
		h.opts.Logger.Warn("batch too large", "size", len(raws), "max", h.opts.MaxBatchSize)
		writeError(w, http.StatusBadRequest, "batch exceeds MAX_BATCH_SIZE")
		return
	}

	// Сначала валидируем и маппим весь батч; при любой ошибке — 400 без публикации.
	now := h.opts.Now()
	canonical := make([]CanonicalEvent, 0, len(raws))
	for i, raw := range raws {
		var ev IncomingEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			m.BadRequests.Add(1)
			h.opts.Logger.Warn("event is not a JSON object", "index", i, "error", err.Error())
			writeError(w, http.StatusBadRequest, "event is not a JSON object")
			return
		}
		if err := Validate(ev); err != nil {
			m.ValidationErrors.Add(1)
			h.opts.Logger.Warn("event validation failed", "index", i, "error", err.Error())
			writeError(w, http.StatusBadRequest, publicValidationMessage(err))
			return
		}
		ce, err := Canonicalize(raw, ev, h.opts.Salt, now)
		if err != nil {
			// Сбой обогащения (например, генерация UUID) — внутренняя ошибка.
			h.opts.Logger.Error("canonicalize failed", "index", i, "error", err.Error())
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		canonical = append(canonical, ce)
	}

	for _, ce := range canonical {
		if err := h.opts.Publisher.Publish(ce); err != nil {
			// Сбой только синхронной части (сериализация); доставка асинхронна.
			m.PublishErrors.Add(1)
			h.opts.Logger.Error("publish failed", "event_name", ce.EventName, "error", err.Error())
			continue
		}
		m.EventsPublished.Add(1)
	}

	m.BatchesAccepted.Add(1)
	m.EventsAccepted.Add(int64(len(canonical)))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]int{"accepted": len(canonical)})
}

// splitEvents разбирает тело как одно событие (объект) или батч (массив),
// возвращая список сырых JSON-элементов для последующей валидации/маппинга.
func splitEvents(body []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	// Одиночное событие: убеждаемся, что это валидный JSON, не докручивая структуру.
	if !json.Valid(body) {
		return nil, errors.New("invalid JSON")
	}
	return []json.RawMessage{json.RawMessage(body)}, nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
