package server

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /ingest", s.handleIngest)
	s.mux.HandleFunc("GET /logs", s.handleListLogs)
}
