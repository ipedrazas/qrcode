package qr

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"testing"

	"github.com/makiuchi-d/gozxing"
	gozxingqr "github.com/makiuchi-d/gozxing/qrcode"
)

// svgRE matches the complete document RenderSVG is allowed to produce. Any
// other markup — a width attribute, a <title>, a comment — fails parsing.
var (
	svgRE = regexp.MustCompile(`^<svg xmlns="http://www\.w3\.org/2000/svg" viewBox="0 0 (\d+) (\d+)">` +
		`(?:<rect width="(\d+)" height="(\d+)" fill="(#[0-9a-f]{6})"/>)?` +
		`<path fill="(#[0-9a-f]{6})" shape-rendering="crispEdges" d="([^"]*)"/></svg>$`)
	runRE = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)
)

type parsedSVG struct {
	side    int
	hasRect bool
	bg, fg  string
	grid    [][]bool // [y][x] over the full viewBox, quiet zone included
}

// parseSVG rasterizes an SVG produced by RenderSVG back into a module grid,
// failing on anything it does not expect. Each path run must be maximal, in
// bounds and non-overlapping.
func parseSVG(t testing.TB, svg []byte) parsedSVG {
	t.Helper()
	m := svgRE.FindSubmatch(svg)
	if m == nil {
		t.Fatalf("SVG does not match the expected structure: %.300s", svg)
	}
	side := mustAtoi(t, m[1])
	if h := mustAtoi(t, m[2]); h != side {
		t.Fatalf("viewBox is not square: %d×%d", side, h)
	}
	p := parsedSVG{side: side, hasRect: m[3] != nil, fg: string(m[6])}
	if p.hasRect {
		if mustAtoi(t, m[3]) != side || mustAtoi(t, m[4]) != side {
			t.Fatalf("background rect does not cover the viewBox")
		}
		p.bg = string(m[5])
	}

	p.grid = make([][]bool, side)
	for y := range p.grid {
		p.grid[y] = make([]bool, side)
	}
	d := m[7]
	type run struct{ x, y, w int }
	var runs []run
	pos := 0
	for _, loc := range runRE.FindAllSubmatchIndex(d, -1) {
		if loc[0] != pos {
			t.Fatalf("unexpected path data at offset %d: %.40q", pos, d[pos:])
		}
		pos = loc[1]
		x := mustAtoi(t, d[loc[2]:loc[3]])
		y := mustAtoi(t, d[loc[4]:loc[5]])
		w := mustAtoi(t, d[loc[6]:loc[7]])
		if back := mustAtoi(t, d[loc[8]:loc[9]]); back != w || w == 0 {
			t.Fatalf("malformed run at offset %d", loc[0])
		}
		if y >= side || x+w > side {
			t.Fatalf("run M%d %d h%d leaves the viewBox (%d)", x, y, w, side)
		}
		for i := x; i < x+w; i++ {
			if p.grid[y][i] {
				t.Fatalf("runs overlap at (%d, %d)", i, y)
			}
			p.grid[y][i] = true
		}
		runs = append(runs, run{x, y, w})
	}
	if pos != len(d) {
		t.Fatalf("trailing path data at offset %d: %.40q", pos, d[pos:])
	}
	for _, r := range runs {
		if (r.x > 0 && p.grid[r.y][r.x-1]) || (r.x+r.w < side && p.grid[r.y][r.x+r.w]) {
			t.Fatalf("run M%d %d h%d is not maximal", r.x, r.y, r.w)
		}
	}
	return p
}

func mustAtoi(t testing.TB, b []byte) int {
	t.Helper()
	n, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatalf("bad integer %q: %v", b, err)
	}
	return n
}

// assertGridMatchesMatrix checks that the rendered grid shows exactly m,
// offset by margin, with nothing else dark.
func assertGridMatchesMatrix(t testing.TB, grid [][]bool, m *Matrix, margin int) {
	t.Helper()
	if want := m.Size() + 2*margin; len(grid) != want {
		t.Fatalf("grid side = %d, want %d", len(grid), want)
	}
	for y, row := range grid {
		for x, dark := range row {
			if want := m.Dark(x-margin, y-margin); dark != want {
				t.Fatalf("grid(%d, %d) = %v, matrix says %v", x, y, dark, want)
			}
		}
	}
}

// toGray paints a grid onto a white image, scale pixels per module.
func toGray(grid [][]bool, scale int) *image.Gray {
	side := len(grid) * scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			v := uint8(0xff)
			if grid[y/scale][x/scale] {
				v = 0
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return img
}

// decode reads a QR code with ZXing — an implementation independent of the
// encoder — and returns the payload and the error correction level it found.
//
// It uses PURE_BARCODE mode: the image is a perfect axis-aligned raster, so
// perspective detection adds nothing, and ZXing's finder-pattern detector
// occasionally mislocates patterns on such synthetic, perfectly sharp images
// (~1–2% of symbols; they decode fine in pure mode and with other decoders).
// Format information, Reed–Solomon correction and data decoding still run in
// full, so a wrong matrix still fails.
func decode(t testing.TB, img image.Image) (text, ec string) {
	t.Helper()
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatalf("binarize: %v", err)
	}
	hints := map[gozxing.DecodeHintType]any{
		gozxing.DecodeHintType_PURE_BARCODE:  true,
		gozxing.DecodeHintType_CHARACTER_SET: "UTF-8",
	}
	res, err := gozxingqr.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return res.GetText(), fmt.Sprint(res.GetResultMetadata()[gozxing.ResultMetadataType_ERROR_CORRECTION_LEVEL])
}

// payload returns a deterministic URL-like string of exactly n bytes.
func payload(n int) string {
	const prefix = "https://example.com/"
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-._~/?=&%"
	b := make([]byte, n)
	for i := range b {
		if i < len(prefix) {
			b[i] = prefix[i]
		} else {
			b[i] = alphabet[(i*7)%len(alphabet)]
		}
	}
	return string(b)
}

var defaultStyle = Style{Margin: 4, Foreground: Black, Background: White}
