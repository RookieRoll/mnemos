# 0.16.0 offline package verification

Date: 2026-10-21. This records the local Windows x64 handoff package and the checks performed against its extracted contents. It is delivery evidence only; the ZIP and this record are not release artifacts.

## Provenance and contents

- Binary: `mnemos 0.16.0+79e68e8`, built from source commit `79e68e8308c5a1887c04092292d8b62e7507c408`, Windows amd64, CGO disabled and stripped. SHA-256: `C43CA3982D8E3E96E67145A4E36C702ADC2BE9F6488E1B6EAB01DF83498EBF89`.
- The Pi manifest, MCP template, skill and five runtime TypeScript files were byte-compared (normalizing checkout line endings) with the same pinned source commit. The documentation is a post-verification snapshot; `checksums.json` identifies every included payload file by SHA-256 and byte count. It records the pinned binary/runtime commits separately from the repository documentation snapshot.
- The documentation link closure includes configuration, workspace diagnostics, architecture, MCP, context, vault, API, backup, embeddings, skills and quickstart references, plus linked archived designs. The current change's `verification-delivery.md` was copied into the package under its future archive path `openspec/changes/archive/2026-10-21-file-based-runtime-configuration/verification-delivery.md`; the source was not moved. The package does not include this offline verification record, avoiding a checksum self-reference.
- No database, user configuration, credential, install script, model, or third-party adapter is included. The Pi package contains exactly five runtime TypeScript files, including `bootstrap.ts`.

## Archive

- File: `C:\Users\chinasoft.h.qin\Downloads\mnemos-pi-0.16.0-windows-amd64-offline-20261021.zip`
- Size: `6,729,232` bytes.
- SHA-256: `A582CF380C17C40599DFEF0A3C02971D65AFBDE1BD5EF974A3BFA40F166AF059`.
- Sidecar: `mnemos-pi-0.16.0-windows-amd64-offline-20261021.zip.sha256`.
- ZIP CRC test: PASS. Extraction inventory, every manifest SHA-256/byte count, and extracted binary `--version`: PASS. The Downloads copy hash and size match the verified build copy.

## Extracted-package gates

- `node verify/pi/checkpoint-schema.mjs` against the extracted package and binary, using Pi SDK 1.3.0 and `pi-mcp-adapter` 5.3.2: PASS, exit 0. The gate made 12 direct adapter tool calls with zero model requests and no authentication. It verified the checkpoint input schema omits server-owned workspace metadata, forged workspace metadata is rejected without changing stored state, and save/replay/update/clear flows pass. All five runtime TypeScript source/copy hashes match. MCP was routed through the extracted executable and an explicit isolated TOML file.
- `pi install <isolated local-package path>` followed by `pi list` and `pi update --extensions --no-approve` in a fresh `PI_CODING_AGENT_DIR` containing only this package: PASS, exit 0. `PI_OFFLINE` was absent. The Pi settings SHA-256 and all 10 installed package file hashes remained unchanged across the update.
- The Pi version requirement follows the host package's engine: the tested Pi 1.3.0 requires Node.js `>=22.19.0`; adapter 5.3.2 requires `>=20`. This package does not claim Pi 1.3.0 installs on Node.js 20.

The gates use synthetic data and isolated temporary settings/databases. They do not access the user's memory database, request a model, use authentication, or prove model adoption or TUI behavior.
