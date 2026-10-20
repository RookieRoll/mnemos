# Spec Delta

## ADDED Requirements

### Requirement: Installation propagates file configuration explicitly
Installer discovery SHALL use file-configured client paths or conventional defaults, not former environment overrides. Installed owned hooks and MCP entries SHALL preserve the selected config route, normalize manifest-relative paths before execution, and preserve unrelated user fields and single-route idempotency.

#### Scenario: Client directories are explicitly configured
- **WHEN** init and doctor run with a selected file containing custom Claude/pi paths
- **THEN** both inspect the same configured targets despite conflicting inherited host-directory overrides

#### Scenario: An installed package already contains a config selector
- **WHEN** init refreshes its owned entry
- **THEN** command/config paths are unambiguous, adapter fields survive and no duplicate global route is created
