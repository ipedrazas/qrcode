// Package httpapi exposes the QR encoder over HTTP: routing, parameter
// validation and canonicalization, caching headers and middleware.
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/ipedrazas/qrcode/internal/qr"
)

// Config configures the HTTP API.
type Config struct {
	// Encoder is required.
	Encoder qr.Encoder
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// MaxURLLen defaults to DefaultMaxURLLen.
	MaxURLLen int
	// Limiter, if non-nil, rate limits every route except /healthz.
	Limiter *RateLimiter
}

const cacheControlImmutable = "public, max-age=31536000, immutable"

// NewHandler returns the service's root handler with all middleware applied.
func NewHandler(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxURLLen <= 0 {
		cfg.MaxURLLen = DefaultMaxURLLen
	}
	a := &api{encoder: cfg.Encoder, maxURLLen: cfg.MaxURLLen}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /qr", a.serveQR)
	mux.HandleFunc("GET /healthz", serveHealthz)
	mux.HandleFunc("/qr", serveMethodNotAllowed)
	mux.HandleFunc("/healthz", serveMethodNotAllowed)
	mux.HandleFunc("/", serveNotFound)

	h := rejectUncleanPaths(mux)
	if cfg.Limiter != nil {
		h = cfg.Limiter.Middleware(h)
	}
	h = recoverPanics(h)
	h = accessLog(cfg.Logger, h)
	return securityHeaders(h)
}

type api struct {
	encoder   qr.Encoder
	maxURLLen int
}

func (a *api) serveQR(w http.ResponseWriter, r *http.Request) {
	p, err := ParseParams(r.URL.RawQuery, a.maxURLLen)
	if err != nil {
		writeError(w, r, err)
		return
	}
	addLogAttrs(r,
		slog.String("url_sha256", fingerprint(p.URL)),
		slog.Int("url_len", len(p.URL)),
		slog.String("ec", p.EC.String()),
	)

	// The response is a pure function of p, so a matching tag means the
	// client already has exactly these bytes; skip encoding altogether.
	etag := p.ETag()
	if etagMatches(r.Header.Values("If-None-Match"), etag) {
		setCacheHeaders(w.Header(), etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	m, err := a.encoder.Encode([]byte(p.URL), p.EC)
	if errors.Is(err, qr.ErrCapacityExceeded) {
		writeError(w, r, capacityError(len(p.URL), p.EC))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	svg, err := qr.RenderSVG(m, p.Style())
	if err != nil {
		writeError(w, r, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(svg)))
	setCacheHeaders(h, etag)
	w.WriteHeader(http.StatusOK)
	// The SVG is built solely from integers and canonical colours; the URL
	// only influences which modules are dark. See qr.RenderSVG.
	_, _ = w.Write(svg) //nolint:gosec // G705: no request text reaches the markup

}

func serveHealthz(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

func serveMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeJSONError(w, &APIError{Status: http.StatusMethodNotAllowed, Code: CodeMethodNotAllowed, Message: "only GET and HEAD are allowed"})
}

func serveNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSONError(w, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "no such endpoint; see GET /qr"})
}

func setCacheHeaders(h http.Header, etag string) {
	h.Set("ETag", etag)
	h.Set("Cache-Control", cacheControlImmutable)
}

// etagMatches implements the If-None-Match comparison (RFC 9110 §13.1.2),
// which uses weak comparison: W/ prefixes are ignored.
func etagMatches(headers []string, etag string) bool {
	for _, h := range headers {
		for tag := range strings.SplitSeq(h, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
				return true
			}
		}
	}
	return false
}

// writeError writes err as a JSON error response. Errors that are not
// *APIError are reported as a generic 500 so internals never leak.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		addLogAttrs(r, slog.String("error", err.Error()))
		apiErr = &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal error"}
	}
	addLogAttrs(r, slog.String("error_code", apiErr.Code))
	writeJSONError(w, apiErr)
}

func writeJSONError(w http.ResponseWriter, e *APIError) {
	body, err := json.Marshal(e)
	if err != nil {
		body = []byte(`{"code":"internal_error","message":"internal error"}`)
	}
	body = append(body, '\n')
	h := w.Header()
	h.Del("ETag")
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(e.Status)
	_, _ = w.Write(body)
}

// fingerprint identifies a URL in logs without recording it: URLs are user
// data and often carry tokens in their query strings.
func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}
