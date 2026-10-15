package server

import (
	"logline/internal/config"
	"net/http"
)

// compile-time check if our server struct satisfied http.Handler interface!
var _ http.Handler = (*Server)(nil)

type Server struct {
	mux *http.ServeMux
	cfg config.Config
}

func New(cfg config.Config) *Server {

	s := &Server{
		mux: http.NewServeMux(),
		cfg: cfg,
	}
	// here we have to register our routes
	s.registerRoutes()

	return s
}

// NOTE: After satisfaying http.Handler interface,
// our Server struct will acts as http.Handler
// so wherever we need http.Handler we could pass our Server struct
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
