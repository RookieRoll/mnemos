# session-continuity Specification

## Purpose

Defines bounded, explicitly shared progress checkpoints that let independent local agent sessions recover task context without claiming work, widening existing memory visibility, or replaying actions.

## Requirements

### Requirement: Continuity uses a verified local workspace

Continuity SHALL resolve an existing canonical worktree root, or an explicitly supplied non-Git root, independently of the legacy project label. The same worktree's subdirectories SHALL share an identity; separate clones and worktrees SHALL remain distinct. Unresolved roots and unbound legacy sessions SHALL NOT enter automatic recovery. Existing project values SHALL remain unchanged.

#### Scenario: Two repositories have the same directory name
- **WHEN** two distinct roots both have the legacy project label `api`
- **THEN** their continuity candidates remain isolated

#### Scenario: A session starts below the repository root
- **WHEN** the caller starts from a subdirectory of an already resolved worktree
- **THEN** the continuity workspace identity matches that worktree root

#### Scenario: A linked worktree or moved repository is encountered
- **WHEN** its canonical root differs from the recorded root
- **THEN** no automatic alias or historical session binding is created

#### Scenario: Root resolution fails
- **WHEN** the root is missing, ambiguous, or cannot be canonicalized
- **THEN** automatic recovery is unavailable without falling back to the directory basename

#### Scenario: Repository selection is overridden by the environment
- **WHEN** a Git query would resolve a root unrelated to the caller's directory
- **THEN** no continuity binding is created from that result

### Requirement: Independent starts have explicit retry identity

A continuity start SHALL resolve an owner, workspace and harness, and require a stable start key for that invocation. Identical concurrent retries SHALL establish one open session. A new invocation key SHALL create an independent session. Reusing a key with conflicting input or a closed target SHALL conflict. Host IDs and memory owner IDs SHALL remain distinct, and an omitted owner SHALL resolve to `default` rather than all owners.

#### Scenario: Duplicate startup calls race
- **WHEN** two callers submit identical starts with the same key and scope
- **THEN** both receive the same single committed session identity

#### Scenario: Another agent takes over
- **WHEN** a new invocation names an earlier session as its recovery source
- **THEN** its new session identity differs from the source and the source remains unchanged

#### Scenario: Two harnesses use the same native conversation ID
- **WHEN** their harness identities differ
- **THEN** the native ID does not merge their sessions

#### Scenario: A completed start is retried after its session closed
- **WHEN** the original start key is reused
- **THEN** the response is a conflict rather than a new session or a reopened old one

### Requirement: Legacy inference excludes continuity-bound sessions

In a continuity-capable binary, legacy heuristic adoption, bare host-ID resolution, newest-session fallback, twin closure and implicit stale closure SHALL exclude continuity-bound sessions. Valid exact-ID operations SHALL retain their target semantics, and legacy-only data SHALL retain existing behavior. This exclusion SHALL NOT remove bound sessions from ordinary administrative listings or backups.

#### Scenario: Legacy and continuity sessions start close together
- **WHEN** a legacy shutdown uses newest-session or twin inference
- **THEN** it cannot close the continuity-bound session even inside the legacy twin window

#### Scenario: A legacy resolver sees a matching bare host ID
- **WHEN** that ID belongs only to a continuity-bound session
- **THEN** the legacy inference does not adopt or mutate that session

#### Scenario: An implicit legacy stale sweep runs
- **WHEN** an old continuity-bound session is present
- **THEN** the sweep does not infer its liveness or close it through the legacy path

### Requirement: Checkpoint mutations target an exact owned session

Checkpoint writes and clears SHALL require an exact Mnemos session ID matching the resolved owner and workspace. Missing, unbound and out-of-scope targets SHALL return the same unavailable outcome without private metadata. The operation SHALL NOT fall back to the newest session or a time-based session group.

#### Scenario: A delayed write targets an old session
- **WHEN** another session has since opened in the same workspace
- **THEN** the delayed operation never modifies the newer session

#### Scenario: A caller names another owner's session
- **WHEN** that session contains a workspace-shared checkpoint
- **THEN** sharing permits reading only and the mutation returns unavailable

### Requirement: Checkpoints contain bounded explicit progress

Each session SHALL hold at most one current checkpoint body. Inputs SHALL contain an explicit task, reported state, summary and optional next steps, blockers and references. Field and complete-object limits SHALL be enforced before writing. Unknown fields, invalid UTF-8 and oversized inputs SHALL fail without truncation or partial writes. Checkpoints SHALL NOT be inferred from raw prompts, transcripts or tool output.

#### Scenario: A valid checkpoint is saved
- **WHEN** task is at most 256 UTF-8 bytes, summary at most 4096 bytes, next steps and blockers each have at most 8 entries of 512 bytes, and references have at most 16 bounded entries
- **THEN** the complete canonical payload including workspace metadata must also fit 16 KiB before it can be accepted

#### Scenario: The request or a nested field exceeds its limit
- **WHEN** raw input exceeds 32 KiB, a reference identifier exceeds 256 bytes, an unknown field appears, or the payload exceeds 16 KiB
- **THEN** validation fails and the prior checkpoint is unchanged

#### Scenario: High-risk content is submitted
- **WHEN** the checkpoint text trips the write-boundary scanner
- **THEN** the write is rejected without logging or storing the raw body

#### Scenario: A tool finishes editing a file
- **WHEN** no agent-authored checkpoint is explicitly submitted
- **THEN** no progress summary or successful-test claim is invented from that event

### Requirement: Workspace sharing is explicit and narrow

Checkpoint visibility SHALL default to owner. Explicit workspace visibility SHALL expose only the published checkpoint and bounded source metadata to readers in that workspace. Reads SHALL recheck current visibility. Publishing SHALL NOT expose the source session's goal, summary, reflection or other private records. Owner identity SHALL be independent of the harness product name.

#### Scenario: Two owners share progress
- **WHEN** owner A publishes a workspace-visible checkpoint and owner B reads from the same workspace
- **THEN** B can read that checkpoint while A's unrelated private records remain unavailable

#### Scenario: Sharing is revoked
- **WHEN** a newer checkpoint changes visibility back to owner
- **THEN** subsequent reads by other owners return unavailable without changing already emitted contexts

#### Scenario: Private candidates exist
- **WHEN** a different owner lists workspace candidates
- **THEN** private titles, bodies and candidate counts are not disclosed

### Requirement: Recovery reads use one consistent source snapshot

Visibility, candidate counts, source revision and returned body SHALL come from one database read snapshot. Later reads SHALL recheck current state. Workspace comparison SHALL use branch and HEAD sampled at the bound root for the current recovery request rather than cached startup metadata; this sample SHALL NOT be presented as an atomic proof of unchanged files.

#### Scenario: Clear races a recovery read
- **WHEN** a source is cleared while a read is in progress
- **THEN** the response represents one consistent pre-clear or post-clear state, without mixing old body and new metadata

#### Scenario: The current session changes branches before recovery
- **WHEN** recovery is requested after startup on another branch
- **THEN** the comparison uses the branch at recovery time

### Requirement: References do not grant source access

Checkpoint references SHALL remain bounded locators. Reading continuity SHALL NOT dereference files or URLs, call unscoped historical reads, or borrow the source owner's memory scope. Any later observation-detail request SHALL independently enforce the receiving caller's existing visibility rules.

#### Scenario: A shared checkpoint names a private observation
- **WHEN** another owner reads the checkpoint and requests that observation through scoped reading
- **THEN** the reference does not make the observation body available

#### Scenario: A checkpoint contains a file reference
- **WHEN** the checkpoint is rendered
- **THEN** no file is opened or executed as part of recovery

### Requirement: Checkpoint replacement uses optimistic concurrency

Mutations SHALL require an expected revision and request ID. A new mutation SHALL replace the complete body only at the expected current revision and increment that revision. A matching retry of the current request SHALL return its original result without another mutation. The same current request ID with different input, or an obsolete expected revision, SHALL conflict. No semantic merge SHALL occur.

#### Scenario: Two writers update the same revision
- **WHEN** independent database clients concurrently replace revision N with different requests
- **THEN** exactly one new revision commits and the other request conflicts

#### Scenario: A retry follows a newer write
- **WHEN** request A committed, request B later committed, and A is retried with its original expected revision
- **THEN** A conflicts without reverting B

#### Scenario: A current request ID is reused for different content
- **WHEN** its normalized operation, scope, expected revision, visibility or body differs
- **THEN** the operation conflicts rather than acknowledging or replacing the original request

### Requirement: Success receipts describe committed state

A mutation SHALL emit an applied or replayed success receipt only after its checkpoint state is committed. The receipt SHALL identify protocol version, session, revision and service-recorded time. A timeout or lost response SHALL be treated as an unknown outcome, not proof of success or failure. An exact retry of the current committed request SHALL preserve its revision, time and recorded workspace metadata.

#### Scenario: Commit fails
- **WHEN** the transaction cannot commit
- **THEN** no success receipt is returned and no partial new checkpoint is visible

#### Scenario: The response is lost after commit
- **WHEN** the caller repeats the exact current request
- **THEN** it receives replayed with the original revision and timestamp

#### Scenario: The workspace branch changes before retry
- **WHEN** the exact current request is retried after its original commit
- **THEN** the recorded workspace metadata is not resampled or replaced

### Requirement: Clearing preserves revision continuity

An owner clear SHALL use the same revision and receipt rules, remove the checkpoint body, reset visibility to owner and increment the revision. Later writes SHALL continue from that revision. Reads by the owner SHALL distinguish a cleared checkpoint from a never-written one. Deleting the owning session SHALL remove its continuity state.

#### Scenario: An old write arrives after clear
- **WHEN** a saved checkpoint is cleared and an earlier write is retried
- **THEN** the old write conflicts and cannot resurrect the cleared body

#### Scenario: A cleared session has a final summary
- **WHEN** candidates are listed after clear
- **THEN** that session does not reappear through a summary fallback

### Requirement: Session closure and checkpoint writes remain distinct

Checkpoint writes SHALL NOT alter final session summary, reflection or lifecycle. New checkpoint bodies SHALL be rejected after session closure, while an exact retry of the current committed request and an owner clear SHALL remain possible. A close racing a checkpoint write SHALL have one transaction order, without reopening the session or claiming both operations succeeded when only one did.

#### Scenario: Closure wins the race
- **WHEN** close commits before a new checkpoint write validates the session state
- **THEN** the new write fails and the final summary is unchanged

#### Scenario: Checkpoint commit wins the race
- **WHEN** a checkpoint commits before close
- **THEN** it remains readable after closure and its exact current retry returns the original receipt

### Requirement: Explicit source selection never substitutes another session

A recovery read SHALL validate the current bound session and any explicit source against workspace and visibility. An unavailable explicit source SHALL return unavailable without selecting an alternative. Reads without an explicit source SHALL exclude the current session and use exact optional task filtering. Existing summaries SHALL be candidates only for the same owner, with a verified binding and no checkpoint or cleared marker.

#### Scenario: A requested source belongs to another worktree
- **WHEN** a visible candidate also exists in the current worktree
- **THEN** the explicit request still returns unavailable without falling back

#### Scenario: An ended session has only an existing summary
- **WHEN** its owner lists candidates in its verified workspace
- **THEN** a bounded summary candidate is returned with source kind session_summary and no inferred next steps

#### Scenario: A checkpoint already represents the session
- **WHEN** the same session also has a final summary
- **THEN** the candidate list does not duplicate it as a summary candidate

### Requirement: Automatic selection preserves ambiguity

Automatic source selection SHALL require explicit resume intent and exactly one matching visible candidate before count or byte limits. That candidate SHALL be a fresh active or blocked checkpoint with known matching branch and HEAD. Otherwise the response SHALL provide none or candidates, with ambiguity and has-more metadata as applicable. Ordering SHALL be stable by recorded time descending then session ID, never by inferred semantic equivalence.

#### Scenario: Two tasks are in progress
- **WHEN** both candidates match the requested filters
- **THEN** neither is automatically selected merely because it is newer

#### Scenario: Limits hide the second candidate
- **WHEN** two candidates are eligible but only one fits the requested limit or response budget
- **THEN** the response remains ambiguous and does not report a unique selected source

#### Scenario: Startup does not express a continuation request
- **WHEN** one fresh candidate exists but resume intent is absent
- **THEN** only a candidate offer is returned

#### Scenario: A second visible candidate is stale or completed
- **WHEN** the task-filtered visible set contains one fresh checkpoint and another stale, done, summary or different-branch source
- **THEN** the result remains ambiguous until the caller narrows the task or chooses an explicit source

### Requirement: Recovery exposes age and workspace uncertainty

Sources older than 24 hours SHALL be marked stale and excluded from automatic selection. Unknown or different branch/HEAD metadata, and reported done state, SHALL also prevent automatic selection. Explicit selection of a visible source SHALL retain its age, reported state, session lifecycle and workspace warnings. Every rendered recovery SHALL require verification of current files and relevant tests before relying on the progress report.

#### Scenario: Explicitly selected progress is old
- **WHEN** its last successful write was more than 24 hours ago
- **THEN** it is readable with a stale marker and is not described as current verified progress

#### Scenario: The branch name matches but the commit changed
- **WHEN** the source HEAD differs from the current HEAD
- **THEN** workspace change is reported and automatic selection is suppressed

#### Scenario: A session ended with unfinished work
- **WHEN** its checkpoint is active but its session is closed
- **THEN** recovery reports these facts separately without declaring the task completed

### Requirement: Recovery is a bounded read without execution

Recovery SHALL enforce a default candidate limit of 5, maximum 20, and a complete JSON response budget of 32 KiB by default and 64 KiB maximum. Complete objects including framing, references and warnings SHALL count toward the budget. Omission SHALL be explicit. Reading SHALL NOT claim, consume, execute, reopen or persist progress, nor create memory, access, injection or successful-use events.

#### Scenario: Several sessions read the same source
- **WHEN** two receivers request the same published checkpoint
- **THEN** both can read it without altering the source or adding ownership state

#### Scenario: Recovery text exceeds the remaining response budget
- **WHEN** its complete object cannot fit
- **THEN** the body is omitted with size metadata rather than returned partially as a complete source

#### Scenario: Next steps contain an executable command
- **WHEN** recovery returns the checkpoint
- **THEN** the command is data and no execution or tool replay occurs

### Requirement: Retention and backup limits are explicit

The first version SHALL retain the latest checkpoint until owner clear or session deletion, without automatic TTL deletion or historical checkpoint versions. A stale marker SHALL NOT imply deletion. Logical exports that omit continuity data SHALL explicitly report that omission, and legacy imports SHALL NOT bind sessions or overwrite local continuity state. Consistent same-machine SQLite backups SHALL preserve current revisions and retry identities.

#### Scenario: A checkpoint becomes stale
- **WHEN** more than 24 hours passes without an update
- **THEN** explicit authorized reading remains possible and no automatic deletion is implied

#### Scenario: A logical dump omits checkpoints
- **WHEN** continuity state exists during export
- **THEN** the export reports the omission rather than claiming complete continuity backup

#### Scenario: An old logical dump is imported
- **WHEN** a matching local session already has a newer checkpoint
- **THEN** its binding, body, revision and request identity remain unchanged

### Requirement: Candidate body materialization is bounded
Candidate enumeration SHALL determine visibility, exact task matching and ambiguity over the complete matching set in one read snapshot while materializing only a bounded display window of complete bodies. Memory used for candidate bodies SHALL scale with the requested window, not total retained history. Existing ordering, explicit-source and response-budget semantics SHALL remain intact.

#### Scenario: Large history is enumerated
- **WHEN** a workspace has many retained checkpoints and a candidate limit of five
- **THEN** at most five complete source bodies are materialized for the window

#### Scenario: A second candidate falls outside the window
- **WHEN** the requested limit is one but another matching stale or completed source exists
- **THEN** the result remains ambiguous and does not automatically select the displayed source

#### Scenario: A matching task is beyond unrelated history
- **WHEN** an exact task filter matches an older candidate after many unrelated candidates
- **THEN** that matching candidate remains eligible in the snapshot

#### Scenario: Recorded times have equivalent textual formats
- **WHEN** sources use different valid timestamp representations
- **THEN** ordering follows their actual time values and the existing session-ID tie break
