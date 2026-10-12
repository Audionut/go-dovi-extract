package extract

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	dovi "github.com/Audionut/go-dovi-extract"
)

// Format is an input envelope. Auto examines signatures, never extensions.
type Format uint8

const (
	Auto Format = iota
	RawHEVC
	Matroska
	RPUFile
)

// Options selects a container stream and finite limits. TrackID zero requires a
// unique HEVC candidate. Raw HEVC rejects nonzero track IDs.
type Options struct {
	Format  Format
	TrackID uint64
	Limits  Limits
	Budget  *MemoryBudget
	KeepRaw bool
}

// Extract reads a caller-owned seeker through normal completion. It currently
// supports standalone RPU, raw Annex B HEVC and Matroska within the documented
// dialect and timing scope. Auto selects signatures rather than extensions.
// Successful extraction validates delivered structure and metadata coverage,
// not entropy-coded picture integrity. It does not close r or decode pixels.
func Extract(ctx context.Context, r io.ReadSeeker, opts Options) (*Extraction, error) {
	if ctx == nil || r == nil || opts.Format > RPUFile {
		return nil, fmt.Errorf("%w: extraction arguments", dovi.ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l, err := opts.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if opts.Format == Auto {
		position, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		memory := &accounting{max: l.MaxRetainedBytes, shared: opts.Budget}
		if _, err := memory.reserve(4096); err != nil {
			return nil, err
		}
		opts.Format, err = detectFormat(ctx, r, l.MaxRPUBytes)
		memory.release()
		_, seekErr := r.Seek(position, io.SeekStart)
		if err = errors.Join(err, seekErr); err != nil {
			return nil, err
		}
	}
	if opts.Format == Matroska {
		return extractMatroska(ctx, r, opts, l)
	}
	if opts.Format == RPUFile {
		return ReadRPU(ctx, r, opts)
	}
	if opts.Format != RawHEVC {
		return nil, fieldError("container-adapter", ErrUnsupportedInput)
	}
	if opts.TrackID != 0 {
		return nil, fieldError("raw-track-ID", dovi.ErrInvalidOptions)
	}
	s, err := NewStream(StreamOptions{Limits: l, Budget: opts.Budget, KeepRaw: opts.KeepRaw})
	if err != nil {
		return nil, err
	}
	defer s.memory.release()
	if _, err := s.memory.reserve(32768); err != nil {
		s.Abort(err)
		return nil, err
	}
	buffer := make([]byte, 32768)
	var offset uint64
	for {
		if err := ctx.Err(); err != nil {
			s.Abort(err)
			return nil, err
		}
		n, readErr := r.Read(buffer)
		if n > 0 {
			if err := s.Push(ctx, Chunk{Data: buffer[:n], ESOffset: offset}); err != nil {
				if readErr != nil && readErr != io.EOF {
					return nil, errors.Join(err, readErr)
				}
				return nil, err
			}
			offset += uint64(n)
		}
		if readErr != nil {
			if readErr != io.EOF {
				s.Abort(readErr)
				return nil, readErr
			}
			break
		}
		if n == 0 {
			s.Abort(io.ErrNoProgress)
			return nil, io.ErrNoProgress
		}
	}
	return finishFile(ctx, s, l, opts.Budget)
}

// probeInput preserves errors returned with read-ahead bytes. bufio may satisfy
// a signature request without surfacing its cached error before we discard it.
type probeInput struct {
	io.Reader
	err error
}

func (r *probeInput) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func detectFormat(ctx context.Context, r io.Reader, maxZeros int64) (format Format, resultErr error) {
	input := &probeInput{Reader: r}
	defer func() { resultErr = errors.Join(resultErr, input.err) }()
	probe := bufio.NewReaderSize(input, 4096)
	head, err := probe.Peek(4)
	if err != nil {
		return Auto, err
	}
	if binary.BigEndian.Uint32(head) == 0x1a45dfa3 {
		return Matroska, nil
	}
	var zeros int64
	for {
		if err := ctx.Err(); err != nil {
			return Auto, err
		}
		v, err := probe.ReadByte()
		if err != nil {
			return Auto, err
		}
		if v == 0 {
			zeros++
			if zeros > maxZeros {
				return Auto, ErrResourceLimit
			}
			continue
		}
		if zeros < 2 || v != 1 {
			return Auto, fieldError("unrecognized-input", ErrUnsupportedInput)
		}
		v, err = probe.ReadByte()
		if err != nil {
			return Auto, err
		}
		if v == 0x19 {
			return RPUFile, nil
		}
		return RawHEVC, nil
	}
}
