package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"logline/internal/config"
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
	bufferedStore *store.BufferedStore
}

func New(cfg config.Config, logger *slog.Logger, logStore *store.Store, bs *store.BufferedStore) *Server {

	s := &Server{
		cfg:           cfg,
		logger:        logger,
		mux:           http.NewServeMux(),
		store:         logStore,
		bufferedStore: bs,
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

func generateTraceID() string {

	randomBytes := make([]byte, 16)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "unknown"
	}

	return hex.EncodeToString(randomBytes)
}
