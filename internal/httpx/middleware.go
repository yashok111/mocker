package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"time"
)

// Middleware wraps a handler. Both planes compose their own chains from these.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first argument is the outermost wrapper
// and therefore runs first.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for _, m := range slices.Backward(mw) {
		h = m(h)
	}
	return h
}

// Recover turns a panic into a 500 instead of a dropped connection.
//
// The mock plane serves whatever a user pasted into a body template, so a panic
// on one workspace's request must not take the process — and every other
// workspace — down with it.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// errorlint cannot tell that rec is an untyped recover() value
				// rather than a wrapped error chain; net/http compares this
				// sentinel with == for the same reason.
				if rec == http.ErrAbortHandler { //nolint:errorlint
					panic(rec) // the server's own signal, not ours to swallow
				}
				log.Error("panic serving request",
					"method", r.Method, "host", r.Host, "path", r.URL.Path,
					"panic", rec, "stack", string(debug.Stack()))
				Err(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLog logs one line per request at debug level, and at warn for 5xx.
func RequestLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := slog.LevelDebug
			if rec.Status >= http.StatusInternalServerError {
				level = slog.LevelWarn
			}
			log.Log(r.Context(), level, "request",
				"method", r.Method, "host", r.Host, "path", r.URL.Path,
				"status", rec.Status, "bytes", rec.Bytes,
				"ms", float64(time.Since(start).Microseconds())/1000)
		})
	}
}

// StatusRecorder captures the status and size of a response.
//
// It deliberately does not implement http.Flusher or http.Hijacker beyond the
// pass-throughs below; anything that needs streaming must assert for the
// interface it wants and get a clear failure rather than silent buffering.
type StatusRecorder struct {
	http.ResponseWriter
	Status  int
	Bytes   int
	written bool
}

// WriteHeader records the status once, matching net/http's own behaviour of
// ignoring repeat calls.
func (s *StatusRecorder) WriteHeader(status int) {
	if s.written {
		return
	}
	s.written = true
	s.Status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *StatusRecorder) Write(b []byte) (int, error) {
	if !s.written {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.Bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, so flushing
// and deadline control keep working through the recorder.
func (s *StatusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// MaxBody caps the request body. It is applied on both planes: an unbounded
// upload into a service backed by a single SQLite writer is a denial of service
// with extra steps.
func MaxBody(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
