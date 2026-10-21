# memory-reading Specification

## Purpose

Defines progressive observation reading through bounded batch details and chronological neighbourhoods with explicit scope, trust, and historical visibility.

## Requirements

### Requirement: Explicit scoped visibility

New batch and neighbourhood reads SHALL require a project and resolved agent scope. Defaults SHALL return live, non-raw observations only. Historical or raw visibility SHALL require explicit options and SHALL NOT relax the project or agent scope.

#### Scenario: A known ID belongs to another project
- **WHEN** a batch requests that ID with the current project's scope
- **THEN** it is unavailable without exposing its title, content, or owning project

#### Scenario: Historical and raw inspection is explicit
- **WHEN** historical or raw options are supplied
- **THEN** eligible records can be returned only within the requested project and agent scope, marked with their original validity and trust metadata

### Requirement: Scoped reads use fact-time validity

Scoped reads SHALL enforce `valid_from <= effective AsOf` unless `IncludeStale` explicitly bypasses temporal validity predicates. `AsOf` SHALL NOT imply a `created_at` system-knowledge snapshot.

#### Scenario: A fact starts in the future
- **WHEN** a scoped read's effective AsOf precedes valid_from and stale inspection is disabled
- **THEN** that observation is unavailable even if already stored

#### Scenario: Fact validity predates its recording
- **WHEN** an eligible observation was created after AsOf but was fact-valid at AsOf
- **THEN** created_at alone does not make it unavailable

### Requirement: Legacy read compatibility

Existing single-ID get and replay behaviour SHALL remain unchanged by the new scoped reading operations.

#### Scenario: Replay includes an invalidated observation
- **WHEN** existing replay reads a session containing an invalidated observation
- **THEN** it retains its existing historical visibility regardless of the new scoped-read defaults

### Requirement: Bounded deterministic batch details

Batch reads SHALL deduplicate IDs, preserve first-requested order, and provide one result status per unique ID. Missing and filtered IDs SHALL share an unavailable status. Requests exceeding declared count or content limits SHALL fail validation or return explicit limit metadata, never silently omit results.

#### Scenario: Duplicate and unavailable IDs
- **WHEN** input is A, A, B, C and B is filtered
- **THEN** output contains A, unavailable B, and C in that order and A is not counted twice

### Requirement: Complete-object response budgets

The byte budget SHALL count complete JSON-encoded returned detail/summary objects, including metadata. Embeddings SHALL be excluded from detail payloads.

#### Scenario: A body exceeds the response budget
- **WHEN** an eligible observation cannot fit in the remaining content budget
- **THEN** its result identifies it as omitted for size or explicitly truncated without pretending the full body was returned

#### Scenario: Metadata exceeds the byte budget
- **WHEN** content fits but a title, rationale, structured field, or tag set makes the complete detail object exceed the remaining byte budget
- **THEN** the item is omitted for size without disclosing a partial detail

### Requirement: Bounded status envelope

The status envelope SHALL enforce 256-byte UTF-8 IDs, a 4096-byte project scope, and a 256-byte agent scope. Oversized stored neighbour identifiers SHALL fail the request.

#### Scenario: A stored neighbour identifier exceeds the envelope limit
- **WHEN** a visible neighbour has an identifier exceeding 256 bytes
- **THEN** the request fails instead of returning that identifier outside the object budget

### Requirement: Explicit safe summary shortening

Summary title/snippet shortening SHALL be UTF-8 safe and explicitly flagged.

#### Scenario: A multilingual summary exceeds its display bound
- **WHEN** a title or snippet must be shortened at a multibyte character boundary
- **THEN** the shortened field remains valid UTF-8 and its truncation flag is set

### Requirement: Chronological neighbourhoods honour visibility

A neighbourhood read SHALL validate its anchor before returning visible observations from the same session and scope. Ordering SHALL be creation time then ID. Before/after limits SHALL count eligible observations. A filtered anchor SHALL return no neighbourhood. A visible anchor without a session SHALL return the anchor and an explicit no-session marker.

#### Scenario: Hidden records lie between visible neighbours
- **WHEN** a timeline has raw or invalidated entries between eligible observations
- **THEN** default before/after counts apply only to eligible observations and hidden content is not disclosed

#### Scenario: Two neighbours have identical timestamps
- **WHEN** eligible neighbours share a creation time
- **THEN** identifier ordering gives a stable result

### Requirement: Transport parity

MCP, HTTP, and the Go client SHALL expose equivalent scope, limit, visibility, result-status, and truncation semantics for the new reading operations. Transport-specific naming SHALL NOT change which observations are visible.

#### Scenario: Equivalent calls across transports
- **WHEN** equivalent batch or neighbourhood inputs are submitted through MCP and HTTP
- **THEN** selected IDs, statuses, and visibility metadata agree

### Requirement: Reads are not outcome evidence

Batch details SHALL increment existing access counts once per unique observation whose content is returned. Filtered, unavailable, and size-omitted items SHALL NOT increment counts. Neighbourhood listing SHALL NOT increment detail-access counts. Neither operation SHALL create injection or successful-use events.

#### Scenario: The same ID is requested twice
- **WHEN** its details are returned once in a batch
- **THEN** its existing access count increases once

#### Scenario: A timeline is browsed repeatedly
- **WHEN** a client repeats a neighbourhood listing
- **THEN** no detail-access counts or surfacing events are added

### Requirement: Reading state is transient

Batch selections, timeline results, continuation hints, snippets, and response-size calculations SHALL NOT be stored as new memories or database entities. Operations SHALL use existing observation storage and SHALL NOT automatically dereference source paths or URLs.

#### Scenario: A result contains an evidence URL
- **WHEN** observation metadata contains that locator
- **THEN** reading returns metadata without fetching the remote content or persisting a copy

### Requirement: Knowledge visibility remains scoped by agent and project labels
Knowledge reads and writes SHALL continue to use the existing `agent_id` and `project` labels as their scope. Workspace roots and progress-session identities SHALL NOT silently rewrite, hash, alias, or migrate those labels. Callers MAY provide distinct project labels to isolate new knowledge; existing records SHALL remain under their stored labels.

#### Scenario: Two workspace roots use the same project label
- **WHEN** two workspaces submit the same agent and project labels
- **THEN** knowledge remains in the shared legacy label scope while continuity progress remains isolated by its verified workspace identity

#### Scenario: A caller chooses distinct labels
- **WHEN** callers explicitly select different project labels for new knowledge
- **THEN** knowledge reads remain isolated by those labels and no old records are moved or aliased

#### Scenario: A workspace root changes
- **WHEN** an application resolves a different progress workspace for a session while the knowledge project label is unchanged
- **THEN** existing knowledge lookup continues under the unchanged agent/project scope and is not implicitly migrated
