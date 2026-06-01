## ADDED Requirements

### Requirement: Configurable privacy posture

The portal SHALL expose a `telemetry` configuration block with a `mode` of
`internal` or `public`, configurable per deployment. `mode` SHALL set the
defaults for `ip_handling` (`full | truncate | hash | drop`), `identity`
(`attributed | anonymous_first`), and `retention_days`. Each default SHALL be
individually overridable.

#### Scenario: Public mode defaults
- **WHEN** the portal starts with `telemetry.mode: public`
- **THEN** `ip_handling` defaults to a non-`full` value, `identity` defaults to
  `anonymous_first`, and a bounded `retention_days` is applied

#### Scenario: Internal mode defaults
- **WHEN** the portal starts with `telemetry.mode: internal`
- **THEN** attributed identity and full IP MAY be retained under the configured
  retention policy

#### Scenario: Explicit override wins over mode default
- **WHEN** a deployment sets `ip_handling` explicitly
- **THEN** the explicit value is used regardless of `mode`

### Requirement: IP handling for request logging and events

When `ip_handling` is not `full`, the portal SHALL transform or omit the client
IP before it is written to request logs or attached to any event. This applies to
the existing request logging path as well as new event ingestion.

#### Scenario: Truncated IP in public mode
- **WHEN** `ip_handling: truncate` and a request is logged
- **THEN** the persisted record contains a truncated IP, not the full address

#### Scenario: Dropped IP
- **WHEN** `ip_handling: drop`
- **THEN** no client IP appears in logs or events

### Requirement: Identity handling in public mode

When `identity: anonymous_first`, the portal SHALL NOT join an authenticated
`user_id` to an anonymous `install_id`, and SHALL NOT persist `user_id` on
product events.

#### Scenario: No identity join in anonymous-first mode
- **WHEN** an authenticated user produces an event under `anonymous_first`
- **THEN** the exported event carries neither `user_id` nor a mapping from
  `install_id` to `user_id`

### Requirement: Content-exclusion guarantee

The system SHALL NOT ingest, store, or export prompts, file contents, command
arguments, or consolidated session/day activity reports. Only the documented
coordinates, enumerated outcome fields, and explicitly volunteered feedback text
are permitted.

#### Scenario: No session or prompt data accepted
- **WHEN** any client attempts to submit prompt content, file contents, or a
  session activity report
- **THEN** the portal does not persist or export that content

### Requirement: Bounded retention

Telemetry records SHALL be retained no longer than the configured
`retention_days`. Records older than the window SHALL be deleted from the
telemetry store.

#### Scenario: Records expire
- **WHEN** a telemetry record is older than `retention_days`
- **THEN** it is removed from the telemetry store
