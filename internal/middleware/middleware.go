package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

type wrappedWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *wrappedWriter) WriteHeader(statusCode int) {

	if !w.written {
		w.statusCode = statusCode
		w.written = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *wrappedWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(data)
}

// chain to wrap all the middleware at once
// initial handler is our mux; it will be wrapped by of all our middleware like Onion

func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// recover middleware
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					buf := make([]byte, 4096)
					stackLength := runtime.Stack(buf, false)

					logger.Error("recovered panic",
						slog.Any("error", err),
						slog.String("path", r.URL.Path),
						slog.String("stack", string(buf[:stackLength])),
						slog.String("method", r.Method),
						slog.String("request_id", GetRequestID(r.Context())),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// logging middleware
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			start := time.Now()

			wrapped := wrappedWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(&wrapped, r)

			// when the call stack resume from the above line
			// we have the actual status_code here, which writen by the handler
			logger.Info("request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status_code", wrapped.statusCode),
				slog.String("duration", time.Since(start).String()),
				slog.String("request_id", GetRequestID(r.Context())),
			)

		})
	}
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		id := generateTraceID()
		ctx := context.WithValue(r.Context(), requestIDKey, id)

		w.Header().Set("X-Request-ID", id)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

func generateTraceID() string {

	randomBytes := make([]byte, 16)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "unknown"
	}

	return hex.EncodeToString(randomBytes)
}
