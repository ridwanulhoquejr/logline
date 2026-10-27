package server

import (
	"logline/internal/auth"
	"net/http"
)

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /api/keys", s.handleCreateAPIKey)
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("GET /register", s.handleRegister)
	s.mux.HandleFunc("GET /logout", s.handleLogout)

	authMW := auth.Middleware(s.apiKeyStore)
	s.mux.Handle("POST /ingest", authMW(http.HandlerFunc(s.handleIngest)))

	// private routes
	s.mux.Handle("GET /logs", s.requireSession(http.HandlerFunc((s.handleListLogs))))
}
