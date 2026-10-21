# Pi integration verification

Recorded 2026-10-20 against the rebuilt candidate after the hook quoting/parser follow-up. This covers tasks 3.1–3.3; it does not claim overall change acceptance.

- Candidate: `bin/mnemos-file-config-candidate.exe`, `mnemos 0.16.0-dev`, SHA-256 `f33cdd7da384daeb875010b85e3761861da699415aa7530f97b724b1768c7d04`.
- `MNEMOS_TEST_BIN=bin/mnemos-file-config-candidate.exe node --test pi/extensions`: 62 passed, 0 failed, 0 skipped.
- `node verify/pi/checkpoint-schema.mjs --sdk "D:\Program Files\nodejs\node_modules\@earendil-works\pi-coding-agent" --package "D:\Study\mnemos\pi" --adapter "C:\Users\chinasoft.h.qin\.pi\agent\npm\node_modules\pi-mcp-adapter" --binary "D:\Study\mnemos\bin\mnemos-file-config-candidate.exe"`: passed against pi SDK 1.3.0 and adapter 5.3.2. The emitted report records 12 adapter direct executions, 0 model requests, `usedAuth: false`, schema discovery without a workspace, forged metadata rejection without mutation, and save/replay/clear success.
- The verifier copied the extension TypeScript into a temporary isolated package and reported matching source/copy hashes. No real user installation was modified.

## 0.16.0 source-snapshot install and real-host gates

Recorded 2026-10-20 after the approved source commit and clean archive build. This records local installation and runtime gates; it does not complete/archive the OpenSpec change.

- Source commit: `79e68e8308c5a1887c04092292d8b62e7507c408` (`main`). Installed version: `mnemos 0.16.0+79e68e8`; SHA-256 `c43ca3982d8e3e96e67145a4e36c702adc2be9f6488e1b6eab01df83498ebf89`. Build metadata reports Go 1.27.2, Windows amd64, `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`.
- `node --test pi/extensions/*.test.ts` against the installed binary selected with the test-only `MNEMOS_TEST_BIN`: 62 passed, 0 failed, 0 skipped.
- `node verify/pi/checkpoint-schema.mjs --binary "D:\\software\\mnemos\\mnemos.exe" --package "C:\\Users\\chinasoft.h.qin\\.pi\\agent\\local-packages\\mnemos" --sdk "D:\\Program Files\\nodejs\\node_modules\\@earendil-works\\pi-coding-agent" --adapter "C:\\Users\\chinasoft.h.qin\\.pi\\agent\\npm\\node_modules\\pi-mcp-adapter"`: passed with 12 actual adapter executions, 0 model requests, `usedAuth: false`, schema discovery without workspace, forged metadata rejection without mutation, and save/replay/clear success. All five copied runtime TypeScript hashes matched the installed package source.
- Real Codex app-server gate against the installed binary: passed with 19 direct MCP calls, 27 tools, 4 resources, 0 turn-start requests, no auth use, and unchanged user Codex config hash. The run used an ephemeral Codex home, isolated database, explicit scratch TOML route, and conflicting legacy `MNEMOS_*` variables to verify they were ignored.
- `pi update --extensions --no-approve` passed in a separate `PI_CODING_AGENT_DIR` containing a copy of the 0.16 package; `PI_OFFLINE` was absent. User-global Pi package settings were not used by that run.
- The selected user TOML previously had no `[continuity].enabled`; it was set to `true` as authorized. Pi and Codex now both select that same TOML explicitly. Existing Pi adapter fields `directTools` and `toolPrefix` were preserved. Backups of the previous executable, TOML, Codex config, and local Pi package were retained under ignored `bin/`.
- Claude Code was not installed and no real Claude gate was run. No model requests were made; these protocol gates do not establish model behavior.
