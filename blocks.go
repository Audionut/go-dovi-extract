package dovi

import "slices"

func parseBlocks(b *bitReader, d *DM, cm40 bool, maxBlocks int) {
	n := b.ue()
	if n > uint64(maxBlocks-len(d.Blocks)) {
		b.fail(ErrLimit, "extension blocks")
		return
	}
	b.align()
	var counts [256]int
	for range n {
		if b.err != nil {
			return
		}
		length := b.ue()
		level := uint8(b.read(8))
		allowed := slices.Contains([]uint8{1, 2, 4, 5, 6, 255}, level)
		if cm40 {
			allowed = slices.Contains([]uint8{3, 8, 9, 10, 11, 253, 254}, level)
		}
		if !allowed {
			b.fail(ErrUnsupported, "extension level/container")
			return
		}
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
			limit = maxBlocks
		}
		b.require(counts[level] <= limit, "extension cardinality")
		if length > uint64((len(b.data)*8-b.pos)/8) {
			b.fail(ErrIncomplete, "extension length")
			return
		}
		start := b.pos
		block := Block{Level: level, Length: uint16(length)}
		read := func(width, count int) {
			for range count {
				block.Values = append(block.Values, int64(b.read(width)))
			}
		}
		switch level {
		case 1, 3:
			b.require(length == 5, "L1/L3 length")
			read(12, 3)
		case 2:
			b.require(length == 11, "L2 length")
			read(12, 6)
			v := int64(b.read(13))
			if v&4096 != 0 {
				v -= 8192
			}
			b.require(v >= -1, "L2 ms weight")
			block.Values = append(block.Values, v)
		case 4:
			b.require(length == 3, "L4 length")
			read(12, 2)
		case 5:
			b.require(length == 7, "L5 length")
			read(13, 4)
		case 6:
			b.require(length == 8, "L6 length")
			read(16, 4)
			for _, v := range block.Values {
				b.require(v <= 10000, "L6 luminance")
			}
		case 8:
			b.require(slices.Contains([]uint64{10, 12, 13, 19, 25}, length), "L8 length")
			read(8, 1)
			read(12, 6)
			if length >= 12 {
				read(12, 1)
			}
			if length >= 13 {
				read(12, 1)
			}
			if length >= 19 {
				read(8, 6)
			}
			if length >= 25 {
				read(8, 6)
			}
		case 9, 10:
			base := uint64(1)
			if level == 10 {
				base = 5
			}
			b.require(length == base || length == base+16, "display block length")
			read(8, 1)
			if level == 10 {
				b.require(!slices.Contains([]int64{1, 16, 18, 21, 27, 28, 37, 38, 42, 48, 49}, block.Values[0]), "preset L10 target")
				read(12, 2)
				read(8, 1)
			}
			primary := block.Values[len(block.Values)-1]
			b.require((primary == 255) == (length > base), "custom primaries")
			if length > base {
				read(16, 8)
				for _, v := range block.Values[len(block.Values)-8:] {
					b.require(v > 0, "primary coordinate")
				}
			}
		case 11:
			b.require(length == 4, "L11 length")
			read(8, 4)
			b.require(block.Values[0] <= 15 && block.Values[3] == 0, "L11 fields")
		case 253:
			b.require(length <= 66, "L253 length")
			if b.err != nil {
				return
			}
			for range length {
				b.require(b.read(8) == 0x55, "L253 filler")
			}
		case 254:
			b.require(length == 2, "L254 length")
			read(8, 2)
		case 255:
			b.require(length == 6, "L255 length")
			read(8, 6)
		}
		if b.err != nil {
			return
		}
		for b.pos < start+int(length)*8 && b.err == nil {
			b.require(b.read(1) == 0, "extension padding")
		}
		if b.err != nil {
			return
		}
		d.Blocks = append(d.Blocks, block)
		v := block.Values
		switch level {
		case 1:
			d.L1 = &L1{uint16(v[0]), uint16(v[1]), uint16(v[2])}
		case 5:
			d.L5 = &L5{uint16(v[0]), uint16(v[1]), uint16(v[2]), uint16(v[3])}
		case 6:
			d.L6 = &L6{uint16(v[0]), uint16(v[1]), uint16(v[2]), uint16(v[3])}
		}
	}
	if cm40 {
		b.require(counts[254] == 1, "CM4 L254 required")
	}
}
