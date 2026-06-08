# Citadel Logs Daemon — Design

**Status:** approved 2026-06-09
**Branch:** `feat/citadel-logs-daemon`

## Problem

Citadel today is a one-shot CLI. Operators run `citadel logs` per service to
tail CloudWatch interactively, which means:

- No central place to spot 500s across all clusterbox backends at once.
- `Aragorn`, `gollum` (NestJS on ECS Fargate) and `smaug` (Go on Lambda) have
  different log group naming and parser conventions; the existing CLI only
  understands ECS.
- Closing the terminal loses context. Errors that happen overnight require
  digging through CloudWatch console.

## Goals

- Always-on local observatory at `http://localhost:5500/logs`.
- Errors-first: surface 500-class events with message, stack, and request id.
- Pluggable per-runtime parsing so adding a new clusterbox backend is "write
  one parser file."
- Opinionated, built for clusterbox conventions — not a general log explorer.

## Non-goals (v1)

- Full log explorer / free-text search across raw lines.
- Hosted / multi-user deployment. localhost only.
- Push-based ingestion (subscription filters / Live Tail).
- Authentication (relies on `localhost` binding + `~/.aws` mount).

## Architecture

Separate binary `cmd/citadel-logs` in the same Go module as `cmd/citadel`,
sharing `internal/aws` and `pkg/config`. Runs as a Docker container with
`~/.aws` and a SQLite volume mounted in. The main `citadel` binary gains a
small `logs-daemon` subcommand group for registry management.

```
cmd/
  citadel/           existing one-shot CLI
  citadel-logs/      NEW daemon binary
internal/
  aws/               reused (CloudWatch FilterLogEvents, ECS log-group discovery)
  registry/          NEW loads ~/.citadel/registry.yml + each repo's citadel.yml
  logsdb/            NEW SQLite schema, queries, retention sweep
  ingest/            NEW per-service poll loop
    parsers/         NEW nestjs.go, golambda.go, Parser interface
pkg/
  config/            extended: Runtime field, Lambda block
web/
  templates/         NEW Go html/template (embed.FS)
  static/            NEW htmx + minimal CSS
Dockerfile.logs      NEW multi-stage → distroless
docker-compose.yml   NEW example run
```

## Configuration

### `~/.citadel/registry.yml` (host) → `/etc/citadel/registry.yml` (container)

```yaml
services:
  - repo: /repos/aragorn   # container-side path
    env: dev
  - repo: /repos/smaug
    env: dev
```

Daemon loads each `<repo>/citadel.yml` for name/region/runtime. Identity is
`<name>-<env>`. fsnotify on the registry file → hot reload.

### `citadel.yml` extension (additive, backward-compatible)

```yaml
name: smaug
region: us-east-1
runtime: lambda          # NEW, default "ecs"
lambda:                  # required when runtime: lambda
  functionName: SmaugFn
```

For `runtime: ecs` the daemon calls existing `ecsClient.DiscoverLogGroup`. For
`runtime: lambda` it resolves `/aws/lambda/<functionName>` and verifies via
`lambda:GetFunction` at startup.

## Data model (SQLite at `/data/citadel-logs.db`)

```sql
CREATE TABLE services (
  id TEXT PRIMARY KEY,          -- "<name>-<env>"
  name TEXT NOT NULL,
  env TEXT NOT NULL,
  region TEXT NOT NULL,
  runtime TEXT NOT NULL,        -- ecs | lambda
  log_group TEXT NOT NULL,
  repo_path TEXT NOT NULL
);

CREATE TABLE ingest_cursor (
  service_id TEXT PRIMARY KEY REFERENCES services(id),
  last_ts INTEGER NOT NULL,     -- ms epoch
  updated_at INTEGER NOT NULL
);

CREATE TABLE error_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  ts INTEGER NOT NULL,
  status INTEGER,               -- HTTP status if parser found one
  level TEXT,
  message TEXT NOT NULL,
  request_id TEXT,
  stack TEXT,
  raw TEXT NOT NULL,
  log_stream TEXT,
  cw_event_id TEXT UNIQUE       -- CloudWatch eventId for dedupe
);
CREATE INDEX idx_error_events_service_ts ON error_events(service_id, ts DESC);
```

Hourly sweeper: `DELETE FROM error_events WHERE ts < now - 7d`.

Raw lines for non-errors are **not** persisted in v1 — errors carry their own
`raw` column for full-line display.

## Ingestion

One goroutine per service. Polls `FilterLogEvents` every 10s with staggered
start (`hash(id) % 10s`). Cursor (`last_ts`) is persisted in SQLite for
resumable polling across restarts. CloudWatch `eventId` dedupes via
`INSERT OR IGNORE`. Throttling → exponential backoff 10s → 60s → 5m steady.

### Parsers

```go
type Parser interface {
    Parse(event types.FilteredLogEvent) (*ErrorEvent, bool)
}
```

- **`nestjs.go`** — JSON lines with `statusCode >= 500` OR `level == "error"`.
  Extracts `message`, `stack`, `requestId`, `path`, `method`.
- **`golambda.go`** — `slog` lines with `level=ERROR`, OR APIGW response lines
  with `5\d\d`. Extracts `requestId` from `START` correlation.

No generic fallback. Unknown runtime = error at startup.

## HTTP server / UI

Standard `net/http` on `:5500`. Routes:

- `GET /logs` — main dashboard (server-rendered template).
- `GET /logs/errors?service=<id>&since=<ts>` — htmx fragment, latest errors.
- `GET /logs/error/<id>` — htmx fragment, expanded detail (stack, raw).
- `GET /api/services` — JSON list.
- `GET /api/errors?service=<id>&limit=&since=` — JSON.
- `GET /healthz` — liveness.

UI is a single page: left rail lists registered services with red badge
showing 7-day error count; right pane shows reverse-chronological errors for
the selected service, htmx-polled every 5s. Click an error to expand stack +
raw. No build step — `web/` is `embed.FS`.

## Packaging

`Dockerfile.logs` is multi-stage: `golang:1.22` build stage → `distroless`
runtime. Image entrypoint = `/citadel-logs`. Example compose:

```yaml
services:
  citadel-logs:
    image: clusterbox/citadel-logs:latest
    ports: ["127.0.0.1:5500:5500"]
    volumes:
      - ~/.aws:/root/.aws:ro
      - ~/.citadel:/etc/citadel:ro
      - ~/repos:/repos:ro
      - citadel-logs-db:/data
    environment:
      AWS_PROFILE: default
    restart: unless-stopped
volumes:
  citadel-logs-db:
```

## CLI surface

Added to existing `citadel` binary (thin convenience over editing YAML):

- `citadel logs-daemon register --env <env>` — appends current dir to registry.
- `citadel logs-daemon list` — prints registered services + status.
- `citadel logs-daemon unregister <id>` — removes entry.

## Testing strategy

- `pkg/config`: table-driven tests for new runtime field + lambda block.
- `internal/registry`: golden-file tests for registry loading.
- `internal/logsdb`: in-memory SQLite, schema migration test, dedupe test,
  retention sweep test.
- `internal/ingest/parsers`: fixture-based — paste real log lines from each
  clusterbox backend into testdata, assert extracted fields.
- HTTP handlers: `httptest` smoke tests for each route.

No AWS-touching integration tests in CI — the existing pattern in
`internal/aws` is unit tests with mocks; we follow it.
