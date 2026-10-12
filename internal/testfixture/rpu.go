// Package testfixture authors synthetic syntax, not encoded video pictures.
package testfixture

import "encoding/binary"

type Bits struct {
	Data []byte
	Pos  int
}

func (b *Bits) Put(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.Pos%8 == 0 {
			b.Data = append(b.Data, 0)
		}
		b.Data[b.Pos/8] |= byte((v>>i)&1) << (7 - uint(b.Pos%8))
		b.Pos++
	}
}
func (b *Bits) UE(v uint64) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	b.Put(0, n)
	b.Put(v, n+1)
}
func (b *Bits) Align() {
	for b.Pos%8 != 0 {
		b.Put(0, 1)
	}
}
func CRC(data []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, v := range data {
		crc ^= uint32(v) << 24
		for range 8 {
			hi := crc >> 31
			crc <<= 1
			if hi != 0 {
				crc ^= 0x04c11db7
			}
		}
	}
	return crc
}
func Seal(body []byte) []byte {
	out := append([]byte{0x19}, body...)
	out = binary.BigEndian.AppendUint32(out, CRC(body))
	return append(out, 0x80)
}
func Escape(data []byte) []byte {
	out := []byte{}
	zeros := 0
	for _, v := range data {
		if zeros == 2 && v <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, v)
		if v == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

type Extension struct {
	Level  uint8
	Length uint64
	Body   Bits
}
type Options struct {
	Float, MMR, Interpolation            bool
	Blocks29, Blocks40                   *[]Extension
	Profile                              int
	FEL, Reuse, CM40, Uncompressed, NoL1 bool
	Min, Max, Avg                        uint16
	Scene                                uint64
}

func RPU(o Options) []byte {
	if o.Profile == 0 {
		o.Profile = 8
	}
	b := Bits{}
	b.Put(2, 6)
	b.Put(18, 11)
	p := uint64(1)
	if o.Profile == 5 {
		p = 0
	}
	b.Put(p, 4)
	b.Put(0, 4)
	b.Put(1, 1)
	b.Put(0, 1)
	width := 23
	if o.Float {
		b.Put(1, 2)
		width = 32
	} else {
		b.Put(0, 2)
		b.UE(23)
	}
	b.Put(1, 2)
	if o.Profile == 5 {
		b.Put(1, 1)
	} else {
		b.Put(0, 1)
	}
	b.UE(2)
	b.UE(2)
	b.UE(4)
	b.Put(0, 1)
	if o.Uncompressed {
		b.Put(0, 3)
	} else {
		b.Put(1, 3)
	}
	if o.Profile == 7 {
		b.Put(1, 1)
		b.Put(0, 1)
	} else {
		b.Put(0, 1)
		b.Put(1, 1)
	}
	b.Put(1, 1)
	if o.Reuse {
		b.Put(1, 1)
		b.UE(0)
	} else {
		b.Put(0, 1)
		b.UE(0)
		b.UE(0)
		b.UE(0)
		for range 3 {
			b.UE(0)
			b.Put(0, 10)
			b.Put(1023, 10)
		}
		if o.Profile == 7 {
			b.Put(0, 3)
			b.Put(0, 10)
			b.Put(1023, 10)
		}
		b.UE(0)
		b.UE(0)
		coefficient := func(integer uint64) {
			if !o.Float {
				b.UE(integer)
			}
			b.Put(0, width)
		}
		for range 3 {
			if o.MMR {
				b.UE(1)
				b.Put(2, 2)
				for range 22 {
					coefficient(0)
				}
			} else {
				b.UE(0)
				b.UE(0)
				if o.Interpolation {
					b.Put(1, 1)
				} else {
					b.Put(0, 1)
				}
				coefficient(0)
				coefficient(2)
			}
		}
		if o.Profile == 7 {
			for range 3 {
				offset := uint64(0)
				if o.FEL {
					offset = 1
				}
				b.Put(offset, 10)
				if !o.Float {
					b.UE(1)
				}
				b.Put(0, width)
				if !o.Float {
					b.UE(0)
				}
				b.Put(0, width)
				if !o.Float {
					b.UE(0)
				}
				b.Put(0, width)
			}
		}
	}
	b.UE(0)
	b.UE(0)
	b.UE(o.Scene)
	if o.Uncompressed {
		for range 9 {
			b.Put(0, 16)
		}
		for range 3 {
			b.Put(0, 32)
		}
		for range 9 {
			b.Put(0, 16)
		}
		b.Put(65535, 16)
		b.Put(0, 16)
		b.Put(0, 16)
		b.Put(0, 32)
		b.Put(10, 5)
		b.Put(0, 2)
		b.Put(0, 2)
		b.Put(0, 2)
		b.Put(7, 12)
		b.Put(3079, 12)
		b.Put(42, 10)
	}
	writeBlocks := func(blocks []Extension) {
		b.UE(uint64(len(blocks)))
		b.Align()
		for _, block := range blocks {
			b.UE(block.Length)
			b.Put(uint64(block.Level), 8)
			for i := 0; i < int(block.Length)*8; i++ {
				v := uint64(0)
				if i < block.Body.Pos {
					v = uint64(block.Body.Data[i/8]>>(7-uint(i%8))) & 1
				}
				b.Put(v, 1)
			}
		}
	}
	if o.Blocks29 != nil {
		writeBlocks(*o.Blocks29)
	} else if o.NoL1 {
		b.UE(0)
		b.Align()
	} else {
		b.UE(1)
		b.Align()
		b.UE(5)
		b.Put(1, 8)
		b.Put(uint64(o.Min), 12)
		b.Put(uint64(o.Max), 12)
		b.Put(uint64(o.Avg), 12)
		b.Put(0, 4)
	}
	if o.Blocks40 != nil {
		writeBlocks(*o.Blocks40)
	} else if o.CM40 {
		b.UE(1)
		b.Align()
		b.UE(2)
		b.Put(254, 8)
		b.Put(0, 8)
		b.Put(2, 8)
	}
	b.Align()
	return Seal(b.Data)
}
func NAL(o Options) []byte  { return append([]byte{0x7c, 1}, Escape(RPU(o))...) }
func File(o Options) []byte { return append([]byte{0, 0, 0, 1}, Escape(RPU(o))...) }
