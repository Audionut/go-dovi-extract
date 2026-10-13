# go-dovi-extract

A pure-Go, library-first Dolby Vision RPU reader, HEVC/Matroska extractor,
structure reporter and L1 metadata plotter. Requires Go 1.27.0. Runtime use needs
no Rust, libdovi, cgo, FFmpeg, dovi_tool, installed fonts or network access.

```go
import (
    dovi "github.com/Audionut/go-dovi-extract"
    "github.com/Audionut/go-dovi-extract/extract"
)

// input is a caller-owned io.ReadSeeker. TrackID is an actual MKV TrackNumber;
// zero requires exactly one eligible HEVC track.
extraction, err := extract.Extract(ctx, input, extract.Options{KeepRaw: true})
if err != nil { return err }
analysis, err := extraction.Analysis(dovi.AnalysisOptions{})
if err != nil { return err }
report, err := dovi.Summarize(analysis, dovi.SummaryOptions{})
if err != nil { return err }
_ = report // JSON-compatible, schema_version: 1; inventories are sorted.
picture, err := dovi.Render(ctx, analysis, dovi.PlotOptions{Title: "Dolby Vision"})
// ErrNoL1 means the structural analysis still succeeded without a plot.
```

Call `png.Encode` on the returned image. Create a new temporary destination,
encode, close successfully, then publish it; callers own destination selection,
replacement policy and partial-output cleanup. `extract.WriteRPU` likewise writes
to a caller-owned writer and may leave partial output on IO failure. See the
executed examples in `example_test.go` and `extract/example_test.go`.

## Inputs and ownership

| Entry point | Input and boundary |
| --- | --- |
| `dovi.ParseRPU` | One unescaped payload, beginning `19`, including CRC and `80` terminator |
| `dovi.ParseNAL` | One escaped type-62 HEVC NAL including `7c 01`; no start code |
| `extract.ReadRPU` | Standalone escaped RPU records, delimited by three/four-byte start codes |
| `extract.Extract` | Standalone RPU, Annex B HEVC, or one selected native MKV HEVC track; signatures or explicit format |
| `extract.NewStream`, `Push`, `Finish` | Incremental Annex B or length-prefixed HEVC; the latter requires matching hvcC parameter sets and length size |

Readers stay caller-owned. A `Chunk` and its markers are borrowed only until
`Push(ctx, chunk)` returns. Offsets must be contiguous. Sample markers carry exact
rational PTS/DTS and optional invisible-output suppression. Discontinuities fail.
Call `Abort(cause)` after a producer error; only normal EOF calls `Finish(ctx)`.
Collectors are synchronous, single-owner and single-use. Cancellation is checked
between bounded work units, but cannot interrupt arbitrary blocking caller IO.
Use deadline-aware IO or close your own resource when that is required.

Results own their slices and are caller-mutable, without concurrent mutation.
`Analysis` deep-copies metadata under its own finite budget. Summary and render
validate caller-visible data; they do not trust counts from previously saved reports.

Supported metadata includes coarse profiles 5, 7 (MEL/FEL evidence), and 8;
integer/float polynomial and MMR traversal; CM2.9 levels 1/2/4/5/6/255 and CM4
levels 3/8/9/10/11/253/254, including supported variable lengths. Unknown levels,
profile 4, polynomial interpolation and unsupported grammar return explicit errors.
Mapping reuse does not inherit DM. Compressed DM retains present extensions but
its absent base/mastering fields stay unknown. Profile 8 is never labelled 8.1/8.4
from RPU evidence alone. L1 values are preserved independently in 0–4095.

Video collection validates base-layer Main/Main10 HEVC header syntax and orders
pictures through POC/reference/IRAP rules. It accepts type-62 RPUs in an explicitly
delimited prefix or picture suffix, including a suffix after EOS. It retains gaps
and rejects duplicate/orphan RPUs. Dolby Vision type-63 enhancement envelopes are
validated as transport and skipped; they are not decoded or counted as extra
pictures. Nonzero HEVC layers and separate BL/EL tracks are unsupported.
This does not validate entropy-coded image bodies or reconstruct profile-7 video.

MP4, TS/M2TS, Blu-ray playlists, AV1, encrypted/compressed tracks, video decoding,
RPU editing/conversion/injection, a production CLI, and upbrr integration are outside
this release. The library does not validate artistic grading or synchronization.

## Timeline, reports and raw output

Standalone input uses `rpu_sequence`: records remain in stored order, the x axis
is RPU index, picture count is zero, and video coverage is `unknown`.
Video uses `presentation_frames`, including pictures with no metadata. Records
are stored in presentation order and referenced once by containing pictures;
`HasRPU=false` represents a gap. Suppressed-picture RPUs are parsed and counted
as observed/suppressed, then excluded from retained records and inventories.

Coverage is `complete`, `partial`, `absent`, or `unknown`. A completed sparse or
zero-RPU video is a successful structural analysis. Interrupted scans return no
completed result. No L1 yields `ErrNoL1` from rendering; zero refresh flags are
valid. Refresh flags are metadata markers, not independently detected scene cuts.

`KeepRaw` retains escaped payload bytes without the HEVC header. `WriteRPU`
validates retained metadata/raw consistency and writes four-byte delimiters.
It refuses sparse video instead of hiding its gaps. Delimiter padding and trailing
framing zeros are discarded; the escaped payload through its terminator is unchanged.
Original raw bytes are optional for all analysis/plot operations.

Reports distinguish source PQ mastering values, L6 mastering/CLL/FALL, L1-derived
statistics, display/content blocks, trims and profile evidence. The L1 mean is
computed in PQ space before conversion to nits. Missing samples are excluded.
L5 ranges preserve missing intervals. Geometry is `unknown` unless the caller
supplies established applicable raster coordinates; display aspect ratio is not
sufficient evidence. This library makes no decoded black-bar comparison.

`Block.Values` follows the pinned wire field order, with `Length` preserving
optional-field presence: L2 target then slope/offset/power/chroma/saturation/signed
MS weight; L8 target index, six trim fields, optional mid contrast/clip trim, six
saturation and six hue bytes; L9 source primary index then optional RGB/white xy;
L10 target index/max PQ/min PQ/primary index then optional RGB/white xy; L11 four
encoded bytes; L254 mode/version. L253 validates filler bytes and records length.
L1/L5/L6 convenience fields must agree with their blocks.

Plots follow `dovi_tool`'s L1 graph: opaque 3000×1200 images, maximum/average/minimum
filled areas, normalized-PQ coordinates with nit-labelled ticks, observed-peak
scaling, and upstream statistics and metadata captions. Samples are drawn directly
without downsampling. Crops select inclusive original indices (nil end differs
from zero), then display frame numbers from zero as the original tool does.
Statistics describe the selected range. These are metadata visualizations, not
measured pixel luminance.

The embedded Go font, text spacing, antialiasing and integer alpha blending can
differ from Rust; PNG bytes are not identical. Long captions are fitted to avoid
overlap. Missing metadata remains a gap and is disclosed, rather than replaced by
synthetic L1 values. Unknown compressed mastering fields remain unknown. Unlike
the original Plotters tick generator, peaks below 100 nits do not hang; all-zero
metadata uses a finite one-code PQ range. See [plot provenance](testdata/PLOT_PROVENANCE.md)
for original-source numerical and image comparisons.

## Full-analysis persistence contract (version 1)

Persist the entire `dovi.Analysis`, not just `Summary`: schema version, timeline,
completion, TrackNumber, every picture's AU/decode ordinal, POC and known flag,
exact PTS value/timescale/valid flag, RPU presence/index, all retained `RPU` header,
DM/extension fields and presence pointers, and observed/suppressed counts. Optional
base fields must remain absent, not become zero-valued facts. Do not infer an
initial scene marker or carry L1/L5 across gaps. A separate raw sidecar is required
only if raw export must be restored. No private source paths belong in this schema.

Consumers own serialization and bounded decoding. Bound the encoded bytes before
decoding, bound nested collections during decoding for untrusted large input, reject
unknown versions, then call `Analysis.Validate` before reporting or rendering.
Recompute summary counts, inventories, scene/L5 ranges and statistics with
`Summarize`; never accept serialized derived counts as authority. Preserve completed
partial coverage separately from interrupted work. Integration must test a full
write/read round trip for exact rational timestamps (including valid zero), gaps,
absent L1/base DM, zero refresh flags, reports and rendered pixels. The tests here
exercise that contract with Go JSON; no application cache or serializer is included.

## Limits and validation

Zero options choose finite defaults: 2,000,000 records/pictures, 512 MiB extractor
accounting, 128 MiB container index, 64 MiB blocks, 64 KiB parameter/slice prefixes,
1 MiB RPUs, 64 reordered pictures (also constrained by syntax), depth 32 and 256
extension blocks per RPU. Storage sublimits cannot exceed the operation limit.
These are allocation-accounting limits, not process RSS. `MemoryBudget` can measure
or constrain active extraction work; returned results become caller-owned.
Analysis/summary each have separate 512 MiB default budgets. Rendering has its
own 48 MiB budget. Retaining multiple results/images and caller PNG buffers can
exceed any one operation's budget.

```powershell
go mod verify
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
$env:CGO_ENABLED='0'
go test ./...
go test -run '^$' -bench . -benchmem ./...
```

Normal tests are offline and use authored syntax fixtures, not private media.
Optional differential tests use the pinned development oracle documented in
`extract/testdata/PROVENANCE.md`. See `THIRD_PARTY_NOTICES.md` for source mappings.
