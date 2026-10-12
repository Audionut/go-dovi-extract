package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"

	dovi "github.com/Audionut/go-dovi-extract"
)

// Framing identifies the elementary-stream NAL envelope.
type Framing uint8

const (
	AnnexB Framing = iota
	LengthPrefixed
)

// MarkerKind identifies a sample boundary or an unsupported discontinuity.
type MarkerKind uint8

const (
	SampleStart MarkerKind = iota
	Discontinuity
	// PESStart carries transport timing without asserting a picture boundary.
	PESStart
	// SourceEnd records the exclusive physical packet extent at normal EOF.
	SourceEnd
)

type Marker struct {
	ESOffset                  uint64
	Kind                      MarkerKind
	PTS, DTS                  Timestamp
	SuppressOutput            bool
	Modulo33                  bool
	PacketIndex, SourceOffset uint64
	HasSourcePosition         bool
	// PESHeader is validated during Push and never retained.
	PESHeader []byte
}

// Chunk borrows data and markers until Push returns. ES offsets are contiguous.
type Chunk struct {
	Data     []byte
	ESOffset uint64
	Markers  []Marker
}

// StreamOptions configures a single-owner elementary stream collector.
type StreamOptions struct {
	Key              StreamKey
	Framing          Framing
	LengthSize       int
	CodecConfig      []byte
	PresentationEdit *PresentationEdit
	KeepRaw          bool
	Limits           Limits
	Budget           *MemoryBudget
}

type nalEvent struct {
	data           []byte
	offset         uint64
	kind, temporal uint8
	update         *Payload
	truncated      bool
	endOffset      uint64
}

// Clip retains normally finished syntax for reader-free presentation assembly.
// It must be released after the final assembly using it.
type Clip struct {
	key         StreamKey
	events      []nalEvent
	markers     []Marker
	memory      *accounting
	limits      Limits
	observed    uint64
	packetRange *PacketRange
	released    bool
	edit        *PresentationEdit
}

// Stream consumes a compressed stream synchronously. It is not safe for
// concurrent method calls. Different streams may share a MemoryBudget.
type Stream struct {
	sourceEnded                               bool
	opts                                      StreamOptions
	limits                                    Limits
	memory                                    *accounting
	scanner                                   nalScanner
	bufferCharge, eventsCharge, markersCharge *charge
	events                                    []nalEvent
	markers                                   []Marker
	observed                                  uint64
	frames                                    int64
	nextOffset                                uint64
	terminal                                  error
	finished                                  bool
	sequences                                 [16]*sequence
	pictures                                  [64]*pictureSet
	sequenceCharges                           [16]*charge
	parameters                                [3]map[string][]byte
	parameterBytes                            int64
}

// NewStream constructs a bounded collector and copies needed hvcC syntax. Input
// configuration is borrowed only for this call. No goroutines are started.
func NewStream(opts StreamOptions) (*Stream, error) {
	l, err := opts.Limits.normalized()
	if err != nil {
		return nil, err
	}
	a := &accounting{max: l.MaxRetainedBytes, shared: opts.Budget}
	s, err := newStream(opts, l, a)
	if err != nil {
		a.release()
	}
	return s, err
}

func newStream(opts StreamOptions, l Limits, a *accounting) (*Stream, error) {
	if err := opts.PresentationEdit.validate(); err != nil {
		return nil, err
	}
	if opts.Framing > LengthPrefixed || opts.Framing == AnnexB && opts.LengthSize != 0 || opts.Framing == LengthPrefixed && (opts.LengthSize < 1 || opts.LengthSize > 4) {
		return nil, fmt.Errorf("%w: framing/length size", dovi.ErrInvalidOptions)
	}
	if _, err := a.reserve(4096); err != nil {
		return nil, err
	}
	s := &Stream{opts: opts, limits: l, memory: a}
	if opts.PresentationEdit != nil {
		edit := *opts.PresentationEdit
		s.opts.PresentationEdit = &edit
	}
	s.opts.CodecConfig = nil
	s.scanner = nalScanner{lengthSize: opts.LengthSize, maxParameter: l.MaxParameterSetBytes, maxSlice: l.MaxSliceHeaderBytes, maxSEI: l.MaxRPUBytes, end: s.nal, grow: s.growBuffer, prefix: s.slicePrefix}
	if opts.Framing == LengthPrefixed && len(opts.CodecConfig) == 0 {
		return nil, fieldError("missing-hvcC", ErrUnsupportedInput)
	}
	if len(opts.CodecConfig) != 0 {
		if err := s.config(opts.CodecConfig); err != nil {
			s.Abort(err)
			return nil, err
		}
	}
	return s, nil
}

func (s *Stream) slicePrefix(data []byte) (bool, error) {
	if (uint16(data[0]&1)<<5)|uint16(data[1]>>3) != 0 {
		return true, nil
	}
	c, err := s.memory.reserve(int64(len(data)))
	if err != nil {
		return false, err
	}
	defer c.release()
	rbsp, err := unescape(data[2:], false, true)
	if err != nil {
		return false, err
	}
	b := bitReader{data: rbsp}
	b.flag()
	kind := (data[0] >> 1) & 63
	if kind >= 16 && kind <= 23 {
		b.flag()
	}
	id := b.boundedUE(63)
	if b.err != nil {
		if errors.Is(b.err, io.ErrUnexpectedEOF) {
			if int64(len(data)) >= s.limits.MaxSliceHeaderBytes {
				return false, fieldError("slice-header-bytes", ErrResourceLimit)
			}
			return false, nil
		}
		return false, syntaxError(&b, "slice-prefix")
	}
	p := s.pictures[id]
	if p == nil || s.sequences[p.sps] == nil {
		return false, nil
	}
	_, err = parseSlice(rbsp, kind, (data[1]&7)-1, *p, *s.sequences[p.sps])
	if errors.Is(err, ErrIncomplete) {
		if int64(len(data)) >= s.limits.MaxSliceHeaderBytes {
			return false, fieldError("slice-header-bytes", ErrResourceLimit)
		}
		return false, nil
	}
	return err == nil, err
}

func (s *Stream) growBuffer(capacity int) error {
	c, err := s.memory.reserve(int64(capacity))
	if err != nil {
		return err
	}
	buf := make([]byte, len(s.scanner.data), capacity)
	copy(buf, s.scanner.data)
	s.scanner.data = buf
	s.bufferCharge.release()
	s.bufferCharge = c
	return nil
}

// Configure replaces hvcC at a complete sample boundary. Failure terminates the
// collector; configuration bytes are not retained.
func (s *Stream) Configure(ctx context.Context, data []byte) error {
	if s == nil || ctx == nil {
		return dovi.ErrInvalidOptions
	}
	if s.terminal != nil {
		return s.terminal
	}
	if s.finished || s.sourceEnded {
		return fieldError("finished-stream", ErrIncomplete)
	}
	err := ctx.Err()
	if err == nil {
		if s.opts.Framing != LengthPrefixed || len(data) < 23 || data[0] != 1 {
			err = fieldError("hvcC", ErrInvalidBitstream)
		} else {
			s.opts.LengthSize = int(data[21]&3) + 1
			s.scanner.lengthSize = s.opts.LengthSize
			err = s.config(data)
		}
	}
	if err != nil {
		s.Abort(err)
	}
	return err
}

func (s *Stream) config(data []byte) error {
	if s.scanner.remaining != 0 || s.scanner.lengthRead != 0 {
		return fieldError("sample-NAL-boundary", errors.Join(ErrInvalidBitstream, ErrIncomplete))
	}
	if s.opts.Framing != LengthPrefixed || len(data) < 23 || data[0] != 1 || int(data[21]&3)+1 != s.opts.LengthSize {
		return fmt.Errorf("%w: hvcC/framing", dovi.ErrInvalidOptions)
	}
	pos := 23
	for range int(data[22]) {
		if len(data)-pos < 3 {
			return fieldError("hvcC", ErrInvalidBitstream)
		}
		kind := data[pos] & 63
		count := int(data[pos+1])<<8 | int(data[pos+2])
		pos += 3
		for range count {
			if len(data)-pos < 2 {
				return fieldError("hvcC", ErrInvalidBitstream)
			}
			n := int(data[pos])<<8 | int(data[pos+1])
			pos += 2
			if n < 2 || n > len(data)-pos {
				return fieldError("hvcC-extent", ErrInvalidBitstream)
			}
			limit := s.limits.MaxParameterSetBytes
			if kind == 39 || kind == 40 {
				limit = s.limits.MaxRPUBytes
			}
			if !ignoredNAL(data[pos:pos+n]) && int64(n) > limit {
				return fieldError("hvcC-NAL-bytes", ErrResourceLimit)
			}
			if (data[pos]>>1)&63 != kind {
				return fieldError("hvcC-NAL-kind", ErrInvalidBitstream)
			}
			if kind >= 32 && kind <= 34 || kind == 39 || kind == 40 {
				if err := s.nal(data[pos:pos+n], s.nextOffset, uint64(n)); err != nil {
					return err
				}
			}
			pos += n
		}
	}
	if pos != len(data) {
		return fieldError("hvcC-trailing-data", ErrInvalidBitstream)
	}
	return nil
}

func (s *Stream) nal(data []byte, offset, size uint64) error {
	if len(data) < 2 || data[0]&0x80 != 0 || data[1]&7 == 0 {
		return fieldError("NAL-header", ErrInvalidBitstream)
	}
	kind := (data[0] >> 1) & 63
	if data[0]&1 != 0 || data[1]>>3 != 0 {
		return fieldError("multilayer-HEVC", ErrUnsupportedInput)
	}
	// Annex B NALs finish after their bytes have been pushed. Sample boundaries
	// cannot split a NAL; transport PES boundaries explicitly may do so.
	marker := sort.Search(len(s.markers), func(i int) bool { return s.markers[i].ESOffset > offset })
	for ; marker < len(s.markers) && s.markers[marker].ESOffset < offset+size; marker++ {
		if s.markers[marker].Kind == SampleStart {
			return fieldError("sample-NAL-boundary", ErrInvalidBitstream)
		}
	}
	if ignoredNAL(data) {
		return nil
	} // Ancillary bodies never add base pictures or Dolby Vision metadata.
	if kind <= 31 && (kind > 21 || kind >= 10 && kind <= 15) {
		return fieldError("reserved-VCL-kind", ErrUnsupportedInput)
	}
	c, err := s.memory.reserve(int64(len(data)) + 512)
	if err != nil {
		return err
	}
	rbsp, err := unescape(data[2:], uint64(len(data)) == size, kind <= 31)
	if err != nil {
		c.release()
		return err
	}
	e := nalEvent{offset: offset, endOffset: offset + size, kind: kind, temporal: (data[1] & 7) - 1, truncated: uint64(len(data)) < size}
	if (kind == 32 || kind == 33 || kind == 36 || kind == 37) && e.temporal != 0 {
		c.release()
		return fieldError("NAL-temporal-id", ErrInvalidBitstream)
	}
	if kind == 62 {
		if data[1]&7 != 1 {
			c.release()
			return fieldError("RPU-temporal-id", ErrUnsupportedInput)
		}
		scratch, err := s.memory.reserve((64 << 10) + int64(len(rbsp)))
		if err != nil {
			c.release()
			return err
		}
		parsed, parseErr := dovi.ParseRPU(rbsp, dovi.ParseOptions{MaxBytes: int(s.limits.MaxRPUBytes)})
		if parseErr != nil {
			scratch.release()
			c.release()
			return parseErr
		}
		retained := parsedStorage(parsed)
		if s.opts.KeepRaw {
			retained += int64(len(data)) * 2
		}
		if _, err = s.memory.reserve(retained); err != nil {
			scratch.release()
			c.release()
			return err
		}
		scratch.release()
		e.update = &Payload{RPU: parsed}
		if s.opts.KeepRaw {
			e.update.Raw = append([]byte(nil), data[2:]...)
		}
		s.observed++
		c.release()
	} else if kind == 39 || kind == 40 {
		if err := validateSEI(rbsp); err != nil {
			c.release()
			return err
		}
		c.release()
		// Keep the prefix boundary even when SEI carries no Dolby Vision payload.
		e.data = nil
	} else if kind <= 31 || kind >= 32 && kind <= 35 || kind == 36 || kind == 37 {
		e.data = rbsp
		if kind >= 32 && kind <= 34 {
			known := s.parameters[kind-32][string(rbsp)]
			parameterData := rbsp
			if known != nil {
				parameterData = known
			}
			if err := s.parameter(kind, e.temporal, parameterData); err != nil {
				c.release()
				return err
			}
			if known != nil {
				e.data = known
				c.release()
			} else {
				if int64(len(rbsp)) > (1<<20)-s.parameterBytes {
					c.release()
					return fieldError("combined-parameter-set-bytes", ErrResourceLimit)
				}
				if _, err := s.memory.reserve(int64(len(rbsp))); err != nil {
					c.release()
					return err
				}
				if s.parameters[kind-32] == nil {
					s.parameters[kind-32] = make(map[string][]byte)
				}
				s.parameters[kind-32][string(rbsp)] = rbsp
				s.parameterBytes += int64(len(rbsp))
			}
		}
		if kind == 35 {
			b := bitReader{data: rbsp}
			if b.read(3) > 2 {
				b.invalid()
			}
			b.trailing()
			if err := syntaxError(&b, "AUD"); err != nil {
				c.release()
				return err
			}
		}
		if (kind == 36 || kind == 37) && len(rbsp) != 0 {
			c.release()
			return fieldError("end-NAL-body", ErrInvalidBitstream)
		}
		if kind <= 31 {
			b := bitReader{data: rbsp}
			if b.flag() {
				s.frames++
				if s.frames > s.limits.MaxFrames {
					c.release()
					return ErrResourceLimit
				}
			}
			if b.err != nil {
				c.release()
				return syntaxError(&b, "slice-header")
			}
			if _, err := s.memory.reserve(int64(len(rbsp)) + 128); err != nil {
				c.release()
				return err
			}
			e.data = append([]byte(nil), rbsp...)
			c.release()
		}
	} else {
		c.release()
		return nil
	}
	if len(s.events) == cap(s.events) {
		capacity := max(16, 2*cap(s.events))
		newCharge, err := s.memory.reserve(int64(capacity) * 96)
		if err != nil {
			c.release()
			return fieldError("NAL-event-index", fmt.Errorf("capacity=%d: %w", capacity, err))
		}
		buf := make([]nalEvent, len(s.events), capacity)
		copy(buf, s.events)
		s.events = buf
		s.eventsCharge.release()
		s.eventsCharge = newCharge
	}
	s.events = append(s.events, e)
	return nil
}

func (s *Stream) parameter(kind, temporal uint8, data []byte) error {
	switch kind {
	case 32:
		_, err := parseVPS(data)
		return err
	case 33:
		c, err := s.memory.reserve(20 << 10)
		if err != nil {
			return err
		}
		v, err := parseSPS(data)
		if err != nil {
			c.release()
			return err
		}
		s.sequences[v.id] = &v
		s.sequenceCharges[v.id].release()
		s.sequenceCharges[v.id] = c
	case 34:
		p, err := parsePPS(data)
		if err != nil {
			return err
		}
		p.temporal = temporal
		s.pictures[p.id] = &p
	}
	return nil
}

// Push consumes borrowed fragments and checks cancellation between bounded
// pieces. A read failure must be passed to Abort by the producer, never Finish.
func (s *Stream) Push(ctx context.Context, chunk Chunk) error {
	if s == nil || s.memory == nil {
		return fmt.Errorf("%w: nil stream", dovi.ErrInvalidOptions)
	}
	if s.terminal != nil {
		return s.terminal
	}
	if s.finished {
		return fieldError("finished-stream", ErrIncomplete)
	}
	fail := func(err error) error { s.Abort(err); return err }
	if s.sourceEnded {
		return fail(fieldError("bytes-after-source-end", ErrIncomplete))
	}
	if ctx == nil {
		return fail(fmt.Errorf("%w: nil context", dovi.ErrInvalidOptions))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if chunk.ESOffset != s.nextOffset || uint64(len(chunk.Data)) > math.MaxUint64-s.nextOffset {
		return fail(fieldError("ES-offset", ErrIncomplete))
	}
	end := s.nextOffset + uint64(len(chunk.Data))
	for index, m := range chunk.Markers {
		if len(m.PESHeader) != 0 {
			if m.Kind != PESStart {
				return fail(fieldError("PES-header-marker", dovi.ErrInvalidOptions))
			}
			if err := validatePESHeader(m.PESHeader); err != nil {
				return fail(err)
			}
			m.PESHeader = nil
		}
		if m.Kind == SourceEnd {
			if !m.HasSourcePosition || m.PacketIndex == 0 || m.ESOffset != end || index != len(chunk.Markers)-1 || m.PTS.Valid || m.DTS.Valid || m.Modulo33 || m.SuppressOutput {
				return fail(fieldError("source-end-marker", ErrInvalidBitstream))
			}
			for i := len(s.markers) - 1; i >= 0; i-- {
				if s.markers[i].HasSourcePosition {
					if m.PacketIndex <= s.markers[i].PacketIndex {
						return fail(fieldError("source-end-packet", ErrInvalidBitstream))
					}
					break
				}
			}
			s.sourceEnded = true
		}
		if m.SuppressOutput && m.Kind != SampleStart {
			return fail(fieldError("output-suppression-marker", dovi.ErrInvalidOptions))
		}
		if m.Kind > SourceEnd || m.ESOffset < chunk.ESOffset || m.ESOffset > end || len(s.markers) > 0 && m.ESOffset < s.markers[len(s.markers)-1].ESOffset || m.PTS.Valid && m.PTS.Timescale == 0 || m.DTS.Valid && m.DTS.Timescale == 0 {
			return fail(fieldError("timing-marker", ErrInvalidBitstream))
		}
		if m.Kind == Discontinuity {
			return fail(fieldError("discontinuity", ErrIncomplete))
		}
		if m.Modulo33 && (m.Kind != PESStart || m.PTS.Valid && (m.PTS.Timescale != 90000 || m.PTS.Value < 0 || m.PTS.Value >= 1<<33) || m.DTS.Valid && (m.DTS.Timescale != 90000 || m.DTS.Value < 0 || m.DTS.Value >= 1<<33)) {
			return fail(fieldError("transport-clock", ErrInvalidBitstream))
		}
		if len(s.markers) == cap(s.markers) {
			capacity := max(16, 2*cap(s.markers))
			c, err := s.memory.reserve(int64(capacity) * 144)
			if err != nil {
				return fail(fieldError("timing-marker-index", fmt.Errorf("capacity=%d: %w", capacity, err)))
			}
			buf := make([]Marker, len(s.markers), capacity)
			copy(buf, s.markers)
			s.markers = buf
			s.markersCharge.release()
			s.markersCharge = c
		}
		s.markers = append(s.markers, m)
	}
	for pos, marker := 0, 0; pos < len(chunk.Data) || marker < len(chunk.Markers); {
		for marker < len(chunk.Markers) && chunk.Markers[marker].ESOffset == chunk.ESOffset+uint64(pos) {
			if chunk.Markers[marker].Kind == SampleStart && s.opts.Framing == LengthPrefixed && (s.scanner.remaining != 0 || s.scanner.lengthRead != 0) {
				return fail(fieldError("sample-NAL-boundary", errors.Join(ErrInvalidBitstream, ErrIncomplete)))
			}
			marker++
		}
		if pos == len(chunk.Data) {
			break
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		n := min(len(chunk.Data)-pos, 32768)
		if marker < len(chunk.Markers) {
			n = min(n, int(chunk.Markers[marker].ESOffset-chunk.ESOffset)-pos)
		}
		if err := s.scanner.push(chunk.Data[pos : pos+n]); err != nil {
			return fail(err)
		}
		pos += n
	}
	s.nextOffset = end
	return nil
}

// finish transfers collected syntax to the presentation resolver at normal EOF.
func (s *Stream) finish(ctx context.Context) (*Clip, error) {
	if s == nil || s.memory == nil {
		return nil, fmt.Errorf("%w: nil stream", dovi.ErrInvalidOptions)
	}
	if s.terminal != nil {
		return nil, s.terminal
	}
	if s.finished {
		return nil, fieldError("finished-stream", ErrIncomplete)
	}
	if ctx == nil {
		s.Abort(dovi.ErrInvalidOptions)
		return nil, s.terminal
	}
	if err := ctx.Err(); err != nil {
		s.Abort(err)
		return nil, err
	}
	if err := s.scanner.finish(); err != nil {
		s.Abort(err)
		return nil, err
	}
	s.finished = true
	s.bufferCharge.release()
	for _, c := range s.sequenceCharges {
		c.release()
	}
	s.sequences = [16]*sequence{}
	s.pictures = [64]*pictureSet{}
	s.parameters = [3]map[string][]byte{}
	s.scanner.data = nil
	c := &Clip{key: s.opts.Key, events: s.events, markers: s.markers, memory: s.memory, limits: s.limits, observed: s.observed, edit: s.opts.PresentationEdit}
	s.events = nil
	s.markers = nil
	return c, nil
}

// Abort makes a stream terminal and discards its owned state. Repeated calls
// preserve the original cause. A successful Finish has already transferred data.
func (s *Stream) Abort(cause error) {
	if s == nil || s.memory == nil || s.terminal != nil || s.finished {
		return
	}
	if cause == nil {
		cause = ErrIncomplete
	}
	s.terminal = cause
	s.memory.release()
	s.sequences = [16]*sequence{}
	s.pictures = [64]*pictureSet{}
	s.parameters = [3]map[string][]byte{}
	s.events = nil
	s.markers = nil
	s.scanner.data = nil
}
