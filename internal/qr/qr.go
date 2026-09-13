// Package qr turns payloads into QR code module matrices and renders those
// matrices as SVG.
//
// Encoding sits behind the Encoder interface so the underlying library can be
// swapped without touching callers. Rendering is done here, not by the
// library, so quiet zone handling, path construction and byte-level
// determinism are under our control.
package qr

import (
	"errors"
	"strings"
)

// ECLevel is a QR code error correction level.
type ECLevel uint8

// Error correction levels, in increasing order of redundancy.
const (
	ECLow      ECLevel = iota // L: ~7% of codewords recoverable
	ECMedium                  // M: ~15%
	ECQuartile                // Q: ~25%
	ECHigh                    // H: ~30%
)

// ECLevels returns every level, from lowest to highest redundancy.
func ECLevels() []ECLevel {
	return []ECLevel{ECLow, ECMedium, ECQuartile, ECHigh}
}

// String returns the single-letter name of the level: L, M, Q or H.
func (l ECLevel) String() string {
	switch l {
	case ECLow:
		return "L"
	case ECMedium:
		return "M"
	case ECQuartile:
		return "Q"
	case ECHigh:
		return "H"
	}
	return "invalid"
}

// Valid reports whether l is one of the four defined levels.
func (l ECLevel) Valid() bool {
	return l <= ECHigh
}

// ParseECLevel parses "L", "M", "Q" or "H", ignoring case.
func ParseECLevel(s string) (ECLevel, bool) {
	switch strings.ToUpper(s) {
	case "L":
		return ECLow, true
	case "M":
		return ECMedium, true
	case "Q":
		return ECQuartile, true
	case "H":
		return ECHigh, true
	}
	return 0, false
}

// ErrCapacityExceeded reports that a payload does not fit in a version 40
// symbol at the requested error correction level.
var ErrCapacityExceeded = errors.New("qr: payload exceeds version 40 capacity")

// byteModeCapacityV40 is how many 8-bit bytes a version 40 symbol holds in
// byte mode at each level (ISO/IEC 18004:2015, Table 7).
var byteModeCapacityV40 = [...]int{
	ECLow:      2953,
	ECMedium:   2331,
	ECQuartile: 1663,
	ECHigh:     1273,
}

// MaxPayloadBytes returns the largest payload, in bytes, that fits in a
// version 40 symbol at level l. Encoders always use byte mode, so this bound
// is exact rather than an estimate. It returns 0 for an invalid level.
func MaxPayloadBytes(l ECLevel) int {
	if !l.Valid() {
		return 0
	}
	return byteModeCapacityV40[l]
}

// Matrix is an immutable square grid of QR modules. It covers the symbol
// only; the quiet zone is added at render time.
type Matrix struct {
	size    int
	modules []bool // row-major; true is dark
}

// NewMatrix builds a size×size matrix, calling dark once per module.
func NewMatrix(size int, dark func(x, y int) bool) *Matrix {
	m := &Matrix{size: size, modules: make([]bool, size*size)}
	for y := range size {
		for x := range size {
			m.modules[y*size+x] = dark(x, y)
		}
	}
	return m
}

// Size returns the side length of the symbol, in modules.
func (m *Matrix) Size() int {
	return m.size
}

// Dark reports whether the module at (x, y) is dark. Coordinates outside the
// symbol are light.
func (m *Matrix) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return false
	}
	return m.modules[y*m.size+x]
}

// Encoder encodes a payload into a module matrix.
//
// Implementations must be deterministic, must encode the payload as a single
// byte-mode segment (so MaxPayloadBytes is exact), must use exactly the
// requested error correction level rather than boosting it, and must return
// an error wrapping ErrCapacityExceeded when the payload does not fit.
type Encoder interface {
	Encode(payload []byte, ec ECLevel) (*Matrix, error)
}
