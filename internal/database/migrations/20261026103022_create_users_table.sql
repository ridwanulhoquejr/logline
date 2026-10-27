-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT UNIQUE NOT NULL CHECK (length(email) <= 254),
    password_hash TEXT NOT NULL CHECK (length(password_hash) = 60),
    name TEXT NOT NULL CHECK (length(name) <= 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS users;
