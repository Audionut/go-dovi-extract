package extract

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOptionalUpstreamRPU(t *testing.T) {
	dir := os.Getenv("DOVI_REFERENCE_DIR")
	if dir == "" {
		t.Skip("optional pinned external fixtures")
	}
	for _, name := range []string{"profile5.bin", "profile8.bin", "profile84.bin", "mel_orig.bin", "fel_orig.bin", "cmv40_full_rpu.bin", "empty_dmv1_blocks.bin", "data_before_crc32.bin", "level253-ext-metadata.bin", "mel_variable_l8_length13.bin"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "assets", "tests", name))
			if err != nil {
				t.Fatal(err)
			}
			e, err := ReadRPU(t.Context(), bytes.NewReader(data), Options{KeepRaw: true})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := WriteRPU(&out, e); err != nil {
				t.Fatal(err)
			}
			t.Logf("records=%d profile=%d EL=%s", len(e.Records), e.Records[0].RPU.Profile, e.Records[0].RPU.EnhancementLayer)
		})
	}
}
func TestOptionalUpstreamVideo(t *testing.T) {
	dir := os.Getenv("DOVI_REFERENCE_DIR")
	if dir == "" {
		t.Skip("optional pinned external fixtures")
	}
	for _, name := range []string{"regular.hevc", "regular.mkv", "no_aud_injected.hevc", "regular_start_code_4_muxed_el.hevc"} {
		t.Run(name, func(t *testing.T) {
			r, err := os.Open(filepath.Join(dir, "assets", "hevc_tests", name))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			e, err := Extract(t.Context(), r, Options{KeepRaw: true})
			if err != nil {
				t.Fatalf("%+v", err)
			}
			t.Log(len(e.Pictures), len(e.Records))
			var out bytes.Buffer
			if err := WriteRPU(&out, e); err != nil {
				t.Fatal(err)
			}
		})
	}
}
