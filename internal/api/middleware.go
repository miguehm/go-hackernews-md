package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

// RequestIDHeader carries the request correlation id in both directions.
const RequestIDHeader = "X-Request-ID"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type requestIDKey struct{}

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestID propagates a well-formed incoming X-Request-ID or generates a new
// one, stores it in the request context and echoes it in the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !requestIDPattern.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// statusRecorder captures the status code and body size for access logs.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(data []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(data)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog writes one structured log line per request. Health probes are
// logged at debug level so they do not flood the logs.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case status == http.StatusServiceUnavailable:
				// Expected while warming up; not a server fault.
				level = slog.LevelWarn
			case status >= 500:
				level = slog.LevelError
			case r.URL.Path == "/health":
				level = slog.LevelDebug
			}
			// r.Pattern is filled in by ServeMux on this same request.
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			logger.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("route", route),
				slog.Int("status", status),
				slog.Int("bytes", rec.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
		})
	}
}

// Recover turns a handler panic into a 500 JSON error and logs the stack.
// It must wrap GzipMiddleware: the buffered response guarantees nothing has
// reached the client yet when a panic unwinds. http.ErrAbortHandler is
// re-raised so net/http can abort the connection as intended.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				logger.LogAttrs(r.Context(), slog.LevelError, "panic",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", v),
					slog.String("stack", string(debug.Stack())),
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", "", nil)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
