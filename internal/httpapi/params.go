package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ipedrazas/qrcode/internal/qr"
)

// Defaults and limits for /qr parameters.
const (
	DefaultMargin = 4
	MinMargin     = 4 // the minimum quiet zone required by ISO/IEC 18004
	MaxMargin     = 16

	// DefaultMaxURLLen is the default upper bound on the url parameter, in bytes.
	DefaultMaxURLLen = 2048
)

// Error codes returned in the JSON body of error responses. They are part of
// the API contract: never change or reuse an existing value.
const (
	CodeInvalidQuery       = "invalid_query"
	CodeUnknownParameter   = "unknown_parameter"
	CodeDuplicateParameter = "duplicate_parameter"
	CodeMissingParameter   = "missing_parameter"
	CodeInvalidURL         = "invalid_url"
	CodeUnsupportedScheme  = "unsupported_scheme"
	CodeURLTooLong         = "url_too_long"
	CodeCapacityExceeded   = "capacity_exceeded"
	CodeInvalidEC          = "invalid_ec"
	CodeInvalidMargin      = "invalid_margin"
	CodeInvalidColor       = "invalid_color"
	CodeNotFound           = "not_found"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeRateLimited        = "rate_limited"
	CodeInternal           = "internal_error"
)

// APIError is an error response with a stable machine-readable code.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

func badRequest(code, param, message string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: code, Param: param, Message: message}
}

// Params is the canonical form of a /qr request. Everything downstream of
// ParseParams works from this, never from the raw query.
type Params struct {
	URL         string
	EC          qr.ECLevel
	Margin      int
	FG          qr.Color
	BG          qr.Color
	Transparent bool // bg=none
}

// Style returns the rendering style described by p.
func (p Params) Style() qr.Style {
	return qr.Style{Margin: p.Margin, Foreground: p.FG, Background: p.BG, Transparent: p.Transparent}
}

// Canonical returns a stable, unambiguous serialisation of p, including the
// output format version. Requests with equal canonical forms produce
// byte-identical responses.
func (p Params) Canonical() string {
	bg := "none"
	if !p.Transparent {
		bg = p.BG.Hex()
	}
	// The URL goes last and is length-prefixed, so no URL can collide with a
	// different parameter set.
	return fmt.Sprintf("v=%d;ec=%s;margin=%d;fg=%s;bg=%s;url=%d:%s",
		qr.FormatVersion, p.EC, p.Margin, p.FG.Hex(), bg, len(p.URL), p.URL)
}

// ETag returns a strong entity tag for the response to p.
func (p Params) ETag() string {
	sum := sha256.Sum256([]byte(p.Canonical()))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

var knownParams = map[string]bool{"url": true, "ec": true, "margin": true, "fg": true, "bg": true}

// ParseParams validates a raw /qr query string and returns its canonical
// form. Any returned error is an *APIError.
func ParseParams(rawQuery string, maxURLLen int) (Params, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return Params{}, badRequest(CodeInvalidQuery, "", "query string is malformed")
	}

	// Check keys in sorted order so the reported error is deterministic.
	keys := slices.Sorted(maps.Keys(values))
	for _, k := range keys {
		if !knownParams[k] {
			return Params{}, badRequest(CodeUnknownParameter, truncate(k, 64),
				fmt.Sprintf("unknown parameter %q; allowed parameters are url, ec, margin, fg and bg", truncate(k, 64)))
		}
	}
	for _, k := range keys {
		if len(values[k]) > 1 {
			return Params{}, badRequest(CodeDuplicateParameter, k, fmt.Sprintf("parameter %q given more than once", k))
		}
	}
	get := func(k string) (string, bool) {
		v, ok := values[k]
		if !ok {
			return "", false
		}
		return v[0], true
	}

	p := Params{EC: qr.ECMedium, Margin: DefaultMargin, FG: qr.Black, BG: qr.White}

	raw, ok := get("url")
	if !ok {
		return Params{}, badRequest(CodeMissingParameter, "url", "url is required")
	}
	if err := validateURL(raw, maxURLLen); err != nil {
		return Params{}, err
	}
	p.URL = raw

	if v, ok := get("ec"); ok {
		ec, ok := qr.ParseECLevel(v)
		if !ok {
			return Params{}, badRequest(CodeInvalidEC, "ec", "ec must be one of L, M, Q or H")
		}
		p.EC = ec
	}

	if v, ok := get("margin"); ok {
		m, err := strconv.Atoi(v)
		if err != nil || m < MinMargin || m > MaxMargin {
			return Params{}, badRequest(CodeInvalidMargin, "margin",
				fmt.Sprintf("margin must be an integer from %d to %d", MinMargin, MaxMargin))
		}
		p.Margin = m
	}

	if v, ok := get("fg"); ok {
		c, ok := qr.ParseHexColor(v)
		if !ok {
			return Params{}, badRequest(CodeInvalidColor, "fg", "fg must be a colour of the form #rrggbb (URL-encode # as %23)")
		}
		p.FG = c
	}

	if v, ok := get("bg"); ok {
		if strings.EqualFold(v, "none") {
			p.Transparent = true
		} else {
			c, ok := qr.ParseHexColor(v)
			if !ok {
				return Params{}, badRequest(CodeInvalidColor, "bg", "bg must be none or a colour of the form #rrggbb (URL-encode # as %23)")
			}
			p.BG = c
		}
	}

	if len(p.URL) > qr.MaxPayloadBytes(p.EC) {
		return Params{}, capacityError(len(p.URL), p.EC)
	}
	return p, nil
}

// validateURL checks that raw is an absolute http(s) URL. Error messages
// never echo the URL: it is user data and may carry credentials.
func validateURL(raw string, maxLen int) error {
	switch {
	case raw == "":
		return badRequest(CodeInvalidURL, "url", "url must not be empty")
	case len(raw) > maxLen:
		return badRequest(CodeURLTooLong, "url", fmt.Sprintf("url is %d bytes; the maximum is %d", len(raw), maxLen))
	case !utf8.ValidString(raw):
		return badRequest(CodeInvalidURL, "url", "url must be valid UTF-8")
	case strings.ContainsFunc(raw, unicode.IsSpace):
		return badRequest(CodeInvalidURL, "url", "url must not contain whitespace; percent-encode it")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return badRequest(CodeInvalidURL, "url", "url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return badRequest(CodeUnsupportedScheme, "url", "url must be absolute and use the http or https scheme")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return badRequest(CodeInvalidURL, "url", "url must be absolute and include a host, e.g. https://example.com/")
	}
	return nil
}

func capacityError(n int, ec qr.ECLevel) *APIError {
	msg := fmt.Sprintf("url is %d bytes, which exceeds the capacity of the largest QR code (version 40) at error correction level %s (%d bytes)",
		n, ec, qr.MaxPayloadBytes(ec))
	var fits []string
	for _, l := range qr.ECLevels() {
		if l < ec && n <= qr.MaxPayloadBytes(l) {
			fits = append(fits, l.String())
		}
	}
	if len(fits) > 0 {
		msg += fmt.Sprintf("; use a lower error correction level (ec=%s) or a shorter URL", strings.Join(fits, " or ec="))
	} else {
		msg += "; use a shorter URL"
	}
	return badRequest(CodeCapacityExceeded, "url", msg)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
