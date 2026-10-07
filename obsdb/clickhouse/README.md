# weft/obsdb/clickhouse

The hosted backend of `weft/obsdb` (ADR 0024, S3.6): the observability
schema as ClickHouse tables that a stock OTel Collector can also feed.

```go
import (
    "github.com/weftgo/weft/obsdb"
    "github.com/weftgo/weft/obsdb/clickhouse"
)

db, err := clickhouse.Open("clickhouse://default:pass@localhost:9000/weft",
    clickhouse.TTL(30*24*time.Hour, 90*24*time.Hour)) // content, spans+runs
// db implements obsdb.DB: Write, Runs, Run, Events, Transcript,
// RunSpans, Trace, Sessions, Session, ResolvePublicID, SaveExperiment,
// Experiments, Experiment, Close.
```

`Open` creates and versions the schema in `obsdb_migrations` (the
SQLite backend's numbering rule), writes go through async inserts
(`async_insert=1, wait_for_async_insert=1`), and the weft tables are
maintained by materialized views:

- `otel_traces`, `otel_logs` — column-compatible with the OTel Collector
  ClickHouse exporter **pinned at v0.162.0** (the version named in
  `migrations/0001_init.sql`): we keep every column its INSERT names,
  and add the weft identity as materialized columns with bloom skip
  indexes on `SessionId`, `PublicId`, `Agent` and `TraceId`. A stock
  collector pinned to that version, configured with
  `create_schema: false`, writes into the same database.
- `weft_records` — `ReplacingMergeTree(InsertTime) ORDER BY (RunId,
  Kind, Pos)` (the transport idempotency key), filled from `otel_logs`
  where `weft.record IN ('event', 'messages')` — never `delta`, never
  `heartbeat`. Exact reads use `FINAL`.
- `weft_runs` — `AggregatingMergeTree ORDER BY RunId` filled from both
  `otel_logs` (heartbeats included, for last-seen) and `otel_traces`;
  `Sessions` groups it by `SessionId`.

Options: `TTL(content, meta)` overrides the retention windows (content
30 days, spans and runs 90 by default; values ≤ 0 keep a class's
default), `KeepDeltas()` stores delta rows in `weft_deltas` for
debugging (counted, never stored by default).

DSN forms: `clickhouse://user:pass@host:9000/database` (native
protocol, the port is 9000 not 8123), `clickhouses://...` or
`?secure=1` for TLS, extra query parameters become clickhouse-go
settings.

## Running the tests

Unit tests run offline. The conformance table
(`obsdbtest.Run`), the collector-shape insert test and the option
tests are gated on `WEFT_CLICKHOUSE_DSN` and need a server; each test
creates and drops its own database.

### Container recipe (one line)

```sh
docker run -d --name weft-clickhouse -p 127.0.0.1:9000:9000 \
  -e CLICKHOUSE_PASSWORD=weft --ulimit nofile=262144:262144 \
  clickhouse/clickhouse-server:25.8-alpine

WEFT_CLICKHOUSE_DSN='clickhouse://default:weft@127.0.0.1:9000/default' \
  go test -race ./...
```

### docker-compose

```yaml
services:
  clickhouse:
    image: clickhouse/clickhouse-server:25.8-alpine
    environment:
      CLICKHOUSE_PASSWORD: weft
    ports:
      - "127.0.0.1:9000:9000"
    ulimits:
      nofile: 262144:262144
```

### CI job (service container)

The job below is what `.github/workflows/ci.yml` runs for this module —
`clickhouse/clickhouse-server` as a service container, the DSN pointing
at the port the service maps onto the runner. A service container takes
`docker create` flags through `options:` (GitHub Actions has no
`ulimits:` key — one makes the whole workflow file invalid), and the
health check holds the steps until the server answers a query on the
native port. A gated test that skips fails the job, so a DSN the tests
cannot read never reads green; the same DSN un-gates `studio/cmd`'s
`--db clickhouse://` test.

```yaml
clickhouse:
  runs-on: ubuntu-latest
  services:
    clickhouse:
      image: clickhouse/clickhouse-server:25.8-alpine
      env:
        CLICKHOUSE_PASSWORD: weft
      ports:
        - 9000:9000
      options: >-
        --ulimit nofile=262144:262144
        --health-cmd "clickhouse-client --password weft --query 'SELECT 1'"
        --health-interval 5s
        --health-timeout 5s
        --health-retries 20
  env:
    WEFT_CLICKHOUSE_DSN: clickhouse://default:weft@127.0.0.1:9000/default
  steps:
    - uses: actions/checkout@v7
    - uses: actions/setup-go@v7
      with:
        go-version: stable
    - name: test (obsdb/clickhouse, gated suite)
      working-directory: obsdb/clickhouse
      run: |
        set -o pipefail
        go test -race -count=1 -v ./... | tee "$RUNNER_TEMP/clickhouse.log"
        if grep -E '^\s*--- SKIP' "$RUNNER_TEMP/clickhouse.log"; then
          echo "gated tests skipped: WEFT_CLICKHOUSE_DSN did not reach them" >&2
          exit 1
        fi
    - name: test (studio/cmd, gated --db clickhouse:// test)
      working-directory: studio/cmd
      run: |
        set -o pipefail
        go test -race -count=1 -v -run '^TestNewServerClickhouse$' ./... | tee "$RUNNER_TEMP/studio-cmd.log"
        grep -q -- '--- PASS: TestNewServerClickhouse' "$RUNNER_TEMP/studio-cmd.log"
```
