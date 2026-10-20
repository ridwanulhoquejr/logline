package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"logline/internal/domain"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{
		db: db,
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) InsertIngest(ctx context.Context, entry domain.LogEntry) error {

	data, err := json.Marshal(entry.Data)
	if err != nil {
		return fmt.Errorf("data field parsing, error: %w", err)
	}

	query :=
		`INSERT INTO logs (level, message, service, timestamp, data) VALUES ($1, $2, $3, $4, $5)`

	args := []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}

	_, err = s.db.QueryContext(ctx, query, args...)

	return err
}
