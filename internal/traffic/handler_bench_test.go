package traffic

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkHTTPRootPrototype(b *testing.B) {
	c, err := NewContract(twoExecutableBundle(b), ActivationRevisionPinned)
	if err != nil {
		b.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.StoreContract(c)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=proto-smoke", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			b.Fatalf("status=%d", rec.Code)
		}
	}
}

func BenchmarkHTTPRootQueryV1(b *testing.B) {
	c, err := NewContract(twoExecutableBundle(b), ActivationRevisionPinned)
	if err != nil {
		b.Fatal(err)
	}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetContextMode(ContextModeQueryV1)
	h.StoreContract(c)
	req := httptest.NewRequest(http.MethodGet, "/?ta_source=network-a&ta_sub=campaign-42", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			b.Fatalf("status=%d", rec.Code)
		}
	}
}
