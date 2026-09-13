package httpapi

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/ipedrazas/qrcode/internal/qr"
)

const exampleURL = "https://example.com/"

func TestParseParamsValidation(t *testing.T) {
	t.Parallel()
	long := func(n int) string { return exampleURL + strings.Repeat("a", n-len(exampleURL)) }

	cases := []struct {
		name     string
		query    string
		code     string
		param    string
		contains []string
	}{
		{"empty query", "", CodeMissingParameter, "url", nil},
		{"url absent", q("ec", "M"), CodeMissingParameter, "url", nil},
		{"url empty", q("url", ""), CodeInvalidURL, "url", nil},
		{"javascript scheme", q("url", "javascript:alert(1)"), CodeUnsupportedScheme, "url", nil},
		{"data scheme", q("url", "data:text/html,<b>hi</b>"), CodeUnsupportedScheme, "url", nil},
		{"scheme-relative", q("url", "//example.com/"), CodeUnsupportedScheme, "url", nil},
		{"ftp scheme", q("url", "ftp://example.com/"), CodeUnsupportedScheme, "url", nil},
		{"no scheme", q("url", "example.com"), CodeUnsupportedScheme, "url", nil},
		{"mailto", q("url", "mailto:a@example.com"), CodeUnsupportedScheme, "url", nil},
		{"opaque http", q("url", "http:example.com"), CodeInvalidURL, "url", nil},
		{"no host", q("url", "http://"), CodeInvalidURL, "url", nil},
		{"port only", q("url", "http://:80/"), CodeInvalidURL, "url", nil},
		{"whitespace", q("url", "https://example.com/a b"), CodeInvalidURL, "url", nil},
		{"newline", q("url", "https://example.com/\nx"), CodeInvalidURL, "url", nil},
		{"control char", q("url", "https://example.com/\x00"), CodeInvalidURL, "url", nil},
		{"bad escape in url", q("url", "https://example.com/%zz"), CodeInvalidURL, "url", nil},
		{"invalid utf-8", "url=https%3A%2F%2Fexample.com%2F%FF", CodeInvalidURL, "url", nil},
		{"too long", q("url", long(2049)), CodeURLTooLong, "url", []string{"2049", "2048"}},
		{"capacity at H", q("url", long(2048), "ec", "H"), CodeCapacityExceeded, "url",
			[]string{"version 40", "level H", "1273", "ec=L or ec=M", "shorter URL"}},
		{"capacity at Q", q("url", long(2048), "ec", "q"), CodeCapacityExceeded, "url",
			[]string{"level Q", "ec=L or ec=M"}},
		{"capacity at H, Q fits", q("url", long(1500), "ec", "H"), CodeCapacityExceeded, "url",
			[]string{"ec=L or ec=M or ec=Q"}},
		{"unknown param", q("url", exampleURL, "size", "200"), CodeUnknownParameter, "size", []string{`"size"`}},
		{"param names are case-sensitive", q("URL", exampleURL), CodeUnknownParameter, "URL", nil},
		{"scale is not a param", q("url", exampleURL, "scale", "2"), CodeUnknownParameter, "scale", nil},
		{"duplicate url", q("url", exampleURL, "url", "https://other.example/"), CodeDuplicateParameter, "url", nil},
		{"duplicate ec", q("url", exampleURL, "ec", "M", "ec", "M"), CodeDuplicateParameter, "ec", nil},
		{"ec invalid", q("url", exampleURL, "ec", "X"), CodeInvalidEC, "ec", nil},
		{"ec empty", q("url", exampleURL, "ec", ""), CodeInvalidEC, "ec", nil},
		{"ec word", q("url", exampleURL, "ec", "medium"), CodeInvalidEC, "ec", nil},
		{"margin below min", q("url", exampleURL, "margin", "3"), CodeInvalidMargin, "margin", nil},
		{"margin above max", q("url", exampleURL, "margin", "17"), CodeInvalidMargin, "margin", nil},
		{"margin negative", q("url", exampleURL, "margin", "-4"), CodeInvalidMargin, "margin", nil},
		{"margin float", q("url", exampleURL, "margin", "4.5"), CodeInvalidMargin, "margin", nil},
		{"margin empty", q("url", exampleURL, "margin", ""), CodeInvalidMargin, "margin", nil},
		{"margin huge", q("url", exampleURL, "margin", "99999999999999999999"), CodeInvalidMargin, "margin", nil},
		{"fg without #", q("url", exampleURL, "fg", "000000"), CodeInvalidColor, "fg", nil},
		{"fg short", q("url", exampleURL, "fg", "#fff"), CodeInvalidColor, "fg", nil},
		{"fg name", q("url", exampleURL, "fg", "red"), CodeInvalidColor, "fg", nil},
		{"fg none", q("url", exampleURL, "fg", "none"), CodeInvalidColor, "fg", nil},
		{"bg transparent word", q("url", exampleURL, "bg", "transparent"), CodeInvalidColor, "bg", nil},
		{"bg empty", q("url", exampleURL, "bg", ""), CodeInvalidColor, "bg", nil},
		{"bg injection", q("url", exampleURL, "bg", `#fff" onload="x`), CodeInvalidColor, "bg", nil},
		{"malformed escape", "url=%zz", CodeInvalidQuery, "", nil},
		{"semicolon separator", "url=" + exampleURL + ";ec=M", CodeInvalidQuery, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseParams(tc.query, DefaultMaxURLLen)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Status != 400 || apiErr.Code != tc.code || apiErr.Param != tc.param {
				t.Fatalf("got %d %s param=%q, want 400 %s param=%q (%s)",
					apiErr.Status, apiErr.Code, apiErr.Param, tc.code, tc.param, apiErr.Message)
			}
			for _, s := range tc.contains {
				if !strings.Contains(apiErr.Message, s) {
					t.Errorf("message %q does not contain %q", apiErr.Message, s)
				}
			}
			if values, err := url.ParseQuery(tc.query); err == nil {
				if u := values.Get("url"); u != "" && strings.Contains(apiErr.Message, u) {
					t.Errorf("message echoes the URL: %q", apiErr.Message)
				}
			}
		})
	}
}

func TestParseParamsMaxURLLen(t *testing.T) {
	t.Parallel()
	u := exampleURL + strings.Repeat("a", 80)
	if _, err := ParseParams(q("url", u), len(u)); err != nil {
		t.Fatalf("URL exactly at the limit rejected: %v", err)
	}
	var apiErr *APIError
	if _, err := ParseParams(q("url", u), len(u)-1); !errors.As(err, &apiErr) || apiErr.Code != CodeURLTooLong {
		t.Fatalf("URL one byte over the limit: err = %v", err)
	}
	// 2048 bytes is within version 40 capacity at L and M.
	u = exampleURL + strings.Repeat("a", DefaultMaxURLLen-len(exampleURL))
	for _, ec := range []string{"L", "M"} {
		if _, err := ParseParams(q("url", u, "ec", ec), DefaultMaxURLLen); err != nil {
			t.Errorf("2048-byte URL at %s rejected: %v", ec, err)
		}
	}
}

func TestParseParamsCanonicalization(t *testing.T) {
	t.Parallel()
	defaults := Params{URL: exampleURL, EC: qr.ECMedium, Margin: 4, FG: qr.Black, BG: qr.White}
	with := func(f func(*Params)) Params { p := defaults; f(&p); return p }

	cases := []struct {
		name  string
		query string
		want  Params
	}{
		{"defaults", q("url", exampleURL), defaults},
		{"explicit defaults", q("url", exampleURL, "ec", "M", "margin", "4", "fg", "#000000", "bg", "#ffffff"), defaults},
		{"ec lower-case", q("url", exampleURL, "ec", "h"), with(func(p *Params) { p.EC = qr.ECHigh })},
		{"ec L", q("url", exampleURL, "ec", "L"), with(func(p *Params) { p.EC = qr.ECLow })},
		{"ec q", q("url", exampleURL, "ec", "q"), with(func(p *Params) { p.EC = qr.ECQuartile })},
		{"margin max", q("url", exampleURL, "margin", "16"), with(func(p *Params) { p.Margin = 16 })},
		{"margin leading zero", q("url", exampleURL, "margin", "08"), with(func(p *Params) { p.Margin = 8 })},
		{"fg upper-case", q("url", exampleURL, "fg", "#ABCDEF"), with(func(p *Params) { p.FG = qr.Color{R: 0xab, G: 0xcd, B: 0xef} })},
		{"bg mixed case", q("url", exampleURL, "bg", "#FaFaFa"), with(func(p *Params) { p.BG = qr.Color{R: 0xfa, G: 0xfa, B: 0xfa} })},
		{"bg none", q("url", exampleURL, "bg", "none"), with(func(p *Params) { p.Transparent = true })},
		{"bg NONE", q("url", exampleURL, "bg", "NONE"), with(func(p *Params) { p.Transparent = true })},
		{"param order irrelevant", q("ec", "H", "url", exampleURL), with(func(p *Params) { p.EC = qr.ECHigh })},
		{"url kept verbatim", q("url", "HTTPS://Example.COM/A?b=1#frag"),
			with(func(p *Params) { p.URL = "HTTPS://Example.COM/A?b=1#frag" })},
		{"unicode url", q("url", "https://例え.jp/"), with(func(p *Params) { p.URL = "https://例え.jp/" })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseParams(tc.query, DefaultMaxURLLen)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestETagStability(t *testing.T) {
	t.Parallel()
	etag := func(t *testing.T, query string) string {
		t.Helper()
		p, err := ParseParams(query, DefaultMaxURLLen)
		if err != nil {
			t.Fatalf("ParseParams(%q): %v", query, err)
		}
		return p.ETag()
	}

	// Pinned so an accidental change to the canonical form, which would
	// silently invalidate every cached response, fails loudly.
	t.Run("pinned", func(t *testing.T) {
		// sha256("v=1;ec=M;margin=4;fg=#000000;bg=#ffffff;url=20:https://example.com/")[:16]
		const want = `"e39f2508e14ccde1a83c3a30e6ffdc45"`
		if got := etag(t, q("url", exampleURL)); got != want {
			t.Fatalf("ETag = %s, want %s. If the canonical form changed on purpose, update this value.", got, want)
		}
	})

	// Each group must share one ETag; different groups must not collide.
	groups := [][]string{
		{
			q("url", exampleURL),
			q("url", exampleURL, "ec", "M"),
			q("url", exampleURL, "ec", "m"),
			q("ec", "M", "url", exampleURL),
			q("url", exampleURL, "margin", "4", "fg", "#000000", "bg", "#FFFFFF"),
		},
		{q("url", exampleURL, "ec", "H"), q("ec", "h", "url", exampleURL)},
		{q("url", exampleURL, "fg", "#ABCDEF"), q("fg", "#abcdef", "url", exampleURL)},
		{q("url", exampleURL, "bg", "none"), q("url", exampleURL, "bg", "None")},
		{q("url", exampleURL, "margin", "5")},
		{q("url", exampleURL, "bg", "#fffffe")},
		{q("url", "https://example.com")},
		{q("url", "HTTPS://example.com/")},
	}
	seen := map[string]int{}
	for i, g := range groups {
		first := etag(t, g[0])
		for _, query := range g[1:] {
			if got := etag(t, query); got != first {
				t.Errorf("group %d: %q has ETag %s, %q has %s", i, g[0], first, query, got)
			}
		}
		if j, dup := seen[first]; dup {
			t.Errorf("groups %d and %d share ETag %s", j, i, first)
		}
		seen[first] = i
	}
}
