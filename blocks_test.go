package dovi

import (
	"errors"
	"fmt"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"testing"
)

func TestExtensionLayouts(t *testing.T) {
	for _, tc := range []struct {
		level  uint8
		length int
		widths []int
		values []uint64
	}{{1, 5, []int{12, 12, 12}, nil}, {2, 11, []int{12, 12, 12, 12, 12, 12, 13}, nil}, {3, 5, []int{12, 12, 12}, nil}, {4, 3, []int{12, 12}, nil}, {5, 7, []int{13, 13, 13, 13}, nil}, {6, 8, []int{16, 16, 16, 16}, nil}, {8, 10, nil, nil}, {8, 12, nil, nil}, {8, 13, nil, nil}, {8, 19, nil, nil}, {8, 25, nil, nil}, {9, 1, []int{8}, nil}, {9, 17, nil, nil}, {10, 5, []int{8, 12, 12, 8}, nil}, {10, 21, nil, nil}, {11, 4, []int{8, 8, 8, 8}, nil}, {253, 0, nil, nil}, {253, 66, nil, nil}, {254, 2, []int{8, 8}, nil}, {255, 6, []int{8, 8, 8, 8, 8, 8}, nil}} {
		t.Run(fmt.Sprintf("L%d/length%d", tc.level, tc.length), func(t *testing.T) {
			var body f.Bits
			switch tc.level {
			case 8:
				body.Put(255, 8)
				for range 6 {
					body.Put(2048, 12)
				}
				if tc.length >= 12 {
					body.Put(1024, 12)
				}
				if tc.length >= 13 {
					body.Put(3, 12)
				}
				if tc.length >= 19 {
					for range 6 {
						body.Put(128, 8)
					}
				}
				if tc.length >= 25 {
					for range 6 {
						body.Put(64, 8)
					}
				}
			case 9:
				if tc.length == 17 {
					body.Put(255, 8)
					for range 8 {
						body.Put(100, 16)
					}
				} else {
					body.Put(2, 8)
				}
			case 10:
				body.Put(255, 8)
				body.Put(3000, 12)
				body.Put(1, 12)
				if tc.length == 21 {
					body.Put(255, 8)
					for range 8 {
						body.Put(100, 16)
					}
				} else {
					body.Put(2, 8)
				}
			case 253:
				for range tc.length {
					body.Put(0x55, 8)
				}
			default:
				for _, w := range tc.widths {
					body.Put(0, w)
				}
			}
			blocks := []f.Extension{{Level: tc.level, Length: uint64(tc.length), Body: body}}
			o := f.Options{NoL1: true}
			cm40 := tc.level == 3 || tc.level == 8 || tc.level == 9 || tc.level == 10 || tc.level == 11 || tc.level == 253 || tc.level == 254
			if cm40 {
				if tc.level != 254 {
					blocks = append(blocks, f.Extension{Level: 254, Length: 2})
				}
				o.Blocks40 = &blocks
			} else {
				o.Blocks29 = &blocks
			}
			r, err := ParseRPU(f.RPU(o), ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !validateRecord(&r) {
				t.Fatal("parsed record fails restore validation")
			}
			blocks[0].Length = 100
			if _, err := ParseRPU(f.RPU(o), ParseOptions{}); err == nil {
				t.Fatal("bad length accepted")
			}
		})
	}
}
func TestUnknownExtensionAndMissingCM4Version(t *testing.T) {
	blocks := []f.Extension{{Level: 42, Length: 1}}
	if _, err := ParseRPU(f.RPU(f.Options{Blocks29: &blocks}), ParseOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	empty := []f.Extension{{Level: 3, Length: 5}}
	if _, err := ParseRPU(f.RPU(f.Options{Blocks40: &empty}), ParseOptions{}); !errors.Is(err, ErrInvalidBitstream) {
		t.Fatal(err)
	}
	blocks = []f.Extension{{Level: 1, Length: 5}, {Level: 1, Length: 5}}
	if _, err := ParseRPU(f.RPU(f.Options{Blocks29: &blocks}), ParseOptions{}); !errors.Is(err, ErrInvalidBitstream) {
		t.Fatal(err)
	}
}
