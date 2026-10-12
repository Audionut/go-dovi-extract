package extract

import (
	"bytes"
	"context"
	"fmt"
	dovi "github.com/Audionut/go-dovi-extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	ebml "github.com/at-wat/ebml-go"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func ExampleReadRPU() {
	ctx := context.Background()
	e, err := ReadRPU(ctx, bytes.NewReader(f.File(f.Options{Max: 1000})), Options{KeepRaw: true})
	if err != nil {
		panic(err)
	}
	var out bytes.Buffer
	if err := WriteRPU(&out, e); err != nil {
		panic(err)
	}
	fmt.Println(e.Timeline, len(e.Pictures), len(e.Records))
	// Output: rpu_sequence 0 1
}

// This executable file example uses a new temporary directory, so publishing
// never overwrites an existing user destination.
func TestMatroskaPNGFileExample(t *testing.T) {
	config, sample := containerSample(t, 4)
	data := mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}})), nil, false, false)
	e, err := Extract(t.Context(), bytes.NewReader(data), Options{Format: Matroska, TrackID: 1})
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := dovi.Render(t.Context(), a, dovi.PlotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, err := os.CreateTemp(dir, "plot-*.png")
	if err != nil {
		t.Fatal(err)
	}
	name := out.Name()
	encodeErr := png.Encode(out, img)
	closeErr := out.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatal(encodeErr, closeErr)
	}
	if err := os.Rename(name, filepath.Join(dir, "L1.png")); err != nil {
		t.Fatal(err)
	}
}
