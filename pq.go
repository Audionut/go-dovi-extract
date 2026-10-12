package dovi

import "math"

const (
	m1 = 2610.0 / 16384
	m2 = 2523.0 / 32
	c1 = 3424.0 / 4096
	c2 = 2413.0 / 128
	c3 = 2392.0 / 128
)

// PQToNits converts finite normalized ST 2084 values in [0,1] to cd/m².
func PQToNits(p float64) (float64, error) {
	if math.IsNaN(p) || p < 0 || p > 1 {
		return 0, ErrInvalidMetadata
	}
	q := math.Pow(p, 1/m2)
	return 10000 * math.Pow(max(q-c1, 0)/(c2-c3*q), 1/m1), nil
}

// NitsToPQ converts finite luminance in [0,10000] cd/m² to ST 2084.
func NitsToPQ(n float64) (float64, error) {
	if math.IsNaN(n) || n < 0 || n > 10000 {
		return 0, ErrInvalidMetadata
	}
	if n == 0 {
		return 0, nil
	}
	p := math.Pow(n/10000, m1)
	return math.Pow((c1+c2*p)/(1+c3*p), m2), nil
}
func codeNits(v float64) float64 { n, _ := PQToNits(v / 4095); return n }
