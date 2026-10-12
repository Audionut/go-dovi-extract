package extract

import (
	"errors"
	"fmt"
	dovi "github.com/Audionut/go-dovi-extract"
)

// Extraction errors have stable errors.Is identities, not stable text.
var (
	ErrRawUnavailable   = errors.New("raw RPU bytes unavailable")
	ErrUnsupportedInput = dovi.ErrUnsupported
	ErrInvalidBitstream = dovi.ErrInvalidBitstream
	ErrIncomplete       = dovi.ErrIncomplete
	ErrResourceLimit    = dovi.ErrLimit
	ErrAmbiguousTrack   = errors.New("ambiguous HEVC track")
)

// Error identifies the field or feature that failed. Offset and AU are zero-based
// and meaningful only when their corresponding Known flag is true. Stream is
// the caller's source identity; Container is empty for standalone RPU decoding.
type Error struct {
	Container   string
	Stream      StreamKey
	Offset      uint64
	OffsetKnown bool
	AU          uint64
	AUKnown     bool
	Field       string
	Err         error
}

func (e *Error) Error() string {
	return fmt.Sprintf("Dolby Vision %s (offset=%d known=%t AU=%d known=%t): %v", e.Field, e.Offset, e.OffsetKnown, e.AU, e.AUKnown, e.Err)
}
func (e *Error) Unwrap() error { return e.Err }

func fieldError(field string, cause error) error { return &Error{Field: field, Err: cause} }
