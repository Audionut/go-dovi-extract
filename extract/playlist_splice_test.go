package extract

import (
	"bytes"
	"errors"
	"testing"

	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
)

func playlistSourceClip(t *testing.T, key StreamKey, data []byte, pts []int64) *Clip {
	t.Helper()
	var markers []Marker
	for pos := 0; pos < len(data); {
		end := len(data)
		if next := bytes.Index(data[pos+3:], []byte{0, 0, 1}); next >= 0 {
			end = pos + 3 + next
		}
		if data[pos+3]>>1&63 <= 31 {
			markers = append(markers, Marker{ESOffset: uint64(pos), Kind: PESStart,
				PTS: Timestamp{Value: pts[len(markers)], Timescale: 90000, Valid: true}})
		}
		pos = end
	}
	s, err := NewStream(StreamOptions{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Push(t.Context(), Chunk{Data: data, Markers: markers}); err != nil {
		t.Fatal(err)
	}
	c, err := s.FinishClip(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Release)
	return c
}

func TestPlaylistSeamlessReferencesDoNotCarryRPUAcrossGap(t *testing.T) {
	predecessor := parameterFixture(t)
	for i, poc := range []uint64{0, 2, 4} {
		kind := byte(1)
		var references []shortReference
		if i == 0 {
			kind = 19
		} else {
			references = []shortReference{{delta: -2}}
			if i == 2 {
				references = append(references, shortReference{delta: -4})
			}
		}
		predecessor = append(predecessor, authoredReferencePicture(poc, kind, 2, references, nil)...)
		predecessor = append(predecessor, 0, 0, 1)
		predecessor = append(predecessor, f.NAL(f.Options{Max: uint16(1000 + i)})...)
	}
	destination := authoredReferencePicture(1, 1, 0,
		[]shortReference{{delta: -1, used: true}}, []shortReference{{delta: 1, used: true}})
	keys := []StreamKey{{SourceID: "authored", ClipID: "predecessor", TrackID: 4113}, {SourceID: "authored", ClipID: "destination", TrackID: 4113}}
	clips := map[StreamKey]*Clip{
		keys[0]: playlistSourceClip(t, keys[0], predecessor, []int64{0, 6000, 12000}),
		keys[1]: playlistSourceClip(t, keys[1], destination, []int64{0}),
	}
	p := Playlist{Occurrences: []Occurrence{
		{Key: keys[0], Out: 1500, ClockOffset: Timestamp{Timescale: 1, Valid: true}, Connection: ConnectionReset},
		{Key: keys[1], Out: 1500, PlaylistStart: 1500, ClockOffset: Timestamp{Timescale: 1, Valid: true}, Connection: ConnectionSeamless},
		{Key: keys[0], Out: 1500, PlaylistStart: 3000, ClockOffset: Timestamp{Timescale: 1, Valid: true}, Connection: ConnectionReset},
		{Key: keys[1], Out: 1500, PlaylistStart: 4500, ClockOffset: Timestamp{Timescale: 1, Valid: true}, Connection: ConnectionSeamless},
	}}
	out, err := Assemble(t.Context(), p, clips, AssemblyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	if len(out.Pictures) != 4 || len(out.Records) != 2 || out.ObservedRPUs != 6 || out.SuppressedRPUs != 4 {
		t.Fatalf("seamless sparse accounting: %#v", out)
	}
	for i, picture := range out.Pictures {
		if picture.OccurrenceOrdinal != uint64(i) || picture.AUOrdinal != 0 || picture.HasRPU != (i%2 == 0) ||
			compareTime(picture.PTS, Timestamp{Value: int64(i * 3000), Timescale: 90000, Valid: true}) != 0 {
			t.Fatalf("picture %d: %#v", i, picture)
		}
		if picture.HasRPU && out.Records[picture.RPUIndex].RPU.DM.L1.MaxPQ != 1000 {
			t.Fatal("unselected predecessor suffix changed metadata")
		}
	}
	var encoded bytes.Buffer
	if err := WriteRPU(&encoded, out); !errors.Is(err, ErrIncomplete) || encoded.Len() != 0 {
		t.Fatalf("sparse playlist RPU export: %v bytes=%d", err, encoded.Len())
	}
	p.Occurrences[1].Connection = ConnectionReset
	if _, err := Assemble(t.Context(), p, clips, AssemblyOptions{}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("reset borrowed predecessor state: %v", err)
	}
}

func TestPlaylistSelectsExactClockEpoch(t *testing.T) {
	key := StreamKey{SourceID: "authored", ClipID: "two-stcs", TrackID: 4113}
	s, err := NewStream(StreamOptions{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	var offset uint64
	for i := range 2 {
		data := parameterFixture(t)
		data = append(data, authoredReferencePicture(0, 19, 2, nil, nil)...)
		data = append(data, 0, 0, 1)
		data = append(data, f.NAL(f.Options{Max: uint16(1000 + i)})...)
		marker := Marker{ESOffset: offset, Kind: PESStart, Modulo33: true, HasSourcePosition: true,
			PacketIndex: uint64(i * 10), PTS: Timestamp{Timescale: 90000, Valid: true},
			PESHeader: []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1}}
		if err := s.Push(t.Context(), Chunk{ESOffset: offset, Data: data, Markers: []Marker{marker}}); err != nil {
			t.Fatal(err)
		}
		offset += uint64(len(data))
	}
	if err := s.Push(t.Context(), Chunk{ESOffset: offset, Markers: []Marker{
		{Kind: SourceEnd, ESOffset: offset, PacketIndex: 20, HasSourcePosition: true},
	}}); err != nil {
		t.Fatal(err)
	}
	clip, err := s.FinishClip(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer clip.Release()
	for i := range 2 {
		occ := Occurrence{Key: key, Out: 1500, Connection: ConnectionReset, ClockOffset: Timestamp{Timescale: 1, Valid: true},
			Packets: &PacketRange{Start: uint64(i * 10), End: uint64((i + 1) * 10), ClockAnchor: Timestamp{Timescale: 90000, Valid: true}}}
		out, err := Assemble(t.Context(), Playlist{Occurrences: []Occurrence{occ}}, map[StreamKey]*Clip{key: clip}, AssemblyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Pictures) != 1 || len(out.Records) != 1 || out.Pictures[0].AUOrdinal != uint64(i) ||
			out.Records[0].RPU.DM.L1.MaxPQ != uint16(1000+i) || compareTime(out.Pictures[0].PTS, Timestamp{Timescale: 1, Valid: true}) != 0 {
			t.Fatalf("epoch %d picked wrong AU: %#v", i, out)
		}
		out.Release()
	}
}

func TestPlaylistModuloClockWrap(t *testing.T) {
	key := StreamKey{SourceID: "authored", ClipID: "wrap", TrackID: 4113}
	s, err := NewStream(StreamOptions{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	var offset uint64
	for i, stamp := range []int64{1<<33 - 3000, 0, 3000} {
		data := parameterFixture(t)
		data = append(data, authoredReferencePicture(0, 19, 2, nil, nil)...)
		data = append(data, 0, 0, 1)
		data = append(data, f.NAL(f.Options{Max: uint16(1000 + i)})...)
		if err := s.Push(t.Context(), Chunk{ESOffset: offset, Data: data, Markers: []Marker{
			{ESOffset: offset, Kind: PESStart, Modulo33: true, PTS: Timestamp{Value: stamp, Timescale: 90000, Valid: true}},
		}}); err != nil {
			t.Fatal(err)
		}
		offset += uint64(len(data))
	}
	clip, err := s.FinishClip(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer clip.Release()
	occ := Occurrence{Key: key, Out: 4500, Connection: ConnectionReset,
		ClockOffset: Timestamp{Value: -(1<<33 - 3000), Timescale: 90000, Valid: true}}
	out, err := Assemble(t.Context(), Playlist{Occurrences: []Occurrence{occ}}, map[StreamKey]*Clip{key: clip}, AssemblyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	if len(out.Pictures) != 3 {
		t.Fatalf("wrapped frame count %d", len(out.Pictures))
	}
	for i, picture := range out.Pictures {
		if picture.AUOrdinal != uint64(i) || compareTime(picture.PTS, Timestamp{Value: int64(i * 3000), Timescale: 90000, Valid: true}) != 0 {
			t.Fatalf("wrapped picture %d: %#v", i, picture)
		}
	}
}
