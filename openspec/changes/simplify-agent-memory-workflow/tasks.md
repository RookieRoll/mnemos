# Tasks

## 1. Shared application session and context entry

- [x] 1.1 Add the application-level task-session/context input and receipt types, resolving knowledge labels separately from verified continuity scope; verify the app-service unit tests cover exact owner/workspace/session receipt propagation and fail-closed root resolution.
- [x] 1.2 Route MCP, HTTP, CLI and supported legacy hook paths through the shared application entry point; verify equivalent-input tests return the same selected context and underlying receipt while preserving each transport envelope.
- [x] 1.3 Add compatibility tests for legacy session and hook payloads, exact continuity retry/CAS/clear behavior, and two roots with the same project label; verify checkpoint visibility remains isolated by the v1 owner/workspace rules.
- [x] 1.4 Document the application entry point and the knowledge-label versus progress-scope boundary in the affected service/transport references; verify examples do not infer roots or imply project labels isolate progress.
- [x] 1.5 At apply start, update the existing `docs/ARCHITECTURE.zh-CN.md` in Chinese with the target two-role model, system layers, nine core tools and maintenance CLI, task-session lifecycle, minimal persistence, maintenance sequence, and stdio scheduling limits; include Mermaid diagrams, mark target behavior as not yet implemented, and verify all target claims match proposal/specs.

## 2. Startup-fixed profiles and explicit maintenance CLI

- [x] 2.1 Add TOML defaults and validation for `core`/`maintenance` MCP profiles and `[maintenance].enabled`, `interval`, `max_pass_duration`, `lease_duration`, and `renew_interval`; verify defaults (`true`, `24h`, `5s`, `15s`, `2s`), custom valid budgets, non-positive durations, invalid ordering, caps (`max_pass_duration <= 5m`, `lease_duration <= 10m`), and invalid profile/config values fail before server startup.
- [x] 2.2 Register the fixed profile tool sets at startup; verify `tools/list` contains exactly the nine required core tools and maintenance adds only existing advanced tools, with no dynamic profile changes during a process lifetime.
- [x] 2.3 Add the `mnemos maintenance` JSON CLI bridge for candidate evidence and explicit resolve/dismiss operations using existing rumination validation; verify JSON tests cover accepted and rejected decisions under the core profile without MCP tool-list mutation.
- [x] 2.4 Document profile selection, the JSON CLI bridge, and explicit authorization boundaries for install/config/backup/migration; state that 0.17.0's default core tool surface is breaking and `maintenance` restores the previous full MCP set. Verify the upgrade instructions match config/command tests and use only the active host-provided Task Agent or Maintenance Agent.

## 3. Shared dream lease and crash-safe cycle state

- [x] 3.1 Add the SQLite maintenance coordinator migration and owner-token lease API using validated TOML `time.Duration` budgets (defaults: 5-second maximum pass, 15-second lease expiry, 2-second renewal), takeover, and compare-and-set completion; verify custom durations reach the coordinator, busy claims are non-blocking, and stale owners cannot renew, release, or advance last-success.
- [x] 3.2 Route one-shot dream and `dream --watch` passes through the same coordinator used by opportunity runs; verify manual runs may bypass due checks but all entry points contend on the same lease and cancellation follows lost ownership.
- [x] 3.3 Make decay cycle-aware by committing `cycle_id`, `decay_applied`, and the importance updates atomically after checking the active owner and lease; verify takeover resumes the same cycle, a crash after decay skips a second decay, and a successful pass creates a new cycle for later work.
- [x] 3.4 Implement owner-token, cycle-ID, and unexpired-lease fencing in the transaction that commits every other mutating dream stage, including prune, promotion, rumination detection/auto-resolution, and journal writes; preserve the existing `promoted-origin` key. Add restart, lease-expiry, and stale-owner tests for each stage so an expired or replaced owner cannot commit later writes, and document any bounded replay behavior.

## 4. Opportunity maintenance and operator behavior

- [x] 4.1 Trigger due deterministic maintenance from MCP session-start, host-hook prewarm session-start, CLI continuity open, and explicit HTTP session-start, honoring both `[maintenance].enabled` and `[integration].disabled`; verify ordinary doctor/export/search/context/recovery/checkpoint calls do not trigger it, `Run(ctx, false)` creates no dream observation, and timeout leaves the committed session receipt visible.
- [x] 4.2 Keep candidates needing semantic judgment pending and expose evidence/resolve/dismiss through the maintenance profile and JSON CLI, while retaining only the existing deterministic `ruminated-from:<id>` auto-resolution; verify no semantic decision or model/provider call occurs automatically.
- [x] 4.3 Document best-effort session-start timing, no-daemon limits, the shared dream lease, all `[maintenance]` duration keys/defaults/validation bounds, minimal persisted coordination fields, and backup/migration behavior; verify all stated config keys, profile names and CLI examples match command/config tests.
- [x] 4.4 Update the same `docs/ARCHITECTURE.zh-CN.md` with implemented behavior, compatibility boundaries, implementation status, and actual verification evidence; include the two roles, layer and lifecycle Mermaid diagrams, nine-tool/core versus maintenance CLI path, cycle/lease persistence, and stdio scheduling limits, then verify claims against passing tests before archive.

## 5. Integration verification

- [x] 5.1 Run the relevant package tests, full `go test ./...`, `go vet ./...`, and migration/config validation; verify failures are resolved or recorded with their environment limitation before review.
- [x] 5.2 Run the cross-transport acceptance tests for fixed profiles, shared receipts, collision compatibility, automatic-maintenance suppression, lease takeover, decay replay, and next-cycle behavior; verify the complete suite passes without a model request or background scheduler.

## Workflow follow-up

After the 19 implementation tasks pass review, prepare the 0.17.0 delivery as
one coordinated release-preparation step: update the eight distribution version
files together in one commit; build a stripped, CGO-free binary from the exact
source commit; make a consistent SQLite backup before updating the local
installation; update the local binary and independent pi package copy; run the
core-nine and maintenance compatibility gates plus package-only pi update; and
produce an offline ZIP without installation scripts, then verify its extracted
contents. Only after delivery review, sync the main specs, archive this change,
and commit/push the archive. Passing the 19 implementation tasks alone does not
mean delivery or archival is complete.
