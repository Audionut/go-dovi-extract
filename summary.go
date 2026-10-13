package dovi

import (
	"cmp"
	"slices"
)

// Coverage describes RPU presence on the output picture timeline; standalone sequences have unknown coverage.
type Coverage string

const (
	CoverageComplete Coverage = "complete"
	CoveragePartial  Coverage = "partial"
	CoverageAbsent   Coverage = "absent"
	CoverageUnknown  Coverage = "unknown"
)

// Counts separates retained output metadata from observed and suppressed source RPUs.
type Counts struct {
	OutputPictures  uint64 `json:"output_pictures"`
	PicturesWithRPU uint64 `json:"pictures_with_rpu"`
	RetainedRPUs    uint64 `json:"retained_rpus"`
	L1Samples       uint64 `json:"l1_samples"`
	MissingPictures uint64 `json:"missing_rpu_pictures"`
	ObservedRPUs    uint64 `json:"observed_source_rpus"`
	SuppressedRPUs  uint64 `json:"suppressed_picture_rpus"`
}
type Inventory struct {
	Value uint64 `json:"value"`
	Count uint64 `json:"count"`
}

// TrimTarget counts encoded L2 target-PQ values or L8 target indices without converting their units.
type TrimTarget struct {
	Level  uint8  `json:"level"`
	Target uint16 `json:"target"`
	Count  uint64 `json:"count"`
}
type Mastering struct {
	SourceMinPQ, SourceMaxPQ uint16
	Count                    uint64
}
type MasteringL6 struct {
	Values L6
	Count  uint64
}

// L1Statistics summarizes native L1 metadata in nits. PQMean fields average PQ
// codes before conversion; they are not arithmetic means of linear luminance.
type L1Statistics struct {
	Samples            uint64  `json:"samples"`
	MinimumNits        float64 `json:"minimum_nits"`
	PeakNits           float64 `json:"peak_nits"`
	MaximumAverageNits float64 `json:"maximum_average_nits"`
	PQMeanPeakNits     float64 `json:"pq_mean_peak_nits"`
	PQMeanAverageNits  float64 `json:"pq_mean_average_nits"`
}

// Raster is supplied only when the L5 coordinate relationship is established.
// Display aspect ratio or container display dimensions do not establish it.
type Raster struct {
	Width, Height   uint32
	CoordinateBasis string
	Applicable      bool
}

// ActiveAreaRange is an inclusive interval of original timeline indices with equal L5 offsets.
// Nil Offsets means missing metadata; active dimensions require an applicable Raster.
type ActiveAreaRange struct {
	Start, End                uint64
	Offsets                   *L5
	Geometry                  string
	ActiveWidth, ActiveHeight uint32
	CoordinateBasis           string
}
type Warning struct {
	Index uint64 `json:"index"`
	Code  string `json:"code"`
}
type MetadataInventory struct {
	Block Block  `json:"block"`
	Count uint64 `json:"count"`
}
type EnhancementInventory struct {
	Type  string `json:"type"`
	Count uint64 `json:"count"`
}

// Summary contains derived observations from validated retained output metadata.
// Scene markers and luminance describe metadata, not independently decoded pictures.
type Summary struct {
	ProfileEvidence   string                 `json:"profile_evidence"`
	EnhancementLayers []EnhancementInventory `json:"enhancement_layers"`
	DisplayAndContent []MetadataInventory    `json:"display_and_content_metadata"`
	SchemaVersion     int                    `json:"schema_version"`
	Timeline          Timeline               `json:"timeline"`
	Complete          bool                   `json:"complete"`
	TrackID           uint64                 `json:"track_id"`
	Coverage          Coverage               `json:"coverage"`
	Counts            Counts                 `json:"counts"`
	Profiles          []Inventory            `json:"profiles"`
	CMVersions        []Inventory            `json:"cm_versions"`
	Levels            []Inventory            `json:"levels"`
	Trims             []TrimTarget           `json:"trim_targets"`
	SceneMarkers      []uint64               `json:"scene_refresh_indices"`
	SourceMastering   []Mastering            `json:"source_mastering_pq"`
	L6Mastering       []MasteringL6          `json:"l6_mastering"`
	ActiveAreas       []ActiveAreaRange      `json:"l5_ranges"`
	L1                *L1Statistics          `json:"l1,omitempty"`
	Warnings          []Warning              `json:"warnings"`
}

// SummaryOptions bounds input validation and derived-output accounting and optionally
// supplies an established L5 raster. Zero limits use the finite AnalysisOptions defaults.
type SummaryOptions struct {
	MaxRecords int
	MaxBytes   int64
	Raster     Raster
}

func (a *Analysis) length() int {
	if a.Timeline == RPUSequence {
		return len(a.Records)
	}
	return len(a.Pictures)
}
func (a *Analysis) record(i int) *RPU {
	if a.Timeline == RPUSequence {
		return &a.Records[i]
	}
	p := a.Pictures[i]
	if !p.HasRPU {
		return nil
	}
	return &a.Records[p.RPUIndex]
}
func (a *Analysis) coverage() Coverage {
	if a.Timeline == RPUSequence {
		return CoverageUnknown
	}
	if len(a.Records) == 0 {
		return CoverageAbsent
	}
	if len(a.Records) == len(a.Pictures) {
		return CoverageComplete
	}
	return CoveragePartial
}

type statisticAccumulator struct {
	count          uint64
	min, max, avg  uint16
	sumMax, sumAvg float64
}

func (s *statisticAccumulator) add(v L1) {
	if s.count == 0 || v.MinPQ < s.min {
		s.min = v.MinPQ
	}
	s.max = max(s.max, v.MaxPQ)
	s.avg = max(s.avg, v.AvgPQ)
	s.sumMax += float64(v.MaxPQ)
	s.sumAvg += float64(v.AvgPQ)
	s.count++
}
func (s statisticAccumulator) result() *L1Statistics {
	if s.count == 0 {
		return nil
	}
	return &L1Statistics{s.count, codeNits(float64(s.min)), codeNits(float64(s.max)), codeNits(float64(s.avg)), codeNits(s.sumMax / float64(s.count)), codeNits(s.sumAvg / float64(s.count))}
}

// Summarize reports only retained output metadata. Metadata refresh markers
// are not independently detected scene cuts; luminance is not measured pixels.
func Summarize(a *Analysis, opts SummaryOptions) (Summary, error) {
	maxRecords, maxBytes, err := operationLimits(AnalysisOptions{opts.MaxRecords, opts.MaxBytes})
	if err != nil {
		return Summary{}, err
	}
	if err := a.Validate(AnalysisOptions{maxRecords, maxBytes}); err != nil {
		return Summary{}, err
	}
	// Bound all worst-case output runs, warning/scene lists, map growth and
	// conversion to sorted slices before allocating any report collections.
	if int64(a.length()) > (maxBytes-16384)/512 {
		return Summary{}, ErrLimit
	}
	if len(opts.Raster.CoordinateBasis) > 128 {
		return Summary{}, ErrInvalidOptions
	}
	used := int64(16384) + int64(a.length())*512
	charge := func(n int64) bool {
		if n > maxBytes-used {
			return false
		}
		used += n
		return true
	}
	s := Summary{ProfileEvidence: "rpu_header_coarse_profile", SchemaVersion: 1, Timeline: a.Timeline, Complete: true, TrackID: a.TrackID, Coverage: a.coverage(), Counts: Counts{OutputPictures: uint64(len(a.Pictures)), RetainedRPUs: uint64(len(a.Records)), ObservedRPUs: a.ObservedRPUs, SuppressedRPUs: a.SuppressedRPUs}}
	if a.Timeline != RPUSequence {
		s.Counts.PicturesWithRPU = uint64(len(a.Records))
		s.Counts.MissingPictures = uint64(len(a.Pictures) - len(a.Records))
	}
	profiles := map[uint64]uint64{}
	cm := map[uint64]uint64{}
	levels := map[uint64]uint64{}
	trims := map[[2]uint16]uint64{}
	masters := map[[2]uint16]uint64{}
	l6 := map[L6]uint64{}
	display := map[[26]int64]uint64{}
	enhancement := map[string]uint64{}
	var stats statisticAccumulator
	for i := range a.length() {
		r := a.record(i)
		var offsets *L5
		if r != nil {
			profiles[uint64(r.Profile)]++
			enhancement[r.EnhancementLayer]++
			if d := r.DM; d != nil {
				if d.CM29 {
					cm[29]++
				}
				if d.CM40 {
					cm[40]++
				}
				if d.SceneRefresh == 1 {
					s.SceneMarkers = append(s.SceneMarkers, uint64(i))
				}
				if d.Base != nil {
					key := [2]uint16{d.Base.SourceMinPQ, d.Base.SourceMaxPQ}
					if masters[key] == 0 && !charge(256) {
						return Summary{}, ErrLimit
					}
					masters[key]++
				}
				if d.L6 != nil {
					if l6[*d.L6] == 0 && !charge(256) {
						return Summary{}, ErrLimit
					}
					l6[*d.L6]++
				}
				for _, b := range d.Blocks {
					levels[uint64(b.Level)]++
					if b.Level == 9 || b.Level == 10 || b.Level == 11 || b.Level == 254 {
						key := [26]int64{int64(b.Level), int64(b.Length)}
						copy(key[2:], b.Values)
						if display[key] == 0 && !charge(1024) {
							return Summary{}, ErrLimit
						}
						display[key]++
					}
					if (b.Level == 2 || b.Level == 8) && len(b.Values) > 0 {
						key := [2]uint16{uint16(b.Level), uint16(b.Values[0])}
						if trims[key] == 0 && !charge(256) {
							return Summary{}, ErrLimit
						}
						trims[key]++
					}
				}
				if d.L1 != nil {
					stats.add(*d.L1)
					if d.L1.MinPQ > d.L1.AvgPQ || d.L1.AvgPQ > d.L1.MaxPQ {
						s.Warnings = append(s.Warnings, Warning{uint64(i), "unusual_l1_relationship"})
					}
				}
				offsets = d.L5
			}
		}
		if len(s.ActiveAreas) > 0 {
			prev := &s.ActiveAreas[len(s.ActiveAreas)-1]
			if (prev.Offsets == nil && offsets == nil) || (prev.Offsets != nil && offsets != nil && *prev.Offsets == *offsets) {
				prev.End = uint64(i)
				continue
			}
		}
		area := ActiveAreaRange{Start: uint64(i), End: uint64(i), Geometry: "missing", CoordinateBasis: opts.Raster.CoordinateBasis}
		if offsets != nil {
			area.Offsets = new(*offsets)
			area.Geometry = "unknown"
			raster := opts.Raster
			if raster.Applicable && raster.CoordinateBasis != "" {
				area.Geometry = "invalid"
				w := uint32(offsets.Left) + uint32(offsets.Right)
				h := uint32(offsets.Top) + uint32(offsets.Bottom)
				if w < raster.Width && h < raster.Height {
					area.Geometry = "valid"
					area.ActiveWidth = raster.Width - w
					area.ActiveHeight = raster.Height - h
				}
			}
		}
		s.ActiveAreas = append(s.ActiveAreas, area)
	}
	toInventory := func(m map[uint64]uint64) []Inventory {
		out := make([]Inventory, 0, len(m))
		for v, n := range m {
			out = append(out, Inventory{v, n})
		}
		slices.SortFunc(out, func(a, b Inventory) int { return cmp.Compare(a.Value, b.Value) })
		return out
	}
	s.Profiles = toInventory(profiles)
	s.CMVersions = toInventory(cm)
	s.Levels = toInventory(levels)
	for k, n := range trims {
		s.Trims = append(s.Trims, TrimTarget{uint8(k[0]), k[1], n})
	}
	slices.SortFunc(s.Trims, func(a, b TrimTarget) int {
		if a.Level != b.Level {
			return cmp.Compare(a.Level, b.Level)
		}
		return cmp.Compare(a.Target, b.Target)
	})
	for k, n := range masters {
		s.SourceMastering = append(s.SourceMastering, Mastering{k[0], k[1], n})
	}
	slices.SortFunc(s.SourceMastering, func(a, b Mastering) int {
		if a.SourceMinPQ != b.SourceMinPQ {
			return cmp.Compare(a.SourceMinPQ, b.SourceMinPQ)
		}
		return cmp.Compare(a.SourceMaxPQ, b.SourceMaxPQ)
	})
	for k, n := range l6 {
		s.L6Mastering = append(s.L6Mastering, MasteringL6{k, n})
	}
	slices.SortFunc(s.L6Mastering, func(a, b MasteringL6) int {
		for _, pair := range [][2]uint16{{a.Values.MaxMastering, b.Values.MaxMastering}, {a.Values.MinMastering, b.Values.MinMastering}, {a.Values.MaxCLL, b.Values.MaxCLL}, {a.Values.MaxFALL, b.Values.MaxFALL}} {
			if pair[0] != pair[1] {
				return cmp.Compare(pair[0], pair[1])
			}
		}
		return 0
	})
	for _, kind := range []string{"", "FEL", "MEL"} {
		if n := enhancement[kind]; n > 0 {
			label := kind
			if label == "" {
				label = "unknown_or_not_applicable"
			}
			s.EnhancementLayers = append(s.EnhancementLayers, EnhancementInventory{label, n})
		}
	}
	for key, n := range display {
		level, length := uint8(key[0]), uint16(key[1])
		count := 2
		switch level {
		case 9:
			count = 1
			if length == 17 {
				count = 9
			}
		case 10:
			count = 4
			if length == 21 {
				count = 12
			}
		case 11:
			count = 4
		}
		s.DisplayAndContent = append(s.DisplayAndContent, MetadataInventory{Block{level, length, slices.Clone(key[2 : 2+count])}, n})
	}
	slices.SortFunc(s.DisplayAndContent, func(a, b MetadataInventory) int {
		if a.Block.Level != b.Block.Level {
			return cmp.Compare(a.Block.Level, b.Block.Level)
		}
		if a.Block.Length != b.Block.Length {
			return cmp.Compare(a.Block.Length, b.Block.Length)
		}
		return slices.Compare(a.Block.Values, b.Block.Values)
	})
	s.L1 = stats.result()
	s.Counts.L1Samples = stats.count
	return s, nil
}
