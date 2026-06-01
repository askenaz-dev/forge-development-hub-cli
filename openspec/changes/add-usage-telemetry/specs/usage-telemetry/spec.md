## ADDED Requirements

### Requirement: Shared event ingestion endpoint

The portal API SHALL expose `POST /api/v1/events` as the single ingestion point
for product telemetry, accepting events from both the web frontend and the CLI.
Per the BFF rule, no client addresses an analytics backend or OTLP collector
directly. The endpoint generalizes and supersedes `POST /api/v1/activation`,
which SHALL continue to accept activation events for backward compatibility.

#### Scenario: Web frontend posts an event
- **WHEN** the web frontend POSTs a well-formed event to `/api/v1/events`
- **THEN** the portal accepts it (HTTP 200) and forwards it to the configured
  exporter

#### Scenario: CLI posts to the same endpoint
- **WHEN** the CLI POSTs a lifecycle event to `/api/v1/events`
- **THEN** the portal accepts it through the same handler and contract used by
  the web frontend

#### Scenario: Anonymous submission is allowed
- **WHEN** an unauthenticated client posts an event
- **THEN** the portal accepts it (events may originate pre-auth, e.g. onboarding)

#### Scenario: Malformed event is rejected
- **WHEN** a request body is missing required envelope fields or is not valid JSON
- **THEN** the portal responds 400 with an `error` code and does not export
  anything

### Requirement: Stable event envelope

Every event SHALL share a versioned envelope: `schema_version`, `event_name`,
`occurred_at` (UTC), an optional `install_id` or `wizard_session_id`, and an
`attributes` map whose keys are constrained per `event_name`. The envelope is the
transport contract: it SHALL remain stable so the ingestion backend can be split
out later without changing any client.

#### Scenario: Known event name with valid attributes
- **WHEN** an event carries a recognized `event_name` and the attributes its
  taxonomy permits
- **THEN** the portal accepts and exports it with those attributes intact

#### Scenario: Unknown attribute keys are dropped, not rejected
- **WHEN** an event carries attribute keys not defined for its `event_name`
- **THEN** the portal drops the unknown keys and still accepts the event, so
  forward-compatible clients do not break ingestion

### Requirement: Event taxonomy

The system SHALL recognize a closed set of `event_name` values across three
tiers: Tier 0 server-derived (`bundle.downloaded`, `search.zero_results`,
`component.not_found`), Tier 1 CLI lifecycle (`component.installed`,
`component.uninstalled`, `component.updated`, `install.failed`), and Tier 2
voluntary (`feedback.submitted`). Names not in the set SHALL be rejected.

#### Scenario: Recognized event name
- **WHEN** a client posts `component.installed`
- **THEN** the portal accepts it

#### Scenario: Unrecognized event name
- **WHEN** a client posts an `event_name` outside the taxonomy
- **THEN** the portal responds 400 and exports nothing

### Requirement: Tier 0 derivation from server-side signal

Tier 0 metrics SHALL be derived from server-observable signal with no client
telemetry. Each successful download of a wire bundle SHALL increment a labeled
Prometheus counter and emit a `bundle.downloaded` event; each search yielding no
results SHALL emit `search.zero_results`; each component 404 SHALL emit
`component.not_found`.

#### Scenario: Bundle download is counted
- **WHEN** a client successfully GETs a `.../versions/{version}/bundle.tar.gz`
- **THEN** the portal increments the download counter labeled by
  kind/namespace/name/version and emits `bundle.downloaded`

#### Scenario: Zero-result search is captured
- **WHEN** a catalog search request matches no components
- **THEN** the portal emits `search.zero_results` carrying the normalized query

#### Scenario: Component 404 is captured
- **WHEN** a request resolves to a non-existent component
- **THEN** the portal emits `component.not_found` with the requested coordinate

### Requirement: Non-blocking ingestion

Event ingestion and export SHALL NOT block or fail the request that produced the
event. A slow or unavailable exporter SHALL degrade to buffering or dropping
events, never to user-visible errors or added request latency.

#### Scenario: Exporter is unavailable
- **WHEN** the OTLP collector is unreachable while an event is produced
- **THEN** the originating request still succeeds and the portal keeps serving;
  the event is buffered or dropped per configuration

### Requirement: Insights surface in the existing admin UI

Aggregated insights (downloads, demand gaps, funnel, churn, feedback) SHALL be
exposed through the existing portal admin surface. No separate analytics frontend
is introduced.

#### Scenario: Admin views insights
- **WHEN** a user with the `admin` role opens the admin insights view
- **THEN** the portal serves aggregated telemetry; non-admins are forbidden
