# Full-store backup and evolution metadata

Use the current binary's `mnemos export backup.json` and `mnemos import backup.json`
for a metadata-preserving portable round trip of the observation/session/skill
fields below. Continuity state is excluded. Back up SQLite consistently before an
upgrade. The additive migration retains existing observations and adds optional
scoped topic keys plus one owner-bound evidence table. No topic/version/check
history/source-body tables are introduced.

## Session continuity backup boundary

Logical JSON export/import does not contain `session_continuity_bindings` or
`session_checkpoints`, their revisions, or exact startup/mutation retry identities.
Export warns on stderr when such state exists, leaving stdout/file JSON valid.
If detection fails, it reports that backup scope is unknown instead of claiming
that no continuity state exists. Legacy imports neither bind old sessions nor
overwrite a local newer checkpoint or tombstone.

For full same-machine continuity recovery use SQLite's consistent online backup
API (or an equivalently consistent backup after stopping all writers), not a copy
of only the main database while WAL writers are active. This preserves current
revisions, request hashes/IDs and canonical local workspace bindings. A moved or
cross-machine workspace is not automatically rebound. Clearing a checkpoint does
not promise physical erasure from WAL, free pages or existing backups.

Before enabling continuity, restart all clients using that DB onto the capable
binary. Concurrent old/new binaries are unsupported: the old process cannot
exclude bound sessions from its legacy adoption/implicit closure heuristics.
Disable opt-in on the new binary for a non-destructive compatibility fallback;
retain added tables and a consistent backup before considering a binary downgrade.

## Observation evolution fields

The JSON keeps existing `observations`, `sessions`, and `skills`, followed by
`schema_version: 2`, optional `evidence`, and `supersession_links`. Each evidence
record names its `observation_id` and reference. Links preserve original endpoints,
`LinkedAt`, and optional `replacement_hash` (the original normalized request
digest for retry identity), including invalidated endpoints.
Observation/evidence/link export uses one coherent read transaction without
incrementing access; sessions/skills retain their existing export path, so this
is not a cross-domain point-in-time database snapshot. Source files and URLs are
not fetched or copied.

Import rejects unsupported future schema versions, duplicate owners, orphan
references/links, and conflicting existing-ID immutable identity before applying
metadata. Owners restore before references/links. Old dumps without the new fields
remain valid. Fresh-store import preserves original IDs, timestamps, lifecycle,
topics and latest caller-reported evidence markers without rechecking sources.
Other graph-edge types retain their existing narrower/lossy snapshot portability;
the new section promises supersession fidelity only.
A repeated matching import is idempotent: existing-ID rows remain skipped, and
local newer access/pin/trust/check state is not overwritten with an older dump.
Imported evidence is never attached to an unrelated row merely because its ID
collides. Import is a trusted backup operation; it preserves stored provenance
rather than reusing ordinary write-time trust clamping.

The old version retains its references after replacement or invalidation. Physical
delete/prune cascades reference removal with the owning observation. Evidence
preserves only locator/revision/selector/fingerprint identity and a latest explicit
caller check; it cannot reconstruct unavailable source bodies or establish that a
claim is true. Reference count is not independent proof or a confidence score.

Retain the upgraded database and a new-version JSON export before downgrading. Do
not run a destructive down-migration. An older binary may omit new metadata from
its reads/exports; compatibility with old reads is not a metadata-preserving export
round trip. Do not route the only backup through an older binary. Vault exports
and skill packs are intentionally narrower/lossy for topic/evidence/version-link
fidelity; use full-store snapshots when those fields matter. No missing historical
links or source contents are inferred from prose during restore.
