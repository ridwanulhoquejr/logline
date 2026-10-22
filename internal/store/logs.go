package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LogRow is the database representation of a log entry.
// separate from domain.LogEntry because the row has fields
// the API payload does not (id, created_at) and may diverge
// further as the schema evolves.
type LogRow struct {
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Service   string         `json:"service"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
	Timestamp time.Time      `json:"timestamp"`
}

type LogFilter struct {
	Level      string
	Service    string
	Query      string
	DataFilter map[string]any
	Cursor     int64
	Limit      int
	From       time.Time
	To         time.Time
}

func (s *Store) QueryLogs(ctx context.Context, filter LogFilter) ([]LogRow, error) {

	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("SELECT id, level, message, service, timestamp, data, created_at FROM logs WHERE 1=1")
	args := []any{}
	argPos := 1

	if filter.Level != "" {
		fmt.Fprintf(&sqlBuilder, " AND level = $%d", argPos)
		args = append(args, filter.Level)
		argPos++
	}

	if filter.Service != "" {
		fmt.Fprintf(&sqlBuilder, " AND service = $%d", argPos)
		args = append(args, filter.Service)
		argPos++
	}

	if !filter.From.IsZero() {
		fmt.Fprintf(&sqlBuilder, " AND timestamp >= $%d", argPos)
		args = append(args, filter.From)
		argPos++
	}

	if !filter.To.IsZero() {
		fmt.Fprintf(&sqlBuilder, " AND timestamp <= $%d", argPos)
		args = append(args, filter.To)
		argPos++
	}

	if filter.Query != "" {
		fmt.Fprintf(&sqlBuilder, " AND to_tsvector('english', message) @@ websearch_to_tsquery('english', $%d)", argPos)
		args = append(args, filter.Query)
		argPos++
	}

	if filter.DataFilter != nil {
		dataJSON, err := json.Marshal(filter.DataFilter)
		if err != nil {
			return nil, fmt.Errorf("marshal data filter: %w", err)
		}
		fmt.Fprintf(&sqlBuilder, " AND data @> $%d::jsonb", argPos)
		args = append(args, string(dataJSON))
		argPos++
	}

	if filter.Cursor > 0 {
		fmt.Fprintf(&sqlBuilder, " AND id < $%d", argPos)
		args = append(args, filter.Cursor)
		argPos++
	}

	sqlBuilder.WriteString(" ORDER BY id DESC")
	fmt.Fprintf(&sqlBuilder, " LIMIT $%d", argPos)
	args = append(args, filter.Limit)

	rows, err := s.db.QueryContext(ctx, sqlBuilder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var results []LogRow
	for rows.Next() {
		var row LogRow
		var data []byte

		if err := rows.Scan(&row.ID, &row.Level, &row.Message, &row.Service, &row.Timestamp, &data, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan log row: %w", err)
		}

		if data != nil {
			if err := json.Unmarshal(data, &row.Data); err != nil {
				return nil, fmt.Errorf("unmarshal data column: %w", err)
			}
		}

		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate log rows: %w", err)
	}

	return results, nil
}
