package extract

import (
	"context"
	"errors"
	dovi "github.com/Audionut/go-dovi-extract"
	"io"
)

// ReadRPU reads a standalone escaped RPU sequence. Video coverage and timing
// remain unknown. The caller owns r, including cancellation of a blocked Read.
func ReadRPU(ctx context.Context, r io.Reader, opts Options) (*Extraction, error) {
	if ctx == nil || r == nil || opts.TrackID != 0 {
		return nil, dovi.ErrInvalidOptions
	}
	l, err := opts.Limits.normalized()
	if err != nil {
		return nil, err
	}
	a := &accounting{max: l.MaxRetainedBytes, shared: opts.Budget}
	defer a.release()
	if l.MaxRPUBytes > (l.MaxRetainedBytes-32768)/3 {
		return nil, ErrResourceLimit
	}
	if _, err := a.reserve(l.MaxRPUBytes*3 + 32768); err != nil {
		return nil, err
	}
	buf := make([]byte, 32768)
	record := make([]byte, 2, int(l.MaxRPUBytes)+2)
	record[0] = 0x7c
	record[1] = 1
	e := &Extraction{Timeline: dovi.RPUSequence}
	var indexCharge *charge
	flush := func() error {
		if len(record) == 2 {
			return fieldError("empty-RPU-record", ErrIncomplete)
		}
		if int64(len(e.Records)) >= l.MaxFrames {
			return ErrResourceLimit
		}
		scratch, err := a.reserve((64 << 10) + int64(len(record)))
		if err != nil {
			return err
		}
		p, err := dovi.ParseNAL(record, dovi.ParseOptions{MaxBytes: int(l.MaxRPUBytes)})
		if err != nil {
			scratch.release()
			return err
		}
		retained := parsedStorage(p)
		if opts.KeepRaw {
			retained += int64(len(record)) * 2
		}
		if _, err := a.reserve(retained); err != nil {
			scratch.release()
			return err
		}
		scratch.release()
		if len(e.Records) == cap(e.Records) {
			capacity := max(16, cap(e.Records)*2)
			c, err := a.reserve(int64(capacity) * 256)
			if err != nil {
				return err
			}
			records := make([]Payload, len(e.Records), capacity)
			copy(records, e.Records)
			e.Records = records
			indexCharge.release()
			indexCharge = c
		}
		payload := Payload{RPU: p}
		if opts.KeepRaw {
			payload.Raw = append([]byte(nil), record[2:]...)
		}
		e.Records = append(e.Records, payload)
		record = record[:2]
		return nil
	}
	started := false
	zeros := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := r.Read(buf)
		for _, v := range buf[:n] {
			if v == 0 {
				zeros++
				if zeros > l.MaxRPUBytes {
					return nil, ErrResourceLimit
				}
				continue
			}
			if v == 1 && zeros >= 2 {
				if started {
					if err := flush(); err != nil {
						return nil, errors.Join(err, nonEOF(readErr))
					}
				}
				started = true
				zeros = 0
				continue
			}
			if !started {
				return nil, fieldError("RPU-start-code", ErrInvalidBitstream)
			}
			if int64(len(record)-2)+zeros+1 > l.MaxRPUBytes {
				return nil, ErrResourceLimit
			}
			for range zeros {
				record = append(record, 0)
			}
			zeros = 0
			record = append(record, v)
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, readErr
			}
			break
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	if !started {
		return nil, ErrIncomplete
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.Complete = true
	e.ObservedRPUs = uint64(len(e.Records))
	e.retainedBytes = a.used
	return e, nil
}
func nonEOF(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}

// Finish validates normal EOF and resolves presentation ordering. A producer
// failure must call Abort instead. A collector can finish only once.
func (s *Stream) Finish(ctx context.Context) (*Extraction, error) {
	if s == nil || s.memory == nil {
		return nil, dovi.ErrInvalidOptions
	}
	defer s.memory.release()
	e, err := finishFile(ctx, s, s.limits, s.opts.Budget)
	if err != nil && s.terminal == nil {
		s.terminal = err
	}
	return e, err
}

// FinishClip transfers normally completed syntax for playlist assembly. The
// caller owns Release; this method and Finish are mutually exclusive.
func (s *Stream) FinishClip(ctx context.Context) (*Clip, error) {
	return s.finish(ctx)
}

// Release relinquishes collected syntax. No borrowed extraction or other alias
// may be used afterward. Repeated calls are harmless.
func (c *Clip) Release() {
	if c == nil || c.released {
		return
	}
	c.released = true
	c.memory.release()
	c.events, c.markers = nil, nil
}
