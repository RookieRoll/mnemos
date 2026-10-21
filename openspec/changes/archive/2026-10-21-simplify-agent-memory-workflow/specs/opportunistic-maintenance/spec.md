# Spec Delta

## Purpose

Runs deterministic maintenance at eligible task-session starts without a resident scheduler or duplicate concurrent passes.

## ADDED Requirements

### Requirement: Maintenance budgets are configurable and bounded
TOML `[maintenance]` SHALL provide `enabled`, `interval`, `max_pass_duration`, `lease_duration`, and `renew_interval`. Defaults SHALL be `true`, `'24h'`, `'5s'`, `'15s'`, and `'2s'` respectively. Durations SHALL be positive; max pass SHALL be shorter than the lease, renewal SHALL be shorter than the lease, max pass SHALL be at most `5m`, and lease SHALL be at most `10m`. Invalid values SHALL fail config validation before startup or automatic work.

#### Scenario: Maintenance defaults are applied
- **WHEN** the maintenance section is omitted from TOML
- **THEN** settings default to enabled, a 24-hour interval, a 5-second maximum pass, a 15-second lease, and a 2-second renewal interval

#### Scenario: Custom maintenance budgets are accepted
- **WHEN** TOML sets `interval = '30m'`, `max_pass_duration = '3s'`, `lease_duration = '20s'`, and `renew_interval = '4s'`
- **THEN** config validation accepts the values and due checks and pass entry points receive those durations

#### Scenario: Invalid maintenance durations fail before startup
- **WHEN** a duration is zero or negative, `max_pass_duration >= lease_duration`, `renew_interval >= lease_duration`, `max_pass_duration > '5m'`, or `lease_duration > '10m'`
- **THEN** configuration validation rejects it before server startup or automatic maintenance

### Requirement: Task-session starts consult the due coordinator
Each eligible task-session start SHALL consult the shared coordinator. It SHALL run deterministic dream/rumination work only when due, honoring `integration.disabled` and `maintenance.enabled`. Due checks SHALL resume an expired unfinished cycle before applying the interval since `last_success`; without an unfinished cycle, they SHALL use that interval or run when no success exists. Automatic work SHALL suppress journal writes and make no model/provider calls.

#### Scenario: Maintenance is due on a task-session start
- **WHEN** the interval has elapsed or no successful run is recorded
- **THEN** the session start makes one best-effort attempt through the shared due coordinator

#### Scenario: Each supported session-start entry uses the coordinator
- **WHEN** MCP `session_start`, host-hook prewarm `session_start`, CLI continuity open, or explicit HTTP session-start begins a task session
- **THEN** it consults the same due coordinator

#### Scenario: An expired unfinished cycle is recovered before the interval check
- **WHEN** a manual pass started a cycle before the next interval, failed, and its lease later expires while `last_success` is still recent
- **THEN** the next eligible task-session start resumes that same cycle instead of waiting for the interval or creating a new decay cycle

#### Scenario: Maintenance is not yet due
- **WHEN** the configured interval has not elapsed since the last successful run
- **THEN** the session start does not run the pass

#### Scenario: Maintenance is disabled
- **WHEN** TOML disables `[maintenance].enabled` or `[integration].disabled` disables integration
- **THEN** task-session starts do not run the pass

#### Scenario: An ordinary operation does not start a session
- **WHEN** a caller performs doctor, export, search, context, recovery, or checkpoint work without opening a task session
- **THEN** no automatic opportunity pass is started

#### Scenario: HTTP or CLI explicitly opens a task session
- **WHEN** a caller invokes HTTP session-start or CLI continuity open
- **THEN** the same due coordinator used by MCP and host-hook prewarm is consulted

### Requirement: Cross-process lease serializes due work
Every dream entry point SHALL use one owner-token compare-and-set lease; manual passes MAY skip only the due check. Owners SHALL renew leases, and writes SHALL be fenced by the current owner and cycle. Recovery is at-least-once, not exactly-once. Decay SHALL apply at most once per cycle by atomically recording `cycle_id` and `decay_applied` with its transaction.

#### Scenario: Two task-session processes reach the due interval together
- **WHEN** both attempt to claim the maintenance lease
- **THEN** exactly one process runs the pass and the other continues without running it

#### Scenario: A process exits while holding the lease
- **WHEN** the lease expires without a successful completion
- **THEN** a later due session start can acquire it and retry the pass

#### Scenario: Lease takeover resumes unfinished work
- **WHEN** a process claims an expired cycle after a prior owner exited
- **THEN** it retains that cycle ID and validates the new owner token and expiry in each mutating transaction

#### Scenario: An old worker loses its lease
- **WHEN** a lease expires or renewal fails while a worker is still active
- **THEN** the worker is canceled, cannot renew or commit last-success using a stale owner token, and a new owner can recover after the configured expiry

#### Scenario: A manual or watch pass overlaps an automatic pass
- **WHEN** a manual, watch, or opportunity pass attempts to start while another pass owns the lease
- **THEN** all entry points use the same lease and the losing pass does not execute concurrently

#### Scenario: A pass succeeds
- **WHEN** the deterministic pass completes successfully
- **THEN** its completion time becomes the last successful run and the lease is released

#### Scenario: A pass fails
- **WHEN** the pass fails or is canceled
- **THEN** last-success is not advanced, no detailed failure prose is persisted, and a later opportunity may retry after the lease expiry

#### Scenario: Maintenance reaches its configured pass budget
- **WHEN** automatic maintenance reaches the configured maximum duration (5 seconds by default)
- **THEN** it is canceled without failing session start or hiding the already committed continuity receipt

### Requirement: Opportunity maintenance persists only coordination state
The system SHALL persist only last-success, active lease, minimal unfinished-cycle recovery state, and existing rumination candidates. It SHALL NOT persist diagnostic prose, temporary prompts, assembled context, or intermediate maintenance reports. Opportunity passes SHALL NOT create dream observations; explicit dream operations retain their journal behavior.

#### Scenario: A pass builds context or diagnostics
- **WHEN** temporary context, progress text, or detailed failure diagnostics are produced
- **THEN** they remain transient and do not create durable memory or maintenance-report records

#### Scenario: A deterministic pass detects a review candidate
- **WHEN** the existing rumination monitors detect a candidate
- **THEN** the candidate is stored through the existing candidate lifecycle for a later explicit host-agent review

#### Scenario: A deterministic pass has no journal content
- **WHEN** an automatic pass completes with no journal-worthy changes
- **THEN** it records coordination state only and creates no ordinary dream observation

#### Scenario: Only recovery state is stored
- **WHEN** a pass starts, renews, resumes, or completes
- **THEN** storage contains only last-success, lease owner/expiry, active cycle ID/decay marker, and existing candidate lifecycle records

#### Scenario: A process crashes after decay commits
- **WHEN** decay and its cycle marker commit but the overall pass does not complete
- **THEN** lease takeover resumes the same cycle and skips decay already applied for that cycle

#### Scenario: A completed pass starts another cycle
- **WHEN** the current cycle completes and a later pass is due or explicitly requested
- **THEN** the coordinator creates a new cycle so decay may run once for that new cycle

### Requirement: Semantic review remains an explicit host decision
Automatic maintenance SHALL NOT make semantic resolve or dismiss decisions. It MAY retain the existing deterministic provenance-tag auto-resolution for a candidate whose target already carries `ruminated-from:<id>`. All other semantic review requires an explicit host-provided Maintenance Agent using existing evidence and validated resolve/dismiss operations; without one, the candidate SHALL remain pending.

#### Scenario: A pending candidate needs semantic judgment
- **WHEN** an opportunity pass detects a candidate requiring review
- **THEN** it leaves that candidate pending and does not infer a revision or dismissal reason

#### Scenario: Existing provenance proves a candidate was addressed
- **WHEN** the deterministic pass finds the existing `ruminated-from:<id>` provenance tag on a candidate target
- **THEN** the existing deterministic auto-resolution closes that candidate without semantic inference

#### Scenario: The host explicitly resolves or dismisses a candidate
- **WHEN** the active host agent submits a valid resolve or dismiss operation with the required evidence
- **THEN** the existing rumination validation and persistence rules determine the outcome

### Requirement: Opportunity maintenance does not promise unattended scheduling
Opportunity maintenance SHALL run only when an eligible task-session start occurs. It SHALL NOT create or start a resident daemon, operating-system task, or background scheduler.

#### Scenario: No eligible session is active
- **WHEN** the due interval passes while no eligible task-session start occurs
- **THEN** no maintenance pass is promised until a later opportunity

#### Scenario: Strict timing is requested
- **WHEN** an operator requires a wall-clock schedule
- **THEN** this capability leaves scheduling to an explicitly configured external scheduler
