# Design

## Context

See proposal.md. Baseline `57e8c09` uses TOML for core services but `MNEMOS_CONFIG` selects the file, `MNEMOS_DISABLED` disables hooks and pi reads `MNEMOS_BIN`/`MNEMOS_CONTINUITY`. Installer client directories also inspect host variables. Verify inherits these to redirect global hooks; removing only the runtime reads would make that harness unsafe.

## Goals / Non-Goals

**Goals:** all Mnemos application settings derive from files/defaults or explicit operation arguments; one authoritative feature configuration; no secret config projection to pi; identity-safe startup after reload; evidence-based read-only workspace diagnostics; truthful migration and isolated acceptance.

**Non-Goals:** changing the OS/third-party host's own environment semantics, replacing OS home/PATH discovery, discarding safe Git child environment sanitization, adding a TOML parser to the Node package, changing SQLite or continuity wire semantics, introducing a local workspace registry or required project marker, performing workspace data merges/rewriting retry identities, or running paid behavior evaluation.

## Decisions

### File authority and explicit selection

Keep `~/.mnemos/config.toml` as the standard file. Ignore former product variables entirely, without environment migration or fallback. Provide `mnemos --config <path> <command> ...` using context-carried config selection, not mutable globals. Existing command-specific file flags remain explicit selectors; reject conflicting selectors rather than silently choose a different store. Validate arguments before installation effects.

Extend existing TOML with `[integration] disabled=false`, `[continuity] enabled=false`, and `[clients] claude_config_dir="", pi_agent_dir="", claude_desktop_config_path=""`. Paths expand home consistently; explicit directories are validated appropriately. Client defaults are conventional OS-home-based locations. The continuity flag governs automatic integration, not availability of explicitly invoked MCP progress tools. Disabled suppresses automatic hooks/prewarm without opening the store. Do not introduce a global `continuity.workspace_root`, which cannot represent independent non-Git projects.

Resolve relative storage/vault/client paths against the selected configuration file directory, so an identical file cannot select different data merely because process cwd changed. Inject `search.default_limit` and `search.max_context_tokens` into the memory service's omitted-input defaults, and `search.prewarm_max_tokens=500` into every prewarm composer. Explicit per-call limits retain their existing semantics. `vault.enabled=false` rejects export/watch before writes; status remains readable. These formerly ineffective fields and the vault behavior change are covered by tests and migration docs.

Expose strict `LoadReadOnly(path)` plus explicit-selector metadata (`HasExplicitPath(ctx)`): read-only projection/doctor may use built-in defaults only when the implicit conventional file is absent, without creating it. A missing explicitly selected file is an error. Normal first-use default creation retains its existing documented policy outside the read-only modes.

Share config selection through `config.ContextWithPath(ctx,path)` and `config.PathFromContext(ctx)` (or equivalent constructor-injected state), with no process-global override. Installer consumes explicit paths and propagates a nondefault selected config into MCP arguments and owned hook commands. Its route recognizers understand those config-bearing commands; idempotency and unrelated-key preservation remain.

### Pi bootstrap uses an existing file

Read the package-relative `mcp.json` Mnemos server entry for its executable and explicit config selector. No new binary-path setting and no second feature JSON file. The installer normalizes relative executable/config paths against this manifest before writing an absolute installed route. Runtime must not resolve only the hook route differently from the adapter's MCP route: reject cwd-dependent relative paths when the adapter cannot guarantee the same interpretation. A configured bare program name can retain OS executable lookup. Reject ambiguous/malformed bootstrap entries without executing arbitrary shell commands.

`mnemos [--config path] config --client pi` emits only `{protocol_version:1, disabled:<bool>, continuity:{enabled:<bool>}}`. It loads file configuration without opening SQLite, initializing an embedding provider or outputting server/provider keys. The extension validates the projection and refreshes at each real session_start; config/response state is tied to the current generation. A superseded response cannot affect the current session. Malformed configuration never becomes a successful enabled startup or guessed legacy session.

Every real startup retains independent start-key semantics. Current unknown-outcome retries and guardrail-only handling stay intact. When valid configuration disables automatic integration, there are no automatic startup/event calls; explicit MCP remains separate.

### The agent declares a non-Git root for the current invocation

Use the user-selected sequence without a registry lookup or registration step:

1. Use an explicitly supplied root or a valid Git worktree root. An explicit root must satisfy the existing canonical-directory, cwd containment and worktree consistency checks; it cannot conceal broken/inaccessible Git metadata.
2. For unresolved non-Git work, the host agent considers an explicitly identified host workspace, directory structure, build files and the user's task to determine the intended boundary. A build manifest alone does not prove a project boundary: a multi-module project can contain several manifests. This reasoning happens in the host agent, never through an LLM call from Mnemos.
3. The agent passes the selected root as the existing `workspace_root` invocation parameter. Mnemos validates it and returns the exact current session identity/root. Save the existing session binding required by continuity, not a separate registry entry or new config mapping. No marker file is created or required.
4. If the boundary is ambiguous, the agent presents candidates and leaves continuity unbound until the root is made explicit. Mnemos does not select a common ancestor, basename, most recent session or historical binding as a substitute.

`workspace_root` is invocation context, not an application configuration item. Within the confirmed session, callers retain the returned root and current session ID and supply them on subsequent progress operations; a root cannot silently change under that identity. A new session must receive the root explicitly again, from the host or the agent's current assessment. This deliberately gives up registry-based automatic root reuse across sessions, without changing the existing canonical-root hash identity for equivalent declared roots.

For pi startup without a verified root, retain the unbound/guardrail-only behavior. The agent can open and manage an explicit continuity session with its declared root; that explicit session is not silently adopted by the extension's automatic lifecycle hooks. The skill must distinguish the exact session it opened from any source/recovery ID and close it explicitly. Do not add a lifecycle-attachment bridge, hidden history lookup or persistent root cache as part of this configuration change.

### Doctor diagnoses existing roots without registering or merging them

The user's term is `mnemos doctor`, not Docker. Add `doctor --workspaces` with optional JSON rendering as an explicit workspace-only diagnostic mode. Its input is the selected configuration file plus existing `session_continuity_bindings` and aggregate session/checkpoint metadata in one read snapshot. This is local administrative metadata inspection, not an expansion of ordinary scoped MCP reads. Do not read private goals, checkpoint bodies, source files or credentials to generate similarity scores.

This mode must read configuration without creating a default file, open an existing database with SQLite read-only/query-only settings, and skip schema migration, WAL-mode changes, embedding probes, business counters and normal installer checks. The current general doctor uses `config.Load` and `storage.Open`, which can create/migrate state; reusing that setup would violate the new diagnostic's read-only contract. In WAL mode, the reader requires pre-existing regular `-wal` and `-shm` sidecars, so it can include committed WAL rows without creating or deleting source files. SQLite may update transient reader marks in the existing `-shm`; tests assert that the main database and WAL bytes, source directory entries, sidecar presence and size, and business counters remain unchanged from the diagnostic itself. Another concurrent writer may change the source while a read is in progress. An unsupported schema, read failure or incomplete filesystem probe is reported explicitly rather than repaired during diagnosis. A missing database means there is no stored workspace history; an existing database with zero bindings returns an empty successful report.

| Evidence | Classification and advice |
| --- | --- |
| Live paths resolve to the same canonical physical directory | Verified path equivalence; a consolidation candidate, not proof that historical bindings can be rewritten safely |
| A non-Git root contains another recorded root | Possible boundary split; agent/user must determine whether these are modules or independent projects |
| Missing historical root and a similarly named available root | Possible move; insufficient evidence for an automatic identity change |
| Separate validated clone/worktree roots | Keep separate by default, even with matching names, branch, HEAD or shared repository metadata |
| Only lexical similarity or inaccessible metadata | Weak/unknown evidence; never label safe to merge |

For each candidate, report source/possible target root and key, evidence/classification, affected session/open-session/checkpoint/clear-marker counts, and observable conflicts. Active sessions, overlapping startup retry identities and potential scope/visibility changes must be visible as blockers or uncertainties. A diagnostic may identify a possible consolidation but cannot certify a valid migration merely because paths look alike. Ordinary observations/skills are scoped by legacy agent/project, not these roots, and are not relabelled or counted as automatically merged knowledge.

Keep sorting and candidate rules deterministic, without a model call or network lookup. Read metadata and probe only already recorded roots; do not recursively crawl arbitrary directories. Proposed TOML `[diagnostics.workspaces]` controls `max_roots=1000`, `max_candidates=200`, and `timeout="10s"`. Budget exhaustion marks partial coverage and unknowns; omitted comparisons are never reported as clean. Findings without corruption are advisory, including an empty result. Read/protocol failures remain errors.

Return the report to stdout/JSON, with no candidate table, alias map, automatic registration, background monitor or stored similarity score. The agent can use the evidence to review the intended boundary, but a new session still needs an explicit root. Actual data consolidation is a separate future operation requiring explicit source/target selection, a dry-run migration plan, identity/retry/visibility conflict rules, backup and transaction semantics; no such mutation command is included here.

### Verification and examples must follow the new authority

Use explicit argv/config files to isolate all candidate subprocesses and the pi bootstrap. The Python snapshot runner emits store configs and MCP/hook argv with explicit selectors. Behavior runner settings must prevent globally installed hooks from touching a live store or emitting into the off arm; if a safe isolated host-settings launch cannot be established, fail before launching the model rather than rely on ignored environment overrides. Existing auth must never be copied into deliverables. Runner binary/interpreter selections are explicit runner arguments or file entries, not `MNEMOS_*` environment overrides.

Update public examples to read URL, key and project choices from a file or derive nonconfiguration context; no stale environment configuration advertised. Keep historical archived records historical. Update live specs, skills, Chinese diagrams, installation and offline instructions to the new contract.

## Risks / Trade-offs

- Existing environment-only installations stop applying those options → document exact TOML/CLI equivalents and update actual local integration only after review and tests; do not silently import environment values.
- Config selection lost in a subprocess could hit a different store → real executable, installer and pi SDK tests assert the same explicit path, including spaces and conflicting old variables.
- Pi startup adds one bounded config projection process → no database/provider work; only once per real session generation.
- Agent assessments can differ between new non-Git sessions → make the chosen root explicit and visible in each receipt; do not promise cross-session agreement through a registry or repair differences with heuristic historical adoption.
- Similar paths can be distinct projects, and live equivalence does not establish historical identity → report evidence and merge risks without mutating bindings, hashes or visibility. Assert main database/WAL bytes, directory entries, sidecar presence and size, and business counters remain unchanged, including on missing files, old schemas and probe failures. Existing WAL `-shm` bytes may reflect SQLite's transient reader marks; no sidecar may be created or deleted.
- Removing environment isolation can invalidate behavior measurements → isolated settings/explicit argv and fail-closed checks, without making unmeasured efficacy claims.

## Migration Plan

Move deliberate feature choices into TOML, move client directory overrides into `[clients]`, put the actual binary/config selector in the package MCP file, and reconnect hosts. Preserve original user keys and data. No database downgrade/migration is required. Verify candidate configuration, Go/pi regressions, actual Codex and pi clients, local package updating and review before committing/deployment. Refresh the offline handoff so its instructions match the new configuration model.
