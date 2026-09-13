package qr

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

// byteModeCapacity is taken from ISO/IEC 18004:2015 Table 7: how many bytes
// fit in byte mode at levels L, M, Q, H. It is written out independently of
// the encoder so the two can check each other.
var byteModeCapacity = []struct {
	version int
	caps    [4]int // indexed by ECLevel
}{
	{1, [4]int{17, 14, 11, 7}},
	{2, [4]int{32, 26, 20, 14}},
	{5, [4]int{106, 84, 60, 44}},
	{10, [4]int{271, 213, 151, 119}},
	{20, [4]int{858, 666, 482, 382}},
	{39, [4]int{2809, 2213, 1579, 1219}},
	{40, [4]int{2953, 2331, 1663, 1273}},
}

func symbolSize(version int) int { return 17 + 4*version }

// TestVersionBoundaries checks that a payload exactly at a version's capacity
// lands in that version and that one more byte moves it to the next — or,
// past version 40, fails with ErrCapacityExceeded. It also proves the level
// is never silently boosted, since boosting would change these boundaries.
func TestVersionBoundaries(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()
	for _, row := range byteModeCapacity {
		for _, ec := range ECLevels() {
			n := row.caps[ec]
			t.Run(fmt.Sprintf("v%d/%s", row.version, ec), func(t *testing.T) {
				t.Parallel()
				m, err := enc.Encode([]byte(payload(n)), ec)
				if err != nil {
					t.Fatalf("Encode(%d bytes): %v", n, err)
				}
				if got, want := m.Size(), symbolSize(row.version); got != want {
					t.Errorf("%d bytes: size %d, want %d (version %d)", n, got, want, row.version)
				}

				m, err = enc.Encode([]byte(payload(n+1)), ec)
				if row.version == 40 {
					if !errors.Is(err, ErrCapacityExceeded) {
						t.Fatalf("%d bytes: err = %v, want ErrCapacityExceeded", n+1, err)
					}
					if MaxPayloadBytes(ec) != n {
						t.Errorf("MaxPayloadBytes(%s) = %d, want %d", ec, MaxPayloadBytes(ec), n)
					}
					return
				}
				if err != nil {
					t.Fatalf("Encode(%d bytes): %v", n+1, err)
				}
				if got, want := m.Size(), symbolSize(row.version+1); got != want {
					t.Errorf("%d bytes: size %d, want %d (version %d)", n+1, got, want, row.version+1)
				}
			})
		}
	}
}

// TestRoundTrip is the test that matters: encode, render to SVG, rasterize
// the SVG, and decode it with an independent decoder. The decoded payload and
// EC level must match exactly.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	enc := NewEncoder()

	special := []string{
		"http://a.io",
		"https://example.com/",
		"HTTPS://EXAMPLE.COM/UPPER/CASE",
		"https://example.com/search?q=qr+code&lang=en#results",
		"https://user:pass@example.com:8443/a/b;c?d=%20e&f=%2F",
		"https://example.com/?q=<script>alert(1)</script>&x=\"'",
		"https://例え.jp/パス?キー=値",
		"https://example.com/emoji/🙂",
	}

	for _, ec := range ECLevels() {
		var lengths []int
		for _, n := range []int{1, 7, 20, 64, 100, 255, 256, 500, 1000, 1273, 1663, 2048, 2331, 2953} {
			if n <= MaxPayloadBytes(ec) {
				lengths = append(lengths, n)
			}
		}
		// Both sides of every version transition in the capacity table.
		for _, row := range byteModeCapacity {
			n := row.caps[ec]
			lengths = append(lengths, n)
			if n+1 <= MaxPayloadBytes(ec) {
				lengths = append(lengths, n+1)
			}
		}
		slices.Sort(lengths)
		lengths = slices.Compact(lengths)

		inputs := slices.Clone(special)
		for _, n := range lengths {
			inputs = append(inputs, payload(n))
		}

		for _, in := range inputs {
			t.Run(fmt.Sprintf("%s/%d", ec, len(in)), func(t *testing.T) {
				t.Parallel()
				m, err := enc.Encode([]byte(in), ec)
				if err != nil {
					t.Fatalf("Encode: %v", err)
				}
				svg, err := RenderSVG(m, defaultStyle)
				if err != nil {
					t.Fatalf("RenderSVG: %v", err)
				}
				p := parseSVG(t, svg)
				assertGridMatchesMatrix(t, p.grid, m, defaultStyle.Margin)

				text, gotEC := decode(t, toGray(p.grid, 3))
				if text != in {
					t.Fatalf("decoded payload differs\n got: %q\nwant: %q", text, in)
				}
				if gotEC != ec.String() {
					t.Fatalf("decoded EC level %s, want %s", gotEC, ec)
				}
			})
		}
	}
}

func TestEncodeRejectsInvalidLevel(t *testing.T) {
	t.Parallel()
	if _, err := NewEncoder().Encode([]byte("https://example.com/"), ECLevel(9)); err == nil {
		t.Fatal("expected an error for an invalid EC level")
	}
	if MaxPayloadBytes(ECLevel(9)) != 0 {
		t.Fatal("MaxPayloadBytes should be 0 for an invalid level")
	}
}
