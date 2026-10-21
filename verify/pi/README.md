# Explicit pi profile and checkpoint schema verification

`checkpoint-schema.mjs` is a manually invoked, model-free integration gate. It
uses an existing pi SDK and pi-mcp-adapter installation; it installs nothing,
does not run `mnemos init`, and never reads authentication or model configuration.
It is not part of automatic CI. Git and Node compatible with your pi SDK are
required. The script targets the pi 1.3 SDK API and pi-mcp-adapter direct tools.

Build the candidate mnemos binary first, then run from the repository root:

```sh
node verify/pi/checkpoint-schema.mjs --profile core --sdk /path/to/pi-coding-agent --package /path/to/mnemos/pi --adapter /path/to/pi-mcp-adapter --binary /path/to/candidate/mnemos
node verify/pi/checkpoint-schema.mjs --profile maintenance --sdk /path/to/pi-coding-agent --package /path/to/mnemos/pi --adapter /path/to/pi-mcp-adapter --binary /path/to/candidate/mnemos
```

Pass `--profile core` and `--profile maintenance` in separate runs. Each run
writes that explicit profile to its isolated TOML file and checks the exact MCP
tool-name set: nine task tools for `core`, or the existing 27-tool surface for
`maintenance` with rumination enabled. The maintenance run also calls the
existing maintenance-only `mnemos_stats` tool once. Both profiles exercise the
checkpoint lifecycle through direct adapter tools.

All four paths and the profile are required command-line arguments. The verifier does not read
process environment variables for its binary, package, SDK, or adapter selection.
Optional `--temp-parent /path` selects an existing writable directory for the
retained fixture.

The script copies only the mnemos package's manifest, extensions, and skills to
a temporary directory, writes that copy's `mcp.json` to launch the **candidate
binary with an absolute `--config` path**, and loads it with the actual SDK and
adapter. The original package and global installation remain unchanged. Hook
execution uses the same candidate and explicit config path.
Home, pi settings, Git workspace, mnemos config, and SQLite database are isolated.
Network fetches fail, credentials are empty in memory, and no model prompt is run.

The gate examines the exact profile tool list and checkpoint schema visible through the SDK, then executes actual
adapter tools for independent session opens, save, explicit resume, exact replay,
a normal update, clear, and clear replay. It asserts `payload.workspace` is absent
from input, server-sampled workspace remains in output, and submitting forged
workspace metadata returns validation without changing the old source or revision.
The retained fixture includes `agent-checkpoint-schema.json` and `transcript.json`
with synthetic inputs/results for review. Success prints its path and isolation
details. `provenance.json` records the candidate's SHA-256/version, parsed MCP
command, and matching source/copy SHA-256 for every extension TypeScript file.
The output counts adapter direct executions and actual provider requests (zero).
Only existing local package/SDK/adapter directories and a local executable are
accepted; remote package specs and `npx` are not used. Delete that fixture after
review using your normal file manager.

Calls use the SDK's public registered tool definition and its adapter `execute`
method. This exercises real local MCP discovery and execution, but does not
simulate model tool selection, a full TUI turn, or its tool-call hooks. It does
not measure model behavior or capture quality. Adapter/SDK API changes may require updating this explicit
gate; failures must not be interpreted as model performance measurements.
