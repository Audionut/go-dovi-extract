package extract

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	ebml "github.com/at-wat/ebml-go"
)

func TestRestoredRawExport(t *testing.T) {
	blocks := []f.Extension{{Level: 253}, {Level: 254, Length: 2}}
	for _, opts := range []f.Options{{NoL1: true}, {NoL1: true, Blocks40: &blocks}, {Profile: 5}, {Profile: 8}} {
		e, err := ReadRPU(t.Context(), bytes.NewReader(f.File(opts)), Options{KeepRaw: true})
		if err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := WriteRPU(&before, e); err != nil {
			t.Fatal(err)
		}
		a, err := e.Analysis(dovi.AnalysisOptions{})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var restored dovi.Analysis
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if err := restored.Validate(dovi.AnalysisOptions{}); err != nil {
			t.Fatal(err)
		}
		e.Records[0].RPU = restored.Records[0]
		e.Records[0].RPU.Remaining = []byte{} // interchangeable empty collection
		var after bytes.Buffer
		if err := WriteRPU(&after, e); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("changed raw output")
		}
		e.Records[0].RPU.CRC++
		if err := WriteRPU(io.Discard, e); !errors.Is(err, dovi.ErrInvalidMetadata) {
			t.Fatal("real mismatch accepted", err)
		}
	}
}

func TestAutoPaddedRPU(t *testing.T) {
	for _, padding := range []int{0, 3, 4, 8, 100, 4096} {
		data := append(bytes.Repeat([]byte{0}, padding), f.File(f.Options{})...)
		e, err := Extract(t.Context(), bytes.NewReader(data), Options{})
		if err != nil || e.Timeline != dovi.RPUSequence || len(e.Records) != 1 {
			t.Fatal(padding, err)
		}
	}
}

type probeReader struct {
	*bytes.Reader
	err       error
	cancel    context.CancelFunc
	dataError bool
}

func (r *probeReader) Read(p []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
	}
	if r.err != nil {
		err := r.err
		r.err = nil
		if r.dataError {
			n, _ := r.Reader.Read(p)
			return n, err
		}
		return 0, err
	}
	return r.Reader.Read(p)
}

func TestAutoProbeDataAndError(t *testing.T) {
	config, sample := containerSample(t, 4)
	block := mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}})
	mkv := mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), block), nil, false, false)
	sentinel := errors.New("read-ahead failure")
	for _, tc := range []struct {
		format Format
		data   []byte
	}{
		{RPUFile, f.File(f.Options{})}, {RawHEVC, videoFixture(t, -1)}, {Matroska, mkv},
	} {
		for _, format := range []Format{Auto, tc.format} {
			for _, readErr := range []error{sentinel, io.EOF} {
				r := &probeReader{Reader: bytes.NewReader(tc.data), err: readErr, dataError: true}
				e, err := Extract(t.Context(), r, Options{Format: format})
				if readErr == io.EOF {
					if err != nil || e == nil {
						t.Fatalf("format %d data+EOF: %v", format, err)
					}
				} else if e != nil || !errors.Is(err, sentinel) {
					t.Fatalf("format %d discarded error: result=%v err=%v", format, e != nil, err)
				}
			}
		}
	}
}

func TestAutoProbeBoundariesAndFailures(t *testing.T) {
	for _, zeros := range []int{256, 257} {
		data := append(bytes.Repeat([]byte{0}, zeros-3), f.File(f.Options{})...)
		r := bytes.NewReader(append([]byte("skip"), data...))
		if _, err := r.Seek(4, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		e, err := Extract(t.Context(), r, Options{Limits: Limits{MaxRPUBytes: 256}})
		if zeros == 256 && (err != nil || len(e.Records) != 1) {
			t.Fatal(err)
		}
		if zeros == 257 && (e != nil || !errors.Is(err, ErrResourceLimit)) {
			t.Fatal(err)
		}
	}
	sentinel := errors.New("probe reader failed")
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		r := &probeReader{Reader: bytes.NewReader(f.File(f.Options{})), err: sentinel}
		want := sentinel
		if canceled {
			r.err = nil
			r.cancel = cancel
			want = context.Canceled
		}
		e, err := Extract(ctx, r, Options{})
		cancel()
		if e != nil || !errors.Is(err, want) {
			t.Fatal(err)
		}
		if pos, err := r.Seek(0, io.SeekCurrent); err != nil || pos != 0 {
			t.Fatal("probe did not restore seeker", pos, err)
		}
	}
}
