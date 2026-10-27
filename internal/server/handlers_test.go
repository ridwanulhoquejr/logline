package server

import (
	"context"
	"database/sql"
	"log/slog"
	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testConfig() config.Config {
	return config.Config{
		Port:        4000,
		Env:         "development",
		LogLevel:    "error",
		DatabaseURL: "",
		DBMaxConns:  5,
		DBMaxIdle:   1,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setupDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("database url is empty, skipping db test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening db connection, error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connecting database %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db

}

func newTestServer(t *testing.T) *Server {
	/// t.Helper() helps to find which line got the error in the call side
	// as this is a common func several other usecaes wouldcall this helper
	// Go skips helper frames and reports the line in the test that called the helper:
	t.Helper()

	cfg := testConfig()
	logger := testLogger()
	db := setupDB(t)

	logStore := store.New(db)
	apiKeyStore := auth.NewApiKeyStore(db)
	bufferedStore := setupBufferedStore(t, logStore)
	us := auth.NewUserStore(db)
	ss := auth.NewSessionStore(db)

	return New(cfg, logger, logStore, bufferedStore, apiKeyStore, us, ss)
}

func setupBufferedStore(t *testing.T, s *store.Store) *store.BufferedStore {
	t.Helper()

	bs := store.NewBufferedStore(s, 10, 100*time.Second)
	t.Cleanup(bs.Close)

	return bs
}

func TestHandleHealth_OK(t *testing.T) {

	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandleHealth_DBDown(t *testing.T) {

	db := setupDB(t)
	logStore := store.New(db)
	apiKeyStore := auth.NewApiKeyStore(db)
	bs := setupBufferedStore(t, logStore)
	us := auth.NewUserStore(db)
	ss := auth.NewSessionStore(db)
	srv := New(testConfig(), testLogger(), logStore, bs, apiKeyStore, us, ss)

	db.Close()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestIngestValidEntry(t *testing.T) {

	srv := newTestServer(t)

	payload := `{"level":"error","message":"connection refused","service":"auth","timestamp":"2026-02-27T14:30:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestIngestWrongContentType(t *testing.T) {

	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", rec.Code)
	}
}

func TestIngestEmptyBody(t *testing.T) {

	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestInvalidJSON(t *testing.T) {

	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("definitely not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestWrongMethodReturns405(t *testing.T) {

	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}
