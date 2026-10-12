// PES header preflight adapted from the MIT-licensed go-hdr10-plus extractor.
package extract

import (
	"bytes"
	"encoding/binary"
)

func validPESTimestamp(b []byte, prefix byte) bool {
	return len(b) == 5 && b[0]>>4 == prefix && b[0]&1 == 1 && b[2]&1 == 1 && b[4]&1 == 1
}

func validatePESOptional(data []byte) error {
	b := bitReader{data: data[9:]}
	flags := data[7]
	if flags>>6 == 2 {
		b.skip(40)
	} else if flags>>6 == 3 {
		b.skip(80)
	}
	if flags&0x20 != 0 {
		b.expect(2, 3)
		for _, width := range []uint{3, 15, 15} {
			b.skip(width)
			b.expect(1, 1)
		}
		if b.read(9) >= 300 {
			b.invalid()
		}
		b.expect(1, 1)
	}
	if flags&0x10 != 0 {
		b.expect(1, 1)
		if b.read(22) == 0 {
			b.invalid()
		}
		b.expect(1, 1)
	}
	if flags&8 != 0 {
		b.skip(8)
		if b.err == nil {
			return fieldError("DSM-trick-mode", ErrUnsupportedInput)
		}
	}
	if flags&4 != 0 {
		b.expect(1, 1)
		b.skip(7)
	}
	if flags&2 != 0 {
		b.skip(16) // A preceding-packet CRC remains a bounded diagnostic field.
	}
	if flags&1 != 0 {
		ext := b.read(8)
		if ext&14 != 14 {
			b.invalid()
		}
		if ext&0x80 != 0 {
			b.skip(128)
		}
		if ext&0x40 != 0 {
			size := int(b.read(8))
			start := b.pos / 8
			b.skip(uint(size) * 8)
			if b.err == nil {
				pack := bitReader{data: b.data[start : start+size]}
				pack.expect(32, 0x000001ba)
				if pack.err == nil && len(pack.data) >= 12 && pack.data[4]>>4 == 2 {
					return fieldError("MPEG1-pack-header", ErrUnsupportedInput)
				}
				pack.expect(2, 1)
				for _, width := range []uint{3, 15, 15} {
					pack.skip(width)
					pack.expect(1, 1)
				}
				if pack.read(9) >= 300 {
					pack.invalid()
				}
				pack.expect(1, 1)
				if pack.read(22) == 0 {
					pack.invalid()
				}
				pack.expect(2, 3)
				pack.expect(5, 31)
				stuffing := pack.read(3)
				for range stuffing {
					pack.expect(8, 255)
				}
				if pack.err == nil && pack.pos < len(pack.data)*8 {
					// Embedded system headers have independent program constraints.
					if len(pack.data)-pack.pos/8 >= 4 && binary.BigEndian.Uint32(pack.data[pack.pos/8:]) == 0x000001bb {
						return fieldError("pack-system-header", ErrUnsupportedInput)
					}
					pack.invalid()
				}
				if err := syntaxError(&pack, "PES-pack-header"); err != nil {
					return err
				}
			}
		}
		if ext&0x20 != 0 {
			for range 2 {
				b.expect(1, 1)
				b.skip(7)
			}
		}
		if ext&0x10 != 0 {
			b.expect(2, 1)
			b.skip(14)
		}
		if ext&1 != 0 {
			b.expect(1, 1)
			size := int(b.read(7))
			start := b.pos / 8
			b.skip(uint(size) * 8)
			if b.err == nil {
				extra := bitReader{data: b.data[start : start+size]}
				if extra.flag() {
					extra.expect(6, 63)
					if !extra.flag() {
						extra.expect(4, 15)
						for _, width := range []uint{3, 15, 15} {
							extra.skip(width)
							extra.expect(1, 1)
						}
					}
				} else {
					extra.skip(7)
					if data[3] != 0xfd {
						extra.invalid()
					}
				}
				for extra.err == nil && extra.pos < len(extra.data)*8 {
					extra.expect(8, 255)
				}
				if err := syntaxError(&extra, "PES-extension2"); err != nil {
					return err
				}
			}
		}
	}
	if len(b.data)-b.pos/8 > 32 {
		b.invalid()
	}
	for b.err == nil && b.pos < len(b.data)*8 {
		b.expect(8, 255)
	}
	return syntaxError(&b, "PES-optional-header")
}

// Validate the complete original bounded PES header before either producer
// discards it. Marker headers are borrowed and never retained by the collector.
func validatePESHeader(data []byte) error {
	if len(data) < 9 || len(data) != 9+int(data[8]) || !bytes.Equal(data[:3], []byte{0, 0, 1}) || data[6]&0xc0 != 0x80 {
		return fieldError("PES-fixed-header", ErrInvalidBitstream)
	}
	if (data[3] < 0xe0 || data[3] > 0xef) && data[3] != 0xfd || data[6]&0x30 != 0 {
		return fieldError("PES-stream-ID/scrambling", ErrUnsupportedInput)
	}
	flags := data[7] >> 6
	if flags == 1 || flags == 2 && (len(data) < 14 || !validPESTimestamp(data[9:14], 2)) || flags == 3 && (len(data) < 19 || !validPESTimestamp(data[9:14], 3) || !validPESTimestamp(data[14:19], 1)) {
		return fieldError("PES-timestamp", ErrInvalidBitstream)
	}
	length := int(binary.BigEndian.Uint16(data[4:6]))
	if length != 0 && length < len(data)-6 {
		return fieldError("PES-declared-length", ErrInvalidBitstream)
	}
	return validatePESOptional(data)
}
