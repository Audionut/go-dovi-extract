# Fixture and oracle provenance

`pictures.hevc` is an MIT-licensed authored syntax fixture copied unchanged from
`Audionut/go-hdr10-plus/extract/testdata/no-metadata.hevc`, revision
`656d2051b011b88f2d0b8db9d304e779bdb4f609`. It contains arbitrary skipped VCL bytes,
not decodable picture content. Its decoded AU/POC order is 0/0, 1/2, 2/1;
expected presentation AU order is [0,2,1]. Parameter IDs are 2/3/5.

`internal/testfixture/rpu.go` authors RPU syntax independently with known raw L1
values, profiles, reuse/compression flags, polynomial/MMR integer/float forms,
NLQ MEL/FEL values and selectable extension blocks. This is test-only generation;
the library exposes no RPU editing/writing API. HEVC RPU association expectations
are authored in Go tests and do not rely on upstream missing/duplicate behavior.

The optional Rust oracle is quietvoid/dovi_tool revision
`614c816b6446dcd1dbaf433403d499a6026fbb5a` (dovi_tool 2.3.4,
dolby_vision 3.4.0, Rust 1.95.0). Build outside this repository:

```powershell
cargo build --locked --release --features internal-font
$env:DOVI_REFERENCE_DIR='C:\path\to\pinned\dovi_tool'
$env:DOVI_ORACLE="$env:DOVI_REFERENCE_DIR\target\release\dovi_tool.exe"
go test ./extract -run 'TestOptional' -v
```

The harness invokes these exact argument forms, with test-specific temporary outputs:

```text
dovi_tool.exe export -i INPUT -d all=OUTPUT.json
dovi_tool.exe extract-rpu -i INPUT -o OUTPUT.bin
```

It compares every retained base-DM field, header evidence, L1/L5/L6, extension
field order/values, profile and MEL/FEL evidence, CRC, record count and raw
presentation-ordered export bytes. Original picture identities/gaps/suppression
are independently asserted in offline synthetic tests. CRC-corrupt inputs are
rejected even though upstream no-mode extraction may copy them.

External RPU cases: assets/tests/{profile5,profile8,profile84,mel_orig,fel_orig,
cmv40_full_rpu,empty_dmv1_blocks,data_before_crc32,level253-ext-metadata,
mel_variable_l8_length13}.bin. External video cases: assets/hevc_tests/{regular.hevc,
regular.mkv,no_aud_injected.hevc,regular_start_code_4_muxed_el.hevc}.
They stay outside the repository; redistribution provenance is unresolved.
No private media is included. Normal tests invoke no external executable/network.

`SHA256.json` records exact source and external fixture hashes used during initial
implementation. The external oracle executable and Cargo.lock hashes, build/check
logs and platform limitations are recorded in the task-local check receipt.
