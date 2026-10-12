package extract

import (
	"bytes"
	"context"
	"errors"
	dovi "github.com/Audionut/go-dovi-extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"os"
	"reflect"
	"slices"
	"testing"
)

func syntaxNALs(t testing.TB) [][]byte {
	t.Helper()
	b, err := os.ReadFile("testdata/pictures.hevc")
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(b, []byte{0, 0, 1})[1:]
}
func videoFixture(t testing.TB, missing int) []byte {
	t.Helper()
	var out []byte
	pic := 0
	for _, n := range syntaxNALs(t) {
		out = append(out, 0, 0, 1)
		out = append(out, n...)
		if n[0]>>1&63 <= 31 {
			if pic != missing {
				out = append(out, 0, 0, 1)
				out = append(out, f.NAL(f.Options{Max: uint16(1000 + pic), Avg: 500, Scene: uint64(pic % 2)})...)
			}
			pic++
		}
	}
	return out
}
func rawFixture(t testing.TB, name string) []byte {
	t.Helper()
	var out []byte
	for _, n := range syntaxNALs(t) {
		out = append(out, 0, 0, 1)
		out = append(out, n...)
		if n[0]>>1&63 <= 31 {
			out = append(out, 0, 0, 1)
			out = append(out, f.NAL(f.Options{Max: 1000, Avg: 500})...)
			break
		}
	}
	return out
}
func TestOrderingAndGaps(t *testing.T) {
	for _, missing := range []int{-1, 0, 1, 2} {
		e, err := Extract(t.Context(), bytes.NewReader(videoFixture(t, missing)), Options{KeepRaw: true})
		if err != nil {
			t.Fatal(missing, err)
		}
		if len(e.Pictures) != 3 {
			t.Fatal(len(e.Pictures))
		}
		for i, au := range []uint64{0, 2, 1} {
			p := e.Pictures[i]
			if p.AUOrdinal != au || p.HasRPU != (int(au) != missing) {
				t.Fatalf("missing %d picture %+v", missing, p)
			}
			if p.HasRPU && e.Records[p.RPUIndex].RPU.DM.L1.MaxPQ != 1000+uint16(au) {
				t.Fatal("shifted RPU")
			}
		}
		a, err := e.Analysis(dovi.AnalysisOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := dovi.Summarize(a, dovi.SummaryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = WriteRPU(&out, e)
		if missing < 0 {
			if err != nil || s.Coverage != dovi.CoverageComplete {
				t.Fatal(err, s.Coverage)
			}
		} else {
			if !errors.Is(err, ErrIncomplete) || s.Coverage != dovi.CoveragePartial {
				t.Fatal(err, s.Coverage)
			}
		}
	}
}
func TestAbsentAndDuplicate(t *testing.T) {
	var data []byte
	for _, n := range syntaxNALs(t) {
		data = append(data, 0, 0, 1)
		data = append(data, n...)
	}
	e, err := Extract(t.Context(), bytes.NewReader(data), Options{})
	if err != nil || len(e.Records) != 0 || len(e.Pictures) != 3 {
		t.Fatal(e, err)
	}
	a, err := e.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := dovi.Summarize(a, dovi.SummaryOptions{})
	if err != nil || s.Coverage != dovi.CoverageAbsent {
		t.Fatal(s, err)
	}
	data = rawFixture(t, "")
	data = append(data, 0, 0, 1)
	data = append(data, f.NAL(f.Options{})...)
	if e, err := Extract(t.Context(), bytes.NewReader(data), Options{}); e != nil || err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestStreamSplitsAndFailure(t *testing.T) {
	data := videoFixture(t, -1)
	want, err := Extract(t.Context(), bytes.NewReader(data), Options{KeepRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	for split := range len(data) + 1 {
		s, err := NewStream(StreamOptions{KeepRaw: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct {
			offset int
			data   []byte
		}{{0, data[:split]}, {split, data[split:]}} {
			borrowed := slices.Clone(p.data)
			if err := s.Push(t.Context(), Chunk{Data: borrowed, ESOffset: uint64(p.offset)}); err != nil {
				t.Fatal(split, err)
			}
			clear(borrowed)
		}
		got, err := s.Finish(t.Context())
		if err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if got.RetainedBytes() <= 0 || want.RetainedBytes() <= 0 {
			t.Fatal("finished extraction lost accounting")
		}
		// The Extract producer also owns a read buffer. Its conservative memory
		// bound may differ from direct streaming; all extracted evidence must match.
		gotValue, wantValue := *got, *want
		gotValue.retainedBytes, wantValue.retainedBytes = 0, 0
		if !reflect.DeepEqual(wantValue, gotValue) {
			t.Fatalf("split %d changed extracted evidence", split)
		}
		if _, err := s.Finish(t.Context()); err == nil {
			t.Fatal("repeat finish")
		}
	}
	s, _ := NewStream(StreamOptions{})
	sentinel := errors.New("producer failure")
	s.Abort(sentinel)
	if _, err := s.Finish(t.Context()); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := s.Push(t.Context(), Chunk{}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e, err := Extract(ctx, bytes.NewReader(data), Options{}); e != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestStandaloneAndCopy(t *testing.T) {
	data := append(f.File(f.Options{Max: 1000}), f.File(f.Options{NoL1: true, Reuse: true})...)
	e, err := ReadRPU(t.Context(), bytes.NewReader(data), Options{KeepRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Timeline != dovi.RPUSequence || len(a.Pictures) != 0 {
		t.Fatal(a)
	}
	a.Records[0].DM.L1.MaxPQ = 10
	if e.Records[0].RPU.DM.L1.MaxPQ != 1000 {
		t.Fatal("aliased copy")
	}
	var out bytes.Buffer
	if err := WriteRPU(&out, e); err != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatal(err)
	}
	if _, err := e.Analysis(dovi.AnalysisOptions{MaxRecords: 1}); !errors.Is(err, dovi.ErrLimit) {
		t.Fatal(err)
	}
}
func BenchmarkHEVC(b *testing.B) {
	data := videoFixture(b, -1)
	for b.Loop() {
		if _, err := Extract(b.Context(), bytes.NewReader(data), Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
func FuzzHEVC(fz *testing.F) {
	fz.Add([]byte{0, 0, 1, 0x7c, 1})
	fz.Add(videoFixture(fz, -1))
	fz.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			t.Skip()
		}
		_, _ = Extract(t.Context(), bytes.NewReader(data), Options{Format: RawHEVC, Limits: Limits{MaxFrames: 100}})
	})
}
func BenchmarkRPUSequence(b *testing.B) {
	data := bytes.Repeat(f.File(f.Options{Profile: 7, Max: 3079, Avg: 2048}), 1000)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := ReadRPU(b.Context(), bytes.NewReader(data), Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
