package server

import (
	"log/slog"
	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/middleware"
	"logline/internal/store"
	"net/http"
	"os"
)

// compile-time check if our server struct satisfied http.Handler interface!
var _ http.Handler = (*Server)(nil)

type Server struct {
	cfg           config.Config
	logger        *slog.Logger
	mux           *http.ServeMux
	store         *store.Store
	apiKeyStore   *auth.ApiKeyStore
	bufferedStore *store.BufferedStore
	handler       http.Handler
}

func New(cfg config.Config, logger *slog.Logger, logStore *store.Store, bs *store.BufferedStore, aks *auth.ApiKeyStore) *Server {

	srv := &Server{
		cfg:           cfg,
		logger:        logger,
		mux:           http.NewServeMux(),
		store:         logStore,
		bufferedStore: bs,
		apiKeyStore:   aks,
	}

	// here we have to register our routes
	srv.registerRoutes()

	// register all of our middlewares
	srv.handler = middleware.Chain(
		srv.mux,
		middleware.Recovery(srv.logger),
		middleware.RequestID,
		middleware.Logging(srv.logger),
	)

	return srv
}

// NOTE: After satisfaying http.Handler interface,
// our Server struct will acts as http.Handler
// so wherever we need http.Handler we could pass our Server struct
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func NewLogger(env, logLevel string) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: parseLogLevel(logLevel),
	}

	switch env {
	case "production":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
