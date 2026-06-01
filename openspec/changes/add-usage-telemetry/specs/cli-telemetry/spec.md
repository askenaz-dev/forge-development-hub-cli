## ADDED Requirements

### Requirement: CLI lifecycle events

The CLI SHALL emit a lifecycle event to the portal's `/api/v1/events` endpoint on
`install`, `uninstall`, and `update`, and an `install.failed` event when an
install does not complete. Each event SHALL carry only the component coordinate
(`kind`, `namespace`, `name`, `version`) plus structured outcome fields (`os`,
`cli_version`, `scope`, `agent`, `install_id`).

#### Scenario: Successful install emits an event
- **WHEN** `fdh install` completes successfully and telemetry is enabled
- **THEN** the CLI emits `component.installed` with the coordinate and outcome
  fields

#### Scenario: Uninstall emits churn signal
- **WHEN** `fdh uninstall` removes a Forge-managed component
- **THEN** the CLI emits `component.uninstalled`

#### Scenario: Failed install reports a structured error class
- **WHEN** an install fails
- **THEN** the CLI emits `install.failed` with `error_class` ∈
  {`signature_mismatch`, `network`, `disk`, `permission`, `other`} and no raw
  error payload, argv, or file contents

### Requirement: Payload exclusion guardrail

CLI events SHALL NOT contain prompts, file contents, command arguments, source
paths, or any free-form text beyond the fixed coordinate and enumerated outcome
fields.

#### Scenario: No developer content leaves the machine
- **WHEN** the CLI builds any lifecycle event
- **THEN** the serialized event contains only the documented coordinate and
  enumerated outcome fields, and nothing derived from the developer's prompts,
  files, or argv

### Requirement: Opt-out telemetry control

CLI telemetry SHALL be enabled by default and opt-out. The CLI SHALL provide
`fdh config telemetry off|on`, SHALL honor the `DO_NOT_TRACK` environment
variable as a disable signal, and SHALL print a one-time first-run notice
describing what is collected and how to disable it.

#### Scenario: User disables telemetry
- **WHEN** the user runs `fdh config telemetry off`
- **THEN** no subsequent command emits any event

#### Scenario: DO_NOT_TRACK is honored
- **WHEN** `DO_NOT_TRACK` is set to a truthy value in the environment
- **THEN** the CLI emits no events regardless of stored config

#### Scenario: First-run notice
- **WHEN** the CLI runs for the first time with telemetry enabled
- **THEN** it prints a one-time notice stating what is collected and how to opt
  out

### Requirement: Anonymous installation identifier

The CLI SHALL persist an `install_id` that is a randomly generated UUID derived
from no personally identifying information (no hostname, MAC address, or
username). The user SHALL be able to regenerate it. It SHALL be stable enough to
measure retention over a window.

#### Scenario: Install id is generated on first run
- **WHEN** the CLI runs for the first time
- **THEN** it generates and stores a random `install_id` not derived from any PII

#### Scenario: User regenerates the identifier
- **WHEN** the user regenerates the `install_id`
- **THEN** a new random value replaces the old one and prior events cannot be
  linked to the new one by the client

### Requirement: Telemetry never blocks the command

Emitting an event SHALL NOT delay or fail the user's command. Network errors,
timeouts, or an unreachable portal SHALL be swallowed silently (visible only
under verbose/debug).

#### Scenario: Portal unreachable during install
- **WHEN** the portal cannot be reached while emitting an event
- **THEN** the `fdh` command still completes successfully and reports no
  telemetry error to the user
