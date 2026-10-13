package extract

import (
	"bytes"
	"errors"
	dovi "github.com/Audionut/go-dovi-extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	ebml "github.com/at-wat/ebml-go"
	"io"
	"testing"
)

func TestRPUFileRetainedAccounting(t *testing.T) {
	data := append(f.File(f.Options{}), f.File(f.Options{})...)
	var parsedBytes int64
	for _, raw := range []bool{false, true} {
		budget, err := NewMemoryBudget(16 << 20)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Extract(t.Context(), bytes.NewReader(data), Options{KeepRaw: raw, Budget: budget})
		if err != nil {
			t.Fatal(err)
		}
		if e.Timeline != dovi.RPUSequence || e.RetainedBytes() <= 0 {
			t.Fatalf("standalone records lost retained accounting: timeline=%s bytes=%d", e.Timeline, e.RetainedBytes())
		}
		if raw && e.RetainedBytes() <= parsedBytes {
			t.Fatal("raw retention was not charged")
		}
		parsedBytes = e.RetainedBytes()
		if used, _ := budget.Usage(); used != 0 {
			t.Fatal("completed read held shared charges")
		}
		e.Release()
		if e.RetainedBytes() != 0 || e.Records != nil {
			t.Fatal("release retained storage")
		}
	}
}

func TestMatroskaQuotaBoundaries(t *testing.T) {
	config, sample := containerSample(t, 4)
	block := mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}})
	data := mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), block), nil, false, false)
	for _, tc := range []struct {
		name          string
		below, enough Limits
	}{
		{"index", Limits{MaxContainerIndexBytes: int64(1279 + len(config))}, Limits{MaxContainerIndexBytes: int64(1280 + len(config))}},
		{"block", Limits{MaxBlockBytes: int64(3 + len(sample))}, Limits{MaxBlockBytes: int64(4 + len(sample))}},
		{"nesting", Limits{MaxNestingDepth: 2}, Limits{MaxNestingDepth: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget, _ := NewMemoryBudget(8 << 20)
			if e, err := Extract(t.Context(), bytes.NewReader(data), Options{Limits: tc.below, Budget: budget}); e != nil || !errors.Is(err, ErrResourceLimit) {
				t.Fatal("below boundary", err)
			}
			if used, _ := budget.Usage(); used != 0 {
				t.Fatal("failed extraction retained memory")
			}
			if e, err := Extract(t.Context(), bytes.NewReader(data), Options{Limits: tc.enough, Budget: budget}); err != nil || len(e.Pictures) != 1 {
				t.Fatal("at boundary", err)
			}
			if used, _ := budget.Usage(); used != 0 {
				t.Fatal("completed extraction retained memory")
			}
		})
	}
}

func TestIndependentQuotas(t *testing.T) {
	data := videoFixture(t, -1)
	for _, l := range []Limits{{MaxFrames: 2}, {MaxRPUBytes: 32}, {MaxParameterSetBytes: 8}, {MaxSliceHeaderBytes: 2}, {MaxReorderPictures: 1}} {
		if e, err := Extract(t.Context(), bytes.NewReader(data), Options{Limits: l}); e != nil || !errors.Is(err, ErrResourceLimit) {
			t.Fatal(l, err)
		}
	}
	for _, o := range []Options{{Limits: Limits{MaxFrames: 1}}, {Limits: Limits{MaxRPUBytes: 8}}} {
		data := append(f.File(f.Options{}), f.File(f.Options{})...)
		if e, err := ReadRPU(t.Context(), bytes.NewReader(data), o); e != nil || !errors.Is(err, ErrResourceLimit) {
			t.Fatal(o, err)
		}
	}
	budget, _ := NewMemoryBudget(1024)
	if e, err := Extract(t.Context(), bytes.NewReader(data), Options{Budget: budget}); e != nil || !errors.Is(err, ErrResourceLimit) {
		t.Fatal(err)
	}
	if used, _ := budget.Usage(); used != 0 {
		t.Fatal("failed operation retained charge")
	}
}

type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestIOFailuresAndRawMutation(t *testing.T) {
	sentinel := errors.New("reader failure")
	e, err := ReadRPU(t.Context(), &failingReader{f.File(f.Options{}), sentinel}, Options{})
	if e != nil || !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	e, err = ReadRPU(t.Context(), bytes.NewReader(f.File(f.Options{})), Options{KeepRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRPU(shortWriter{}, e); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	e.Records[0].RPU.Header.FullRange = true
	if err := WriteRPU(io.Discard, e); !errors.Is(err, dovi.ErrInvalidMetadata) {
		t.Fatal(err)
	}
}
func TestVCLBodyStorageBound(t *testing.T) {
	base := rawFixture(t, "")
	parts := bytes.Split(base, []byte{0, 0, 1})
	var large []byte
	for _, part := range parts[1:] {
		large = append(large, 0, 0, 1)
		large = append(large, part...)
		if part[0]>>1&63 <= 31 {
			large = append(large, bytes.Repeat([]byte{0x55}, 2<<20)...)
		}
	}
	budget, _ := NewMemoryBudget(8 << 20)
	e, err := Extract(t.Context(), bytes.NewReader(large), Options{Budget: budget})
	if err != nil || len(e.Pictures) != 1 {
		t.Fatal(err)
	}
	used, peak := budget.Usage()
	if used != 0 || peak >= 4<<20 {
		t.Fatal(used, peak)
	}
}
func TestEOSBeforeRPUAndOrphan(t *testing.T) {
	data := rawFixture(t, "")
	pos := bytes.Index(data, []byte{0, 0, 1, 0x7c, 1})
	withEOS := append([]byte(nil), data[:pos]...)
	withEOS = append(withEOS, 0, 0, 1, 72, 1)
	withEOS = append(withEOS, data[pos:]...)
	e, err := Extract(t.Context(), bytes.NewReader(withEOS), Options{})
	if err != nil || len(e.Records) != 1 || len(e.Pictures) != 1 {
		t.Fatal(err)
	}
	orphan := append(append([]byte(nil), data...), 0, 0, 1, 70, 1, 0x10)
	orphan = append(orphan, 0, 0, 1)
	orphan = append(orphan, f.NAL(f.Options{})...)
	if e, err := Extract(t.Context(), bytes.NewReader(orphan), Options{}); e != nil || err == nil {
		t.Fatal("orphan accepted")
	}
}
