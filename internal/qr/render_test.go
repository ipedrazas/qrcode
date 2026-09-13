package qr

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

func mustRender(t testing.TB, in string, ec ECLevel, s Style) []byte {
	t.Helper()
	m, err := NewEncoder().Encode([]byte(in), ec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	svg, err := RenderSVG(m, s)
	if err != nil {
		t.Fatalf("RenderSVG: %v", err)
	}
	return svg
}

// TestGolden pins the exact bytes of the full encode+render pipeline. A
// failure here after a dependency upgrade means cached responses would
// change: regenerate with `task golden` and bump FormatVersion.
func TestGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    string
		ec    ECLevel
		style Style
	}{
		{"default", "https://example.com/", ECMedium, defaultStyle},
		{"transparent", "https://example.com/", ECMedium, Style{Margin: 4, Foreground: Black, Transparent: true}},
		{"colours-margin16-ecH", "https://example.com/path?q=1", ECHigh,
			Style{Margin: 16, Foreground: Color{0x1a, 0x2b, 0x3c}, Background: Color{0xfa, 0xfa, 0xf0}}},
		{"long-ecL", payload(300), ECLow, defaultStyle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustRender(t, tc.in, tc.ec, tc.style)
			path := filepath.Join("testdata", "golden", tc.name+".svg")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run `task golden` to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output differs from %s. If the change is intentional, run `task golden` "+
					"and bump qr.FormatVersion so cached ETags are invalidated.", path)
			}
		})
	}
}

// TestDeterminism renders the same inputs many times, concurrently and with
// fresh encoders, and requires byte-identical output every time.
func TestDeterminism(t *testing.T) {
	t.Parallel()
	inputs := []string{"https://example.com/", payload(777), "https://例え.jp/"}
	styles := []Style{defaultStyle, {Margin: 9, Foreground: Color{1, 2, 3}, Transparent: true}}
	for _, in := range inputs {
		for _, ec := range ECLevels() {
			for _, s := range styles {
				want := mustRender(t, in, ec, s)
				var wg sync.WaitGroup
				results := make([][]byte, 32)
				for i := range results {
					wg.Go(func() { results[i] = mustRender(t, in, ec, s) })
				}
				wg.Wait()
				for i, got := range results {
					if !bytes.Equal(got, want) {
						t.Fatalf("render %d of %q at %s differs", i, in, ec)
					}
				}
			}
		}
	}
}

// TestQuietZone checks that exactly margin light modules surround the symbol
// on every side: nothing dark in the margin, and the finder patterns' outer
// corners sitting right at its inner edge.
func TestQuietZone(t *testing.T) {
	t.Parallel()
	m, err := NewEncoder().Encode([]byte("https://example.com/quiet"), ECQuartile)
	if err != nil {
		t.Fatal(err)
	}
	size := m.Size()
	for margin := 0; margin <= 16; margin++ {
		for _, transparent := range []bool{false, true} {
			svg, err := RenderSVG(m, Style{Margin: margin, Foreground: Black, Background: White, Transparent: transparent})
			if err != nil {
				t.Fatal(err)
			}
			p := parseSVG(t, svg)
			if p.side != size+2*margin {
				t.Fatalf("margin %d: viewBox side %d, want %d", margin, p.side, size+2*margin)
			}
			for y := range p.side {
				for x := range p.side {
					inSymbol := x >= margin && x < margin+size && y >= margin && y < margin+size
					if !inSymbol && p.grid[y][x] {
						t.Fatalf("margin %d: dark module in quiet zone at (%d, %d)", margin, x, y)
					}
				}
			}
			lo, hi := margin, margin+size-1
			for _, c := range [][2]int{{lo, lo}, {hi, lo}, {lo, hi}} {
				if !p.grid[c[1]][c[0]] {
					t.Fatalf("margin %d: finder corner (%d, %d) is light; quiet zone is wider than requested", margin, c[0], c[1])
				}
			}
		}
	}
}

func TestSVGStructure(t *testing.T) {
	t.Parallel()
	const in = "https://evil.example/<script>alert(1)</script>"
	for _, transparent := range []bool{false, true} {
		svg := mustRender(t, in, ECMedium, Style{Margin: 4, Foreground: Black, Background: White, Transparent: transparent})
		s := string(svg)
		p := parseSVG(t, svg) // enforces the exact root element, attributes and path syntax
		if p.hasRect == transparent {
			t.Errorf("transparent=%v but hasRect=%v", transparent, p.hasRect)
		}
		for _, forbidden := range []string{"<?xml", "<!DOCTYPE", "width=\"" + "0", "<title", "<desc", "<!--", "data-", "evil", "script", "<rect x"} {
			if strings.Contains(s, forbidden) {
				t.Errorf("output contains %q", forbidden)
			}
		}
		if n := strings.Count(s, "<path"); n != 1 {
			t.Errorf("found %d <path> elements, want 1", n)
		}
		if strings.Count(s, "<rect") > 1 {
			t.Errorf("more than one <rect>")
		}
		if root, _, _ := strings.Cut(s, ">"); strings.Contains(root, "width") || strings.Contains(root, "height") {
			t.Errorf("root element has width/height: %s", root)
		}
	}
}

func TestRenderSVGRejectsBadInput(t *testing.T) {
	t.Parallel()
	m := NewMatrix(21, func(x, y int) bool { return (x+y)%2 == 0 })
	if _, err := RenderSVG(m, Style{Margin: -1}); err == nil {
		t.Error("negative margin: expected an error")
	}
	if _, err := RenderSVG(nil, defaultStyle); err == nil {
		t.Error("nil matrix: expected an error")
	}
}

func TestParseHexColor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"#000000", Black, true},
		{"#ffffff", White, true},
		{"#FFFFFF", White, true},
		{"#1a2B3c", Color{0x1a, 0x2b, 0x3c}, true},
		{"", Color{}, false},
		{"#fff", Color{}, false},
		{"000000", Color{}, false},
		{"#0000000", Color{}, false},
		{"#00000g", Color{}, false},
		{"#-12345", Color{}, false},
		{"red", Color{}, false},
		{"＃000000", Color{}, false},
	}
	for _, tc := range cases {
		got, ok := ParseHexColor(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseHexColor(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
		if ok {
			if again, _ := ParseHexColor(got.Hex()); again != got || got.Hex() != strings.ToLower(tc.in) {
				t.Errorf("Hex() of %q = %q, not canonical", tc.in, got.Hex())
			}
		}
	}
}

func TestParseECLevel(t *testing.T) {
	t.Parallel()
	for _, l := range ECLevels() {
		for _, s := range []string{l.String(), strings.ToLower(l.String())} {
			if got, ok := ParseECLevel(s); !ok || got != l {
				t.Errorf("ParseECLevel(%q) = %v, %v", s, got, ok)
			}
		}
	}
	for _, s := range []string{"", "X", "LM", "low", " M"} {
		if _, ok := ParseECLevel(s); ok {
			t.Errorf("ParseECLevel(%q) accepted", s)
		}
	}
}

// TestNoNetworkDependencies asserts that nothing in this package's import
// graph — ours or the encoder library's — can open a network connection.
func TestNoNetworkDependencies(t *testing.T) {
	t.Parallel()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.CommandContext(t.Context(), goBin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		switch dep {
		case "net", "net/http", "crypto/tls", "os/exec":
			t.Errorf("internal/qr transitively imports %s", dep)
		}
	}
}
