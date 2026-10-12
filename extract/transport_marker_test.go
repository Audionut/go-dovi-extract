package extract

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestPESBoundariesPreserveRPUAssociation(t *testing.T) {
	data := videoFixture(t, -1)
	want, err := Extract(t.Context(), bytes.NewReader(data), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for split := range len(data) + 1 {
		stream, err := NewStream(StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i, part := range [][]byte{data[:split], data[split:]} {
			offset := uint64(0)
			if i == 1 {
				offset = uint64(split)
			}
			if err := stream.Push(t.Context(), Chunk{Data: part, ESOffset: offset, Markers: []Marker{{Kind: PESStart, ESOffset: offset, HasSourcePosition: true, PacketIndex: uint64(i)}}}); err != nil {
				t.Fatalf("split %d: %v", split, err)
			}
		}
		end := uint64(len(data))
		if err := stream.Push(t.Context(), Chunk{ESOffset: end, Markers: []Marker{{Kind: SourceEnd, ESOffset: end, HasSourcePosition: true, PacketIndex: 2}}}); err != nil {
			t.Fatal(err)
		}
		got, err := stream.Finish(t.Context())
		if err != nil || !reflect.DeepEqual(got.Pictures, want.Pictures) || !reflect.DeepEqual(got.Records, want.Records) {
			t.Fatalf("PES split %d changed picture/RPU association: %v", split, err)
		}
	}
}

func TestTransportMarkersRejectInvalidAuthority(t *testing.T) {
	for _, marker := range []Marker{
		{Kind: PESStart, Modulo33: true, PTS: Timestamp{Value: -1, Timescale: 90000, Valid: true}},
		{Kind: PESStart, Modulo33: true, DTS: Timestamp{Value: 1 << 33, Timescale: 90000, Valid: true}},
		{Kind: PESStart, Modulo33: true, PTS: Timestamp{Timescale: 1000, Valid: true}},
		{Kind: PESStart, PESHeader: []byte{0, 0, 1}},
		{Kind: SourceEnd},
		{Kind: Discontinuity},
	} {
		stream, err := NewStream(StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Push(t.Context(), Chunk{Markers: []Marker{marker}}); err == nil {
			t.Fatal("accepted invalid transport authority")
		}
	}
	stream, err := NewStream(StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(t.Context(), Chunk{Markers: []Marker{{Kind: SourceEnd, HasSourcePosition: true, PacketIndex: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(t.Context(), Chunk{Data: []byte{0}}); !errors.Is(err, ErrIncomplete) {
		t.Fatal("bytes accepted after certified EOF")
	}
}
