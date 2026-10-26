## ADDED Requirements

### Requirement: Release archives ship the documentation

Release builds SHALL include the repository `docs/` directory in every os/arch archive
produced by `.goreleaser.yml`, so a user who downloads the binary has the documentation
its help text and skill reference without network access.

#### Scenario: Archive contents
- **WHEN** a release archive is unpacked
- **THEN** `docs/` is present next to the `mnemos` binary and its file set matches `docs/` at that tag

### Requirement: Skill documentation references resolve in every channel

The three `SKILL.md` copies (`.claude/skills/mnemos/`, `.agents/skills/mnemos/`,
`pi/skills/mnemos/`) SHALL reference only documentation that ships with the same
artifact. The chosen form is relative `docs/*.md` paths — supported by the release
archive requirement above and by plugin/marketplace installs being repository checkouts
— and every such reference SHALL name a file that exists in `docs/`.

#### Scenario: Dangling documentation reference
- **WHEN** a `SKILL.md` references a `docs/` path that does not exist at that tag
- **THEN** the reference is a release defect to be fixed, not an acceptable link

### Requirement: Capability documentation matches live behaviour and names non-default profiles

The capability document (`mnemos_read_capabilities`) SHALL report `pinned_memory: true`
while pinning behaviour is live in the running store, matching the
`memory-lifecycle-retrieval` contract that a capability is reported only while its
behaviour is live — pinning is live in the default `core` profile through the `pinned`
parameter of `mnemos_save` (excluded from decay, TTL cleared, Tier-0 prewarm slot).
Tool availability SHALL NOT downgrade a live capability.

The capability document and the three `SKILL.md` copies SHALL state the profile
boundary of pin state: the standalone tool that changes pin state after the fact
(`mnemos_pin`) is registered only by the `maintenance` profile, and under the default
`[mcp].profile = "core"` pin state can be set only at `mnemos_save` time. Any other
capability registered only by the `maintenance` profile SHALL likewise be attributed to
that profile instead of appearing as a default capability.

#### Scenario: Pin is live in the core profile
- **WHEN** an agent reads the capability document with the default core profile
- **THEN** it reports `pinned_memory: true` and describes pinning as available through the `mnemos_save` `pinned` parameter

#### Scenario: Post-hoc pin tooling is attributed to maintenance
- **WHEN** the documentation describes `mnemos_pin`
- **THEN** it states that the tool is registered only by the `maintenance` profile and that core can pin only at save time

#### Scenario: Maintenance-only capability
- **WHEN** a capability is registered only by the maintenance profile
- **THEN** the documentation names the profile rather than implying default availability
