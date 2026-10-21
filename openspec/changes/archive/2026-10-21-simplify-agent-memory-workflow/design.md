# Design

## Context

See `proposal.md` for the product goals and the spec deltas for externally visible behavior. Today session composition is split across transports, MCP registers a broad tool set, continuity is already workspace-bound and receipt-based, and `dream.Service.Run` performs several persistent stages before optionally writing a dream observation. The new workflow must keep those existing contracts while making the shared path and maintenance coordination explicit.

## Goals / Non-Goals

**Goals:**

- Provide one application service for task-session start, resolved knowledge scope, verified progress scope, context composition, and returned receipts.
- Select a fixed `core` or `maintenance` MCP profile before server startup, while keeping a JSON-capable CLI bridge for explicit maintenance tasks.
- Run deterministic maintenance opportunistically at effective task-session starts with one shared database lease used by automatic, manual, and watch passes.
- Make crash recovery and replay behavior explicit, preserve candidates for host review, and keep all model judgment in the host-provided Task Agent or Maintenance Agent.

**Non-Goals:**

- No model/provider integration, agent scheduler, daemon, OS task, or claim that stdio can wake an agent.
- No new project identity system, basename rewrite, registry, aliasing, or bulk knowledge migration.
- No dynamic MCP tool registration and no change to v1 continuity ownership, root, retry, CAS, clear, or checkpoint visibility rules.

## Decisions

### Shared application composition and scope

Add an application-level session/context service that accepts normalized task-session input and returns both the established lifecycle receipt and the composed context. MCP, HTTP, CLI, and host adapters translate their own envelopes into that service and render the returned result; they do not resolve scope or reimplement selection policy. The service resolves the existing `agent_id/project` knowledge labels independently from the existing continuity `owner/workspace/session` receipt. It binds context reads to the exact receipt returned by session start. If context composition fails after session start, the committed receipt remains visible to the caller.

Continuity keeps the existing canonical-worktree or explicitly supplied non-Git root rules. The application service does not infer a root from the current directory, project label, history, or shared ancestor. Legacy hook interfaces remain adapters into the same behavior. Knowledge continues to use its legacy labels: same agent/project labels across roots continue to share knowledge, while progress stays isolated by verified workspace and owner. Callers may supply distinct project labels for new records. A future identity migration would be a separately reviewed change.

### Fixed MCP profiles and explicit maintenance bridge

Parse `[mcp].profile` once during configuration validation and register the selected tools before serving. Omission means `core`; any unknown value fails startup. `core` registers exactly the nine task tools stated in the spec. `maintenance` restores the existing full MCP tool set. Restarting the server is the only way to change profiles; there is no runtime tool-list mutation. The 0.17.0 default is intentionally a breaking change for MCP clients that relied on an advanced tool while leaving the profile unset. Upgrade guidance must tell those operators to set `[mcp].profile = "maintenance"` before restarting.

Add a documented `mnemos maintenance` command family with stable JSON input/output for candidate inspection and explicit resolve/dismiss actions. It delegates to existing rumination validation and persistence, and does not duplicate business rules in the CLI. This bridge makes explicit maintenance possible when the long-lived MCP server uses `core`; it does not make the MCP server wake a model. Installation, config edits, backup, and migration still require an explicit task or configured policy. GPT-6 Luna/OpenAI is not a product dependency; both responsibilities are supplied by the active host.

### One coordination lease for every dream pass

Introduce a small database coordinator for dream execution with an owner token, lease expiry, heartbeat, bounded maximum duration, last-success timestamp, and an unfinished `cycle_id` plus `decay_applied` marker. Every entry point (`dream` one-shot, `dream --watch`, and opportunity maintenance) must acquire the same lease. Manual entry points may bypass only the due-time check. A failed claim returns a busy result without starting any pass work. These fields are the complete durable recovery record; do not add run history or diagnostic text.

Maintenance budgets are file-configurable through `[maintenance]`: `enabled = true`, `interval = '24h'`, `max_pass_duration = '5s'`, `lease_duration = '15s'`, and `renew_interval = '2s'` are defaults, not constants. TOML duration strings are parsed once by the existing config package and injected into the coordinator as validated `time.Duration` values; the driver does not own or duplicate defaults. All duration values must be positive. `max_pass_duration` must be shorter than `lease_duration`, `renew_interval` must be shorter than `lease_duration`, `max_pass_duration` is capped at 5 minutes, and `lease_duration` at 10 minutes. Invalid values or combinations fail config validation before server startup or automatic work. The Pi nonsecret projection does not include these settings.

Session-start acquisition is non-blocking: a busy lease skips the opportunity. The configured maximum duration bounds automatic, one-shot, and watch passes; a pass context is canceled on renewal failure or duration expiry. Every mutating stage transaction checks the current owner token, cycle ID, and unexpired lease in the same transaction that commits its effects; a stale process cannot write after a replacement owner takes over. Every completion and last-success update also compares the current owner token. Expiry permits recovery after a crash, but does not by itself fence an arbitrarily paused process and is not an exactly-once guarantee. Session lifecycle/context receipts are committed before maintenance; pass timeout or failure never fails session start or hides its receipt.

Decay is the non-idempotent case: the coordinator creates one `cycle_id`, and the observation store applies `DecayImportance` only if `decay_applied` is false, setting that marker in the same SQLite writer transaction as the importance updates after validating the owner/cycle/lease. A replacement owner resumes the same cycle; after successful pass completion, the next explicit or due pass receives a new cycle and may decay once again. Prune is naturally repeat-safe, promotion retains its existing `promoted-origin` idempotency key, and rumination retains its candidate upsert/resolve semantics. Tests must cover takeover, crash immediately after the decay transaction, and creation of a new cycle after success. All later mutating stages still fence their commits on the active owner and cycle. If a stage's replay effects cannot be justified and tested, do not enable it in an automatic retry pass.

An opportunity pass invokes the existing `dream.Service.Run(ctx, false)` behavior, so it can prune, decay, promote and persist rumination candidates without creating a dream observation as an empty-run log. Explicit `dream` retains `Run(ctx, true)` and existing journal semantics. Automatic execution requires an effective task-session start, `[maintenance].enabled`, and integration not disabled. Due evaluation first checks for an unfinished cycle whose lease has expired; that cycle is recoverable even if `last_success` is recent, including when an earlier manual pass failed after running before the normal interval. Only when no unfinished cycle exists does the coordinator compare `last_success` with the configured interval (24 hours by default), or run if no success exists. This makes failure recovery independent of a later new-cycle due time without creating a new cycle for each manual run. Effective starts are MCP `session_start`, host-hook prewarm `session_start`, CLI continuity open, and explicit HTTP session-start. Ordinary doctor, export, search, context, recovery, and checkpoint calls do not open sessions and do not trigger maintenance. A contending start continues normally. The configured maximum duration bounds all pass entry points and limits session-start delay, while the committed lifecycle receipt survives a maintenance timeout.

Dream reports stage failures as an aggregated error after retaining any earlier committed stage effects; explicit journal behavior may also have written a partial summary. The coordinator advances `last_success` only when the pass returns without an aggregated error and completion succeeds under the current owner token. A failure or canceled pass leaves the cycle available for safe takeover. Lease loss or cancellation stops later stage execution, and every stage transaction independently fences its writes by owner, cycle, and unexpired lease. Decay uses its atomic `decay_applied` marker; other stages rely on their existing stable replay identity: prune predicates, `promoted-origin` keys, and rumination candidate upserts. Explicit journal writes use a stable cycle ID so retrying a cycle cannot create a duplicate journal observation. These rules describe safe replay for the identified effects and do not claim general exactly-once execution.

### Keep semantic review and durable data narrow

Deterministic maintenance can create or update existing rumination candidates, but cannot resolve/dismiss them except for existing deterministic provenance-tag auto-resolution. A host Maintenance Agent obtains evidence through the selected profile or JSON CLI, then submits the existing validated explicit decision. Without an active host agent, candidates remain pending.

Persist only maintenance last-success, active lease and stage coordination needed for safe retries, and existing candidate lifecycle state. Do not persist assembled prompts, temporary context, diagnostic narratives, or an ordinary observation just to record an empty opportunity pass. Preserve existing SQLite backup behavior and add only the coordination migration needed for cross-process CAS.

### Configuration and compatibility rollout

Add the MCP profile and maintenance settings through the existing TOML defaults/validation path. Existing config files that omit `[mcp].profile` will receive the new `core` default, so the 0.17.0 default MCP tool surface is breaking. Operators who rely on an advanced MCP tool must select `maintenance`, which restores the full existing MCP surface. The CLI and HTTP API behavior, legacy hook interfaces, stored knowledge labels, and v1 continuity owner/root/CAS/retry/clear semantics retain their separate compatibility guarantees. Opportunity maintenance defaults on with a 24-hour cadence; `[integration].disabled` suppresses automatic session-start work and operators can set `[maintenance].enabled = false` independently. The defaults and validation bounds for all four duration settings are specified above; every duration remains TOML-configurable. Older config files gain defaults without an installation-time rewrite.

Before implementation, update the existing `docs/ARCHITECTURE.zh-CN.md` with a Chinese Mermaid architecture draft that clearly marks these behaviors as target/not yet implemented. After implementation and verification, update that same document with observed implementation status and evidence. Do not create a parallel architecture document; the final update must be complete before archiving this change.

## Risks / Trade-offs

- **A lease is lost while an older worker is still running** → heartbeat renewal, maximum duration, owner-token checks before writes, cancellation, and tests that simulate renewal loss prevent stale completion; databases still cannot promise cancellation of a transaction already committed.
- **A crash occurs after a mutating stage but before pass completion** → decay's marker commits atomically with its importance updates; prune, promotion, rumination, and journal replay use their existing or specified stable identities. Restart tests cover those identities. Do not claim a universal per-stage exactly-once marker or retry any stage whose effects are not safe under its identity.
- **A long lease delays recovery after a hard crash** → choose lease duration from a measured pass bound, renew during normal execution, and recover only after expiry; expose only concise transient status, not persisted diagnostic text.
- **The core profile omits advanced review tools** → the explicit JSON CLI bridge supports evidence and validated decisions without dynamic MCP registration; docs must distinguish an active host agent from unattended maintenance.
- **Legacy project labels collide across roots** → preserve current lookup behavior and explicitly document shared knowledge scope. Verified progress remains root/owner-bound. This change does not resolve historical knowledge collisions.
- **A profile or maintenance config value is invalid** → fail config validation before starting the MCP server or attempting automatic work.

## Migration Plan

1. Add the coordinator schema and migration, with safe defaults for existing databases and no changes to observation or continuity identity.
2. Implement and test the lease and stage-idempotency contract; route existing explicit dream/watch execution through it before enabling automatic starts.
3. Add the shared application entry point and adapt transports/legacy hooks without changing their external payload contracts.
4. Add fixed MCP profiles, maintenance JSON CLI commands, and TOML defaults/validation for every maintenance setting and duration constraint. Verify default and custom budgets, rejected invalid combinations, core's exact tool set, and maintenance's additive tool set.
5. Enable opportunity maintenance with the 24-hour default at all effective session-start entry points, honoring both integration disablement and maintenance enablement. Verify journal=false and host candidate review behavior.
6. Roll back automatic work by disabling `[maintenance].enabled`; restore the prior full MCP surface by selecting `maintenance`. The config schema remains readable by older versions, but an older binary does not gain support for the new profile semantics. Rollback does not delete coordination state or migrate knowledge labels.

## Open Questions

None. The default interval, legacy label compatibility, CLI bridge, and at-least-once replay contract are specified for review before implementation.
