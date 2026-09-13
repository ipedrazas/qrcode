package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/ipedrazas/qrcode/internal/qr"
)

func TestQRSuccess(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	w := serve(h, http.MethodGet, "/qr", q("url", exampleURL))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	hdr := w.Header()
	want := map[string]string{
		"Content-Type":            "image/svg+xml; charset=utf-8",
		"Cache-Control":           "public, max-age=31536000, immutable",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
		"Content-Length":          strconv.Itoa(w.Body.Len()),
	}
	for k, v := range want {
		if got := hdr.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !regexp.MustCompile(`^"[0-9a-f]{32}"$`).MatchString(hdr.Get("ETag")) {
		t.Errorf("ETag %q is not a strong hex tag", hdr.Get("ETag"))
	}
	if !svgShapeRE.Match(w.Body.Bytes()) {
		t.Errorf("unexpected SVG: %.200s", w.Body)
	}
}

func TestHEAD(t *testing.T) {
	t.Parallel()
	w := serve(newHandler(t, Config{}), http.MethodHead, "/qr", q("url", exampleURL))
	if w.Code != http.StatusOK || w.Header().Get("ETag") == "" {
		t.Fatalf("HEAD: status %d, ETag %q", w.Code, w.Header().Get("ETag"))
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{Limiter: NewRateLimiter(1, 1)})
	etag := serve(h, http.MethodGet, "/qr", q("url", exampleURL), withRemote("192.0.2.10:1")).Header().Get("ETag")

	cases := []struct {
		name   string
		method string
		path   string
		query  string
		opts   []reqOpt
		status int
	}{
		{"ok", http.MethodGet, "/qr", q("url", exampleURL), nil, http.StatusOK},
		{"not modified", http.MethodGet, "/qr", q("url", exampleURL), []reqOpt{withHeader("If-None-Match", etag)}, http.StatusNotModified},
		{"bad request", http.MethodGet, "/qr", q("url", "javascript:alert(1)"), nil, http.StatusBadRequest},
		{"not found", http.MethodGet, "/nope", "", nil, http.StatusNotFound},
		{"method not allowed", http.MethodPost, "/qr", "", nil, http.StatusMethodNotAllowed},
		{"healthz", http.MethodGet, "/healthz", "", nil, http.StatusOK},
		{"rate limited", http.MethodGet, "/qr", q("url", exampleURL), []reqOpt{withRemote("192.0.2.10:1")}, http.StatusTooManyRequests},
		{"ui page", http.MethodGet, "/", "", nil, http.StatusOK},
		{"ui asset", http.MethodGet, "/app.js", "", nil, http.StatusOK},
		{"favicon", http.MethodGet, "/favicon.svg", "", nil, http.StatusOK},
	}
	for i, tc := range cases {
		opts := append([]reqOpt{withRemote("198.51.100." + strconv.Itoa(i+1) + ":1")}, tc.opts...)
		w := serve(h, tc.method, tc.path, tc.query, opts...)
		if w.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.name, w.Code, tc.status)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", tc.name, got)
		}
		// Only the UI page itself relaxes the policy, to run its own script.
		wantCSP := "default-src 'none'; sandbox"
		if tc.path == "/" {
			wantCSP = uiPagePolicy
		}
		if got := w.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s: Content-Security-Policy = %q", tc.name, got)
		}
	}
}

func TestErrorResponses(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	cases := []struct {
		name   string
		method string
		path   string
		query  string
		status int
		code   string
	}{
		{"missing url", http.MethodGet, "/qr", "", 400, CodeMissingParameter},
		{"bad scheme", http.MethodGet, "/qr", q("url", "javascript:alert(1)"), 400, CodeUnsupportedScheme},
		{"unknown param", http.MethodGet, "/qr", q("url", exampleURL, "size", "9"), 400, CodeUnknownParameter},
		{"capacity", http.MethodGet, "/qr", q("url", exampleURL+string(bytes.Repeat([]byte("a"), 2000)), "ec", "H"), 400, CodeCapacityExceeded},
		{"not found", http.MethodGet, "/qr/", q("url", exampleURL), 404, CodeNotFound},
		{"index.html", http.MethodGet, "/index.html", "", 404, CodeNotFound},
		{"ui subpath", http.MethodGet, "/app.js/x", "", 404, CodeNotFound},
		{"post root", http.MethodPost, "/", "", 405, CodeMethodNotAllowed},
		{"post favicon", http.MethodPost, "/favicon.ico", "", 405, CodeMethodNotAllowed},
		// Non-canonical paths must not get ServeMux's HTML 307 to the clean path.
		{"double slash", http.MethodGet, "//qr", q("url", exampleURL), 404, CodeNotFound},
		{"dot segment", http.MethodGet, "/./qr", q("url", exampleURL), 404, CodeNotFound},
		{"dot-dot segment", http.MethodGet, "/x/../qr", q("url", exampleURL), 404, CodeNotFound},
		{"double slash healthz", http.MethodGet, "//healthz", "", 404, CodeNotFound},
		{"post", http.MethodPost, "/qr", q("url", exampleURL), 405, CodeMethodNotAllowed},
		{"delete healthz", http.MethodDelete, "/healthz", "", 405, CodeMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := serve(h, tc.method, tc.path, tc.query)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			if e := decodeError(t, w); e.Code != tc.code {
				t.Fatalf("code %q, want %q", e.Code, tc.code)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := w.Header().Get("ETag"); got != "" {
				t.Errorf("error response carries ETag %q", got)
			}
			if got := w.Header().Get("Location"); got != "" {
				t.Errorf("error response redirects to %q", got)
			}
			if tc.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("Allow = %q", w.Header().Get("Allow"))
			}
		})
	}
}

func TestConditionalRequests(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	query := q("url", exampleURL)
	first := serve(h, http.MethodGet, "/qr", query)
	etag := first.Header().Get("ETag")

	cases := []struct {
		inm  string
		want int
	}{
		{etag, http.StatusNotModified},
		{"W/" + etag, http.StatusNotModified},
		{`"deadbeef", ` + etag, http.StatusNotModified},
		{"*", http.StatusNotModified},
		{`"deadbeef"`, http.StatusOK},
		{etag[:len(etag)-2] + `0"`, http.StatusOK},
	}
	for _, tc := range cases {
		w := serve(h, http.MethodGet, "/qr", query, withHeader("If-None-Match", tc.inm))
		if w.Code != tc.want {
			t.Errorf("If-None-Match %s: status %d, want %d", tc.inm, w.Code, tc.want)
			continue
		}
		if w.Code == http.StatusNotModified {
			if w.Body.Len() != 0 {
				t.Errorf("304 has a body")
			}
			if w.Header().Get("ETag") != etag || w.Header().Get("Cache-Control") != cacheControlImmutable {
				t.Errorf("304 headers: ETag %q, Cache-Control %q", w.Header().Get("ETag"), w.Header().Get("Cache-Control"))
			}
		}
	}

	// A tag for different parameters must not match.
	w := serve(h, http.MethodGet, "/qr", q("url", exampleURL, "ec", "H"), withHeader("If-None-Match", etag))
	if w.Code != http.StatusOK {
		t.Errorf("ETag for ec=M matched a request for ec=H")
	}
	// An invalid request is rejected even if it presents a wildcard tag.
	w = serve(h, http.MethodGet, "/qr", q("url", "ftp://x"), withHeader("If-None-Match", "*"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid request with If-None-Match: status %d", w.Code)
	}
}

func TestCanonicalRequestsShareCacheEntry(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	variants := []string{
		q("url", exampleURL, "ec", "m"),
		q("ec", "M", "url", exampleURL),
		q("url", exampleURL),
		q("bg", "#FFFFFF", "url", exampleURL, "fg", "#000000", "margin", "4"),
	}
	base := serve(h, http.MethodGet, "/qr", variants[0])
	for _, v := range variants[1:] {
		w := serve(h, http.MethodGet, "/qr", v)
		if w.Header().Get("ETag") != base.Header().Get("ETag") || !bytes.Equal(w.Body.Bytes(), base.Body.Bytes()) {
			t.Errorf("%q and %q differ", variants[0], v)
		}
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	w := serve(newHandler(t, Config{}), http.MethodGet, "/healthz", "")
	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body)
	}
}

func TestLogsNeverContainURL(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := newHandler(t, Config{Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})

	const secret = "tok_5up3r53cr3t"
	serve(h, http.MethodGet, "/qr", q("url", "https://example.com/reset?token="+secret))
	serve(h, http.MethodGet, "/qr", q("url", "ftp://example.com/?token="+secret))
	serve(h, http.MethodGet, "/qr", q("url", "https://example.com/?token="+secret, "bogus", "1"))

	out := logs.String()
	if bytes.Contains(logs.Bytes(), []byte(secret)) {
		t.Fatalf("logs contain the raw URL:\n%s", out)
	}
	for _, want := range []string{`"url_sha256":"`, `"url_len":`, `"error_code":"unsupported_scheme"`, `"path":"/qr"`} {
		if !bytes.Contains(logs.Bytes(), []byte(want)) {
			t.Errorf("logs lack %s:\n%s", want, out)
		}
	}
}

func TestLogURLsOptIn(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := newHandler(t, Config{LogURLs: true, Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})

	const valid = "https://example.com/menu?table=12"
	serve(h, http.MethodGet, "/qr", q("url", valid))
	// Rejected input is never logged, even with LogURLs on.
	serve(h, http.MethodGet, "/qr", q("url", "ftp://example.com/?token=tok_5up3r53cr3t"))

	out := logs.String()
	if !bytes.Contains(logs.Bytes(), []byte(`"url":"`+valid+`"`)) {
		t.Errorf("logs lack the valid URL:\n%s", out)
	}
	if bytes.Contains(logs.Bytes(), []byte("tok_5up3r53cr3t")) {
		t.Errorf("logs contain a rejected URL:\n%s", out)
	}
}

func TestRateLimiting(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(0.5, 2)
	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }
	h := newHandler(t, Config{Limiter: rl})

	client := withRemote("203.0.113.7:4321")
	for i := range 2 {
		if w := serve(h, http.MethodGet, "/qr", q("url", exampleURL), client); w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
	}
	w := serve(h, http.MethodGet, "/qr", q("url", exampleURL), client)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over limit: status %d", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	if e := decodeError(t, w); e.Code != CodeRateLimited {
		t.Errorf("code %q", e.Code)
	}

	if w := serve(h, http.MethodGet, "/healthz", "", client); w.Code != http.StatusOK {
		t.Errorf("healthz was rate limited: %d", w.Code)
	}
	if w := serve(h, http.MethodGet, "/qr", q("url", exampleURL), withRemote("203.0.113.8:1")); w.Code != http.StatusOK {
		t.Errorf("another client was limited: %d", w.Code)
	}

	now = now.Add(2 * time.Second)
	if w := serve(h, http.MethodGet, "/qr", q("url", exampleURL), client); w.Code != http.StatusOK {
		t.Errorf("after refill: status %d", w.Code)
	}
}

type fakeEncoder struct {
	err   error
	panic bool
}

func (f fakeEncoder) Encode([]byte, qr.ECLevel) (*qr.Matrix, error) {
	if f.panic {
		panic("boom")
	}
	return nil, f.err
}

func TestEncoderFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		enc    fakeEncoder
		status int
		code   string
	}{
		{"internal error", fakeEncoder{err: errors.New("secret internal detail")}, 500, CodeInternal},
		{"capacity", fakeEncoder{err: qr.ErrCapacityExceeded}, 400, CodeCapacityExceeded},
		{"panic", fakeEncoder{panic: true}, 500, CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := serve(newHandler(t, Config{Encoder: tc.enc}), http.MethodGet, "/qr", q("url", exampleURL))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			e := decodeError(t, w)
			if e.Code != tc.code {
				t.Fatalf("code %q, want %q", e.Code, tc.code)
			}
			if bytes.Contains(w.Body.Bytes(), []byte("secret")) || bytes.Contains(w.Body.Bytes(), []byte("boom")) {
				t.Fatalf("internal detail leaked: %s", w.Body)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers missing on 500")
			}
		})
	}
}
