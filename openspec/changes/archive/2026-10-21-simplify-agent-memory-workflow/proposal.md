# Proposal

## Why

Mnemos currently assembles session and memory context through several transport paths, exposes a broad MCP surface to every task, and relies on a manually launched dream watcher for deterministic maintenance. A small, explicit task-agent surface plus a fixed maintenance profile and opportunity checks at effective task-session starts will make routine use simpler while preserving the host as the only model runtime.

## What Changes

- Introduce two logical responsibilities: the **Task Agent** uses memory and progress during work; the **Maintenance Agent** reviews knowledge quality and performs installation, configuration, backup, or migration only when an explicit task or policy authorizes it. One hosted agent may switch responsibilities or delegate; Mnemos does not launch an LLM.
- Add a shared application-layer session/context assembly entry point so MCP, HTTP, CLI and host adapters carry one resolved scope and receipt while retaining existing v1 continuity, legacy hook and session behavior.
- Add a startup-fixed TOML-selected MCP profile. In 0.17.0, `core` becomes the default and exposes exactly `session_start`, `session_end`, `search`, `get_many`, `save`, `correct`, `convention`, `checkpoint`, and `resume_context`; this is a breaking change to the default MCP tool surface. `maintenance` restores the existing full MCP tool set. CLI/API behavior, legacy hook interfaces, data identities, and continuity receipts retain their separate compatibility contracts. Tool lists do not change dynamically after startup.
- Add default-on, opportunity-only deterministic maintenance at every effective task-session start (MCP, host-hook prewarm, CLI continuity open, and explicit HTTP session-start), reusing the existing dream and rumination services. TOML `[maintenance]` settings default to `enabled = true`, `interval = '24h'`, `max_pass_duration = '5s'`, `lease_duration = '15s'`, and `renew_interval = '2s'`; all duration values are configurable and validated. One database lease/CAS serializes automatic, manual, and watch passes. Persist only the last successful run, active lease, the unfinished cycle ID and decay-applied marker needed for safe crash recovery, and existing candidate status; do not persist diagnostic prose or rendered context. Semantic resolve/dismiss remains an explicit decision by the active host agent; preserve only the existing deterministic provenance-tag auto-resolution.
- Keep agent/project knowledge scope separate from owner/workspace/session progress scope. Preserve the current knowledge labels and lookup compatibility, the v1 owner/root/revision/request receipt semantics, and legacy interfaces. Same-basename project labels may still share knowledge; callers may explicitly choose distinct labels. This change adds no project registry, root-derived hash, automatic alias, or knowledge migration.
- Update the existing Chinese architecture document before implementation and again after verification, using that same document to distinguish target behavior from shipped behavior; complete it before this change is archived.
- Document that opportunity maintenance is best-effort when an effective task session starts. It does not promise a scheduled run while no eligible session starts or guarantee that another host can provide a delegated Maintenance Agent. Strict schedules remain an optional external scheduler concern; no daemon or OS task installation is added.

## Capabilities

### New Capabilities

- `agent-workflow`: stable task and maintenance MCP profiles, shared session/context entry point, role boundaries, and explicit host-agent maintenance decisions.
- `opportunistic-maintenance`: due checks and cross-process lease/CAS around deterministic task-session-start maintenance, with no embedded model or always-on scheduler.

### Modified Capabilities

- `memory-context-delivery`: assemble session context through one application entry point while retaining bounded selection, safety, surfacing, and continuity rendering contracts.
- `memory-reading`: preserve agent/project as the knowledge visibility scope and define compatibility when project labels collide.
- `session-continuity`: carry the existing validated owner/workspace/session receipt through the unified entry point without changing v1 identity, retry, CAS, clear, or visibility semantics.

## Impact

The change affects `internal/prewarm`, `internal/continuityflow`, MCP/HTTP/CLI construction, `internal/dream`, `internal/rumination`, configuration, storage migrations for maintenance coordination, and architecture/operator documentation. It adds no model/provider dependency and does not add a resident daemon. Existing store identity and progress visibility remain the compatibility boundary.
