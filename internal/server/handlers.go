package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"logline/internal/domain"
	"net/http"
	"time"
)

const maxBodySize = 1_048_576 // 1 MB

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

	// todo: move trace ID generation to a middleware and pass it via context
	traceID := generateTraceID()
	logger := s.logger.With(slog.String("trace_id", traceID))

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
