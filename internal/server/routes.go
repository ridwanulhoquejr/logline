package server

import (
	"logline/internal/auth"
	"net/http"
)

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /logs", s.handleListLogs)
	s.mux.HandleFunc("POST /api/keys", s.handleCreateAPIKey)

	authMW := auth.Middleware(s.apiKeyStore)
	s.mux.Handle("POST /ingest", authMW(http.HandlerFunc(s.handleIngest)))
}
