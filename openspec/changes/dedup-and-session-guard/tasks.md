## 1. Storage and schema

- [ ] 1.1 Add the additive migration `0012_observation_near_dups.sql`: `content_hash_normalized` column, `(agent_id, project, content_hash_normalized)` index, `(session_id, title, created_at)` index, `observation_near_dups` table (with `incoming_content`) + `survivor_id` index.
- [ ] 1.2 Add store methods: write the normalized hash on insert; live-row lookup by `(agent, project, normalized hash, since)`; live-row lookup by `(session_id, title, since)`; insert provenance row with the verbatim merged-away content. Keep them on the existing reader/writer store split.
- [ ] 1.3 Storage tests: migration applies to a database created by the previous schema version; old rows keep `NULL`; `incoming_content` round-trips byte-for-byte (including whitespace and punctuation that normalization drops).

## 2. Memory save path

- [ ] 2.1 Add `hashNormalized` next to `hashContent` (letters+digits only, lowercased) with a table-driven test: case, whitespace, punctuation, CJK text, empty-ish content.
- [ ] 2.2 [P] Wire the fallback order from design D2 into the save path, with an injected clock so the window is testable. [P with 2.1]
- [ ] 2.3 Merge: access bump on the survivor + provenance row carrying the whole incoming content + return the survivor id; new-vs-merged outcome carried on the save result.
- [ ] 2.4 Out-of-window candidate produces a hint and still inserts.
- [ ] 2.5 Table-driven save tests: punctuation-only difference merges; case-only merges; whitespace-only merges; a single changed letter does not merge; different `(agent, project)` does not merge; outside window does not merge and hints; same session + title inside window merges and leaves one live row.
- [ ] 2.6 Regression: `hashContent` output unchanged (evolution tests); Restore leaves `content_hash` untouched; near-dup provenance does not alter ranking scores and provenance content is never returned by search or prewarm.

## 5. Session-end ownership guard

- [ ] 5.1 [P] Resolve the invocation's bound session id at the same seam the hook path uses; add it to the end-request context.
- [ ] 5.2 [P] Add `ErrSessionNotOwned` in `internal/session` and enforce the guard: bound id, or receipt with matching workspace binding; workspace-only, agent-agnostic.
- [ ] 5.3 Map the error to `session_not_owned` in the MCP and HTTP transports with guidance text naming `mnemos sessions close <id>`.
- [ ] 5.4 Table-driven tests: own session accepted; foreign UUIDv7 rejected; foreign id with a matching-workspace receipt accepted; receipt with a different workspace rejected; no resolvable binding + no receipt rejected; unknown id rejected with the same code.
- [ ] 5.5 Regression: `EndForHost` / `CloseForHost` unchanged for hooks; CLI `sessions close` still closes an arbitrary id.

## 6. Docs and skill text

- [ ] 6.1 [P] Update `docs/MCP_TOOLS.md` for the session-end contract and the dedup behaviour, stating that a merge retains the merged-away content in the provenance record.
- [ ] 6.2 [P] Update the mnemos skill text in `.agents/skills/mnemos/SKILL.md`, `.claude/skills/mnemos/SKILL.md`, `pi/skills/mnemos/SKILL.md` from "only close the current session" to the enforced rule plus the CLI escape hatch — keep the shared body identical across the three mirrors.
- [ ] 6.3 [P] Note the breaking change for existing automation in the change notes.

## 7. Verification

- [ ] 7.1 `go build ./... && go vet ./...`, targeted `go test ./internal/memory/ ./internal/storage/ ./internal/session/ ./internal/config/ ./internal/mcp/ -count=1` (no `-race` locally).
- [ ] 7.2 `openspec validate --change dedup-and-session-guard`.
