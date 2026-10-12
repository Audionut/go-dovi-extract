package dovi_test

import (
	"bytes"
	"context"
	"fmt"
	dovi "github.com/Audionut/go-dovi-extract"
	"github.com/Audionut/go-dovi-extract/extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"image/png"
)

func ExampleParseRPU() {
	r, err := dovi.ParseRPU(f.RPU(f.Options{Max: 3000, Avg: 1500}), dovi.ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(r.Profile, r.DM.L1.MaxPQ, r.DM.Base == nil)
	// Output: 8 3000 true
}
func ExampleRender() {
	ctx := context.Background()
	e, err := extract.ReadRPU(ctx, bytes.NewReader(f.File(f.Options{Max: 3000, Avg: 1500})), extract.Options{})
	if err != nil {
		panic(err)
	}
	a, err := e.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		panic(err)
	}
	s, err := dovi.Summarize(a, dovi.SummaryOptions{})
	if err != nil {
		panic(err)
	}
	img, err := dovi.Render(ctx, a, dovi.PlotOptions{})
	if err != nil {
		panic(err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		panic(err)
	}
	fmt.Println(s.Coverage, img.Bounds().Dx(), img.Bounds().Dy())
	// Output: unknown 3000 1200
}
