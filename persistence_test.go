package dovi

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"image"
	"reflect"
	"testing"
)

func TestExpandedAnalysisBudgets(t *testing.T) {
	a := testAnalysis(t)
	a.Records[0].Remaining = make([]byte, 8192)
	if _, err := a.Clone(AnalysisOptions{MaxBytes: 4096}); !errors.Is(err, ErrLimit) {
		t.Fatal("expanded copy exceeded budget", err)
	}
	if _, err := a.Clone(AnalysisOptions{MaxBytes: 65536}); err != nil {
		t.Fatal(err)
	}
	// The input fits this budget, but independently allocated report output does not.
	if err := a.Validate(AnalysisOptions{MaxBytes: 12000}); err != nil {
		t.Fatal(err)
	}
	if _, err := Summarize(a, SummaryOptions{MaxBytes: 12000}); !errors.Is(err, ErrLimit) {
		t.Fatal("report exceeded budget", err)
	}
	if _, err := Summarize(a, SummaryOptions{MaxBytes: 65536}); err != nil {
		t.Fatal(err)
	}
}

func TestFullAnalysisRoundTrip(t *testing.T) {
	a := testAnalysis(t)
	a.Pictures[0].PTS = Timestamp{Value: 0, Timescale: 24000, Valid: true}
	a.Pictures[2].PTS = Timestamp{Value: 2002, Timescale: 24000, Valid: true}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var restored Analysis
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(AnalysisOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := Summarize(a, SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := Summarize(&restored, SummaryOptions{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("report round trip", err)
	}
	img1, err := Render(t.Context(), a, PlotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	img2, err := Render(t.Context(), &restored, PlotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img1.(*image.RGBA).Pix, img2.(*image.RGBA).Pix) {
		t.Fatal("pixel round trip")
	}
}
func FuzzAnalysisValidation(f *testing.F) {
	f.Add([]byte(`{"schema_version":1,"complete":false}`))
	seed, err := json.Marshal(testAnalysis(f))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			t.Skip()
		}
		var a Analysis
		if json.Unmarshal(data, &a) != nil {
			return
		}
		if a.Validate(AnalysisOptions{MaxRecords: 100, MaxBytes: 1 << 20}) == nil {
			_, _ = Summarize(&a, SummaryOptions{MaxRecords: 100, MaxBytes: 1 << 20})
		}
	})
}
