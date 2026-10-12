package dovi

import (
	"errors"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"math"
	"testing"
)

func TestRPUProfilesAndPresence(t *testing.T) {
	for _, o := range []f.Options{{Profile: 5}, {Profile: 7}, {Profile: 7, FEL: true}, {Profile: 8}, {Profile: 8, CM40: true}, {Profile: 8, Reuse: true}, {Profile: 8, Uncompressed: true}} {
		o.Min = 4095
		o.Max = 0
		o.Avg = 2048
		o.Scene = 1
		r, err := ParseNAL(f.NAL(o), ParseOptions{})
		if err != nil {
			t.Fatal(o, err)
		}
		if int(r.Profile) != o.Profile || r.DM.L1 == nil || *r.DM.L1 != (L1{4095, 0, 2048}) || r.DM.SceneRefresh != 1 || r.DM.CM40 != o.CM40 || r.Header.ReuseMapping != o.Reuse {
			t.Fatalf("%+v: %+v", o, r)
		}
		if (r.DM.Base != nil) != o.Uncompressed {
			t.Fatal("fabricated base")
		}
		if o.Profile == 7 {
			want := "MEL"
			if o.FEL {
				want = "FEL"
			}
			if r.EnhancementLayer != want {
				t.Fatal(r.EnhancementLayer)
			}
		}
	}
}
func TestCRCAndMalformed(t *testing.T) {
	if crcMPEG2([]byte("123456789")) != 0x0376e6e7 {
		t.Fatal("CRC vector")
	}
	data := f.RPU(f.Options{})
	for i := range len(data) {
		if _, err := ParseRPU(data[:i], ParseOptions{}); err == nil {
			t.Fatalf("truncation %d", i)
		}
	}
	for i := 1; i < len(data)-5; i++ {
		bad := append([]byte(nil), data...)
		bad[i] ^= 1
		if _, err := ParseRPU(bad, ParseOptions{}); !errors.Is(err, ErrInvalidBitstream) {
			t.Fatalf("bit flip %d: %v", i, err)
		}
	}
	if _, err := ParseNAL([]byte{0x7c, 1, 0, 0, 3, 4}, ParseOptions{}); !errors.Is(err, ErrInvalidBitstream) {
		t.Fatal(err)
	}
	if _, err := ParseRPU(data, ParseOptions{MaxBytes: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func TestPQ(t *testing.T) {
	for _, p := range []float64{0, .1, .5, .75, 1} {
		n, err := PQToNits(p)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := NitsToPQ(n)
		if math.Abs(got-p) > 1e-12 {
			t.Fatalf("%g => %g", p, got)
		}
	}
	n, _ := PQToNits(1)
	if n != 10000 {
		t.Fatal(n)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1, 1.01} {
		if _, err := PQToNits(v); err == nil {
			t.Fatal(v)
		}
	}
}
func FuzzParseRPU(fz *testing.F) {
	fz.Add(f.RPU(f.Options{CM40: true}))
	fz.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseRPU(b, ParseOptions{MaxBytes: 65536}) })
}
func BenchmarkParseRPU(b *testing.B) {
	data := f.RPU(f.Options{Profile: 7})
	for b.Loop() {
		if _, err := ParseRPU(data, ParseOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
func TestCoefficientForms(t *testing.T) {
	for _, o := range []f.Options{{Float: true}, {MMR: true}, {Float: true, MMR: true}, {Profile: 7, Float: true}} {
		if _, err := ParseRPU(f.RPU(o), ParseOptions{}); err != nil {
			t.Fatal(o, err)
		}
	}
	if _, err := ParseRPU(f.RPU(f.Options{Interpolation: true}), ParseOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
func FuzzNAL(fz *testing.F) {
	fz.Add(f.NAL(f.Options{Profile: 7, CM40: true}))
	fz.Fuzz(func(t *testing.T, data []byte) { _, _ = ParseNAL(data, ParseOptions{MaxBytes: 65536}) })
}
