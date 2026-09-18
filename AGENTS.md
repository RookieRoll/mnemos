# AGENTS.md — mnemos

Guidance for AI coding agents working in this repository. Read this before your
first edit. `README.md` sells the project, `docs/ARCHITECTURE.md` explains it,
`CONTRIBUTING.md` states the ground rules — this file is the working summary you
need to make a change that survives review.

## What this is

Mnemos is a **local-first persistent memory layer for AI coding agents**, shipped
as a single static, CGO-free Go binary (~15 MB). It exposes an MCP server (plus
an HTTP API) that stores observations, sessions, skills, and correction records
in one SQLite file, and injects a token-budgeted block of them at session start.

- Module: `github.com/polyxmedia/mnemos`
- Binary: `cmd/mnemos` → `mnemos`
- Language: Go (`go.mod` declares `go 1.25.0`; CI tests 1.25 and 1.26)
- Runtime state: `~/.mnemos/mnemos.db` (SQLite) + `~/.mnemos/config.toml`
- License: MIT

## Hard invariants — do not break these

1. **Zero CGO.** `CGO_ENABLED=0` for all release builds. The SQLite driver is
   `modernc.org/sqlite` (pure Go). Never introduce `mattn/go-sqlite3`, `sqlite-vec`,
   or any cgo dependency. Cross-compilation to linux/darwin/windows must keep working.
2. **No LLM calls inside the memory layer.** Mnemos stores; the agent thinks.
   Skill promotion is *deterministic pattern-mining* over structured correction
   records — no model inference, no prompts. The same input must always produce
   the same output.
3. **No new dependencies without an issue first.** Everything outside the
   approved list (`go-sdk`, `BurntSushi/toml`, `oklog/ulid/v2`, `x/sync`,
   `yaml.v3`, `modernc.org/sqlite`) is stdlib.
4. **No global state, no `init()` side effects, no reflection wiring.**
   Dependencies are constructor-injected.
5. **Transports are thin.** `internal/mcp` and `internal/api` translate protocol
   shapes into service calls. No business logic in transports.
6. **Interfaces are declared where they are consumed** (stdlib `io.Reader`
   idiom), not next to their implementations.
7. **Every exported identifier has a godoc comment.** Errors are wrapped with
   `fmt.Errorf("context: %w", err)`. Every public method takes `context.Context`
   first. Logging is `log/slog` only.
8. **Every change ships with tests.** Table-driven tests are the default pattern;
   tests live next to the code they cover.

## Layout

```
cmd/mnemos/            CLI: serve, init, doctor, prewarm, hook, dream, vault,
                       verify, skill, replay, update, digest, efficacy, ...
internal/mcp/          official Model Context Protocol Go SDK wrapper: 21 tools,
                       3 resources — thin adapter only
internal/api/          stdlib net/http REST transport + API-key middleware
internal/memory/       observation domain: types, service, hybrid ranker, decay,
                       store interfaces (Reader/Writer/Maintenance/Exportable/Vectorable)
internal/session/      session service + types
internal/skills/       procedural memory service, packs (export/import), scoring
internal/prewarm/      session_start + compaction-recovery context composers
internal/rumination/   threshold monitors, hostile-review packaging, resolution
internal/dream/        consolidation daemon: prune → decay → promote → journal
internal/safety/       prompt-injection scanner (write boundary)
internal/injection/    records which memories were surfaced (provenance signal)
internal/storage/      SQLite + FTS5, migrations (//go:embed numbered SQL), stores
internal/embedding/    Ollama / OpenAI / Noop embedders
internal/vault/        Obsidian export + watcher
internal/installer/    agent client wire-up (Claude Code/Desktop, Cursor, Windsurf, Codex, pi)
internal/verify/       efficacy harness runners (retrieval, behavior, capture)
internal/replay/       session replay as markdown
internal/config/       TOML config load/defaults/validation
internal/version/      build-time Version var, set via -ldflags
pkg/client/            typed Go client for the HTTP API (public)
verify/                fixtures + on/off arm runners for the efficacy harness
pi/                    pi package: extensions (hook mapping), skills, mcp.json
docs/                  ARCHITECTURE.md, MCP_TOOLS.md, SKILLS.md, VAULT.md, ...
```

Layering: `cmd` → transports (`mcp`, `api`) → services (`prewarm`, `safety`,
`dream`, `rumination`, `memory`, `session`, `skills`) → `storage`. Dependencies
point down only.

## Build, test, lint

```bash
make test     # go test ./... -race -count=1
make build    # bin/mnemos, CGO_ENABLED=0, -s -w, version from git describe
make lint     # golangci-lint run ./...
make fmt      # gofmt -s -w . && go mod tidy
make cover    # coverage.html
```

Run `make fmt lint test` before pushing; CI runs the same checks plus a manifests
job (below).

**On this Windows workstation** the toolchain differs from CI in ways worth knowing:

- `make`, `gcc`, and `golangci-lint` are **not on PATH**. Use `go build ./...`,
  `go vet ./...`, `go fmt ./...` directly.
- `-race` requires cgo, so `go test ./... -race` fails locally even though it
  works in CI. Use `go test ./<pkg>/ -count=1` for local iteration and let CI
  own the race detector. (CI's own comment says this explicitly.)
- The first `go build ./...` downloads the whole dependency graph and can exceed
  a 2-minute command timeout. Give it a long window or repeat the call; after the
  module cache is warm, builds are seconds.
- Prefer PowerShell over bash for compound commands on this machine.

## Code conventions

Observed in-tree style, in addition to `CONTRIBUTING.md`:

- Package doc comment starts each package's primary file (`// Package storage …`).
- Handlers/services are constructed via `New…(Config{...})` with a `Config`
  struct of required fields; optional fields are documented as such in the struct.
- Errors carry a lowercase subsystem prefix: `fmt.Errorf("storage: open sqlite: %w", err)`.
- Tests use table-driven subtests with `t.Run`, plus `_extra_test.go` /
  `coverage_test.go` files for edge-case fills. Fixtures are built with helpers
  (`newService`, `openTestDB`, `newHarness`) rather than duplicated setup.
- Keep comments explaining *why* the design is the way it is (the existing code
  is heavy on rationale comments; match that density, don't add noise).

## Architecture notes you will need

- **Bi-temporal store.** Observations carry `valid_from`/`valid_until` (fact time)
  and `created_at`/`invalidated_at` (system time). Supersession invalidates rather
  than deletes; historical queries use `SearchInput.AsOf`. Migrations must stay
  non-breaking (`schema_migrations` + embedded numbered SQL files).
- **Ranking:**
  `score = bm25 × importance_weight × recency_factor × access_factor`
  with `importance_weight = 0.5 + 0.5×(importance/10)`,
  `recency_factor = (1+age_days)^(-decay_rate)`, `access_factor = 1 + 0.1×ln(1+access_count)`.
  Search pulls `limit × 3` raw hits, re-ranks, truncates.
- **FTS5 external-content virtual table** mirrors observations; triggers keep it in
  sync. Content hash on insert powers dedup-on-save per `(agent, project)`.
- **Embeddings are BLOBs in the same SQLite file.** Cosine similarity runs in pure
  Go over top-N BM25 candidates via Reciprocal Rank Fusion. `hybrid_alpha` is
  `1.0 = pure BM25`, `0.0 = pure vector`; auto-enables when Ollama is reachable.
- **Prewarm** composes conventions → recent sessions → matching skills →
  corrections → hot files, runs each section through `safety.Scanner`, and budgets
  the block to ~500 tokens. Fires on session start only; no mid-session push.
- **Dream pass order matters:** prune → decay → promote → journal. Promotion
  clusters live corrections by `(agent_id, project, label)`, and 3+ members yield
  a templated skill. Idempotency is a 12-char sha256 of the group carried as a
  `promoted-origin:<hash>` tag — never replace that keying scheme without a migration.
- **Rumination** is the falsifiability guard: resolving a candidate requires a
  `why_better` that names a *new prediction the revision makes*; cosmetic rewording
  is rejected. Dismissals require a real reason and are preserved.

## Surfaces

CLI subcommands are registered in the `commands` map in `cmd/mnemos/main.go`; the
usage text lives in `printUsage` in the same file. Add a subcommand by writing a
`run<Name>(ctx, args)` handler (usually in `subcommands.go`/`commands.go`) and
registering it in both places.

MCP tools are registered with `mcpsdk.AddTool` in `internal/mcp/tools.go`
(schemas inferred from struct tags). There are 21 registrations: 17 core, 4
optional `mnemos_ruminate_*` tools enabled by `[rumination].enabled`. Resources
are read-only JSON snapshots in `internal/mcp/resources.go`. Tool descriptions are
**load-bearing** — they are tuned from capture-rate measurements in `README.md`;
treat edits to them as behavioural changes, not copy edits.

## Configuration and environment

`~/.mnemos/config.toml`, all fields optional, auto-created on first run. Sections:
`storage`, `search`, `server`, `embedding`, `vault`, `dream`, `rumination`. Defaults
live in `config.Default()` in `internal/config/config.go`; `Load` applies defaults,
expands `~`, then validates. `mnemos config` prints the resolved config.

Environment variables:

- `MNEMOS_DISABLED` — non-empty makes every `mnemos hook` and `mnemos prewarm`
  no-op. The verify off-arm depends on this; do not remove the check.
- `MNEMOS_API_KEY` — API key for `mnemos serve --http :PORT`.
- `CLAUDE_CONFIG_DIR` — relocates Claude Code config; honoured by the installer and
  hook wiring. Keep installer and hook paths in agreement.

## Version bumps — five files, one commit

A release version lives in more than one manifest and CI only enforces part of it:

| File | CI check |
| --- | --- |
| `plugin.json` | `jq .` + must be **byte-identical** to `.claude-plugin/plugin.json` |
| `.claude-plugin/plugin.json` | same diff check |
| `marketplace.extended.json` | `jq .` |
| `.claude-plugin/marketplace.json` | `jq .` (its plugin entry version has historically drifted) |
| `.claude/skills/mnemos/SKILL.md`, `.agents/skills/mnemos/SKILL.md` | frontmatter `version:` |

Known drift to be aware of (as of v0.11.0): `.claude-plugin/marketplace.json`
still declares `0.5.1` while everything else says `0.11.0`. If you touch versions,
consider reconciling it. Release flow is `make release V=vX.Y.Z` (tags + pushes;
GitHub Actions + goreleaser does the rest) — never hand-build release artifacts.

## Efficacy harness

`verify/` holds fixtures and the runners behind the numbers in `README.md`:

- `mnemos verify retrieval` — precision@K over `verify/retrieval.yaml`; cheap, no
  API tokens. Add a fixture entry whenever you save a high-importance memory.
- `mnemos verify behavior` — paired A/B (`on` vs `off`) over `verify/behavior.yaml`,
  expensive and Claude-CLI-specific. The arms hardcode absolute paths from the
  original author's machine (`/Users/andrefigueira/...`) — they need editing before
  they run anywhere else, including here.
- `mnemos verify capture` — single-arm: does the agent record corrections handed to it.

Only the retrieval mode is safe/cheap to run in CI or in this environment.

## Things we deliberately do not build

- Embedding providers baked into core (they go through the optional `Embedder` interface).
- Multi-tenant SaaS wiring, auth, or ACL (local-first; that lives downstream of the HTTP API).
- LLM calls from inside the memory layer.
- Automatic mid-session context injection beyond session start and explicit
  `recovery` mode.

## In-flight work: OpenSpec change queue

This repo uses OpenSpec (`openspec/config.yaml`, schema `spec-driven`). Planning
artifacts live in `openspec/changes/<change-id>/` (`proposal.md`, `design.md`,
`tasks.md`, `specs/<capability>/spec.md`); completed changes move to
`openspec/changes/archive/`. Follow the OpenSpec skills
(`openspec-new-change`, `openspec-apply-change`, `openspec-verify-change`,
`openspec-archive-change`) when the user asks to work on a change; do not invent
parallel planning documents.

**No active changes.** `add-pi-hook-parity` is archived at
`openspec/changes/archive/2026-09-18-add-pi-hook-parity/`, and its 18 requirements
were synced into `openspec/specs/` (`harness-neutral-hooks`, `pi-integration`) —
those two specs are now live and are what a new change would modify.

The archive is still the best reference for the hook architecture, because it is
where the reasoning lives. Three things in it matter before touching hook
rendering or `internal/installer`:

- **Claude Code compatibility is enforced by byte comparison, not transcription.**
  Three tests build a binary from the pre-change commit via `git worktree` and diff
  its output against the current one. A drifted format string fails them; a test
  that only transcribed the expected bytes would pass a wrong expectation. Set
  `MNEMOS_PRE_CHANGE_BIN` to point at a build, or the tests skip.
- **`installer.Install` merges rather than replaces** an existing MCP server entry,
  so adapter-only fields (`directTools`, `toolPrefix`, lifecycle keys) survive a
  re-run of `mnemos init`. This changed behaviour for every host, not just pi —
  `desiredEntry` plus the `owned` key list in `internal/installer` is the contract.
- **Tool-name matching is by suffix on both sides.** `matchesMnemosWriteTool` and
  `isFileEditToolFor` take an `allowPrefixed` / `foldCase` flag that only the
  neutral-format caller sets. The Claude Code path stays exact-match. The reason is
  that the name pi's adapter actually derives is not the one `toolPrefix` implies —
  a package-manifest route adds the package name — so a prefix match would cover
  one installation route and silently miss the shipped one.

The archive's `pi-verification.md` holds a hand-run observation of the five README
behavior scenarios in pi. It is observation, not harness output — one run per
scenario, no off-arm — and no shipped file claims a pi effect figure.

### pi constraints worth remembering

- `pi.exec` has **no stdin parameter**, so payloads travel as `--payload` argv. That
  flag exists only because of this.
- `before_agent_start` returning `systemPrompt` is the only non-accumulating
  injection channel. `tool_call` returns `{block, reason, terminate}` and nothing
  else — a system prompt returned there is silently discarded. That is why the
  pre-tool memory push does not reach the model on pi while the guardrail does.
- `session_start` fires for `startup|reload|new|resume|fork`; each is a distinct
  session, so session-scoped state must be rebuilt rather than carried over.

## Working with the repository's own memory

This repo ships a mnemos skill (`.agents/skills/mnemos/SKILL.md`, mirrored at
`.claude/skills/mnemos/SKILL.md`). If `mnemos_*` MCP tools are available in your
session, use them on this codebase:

- Record genuine corrections, project conventions, and architectural decisions as
  you make them (`mnemos_correct`, `mnemos_convention`, `mnemos_save`) — the store
  starts empty and only compounds if you write to it.
- Close your session with `mnemos_session_end` and a real summary.
- `graphify-out/` holds a pre-built knowledge graph of this codebase
  (commit `a4014537`); prefer `graphify query "…"` over grepping for
  architecture questions, and rebuild with `graphify update .` after code changes.
