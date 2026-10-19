# Context composition protocol

Prewarm has two distinct recovery purposes. Ordinary compaction recovery restores the **current** session's state. Opt-in continuity startup supplies reported progress from a **different** explicitly selected source to a newly established independent session. A source session ID never replaces the receiver's session ID, and reading a source neither claims work nor executes its suggested steps.

## Composer input and identity

`prewarm.Service.Build(ctx, prewarm.Request)` composes context without creating or adopting a session. Continuity callers first commit or confirm an exact session through the continuity service, then obtain a scoped `continuity.ContextResult`, and supply it through `Request.Continuity` with the returned `SessionID`, resolved `AgentID` and legacy `Project`. The composer checks the exact current session's owner/project; it does not look up or reselect a source. Workspace and sharing validation belong to the continuity service before composition.

A non-nil continuity result, including an empty `none` result, enables the continuity startup pipeline. A missing owner/session or a non-session-start mode is rejected. A nil result retains the legacy pipeline and output. This opt-in never widens ordinary observation or skill scope. It also does not retroactively resolve collisions in legacy project names.

Continuity startup order is pinned core, continuity report or candidate offer, conventions, pending reviews, matching skills, corrections, and hot files. The old recent-session section is omitted entirely, so an unbound session or another same-named checkout cannot bypass scoped candidate selection through a legacy goal/summary. Existing sections retain their relative order. Compaction-recovery and premortem pipelines remain unchanged when continuity is absent.

## Selection, budget and safety

A selected source includes its kind (`checkpoint` or `session_summary`), exact session ID, checkpoint revision when present, recording time, owner/harness, reported task state, session closed status and applicable `stale`, `workspace_changed` or `workspace_unknown` cautions. The report always instructs the receiving agent to verify current files, diff and relevant tests. A changed HEAD is a warning; matching HEAD is not proof that dirty files are unchanged.

The full checkpoint representation includes task, entire summary, reported next steps and blockers, and locators marked as not opened. The compact representation retains the entire task and summary plus every required provenance field and caution; it explicitly reports that next steps, blockers and references were omitted and can be read from the exact source. It never cuts a summary assertion at a byte or sentence prefix. A final-summary source has no invented next steps. Cleared sources produce a body-free notice, never a delivery reference.

Candidates are offers, not selected task bodies. Offers contain source metadata and no candidate summary or task prose. An ambiguous offer stays ambiguous even when context space is short: either its complete metadata representation or a complete compact notice is selected, otherwise the offer is omitted. Candidate hints do not accumulate in pi's history; the extension delivers startup context through its existing first-turn `before_agent_start` channel, or the agent explicitly requests a later read. The composer itself stores no delivered flag or subscription.

Selected progress uses the same item-complete budget as ordinary context: `ceil(UTF-8 bytes / 4)`, including section headings, separators, provenance, verification text, compact/omission notices and safety wrappers. Full material is scanned before choosing representations; sanitized text and the selected joined form are scanned too. A compact report cannot discard a safety warning triggered by material omitted from that representation. If required annotations do not fit, the whole source is omitted. Unicode remains valid.

## References and evidence limits

`Block.Surfaced` continues to contain selected observation/skill references only, with the existing best-effort injection logging. `Block.ContinuityRefs` is separate and contains only source bodies actually rendered in the final block. Offers, cleared markers and size-omitted sources produce no continuity reference. A locator inside a checkpoint does not itself become a surfaced observation or authorize a private source read.

For example, a rendered report containing `source_kind=checkpoint session_id=source-session revision=7 recorded_at=2026-10-19T08:00:00Z` and its full or explicit compact summary has the following continuity-reference projection:

```json
[
  {"session_id":"source-session","revision":7,"source_kind":"checkpoint"}
]
```

If the same source cannot fit with all cautions, that array is empty. If only candidate metadata appears, it is also empty. These references describe emitted context; they prove neither host receipt nor model attention or successful task execution. There are no checkpoint injection events, consumer claims, access increments, successful-use events, durable render caches or saved candidate lists. Multiple receivers may independently read the same still-visible source.

Context composition failure after continuity open does not revoke the committed session or justify opening another one. The startup flow preserves the exact committed identity and reports context unavailable; a retry uses the original start key. Old hook empty envelopes and successful exits are not continuity commit receipts.
