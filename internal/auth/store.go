package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"time"
)

type ApiKey struct {
	ID        int64
	Name      string
	KeyPrefix string
	Service   string
	CreatedAt time.Time
	RevokedAt *time.Time
}

type ApiKeyStore struct {
	db *sql.DB
}

func NewApiKeyStore(db *sql.DB) *ApiKeyStore {
	return &ApiKeyStore{
		db: db,
	}
}

func (s *ApiKeyStore) Create(ctx context.Context, name, service string) (string, error) {
	rawKey, err := GenerateKey()
	if err != nil {
		return "", fmt.Errorf("generating api keys: %w", err)
	}

	keyHash := HashKey(rawKey)
	prefix := ExtractPrefix(rawKey)

	query :=
		`INSERT INTO api_keys (name, key_prefix, key_hash, service)
		VALUES ($1, $2, $3, $4)`
	args := []any{name, prefix, keyHash, service}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("inserting api key: %w", err)
	}

	return rawKey, nil
}

func (s *ApiKeyStore) Validate(ctx context.Context, rawKey string) (*ApiKey, error) {

	keyHash := HashKey(rawKey)

	query :=
		`SELECT id, name, service, key_prefix, key_hash, created_at, revoked_at
		FROM api_keys
		WHERE key_hash = $1`

	var apiKey ApiKey
	var storedHash string

	err := s.db.QueryRowContext(ctx, query, keyHash).Scan(
		&apiKey.ID,
		&apiKey.Name,
		&apiKey.Service,
		&apiKey.KeyPrefix,
		&storedHash,
		&apiKey.CreatedAt,
		&apiKey.RevokedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid api key")
	}

	if err != nil {
		return nil, fmt.Errorf("querying api key: %w", err)
	}

	if subtle.ConstantTimeCompare([]byte(keyHash), []byte(storedHash)) != 1 {
		return nil, fmt.Errorf("invalid api key")
	}

	return &apiKey, nil
}

func (s *ApiKeyStore) Revoke(ctx context.Context, keyID int64) error {

	query :=
		`UPDATE api_keys SET revoked_at = NOW()
		WHERE id = $1 and revoked_at IS NULL`

	result, err := s.db.ExecContext(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("revoking api key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("api key not found or already revoked")
	}

	return nil
}
