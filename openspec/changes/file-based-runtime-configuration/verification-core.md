# Core verification — file-based runtime configuration

Implementation lane: core config, CLI/service consumers, installer route consistency, and prewarm budget propagation. OpenSpec tasks 1.1, 1.2, 1.3, 2.1, and 2.2 were marked complete after reviewing their tests and the checks below. Pi and workspace-diagnostics task checkboxes are preserved from their owners.

## Go checks

- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/config ./internal/installer ./internal/memory ./internal/continuityflow ./cmd/mnemos -count=1` — passed all five packages. This covered missing/explicit config behavior, ignored legacy environment routing, projection non-disclosure/no side effects, disabled hooks and vault behavior, configured search/context/prewarm budgets, and installer route preservation/quoting.
- After the POSIX apostrophe parser/command quoting follow-up, `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/installer -count=1` — passed.
- `CGO_ENABLED=0 GOMAXPROCS=2 go vet -p 1 ./internal/config ./internal/installer ./internal/memory ./internal/continuityflow ./cmd/mnemos` — passed with no diagnostics.
- `gofmt -d` over changed Go files found one formatting delta in the modified `internal/installer/plan.go`; formatted it and reran the installer package tests successfully.
- Built with `CGO_ENABLED=0 GOMAXPROCS=2 go build -p 1 -ldflags "-X github.com/polyxmedia/mnemos/internal/version.Version=0.16.0-dev" -o bin/mnemos-file-config-candidate.exe ./cmd/mnemos`. Candidate version: `0.16.0-dev`; SHA-256: `f33cdd7da384daeb875010b85e3761861da699415aa7530f97b724b1768c7d04`.

## Runtime gates

- `python bin/verify-codex-file-config-candidate.py` — PASS against the real Codex app-server MCP client, with temporary `CODEX_HOME`, explicit selector to a config path containing spaces, isolated SQLite store, and conflicting legacy `MNEMOS_*` environment values. Runtime connected with 27 tools and 4 resources; 19 direct MCP calls passed, covering save/read/search/delete and session/checkpoint/retry/update/clear. No `turn/start` request or auth file; the real user Codex config hash was unchanged. The app-server log had unauthenticated 401 warnings for optional featured-plugin/websocket activity, while the Mnemos MCP route and all assertions passed.
- `python bin/verify-nongit-continuity-candidate.py` — PASS with an explicit root outside all Git worktrees, path containing spaces, isolated config/store/`CODEX_HOME`, and conflicting legacy route variables. Nine direct MCP calls verified distinct sessions, a shared workspace checkpoint visible to the second session, idempotent save and clear retries, and cleared readback. While the MCP route remained connected and idle, the same gate ran candidate `doctor --workspaces --json` against a nested non-Git root fixture. The report showed the expected two roots and grouped session/checkpoint/clear counts, emitted an advisory `possible_boundary_split` with `merge_safety=not_established`, omitted private goals/checkpoint/retry text, and left the database and WAL bytes plus directory/sidecar presence and sizes unchanged (the existing SHM reader marks may vary). No `turn/start` request or auth file.

Temporary gate scripts and fixtures are under ignored `bin/` or the system temporary directory; no installation or real configuration write was performed. Windows `make`/`golangci-lint` are unavailable on PATH; the checks above used the existing warm Go cache, CGO disabled, and serialized package execution.


## Full-repository acceptance checks (2026-10-20)

- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./... -count=1` — passed every package, including `cmd/mnemos`, `internal/storage`, and `internal/workspacediagnostics`.
- `CGO_ENABLED=0 GOMAXPROCS=2 go vet -p 1 ./...` — passed with no diagnostics.
- `gofmt -d` over 38 changed/new Go files — clean. Git emitted only its expected LF-to-CRLF working-copy notices.
- Independent read-only review of Pi bootstrap/runtime/generation/config routing and `verify/runners/snapshot.py` / `verify/pi/checkpoint-schema.mjs` — no actionable correctness, isolation, or compatibility findings. Review covered the shared package MCP/hook route, strict bounded config projection, stale-generation invalidation, continuity unknown-outcome retry behavior, and explicit isolated verify routes.



- After formatting the final CLI help test, `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./cmd/mnemos -count=1` — passed; `gofmt -d` over all 38 changed/new Go files — clean.
