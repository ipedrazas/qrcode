package httpapi

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestUIAssets(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	for _, a := range uiAssets {
		w := serve(h, http.MethodGet, a.path, "")
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", a.path, w.Code, w.Body.Len())
			continue
		}
		hdr := w.Header()
		if got := hdr.Get("Content-Type"); got != a.contentType {
			t.Errorf("%s: Content-Type = %q, want %q", a.path, got, a.contentType)
		}
		if got := hdr.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", a.path, got)
		}
		etag := hdr.Get("ETag")
		if !regexp.MustCompile(`^"[0-9a-f]{32}"$`).MatchString(etag) {
			t.Errorf("%s: ETag %q is not a strong hex tag", a.path, etag)
		}
		if w := serve(h, http.MethodGet, a.path, "", withHeader("If-None-Match", etag)); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Errorf("%s: revalidation got status %d with %d bytes", a.path, w.Code, w.Body.Len())
		}
		if w := serve(h, http.MethodHead, a.path, ""); w.Code != http.StatusOK {
			t.Errorf("%s: HEAD status %d", a.path, w.Code)
		}
	}
}

// TestUIPageWorksUnderItsCSP checks for everything the page's CSP would
// silently block: inline scripts, inline styles and event-handler attributes.
func TestUIPageWorksUnderItsCSP(t *testing.T) {
	t.Parallel()
	page := serve(newHandler(t, Config{}), http.MethodGet, "/", "").Body.String()

	for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(tag, " src=") {
			t.Errorf("inline script: %s", tag)
		}
	}
	if m := regexp.MustCompile(`(?i)<style|\sstyle=|\son[a-z]+=`).FindString(page); m != "" {
		t.Errorf("page has inline style or handler: %q", m)
	}
	if !strings.Contains(page, `<link rel="icon" href="favicon.svg"`) {
		t.Error("page does not link the favicon")
	}
}

// TestUIReferencesResolve requests every file the page links to, and checks
// that every element app.js looks up exists in the page.
func TestUIReferencesResolve(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})
	page := serve(h, http.MethodGet, "/", "").Body.String()

	refs := regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) < 4 {
		t.Fatalf("found only %d references in the page", len(refs))
	}
	for _, m := range refs {
		if w := serve(h, http.MethodGet, "/"+m[1], ""); w.Code != http.StatusOK {
			t.Errorf("page references %s, which answers %d", m[1], w.Code)
		}
	}

	js := serve(h, http.MethodGet, "/app.js", "").Body.String()
	ids := regexp.MustCompile(`getElementById\('([^']+)'\)`).FindAllStringSubmatch(js, -1)
	if len(ids) == 0 {
		t.Fatal("app.js looks up no elements")
	}
	for _, m := range ids {
		if !strings.Contains(page, `id="`+m[1]+`"`) {
			t.Errorf("app.js looks up #%s, which the page lacks", m[1])
		}
	}
}

func TestFavicons(t *testing.T) {
	t.Parallel()
	h := newHandler(t, Config{})

	svg := serve(h, http.MethodGet, "/favicon.svg", "").Body.Bytes()
	if !bytes.HasPrefix(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">`)) {
		t.Errorf("favicon.svg: unexpected start %.80s", svg)
	}

	ico := serve(h, http.MethodGet, "/favicon.ico", "").Body.Bytes()
	le := binary.LittleEndian
	if len(ico) < 6 || le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 {
		t.Fatalf("favicon.ico: not an ICO header: % x", ico[:min(len(ico), 6)])
	}
	count := int(le.Uint16(ico[4:]))
	sizes := map[int]bool{}
	for i := range count {
		e := ico[6+16*i:]
		dim := int(e[0])
		size, offset := le.Uint32(e[8:]), le.Uint32(e[12:])
		img, err := png.Decode(bytes.NewReader(ico[offset : offset+size]))
		if err != nil {
			t.Fatalf("favicon.ico entry %d: %v", i, err)
		}
		if b := img.Bounds(); b.Dx() != dim || b.Dy() != dim {
			t.Errorf("favicon.ico entry %d: directory says %d px, image is %v", i, dim, b)
		}
		sizes[dim] = true
	}
	if !sizes[16] || !sizes[32] {
		t.Errorf("favicon.ico sizes %v, want 16 and 32", sizes)
	}
}
