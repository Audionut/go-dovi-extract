// SPDX-License-Identifier: MIT
// Playlist assembly adapted from go-hdr10-plus, Copyright (c) 2026 Audionut.
package extract

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"sort"

	dovi "github.com/Audionut/go-dovi-extract"
)

// Connection explicitly classifies a logical decode splice. Zero is unknown.
type Connection uint8

const (
	ConnectionUnknown Connection = iota
	ConnectionReset
	ConnectionSeamless
)

// Occurrence selects a half-open source interval in 45-kHz ticks. ClockOffset
// maps unwrapped source PTS to that interval's epoch; zero requires Valid=true.
// ConnectionSeamless asserts verified decode continuity, not just equal PIDs.
type Occurrence struct {
	Key                    StreamKey
	Angle                  uint32
	In, Out, PlaylistStart int64
	ClockOffset            Timestamp
	Connection             Connection
	Packets                *PacketRange
}

// PacketRange selects one proven transport clock epoch. Packet positions refer
// to the producer's original container; End is exclusive. ClockAnchor supplies
// an exact reference for unwrapping the first modulo timestamp in this epoch.
// Start must be zero or an observed PESStart; End must be an observed PESStart
// or SourceEnd. Sparse PES positions cannot prove boundaries inside a PES.
type PacketRange struct {
	Start, End  uint64
	ClockAnchor Timestamp
}

// Playlist is a caller-supplied ordered timeline; no source reader is accepted.
type Playlist struct {
	ID          string
	Occurrences []Occurrence
}

// AssemblyOptions bounds retained output and replay scratch across occurrences.
type AssemblyOptions struct {
	Limits Limits
	Budget *MemoryBudget
}

func rationalTime(parts ...Timestamp) (Timestamp, error) {
	var sum, term big.Rat
	for _, p := range parts {
		if !p.Valid || p.Timescale == 0 {
			return Timestamp{}, fieldError("missing-timing", ErrUnsupportedInput)
		}
		var num, den big.Int
		num.SetInt64(p.Value)
		den.SetUint64(p.Timescale)
		term.SetFrac(&num, &den)
		sum.Add(&sum, &term)
	}
	if !sum.Num().IsInt64() || !sum.Denom().IsUint64() {
		return Timestamp{}, fieldError("timing-range", ErrUnsupportedInput)
	}
	return Timestamp{Value: sum.Num().Int64(), Timescale: sum.Denom().Uint64(), Valid: true}, nil
}

func trimPoint(p resolvedPicture, o Occurrence) (bool, Timestamp, error) {
	t, err := rationalTime(p.picture.PTS, o.ClockOffset)
	if err != nil {
		return false, Timestamp{}, err
	}
	in := Timestamp{Value: o.In, Timescale: 45000, Valid: true}
	out := Timestamp{Value: o.Out, Timescale: 45000, Valid: true}
	if compareTime(t, in) < 0 || compareTime(t, out) >= 0 {
		return false, Timestamp{}, nil
	}
	in.Value = -in.Value
	output, err := rationalTime(t, in, Timestamp{Value: o.PlaylistStart, Timescale: 45000, Valid: true})
	return true, output, err
}

func prefixThrough(c *Clip, ordinal uint64) *Clip {
	seen := uint64(0)
	nextStart := 0
	pictureActive := false
	for i, e := range c.events {
		if e.kind >= 32 && e.kind <= 35 || e.kind == 39 {
			pictureActive = false
		}
		if e.kind > 31 || len(e.data) == 0 {
			// DV RPUs may follow the selected VCL. Keep that picture's suffix,
			// stopping when prefix syntax commits it and begins the next AU.
			if pictureActive && (e.kind == 62 || e.kind == 40 || e.kind == 36 || e.kind == 37) {
				nextStart = i + 1
			}
			continue
		}
		if e.data[0]&0x80 != 0 {
			if seen > ordinal {
				copy := *c
				copy.events = c.events[:nextStart]
				if len(copy.events) > 0 {
					end := copy.events[len(copy.events)-1].endOffset
					copy.markers = c.markers[:sort.Search(len(c.markers), func(i int) bool { return c.markers[i].ESOffset >= end })]
				}
				return &copy
			}
			seen++
		}
		pictureActive = true
		// Include every segment of this AU, without the following AU's prefix.
		nextStart = i + 1
	}
	return c
}

type splicePredecessor struct {
	base               *resolver
	clip               *Clip
	trace              []Picture
	lastAU, occurrence uint64
}

func cloneResolver(r *resolver) *resolver {
	copy := *r
	copy.queue = slices.Clone(r.queue)
	copy.seen = maps.Clone(r.seen)
	copy.output = slices.Clone(r.output)
	copy.trace = nil
	return &copy
}

// Resolve a splice from its selected predecessor prefix. Only an explicit
// missing reference can extend that prefix, to the matching retained source AU.
// Repeating this with the selected destination prefix excludes lookahead needed
// solely by the destination's unselected suffix. No physical reader is involved.
func replaySplice(ctx context.Context, previous *splicePredecessor, destination *Clip, occurrence uint64, trace *[]Picture) (*resolver, *resolver, error) {
	through := previous.lastAU
	for {
		state := cloneResolver(previous.base)
		if err := state.resolve(ctx, prefixThrough(previous.clip, through), previous.occurrence); err != nil {
			return nil, nil, err
		}
		entry := cloneResolver(state)
		if trace != nil {
			*trace = (*trace)[:0]
			state.trace = trace
		}
		err := state.resolve(ctx, destination, occurrence)
		state.trace = nil
		if err == nil {
			return entry, state, nil
		}
		var missing *missingReference
		if !errors.As(err, &missing) || missing.partial {
			return nil, nil, fieldError("unverified-seamless-splice", errors.Join(ErrUnsupportedInput, err))
		}
		found := false
		matches := 0
		var candidate uint64
		for _, picture := range previous.trace {
			if picture.POC == missing.poc && picture.AUOrdinal > through {
				candidate = picture.AUOrdinal
				found = true
				matches++
			}
		}
		if matches > 1 {
			return nil, nil, fieldError("ambiguous-seamless-reference", ErrUnsupportedInput)
		}
		through = candidate
		if !found {
			return nil, nil, fieldError("unverified-seamless-reference", errors.Join(ErrUnsupportedInput, err))
		}
	}
}

// Assemble replays normally collected clips for each occurrence, then trims
// presentation starts and recomputes profile/scenes. It performs no source IO.
func Assemble(ctx context.Context, p Playlist, clips map[StreamKey]*Clip, opts AssemblyOptions) (*Extraction, error) {
	if ctx == nil || len(p.Occurrences) == 0 {
		return nil, fmt.Errorf("%w: assembly arguments", dovi.ErrInvalidOptions)
	}
	l, err := opts.Limits.normalized()
	if err != nil {
		return nil, err
	}
	for i, o := range p.Occurrences {
		if o.In < 0 || o.Out <= o.In || o.PlaylistStart < 0 || !o.ClockOffset.Valid || o.ClockOffset.Timescale == 0 || o.Connection == ConnectionUnknown || o.Connection > ConnectionSeamless || i == 0 && o.Connection == ConnectionSeamless {
			return nil, fieldError("playlist-boundary-clock", ErrUnsupportedInput)
		}
		if o.Packets != nil && (o.Packets.End <= o.Packets.Start || !o.Packets.ClockAnchor.Valid || o.Packets.ClockAnchor.Timescale != 90000 || o.Packets.ClockAnchor.Value < 0 || o.Packets.ClockAnchor.Value >= 1<<33) {
			return nil, fieldError("source-packet-epoch", ErrUnsupportedInput)
		}
		if i > 0 {
			previous := p.Occurrences[i-1]
			end, err := rationalTime(Timestamp{Value: previous.PlaylistStart, Timescale: 45000, Valid: true}, Timestamp{Value: previous.Out - previous.In, Timescale: 45000, Valid: true})
			if err != nil {
				return nil, err
			}
			if compareTime(Timestamp{Value: o.PlaylistStart, Timescale: 45000, Valid: true}, end) < 0 {
				return nil, fieldError("playlist-overlap", ErrInvalidBitstream)
			}
		}
		c := clips[o.Key]
		if c == nil || c.released || c.key != o.Key || c.edit != nil {
			return nil, fieldError("missing-boundary-clip", ErrIncomplete)
		}
	}
	a := &accounting{max: l.MaxRetainedBytes, shared: opts.Budget}
	defer a.release()
	var count int64
	for _, o := range p.Occurrences {
		n := int64(len(clips[o.Key].events))
		if n > l.MaxRetainedBytes/1024-count {
			return nil, ErrResourceLimit
		}
		count += n
	}
	if _, err := a.reserve(count*1024 + resolutionScratch + pocHistoryScratch); err != nil {
		return nil, err
	}
	r := &resolver{first: true, limits: l, playlist: true}
	var predecessor *splicePredecessor
	var completed []resolvedPicture
	var observed uint64
	for i, o := range p.Occurrences {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if o.Connection == ConnectionReset {
			if err := r.flush(); err != nil {
				return nil, err
			}
			completed = append(completed, r.output...)

			r = &resolver{first: true, limits: l, playlist: true}
			predecessor = nil
		}
		view := *clips[o.Key]
		view.packetRange = o.Packets
		if o.Packets != nil {
			startKnown, endKnown := o.Packets.Start == 0, false
			for _, marker := range view.markers {
				if !marker.HasSourcePosition {
					continue
				}
				if marker.Kind == PESStart {
					startKnown = startKnown || marker.PacketIndex == o.Packets.Start
					endKnown = endKnown || marker.PacketIndex == o.Packets.End
				}
				if marker.Kind == SourceEnd {
					if o.Packets.End > marker.PacketIndex {
						return nil, fieldError("packet-epoch-beyond-source", ErrUnsupportedInput)
					}
					endKnown = endKnown || marker.PacketIndex == o.Packets.End
				}
			}
			if !startKnown || !endKnown {
				return nil, fieldError("unproven-packet-epoch", ErrUnsupportedInput)
			}
		}
		c := &view
		trial := cloneResolver(r)
		var trace []Picture
		trial.trace = &trace
		if predecessor != nil && o.Connection == ConnectionSeamless {
			var err error
			_, trial, err = replaySplice(ctx, predecessor, c, uint64(i), &trace)
			if err != nil {
				return nil, err
			}
		} else if err := trial.resolve(ctx, c, uint64(i)); err != nil {
			return nil, err
		}
		if err := trial.flush(); err != nil {
			return nil, err
		}
		// Preserve observed presence without inheriting the discarded suffix state.
		if c.observed > ^uint64(0)-observed {
			return nil, ErrResourceLimit
		}
		observed += c.observed
		var lastAU uint64
		found := false
		for _, picture := range trial.output {
			if picture.picture.OccurrenceOrdinal != uint64(i) {
				continue
			}
			selected, _, err := trimPoint(picture, o)
			if err != nil {
				return nil, err
			}
			if selected {
				found = true
				lastAU = max(lastAU, picture.picture.AUOrdinal)
			}
		}
		if !found {
			return nil, fieldError("empty-occurrence", ErrIncomplete)
		}
		base := cloneResolver(r)
		if predecessor != nil && o.Connection == ConnectionSeamless {
			var err error
			base, r, err = replaySplice(ctx, predecessor, prefixThrough(c, lastAU), uint64(i), nil)
			if err != nil {
				return nil, err
			}
		} else if err := r.resolve(ctx, prefixThrough(c, lastAU), uint64(i)); err != nil {
			return nil, err
		}
		predecessor = &splicePredecessor{base: base, clip: c, trace: trace, lastAU: lastAU, occurrence: uint64(i)}
	}
	if err := r.complete(); err != nil {
		return nil, err
	}
	completed = append(completed, r.output...)
	var selected []resolvedPicture
	for _, picture := range completed {
		o := p.Occurrences[picture.picture.OccurrenceOrdinal]
		keep, pts, err := trimPoint(picture, o)
		if err != nil {
			return nil, err
		}
		if keep {
			picture.picture.PTS = pts
			selected = append(selected, picture)
		}
	}
	for i := 1; i < len(selected); i++ {
		if compareTime(selected[i-1].picture.PTS, selected[i].picture.PTS) >= 0 {
			return nil, fieldError("playlist-output-order", ErrInvalidBitstream)
		}
	}
	out, err := makeExtraction(ctx, selected, l, opts.Budget, observed, a)
	if err == nil {
		out.Timeline = dovi.PlaylistFrames
		out.TrackID = 0
		// Payload backing may be shared with clips, but stays charged to each
		// returned result so releasing a clip cannot undercount surviving data.
		owned := &accounting{max: l.MaxRetainedBytes - a.used, shared: opts.Budget}
		retain := func(n int64) bool {
			_, err = owned.reserve(n)
			return err == nil
		}
		if !retain(int64(len(out.Pictures)) * 512) {
			owned.release()
			return nil, err
		}
		for _, picture := range out.Pictures {
			if !retain(int64(len(picture.Stream.SourceID) + len(picture.Stream.ClipID))) {
				owned.release()
				return nil, err
			}
		}
		for _, payload := range out.Records {
			if !retain(parsedStorage(payload.RPU) + int64(cap(payload.Raw))) {
				owned.release()
				return nil, err
			}
		}
		out.memory = owned
	}
	return out, err
}
