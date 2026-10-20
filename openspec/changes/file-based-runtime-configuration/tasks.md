# Tasks

## 1. Core configuration and service consumers

- [x] 1.1 Add file-authoritative switches/client/diagnostic/prewarm fields and context-carried selectors; test ignored old environment, relative paths, explicit conflicts and read-only missing-file handling.
- [x] 1.2 Wire CLI/hook/prewarm/serve/continuity to the selected file and add the nonsecret pi projection; test no DB/provider/config writes in projection and disabled hooks.
- [x] 1.3 Honor configured search/context/prewarm budgets and vault enablement; test nondefault omitted-input limits and disabled vault without output effects.
- [x] 1.4 Document defaults and breaking file/argument migration in configuration reference and CLI help; verify examples and field inventory match implemented consumers.

## 2. Installed client route consistency

- [x] 2.1 Replace installer environment discovery with injected file paths and propagate config selectors to owned MCP/hooks; test spaces, custom directories, package reuse and idempotency.
- [x] 2.2 Normalize installed routes and preserve unrelated commands, servers and adapter fields; test formerly relative entries and conflicting route diagnostics.
- [x] 2.3 Update installation instructions and agent skills for explicit non-Git roots without registries or required markers; verify the exact session/root lifecycle guidance.

## 3. Pi file configuration

- [x] 3.1 Load the package MCP route and validate the versioned nonsecret projection without product environment reads; test absolute paths, rejected ambiguity, conflicting old environment and missing config.
- [x] 3.2 Make config/route/invoker session-generation scoped and preserve unknown-startup safeguards; test reload/late responses, disabled mode and unbound guardrails.
- [x] 3.3 Update package runtime inventory, pi docs and real SDK verifier to explicit config argv; run the complete pi suite and an isolated real SDK gate.

## 4. Read-only workspace diagnostics

- [x] 4.1 Implement snapshot metadata reading from existing SQLite without migrations, source-sidecar creation/deletion or journal-mode changes; test absent store, old schema, committed WAL visibility, main database/WAL byte integrity, sidecar presence/size and business counters (existing `-shm` reader marks may change temporarily).
- [x] 4.2 Implement evidence-based candidate classes/counts/conflict labels with file-configured budgets; test aliases, parent-child/missing roots, independent worktrees, truncation and cancellation.
- [x] 4.3 Add doctor --workspaces text/JSON before mutating general-doctor setup; test malformed flags, explicit selectors, empty reports and no disclosure of private bodies/retry values.
- [x] 4.4 Document diagnostic classifications and merge limitations; verify no registry, mutation or safe-merge certification is advertised.

## 5. Evaluation and examples

- [x] 5.1 Replace runner environment routing with explicit scratch configs/MCP/hooks and isolated host settings; unit-test snapshot consistency, ignored global routes and fail-closed behavior before model launch.
- [x] 5.2 Replace product environment configuration in public hook examples/live docs and update Chinese architecture diagrams; validate shell/Python/Markdown references and ensure no stale product-setting instructions remain outside historical records.

## 6. Integration and acceptance

- [x] 6.1 Run full Go regression, vet, formatting/manifest checks and independent cross-review; resolve findings and record local tooling/CI limits truthfully.
- [ ] 6.2 Verify real Codex and pi MCP clients against the candidate using isolated stores, explicit config paths and conflicting old environment; verify installed-package update succeeds without network bypass.
- [x] 6.3 Verify no-registry non-Git explicit root identity and diagnostic integrity end-to-end; preserve owner/revision/clear/retry behavior and report-only merge semantics.
- [ ] 6.4 Synchronize live specifications and produce a versioned installation/offline handoff with configuration migration and per-file hashes; verify actual installed binary/package and extracted handoff.

## Workflow follow-up

- Archive after independent review and acceptance, then commit/push according to the user's established workflow.
- Published releases continue to use tags/CI; local development installations and offline handoffs are not published release artifacts.
