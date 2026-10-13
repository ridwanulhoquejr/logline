package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

const maxBodySize = 1_048_576 // 1 MB

type LogEntry struct {
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Service   string         `json:"service"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type statusResponse struct {
	Status string `json:"status"`
}

func main() {

	mux := http.NewServeMux()

	mux.HandleFunc("POST /ingest", handleIngest)
	mux.HandleFunc("GET /health", handleHealth)

	addr := ":4000"

	log.Printf("logline starting on %s ", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK,
		statusResponse{Status: "logline is healthy"})
}

func handleIngest(w http.ResponseWriter, r *http.Request) {

	// if Header Content-Type is json then rejects
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType,
			statusResponse{Status: "Content-Type should be application/json"})
		return
	}

	le := LogEntry{}
	err := parseJSON(w, r, &le)

	if err != nil {
		var maxByteErr *http.MaxBytesError
		if errors.As(err, &maxByteErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				errorResponse{Error: "Payload is too large"})
			return
		}

		writeJSON(w, http.StatusBadRequest,
			errorResponse{Error: "Invalid JSON" + err.Error()})
		return
	}

	// if not-empty then failed the validation and rejects
	if validattion := validateLogEntry(le); validattion != "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: validattion,
		})
		return
	}

	fmt.Printf("received: level=%s service=%s message=%s\n",
		le.Level, le.Service, le.Message)

	writeJSON(w, http.StatusCreated, statusResponse{Status: "accepted"})
}

func parseJSON(w http.ResponseWriter, r *http.Request, dst any) error {

	// max payload size is 1 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	// decoder for parsing the json payload
	decoder := json.NewDecoder(r.Body)
	// for rejecting unkownfields
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

func validateLogEntry(entry LogEntry) string {
	if entry.Level == "" {
		return "level is required"
	}
	if entry.Message == "" {
		return "message is required"
	}
	if entry.Service == "" {
		return "service is required"
	}
	if entry.Timestamp.IsZero() {
		return "timestamp is required"
	}

	switch entry.Level {
	case "debug", "info", "warn", "error", "fatal":
		// valid
	default:
		return "level must be one of: debug, info, warn, error, fatal"
	}

	return ""
}
