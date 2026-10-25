package auth_test

import (
	"context"
	"database/sql"
	"logline/internal/auth"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func setupTestDB(t *testing.T) *sql.DB {
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

func TestCreateAndValidate(t *testing.T) {
	db := setupTestDB(t)
	apiKeyStore := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, err := apiKeyStore.Create(ctx, "test-key", "payments")
	if err != nil {
		t.Fatal(err)
	}

	key, err := apiKeyStore.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if key.Name != "test-key" {
		t.Errorf("expected name test-key, got %q", key.Name)
	}
	if key.Service != "payments" {
		t.Errorf("expected service payments, got %q", key.Service)
	}
	if key.RevokedAt != nil {
		t.Error("new key should not be revoked")
	}
}

func TestMiddleware_RevokedKey(t *testing.T) {
	db := setupTestDB(t)
	apiKeyStore := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, _ := apiKeyStore.Create(ctx, "revoked-mw", "api")
	key, _ := apiKeyStore.Validate(ctx, rawKey)
	apiKeyStore.Revoke(ctx, key.ID)

	handler := auth.Middleware(apiKeyStore)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for revoked key, got %d", rec.Code)
	}
}

func TestRevoke_AlreadyRevoked(t *testing.T) {
	db := setupTestDB(t)
	apiKeyStore := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, _ := apiKeyStore.Create(ctx, "double-revoke", "api")
	key, _ := apiKeyStore.Validate(ctx, rawKey)

	apiKeyStore.Revoke(ctx, key.ID)

	err := apiKeyStore.Revoke(ctx, key.ID)
	if err == nil {
		t.Error("expected error revoking already-revoked key")
	}
}
