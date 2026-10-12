package dovi

import (
	"fmt"
	"slices"
)

type Timeline string

const (
	RPUSequence        Timeline = "rpu_sequence"
	PresentationFrames Timeline = "presentation_frames"
	PlaylistFrames     Timeline = "playlist_frames"
)

type Timestamp struct {
	Value     int64
	Timescale uint64
	Valid     bool
}
type StreamKey struct {
	SourceID, ClipID string
	TrackID          uint64
}
type Picture struct {
	Stream                   StreamKey
	OccurrenceOrdinal        uint64
	AUOrdinal, DecodeOrdinal uint64
	POC                      int64
	POCKnown                 bool
	PTS                      Timestamp
	RPUIndex                 uint32
	HasRPU                   bool
}

// Analysis is the version-1 complete, owned metadata timeline. Counts and
// inventories are derived by Summarize, never trusted from a serialized report.
// Standalone sequences have no Pictures. Raw bytes are intentionally separate.
type Analysis struct {
	SchemaVersion  int       `json:"schema_version"`
	Timeline       Timeline  `json:"timeline"`
	Complete       bool      `json:"complete"`
	TrackID        uint64    `json:"track_id"`
	Records        []RPU     `json:"records"`
	Pictures       []Picture `json:"pictures"`
	ObservedRPUs   uint64    `json:"observed_rpus"`
	SuppressedRPUs uint64    `json:"suppressed_rpus"`
}

// AnalysisOptions bounds newly owned storage per call, not process RSS.
type AnalysisOptions struct {
	MaxRecords int
	MaxBytes   int64
}

func operationLimits(o AnalysisOptions) (int, int64, error) {
	if o.MaxRecords < 0 || o.MaxBytes < 0 {
		return 0, 0, ErrInvalidOptions
	}
	if o.MaxRecords == 0 {
		o.MaxRecords = 2000000
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 512 << 20
	}
	return o.MaxRecords, o.MaxBytes, nil
}

// Validate preflights caller-visible sizes and validates the full timeline.
// Complete sparse scans are valid; interrupted scans never are.
func (a *Analysis) Validate(opts AnalysisOptions) error {
	_, err := a.preflight(opts)
	return err
}

// RetainedBytes validates the complete model and reports its conservative owned
// storage bound. It excludes caller aliases and Go runtime overhead.
func (a *Analysis) RetainedBytes(opts AnalysisOptions) (int64, error) {
	return a.preflight(opts)
}
func (a *Analysis) preflight(opts AnalysisOptions) (int64, error) {
	maxRecords, maxBytes, err := operationLimits(opts)
	if err != nil {
		return 0, err
	}
	bad := func(field string) (int64, error) { return 0, fmt.Errorf("%s: %w", field, ErrInvalidMetadata) }
	if a == nil || (a.SchemaVersion != 1 && a.SchemaVersion != 2) || !a.Complete || (a.Timeline == PlaylistFrames) != (a.SchemaVersion == 2) {
		return bad("schema/completion")
	}
	if len(a.Records) > maxRecords || len(a.Pictures) > maxRecords {
		return 0, fmt.Errorf("records limit %d: %w", maxRecords, ErrLimit)
	}
	used := int64(512)
	charge := func(n int, unit int64) bool {
		if int64(n) > (maxBytes-used)/unit {
			return false
		}
		used += int64(n) * unit
		return true
	}
	if maxBytes < used || !charge(len(a.Records), 256) || !charge(len(a.Pictures), 128) {
		return 0, ErrLimit
	}
	for i := range a.Records {
		r := &a.Records[i]
		if len(r.Remaining) > 1<<20 || !charge(len(r.Remaining), 1) {
			return 0, ErrLimit
		}
		if r.Profile != 5 && r.Profile != 7 && r.Profile != 8 {
			return bad("profile")
		}
		if r.EnhancementLayer != "" && r.EnhancementLayer != "MEL" && r.EnhancementLayer != "FEL" {
			return bad("enhancement evidence")
		}
		if (r.DM != nil) != r.Header.DMPresent {
			return bad("DM presence")
		}
		if r.DM == nil {
			if !validateRecord(r) {
				return bad("header")
			}
			continue
		}
		d := r.DM
		if len(d.Blocks) > 256 || !charge(len(d.Blocks), 64) || !charge(1, 256) {
			return 0, ErrLimit
		}
		for _, b := range d.Blocks {
			if len(b.Values) > 24 || !charge(len(b.Values), 8) {
				return 0, ErrLimit
			}
		}
		if d.Compressed != (d.Base == nil) || d.AffectedID > 15 || !d.CM29 {
			return bad("DM base/container")
		}
		if !validateRecord(r) {
			return bad("record fields")
		}
		if d.L1 != nil && (d.L1.MinPQ > 4095 || d.L1.MaxPQ > 4095 || d.L1.AvgPQ > 4095) {
			return bad("L1")
		}
		if d.L5 != nil && (d.L5.Left > 8191 || d.L5.Right > 8191 || d.L5.Top > 8191 || d.L5.Bottom > 8191) {
			return bad("L5")
		}
		if d.L6 != nil && (d.L6.MaxMastering > 10000 || d.L6.MinMastering > 10000 || d.L6.MaxCLL > 10000 || d.L6.MaxFALL > 10000) {
			return bad("L6")
		}
	}
	if a.ObservedRPUs < uint64(len(a.Records)) || a.SuppressedRPUs != a.ObservedRPUs-uint64(len(a.Records)) {
		return bad("source counts")
	}
	switch a.Timeline {
	case RPUSequence:
		if len(a.Pictures) != 0 || len(a.Records) == 0 || a.SuppressedRPUs != 0 || a.TrackID != 0 {
			return bad("RPU sequence")
		}
	case PresentationFrames, PlaylistFrames:
		if len(a.Pictures) == 0 {
			return bad("empty video timeline")
		}
		// Extraction stores records in presentation order, one unique record per
		// containing picture. This makes duplicate/orphan checks allocation-free.
		n := 0
		for _, p := range a.Pictures {
			if p.PTS.Valid && p.PTS.Timescale == 0 || a.Timeline == PresentationFrames &&
				(p.Stream.TrackID != a.TrackID || p.Stream.SourceID != "" || p.Stream.ClipID != "" || p.OccurrenceOrdinal != 0) {
				return bad("picture timing/track")
			}
			if a.Timeline == PlaylistFrames {
				if a.TrackID != 0 || !p.PTS.Valid || p.Stream.SourceID == "" || p.Stream.ClipID == "" || p.Stream.TrackID == 0 {
					return bad("playlist identity")
				}
				if len(p.Stream.SourceID) > 32768 || len(p.Stream.ClipID) > 32768 || !charge(len(p.Stream.SourceID), 1) || !charge(len(p.Stream.ClipID), 1) {
					return 0, ErrLimit
				}
			}
			if p.HasRPU {
				if uint64(p.RPUIndex) != uint64(n) || n >= len(a.Records) {
					return bad("RPU association")
				}
				n++
			} else if p.RPUIndex != 0 {
				return bad("missing RPU index")
			}
		}
		if n != len(a.Records) {
			return bad("orphan RPU")
		}
	default:
		return bad("timeline")
	}
	return used, nil
}

// Clone returns independently owned data after a finite-budget preflight.
func (a *Analysis) Clone(opts AnalysisOptions) (*Analysis, error) {
	if _, err := a.preflight(opts); err != nil {
		return nil, err
	}
	out := *a
	out.Records = make([]RPU, len(a.Records))
	out.Pictures = slices.Clone(a.Pictures)
	for i, r := range a.Records {
		out.Records[i] = cloneRPU(r)
	}
	return &out, nil
}

// cloneRPU copies a parsed record. Callers handling untrusted restored data
// should validate the containing Analysis before requesting owned copies.
func cloneRPU(r RPU) RPU {
	r.Remaining = slices.Clone(r.Remaining)
	if r.DM != nil {
		d := *r.DM
		if d.Base != nil {
			d.Base = new(*d.Base)
		}
		if d.L1 != nil {
			d.L1 = new(*d.L1)
		}
		if d.L5 != nil {
			d.L5 = new(*d.L5)
		}
		if d.L6 != nil {
			d.L6 = new(*d.L6)
		}
		d.Blocks = slices.Clone(d.Blocks)
		for i := range d.Blocks {
			d.Blocks[i].Values = slices.Clone(d.Blocks[i].Values)
		}
		r.DM = &d
	}
	return r
}
