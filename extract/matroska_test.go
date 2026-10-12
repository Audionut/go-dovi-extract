package extract

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"slices"
	"testing"

	ebml "github.com/at-wat/ebml-go"
)

// These containers are constructed independently from their expected timings.
// The HEVC headers and T.35 values come from the authored, checked-in fixtures.
func containerSample(t testing.TB, length int) (config, sample []byte) {
	t.Helper()
	raw := rawFixture(t, "single")
	config = make([]byte, 23)
	config[0], config[21], config[22] = 1, byte(length-1), 3
	for pos := 0; pos < len(raw); {
		next := bytes.Index(raw[pos+3:], []byte{0, 0, 1})
		end := len(raw)
		if next >= 0 {
			end = pos + 3 + next
		}
		nal := slices.Clone(raw[pos+3 : end])
		kind := nal[0] >> 1 & 63
		if kind >= 32 && kind <= 34 {
			config = append(config, kind, 0, 1, byte(len(nal)>>8), byte(len(nal)))
			config = append(config, nal...)
		} else {
			if kind == 19 {
				nal[2] &^= 0x40 // do not discard output of the preceding IDR
			}
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(len(nal)))
			sample = append(sample, size[4-length:]...)
			sample = append(sample, nal...)
		}
		pos = end
	}
	return config, sample
}
func ebmlElement(id uint32, body ...[]byte) []byte {
	var n int
	for _, p := range body {
		n += len(p)
	}
	var ids [4]byte
	binary.BigEndian.PutUint32(ids[:], id)
	i := 0
	for ids[i] == 0 {
		i++
	}
	width := 1
	for uint64(n) >= (uint64(1)<<(7*width))-1 {
		width++
	}
	var sizes [8]byte
	binary.BigEndian.PutUint64(sizes[:], uint64(n)|(uint64(1)<<(7*width)))
	out := append(slices.Clone(ids[i:]), sizes[8-width:]...)
	for _, p := range body {
		out = append(out, p...)
	}
	return out
}
func ebmlUint(id uint32, v uint64) []byte {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], v)
	i := 0
	for i < 7 && data[i] == 0 {
		i++
	}
	return ebmlElement(id, data[i:])
}
func ebmlCRC(body ...[]byte) []byte {
	joined := bytes.Join(body, nil)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(joined))
	return append(ebmlElement(0xbf, crc[:]), joined...)
}
func mkvTestFile(config []byte, duration uint64, cluster, moreTracks []byte, late, unknown bool) []byte {
	head := ebmlElement(mkvHeader, ebmlElement(0x4282, []byte("matroska")))
	info := ebmlElement(mkvInfo, ebmlCRC(ebmlUint(0x2ad7b1, 1)))
	track := ebmlElement(0xae, ebmlUint(0xd7, 1), ebmlUint(0x83, 1), ebmlElement(0x86, []byte("V_MPEGH/ISO/HEVC")), ebmlElement(0x63a2, config), ebmlUint(0x23e383, duration))
	tracks := ebmlElement(mkvTracks, track, moreTracks)
	parts := [][]byte{info, tracks, cluster}
	if late {
		parts = [][]byte{cluster, info, tracks}
	}
	segment := ebmlElement(mkvSegment, ebmlCRC(parts...))
	if unknown {
		segment = append([]byte{0x18, 0x53, 0x80, 0x67, 0xff}, ebmlCRC(parts...)...)
		return bytes.Join([][]byte{head, ebmlElement(0xec, []byte{0}), segment}, nil)
	}
	return bytes.Join([][]byte{head, ebmlElement(0xec, []byte{0}), segment, ebmlElement(0xec)}, nil)
}
func mkvTestBlock(t testing.TB, id uint32, b ebml.Block) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := ebml.MarshalBlock(&b, &data); err != nil {
		t.Fatal(err)
	}
	return ebmlElement(id, data.Bytes())
}
func TestMatroskaLacesAndInvisible(t *testing.T) {
	for length := 1; length <= 4; length++ {
		for _, lace := range []ebml.LacingMode{ebml.LacingNo, ebml.LacingXiph, ebml.LacingFixed, ebml.LacingEBML} {
			config, sample := containerSample(t, length)
			count := 3
			if lace == ebml.LacingNo {
				count = 1
			}
			frames := make([][]byte, count)
			for i := range frames {
				frames[i] = sample
			}
			block := mkvTestBlock(t, 0xa1, ebml.Block{TrackNumber: 1, Lacing: lace, Data: frames})
			group := ebmlElement(0xa0, block, ebmlUint(0x9b, 100003))
			cluster := ebmlElement(mkvCluster, ebmlUint(0xe7, 0), group)
			e, err := Extract(t.Context(), bytes.NewReader(mkvTestFile(config, 0, cluster, nil, false, false)), Options{KeepRaw: true})
			if err != nil {
				t.Fatalf("length %d lace %d: %v", length, lace, err)
			}
			if len(e.Pictures) != count || len(e.Records) != count {
				t.Fatal(e)
			}
			for i, p := range e.Pictures {
				want := Timestamp{Value: int64(i * 100003), Timescale: uint64(count) * 1000000000, Valid: true}
				if compareTime(p.PTS, want) != 0 {
					t.Fatal(p.PTS, want)
				}
			}
		}
	}
	config, sample := containerSample(t, 4)
	var blocks []byte
	for i := range 3 {
		blocks = append(blocks, mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Timecode: int16(i), Invisible: i == 1, Data: [][]byte{sample}})...)
	}
	data := mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), blocks), nil, true, true)
	e, err := Extract(t.Context(), bytes.NewReader(data), Options{KeepRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Pictures) != 2 || len(e.Records) != 2 || e.ObservedRPUs != 3 || e.SuppressedRPUs != 1 {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if err := WriteRPU(&out, e); err != nil {
		t.Fatal(err)
	}
	for cut := range len(data) {
		if e, err := Extract(t.Context(), bytes.NewReader(data[:cut]), Options{}); err == nil && e != nil {
			t.Fatalf("truncated MKV accepted at %d", cut)
		}
	}
}
func TestMatroskaSelection(t *testing.T) {
	config, sample := containerSample(t, 4)
	track := ebmlElement(0xae, ebmlUint(0xd7, 7), ebmlUint(0x83, 1), ebmlElement(0x86, []byte("V_MPEGH/ISO/HEVC")), ebmlElement(0x63a2, config))
	cluster := ebmlElement(mkvCluster, ebmlUint(0xe7, 0), mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}}))
	data := mkvTestFile(config, 1, cluster, track, false, false)
	if _, err := Extract(t.Context(), bytes.NewReader(data), Options{}); !errors.Is(err, ErrAmbiguousTrack) {
		t.Fatal(err)
	}
	if _, err := Extract(t.Context(), bytes.NewReader(data), Options{TrackID: 1}); err != nil {
		t.Fatal(err)
	}
}
func FuzzMatroska(f *testing.F) {
	f.Add([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x80})
	config, sample := containerSample(f, 4)
	f.Add(mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), mkvTestBlock(f, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}})), nil, false, false))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			t.Skip()
		}
		_, _ = Extract(t.Context(), bytes.NewReader(data), Options{Format: Matroska, Limits: Limits{MaxFrames: 100}})
	})
}
func BenchmarkMatroska(b *testing.B) {
	config, sample := containerSample(b, 4)
	data := mkvTestFile(config, 1, ebmlElement(mkvCluster, ebmlUint(0xe7, 0), mkvTestBlock(b, 0xa3, ebml.Block{TrackNumber: 1, Data: [][]byte{sample}})), nil, false, false)
	for b.Loop() {
		if _, err := Extract(b.Context(), bytes.NewReader(data), Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
