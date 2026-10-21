# Pi and runner verification evidence

Recorded 2026-10-20 for tasks 3.1, 3.2, 3.3 and 5.1 only. This is iteration evidence, not overall change acceptance.

## pi package

- `MNEMOS_TEST_BIN=bin/mnemos-file-config-candidate.exe node --test pi/extensions`: 62 tests passed, 0 failed, 0 skipped. The candidate is `mnemos 0.16.0-dev`, SHA-256 `c8e06f6ca96263422cae516e4fff026985a5fd390eb9c306e5ead548cdedc2c5`, built at 2026-10-20 17:02 before a follow-up hook quoting fix. Rebuild and rerun against the final candidate during task 6.x acceptance.
- `node verify/pi/checkpoint-schema.mjs --sdk "D:\Program Files\nodejs\node_modules\@earendil-works\pi-coding-agent" --package "D:\Study\mnemos\pi" --adapter "C:\Users\chinasoft.h.qin\.pi\agent\npm\node_modules\pi-mcp-adapter" --binary "D:\Study\mnemos\bin\mnemos-file-config-candidate.exe"`: passed against the installed pi SDK 1.3.0 and adapter 5.3.2. The report recorded 12 direct adapter executions, 0 model requests, no auth use, schema discovery without a workspace, forged metadata rejection without mutation, and save/replay/clear success. The same iteration candidate caveat applies.
- `node --check verify/pi/checkpoint-schema.mjs` and scoped `git diff --check` passed.

## behavior/capture runner

- `python -B verify/runners/test_snapshot.py`: 9 tests run, 8 passed, 1 skipped because Unix executable permission bits are not applicable on Windows. Coverage includes source-home resolution, explicit candidate/config routes, host gates, isolated on/off setup, consistent SQLite WAL backup and fixture-write isolation, and failure before model launch.
- A real candidate `mnemos --config <fixture>/source.toml config` check with `storage.path = "~/.mnemos/fixture.db"` and HOME/USERPROFILE set to a temporary fixture resolved the path under that fixture home; it created no database.
- Runner `--help` and scoped `git diff --check` passed. No Claude Code executable is installed locally, so no real-host behavior gate or model request was run.
