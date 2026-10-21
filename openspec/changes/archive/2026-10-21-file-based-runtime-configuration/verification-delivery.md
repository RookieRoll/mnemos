# 0.16.0 local delivery verification

Recorded 2026-10-20 after installation from the approved source commit. This is delivery evidence for task 6.2; the offline handoff and extracted-package verification remain task 6.4.

## Source and installed binary

- Source: `main` at `79e68e8308c5a1887c04092292d8b62e7507c408`.
- Installed executable: `D:\software\mnemos\mnemos.exe`, version `mnemos 0.16.0+79e68e8`, SHA-256 `c43ca3982d8e3e96e67145a4e36c702adc2be9f6488e1b6eab01df83498ebf89`.
- Reproducible local build was produced from a clean `git archive` of that commit with Go 1.27.2, Windows amd64, `CGO_ENABLED=0`, `-trimpath`, and `-buildvcs=false`.

## Local configuration and Pi package

- Selected TOML: `C:\Users\chinasoft.h.qin\.mnemos\config.toml`. `[continuity].enabled` was previously unset and is now `true`; no pre-existing explicit `false` was overwritten.
- Codex's `mnemos` MCP route and Pi's `mnemos` package route both select `D:/software/mnemos/mnemos.exe --config C:/Users/chinasoft.h.qin/.mnemos/config.toml serve`.
- Pi package: `C:\Users\chinasoft.h.qin\.pi\agent\local-packages\mnemos`, package version `0.16.0`. The manifest and five runtime TypeScript files match the committed package inventory. Pi adapter fields `directTools` and `toolPrefix` were preserved.
- Backups of the previous executable, TOML, Codex config, and full Pi package: `D:\Study\mnemos\bin\local-0.16-backup-20261020-175630`. The atomic replacement API also retained its prior executable at `D:\Study\mnemos\bin\local-0.16-backup-20261020-175630\mnemos.exe.atomic-rollback`.

## Gates

- Pi extension tests against the installed executable selected with the test-only `MNEMOS_TEST_BIN`: 62 passed, 0 failed, 0 skipped.
- Real Pi SDK 1.3.0 and adapter 5.3.2 gate against the installed package/binary: passed, 12 direct adapter executions, 0 model requests, no auth, schema available without workspace, forged metadata rejected without mutation, save/replay/clear passed; all five runtime TypeScript source/copy hashes matched.
- Real Codex app-server MCP gate: passed, 19 direct calls, 27 tools and 4 resources, 0 turn-start requests, no auth, user's Codex config hash unchanged. It used a temporary Codex home/database and explicit scratch TOML route while setting conflicting legacy `MNEMOS_*` variables; the candidate ignored them.
- `pi update --extensions --no-approve` passed in an isolated `PI_CODING_AGENT_DIR` with a copied 0.16 package. `PI_OFFLINE` was absent. The user's global Pi package settings were not used.
- No real Claude Code gate was run because Claude Code is not installed. No model requests were made.
