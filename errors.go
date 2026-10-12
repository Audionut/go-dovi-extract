// Package dovi reads Dolby Vision metadata without decoding video pictures.
package dovi

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidBitstream = errors.New("invalid Dolby Vision bitstream")
	ErrUnsupported      = errors.New("unsupported syntax or input")
	ErrIncomplete       = errors.New("incomplete input")
	ErrLimit            = errors.New("resource limit exceeded")
	ErrNoL1             = errors.New("no L1 metadata")
	ErrInvalidMetadata  = errors.New("invalid caller metadata")
	ErrInvalidOptions   = errors.New("invalid options")
)

// ParseError supplies a field and zero-based bit offset in the unescaped RPU.
// OffsetKnown is false for errors without a single syntactic location.
type ParseError struct {
	Field       string
	BitOffset   int
	OffsetKnown bool
	Err         error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s at bit %d (known=%t): %v", e.Field, e.BitOffset, e.OffsetKnown, e.Err)
}
func (e *ParseError) Unwrap() error { return e.Err }
