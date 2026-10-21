# Spec Delta

## ADDED Requirements

### Requirement: Session and context assembly has one application behavior
Task-session start and context assembly SHALL use the same application-level behavior across MCP, HTTP, CLI, and host adapters. Adapters SHALL translate protocol inputs and outputs without reimplementing scope resolution, session lifecycle, context selection, or maintenance policy. Existing legacy hook rendering and explicit continuity v1 semantics SHALL remain compatible.

#### Scenario: Equivalent transports assemble the same scope
- **WHEN** equivalent task-session inputs with the same configuration and scope arrive through MCP, HTTP, and CLI
- **THEN** they use the same session and context behavior and return the same underlying receipt and selected context, allowing only transport-envelope differences

#### Scenario: Continuity startup commits before context assembly
- **WHEN** an application-level start opens a continuity session
- **THEN** context is assembled using that exact returned owner, workspace, and session identity, and a context failure does not replace or hide the committed receipt

#### Scenario: A host adapter receives an existing legacy request
- **WHEN** an adapter forwards a supported legacy hook or session request
- **THEN** its existing protocol output and lifecycle behavior remain compatible while the application entry performs the underlying policy

#### Scenario: A caller omits or supplies an ambiguous workspace root
- **WHEN** an application-level start cannot verify the explicitly supplied root
- **THEN** it preserves the existing unbound behavior and does not infer a root from project labels, current history, or a shared ancestor
