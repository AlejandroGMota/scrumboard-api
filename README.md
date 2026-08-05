# scrumboard-api

The backend of a self-hosted, single-user Kanban board: configurable columns,
cards dragged between them, and live sync across every device I have open.

Go 1.25, standard `net/http`, SQLite, hexagonal architecture. No framework, no
ORM, no test framework — the seams are the point.

The frontend lives in a separate repository. The two never talk to each other:
their only meeting point is nginx.

## Why it is built this way

| Decision | Reason |
|---|---|
| Standard `net/http`, no framework | Go 1.22+ pattern routing is enough for ~15 endpoints |
| SQLite via `modernc.org/sqlite` | One file, zero infrastructure. A pure-Go driver means no cgo and a static binary in a `scratch` image |
| Hexagonal (ports and adapters) | SQLite and the WebSocket stay replaceable details; learning where the seams go was an explicit goal |
| Client-generated UUID v7 ids | A card created offline needs an identity before the server has seen it |
| Soft deletes everywhere | With deferred sync, "this was deleted" has to be distinguishable from "this has not arrived yet" |
| Atlas for migrations, `go:embed` to apply them | Atlas is an authoring tool for development and CI; the production image stays one static binary with nothing extra to fail |

Full reasoning in [docs/00-scope.md](../docs/00-scope.md) and
[docs/01-structure.md](../docs/01-structure.md).

## Running it

```sh
make dev      # SQLite in /tmp, port 8020, a development token
make test     # the whole suite, in seconds
make help     # everything else
```

`make dev` is all it takes. The frontend's Vite proxy points at
`localhost:8020`, so working on both needs no Docker at all.

## Configuration

Four environment variables, listed in [.env.example](.env.example). The one
with no default is `SCRUMBOARD_TOKEN`: the process refuses to start without it
rather than coming up unprotected.

## Access control

Two layers, because this is exposed to the internet:

1. **Basic auth in nginx** over `/app`, `/api` and `/ws` — it filters scanners
   before they reach the Go process. `/` stays out of it, or Googlebot gets a
   401 and the landing page is never indexed.
2. **`SCRUMBOARD_TOKEN`**, sent in `X-Scrumboard-Token` and checked by
   middleware here and during the WebSocket handshake. No user table, no
   password hashing, because there are no users.

The token deliberately does **not** use `Authorization`. Layer 1 already owns
that header, a request carries exactly one of them, and a bearer token from the
browser displaces the basic credentials — nginx then answers 401 before this
process is reached. `Authorization: Bearer` is still accepted for callers with
nothing in front of them (curl, CI); the WebSocket takes `?token=` because
browsers cannot set headers on it at all.

`TestEveryRouteRequiresAuth` walks the route table and checks that every
non-public route answers 401 without a token.
`TestRouteTableMatchesRouter` is the one that matters: it fails when a route is
registered but missing from the table, so a new endpoint cannot quietly escape
the first test.

## Tests

```sh
make test           # everything — target: under 10 seconds
make test-race      # before every PR
make test-endpoints # just the endpoint suite, verbosely
make cover          # to find what was forgotten, not to hit a number
```

The endpoint tests use **no mocks**. Router, middleware, services and SQLite
repositories are all real, over a temporary database that each test owns. The
only substitutions are the clock, the id generator and the event publisher —
time, randomness, and the thing assertions need to observe.

There is no coverage threshold and there will not be one. The metric is the
matrix in [docs/02-testing.md](../docs/02-testing.md): complete rows, not
executed lines.

## Migrations

`schema.sql` describes the schema as it should be. Atlas diffs against it:

```sh
make migrate-new name=add_something   # generate
make migrate-lint                     # warn before something destructive
make schema-check                     # fail on drift, also run in CI
```

At runtime the container applies the embedded migrations itself, with no Atlas
binary involved.

Two rules that avoid the usual pain: never hand-edit `atlas.sum` (regenerate it
with `make migrate-hash`), and never edit a migration that has already run —
write a new one.

> **First-time setup:** `migrations/atlas.sum` is generated, not written. Run
> `make migrate-hash` once after cloning, before CI can lint the directory.

## Backups

The `.db` file lives in a Docker volume, and the offline write queue raises the
stakes: a lost volume is no longer "I will rebuild the board", it is changes
that devices believe were synced.

`deploy/backup.sh` runs nightly from cron, rotates seven days, and copies the
result off the VM. It takes the snapshot with SQLite's own hot-backup path
rather than `cp` — with WAL enabled, copying the file while writes are in
flight produces a snapshot that looks fine until the day it is needed.

```sh
make backup         # run it now
make restore-check  # pull the newest backup and actually open it
```

A backup nobody has restored is a hypothesis.

## Layout

```
cmd/api/          composition root: the only place anything is wired together
internal/
  domain/         entities and rules. Imports nothing
  app/
    port/         the interfaces: what the app offers, what it needs
    service/      use cases
  adapter/
    in/http/      handlers, middleware, the route table
    in/ws/        the WebSocket hub
    out/sqlite/   the only package that knows SQL exists
  testutil/       boots the full stack over a temporary database
schema.sql        the desired schema; Atlas diffs against it
migrations/       generated, embedded, applied at startup
deploy/           compose file, nginx block, backup script
```

Dependencies point inward and never the other way. `domain` imports nothing,
`app` imports `domain` and declares interfaces, and `adapter` is the only layer
that knows HTTP, SQL and WebSockets exist.

## Licence

MIT. See [LICENSE](LICENSE).
