# Spec Delta

## Purpose

Preserves verified progress identity and retry semantics as transports use a shared application-level context assembly entry point.

## ADDED Requirements

### Requirement: Unified context assembly carries the verified continuity receipt
The shared application entry point SHALL resolve knowledge scope separately from continuity owner/workspace/session scope and carry the exact validated continuity receipt through checkpoint and recovery calls. It SHALL preserve v1 owner, canonical root, CAS, retry, clear, and visibility semantics and legacy interfaces; it SHALL NOT infer a root or use a knowledge project label to widen progress visibility.

#### Scenario: The shared project label is equal across roots
- **WHEN** two verified workspaces use the same legacy project label and have different owners or roots
- **THEN** each continuity receipt retains its own owner, workspace, and session identities, and neither workspace can read the other's checkpoint

#### Scenario: The caller retries an exact checkpoint write
- **WHEN** the unified entry point receives the same valid session and request identity with the same checkpoint input
- **THEN** the existing v1 CAS and retry behavior returns the original receipt without creating a second write

#### Scenario: Root resolution is unavailable
- **WHEN** no verified workspace root or explicit non-Git root is supplied
- **THEN** the entry point does not derive continuity scope from the project basename or another inferred path
