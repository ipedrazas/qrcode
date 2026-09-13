package qr

import (
	"errors"
	"fmt"

	goqr "github.com/piglig/go-qr"
)

// NewEncoder returns the default Encoder, backed by github.com/piglig/go-qr.
func NewEncoder() Encoder {
	return goQREncoder{}
}

type goQREncoder struct{}

func (goQREncoder) Encode(payload []byte, ec ECLevel) (*Matrix, error) {
	ecc, err := goqrECC(ec)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		payload = []byte{}
	}
	seg, err := goqr.MakeBytes(payload)
	if err != nil {
		return nil, fmt.Errorf("qr: %w", err)
	}
	// boostEcl=false pins the requested level. The library still picks the
	// smallest version that fits and the lowest-penalty mask, both of which
	// are deterministic.
	code, err := goqr.EncodeSegments([]*goqr.QrSegment{seg}, ecc, goqr.MinVersion, goqr.MaxVersion, -1, false)
	if errors.Is(err, goqr.ErrDataTooLong) {
		return nil, fmt.Errorf("%w: %d bytes at level %s", ErrCapacityExceeded, len(payload), ec)
	}
	if err != nil {
		return nil, fmt.Errorf("qr: %w", err)
	}
	return NewMatrix(code.Size(), code.Module), nil
}

func goqrECC(ec ECLevel) (goqr.Ecc, error) {
	switch ec {
	case ECLow:
		return goqr.Low, nil
	case ECMedium:
		return goqr.Medium, nil
	case ECQuartile:
		return goqr.Quartile, nil
	case ECHigh:
		return goqr.High, nil
	}
	return 0, fmt.Errorf("qr: invalid error correction level %d", ec)
}
