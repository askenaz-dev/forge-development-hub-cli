## ADDED Requirements

### Requirement: OTLP-only export

The portal SHALL export telemetry exclusively via OTLP to an OpenTelemetry
Collector. The application SHALL NOT contain any backend-specific exporter
(no direct ELK, Datadog, Loki, or ClickHouse client). Switching or adding
backends SHALL be a Collector configuration change, not an application change.

#### Scenario: App emits OTLP only
- **WHEN** the portal exports any signal
- **THEN** it is sent via OTLP to the configured collector endpoint, with no
  vendor-specific exporter compiled into the app

#### Scenario: Backend switch needs no app change
- **WHEN** the target backend changes from ELK to another system
- **THEN** only the Collector configuration changes; the portal binary and
  config (other than the OTLP endpoint) are unaffected

### Requirement: Instrument all four signals

The portal SHALL emit traces, metrics, logs, and product events over OTLP. Trace
context already propagated via `traceparent` SHALL be carried into exported
traces; existing Prometheus metrics SHALL be reconciled into the OTLP metrics
pipeline; structured logs SHALL be bridged to OTLP log records.

#### Scenario: Trace context is preserved
- **WHEN** a request arrives with a valid `traceparent`
- **THEN** the exported trace carries the inbound trace id

#### Scenario: Existing operational metrics remain available
- **WHEN** OTLP export is enabled
- **THEN** request duration, in-flight, and registry-refresh metrics continue to
  be observable

### Requirement: Product events as OTLP log records

Tier 0/1/2 product events SHALL be modeled as OTLP log records carrying a
well-known `event.name` attribute. The Collector SHALL route `event.*` records to
the analytics store and operational signals to their respective backends.

#### Scenario: Product event is routed to analytics
- **WHEN** a record with `event.name = component.installed` reaches the Collector
- **THEN** the Collector routes it to the analytics store

#### Scenario: Additive analytics backend
- **WHEN** a columnar store is added for heavy product aggregations
- **THEN** it is configured as an additional Collector exporter without changing
  the application

### Requirement: Degradable export

Export failures SHALL be isolated from request handling. If the Collector is
unreachable, the portal SHALL continue serving and SHALL buffer or drop telemetry
without surfacing errors to clients.

#### Scenario: Collector down
- **WHEN** the OTLP collector is unreachable
- **THEN** the portal keeps serving requests and export failures are not
  user-visible
