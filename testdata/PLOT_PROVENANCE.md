# L1 plot parity

Reference: quietvoid/dovi_tool `614c816b6446dcd1dbaf433403d499a6026fbb5a`
(dovi_tool 2.3.4, Plotters 0.3.7). Test inputs are newly authored by
`parityAnalysis` in `render_parity_test.go`; no private media is redistributed.

`plot-*.json` comes from `plot_oracle.rs`, which includes the pinned upstream
`rpu_info.rs` and `plotter.rs` directly. It calls `RpusListSummary::new` and
`PlotCoord::new`, not a Go calculation. Comparisons cover maximum/mean maximum,
maximum/mean average, **maximum** minimum, selected-range statistics, upper PQ
range, tick points, profile/EL/CM counts, source mastering, L5 ranges, L6 inventory,
L2 and L8 targets and L9 primaries. Nit tolerance is max(1e-6, abs(reference)*1e-9);
PQ tolerance is 1e-12. The public report's minimum statistic remains the minimum;
the plot legend intentionally follows upstream's maximum of the minimum series.

The seven cases are varying, dark, black, spike, single, metadata and crop.
The crop selects inclusive indices 20 through 52. The metadata case includes
profile 7 MEL/FEL, mixed CM2.9/CM4.0, varying active areas, three L6 sets, two L2
targets, duplicate L8 targets resolving to the same nit value, and two primaries.

For peaks <=100 nits, the upstream positive-step descending linspace does not
terminate (observed with the dark fixture). The oracle skips only that coordinate
call and records null ticks. Its upstream numeric statistics and captions remain
checked. Go handles dark/zero inputs without a 100-nit floor. Missing samples,
unknown compressed source mastering and isolated-sample visibility preserve the
Go library's explicit-evidence contract rather than upstream default synthesis.

`plot-fill-reference.json` samples the original `dovi_tool plot` PNG for the varying
fixture at normalized chart coordinates. Its plot rectangle is (120,162)-(2940,1110).
The Go chart uses 20 extra left pixels for font spacing. RGB tolerance is two units
per channel for integer alpha blending; expected colors are from the Rust PNG.
The pre-change Go line renderer fails this test. The gap-image SHA256 in
`summary_test.go` is a separately inspected Go-only visual regression baseline.
It is not original-tool equality evidence.

Reproduction (PowerShell, outputs outside the repository):

```powershell
$env:DOVI_PLOT_REFERENCE_OUTPUT = 'E:\plot-reference'
go test -run '^TestPlotReference$' -v .
# Build the development oracle with DOVI_REFERENCE set to the pinned source.
# Dependencies: dolby_vision (path, serde), anyhow 1.0.103, clap 4.6.1 (derive),
# itertools 0.15.0, serde_json 1.0.150, plotters 0.3.7
# (bitmap_backend, bitmap_encoder, all_series, ab_glyph; no default features).
# Define the oracle crate feature system-font to omit the upstream embedded-font
# registration: this oracle computes numbers only and does not render images.
plot_oracle.exe E:\plot-reference\varying.bin > testdata\plot-varying.json
plot_oracle.exe E:\plot-reference\crop.bin 20 52 > testdata\plot-crop.json
dovi_tool.exe plot E:\plot-reference\varying.bin -o E:\plot-reference\rust-varying.png -t 'Dolby Vision synthetic varying'
```

Actual original-tool PNGs for varying, spike, metadata and crop were inspected
beside the Go images. Fonts, text metrics and antialiasing are intentionally not
pixel-identity guarantees. Runtime requires no Rust, external executable or font.

## Full-length output validation

Four additional full-length Rust/Go PNG pairs and anonymized comparison results
are retained in [real-media-l1](real-media-l1/README.md). They complement the
reproducible synthetic tests with output checks from complete media inputs.
