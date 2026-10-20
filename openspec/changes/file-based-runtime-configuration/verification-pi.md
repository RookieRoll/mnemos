# Pi integration verification

Recorded 2026-10-20 against the rebuilt candidate after the hook quoting/parser follow-up. This covers tasks 3.1–3.3; it does not claim overall change acceptance.

- Candidate: `bin/mnemos-file-config-candidate.exe`, `mnemos 0.16.0-dev`, SHA-256 `f33cdd7da384daeb875010b85e3761861da699415aa7530f97b724b1768c7d04`.
- `MNEMOS_TEST_BIN=bin/mnemos-file-config-candidate.exe node --test pi/extensions`: 62 passed, 0 failed, 0 skipped.
- `node verify/pi/checkpoint-schema.mjs --sdk "D:\Program Files\nodejs\node_modules\@earendil-works\pi-coding-agent" --package "D:\Study\mnemos\pi" --adapter "C:\Users\chinasoft.h.qin\.pi\agent\npm\node_modules\pi-mcp-adapter" --binary "D:\Study\mnemos\bin\mnemos-file-config-candidate.exe"`: passed against pi SDK 1.3.0 and adapter 5.3.2. The emitted report records 12 adapter direct executions, 0 model requests, `usedAuth: false`, schema discovery without a workspace, forged metadata rejection without mutation, and save/replay/clear success.
- The verifier copied the extension TypeScript into a temporary isolated package and reported matching source/copy hashes. No real user installation was modified.
