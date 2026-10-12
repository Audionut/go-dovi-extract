package dovi

// A sticky error prevents a failed read from driving subsequent unbounded loops.
type bitReader struct {
	data []byte
	pos  int
	err  error
}

func (b *bitReader) fail(err error, field string) {
	if b.err == nil {
		b.err = &ParseError{Field: field, BitOffset: b.pos + 8, OffsetKnown: true, Err: err}
	}
}
func (b *bitReader) read(n int) uint64 {
	if b.err != nil {
		return 0
	}
	if n < 0 || n > 64 || n > len(b.data)*8-b.pos {
		b.fail(ErrIncomplete, "bits")
		return 0
	}
	var v uint64
	for range n {
		v = v<<1 | uint64((b.data[b.pos/8]>>(7-uint(b.pos%8)))&1)
		b.pos++
	}
	return v
}
func (b *bitReader) flag() bool { return b.read(1) != 0 }
func (b *bitReader) ue() uint64 {
	n := 0
	for b.err == nil && b.read(1) == 0 {
		n++
		if n > 62 {
			b.fail(ErrInvalidBitstream, "Exp-Golomb overflow")
			return 0
		}
	}
	if b.err != nil {
		return 0
	}
	return (uint64(1) << n) - 1 + b.read(n)
}
func (b *bitReader) boundedUE(max uint64, field string) uint64 {
	v := b.ue()
	if v > max {
		b.fail(ErrInvalidBitstream, field)
		return 0
	}
	return v
}
func (b *bitReader) align() {
	for b.err == nil && b.pos%8 != 0 {
		if b.flag() {
			b.fail(ErrInvalidBitstream, "alignment")
		}
	}
}
func (b *bitReader) require(ok bool, field string) {
	if !ok {
		b.fail(ErrInvalidBitstream, field)
	}
}
