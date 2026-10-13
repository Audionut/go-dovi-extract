package dovi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"image"
	"image/png"
	"math"
	"os"
	"testing"
)

func TestRenderMetadataCaptions(t *testing.T) {
	for _, field := range []string{"profile-without-DM", "source-mastering", "L6-mastering"} {
		t.Run(field, func(t *testing.T) {
			a := testAnalysis(t)
			if field == "source-mastering" {
				r, err := ParseRPU(f.RPU(f.Options{Uncompressed: true, Max: 1000, Avg: 500}), ParseOptions{})
				if err != nil {
					t.Fatal(err)
				}
				a.Records[0] = r
			}
			if field == "profile-without-DM" {
				a.Records[0].DM = nil
				a.Records[0].Header.DMPresent = false
			}
			before, err := Render(t.Context(), a, PlotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "profile-without-DM":
				a.Records[0].Profile = 5
				a.Records[0].Header.Profile = 0
				a.Records[0].Header.FullRange = true
			case "source-mastering":
				a.Records[0].DM.Base.SourceMaxPQ = 2500
			case "L6-mastering":
				a.Records[0].DM.L6 = &L6{1000, 50, 900, 400}
				a.Records[0].DM.Blocks = append(a.Records[0].DM.Blocks, Block{Level: 6, Length: 8, Values: []int64{1000, 50, 900, 400}})
			}
			after, err := Render(t.Context(), a, PlotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before.(*image.RGBA).Pix, after.(*image.RGBA).Pix) {
				t.Fatal("caption discarded metadata evidence")
			}
		})
	}
}

func TestRenderSpikeAndMissingSamples(t *testing.T) {
	a := testAnalysis(t)
	base := a.Records[0]
	a.Records = make([]RPU, 6000)
	a.Pictures = make([]Picture, 6000)
	a.ObservedRPUs = 6000
	for i := range a.Records {
		a.Records[i] = cloneRPU(base)
		a.Pictures[i] = Picture{HasRPU: true, RPUIndex: uint32(i)}
	}
	a.Records[3000].DM.L1.MaxPQ = 4095
	a.Records[3000].DM.Blocks[0].Values[1] = 4095
	img, err := Render(t.Context(), a, PlotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	isBlue := func(x, y int) bool { r, g, b, _ := img.At(x, y).RGBA(); return b > r+10000 && b > g+5000 }
	found := false
	for y := chartRect.Min.Y; y < chartRect.Min.Y+20; y++ {
		for x := 1530; x < 1550; x++ {
			if isBlue(x, y) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("narrow peak lost")
	}
	// Gap test uses three distant samples: the center missing point must not
	// be interpolated by the maximum curve.
	gap := testAnalysis(t)
	img, err = Render(t.Context(), gap, PlotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for y := chartRect.Min.Y + 20; y < chartRect.Max.Y-140; y++ {
		if isBlue(chartRect.Min.X+chartRect.Dx()/3, y) {
			t.Fatal("curve bridged gap")
		}
	}
}

func testAnalysis(t testing.TB) *Analysis {
	t.Helper()
	a := &Analysis{SchemaVersion: 1, Complete: true, Timeline: PresentationFrames, ObservedRPUs: 2}
	for i := range 2 {
		r, err := ParseRPU(f.RPU(f.Options{Min: 0, Max: uint16(1000 + i*2000), Avg: uint16(500 + i*1000), Scene: uint64(i)}), ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		a.Records = append(a.Records, r)
	}
	a.Pictures = []Picture{{HasRPU: true}, {}, {HasRPU: true, RPUIndex: 1}}
	return a
}
func TestSummaryAndValidation(t *testing.T) {
	a := testAnalysis(t)
	s, err := Summarize(a, SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Coverage != CoveragePartial || s.Counts.L1Samples != 2 || s.Counts.MissingPictures != 1 || len(s.SceneMarkers) != 1 || s.SceneMarkers[0] != 2 {
		t.Fatal(s)
	}
	want := codeNits(2000)
	if math.Abs(s.L1.PQMeanPeakNits-want) > 1e-9 {
		t.Fatal(s.L1)
	}
	nitMean := (codeNits(1000) + codeNits(3000)) / 2
	if math.Abs(nitMean-want) < 1 {
		t.Fatal("fixture must distinguish means")
	}
	a.Pictures[2].RPUIndex = 0
	if err := a.Validate(AnalysisOptions{}); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatal(err)
	}
	a = testAnalysis(t)
	a.Records[0].DM.L1.MaxPQ = 42
	if err := a.Validate(AnalysisOptions{}); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatal("inconsistent restored values", err)
	}
	a = testAnalysis(t)
	a.Complete = false
	if err := a.Validate(AnalysisOptions{}); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatal(err)
	}
}
func TestL5GeometryAndGaps(t *testing.T) {
	a := testAnalysis(t)
	for i := range a.Records {
		d := a.Records[i].DM
		d.L5 = &L5{0, 0, 100, 100}
		d.Blocks = append(d.Blocks, Block{Level: 5, Length: 7, Values: []int64{0, 0, 100, 100}})
	}
	for _, tc := range []struct {
		raster Raster
		want   string
	}{{Raster{}, "unknown"}, {Raster{1920, 1080, "coded_raster", true}, "valid"}, {Raster{1920, 200, "coded_raster", true}, "invalid"}} {
		s, err := Summarize(a, SummaryOptions{Raster: tc.raster})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.ActiveAreas) != 3 || s.ActiveAreas[0].Geometry != tc.want || s.ActiveAreas[1].Geometry != "missing" {
			t.Fatal(s.ActiveAreas)
		}
	}
}
func TestRender(t *testing.T) {
	a := testAnalysis(t)
	img, err := Render(t.Context(), a, PlotOptions{Title: "Dolby Vision synthetic gap test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(img.(*image.RGBA).Pix)); got != "ea77217140044ea17073354e1d76a7b2a0fbcc32ae21dc9802e9ab23be8500df" {
		t.Fatalf("pixel golden %s", got)
	}
	if img.Bounds().Dx() != 3000 || img.Bounds().Dy() != 1200 {
		t.Fatal(img.Bounds())
	}
	for y := range 1200 {
		for x := range 3000 {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha != 65535 {
				t.Fatal("transparent pixel")
			}
		}
	}
	if path := os.Getenv("DOVI_PLOT_OUTPUT"); path != "" {
		w, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(w, img)
		closeErr := w.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	if _, err := Render(t.Context(), a, PlotOptions{End: new(uint64(0))}); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(t.Context(), a, PlotOptions{Start: new(uint64(1)), End: new(uint64(1))}); !errors.Is(err, ErrNoL1) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Render(ctx, a, PlotOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Render(t.Context(), a, PlotOptions{MaxBytes: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func BenchmarkSummary(b *testing.B) {
	a := testAnalysis(b)
	for b.Loop() {
		if _, err := Summarize(a, SummaryOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRender(b *testing.B) {
	a := testAnalysis(b)
	for b.Loop() {
		if _, err := Render(b.Context(), a, PlotOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
