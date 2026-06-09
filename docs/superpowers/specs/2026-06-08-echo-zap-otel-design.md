# Echo + Zap OTel Instrumentation Design

**Date:** 2026-06-08
**Status:** Approved
**Goal:** Migrate the existing Go dice roller from `net/http` + `log/slog` to `labstack/echo` + `uber-go/zap`, preserving all three OTel signals (traces, metrics, logs) with the same collector pipeline.

---

## Context

The current instrumentation (`2026-06-05-otel-instrumentation-design.md`) is fully working:
- `otelhttp.NewHandler(mux, service)` → inbound spans + HTTP server metrics
- `otelhttp.NewTransport(http.DefaultTransport)` → outbound trace propagation
- `otelslog.NewHandler(serviceName)` → slog → OTel log bridge (trace_id/span_id auto-attached)

This spec replaces the HTTP framework and logging library while keeping `internal/telemetry/setup.go` and all OTel exporters/providers unchanged.

---

## Dependency Changes

**Add:**
| Package | Purpose |
|---------|---------|
| `github.com/labstack/echo/v4` | HTTP framework |
| `go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho` | Echo OTel middleware (spans + HTTP metrics) |
| `go.uber.org/zap` | Structured logging |
| `go.opentelemetry.io/contrib/bridges/otelzap` | zap → OTel log bridge |

**Remove:**
| Package | Reason |
|---------|--------|
| `go.opentelemetry.io/contrib/bridges/otelslog` | Replaced by otelzap bridge |

**Stays (frontend only):**
| Package | Reason |
|---------|--------|
| `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | `otelhttp.NewTransport` still wraps the outbound `http.Client` in `cmd/frontend` |

---

## Signal Flow (unchanged)

```
curl /rolldice
      │
      ▼
frontend :8080
  otelecho.Middleware("frontend")    ← inbound span + HTTP server metrics
  otelhttp.NewTransport(...)         ← outbound span (injects W3C traceparent)
      │
      ▼  (traceparent header propagated)
backend :8081
  otelecho.Middleware("backend")     ← inbound span (child of frontend span)
  dice.rolls counter                  ← custom metric unchanged
      │
      ▼
OTel Collector :4317 (OTLP gRPC) — unchanged
```

---

## Code Changes

### `internal/telemetry/setup.go`

Replace the slog bridge initialization with the zap tee setup. The function signature and all provider/exporter wiring stay the same.

```diff
-import "go.opentelemetry.io/contrib/bridges/otelslog"
+import (
+    "go.uber.org/zap"
+    "go.uber.org/zap/zapcore"
+    "go.opentelemetry.io/contrib/bridges/otelzap"
+)

 // after global.SetLoggerProvider(lp):
-slog.SetDefault(slog.New(otelslog.NewHandler(serviceName)))
+consoleCore := zapcore.NewCore(
+    zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
+    zapcore.AddSync(os.Stdout),
+    zap.InfoLevel,
+)
+otelCore := otelzap.NewCore(serviceName)
+logger := zap.New(zapcore.NewTee(consoleCore, otelCore), zap.AddCaller())
+zap.ReplaceGlobals(logger)
```

`Setup` now owns the global zap logger. Callers use `zap.L()` (or `zap.S()` for sugared).

### `cmd/backend/main.go`

```diff
-import "net/http"
+import "github.com/labstack/echo/v4"
-import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
+import "go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

-mux := http.NewServeMux()
-mux.HandleFunc("GET /roll", rollHandler(rollCounter))
-srv := &http.Server{
-    Addr:    ":" + port,
-    Handler: otelhttp.NewHandler(mux, "backend"),
-}
+e := echo.New()
+e.Use(otelecho.Middleware("backend"))
+e.GET("/roll", rollHandler(rollCounter))

 // server start:
-srv.ListenAndServe()
+e.Start(":" + port)
```

Handler signature changes from `http.HandlerFunc` to `echo.HandlerFunc`:

```diff
-func rollHandler(counter metric.Int64Counter) http.HandlerFunc {
-    return func(w http.ResponseWriter, r *http.Request) {
-        n := dice.Roll()
-        counter.Add(r.Context(), 1, ...)
-        json.NewEncoder(w).Encode(rollResponse{Result: n})
-        slog.ErrorContext(r.Context(), "encode response", "err", err)
-    }
-}
+func rollHandler(counter metric.Int64Counter) echo.HandlerFunc {
+    return func(c echo.Context) error {
+        n := dice.Roll()
+        counter.Add(c.Request().Context(), 1, ...)
+        return c.JSON(http.StatusOK, rollResponse{Result: n})
+    }
+}
```

Logging: replace `slog.ErrorContext(ctx, ...)` with `zap.L().ErrorContext(ctx, ...)` (zap v1.27+) throughout both services.

### `cmd/frontend/main.go`

Same Echo migration as backend. `otelhttp.NewTransport` on `backendClient` stays — it's transport-layer, not framework-specific.

```diff
+import "github.com/labstack/echo/v4"
+import "go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
 import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"  // kept for NewTransport

-mux := http.NewServeMux()
-mux.HandleFunc("GET /rolldice", rolldiceHandler(backendAddr))
-srv := &http.Server{
-    Addr:    ":" + port,
-    Handler: otelhttp.NewHandler(mux, "frontend"),
-}
+e := echo.New()
+e.Use(otelecho.Middleware("frontend"))
+e.GET("/rolldice", rolldiceHandler(backendAddr))
+e.Start(":" + port)
```

Handler:
```diff
-func rolldiceHandler(backendAddr string) http.HandlerFunc {
-    return func(w http.ResponseWriter, r *http.Request) { ... }
-}
+func rolldiceHandler(backendAddr string) echo.HandlerFunc {
+    return func(c echo.Context) error { ... }
+}
```

### Graceful shutdown

Echo exposes `e.Shutdown(ctx)` — replace `srv.Shutdown(ctx)` calls with this.

---

## Context-Aware Logging

Zap v1.27 added `Logger.InfoContext(ctx, msg, ...Field)` which populates `zapcore.Entry.Context`. The `otelzap.Core` reads this to extract the active span's `trace_id` and `span_id`, enabling Grafana log-trace correlation — same behavior as the slog bridge.

In handlers, always pass the request context:
```go
zap.L().InfoContext(c.Request().Context(), "rolling dice", zap.Int("result", n))
```

---

## Testing

- `go test ./...` stays green — handler tests use `httptest` or Echo's test helpers
- `internal/telemetry/setup_test.go` smoke test stays valid (no live collector needed)
- Echo handlers testable via `httptest.NewRecorder` + `echo.NewContext`

---

## Out of Scope

- Echo-specific middleware (CORS, rate limiting, auth)
- Zap log sampling configuration
- Changing the OTel collector pipeline or Grafana dashboards

---

## Success Criteria

1. `go test ./...` passes
2. `./scripts/run-apps.sh` starts both services
3. `curl localhost:8080/rolldice` returns 1–6
4. Grafana shows the same three signals as before:
   - Tempo: distributed trace, two spans
   - Prometheus: `http_server_request_duration`, `dice_rolls_total`
   - Loki: logs with `trace_id` matching Tempo
