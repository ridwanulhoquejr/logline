package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"logline/internal/domain"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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

func (s *Store) CopyFrom(ctx context.Context, entries []domain.LogEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// reserve a connection ffrom sql.DB conenctions pool
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring connection: %w", err)
	}
	// unreserve the conn as soon as this func is retured
	defer conn.Close()

	var count int64
	err = conn.Raw(func(driverConn any) error {

		stdConn, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("expected *stdlib.Conn, got %T", driverConn)
		}

		// .Conn() unwraps that to the native *pgx.Conn, which has the CopyFrom method.
		pgxConn := stdConn.Conn()
		rows := make([][]any, len(entries))
		for i, entry := range entries {
			data, err := json.Marshal(entry.Data)
			if err != nil {
				return fmt.Errorf("marshaling entry %d: %w", i, err)
			}
			rows[i] = []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}
		}

		count, err = pgxConn.CopyFrom(
			ctx,
			pgx.Identifier{"logs"},
			[]string{"level", "message", "service", "timestamp", "data"},
			pgx.CopyFromRows(rows),
		)
		return err
	})

	return count, err
}

func (s *Store) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")

	args := make([]any, 0, len(entries)*5)

	for i, entry := range entries {
		if i > 0 {
			sqlBuilder.WriteString(", ")
		}

		data, err := json.Marshal(entry.Data)
		if err != nil {
			return fmt.Errorf("marshaling entry %d data: %w", i, err)
		}

		// $1-$5 for first row, $6-$10 for second, etc.
		offset := i * 5
		fmt.Fprintf(&sqlBuilder, "($%d,$%d,$%d,$%d,$%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5)

		args = append(args, entry.Level, entry.Message, entry.Service, entry.Timestamp, data)
	}

	_, err := s.db.ExecContext(ctx, sqlBuilder.String(), args...)
	return err
}

func (s *Store) InsertLog(ctx context.Context, entry domain.LogEntry) error {

	data, err := json.Marshal(entry.Data)
	if err != nil {
		return fmt.Errorf("data field parsing, error: %w", err)
	}

	query :=
		`INSERT INTO logs (level, message, service, timestamp, data) VALUES ($1, $2, $3, $4, $5)`

	args := ([]any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data})

	_, err = s.db.QueryContext(ctx, query, args...)

	return err
}
