# Scoped observation reading API

These additive routes share the domain operations behind `mnemos_get_many` and
`mnemos_neighborhood`. They require non-empty `project`; omitted `agent_id`
resolves to `default`, matching save. This scope is additive and does not redefine
legacy single-ID get or replay as access-controlled operations.

| Method and route | Input | Result |
|---|---|---|
| `POST /v1/observations/batch` | `ids` (1-32), `project`, visibility and byte options | Ordered, deduplicated `items` with per-ID statuses |
| `POST /v1/observations/neighborhood` | `id`, `project`, optional `before`/`after` (0-10, default 3), same options | Ordered summaries, window and limit metadata |

Both accept `agent_id`, `include_raw`, `include_stale`, `as_of` (RFC3339), and
`max_bytes` (default 32768, range 1-262144). Default visibility is valid-now and
non-raw. History/raw options never relax project/agent scope. `as_of` selects
fact/system validity using current metadata, not a complete historical database
snapshot; `include_stale` explicitly includes all temporal states.

```json
{"project":"mnemos","ids":["01A","01B","01A"],"max_bytes":32768}
```

```json
{"project":"mnemos","id":"01A","before":0,"after":3}
```

Each unique result has `id`, `status` (`ok`, `unavailable`, `omitted_for_size`).
Unavailable deliberately covers missing and filtered IDs without identifying why.
Only `ok` entries contain an `observation` (batch) or `summary` (neighbourhood).
New envelope fields use snake_case; full observations retain existing PascalCase field names, with additive
`topic_key` and `evidence` in snake_case. The Go client handles this distinction.

The shared limit counts compact JSON-encoded selected observation/summary
objects, including all their text and metadata. Detail vectors/model names are
excluded. Bounded status-envelope overhead and transport whitespace are outside
the budget. Details are complete or omitted, never silently truncated. Responses
report `bytes_returned`, `max_bytes`, and `content_limited`. To inspect an omitted
large detail, retry that ID with a larger permitted budget. Summaries cap title
at 256 and snippet at 512 UTF-8 bytes and expose truncation flags. Window counts
apply after visibility filtering; hidden anchors expose no sibling counts.

Malformed/invalid inputs return HTTP 400; operational storage/bookkeeping errors
return HTTP 500; unavailable IDs are successful HTTP 200 per-item results.
Existing API-key authentication applies to these routes.

```go
c := client.New("http://localhost:8080", client.WithAPIKey(key))
details, err := c.GetMany(ctx, client.GetManyInput{
    Project: "mnemos", IDs: []string{"01A", "01B", "01A"}, MaxBytes: 32768,
})
// Check err, then details.Items[i].Status before reading Observation.
_ = details
_ = err
zero, three := 0, 3
window, err := c.Neighborhood(ctx, client.NeighborhoodInput{
    Project: "mnemos", ID: "01A", Before: &zero, After: &three,
})
_ = window
_ = err
```

For explicit inspection, set `IncludeRaw: true`, `IncludeStale: true` or a nonzero
`AsOf` on either client input. Zero pointers preserve a zero-width side; nil
pointers use defaults. Methods accept `context.Context` first.

Only unique returned detail observations increment existing access aggregates.
Bookkeeping failures fail the request atomically. These are service-read measures,
not confirmed network receipt or successful-use evidence. Summary browsing changes
no access counts. No database migration, read-event table, stored cursor, copied
result memory or injection event is added. Requests, snippets and size calculations
are transient; referenced paths/URLs are never automatically dereferenced.


## Topics, explicit replacement, and evidence

`POST /v1/observations` accepts optional `TopicKey`, `ReplacesID`,
`UnpinReplacement`, and `Evidence` alongside the existing fields. Full observations retain existing PascalCase fields and add `topic_key` and
`evidence` in snake_case; each evidence entry also uses snake_case. Search
accepts exact `TopicKey` as a filter without changing score. Details, search results
and neighbourhood summaries expose version metadata where present.

```json
{"Project":"mnemos","Title":"transaction rule","Content":"multiple writes use a transaction","Type":"convention","TopicKey":"database/transactions","Evidence":[{"locator":"docs/decision.md","relation":"supports","revision":"abc","selector":"transaction-section","fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}
```

The same topic appends; only `ReplacesID` replaces a live same-agent/project target.
The replacement creates a version, supersession link, and old-version invalidation
atomically. Omitted topic inherits; changing the target topic is rejected. A pin
inherits unless `UnpinReplacement` explicitly acknowledges unpinning; pinned expiry
stays cleared. Raw cannot replace trusted content. A matching request retry is
idempotent; a changed request or stale target yields HTTP 409 with
`{"code":"conflict","error":"..."}`. Existing-ID metadata restore uses the same
conflict classification. Other bad metadata requests are HTTP 400.

Evidence is optional and independent of topics. Requests may submit at most 16 references before deduplication; repeated identities
are stored once. Each reference contains: bounded locator (2048 bytes), revision (256), selector (512), optional
64-hex SHA-256 fingerprint, relation `supports`/`refutes`, and latest marker. There
is no source-body/quote field. New saves derive `key` and start `unverified`; repeated
identities are stored once and do not count as independent proof. Locators are
never fetched. Old evidence stays on its version; replacements do not inherit it.

`POST /v1/observations/{id}/evidence/check` accepts:

```json
{"evidence_key":"<stored key>","status":"changed"}
```

The server stores its clock's `checked_at` and the latest caller-reported
`unverified`, `matched`, `changed`, or `unavailable` status. `matched` requires a
recorded fingerprint baseline. This is not independent verification, successful
use, or a confidence score; it never changes observation trust/rank/validity/pins.
No check history or source content is saved. Missing observation/reference is
unavailable as an error; typed conflicts use 409; invalid inputs use 400;
operational failures use 500.

```go
saved, err := c.Save(ctx, client.SaveInput{
    Project: "mnemos", Title: "transaction rule", Content: "writes use transactions",
    Type: "convention", TopicKey: "database/transactions",
    Evidence: []client.EvidenceReference{{Locator: "docs/decision.md", Relation: "supports"}},
})
if err != nil { return err }
checked, err := c.CheckEvidence(ctx, client.EvidenceCheckInput{
    ObservationID: saved.Observation.ID,
    EvidenceKey: saved.Observation.Evidence[0].Key, Status: "unavailable",
})
_ = checked
if client.IsConflict(err) { /* inspect the current version before retrying */ }
```

See [BACKUP.md](BACKUP.md) for the metadata-preserving snapshot path. These fields
add optional topic metadata, one owner-bound evidence table, and an optional original
`replacement_hash` on existing supersession links for original-request retry identity; they do not
introduce a topic hierarchy, revision ledger, check-history or source-document store.
