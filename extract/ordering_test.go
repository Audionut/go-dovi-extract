package extract

import (
	"bytes"
	"errors"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func ueBits(v uint64) string {
	bits := strconv.FormatUint(v+1, 2)
	return strings.Repeat("0", len(bits)-1) + bits
}

type shortReference struct {
	delta int16
	used  bool
}

func authoredReferencePicture(poc uint64, kind byte, slice uint64, negative, positive []shortReference) []byte {
	bits := "1"
	if kind >= 16 {
		bits += "0"
	}
	bits += ueBits(5) + ueBits(slice) + "1"
	if kind != 19 && kind != 20 {
		bits += strconv.FormatUint(poc|256, 2)[1:] + "0" + ueBits(uint64(len(negative))) + ueBits(uint64(len(positive)))
		previous := int16(0)
		for _, ref := range negative {
			bits += ueBits(uint64(previous - ref.delta - 1))
			if ref.used {
				bits += "1"
			} else {
				bits += "0"
			}
			previous = ref.delta
		}
		previous = 0
		for _, ref := range positive {
			bits += ueBits(uint64(ref.delta - previous - 1))
			if ref.used {
				bits += "1"
			} else {
				bits += "0"
			}
			previous = ref.delta
		}
		if slice != 2 {
			bits += "0"
			if slice == 0 {
				bits += "0"
			}
			bits += ueBits(0)
		}
	}
	bits += ueBits(0) + "1" // QP0 and byte_alignment
	return authoredNAL(kind, append(packedHeader(bits), 0xff, 0xff))
}
func authoredNAL(kind byte, rbsp []byte) []byte {
	out := []byte{0, 0, 1, kind << 1, 1}
	zeros := 0
	for _, v := range rbsp {
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
func packedHeader(bits string) []byte {
	bits += strings.Repeat("0", (-len(bits))&7)
	out := make([]byte, len(bits)/8)
	for i, v := range bits {
		out[i/8] = out[i/8]<<1 | byte(v-'0')
	}
	return out
}

func parameterFixture(t testing.TB) []byte {
	t.Helper()
	var out []byte
	for _, n := range syntaxNALs(t) {
		if n[0]>>1&63 >= 32 {
			out = append(out, 0, 0, 1)
			out = append(out, n...)
		}
	}
	return out
}
func TestPOCWrapAndIRAP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kinds   []byte
		pocs    []uint64
		want    []int64
		invalid bool
	}{
		{"wrap", []byte{19, 1, 1, 1}, []uint64{0, 120, 240, 4}, []int64{0, 120, 240, 260}, false},
		{"reset", []byte{19, 1, 19, 1}, []uint64{0, 1, 0, 1}, []int64{0, 1, 0, 1}, false},
		{"CRA-RASL", []byte{21, 9, 1}, []uint64{0, 255, 1}, []int64{0, 1}, false},
		{"CRA-RADL", []byte{21, 7, 1}, []uint64{0, 255, 1}, []int64{-1, 0, 1}, false},
		{"IDR-forbidden-RASL", []byte{19, 9, 1}, []uint64{0, 255, 1}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := parameterFixture(t)
			for i, poc := range tc.pocs {
				data = append(data, authoredReferencePicture(poc, tc.kinds[i], 2, nil, nil)...)
				data = append(data, 0, 0, 1)
				data = append(data, f.NAL(f.Options{Max: uint16(1000 + i)})...)
			}
			e, err := Extract(t.Context(), bytes.NewReader(data), Options{})
			if tc.invalid {
				if !errors.Is(err, ErrInvalidBitstream) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for _, p := range e.Pictures {
				got = append(got, p.POC)
				if e.Records[p.RPUIndex].RPU.DM.L1.MaxPQ != 1000+uint16(p.AUOrdinal) {
					t.Fatal("misassociated RPU")
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatal(got, tc.want)
			}
			if tc.name == "CRA-RASL" && e.SuppressedRPUs != 1 {
				t.Fatal(e.SuppressedRPUs)
			}
		})
	}
}
func TestMultiSliceAssociation(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		data := parameterFixture(t)
		data = append(data, authoredReferencePicture(0, 19, 2, nil, nil)...)
		bits := "00" + ueBits(5)
		if dependent {
			bits += "11"
		} else {
			bits += "01" + ueBits(2) + "1" + ueBits(0)
		}
		bits += "1"
		data = append(data, authoredNAL(19, append(packedHeader(bits), 0xff))...)
		data = append(data, 0, 0, 1)
		data = append(data, f.NAL(f.Options{Max: 2048})...)
		e, err := Extract(t.Context(), bytes.NewReader(data), Options{})
		if err != nil || len(e.Pictures) != 1 || len(e.Records) != 1 {
			t.Fatal(dependent, e, err)
		}
	}
}
