package extract

import (
	"context"
	dovi "github.com/Audionut/go-dovi-extract"
	"io"
	"reflect"
	"slices"
)

type Timestamp = dovi.Timestamp
type StreamKey = dovi.StreamKey
type Picture = dovi.Picture

// Payload owns a parsed record and optionally its original escaped bytes.
type Payload struct {
	RPU dovi.RPU
	Raw []byte
}

// Extraction owns a completed metadata timeline. File reads release shared operation
// charges before returning; playlist results retain charges until Release.
// RetainedBytes bounds overlap with conversion copies in either case.
type Extraction struct {
	Records                      []Payload
	Pictures                     []Picture
	Timeline                     dovi.Timeline
	Complete                     bool
	ObservedRPUs, SuppressedRPUs uint64
	TrackID                      uint64
	retainedBytes                int64
	memory                       *accounting
}

// RetainedBytes reports a conservative bound on extraction-owned storage at
// completion, including resolver scratch that may already be collectible. It
// excludes caller mutations and runtime overhead. Use it to bound overlapping
// conversion copies; it is not a process RSS measurement.
func (e *Extraction) RetainedBytes() int64 {
	if e == nil {
		return 0
	}
	if e.memory != nil {
		return e.memory.used
	}
	return e.retainedBytes
}

// Release discards this result and returns any retained playlist charges.
// Callers must not use aliases to its data after release.
func (e *Extraction) Release() {
	if e == nil {
		return
	}
	if e.memory != nil {
		e.memory.release()
	}
	e.Pictures, e.Records = nil, nil
	e.retainedBytes = 0
}

func (e *Extraction) model() *dovi.Analysis {
	a := &dovi.Analysis{SchemaVersion: 1, Timeline: e.Timeline, Complete: e.Complete, TrackID: e.TrackID, Pictures: e.Pictures, ObservedRPUs: e.ObservedRPUs, SuppressedRPUs: e.SuppressedRPUs}
	if e.Timeline == dovi.PlaylistFrames {
		a.SchemaVersion = 2
	}
	a.Records = make([]dovi.RPU, len(e.Records))
	for i := range e.Records {
		a.Records[i] = e.Records[i].RPU
	}
	return a
}

// Analysis returns an owned, independently budgeted copy.
func (e *Extraction) Analysis(opts dovi.AnalysisOptions) (*dovi.Analysis, error) {
	if e == nil {
		return nil, dovi.ErrInvalidMetadata
	}
	maxRecords := opts.MaxRecords
	if maxRecords == 0 {
		maxRecords = 2000000
	}
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = 512 << 20
	}
	if maxRecords < 0 || maxBytes < 0 {
		return nil, dovi.ErrInvalidOptions
	}
	if len(e.Records) > maxRecords || len(e.Pictures) > maxRecords || int64(len(e.Records)) > maxBytes/256 {
		return nil, dovi.ErrLimit
	}
	// Charge the temporary flattened view in addition to the deep copy.
	opts.MaxBytes = maxBytes - int64(len(e.Records))*256
	if opts.MaxBytes <= 0 {
		return nil, dovi.ErrLimit
	}
	return e.model().Clone(opts)
}

func makeExtraction(ctx context.Context, pictures []resolvedPicture, l Limits, budget *MemoryBudget, observed uint64, parent *accounting) (*Extraction, error) {
	if len(pictures) == 0 {
		return nil, fieldError("no-output-pictures", ErrIncomplete)
	}
	if int64(len(pictures)) > l.MaxFrames {
		return nil, ErrResourceLimit
	}
	if _, err := parent.reserve(int64(len(pictures)) * 512); err != nil {
		return nil, err
	}
	e := &Extraction{Timeline: dovi.PresentationFrames, Complete: true, ObservedRPUs: observed, TrackID: pictures[0].picture.Stream.TrackID, Pictures: make([]Picture, len(pictures)), Records: make([]Payload, 0, len(pictures))}
	for i, p := range pictures {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e.Pictures[i] = p.picture
		e.Pictures[i].POCKnown = true
		if p.payload != nil {
			e.Pictures[i].HasRPU = true
			e.Pictures[i].RPUIndex = uint32(len(e.Records))
			e.Records = append(e.Records, *p.payload)
		}
	}
	if observed < uint64(len(e.Records)) {
		return nil, fieldError("observed-RPU-count", ErrIncomplete)
	}
	e.SuppressedRPUs = observed - uint64(len(e.Records))
	e.retainedBytes = parent.used
	return e, nil
}

// WriteRPU validates every raw record before writing. Sparse video is refused;
// an IO error can leave partial output, whose publication the caller owns.
func WriteRPU(w io.Writer, e *Extraction) error {
	if w == nil {
		return dovi.ErrInvalidOptions
	}
	a, err := e.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		return err
	}
	if a.Timeline != dovi.RPUSequence && len(a.Records) != len(a.Pictures) {
		return fieldError("sparse-RPU-export", ErrIncomplete)
	}
	for _, p := range e.Records {
		if len(p.Raw) > 1<<20 {
			return dovi.ErrLimit
		}
		if len(p.Raw) == 0 {
			return fieldError("raw-not-retained", ErrRawUnavailable)
		}
		nal := append([]byte{0x7c, 1}, p.Raw...)
		parsed, err := dovi.ParseNAL(nal, dovi.ParseOptions{})
		if err != nil {
			return err
		}
		// The extraction model is mutable. Raw export must not silently disagree
		// with the retained metadata after caller edits.
		if !equalRPU(parsed, p.RPU) {
			return dovi.ErrInvalidMetadata
		}
	}
	for _, p := range e.Records {
		for _, data := range [][]byte{{0, 0, 0, 1}, p.Raw} {
			n, err := w.Write(data)
			if err != nil {
				return err
			}
			if n != len(data) {
				return io.ErrShortWrite
			}
		}
	}
	return nil
}

func equalRPU(a, b dovi.RPU) bool {
	// Empty collections carry no presence information. Serializers may restore
	// nil slices as empty slices; optional metadata pointers still carry presence.
	if !slices.Equal(a.Remaining, b.Remaining) || (a.DM == nil) != (b.DM == nil) {
		return false
	}
	a.Remaining, b.Remaining = nil, nil
	if a.DM != nil {
		if !slices.EqualFunc(a.DM.Blocks, b.DM.Blocks, func(x, y dovi.Block) bool {
			return x.Level == y.Level && x.Length == y.Length && slices.Equal(x.Values, y.Values)
		}) {
			return false
		}
		ad, bd := *a.DM, *b.DM
		ad.Blocks, bd.Blocks = nil, nil
		a.DM, b.DM = &ad, &bd
	}
	return reflect.DeepEqual(a, b)
}

// Conservative 64-bit allocation classes, including slice capacity rounding.
func parsedStorage(r dovi.RPU) int64 {
	n := int64(256 + cap(r.Remaining))
	if d := r.DM; d != nil {
		n += 256 + int64(cap(d.Blocks))*64
		for _, b := range d.Blocks {
			n += int64(cap(b.Values)) * 8
		}
	}
	return n
}
