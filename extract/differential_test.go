package extract

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	dovi "github.com/Audionut/go-dovi-extract"
	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func oracleFields(t *testing.T, binary, input string, records []Payload) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "oracle.json")
	cmd := exec.Command(binary, "export", "-i", input, "-d", "all="+output)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("oracle: %v %s", err, out)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var expected []map[string]any
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(records) {
		t.Fatal("oracle record count")
	}
	for i, p := range records {
		compareOracleRecord(t, p.RPU, expected[i])
	}
}
func compareOracleRecord(t *testing.T, r dovi.RPU, m map[string]any) {
	t.Helper()
	check := func(obj map[string]any, key string, want any) {
		t.Helper()
		got, ok := obj[key]
		if !ok {
			t.Fatalf("oracle missing %s", key)
		}
		text := fmt.Sprint(got)
		if n, ok := got.(float64); ok {
			text = fmt.Sprintf("%.0f", n)
		}
		if text != fmt.Sprint(want) {
			t.Fatalf("%s: oracle=%v Go=%v", key, got, want)
		}
	}
	check(m, "dovi_profile", r.Profile)
	check(m, "rpu_data_crc32", r.CRC)
	if r.EnhancementLayer != "" {
		check(m, "el_type", r.EnhancementLayer)
	}
	h := r.Header
	hm := m["header"].(map[string]any)
	for key, v := range map[string]any{"rpu_format": h.Format, "vdr_rpu_profile": h.Profile, "vdr_rpu_level": h.Level, "vdr_seq_info_present_flag": h.SequenceInfo, "chroma_resampling_explicit_filter_flag": h.ChromaFilter, "coefficient_data_type": h.CoefficientType, "coefficient_log2_denom": h.CoefficientDenominator, "vdr_rpu_normalized_idc": h.NormalizedIDC, "bl_video_full_range_flag": h.FullRange, "bl_bit_depth_minus8": h.BLBitDepth - 8, "el_bit_depth_minus8": h.ELBitDepth - 8, "vdr_bit_depth_minus8": h.VDRBitDepth - 8, "spatial_resampling_filter_flag": h.SpatialFilter, "el_spatial_resampling_filter_flag": h.ELSpatialFilter, "reserved_zero_3bits": h.Reserved, "disable_residual_flag": h.DisableResidual, "vdr_dm_metadata_present_flag": h.DMPresent, "use_prev_vdr_rpu_flag": h.ReuseMapping, "prev_vdr_rpu_id": h.PreviousID} {
		check(hm, key, v)
	}
	if r.DM == nil {
		return
	}
	d := r.DM
	dm := m["vdr_dm_data"].(map[string]any)
	check(dm, "compressed", d.Compressed)
	check(dm, "affected_dm_metadata_id", d.AffectedID)
	check(dm, "current_dm_metadata_id", d.CurrentID)
	check(dm, "scene_refresh_flag", d.SceneRefresh)
	if x := d.Base; x != nil {
		for i, v := range x.YCCToRGB {
			check(dm, fmt.Sprintf("ycc_to_rgb_coef%d", i), v)
		}
		for i, v := range x.YCCOffsets {
			check(dm, fmt.Sprintf("ycc_to_rgb_offset%d", i), v)
		}
		for i, v := range x.RGBToLMS {
			check(dm, fmt.Sprintf("rgb_to_lms_coef%d", i), v)
		}
		for key, v := range map[string]any{"signal_eotf": x.EOTF, "signal_eotf_param0": x.EOTFParam0, "signal_eotf_param1": x.EOTFParam1, "signal_eotf_param2": x.EOTFParam2, "signal_bit_depth": x.BitDepth, "signal_color_space": x.ColorSpace, "signal_chroma_format": x.ChromaFormat, "signal_full_range_flag": x.FullRange, "source_min_pq": x.SourceMinPQ, "source_max_pq": x.SourceMaxPQ, "source_diagonal": x.SourceDiagonal} {
			check(dm, key, v)
		}
	}
	fields := map[uint8][]string{1: {"min_pq", "max_pq", "avg_pq"}, 2: {"target_max_pq", "trim_slope", "trim_offset", "trim_power", "trim_chroma_weight", "trim_saturation_gain", "ms_weight"}, 3: {"min_pq_offset", "max_pq_offset", "avg_pq_offset"}, 4: {"anchor_pq", "anchor_power"}, 5: {"active_area_left_offset", "active_area_right_offset", "active_area_top_offset", "active_area_bottom_offset"}, 6: {"max_display_mastering_luminance", "min_display_mastering_luminance", "max_content_light_level", "max_frame_average_light_level"}, 8: {"target_display_index", "trim_slope", "trim_offset", "trim_power", "trim_chroma_weight", "trim_saturation_gain", "ms_weight", "target_mid_contrast", "clip_trim"}, 9: {"source_primary_index"}, 10: {"target_display_index", "target_max_pq", "target_min_pq", "target_primary_index"}, 254: {"dm_mode", "dm_version_index"}, 255: {"dm_run_mode", "dm_run_version", "dm_debug0", "dm_debug1", "dm_debug2", "dm_debug3"}}
	for _, prefix := range []string{"saturation", "hue"} {
		for i := range 6 {
			fields[8] = append(fields[8], fmt.Sprintf("%s_vector_field%d", prefix, i))
		}
	}
	for _, level := range []uint8{9, 10} {
		prefix := "source"
		if level == 10 {
			prefix = "target"
		}
		for _, color := range []string{"red", "green", "blue", "white"} {
			for _, axis := range []string{"x", "y"} {
				fields[level] = append(fields[level], prefix+"_primary_"+color+"_"+axis)
			}
		}
	}
	index := 0
	for _, container := range []struct {
		key     string
		present bool
	}{{"cmv29_metadata", d.CM29}, {"cmv40_metadata", d.CM40}} {
		obj, ok := dm[container.key]
		if ok != container.present {
			t.Fatal("container presence")
		}
		if !ok {
			continue
		}
		cm := obj.(map[string]any)
		blocks := cm["ext_metadata_blocks"].([]any)
		check(cm, "num_ext_blocks", len(blocks))
		for _, entry := range blocks {
			if index >= len(d.Blocks) {
				t.Fatal("block count")
			}
			block := d.Blocks[index]
			index++
			values := entry.(map[string]any)[fmt.Sprintf("Level%d", block.Level)].(map[string]any)
			switch block.Level {
			case 11:
				check(values, "content_type", block.Values[0])
				check(values, "whitepoint", block.Values[1]&15)
				check(values, "reference_mode_flag", block.Values[1]&16 != 0)
				check(values, "reserved_byte2", block.Values[2])
				check(values, "reserved_byte3", block.Values[3])
			case 253:
				raw := values["bytes"].([]any)
				if len(raw) != int(block.Length) {
					t.Fatal("filler length")
				}
				for _, v := range raw {
					if v != float64(0x55) {
						t.Fatal(v)
					}
				}
			default:
				for j, v := range block.Values {
					check(values, fields[block.Level][j], v)
				}
			}
		}
	}
	if index != len(d.Blocks) {
		t.Fatal("block count")
	}
}

func TestOptionalOracleParity(t *testing.T) {
	binary := os.Getenv("DOVI_ORACLE")
	dir := os.Getenv("DOVI_REFERENCE_DIR")
	if binary == "" || dir == "" {
		t.Skip("set pinned DOVI_ORACLE and DOVI_REFERENCE_DIR")
	}
	for _, name := range []string{"profile5.bin", "profile8.bin", "profile84.bin", "mel_orig.bin", "fel_orig.bin", "cmv40_full_rpu.bin", "empty_dmv1_blocks.bin", "data_before_crc32.bin", "level253-ext-metadata.bin", "mel_variable_l8_length13.bin"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "assets", "tests", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			e, err := ReadRPU(t.Context(), bytes.NewReader(data), Options{})
			if err != nil {
				t.Fatal(err)
			}
			oracleFields(t, binary, path, e.Records)
		})
	}
	for _, o := range []f.Options{{Profile: 5, Min: 4095, Max: 0, Avg: 2048}, {Profile: 7}, {Profile: 7, FEL: true}, {Profile: 8, CM40: true}, {Profile: 8, Reuse: true}, {Profile: 8, Uncompressed: true}, {Profile: 8, NoL1: true}, {Profile: 8, Float: true}, {Profile: 8, MMR: true}, {Profile: 8, MMR: true, Float: true}} {
		data := f.File(o)
		path := filepath.Join(t.TempDir(), "synthetic.bin")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		e, err := ReadRPU(t.Context(), bytes.NewReader(data), Options{})
		if err != nil {
			t.Fatal(err)
		}
		oracleFields(t, binary, path, e.Records)
	}
	for _, name := range []string{"regular.hevc", "regular.mkv", "no_aud_injected.hevc", "regular_start_code_4_muxed_el.hevc"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "assets", "hevc_tests", name)
			input, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			e, err := Extract(t.Context(), input, Options{KeepRaw: true})
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := WriteRPU(&got, e); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "RPU.bin")
			cmd := exec.Command(binary, "extract-rpu", "-i", path, "-o", output)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			want, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, got.Bytes()) {
				t.Fatal("presentation-ordered raw export differs")
			}
			oracleFields(t, binary, output, e.Records)
		})
	}
}
