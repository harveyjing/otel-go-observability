# OTel Instrumentation Design

**Date:** 2026-06-05
**Status:** Approved
**Goal:** Add OpenTelemetry traces, metrics, and logs to the Go dice roller, exporting all three signals via OTLP gRPC to `localhost:4317`. This is the "after" half of the baseline vs. instrumented comparison.

---

## Context

The baseline app (`2026-06-05-go-dice-baseline-design.md`) is running on `main`:
- `cmd/frontend` (:8080) — `GET /rolldice`, calls backend via HTTP
- `cmd/backend` (:8081) — `GET /roll`, returns `{"result": N}`
- `internal/middleware` — custom `log/slog` request logger (to be replaced)
- OTel Collector already running in kind at `localhost:4317`, routing traces → Tempo, metrics → Prometheus, logs → Loki

---

## Signal Flow

```
curl /rolldice
      │
      ▼
frontend :8080
  otelhttp.NewHandler(mux)          ← inbound span + HTTP server metrics
  otelhttp.NewTransport(...)        ← outbound span (injects W3C traceparent)
      │
      ▼  (traceparent header propagated)
backend :8081
  otelhttp.NewHandler(mux)          ← inbound span (child of frontend span)
  dice.rolls counter                 ← custom metric: result attribute 1–6
      │
      ▼
OTel Collector :4317 (OTLP gRPC)
  traces  → Tempo
  metrics → Prometheus (remote write)
  logs    → Loki (OTLP HTTP)
```

**Three signals per service:**

- **Traces:** `otelhttp.NewHandler` wraps the HTTP mux on both services. `otelhttp.NewTransport` wraps `backendClient`'s transport on the frontend, injecting `traceparent` so Tempo shows a single distributed trace across both hops.
- **Metrics:** `otelhttp` automatically records `http.server.request.duration`, `http.server.active_requests`, and related semantic-convention metrics. The backend additionally increments a custom `dice.rolls` counter with attribute `result` (int, 1–6).
- **Logs:** The `otelslog` bridge replaces `slog.SetDefault(slog.New(slog.NewJSONHandler(...)))` with `slog.SetDefault(slog.New(otelslog.NewHandler(...)))`. All existing `slog.*` call sites are unchanged. When a span is active, the bridge automatically attaches `trace_id` and `span_id` to log records, enabling Grafana log-trace correlation.

---

## Code Changes

### New: `app/internal/telemetry/setup.go`

```go
func Setup(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error)
```

- Creates an OTLP gRPC exporter pointing to `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4317`, insecure)
- Initializes `TracerProvider`, `MeterProvider`, and `LoggerProvider` with a `resource` carrying `service.name`
- Registers all three providers globally: `otel.SetTracerProvider`, `otel.SetMeterProvider`, `global.SetLoggerProvider`
- Sets W3C TraceContext + Baggage composite propagator globally: `otel.SetTextMapPropagator`
- Replaces `slog` default handler with `otelslog.NewHandler(...)` backed by the LoggerProvider — the existing `slog.SetDefault(slog.New(slog.NewJSONHandler(...)))` line in each `main()` is removed; `telemetry.Setup` owns the slog default from this point forward
- Returns a single `shutdown` func that calls `Shutdown` on all three providers with a 5s deadline

No test file — wiring only; tested by the smoke test in `setup_test.go`.

### New: `app/internal/telemetry/setup_test.go`

One smoke test: calls `Setup` with endpoint `localhost:19999` (nothing listening), verifies it returns a non-nil shutdown func without error. The OTLP gRPC exporter connects lazily, so this confirms wiring compiles and runs without a live collector.

### Modified: `app/cmd/backend/main.go`

```diff
+ shutdown, err := telemetry.Setup(ctx, "backend")
+ defer shutdown(ctx)

- mux.HandleFunc("GET /roll", rollHandler)
+ mux.HandleFunc("GET /roll", rollHandler)   // handler gains counter (see below)

- Handler: middleware.Log(mux),
+ Handler: otelhttp.NewHandler(mux, "backend"),
```

`rollHandler` gains a `dice.rolls` counter increment:
```go
meter := otel.Meter("dice")
rollCounter, _ := meter.Int64Counter("dice.rolls")
// inside handler:
rollCounter.Add(r.Context(), 1, metric.WithAttributes(attribute.Int("result", n)))
```

The counter is initialized once in `main()` and passed to `rollHandler` as a closure parameter — `rollHandler(counter metric.Int64Counter) http.HandlerFunc` — consistent with the existing `rolldiceHandler(backendAddr string)` pattern.

### Modified: `app/cmd/frontend/main.go`

```diff
+ shutdown, err := telemetry.Setup(ctx, "frontend")
+ defer shutdown(ctx)

- var backendClient = &http.Client{Timeout: 10 * time.Second}
+ var backendClient = &http.Client{
+     Timeout:   10 * time.Second,
+     Transport: otelhttp.NewTransport(http.DefaultTransport),
+ }

- Handler: middleware.Log(mux),
+ Handler: otelhttp.NewHandler(mux, "frontend"),
```

### Deleted: `app/internal/middleware/`

`log.go` and `log_test.go` are removed. `otelhttp.NewHandler` subsumes the request-logging responsibility (it emits spans and metrics; structured log emission for each request comes from the slog bridge receiving spans). The import of `dice/internal/middleware` is removed from both `main.go` files.

---

## Dependencies Added

| Package | Purpose |
|---------|---------|
| `go.opentelemetry.io/otel` | Core API (tracer, meter, propagator) |
| `go.opentelemetry.io/otel/sdk` | SDK (TracerProvider, MeterProvider) |
| `go.opentelemetry.io/otel/sdk/log` | LoggerProvider |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` | Trace OTLP gRPC exporter |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` | Metric OTLP gRPC exporter |
| `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` | Log OTLP gRPC exporter |
| `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | HTTP server + client instrumentation |
| `go.opentelemetry.io/contrib/bridges/otelslog` | slog → OTel log bridge |

---

## Configuration

| Env var | Default | Effect |
|---------|---------|--------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector gRPC endpoint |
| `OTEL_SERVICE_NAME` | (set per-binary as `"frontend"` / `"backend"`) | service.name resource attribute |

---

## Testing

- `go test ./...` stays green after the change
- `internal/middleware/` tests are deleted with the package
- `internal/telemetry/setup_test.go` adds one smoke test (no live collector needed)
- `cmd/backend` and `cmd/frontend` handler tests are unchanged — no-op providers are safe

---

## Out of Scope

- Custom Grafana dashboards (the collector already routes to existing datasources)
- Sampling configuration (default: always-on, appropriate for local demo)
- Authentication / TLS to collector (collector runs insecure in kind)

---

## Success Criteria

1. `go test ./...` passes with no failures
2. `./scripts/run-apps.sh` starts both services
3. `curl localhost:8080/rolldice` returns 1–6
4. Grafana (localhost:3000) shows:
   - Tempo: distributed trace with two spans (frontend → backend)
   - Prometheus: `http_server_request_duration` and `dice_rolls_total` metrics
   - Loki: structured log lines with `trace_id` field matching Tempo trace IDs
