package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestID(t *testing.T) {
	var ctxID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = GetRequestID(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headerID := rec.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Fatal("expected X-Request-ID header to be set")
	}
	if len(headerID) != 32 {
		t.Errorf("expected 32-char hex id, got %q (len %d)", headerID, len(headerID))
	}
	if ctxID != headerID {
		t.Errorf("context id %q does not match header id %q", ctxID, headerID)
	}
}

func TestRequestID_Unique(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec1.Header().Get("X-Request-ID") == rec2.Header().Get("X-Request-ID") {
		t.Error("expected different request ids for separate requests")
	}
}

func TestGetRequestID_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if id := GetRequestID(req.Context()); id != "" {
		t.Errorf("expected empty id, got %q", id)
	}
}

func TestRecovery(t *testing.T) {
	handler := Recovery(testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// if Recovery fails, the panic escapes and the test crashes
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestRecovery_NoPanic(t *testing.T) {
	handler := Recovery(testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("expected status 418, got %d", rec.Code)
	}
}

func TestChain_Order(t *testing.T) {
	var calls []string

	record := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name+"-before")
				next.ServeHTTP(w, r)
				calls = append(calls, name+"-after")
			})
		}
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
	})

	handler := Chain(final, record("outer"), record("inner"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"outer-before", "inner-before", "handler", "inner-after", "outer-after"}
	if !slices.Equal(calls, want) {
		t.Errorf("call order mismatch\n got: %v\nwant: %v", calls, want)
	}
}
