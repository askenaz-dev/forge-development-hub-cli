---
name: forge-architecture-patterns
description: Reference architecture patterns and design principles applied across forge services. Covers service boundaries, data ownership, API design, event-driven patterns, and integration approaches. Use when designing a new system, evaluating an existing one, or arguing for a refactor.
license: MIT
metadata:
  author: architecture-guild
  sdlc_phase: architecture
  reference_repos:
    - https://gitlab.forge.internal/architecture/reference-implementations
    - https://gitlab.forge.internal/architecture/adr-catalog
---

# forge architecture patterns

This skill is the lookup table for "how do we do that here?". It does
not invent new patterns; it documents the ones already in production
across forge services, so a new design starts from precedent rather
than blank page.

## Principles before patterns

Six principles every forge service is expected to honor. When
patterns conflict, the principle wins.

1. **One owner per piece of data.** Each entity has exactly one
   service of record. Other services read snapshots or events; they
   never write through.
2. **APIs are the contract; implementation is private.** Anything an
   external consumer depends on must appear in a versioned, documented
   API. Implementation details leaking through the API surface are bugs.
3. **Events describe facts, not commands.** Use past-tense names
   (`OrderPlaced`, not `PlaceOrder`). Consumers decide what to do with
   the fact; producers do not orchestrate consumers.
4. **Idempotency at every boundary.** Any operation that crosses a
   process or network boundary must be safely retryable. Carry an
   idempotency key from the originating client through to persistence.
5. **Backward compatibility is required, not aspirational.** Add
   fields, never remove. Default to optional. Use `unknown` enum values.
   Deprecate via a documented sunset window (minimum 90 days for public
   APIs, 30 for internal).
6. **Observability is part of the design, not added later.** Decide on
   traces, key metrics, and structured log fields before the first line
   of code. See `forge-tech-stack` for the approved tooling.

## Service boundaries

A service owns:

- One or more related entities (e.g. `orders` and `order_items`).
- The persistence for those entities.
- The lifecycle rules for those entities (validation, state machine).
- The APIs that expose those entities (synchronous + events).

A service does NOT own:

- The UI rendering its data — that's the consuming surface's concern.
- The business workflow that orchestrates multiple services — that's a
  workflow / saga concern, distinct from any single service.
- Aggregations across multiple services' data — that's analytics, a
  separate layer.

When two services find themselves co-modifying the same data, that's a
boundary violation. Either merge them or move the disputed data into a
separate service that owns it.

## API design

### REST is the default for synchronous request/response

- Use HTTP semantics correctly: `GET` is safe and idempotent, `PUT` is
  idempotent, `POST` is for creation or non-idempotent operations, `PATCH`
  for partial updates, `DELETE` is idempotent.
- Resource names are nouns, plural, kebab-case: `/order-items`, not
  `/getOrderItems`.
- Version in the URL path: `/api/v1/orders`. Bump major on breaking
  changes; v1 stays alive through the deprecation window.
- Pagination is cursor-based for catalog-style endpoints, offset-based
  only for admin / debugging endpoints where total count is needed.
- Errors use the [RFC 7807 Problem Details](https://www.rfc-editor.org/rfc/rfc7807)
  shape with a `type` URL pointing at our error registry.

### Use gRPC for service-to-service in the same trust zone

- Schemas live in a shared proto repository with their own versioning.
- Backward compatibility is enforced at the build step via
  `buf breaking`.
- Outside the trust zone (browser, partner, mobile), use REST or
  GraphQL.

### GraphQL only at the BFF layer

- Single GraphQL gateway per consuming surface (web, mobile).
- The gateway composes underlying REST/gRPC services. Services do not
  expose GraphQL directly.

## Event-driven patterns

### Outbox + change feed

Services that emit events use the **outbox pattern**: every state change
writes both to the entity table and to an outbox table in the same
transaction. A separate process tails the outbox and publishes to Kafka.
This guarantees at-least-once delivery without dual-write inconsistency.

### Saga over distributed transaction

Multi-service workflows use the **saga** pattern with compensating
transactions. Each step is its own service-local transaction;
compensation is its own service-local transaction. There are no
two-phase commits at forge.

### Event schema evolution

- Events are versioned via the topic name (`order-events-v1`) or via a
  `schema_version` field in the payload. Pick one per domain and stay
  consistent.
- Consumers MUST tolerate unknown fields (forward compatibility).
- Producers MUST NOT remove or rename fields within a version.

## Caching

- **Read-through cache** (cache-aside) is the default. Service reads
  through Redis; on miss, hits the DB and populates the cache.
- **Write-through cache** is acceptable for high-throughput, low-stake
  paths (session data, feature flags).
- **Eventually-consistent denormalisations** (read-model projections
  built from events) replace read-through caches for catalog-style data
  where staleness is acceptable and read volume is very high.

Cache TTLs are explicit. Never "cache forever and hope".

## Integration with external partners

- All external integration goes through a dedicated **integration
  service** owned by the team that consumes the partner. Partner
  contracts (auth tokens, retry policies, rate limits) do not leak into
  the rest of the platform.
- Outbound calls have circuit breakers (Resilience4j, gobreaker, or
  equivalent — see `forge-tech-stack`).
- Partner downtime is a recoverable error, not a 5xx propagated to the
  user.

## When to deviate

These patterns are defaults, not laws. Deviate when:

- The cost of the pattern exceeds the value (e.g. a single internal CLI
  doesn't need a saga).
- A new pattern is genuinely better and worth the org-wide cost of
  adopting two patterns for the same concern.

Either way, **document the deviation in an ADR**
(`architecture/adr-generation` skill). The ADR is the input to the next
design review; a deviation without an ADR is debt.

## Where to read more

- ADR catalog: see `metadata.reference_repos` above.
- Reference implementations: same.
- Architecture guild office hours: weekly, schedule on the platform
  calendar.
- For language-/framework-specific guidance, see `forge-tech-stack`.
