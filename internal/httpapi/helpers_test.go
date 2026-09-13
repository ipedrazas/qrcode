package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ipedrazas/qrcode/internal/qr"
)

// svgShapeRE matches every SVG the service may return. Only integers and
// canonical colours can appear, so a match proves no request text leaked in.
var svgShapeRE = regexp.MustCompile(`^<svg xmlns="http://www\.w3\.org/2000/svg" viewBox="0 0 \d+ \d+">` +
	`(?:<rect width="\d+" height="\d+" fill="#[0-9a-f]{6}"/>)?` +
	`<path fill="#[0-9a-f]{6}" shape-rendering="crispEdges" d="(?:M\d+ \d+h\d+v1h-\d+z)+"/></svg>$`)

func newHandler(t testing.TB, cfg Config) http.Handler {
	t.Helper()
	if cfg.Encoder == nil {
		cfg.Encoder = qr.NewEncoder()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return NewHandler(cfg)
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Add(k, v) } }

func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }

// serve sends a request with a raw, unvalidated query string.
func serve(h http.Handler, method, path, rawQuery string, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.URL.RawQuery = rawQuery
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// q builds an encoded query string from alternating keys and values.
func q(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv[i]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv[i+1]))
	}
	return b.String()
}

func decodeError(t testing.TB, w *httptest.ResponseRecorder) APIError {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("error Content-Type = %q", ct)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("<svg")) {
		t.Fatal("error response contains an SVG")
	}
	var e APIError
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("error body is not the expected JSON: %v: %s", err, w.Body)
	}
	if e.Code == "" || e.Message == "" {
		t.Fatalf("error body lacks code or message: %s", w.Body)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("trailing data after error JSON: %s", w.Body)
	}
	return e
}
