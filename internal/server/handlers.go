package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"logline/internal/domain"
	"net/http"
)

const maxBodySize = 1_048_576 // 1 MB

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK,
		domain.StatusResponse{Status: "logline is healthy"})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {

	// if Header Content-Type is json then rejects
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType,
			domain.StatusResponse{Status: "Content-Type should be application/json"})
		return
	}

	var le domain.LogEntry

	err := parseJSON(w, r, &le)

	if err != nil {
		var maxByteErr *http.MaxBytesError
		if errors.As(err, &maxByteErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				domain.ErrorResponse{Error: "Payload is too large"})
			return
		}

		writeJSON(w, http.StatusBadRequest,
			domain.ErrorResponse{Error: "Invalid JSON" + err.Error()})
		return
	}

	// if not-empty then failed the validation and rejects
	if validattion := domain.ValidateLogEntry(le); validattion != "" {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: validattion,
		})
		return
	}

	fmt.Printf("received: level=%s service=%s message=%s\n",
		le.Level, le.Service, le.Message)

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
