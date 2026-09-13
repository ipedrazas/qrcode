package qr

import (
	"errors"
	"strconv"
)

// FormatVersion identifies the byte-level output format. Bump it whenever the
// SVG produced for a given input could change — a renderer change or an
// encoder upgrade — so content-addressed caches (ETags) are invalidated.
const FormatVersion = 1

// Color is an opaque sRGB colour.
type Color struct{ R, G, B uint8 }

// Default colours.
var (
	Black = Color{0x00, 0x00, 0x00}
	White = Color{0xff, 0xff, 0xff}
)

// ParseHexColor parses a colour of the form #rrggbb, ignoring case.
func ParseHexColor(s string) (Color, bool) {
	if len(s) != 7 || s[0] != '#' {
		return Color{}, false
	}
	var c [3]uint8
	for i := range c {
		hi, ok1 := unhex(s[1+2*i])
		lo, ok2 := unhex(s[2+2*i])
		if !ok1 || !ok2 {
			return Color{}, false
		}
		c[i] = hi<<4 | lo
	}
	return Color{c[0], c[1], c[2]}, true
}

func unhex(b byte) (byte, bool) {
	switch {
	case '0' <= b && b <= '9':
		return b - '0', true
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10, true
	case 'A' <= b && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// Hex returns the colour in canonical form: lowercase #rrggbb.
func (c Color) Hex() string {
	return string(c.appendHex(nil))
}

const hexDigits = "0123456789abcdef"

func (c Color) appendHex(b []byte) []byte {
	b = append(b, '#')
	for _, v := range [...]uint8{c.R, c.G, c.B} {
		b = append(b, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return b
}

// Style controls how a Matrix is rendered.
type Style struct {
	// Margin is the quiet zone, in modules, on each side.
	Margin     int
	Foreground Color
	Background Color
	// Transparent omits the background rectangle; Background is ignored.
	Transparent bool
}

// ErrInvalidStyle reports a Style that cannot be rendered.
var ErrInvalidStyle = errors.New("qr: invalid style")

// RenderSVG renders m as a standalone SVG document.
//
// The viewBox is in module units and the root element has no width or
// height, so the image scales to whatever box it is placed in. Dark modules
// are emitted as a single path made of one closed rectangle per horizontal
// run.
//
// The output is a pure function of m and s. Every value written into the
// markup is either an integer or a colour formatted by this package, so no
// caller-supplied text — in particular, not the encoded payload — can reach
// the document.
func RenderSVG(m *Matrix, s Style) ([]byte, error) {
	if m == nil {
		return nil, errors.New("qr: nil matrix")
	}
	if s.Margin < 0 {
		return nil, ErrInvalidStyle
	}
	size := m.Size()
	side := strconv.AppendInt(nil, int64(size+2*s.Margin), 10)

	b := make([]byte, 0, 256+size*size*2)
	b = append(b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `...)
	b = append(b, side...)
	b = append(b, ' ')
	b = append(b, side...)
	b = append(b, `">`...)

	if !s.Transparent {
		b = append(b, `<rect width="`...)
		b = append(b, side...)
		b = append(b, `" height="`...)
		b = append(b, side...)
		b = append(b, `" fill="`...)
		b = s.Background.appendHex(b)
		b = append(b, `"/>`...)
	}

	b = append(b, `<path fill="`...)
	b = s.Foreground.appendHex(b)
	b = append(b, `" shape-rendering="crispEdges" d="`...)
	for y := range size {
		for x := 0; x < size; {
			if !m.Dark(x, y) {
				x++
				continue
			}
			start := x
			for x < size && m.Dark(x, y) {
				x++
			}
			w := int64(x - start)
			b = append(b, 'M')
			b = strconv.AppendInt(b, int64(start+s.Margin), 10)
			b = append(b, ' ')
			b = strconv.AppendInt(b, int64(y+s.Margin), 10)
			b = append(b, 'h')
			b = strconv.AppendInt(b, w, 10)
			b = append(b, "v1h-"...)
			b = strconv.AppendInt(b, w, 10)
			b = append(b, 'z')
		}
	}
	b = append(b, `"/></svg>`...)
	return b, nil
}
