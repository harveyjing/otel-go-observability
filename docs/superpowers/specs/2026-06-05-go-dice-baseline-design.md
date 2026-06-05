# Go Dice Roller — Baseline (No OTel) Design

**Date:** 2026-06-05
**Status:** Approved
**Goal:** Implement the two-service Go dice roller *without* OpenTelemetry instrumentation, as the baseline half of a before/after OTel comparison. The observability stack (Grafana, Tempo, Loki, Prometheus, OTel Collector at `localhost:4317`) is already running via kind.

---

## Context

The `otel-go-observability/` directory already contains:
- `deploy/` — Helm values for kube-prometheus-stack, Loki, Tempo, OTel Collector
- `deploy/kind/cluster.yaml` — kind cluster with port mappings (Grafana :3000, OTel gRPC :4317)
- `scripts/bootstrap-cluster.sh` — installs the full stack; references `run-apps.sh` and `load.sh` which this spec defines

The Go app lives at `otel-go-observability/app/` alongside the existing infra scripts.

---

## Architecture

```
Client (curl / load.sh)
        │
        │ GET /rolldice
        ▼
  frontend :8080
        │
        │ GET /roll  (HTTP, 10s timeout)
        ▼
  backend :8081
        │
        └── rolls dice → {"result": N}

  frontend returns the result as plain text "N\n"
```

Two services, one Go module, HTTP transport between them.

---

## Repository Layout

```
otel-go-observability/app/
├── go.mod                        # module: dice
├── go.sum
├── cmd/
│   ├── backend/
│   │   └── main.go
│   └── frontend/
│       └── main.go
└── internal/
    └── dice/
        ├── roll.go
        └── roll_test.go
```

Supporting scripts added to `otel-go-observability/scripts/`:
- `run-apps.sh` — builds and starts both binaries, prints PIDs
- `load.sh` — loops `curl localhost:8080/rolldice` to generate traffic
- `teardown.sh` — kills app processes, optionally deletes the kind cluster

---

## Services

### backend (`:8081`)

- `GET /roll` — calls `dice.Roll()`, returns `{"result": N}` JSON with `Content-Type: application/json`
- Logs each request: method, path, status, duration (via logging middleware)

### frontend (`:8080`)

- `GET /rolldice` — calls `http://<BACKEND_ADDR>/roll`, writes the integer result as plain text
- Uses a package-level `http.Client` with 10s timeout (not `http.DefaultClient`)
- Logs each request: method, path, status, duration, backend URL called

---

## Internal Package

### `internal/dice/roll.go`

```go
// Roll returns a random integer in [1, 6].
func Roll() int
```

Uses `math/rand/v2` (Go 1.22+) — no manual seeding required.

### `internal/dice/roll_test.go`

Table-driven test verifying `Roll()` always returns a value in [1, 6] across N iterations.

---

## Logging

Both services configure `log/slog` with a JSON handler writing to stdout at program startup:

```go
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

A shared `logMiddleware(next http.Handler) http.Handler` wraps each mux and logs:
- `method`, `path`, `status`, `duration_ms`
- Frontend additionally logs `backend_url`

This is the realistic "structured logs, no traces/metrics" baseline. In the OTel phase, this middleware is augmented or replaced by `otelhttp`.

---

## Configuration

| Env var         | Default               | Used by  |
|-----------------|-----------------------|----------|
| `BACKEND_ADDR`  | `http://localhost:8081` | frontend |
| `FRONTEND_PORT` | `8080`                | frontend |
| `BACKEND_PORT`  | `8081`                | backend  |

---

## Graceful Shutdown

Both servers listen for `SIGINT`/`SIGTERM`, call `server.Shutdown(ctx)` with a 5s deadline, and exit 0 on success. This ensures `run-apps.sh` can cleanly stop processes without leaving ports bound.

---

## Scripts

### `scripts/run-apps.sh`

1. Builds `cmd/backend` and `cmd/frontend` via `go build`
2. Starts each in the background, redirecting logs to stdout
3. Prints PIDs so `teardown.sh` can kill them
4. Polls `curl -sf` on each port until it responds (max 10 retries, 0.5s sleep) before returning

### `scripts/load.sh`

Loops `curl -s localhost:8080/rolldice` with a short sleep (0.5s) until interrupted. Prints each result.

### `scripts/teardown.sh`

Kills background app processes (by reading PIDs or `pkill`) and deletes the kind cluster with `kind delete cluster --name otelpoc`.

---

## Out of Scope (This Phase)

- OpenTelemetry SDK initialization
- Trace/metric/log exporters
- `otelhttp` middleware
- Containerization / Kubernetes deployment of the app

These are deferred to the next phase (OTel instrumentation), which will diff cleanly against this baseline.

---

## Success Criteria

1. `go test ./...` passes
2. `scripts/run-apps.sh` starts both services without error
3. `curl localhost:8080/rolldice` returns a number 1–6
4. Both services emit structured JSON logs to stdout on each request
5. `scripts/teardown.sh` cleanly stops both processes
