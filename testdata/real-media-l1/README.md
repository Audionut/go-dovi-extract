# Full-length L1 plot output validation

These four pairs preserve outputs from independent full-length media checks on
13 October 2026. Case identifiers are neutral; source names, paths, media and raw
RPU streams are not included. All images use the same generic title and contain
no PNG text or EXIF metadata.

| Case | Frames | Rust reference | Go output |
| --- | ---: | --- | --- |
| 1 | 74,537 | [PNG](case-1-rust.png) | [PNG](case-1-go.png) |
| 2 | 81,773 | [PNG](case-2-rust.png) | [PNG](case-2-go.png) |
| 3 | 157,432 | [PNG](case-3-rust.png) | [PNG](case-3-go.png) |
| 4 | 243,974 | [PNG](case-4-rust.png) | [PNG](case-4-go.png) |

## Method and results

The reference is quietvoid/dovi_tool revision
`614c816b6446dcd1dbaf433403d499a6026fbb5a` (2.3.4, Plotters 0.3.7).
Its executable independently extracted RPUs from each complete Matroska input and
rendered the default L1 plot without a frame limit or crop.

The Go outputs use this library through the consuming application's native
extraction, evidence publication, restoration and rendering path. The complete
Dolby Vision evidence remained identical across the save/restore step.

A diagnostic wrapper around the pinned Rust parser and plotting summary exported
all L1 values, statistics, captions and axis ticks. These were compared with the
Go renderer's data in presentation order: **557,716 frames and 1,673,148 minimum,
maximum and average PQ values matched exactly**, with zero mismatched frames.
Captions matched exactly; axis ticks and the PQ ceiling matched within `1e-12`.
Nit statistics passed `max(1e-6, abs(reference) * 1e-9)` tolerance; the largest
absolute difference was approximately `5.32e-9` nits.

All eight PNGs are 3000 × 1200. Visual inspection confirmed corresponding curves,
spikes, fills and labels. Font metrics, the Go plot's additional 20-pixel left
margin, legend geometry, antialiasing and alpha blending differ. These are output
validation artifacts, not pixel-identical golden images or an automated test
that can be regenerated without the private source media. The reproducible
synthetic checks remain documented in [PLOT_PROVENANCE.md](../PLOT_PROVENANCE.md).

[validation.json](validation.json) records per-case results and image SHA-256
hashes. These checks cover Dolby Vision L1 metadata plotting; they do not establish
decoded-picture accuracy, grade equivalence, HDR10+ parity or performance.
