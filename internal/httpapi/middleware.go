package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// securityHeaders sets headers that must be present on every response. The
// CSP forbids all subresources and scripts, so even if an SVG were somehow
// coaxed into carrying markup it could not execute when opened directly.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		next.ServeHTTP(w, r)
	})
}

type logFieldsKey struct{}

// logFields collects attributes that handlers want on the access log line.
// It is only touched from the request's own goroutine.
type logFields struct {
	attrs []slog.Attr
}

func addLogAttrs(r *http.Request, attrs ...slog.Attr) {
	if f, ok := r.Context().Value(logFieldsKey{}).(*logFields); ok {
		f.attrs = append(f.attrs, attrs...)
	}
}

// accessLog writes one structured line per request. It deliberately logs the
// path but never the query string, which carries the user's URL.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		fields := &logFields{}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), logFieldsKey{}, fields)))

		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			remote = r.RemoteAddr
		}
		attrs := make([]slog.Attr, 0, 6+len(fields.attrs))
		attrs = append(attrs,
			slog.String("method", r.Method),
			slog.String("path", truncate(r.URL.Path, 128)),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("remote_ip", remote),
		)
		attrs = append(attrs, fields.attrs...)
		level := slog.LevelInfo
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		logger.LogAttrs(r.Context(), level, "request", attrs...)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// recoverPanics turns a handler panic into a 500 JSON response instead of a
// dropped connection.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
				panic(v)
			}
			addLogAttrs(r, slog.String("panic", fmt.Sprint(v)))
			writeJSONError(w, &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal error"})
		}()
		next.ServeHTTP(w, r)
	})
}
