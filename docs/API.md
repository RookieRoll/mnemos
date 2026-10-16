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
New envelope fields use snake_case; full observations retain their existing
PascalCase field names. The Go client handles this distinction.

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
