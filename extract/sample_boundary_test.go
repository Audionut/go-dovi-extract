package extract

import (
	"bytes"
	"encoding/binary"
	"testing"

	f "github.com/Audionut/go-dovi-extract/internal/testfixture"
	ebml "github.com/at-wat/ebml-go"
)

func TestSampleRPUAssociation(t *testing.T) {
	config, _ := containerSample(t, 4)
	var vcl, parameters [][]byte
	for _, n := range syntaxNALs(t) {
		kind := n[0] >> 1 & 63
		if kind <= 31 {
			vcl = append(vcl, n)
		}
		if kind >= 32 && kind <= 34 {
			parameters = append(parameters, n)
		}
	}
	for _, tc := range []struct {
		name                    string
		prefix, first, suppress bool
	}{
		{"prefix-after-gap", true, false, false},
		{"prefix-after-RPU", true, true, false},
		{"suppressed-prefix", true, false, true},
		{"suffix", false, true, false},
	} {
		for _, mode := range []string{"length", "annex", "mkv"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				pack := func(nals ...[]byte) []byte {
					var out []byte
					for _, n := range nals {
						if mode == "annex" {
							out = append(out, 0, 0, 1)
						} else {
							out = binary.BigEndian.AppendUint32(out, uint32(len(n)))
						}
						out = append(out, n...)
					}
					return out
				}
				frames := make([][]byte, 3)
				for i := range frames {
					nals := [][]byte{vcl[i]}
					if i == 1 || i == 0 && tc.first {
						rpu := f.NAL(f.Options{Max: uint16(1000 + i)})
						if tc.prefix {
							nals = append([][]byte{rpu}, nals...)
						} else {
							nals = append(nals, rpu)
						}
					}
					if i == 0 && mode == "annex" {
						nals = append(append([][]byte{}, parameters...), nals...)
					}
					frames[i] = pack(nals...)
				}
				var got *Extraction
				var err error
				if mode == "mkv" {
					var blocks []byte
					for i, frame := range frames {
						blocks = append(blocks, mkvTestBlock(t, 0xa3, ebml.Block{TrackNumber: 1, Timecode: []int16{0, 2, 1}[i], Invisible: i == 1 && tc.suppress, Data: [][]byte{frame}})...)
					}
					cluster := ebmlElement(mkvCluster, ebmlUint(0xe7, 0), blocks)
					got, err = Extract(t.Context(), bytes.NewReader(mkvTestFile(config, 1, cluster, nil, false, false)), Options{})
				} else {
					opts := StreamOptions{}
					if mode == "length" {
						opts = StreamOptions{Framing: LengthPrefixed, LengthSize: 4, CodecConfig: config}
					}
					s, e := NewStream(opts)
					if e != nil {
						t.Fatal(e)
					}
					var offset uint64
					for i, frame := range frames {
						// Split every byte, including prefixes and metadata escapes.
						for j := range frame {
							chunk := Chunk{Data: frame[j : j+1], ESOffset: offset + uint64(j)}
							if j == 0 {
								chunk.Markers = []Marker{{Kind: SampleStart, ESOffset: offset, SuppressOutput: i == 1 && tc.suppress}}
							}
							if err = s.Push(t.Context(), chunk); err != nil {
								t.Fatal(err)
							}
						}
						offset += uint64(len(frame))
					}
					got, err = s.Finish(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range got.Pictures {
					want := p.AUOrdinal == 1 || p.AUOrdinal == 0 && tc.first
					if p.HasRPU != want {
						t.Fatalf("shifted metadata: %+v", got.Pictures)
					}
					if p.HasRPU && got.Records[p.RPUIndex].RPU.DM.L1.MaxPQ != 1000+uint16(p.AUOrdinal) {
						t.Fatal("wrong RPU identity")
					}
				}
				if tc.suppress && (got.SuppressedRPUs != 1 || len(got.Pictures) != 2 || len(got.Records) != 0) {
					t.Fatalf("suppression: %+v", got)
				}
			})
		}
	}
}

func TestInvalidSampleBoundaries(t *testing.T) {
	data := videoFixture(t, -1)
	// A marker inside an RPU must not silently become timing for the next VCL.
	rpu := bytes.Index(data, []byte{0x7c, 1})
	for _, markers := range [][]Marker{
		{{Kind: SampleStart, ESOffset: uint64(rpu + 5)}},
		{{Kind: SampleStart}, {Kind: SampleStart}},
	} {
		s, err := NewStream(StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Push(t.Context(), Chunk{Data: data, Markers: markers}); err == nil {
			_, err = s.Finish(t.Context())
		}
		if err == nil {
			t.Fatal("invalid sample boundary accepted")
		}
	}
}
