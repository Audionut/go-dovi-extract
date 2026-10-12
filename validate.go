package dovi

import "slices"

func validateRecord(r *RPU) bool {
	h := r.Header
	profile := uint8(0)
	if h.Profile == 0 && h.FullRange {
		profile = 5
	} else if h.Profile == 1 {
		profile = 8
		if h.ELSpatialFilter && !h.DisableResidual {
			if h.VDRBitDepth != 12 {
				return false
			}
			profile = 7
		}
	}
	if profile == 0 || r.Profile != profile || !h.SequenceInfo || h.Format&0x700 != 0 || h.Level != 0 || h.BLBitDepth != 10 || h.ELBitDepth != 10 || h.VDRBitDepth < 8 || h.VDRBitDepth > 14 || h.CoefficientType > 1 || h.CoefficientDenominator > 23 || h.NormalizedIDC > 3 || h.Reserved > 1 || h.PreviousID > 15 || r.MappingID > 15 {
		return false
	}
	if r.EnhancementLayer != "" && (profile != 7 || h.ReuseMapping) {
		return false
	}
	if r.DM == nil {
		return !h.DMPresent
	}
	d := r.DM
	if !h.DMPresent || d.Compressed != (h.Reserved == 1) || d.Compressed != (d.Base == nil) || d.AffectedID > 15 || !d.CM29 {
		return false
	}
	if x := d.Base; x != nil {
		if x.BitDepth < 8 || x.BitDepth > 16 || x.ColorSpace > 3 || x.ChromaFormat > 3 || x.FullRange > 3 || x.SourceMinPQ > 4095 || x.SourceMaxPQ > 4095 || x.SourceDiagonal > 1023 {
			return false
		}
		if x.EOTFParam0 == 0 && x.EOTFParam1 == 0 && x.EOTFParam2 == 0 && x.EOTF != 65535 {
			return false
		}
	}
	var counts [256]int
	var l1 *L1
	var l5 *L5
	var l6 *L6
	for _, block := range d.Blocks {
		if !validBlock(block) {
			return false
		}
		level := block.Level
		counts[level]++
		limit := 1
		switch level {
		case 2:
			limit = 8
		case 8:
			limit = 5
		case 10:
			limit = 4
		case 253:
			limit = 256
		}
		if counts[level] > limit {
			return false
		}
		if !slices.Contains([]uint8{1, 2, 4, 5, 6, 255}, level) && !d.CM40 {
			return false
		}
		v := block.Values
		switch level {
		case 1:
			l1 = &L1{uint16(v[0]), uint16(v[1]), uint16(v[2])}
		case 5:
			l5 = &L5{uint16(v[0]), uint16(v[1]), uint16(v[2]), uint16(v[3])}
		case 6:
			l6 = &L6{uint16(v[0]), uint16(v[1]), uint16(v[2]), uint16(v[3])}
		}
	}
	if d.CM40 && counts[254] != 1 {
		return false
	}
	return sameOptional(l1, d.L1) && sameOptional(l5, d.L5) && sameOptional(l6, d.L6)
}
func sameOptional[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func validBlock(b Block) bool {
	v := b.Values
	n := len(v)
	length := int(b.Length)
	for _, x := range v {
		if x < 0 || x > 65535 {
			if b.Level != 2 || x != -1 {
				return false
			}
		}
	}
	all := func(values []int64, max int64) bool {
		for _, x := range values {
			if x < 0 || x > max {
				return false
			}
		}
		return true
	}
	switch b.Level {
	case 1, 3:
		return length == 5 && n == 3 && all(v, 4095)
	case 2:
		return length == 11 && n == 7 && all(v[:6], 4095) && v[6] >= -1 && v[6] <= 4095
	case 4:
		return length == 3 && n == 2 && all(v, 4095)
	case 5:
		return length == 7 && n == 4 && all(v, 8191)
	case 6:
		return length == 8 && n == 4 && all(v, 10000)
	case 8:
		want := map[int]int{10: 7, 12: 8, 13: 9, 19: 15, 25: 21}[length]
		if want == 0 || n != want || v[0] > 255 || !all(v[1:min(9, n)], 4095) {
			return false
		}
		return n <= 9 || all(v[9:], 255)
	case 9, 10:
		base, fields := 1, 1
		if b.Level == 10 {
			base, fields = 5, 4
		}
		if (length != base && length != base+16) || n != fields+(length-base)/2 || !all(v[:fields], 4095) || v[0] > 255 {
			return false
		}
		primary := v[fields-1]
		if primary > 255 || (primary == 255) != (length > base) {
			return false
		}
		if b.Level == 10 && slices.Contains([]int64{1, 16, 18, 21, 27, 28, 37, 38, 42, 48, 49}, v[0]) {
			return false
		}
		for _, x := range v[fields:] {
			if x == 0 {
				return false
			}
		}
		return true
	case 11:
		return length == 4 && n == 4 && all(v, 255) && v[0] <= 15 && v[3] == 0
	case 253:
		return length <= 66 && n == 0
	case 254:
		return length == 2 && n == 2 && all(v, 255)
	case 255:
		return length == 6 && n == 6 && all(v, 255)
	}
	return false
}
