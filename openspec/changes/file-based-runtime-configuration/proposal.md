# Proposal

## Why

Continuity, disable switches and executable/configuration paths currently depend on inherited process environments. Users cannot inspect or reliably update the complete effective configuration from files, particularly after installing an independent pi package on an offline machine.

## What Changes

- **BREAKING:** Mnemos product settings no longer read `MNEMOS_*`, `CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR` or `APPDATA` overrides. Move feature switches and client directory overrides into TOML; choose nondefault config files explicitly with CLI arguments.
- Add TOML `[integration].disabled`, `[continuity].enabled` and explicit `[clients]` paths. Existing storage/search/server/embedding/vault/dream/rumination file settings remain. Do not add a global workspace root, local workspace registry or required project marker. For non-Git work, the agent explicitly supplies a root for the current invocation after identifying the intended project boundary.
- Make existing `search.default_limit`, `search.max_context_tokens` and `vault.enabled` effective. Add `search.prewarm_max_tokens=500` so the existing startup budget stays unchanged by default. Disabled vault configuration rejects export/watch before effects, while status remains available.
- Pi reuses the installed package's `mcp.json` command/config arguments for hooks, and obtains only its nonsecret runtime settings through a validated `config --client pi` projection. It no longer reads environment variables for product settings.
- Carry the selected config through CLI operations, installer-generated MCP entries and hooks. Preserve unrelated user configuration and independent package installation.
- Replace environment-based evaluation isolation, examples and documentation with file/argument based equivalents; test ignored conflicting environment values.
- Add a read-only `doctor --workspaces` diagnostic mode for existing continuity roots: show verified path equivalence, possible boundary splits/moves, affected counts and blockers. Do not merge bindings or introduce an identity registry. Bound the scan with file-configured limits and report incomplete coverage explicitly.

## Capabilities

### New Capabilities
- `runtime-configuration`: File-authoritative application settings, explicit config selection and a nonsecret adapter projection.
- `workspace-diagnostics`: Evidence-based diagnosis of existing workspace identities and possible consolidation, without changing identity, sharing or history.

### Modified Capabilities
- `pi-integration`: File-driven automatic continuity and disable behavior with a shared executable/configuration route.
- `agent-installation`: File-configured client discovery and config propagation to installed entries/hooks.
- `memory-evaluation`: File/argument based isolated test stores and on/off behavior without product environment switches.

## Impact

Go config/CLI/installer and a separate workspace diagnostic service/read-only metadata reader; pi runtime and tests; verification runners, examples and live documentation/skills. No new runtime dependency, schema migration or changes to checkpoint ownership/revisions. Workspace resolution keeps the existing explicit-root/Git contract; agent-side guidance clarifies how to supply non-Git roots, without a new discovery mechanism in the memory layer. Diagnostic findings are transient advice, not root declarations or merge receipts. Operating-system home/executable discovery and safe child-process environment handling are not application configuration sources; external hosts retain their own settings. User's explicit file-only requirement supersedes the earlier documented environment-switch invariants.
