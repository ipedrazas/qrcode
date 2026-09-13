//go:build ignore

// gen_icons writes ui/favicon.svg and ui/favicon.ico from one geometry, so
// the vector and bitmap icons cannot drift apart. Run it with go generate.
//
// The icon is a rounded blue tile with three white finder patterns and a few
// data modules. Every coordinate is even, so at 16px each edge falls on a
// pixel boundary and the icon stays crisp.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"strings"
)

const (
	units  = 32 // viewBox size
	radius = 7  // tile corner radius
)

var (
	tile  = color.NRGBA{0x1d, 0x4e, 0xd8, 0xff}
	light = color.NRGBA{0xff, 0xff, 0xff, 0xff}
)

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y float64) bool {
	return x >= float64(r.x) && x < float64(r.x+r.w) && y >= float64(r.y) && y < float64(r.y+r.h)
}

// shapes are painted white under the even-odd rule: a finder is its outer
// square, the hole inside it and the dot inside that.
func shapes() []rect {
	var rs []rect
	for _, p := range [][2]int{{4, 4}, {18, 4}, {4, 18}} {
		x, y := p[0], p[1]
		rs = append(rs, rect{x, y, 10, 10}, rect{x + 2, y + 2, 6, 6}, rect{x + 4, y + 4, 2, 2})
	}
	data := [5][]int{{0, 2, 4}, {1, 3}, {0, 1, 4}, {2, 3}, {0, 2, 4}}
	for row, cols := range data {
		for _, col := range cols {
			rs = append(rs, rect{18 + 2*col, 18 + 2*row, 2, 2})
		}
	}
	return rs
}

func svg(rs []rect) []byte {
	var d strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&d, "M%d %dh%dv%dh-%dz", r.x, r.y, r.w, r.h, r.w)
	}
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d">`+
		`<rect width="%d" height="%d" rx="%d" fill="#%02x%02x%02x"/>`+
		`<path fill="#fff" fill-rule="evenodd" d="%s"/></svg>`+"\n",
		units, units, units, units, radius, tile.R, tile.G, tile.B, d.String())
}

func insideTile(x, y float64) bool {
	dx := max(radius-x, 0, x-(units-radius))
	dy := max(radius-y, 0, y-(units-radius))
	return dx*dx+dy*dy <= radius*radius
}

// raster renders the icon at dim×dim pixels with 8×8 supersampling.
func raster(rs []rect, dim int) *image.NRGBA {
	const ss = 8
	img := image.NewNRGBA(image.Rect(0, 0, dim, dim))
	scale := float64(units) / float64(dim)
	for py := range dim {
		for px := range dim {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					if !insideTile(x, y) {
						continue
					}
					c := tile
					n := 0
					for _, s := range rs {
						if s.contains(x, y) {
							n++
						}
					}
					if n%2 == 1 {
						c = light
					}
					r, g, b, a = r+float64(c.R), g+float64(c.G), b+float64(c.B), a+1
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(r/a + 0.5), G: uint8(g/a + 0.5), B: uint8(b/a + 0.5),
				A: uint8(a/(ss*ss)*255 + 0.5),
			})
		}
	}
	return img
}

// ico packs PNG images into an ICO container, which every browser accepts.
func ico(sizes []int, rs []rect) ([]byte, error) {
	pngs := make([][]byte, len(sizes))
	for i, dim := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, raster(rs, dim)); err != nil {
			return nil, err
		}
		pngs[i] = buf.Bytes()
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(pngs))}) // reserved, type 1 = icon, count
	offset := 6 + 16*len(pngs)
	for i, p := range pngs {
		out.Write([]byte{byte(sizes[i]), byte(sizes[i]), 0, 0}) // width, height, palette size, reserved
		_ = binary.Write(&out, le, [2]uint16{1, 32})            // colour planes, bits per pixel
		_ = binary.Write(&out, le, [2]uint32{uint32(len(p)), uint32(offset)})
		offset += len(p)
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes(), nil
}

func main() {
	rs := shapes()
	icoBytes, err := ico([]int{16, 32}, rs)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("ui/favicon.svg", svg(rs), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("ui/favicon.ico", icoBytes, 0o644); err != nil {
		log.Fatal(err)
	}
}
