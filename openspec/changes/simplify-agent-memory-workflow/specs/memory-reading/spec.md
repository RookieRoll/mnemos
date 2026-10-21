# Spec Delta

## ADDED Requirements

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
