# Local development

One command brings up the full portal stack — API + Web + Keycloak +
fixture registry — on your machine.

## Prerequisites

- Docker Engine 24+ or Docker Desktop
- ~2 GB of free RAM (Keycloak is the heavy part)
- Ports 3000, 8080, 8088 free on `localhost`

## Up

From the repo root:

```sh
docker compose up
```

First boot takes ~60–90 seconds (Keycloak realm import + Go API build +
Next.js production build). Subsequent boots use the build cache and are
much faster.

When you see the line:

```
api-1   {"msg":"registry refreshed","skill_count":8,...}
```

the stack is ready. Open <http://localhost:3000>.

## What's running

| Service          | URL                              | Purpose                                |
| ---------------- | -------------------------------- | -------------------------------------- |
| Portal web       | <http://localhost:3000>          | Next.js frontend                       |
| Portal API       | <http://localhost:8080>          | Go HTTP API                            |
| Keycloak         | <http://localhost:8088>          | IdP (admin: `admin` / `admin`)         |
| Fixture registry | mounted as a volume              | Pre-built spec-compliant skill registry |

## Test users

Three seeded users in the `fdh-dev` realm:

| Username             | Password   | Groups          | Portal role |
| -------------------- | ---------- | --------------- | ----------- |
| `admin@fdh.local`    | `admin`    | `fdh-admins`    | `admin`     |
| `author@fdh.local`   | `author`   | `fdh-authors`   | `author`    |
| `consumer@fdh.local` | `consumer` | (none)          | `consumer`  |

Sign in at <http://localhost:3000/auth/signin> and pick a user. The
profile page (`/profile`) shows your identity + group claims; the admin
page (`/admin`) is gated to `fdh-admins`.

## Editing the realm

`compose/keycloak/realm-fdh.json` is imported on first boot. To change
groups, users, or the OIDC client config:

1. Edit the JSON file.
2. `docker compose down -v` (removes the volume that holds Keycloak state).
3. `docker compose up`.

The realm is re-imported from the JSON during start-dev.

## Editing skills

The fixture registry is rebuilt every time the `fixture-registry`
container runs (i.e. on every `docker compose up`). To change the seed
skills, edit `scripts/build-fixture-registry/main.go` and restart the
stack. The API picks up new commits via its 60s auto-refresh (or you can
force a refresh by `POST`-ing to `/api/v1/refresh` from inside the
network).

## Tearing down

```sh
docker compose down       # stops containers, keeps the registry volume
docker compose down -v    # also removes Keycloak's H2 DB and the registry volume
```

## Running without Docker

The Go API + Web also run natively side-by-side without containers — see
the repo `README.md` for the bare-metal recipe. Keycloak still needs a
container in dev (running it natively is a slog); the compose file lets
you point the standalone API + Web at the dockerized Keycloak by
exporting the same env vars.
