package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

var knownCodes = set(CodeInvalidQuery, CodeUnknownParameter, CodeDuplicateParameter, CodeMissingParameter,
	CodeInvalidURL, CodeUnsupportedScheme, CodeURLTooLong, CodeCapacityExceeded, CodeInvalidEC,
	CodeInvalidMargin, CodeInvalidColor)

// checkResponse asserts the invariants that must hold for any input: no
// panic (implicit), either a well-formed SVG or a well-formed 400.
func checkResponse(t *testing.T, h http.Handler, rawQuery string) {
	w := serve(h, http.MethodGet, "/qr", rawQuery)
	switch w.Code {
	case http.StatusOK:
		if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml; charset=utf-8" {
			t.Fatalf("Content-Type %q", ct)
		}
		if !svgShapeRE.Match(w.Body.Bytes()) {
			t.Fatalf("SVG has unexpected structure for query %q: %.200s", rawQuery, w.Body)
		}
	case http.StatusBadRequest:
		if e := decodeError(t, w); !knownCodes[e.Code] {
			t.Fatalf("unknown error code %q", e.Code)
		}
	default:
		t.Fatalf("status %d for query %q: %s", w.Code, rawQuery, w.Body)
	}
}

func FuzzURLParam(f *testing.F) {
	for _, s := range []string{
		"https://example.com/",
		"http://a.io",
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"//example.com",
		"https://example.com/\"><script>alert(1)</script>",
		"https://例え.jp/",
		"http://[::1]:80/",
		"http://%41%42/",
		"https://" + strings.Repeat("a", 2100),
		"\x00\xff",
		"",
	} {
		f.Add(s)
	}
	h := newHandler(f, Config{})
	f.Fuzz(func(t *testing.T, s string) {
		checkResponse(t, h, url.Values{"url": {s}}.Encode())
	})
}

func FuzzQuery(f *testing.F) {
	for _, s := range []string{
		"url=https%3A%2F%2Fexample.com%2F",
		"url=https%3A%2F%2Fexample.com%2F&ec=h&margin=16&fg=%23ABCDEF&bg=none",
		"url=https://example.com/&url=x",
		"url=%zz",
		"a;b",
		"&&&=&",
		"url=https%3A%2F%2Fexample.com%2F&margin=99999999999999999999999",
	} {
		f.Add(s)
	}
	h := newHandler(f, Config{})
	f.Fuzz(func(t *testing.T, raw string) {
		checkResponse(t, h, raw)
	})
}
