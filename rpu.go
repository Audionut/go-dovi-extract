package dovi

import (
	"encoding/binary"
	"fmt"
	"slices"
)

// ParseOptions selects finite per-record limits. Zero selects the defaults.
type ParseOptions struct{ MaxBytes, MaxBlocks int }

func (o ParseOptions) limits() (int, int, error) {
	if o.MaxBytes < 0 || o.MaxBlocks < 0 {
		return 0, 0, ErrInvalidOptions
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 1 << 20
	}
	if o.MaxBlocks == 0 {
		o.MaxBlocks = 256
	}
	return o.MaxBytes, o.MaxBlocks, nil
}

// ParseNAL reads an escaped type-62 NAL, including its two-byte HEVC header,
// without a start code. Caller memory is borrowed only for this call.
func ParseNAL(nal []byte, opts ParseOptions) (RPU, error) {
	maxBytes, _, err := opts.limits()
	if err != nil {
		return RPU{}, err
	}
	if len(nal) < 3 {
		return RPU{}, ErrIncomplete
	}
	if nal[0]&0x80 != 0 || (nal[0]>>1)&63 != 62 || nal[1]&7 == 0 {
		return RPU{}, fmt.Errorf("NAL header: %w", ErrInvalidBitstream)
	}
	if nal[0]&1 != 0 || nal[1]>>3 != 0 || nal[1]&7 != 1 {
		return RPU{}, fmt.Errorf("RPU layer/temporal ID: %w", ErrUnsupported)
	}
	if len(nal)-2 > maxBytes {
		return RPU{}, fmt.Errorf("RPU bytes limit %d: %w", maxBytes, ErrLimit)
	}
	rbsp, err := unescape(nal[2:])
	if err != nil {
		return RPU{}, err
	}
	return ParseRPU(rbsp, opts)
}

func unescape(src []byte) ([]byte, error) {
	dst := make([]byte, 0, len(src))
	zeros := 0
	for i := 0; i < len(src); i++ {
		v := src[i]
		if zeros >= 2 {
			if v == 3 {
				if i+1 == len(src) || src[i+1] > 3 {
					return nil, fmt.Errorf("emulation prevention: %w", ErrInvalidBitstream)
				}
				zeros = 0
				continue
			}
			if v < 3 {
				return nil, fmt.Errorf("missing emulation prevention: %w", ErrInvalidBitstream)
			}
		}
		dst = append(dst, v)
		if v == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return dst, nil
}

func crcMPEG2(data []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, v := range data {
		crc ^= uint32(v) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// ParseRPU reads one unescaped payload beginning with 0x19 and ending with
// CRC-32/MPEG-2 and 0x80. Permitted trailing zero bytes are discarded.
func ParseRPU(data []byte, opts ParseOptions) (RPU, error) {
	maxBytes, maxBlocks, err := opts.limits()
	if err != nil {
		return RPU{}, err
	}
	if len(data) > maxBytes {
		return RPU{}, fmt.Errorf("RPU bytes limit %d: %w", maxBytes, ErrLimit)
	}
	for len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	if len(data) < 7 {
		return RPU{}, ErrIncomplete
	}
	if data[0] != 0x19 || data[len(data)-1] != 0x80 {
		return RPU{}, fmt.Errorf("prefix/terminator: %w", ErrInvalidBitstream)
	}
	end := len(data) - 5
	crc := binary.BigEndian.Uint32(data[end : end+4])
	if crcMPEG2(data[1:end]) != crc {
		return RPU{}, fmt.Errorf("CRC: %w", ErrInvalidBitstream)
	}
	b := &bitReader{data: data[1:end]}
	h := parseHeader(b)
	r := RPU{Header: h, CRC: crc}
	switch {
	case h.Profile == 0 && h.FullRange:
		r.Profile = 5
	case h.Profile == 1:
		r.Profile = 8
		if h.ELSpatialFilter && !h.DisableResidual {
			if h.VDRBitDepth == 12 {
				r.Profile = 7
			} else {
				b.fail(ErrUnsupported, "profile 4")
			}
		}
	default:
		b.fail(ErrUnsupported, "profile")
	}
	if !h.ReuseMapping && b.err == nil {
		parseMapping(b, &r)
	}
	if h.DMPresent && b.err == nil {
		r.DM = parseDM(b, h, maxBlocks)
	}
	b.align()
	if b.err != nil {
		return RPU{}, b.err
	}
	r.Remaining = slices.Clone(b.data[b.pos/8:])
	return r, nil
}

func parseHeader(b *bitReader) Header {
	b.require(b.read(6) == 2, "RPU type")
	h := Header{Format: uint16(b.read(11)), Profile: uint8(b.read(4)), Level: uint8(b.read(4)), SequenceInfo: b.flag()}
	if !h.SequenceInfo {
		b.fail(ErrUnsupported, "absent sequence info")
		return h
	}
	h.ChromaFilter = b.flag()
	h.CoefficientType = uint8(b.read(2))
	if h.CoefficientType == 0 {
		h.CoefficientDenominator = uint8(b.boundedUE(23, "coefficient denominator"))
	} else if h.CoefficientType != 1 {
		b.fail(ErrUnsupported, "coefficient type")
	}
	h.NormalizedIDC = uint8(b.read(2))
	h.FullRange = b.flag()
	if h.Format&0x700 != 0 {
		b.fail(ErrUnsupported, "RPU format")
		return h
	}
	h.BLBitDepth = uint8(b.boundedUE(8, "BL bit depth")) + 8
	el := b.boundedUE(65535, "EL bit depth and mapping")
	h.ELBitDepth = uint8(el&255) + 8
	h.ExtendedMapping = uint8(el >> 8)
	h.VDRBitDepth = uint8(b.boundedUE(6, "VDR bit depth")) + 8
	h.SpatialFilter = b.flag()
	h.Reserved = uint8(b.read(3))
	h.ELSpatialFilter = b.flag()
	h.DisableResidual = b.flag()
	h.DMPresent = b.flag()
	h.ReuseMapping = b.flag()
	if h.ReuseMapping {
		h.PreviousID = b.boundedUE(15, "previous mapping ID")
	}
	b.require(h.Level == 0 && h.BLBitDepth == 10 && (el&255) == 2, "header depths/level")
	if h.Reserved > 1 {
		b.fail(ErrUnsupported, "reserved header flags")
	}
	return h
}

func parseMapping(b *bitReader, r *RPU) {
	h := r.Header
	width := int(h.CoefficientDenominator)
	if h.CoefficientType == 1 {
		width = 32
	}
	r.MappingID = b.boundedUE(15, "mapping ID")
	b.require(b.ue() == 0, "mapping color space")
	b.require(b.ue() == 0, "mapping chroma format")
	var pieces [3]int
	for c := range 3 {
		n := b.boundedUE(7, "pivot count")
		pieces[c] = int(n) + 1
		for range n + 2 {
			b.read(int(h.BLBitDepth))
		}
	}
	nlq := !h.DisableResidual
	if nlq {
		b.require(r.Profile == 7, "profile residual")
		b.require(b.read(3) == 0, "NLQ method")
		a := b.read(int(h.BLBitDepth))
		z := b.read(int(h.BLBitDepth))
		b.require(a+z == 1023, "NLQ pivots")
	}
	b.ue()
	b.ue() // Partition counts do not drive coefficient traversal.
	coefficient := func() {
		if h.CoefficientType == 0 {
			b.ue()
		}
		b.read(width)
	}
	for _, n := range pieces {
		for range n {
			if b.err != nil {
				return
			}
			switch b.ue() {
			case 0:
				order := b.boundedUE(1, "polynomial order")
				if order == 0 && b.flag() {
					b.fail(ErrUnsupported, "polynomial interpolation")
					return
				}
				for range order + 2 {
					coefficient()
				}
			case 1:
				order := b.read(2)
				b.require(order <= 2, "MMR order")
				coefficient()
				for range (order + 1) * 7 {
					coefficient()
				}
			default:
				b.fail(ErrUnsupported, "mapping method")
			}
		}
	}
	if nlq {
		mel := true
		for range 3 {
			if b.read(int(h.ELBitDepth)) != 0 {
				mel = false
			}
			for j := range 3 {
				var integer uint64
				if h.CoefficientType == 0 {
					integer = b.ue()
				}
				fraction := b.read(width)
				want := uint64(0)
				if j == 0 {
					want = 1
				}
				if integer != want || fraction != 0 {
					mel = false
				}
			}
		}
		r.EnhancementLayer = "FEL"
		if mel {
			r.EnhancementLayer = "MEL"
		}
	}
}

func parseDM(b *bitReader, h Header, maxBlocks int) *DM {
	d := &DM{Compressed: h.Reserved == 1, AffectedID: b.boundedUE(15, "affected DM ID"), CurrentID: b.ue(), SceneRefresh: b.ue()}
	if !d.Compressed {
		x := &BaseDM{}
		for i := range x.YCCToRGB {
			x.YCCToRGB[i] = int16(b.read(16))
		}
		for i := range x.YCCOffsets {
			x.YCCOffsets[i] = uint32(b.read(32))
		}
		for i := range x.RGBToLMS {
			x.RGBToLMS[i] = int16(b.read(16))
		}
		x.EOTF = uint16(b.read(16))
		x.EOTFParam0 = uint16(b.read(16))
		x.EOTFParam1 = uint16(b.read(16))
		x.EOTFParam2 = uint32(b.read(32))
		x.BitDepth = uint8(b.read(5))
		x.ColorSpace = uint8(b.read(2))
		x.ChromaFormat = uint8(b.read(2))
		x.FullRange = uint8(b.read(2))
		x.SourceMinPQ = uint16(b.read(12))
		x.SourceMaxPQ = uint16(b.read(12))
		x.SourceDiagonal = uint16(b.read(10))
		b.require(x.BitDepth >= 8 && x.BitDepth <= 16, "signal bit depth")
		b.require(x.EOTFParam0 != 0 || x.EOTFParam1 != 0 || x.EOTFParam2 != 0 || x.EOTF == 65535, "signal EOTF")
		d.Base = x
	}
	d.CM29 = true
	parseBlocks(b, d, false, maxBlocks)
	// Upstream requires 16 bits before the reserved CRC/terminator for CM4.
	if b.err == nil && len(b.data)*8-b.pos >= 16 {
		d.CM40 = true
		parseBlocks(b, d, true, maxBlocks)
	}
	return d
}
