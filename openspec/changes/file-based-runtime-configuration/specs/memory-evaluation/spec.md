# Spec Delta

## ADDED Requirements

### Requirement: Agent runners isolate stores with files and arguments
Evaluation runners SHALL bind their exact candidate binary and scratch configuration through explicit files/argv. Host settings SHALL prevent global hooks or servers from accessing the live store or contaminating the off arm. Unsafe isolation SHALL fail before any model launch; product environment overrides SHALL not be required.

#### Scenario: Global hooks target another binary
- **WHEN** an on/off runner is prepared
- **THEN** its isolated host settings exclude those hooks and use explicit scratch routes only

#### Scenario: The off arm is prepared
- **WHEN** no memory is enabled for the arm
- **THEN** MCP and automatic hooks are disabled by isolated files without changing the user's configuration or copying credentials
