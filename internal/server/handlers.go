package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"logline/internal/auth"
	"logline/internal/domain"
	"logline/internal/middleware"
	"logline/internal/store"
	"net/http"
	"strconv"
	"time"
)

const maxBodySize = 1_048_576 // 1 MB
type LogRowResponse struct {
	Logs       []store.LogRow `json:"logs"`
	NextCursor *int64         `json:"next_cursor,omitempty"`
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {

	var req struct {
		Name    string `json:"name"`
		Service string `json:"service"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "invalid JSON",
		})
		return
	}

	if req.Name == "" || req.Service == "" {
		writeJSON(w, http.StatusUnprocessableEntity, domain.ErrorResponse{
			Error: "name and service is required",
		})
		return
	}

	apiKey, err := s.apiKeyStore.Create(r.Context(), req.Name, req.Service)
	if err != nil {
		s.logger.Error("failed to create api key", slog.String("error", err.Error()))

		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "failed to create api key",
		})
		return
	}

	s.logger.Info("created api key",
		slog.String("name", req.Name),
		slog.String("service", req.Service),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"api_key": apiKey,
		"message": "Store this api key securely. It will not be shown again",
	})

}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {

	filter, err := parseLogFilter(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	logs, err := s.store.QueryLogs(r.Context(), filter)
	if err != nil {
		s.logger.Error("failed to query logs", slog.String("error", err.Error()))
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "internal error",
		})
		return
	}

	// if db returns an empty page as, convert it to [], not null
	if logs == nil {
		logs = []store.LogRow{}
	}

	// envelope response body
	resp := LogRowResponse{Logs: logs}

	// if logs length is equal to our limit
	// then we can say, there would be more rows
	// so, point the last row.ID as CursorID
	if len(logs) == filter.Limit {
		lastID := logs[len(logs)-1].ID
		resp.NextCursor = &lastID
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check failed: database unreachable",
			slog.String("error", err.Error()),
		)

		writeJSON(w, http.StatusServiceUnavailable,
			domain.ErrorResponse{Error: "database unreachable"},
		)
		return
	}

	writeJSON(w, http.StatusOK,
		domain.StatusResponse{Status: "ok"},
	)
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()
	reqID := middleware.GetRequestID(ctx)

	logger := s.logger.With(slog.String("request_id", reqID))

	if r.Header.Get("Content-Type") != "application/json" {
		logger.Warn("unsupported content type",
			slog.String("content_type", r.Header.Get("Content-Type")),
		)
		writeJSON(w, http.StatusUnsupportedMediaType, domain.ErrorResponse{
			Error: "content-type must be application/json",
		})
		return
	}

	var entry domain.LogEntry
	err := parseJSON(w, r, &entry)

	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			logger.Warn("request body too large",
				slog.String("error", err.Error()),
			)
			writeJSON(w, http.StatusRequestEntityTooLarge, domain.ErrorResponse{
				Error: "request body too large",
			})
			return
		}

		logger.Warn("invalid JSON in request",
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "invalid JSON: " + err.Error(),
		})
		return
	}

	if msg := domain.ValidateLogEntry(entry); msg != "" {
		logger.Warn("validation failed",
			slog.String("reason", msg),
		)
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: msg,
		})
		return
	}

	apiKey := auth.KeyFromContext(ctx)
	if apiKey != nil && apiKey.Service != entry.Service {

		logger.Warn("api key scope mismatched",
			slog.String("key_service", apiKey.Service),
			slog.String("entry_service", entry.Service),
		)

		writeJSON(w, http.StatusForbidden, domain.ErrorResponse{
			Error: fmt.Sprintf("api key is scoped to service %q, not %q", apiKey.Service, entry.Service),
		})
		return
	}

	// if we want to use the batch-insert;
	// use `s.bufferedStore.Add(ctx, entry)`, instead of `s.store.InsertLog`
	if err := s.store.InsertLog(ctx, entry); err != nil {
		logger.Error("failed to store log entry",
			slog.String("error", err.Error()),
			slog.String("service", entry.Service),
		)
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "failed to store log entry",
		})
		return
	}

	logger.Info("log entry stored",
		slog.String("service", entry.Service),
		slog.String("entry_level", entry.Level),
	)

	writeJSON(w, http.StatusCreated, domain.StatusResponse{Status: "accepted"})
}

func parseLogFilter(r *http.Request) (store.LogFilter, error) {
	query := r.URL.Query()

	filter := store.LogFilter{
		Level:   query.Get("level"),
		Service: query.Get("service"),
		Query:   query.Get("q"),
		Limit:   50, // treats as default value
	}

	// validate `from` query param
	if value := query.Get("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return store.LogFilter{}, fmt.Errorf("invalid 'from' timestamp: %w", err)
		}
		filter.From = parsed
	}

	// `to`
	if value := query.Get("to"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return store.LogFilter{}, fmt.Errorf("invalid 'to' timestamp: %w", err)
		}
		filter.To = parsed
	}

	if value := query.Get("cursor"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return store.LogFilter{}, fmt.Errorf("invalid 'cursor' value: %w", err)
		}
		filter.Cursor = parsed
	}

	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return store.LogFilter{}, fmt.Errorf("invalid 'limit' value: %w", err)
		}
		filter.Limit = parsed
	}

	if value := query.Get("data"); value != "" {

		var dataFilter map[string]any
		if err := json.Unmarshal([]byte(value), &dataFilter); err != nil {
			return store.LogFilter{}, fmt.Errorf("invalid 'data' value: %w", err)
		}
		filter.DataFilter = dataFilter
	}

	// if limit is exceeding; assign the default of 50
	if filter.Limit < 1 || filter.Limit > 200 {
		filter.Limit = 50
	}

	return filter, nil
}

func parseJSON(w http.ResponseWriter, r *http.Request, dst any) error {

	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	err := decoder.Decode(dst)
	if err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(v)

	return
}
