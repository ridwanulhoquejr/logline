package server

import (
	"context"
	"log/slog"
	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/domain"
	"logline/internal/middleware"
	"logline/internal/store"
	"net/http"
	"os"
	"time"
)

// compile-time check if our server struct satisfied http.Handler interface!
var _ http.Handler = (*Server)(nil)

type contextKey string

const userContextKey contextKey = "user"

type Server struct {
	cfg           config.Config
	logger        *slog.Logger
	mux           *http.ServeMux
	store         *store.Store
	apiKeyStore   *auth.ApiKeyStore
	userStore     *auth.UserStore
	sessionStore  *auth.SessionStore
	bufferedStore *store.BufferedStore
	handler       http.Handler
}

func New(cfg config.Config,
	logger *slog.Logger,
	logStore *store.Store,
	bs *store.BufferedStore,
	aks *auth.ApiKeyStore,
	userStore *auth.UserStore,
	sessionStore *auth.SessionStore,
) *Server {

	srv := &Server{
		cfg:           cfg,
		logger:        logger,
		mux:           http.NewServeMux(),
		store:         logStore,
		bufferedStore: bs,
		apiKeyStore:   aks,
		userStore:     userStore,
		sessionStore:  sessionStore,
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

	// starting a new go routine for cleanup
	go srv.startSessionCleanUp(context.Background())

	return srv
}

// NOTE: After satisfaying http.Handler interface,
// our Server struct will acts as http.Handler
// so wherever we need http.Handler we could pass our Server struct
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) startSessionCleanUp(ctx context.Context) {

	ticker := time.NewTicker(60 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			counts, err := s.sessionStore.DeleteExpired(ctx)
			if err != nil {
				s.logger.Error("session clean up failed",
					slog.String("error", err.Error()),
				)
				continue
			}
			if counts > 0 {
				s.logger.Info("expired session cleaned up",
					slog.Int("counts", int(counts)))
			}

		case <-ctx.Done():
			return
		}
	}

}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_token")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, domain.ErrorResponse{
				Error: "authentication required",
			})
			return
		}

		_, user, err := s.sessionStore.Validate(r.Context(), cookie.Value)
		if err != nil {
			// overwrite the current cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "session_token",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   s.cfg.Env != "development",
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})

			writeJSON(w, http.StatusUnauthorized, domain.ErrorResponse{
				Error: "authentication required",
			})
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromContext(ctx context.Context) *auth.User {
	if user, ok := ctx.Value(userContextKey).(*auth.User); ok {
		return user
	}
	return nil
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
