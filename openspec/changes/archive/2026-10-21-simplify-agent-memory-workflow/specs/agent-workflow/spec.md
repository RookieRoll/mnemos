# Spec Delta

## Purpose

Defines the two Mnemos operating responsibilities and the fixed MCP profiles that let a host expose only the tools needed for a task or an explicit maintenance assignment.

## ADDED Requirements

### Requirement: Task and maintenance responsibilities are distinct
The Task Agent SHALL use knowledge and progress tools for assigned work. The Maintenance Agent SHALL review knowledge quality and perform installation, configuration, backup, or migration only under an explicit task or configured policy. A host-provided agent MAY switch responsibilities or delegate; Mnemos SHALL NOT start or call a model for either responsibility.

#### Scenario: Routine task work uses task responsibilities
- **WHEN** a host starts a normal work session with the core profile
- **THEN** the exposed role supports knowledge recall, capture, session lifecycle, and exact progress operations without requiring maintenance tools

#### Scenario: Maintenance is explicitly assigned
- **WHEN** a user task or configured policy authorizes a maintenance operation
- **THEN** the Maintenance Agent can inspect the relevant evidence and perform only the authorized operation

#### Scenario: An expiry or due check occurs
- **WHEN** a maintenance candidate or scheduled-due marker exists without an explicit task or policy authorizing an installation, configuration, backup, or migration operation
- **THEN** no such administrative change is performed automatically

#### Scenario: The host has no maintenance agent available
- **WHEN** a semantic review requires judgment and no host-provided Maintenance Agent is active or delegable
- **THEN** Mnemos retains the candidate for a later explicit review and does not resolve or dismiss it

### Requirement: MCP tool profile is fixed at server startup
The MCP server SHALL read one TOML-selected profile at startup and keep its registered tool set fixed for that process lifetime. In 0.17.0, default `core` SHALL expose exactly the nine task tools; this is a breaking change to the default MCP tool surface. `maintenance` SHALL restore the existing full MCP tool set. CLI/API behavior, legacy hook interfaces, knowledge labels, and continuity receipts have separate compatibility contracts and are not changed by this tool-surface migration.

#### Scenario: Core profile is the default
- **WHEN** the TOML profile is omitted or set to `core`
- **THEN** tools/list contains exactly `mnemos_session_start`, `mnemos_session_end`, `mnemos_search`, `mnemos_get_many`, `mnemos_save`, `mnemos_correct`, `mnemos_convention`, `mnemos_checkpoint`, and `mnemos_resume_context`

#### Scenario: An existing installation requires the previous full tool set
- **WHEN** an operator upgrades to 0.17.0 and still requires MCP tools removed from the default core profile
- **THEN** the operator selects `mcp.profile = "maintenance"` before restarting, which restores the existing full MCP tool set

#### Scenario: Maintenance profile is selected before startup
- **WHEN** TOML selects `maintenance` before the MCP process starts
- **THEN** the server exposes the core tools and the existing advanced MCP tools for maintenance work

#### Scenario: Profile changes while a server is running
- **WHEN** the TOML profile changes after the MCP process has started
- **THEN** tools/list remains unchanged until a new server process starts

#### Scenario: An unknown profile is configured
- **WHEN** the selected profile is not supported
- **THEN** configuration validation fails before the server advertises a partial or guessed tool set

### Requirement: Core profile has an explicit maintenance CLI bridge
The `core` profile SHALL provide an executable JSON CLI path for Maintenance Agent operations that require advanced evidence or explicit candidate resolution. The bridge SHALL use the existing maintenance commands and validation, SHALL NOT change the running MCP tool list, and SHALL NOT imply that stdio can wake a model.

#### Scenario: A maintenance assignment runs under the core profile
- **WHEN** the active host agent is explicitly assigned semantic maintenance while only the core profile is available
- **THEN** it can invoke the documented JSON CLI bridge to inspect evidence and submit an explicit validated resolution without dynamically adding MCP tools

#### Scenario: No active host agent can review a candidate
- **WHEN** a candidate needs semantic judgment and no active or delegated Maintenance Agent is available
- **THEN** the candidate remains pending until a host agent explicitly reviews it
