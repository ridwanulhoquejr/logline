-- +goose Up
CREATE TABLE IF NOT EXISTS api_keys (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) <= 128),
    key_prefix TEXT NOT NULL CHECK (length(key_prefix) = 8),
    key_hash TEXT NOT NULL CHECK (length(key_hash) = 64),
    service TEXT NOT NULL CHECK (length(service) <= 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys (key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_service ON api_keys (service);

-- +goose Down
DROP TABLE IF EXISTS api_keys;

DROP INDEX IF EXISTS idx_api_keys_key_hash;
DROP INDEX IF EXISTS idx_api_keys_service;