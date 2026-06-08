# OTel Instrumentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenTelemetry traces, metrics, and logs to the two-service Go dice roller, exporting all three signals via OTLP gRPC to `localhost:4317` (insecure), connecting to the already-running kind/Grafana stack.

**Architecture:** A shared `internal/telemetry` package initializes all three OTel providers (TracerProvider, MeterProvider, LoggerProvider) and registers them globally; both services call `telemetry.Setup(ctx, "service-name")` in `main()`. `otelhttp.NewHandler` replaces the custom `middleware.Log` wrapper on both services; `otelhttp.NewTransport` wraps the frontend's HTTP client for distributed trace propagation. The `otelslog` bridge routes existing `slog` calls through the LoggerProvider so log lines carry `trace_id`/`span_id`. The backend also increments a custom `dice.rolls` counter (attribute: `result` 1–6).

**Tech Stack:** Go 1.24, `go.opentelemetry.io/otel` v1.x, `otelhttp`, `otelslog` bridge, OTLP gRPC exporters, `log/slog`, `net/http`

---

## File Map

| Path | Action | Responsibility |
|------|--------|----------------|
| `app/go.mod` / `app/go.sum` | Modify | Add 9 OTel dependencies |
| `app/internal/telemetry/setup.go` | **Create** | `Setup(ctx, serviceName) (shutdown, error)` — all three providers |
| `app/internal/telemetry/setup_test.go` | **Create** | Smoke test: Setup returns non-nil shutdown without error |
| `app/cmd/backend/main.go` | Modify | Add telemetry.Setup, rollHandler→closure with counter, otelhttp.NewHandler |
| `app/cmd/backend/main_test.go` | Modify | Pass noop counter to rollHandler in all three test cases |
| `app/cmd/frontend/main.go` | Modify | Add telemetry.Setup, otelhttp.NewTransport on backendClient, otelhttp.NewHandler |
| `app/internal/middleware/log.go` | **Delete** | Superseded by otelhttp.NewHandler |
| `app/internal/middleware/log_test.go` | **Delete** | No longer needed |

---

## Task 1: Add OTel dependencies

**Files:**
- Modify: `app/go.mod`, `app/go.sum`

- [ ] **Step 1: Install all OTel packages**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app
  go get \
    go.opentelemetry.io/otel@latest \
    go.opentelemetry.io/otel/sdk@latest \
    go.opentelemetry.io/otel/sdk/log@latest \
    go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@latest \
    go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@latest \
    go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@latest \
    go.opentelemetry.io/otel/log/global@latest \
    go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@latest \
    go.opentelemetry.io/contrib/bridges/otelslog@latest
  go mod tidy
  ```

- [ ] **Step 2: Verify the module compiles with no code changes yet**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go build ./...
  ```

  Expected: no output (clean build).

- [ ] **Step 3: Commit**

  ```bash
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    add app/go.mod app/go.sum
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    commit -m "feat: add OpenTelemetry dependencies"
  ```

---

## Task 2: Create `internal/telemetry` package (TDD)

**Files:**
- Create: `app/internal/telemetry/setup_test.go`
- Create: `app/internal/telemetry/setup.go`

- [ ] **Step 1: Write the failing smoke test**

  Create `app/internal/telemetry/setup_test.go`:

  ```go
  package telemetry

  import (
  	"context"
  	"log/slog"
  	"testing"
  )

  func TestSetup_returnsShutdown(t *testing.T) {
  	original := slog.Default()
  	t.Cleanup(func() { slog.SetDefault(original) })
  	// point at a port with nothing listening — exporter connects lazily
  	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:19999")

  	ctx := context.Background()
  	shutdown, err := Setup(ctx, "test-service")
  	if err != nil {
  		t.Fatalf("Setup() error = %v", err)
  	}
  	if shutdown == nil {
  		t.Fatal("Setup() returned nil shutdown func")
  	}
  	_ = shutdown(ctx)
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./internal/telemetry/...
  ```

  Expected: compile error — `undefined: Setup`

- [ ] **Step 3: Implement Setup**

  Create `app/internal/telemetry/setup.go`:

  ```go
  package telemetry

  import (
  	"context"
  	"errors"
  	"fmt"
  	"log/slog"
  	"os"

  	"go.opentelemetry.io/contrib/bridges/otelslog"
  	"go.opentelemetry.io/otel"
  	"go.opentelemetry.io/otel/attribute"
  	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
  	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
  	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
  	"go.opentelemetry.io/otel/log/global"
  	"go.opentelemetry.io/otel/propagation"
  	sdklog "go.opentelemetry.io/otel/sdk/log"
  	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
  	"go.opentelemetry.io/otel/sdk/resource"
  	sdktrace "go.opentelemetry.io/otel/sdk/trace"
  )

  // Setup initialises all three OTel providers (traces, metrics, logs) against
  // OTEL_EXPORTER_OTLP_ENDPOINT (default localhost:4317, insecure gRPC), registers
  // them globally, and replaces the slog default handler with the OTel bridge so
  // every slog call carries trace_id/span_id when a span is active.
  // Call the returned shutdown func (with a deadline context) in main's defer.
  func Setup(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
  	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
  	if endpoint == "" {
  		endpoint = "localhost:4317"
  	}

  	res, err := resource.New(ctx,
  		resource.WithAttributes(attribute.String("service.name", serviceName)),
  	)
  	if err != nil {
  		return nil, fmt.Errorf("build resource: %w", err)
  	}

  	// --- Traces ---
  	traceExp, err := otlptracegrpc.New(ctx,
  		otlptracegrpc.WithEndpoint(endpoint),
  		otlptracegrpc.WithInsecure(),
  	)
  	if err != nil {
  		return nil, fmt.Errorf("trace exporter: %w", err)
  	}
  	tp := sdktrace.NewTracerProvider(
  		sdktrace.WithBatcher(traceExp),
  		sdktrace.WithResource(res),
  	)

  	// --- Metrics ---
  	metricExp, err := otlpmetricgrpc.New(ctx,
  		otlpmetricgrpc.WithEndpoint(endpoint),
  		otlpmetricgrpc.WithInsecure(),
  	)
  	if err != nil {
  		_ = tp.Shutdown(ctx)
  		return nil, fmt.Errorf("metric exporter: %w", err)
  	}
  	mp := sdkmetric.NewMeterProvider(
  		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
  		sdkmetric.WithResource(res),
  	)

  	// --- Logs ---
  	logExp, err := otlploggrpc.New(ctx,
  		otlploggrpc.WithEndpoint(endpoint),
  		otlploggrpc.WithInsecure(),
  	)
  	if err != nil {
  		_ = tp.Shutdown(ctx)
  		_ = mp.Shutdown(ctx)
  		return nil, fmt.Errorf("log exporter: %w", err)
  	}
  	lp := sdklog.NewLoggerProvider(
  		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
  		sdklog.WithResource(res),
  	)

  	// Register all three providers globally
  	otel.SetTracerProvider(tp)
  	otel.SetMeterProvider(mp)
  	global.SetLoggerProvider(lp)
  	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
  		propagation.TraceContext{},
  		propagation.Baggage{},
  	))

  	// Route slog through the OTel bridge — must come after SetLoggerProvider
  	slog.SetDefault(slog.New(otelslog.NewHandler(serviceName)))

  	return func(ctx context.Context) error {
  		return errors.Join(
  			tp.Shutdown(ctx),
  			mp.Shutdown(ctx),
  			lp.Shutdown(ctx),
  		)
  	}, nil
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./internal/telemetry/...
  ```

  Expected: `ok  dice/internal/telemetry`

- [ ] **Step 5: Commit**

  ```bash
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    add app/internal/telemetry/
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    commit -m "feat: add telemetry.Setup — three-signal OTel SDK init"
  ```

---

## Task 3: Instrument backend service

**Files:**
- Modify: `app/cmd/backend/main.go`
- Modify: `app/cmd/backend/main_test.go`

- [ ] **Step 1: Update `main_test.go` first (tests will fail until main.go is updated)**

  Replace the entire contents of `app/cmd/backend/main_test.go` with:

  ```go
  package main

  import (
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"testing"

  	"go.opentelemetry.io/otel"
  )

  func TestRollHandler_statusOK(t *testing.T) {
  	counter, _ := otel.Meter("test").Int64Counter("test")
  	req := httptest.NewRequest(http.MethodGet, "/roll", nil)
  	rw := httptest.NewRecorder()
  	rollHandler(counter)(rw, req)

  	if rw.Code != http.StatusOK {
  		t.Fatalf("status = %d, want 200", rw.Code)
  	}
  }

  func TestRollHandler_contentType(t *testing.T) {
  	counter, _ := otel.Meter("test").Int64Counter("test")
  	req := httptest.NewRequest(http.MethodGet, "/roll", nil)
  	rw := httptest.NewRecorder()
  	rollHandler(counter)(rw, req)

  	ct := rw.Header().Get("Content-Type")
  	if ct != "application/json" {
  		t.Fatalf("Content-Type = %q, want \"application/json\"", ct)
  	}
  }

  func TestRollHandler_resultInRange(t *testing.T) {
  	counter, _ := otel.Meter("test").Int64Counter("test")
  	for range 100 {
  		req := httptest.NewRequest(http.MethodGet, "/roll", nil)
  		rw := httptest.NewRecorder()
  		rollHandler(counter)(rw, req)

  		var resp rollResponse
  		if err := json.NewDecoder(rw.Body).Decode(&resp); err != nil {
  			t.Fatalf("decode: %v", err)
  		}
  		if resp.Result < 1 || resp.Result > 6 {
  			t.Fatalf("result = %d, want [1, 6]", resp.Result)
  		}
  	}
  }
  ```

- [ ] **Step 2: Run tests to confirm they fail**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./cmd/backend/...
  ```

  Expected: compile error — `rollHandler` takes wrong number of arguments.

- [ ] **Step 3: Replace `app/cmd/backend/main.go`**

  ```go
  package main

  import (
  	"context"
  	"encoding/json"
  	"log/slog"
  	"net/http"
  	"os"
  	"os/signal"
  	"syscall"
  	"time"

  	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
  	"go.opentelemetry.io/otel"
  	"go.opentelemetry.io/otel/attribute"
  	"go.opentelemetry.io/otel/metric"

  	"dice/internal/dice"
  	"dice/internal/telemetry"
  )

  type rollResponse struct {
  	Result int `json:"result"`
  }

  func rollHandler(counter metric.Int64Counter) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		n := dice.Roll()
  		counter.Add(r.Context(), 1, metric.WithAttributes(attribute.Int("result", n)))
  		w.Header().Set("Content-Type", "application/json")
  		if err := json.NewEncoder(w).Encode(rollResponse{Result: n}); err != nil {
  			slog.Error("encode response", "err", err)
  		}
  	}
  }

  func main() {
  	ctx := context.Background()
  	shutdown, err := telemetry.Setup(ctx, "backend")
  	if err != nil {
  		slog.Error("telemetry setup", "err", err)
  		os.Exit(1)
  	}
  	defer func() {
  		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  		defer cancel()
  		if err := shutdown(shutCtx); err != nil {
  			slog.Error("telemetry shutdown", "err", err)
  		}
  	}()

  	port := os.Getenv("BACKEND_PORT")
  	if port == "" {
  		port = "8081"
  	}

  	meter := otel.Meter("dice/backend")
  	rollCounter, err := meter.Int64Counter("dice.rolls",
  		metric.WithDescription("Number of dice rolls by face value"),
  	)
  	if err != nil {
  		slog.Error("create roll counter", "err", err)
  		os.Exit(1)
  	}

  	mux := http.NewServeMux()
  	mux.HandleFunc("GET /roll", rollHandler(rollCounter))

  	srv := &http.Server{
  		Addr:    ":" + port,
  		Handler: otelhttp.NewHandler(mux, "backend"),
  	}

  	go func() {
  		slog.Info("backend starting", "port", port)
  		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
  			slog.Error("listen error", "err", err)
  			os.Exit(1)
  		}
  	}()

  	quit := make(chan os.Signal, 1)
  	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
  	<-quit

  	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  	defer cancel()
  	if err := srv.Shutdown(shutCtx); err != nil {
  		slog.Error("shutdown error", "err", err)
  		os.Exit(1)
  	}
  	slog.Info("backend stopped")
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./cmd/backend/...
  ```

  Expected: `ok  dice/cmd/backend`

- [ ] **Step 5: Commit**

  ```bash
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    add app/cmd/backend/main.go app/cmd/backend/main_test.go
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    commit -m "feat: instrument backend with OTel traces, metrics, logs"
  ```

---

## Task 4: Instrument frontend service

**Files:**
- Modify: `app/cmd/frontend/main.go`

The existing `app/cmd/frontend/main_test.go` requires **no changes** — `otelhttp.NewTransport` is attached at package-level init; the handler closure and mock backend tests are unaffected.

- [ ] **Step 1: Verify the existing frontend tests currently pass**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./cmd/frontend/...
  ```

  Expected: `ok  dice/cmd/frontend`

- [ ] **Step 2: Replace `app/cmd/frontend/main.go`**

  ```go
  package main

  import (
  	"context"
  	"encoding/json"
  	"fmt"
  	"log/slog"
  	"net/http"
  	"os"
  	"os/signal"
  	"syscall"
  	"time"

  	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

  	"dice/internal/telemetry"
  )

  var backendClient = &http.Client{
  	Timeout:   10 * time.Second,
  	Transport: otelhttp.NewTransport(http.DefaultTransport),
  }

  func rolldiceHandler(backendAddr string) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		outReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, backendAddr+"/roll", nil)
  		if err != nil {
  			slog.Error("build request failed", "err", err)
  			http.Error(w, "internal error", http.StatusInternalServerError)
  			return
  		}
  		resp, err := backendClient.Do(outReq)
  		if err != nil {
  			slog.Error("backend call failed", "err", err, "backend", backendAddr)
  			http.Error(w, "backend unavailable", http.StatusBadGateway)
  			return
  		}
  		defer resp.Body.Close()

  		if resp.StatusCode != http.StatusOK {
  			slog.Error("backend returned non-200", "status", resp.StatusCode)
  			http.Error(w, "backend error", http.StatusBadGateway)
  			return
  		}

  		var result struct {
  			Result int `json:"result"`
  		}
  		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
  			slog.Error("decode failed", "err", err)
  			http.Error(w, "invalid backend response", http.StatusInternalServerError)
  			return
  		}

  		fmt.Fprintln(w, result.Result)
  	}
  }

  func main() {
  	ctx := context.Background()
  	shutdown, err := telemetry.Setup(ctx, "frontend")
  	if err != nil {
  		slog.Error("telemetry setup", "err", err)
  		os.Exit(1)
  	}
  	defer func() {
  		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  		defer cancel()
  		if err := shutdown(shutCtx); err != nil {
  			slog.Error("telemetry shutdown", "err", err)
  		}
  	}()

  	port := os.Getenv("FRONTEND_PORT")
  	if port == "" {
  		port = "8080"
  	}
  	backendAddr := os.Getenv("BACKEND_ADDR")
  	if backendAddr == "" {
  		backendAddr = "http://localhost:8081"
  	}

  	mux := http.NewServeMux()
  	mux.HandleFunc("GET /rolldice", rolldiceHandler(backendAddr))

  	srv := &http.Server{
  		Addr:    ":" + port,
  		Handler: otelhttp.NewHandler(mux, "frontend"),
  	}

  	go func() {
  		slog.Info("frontend starting", "port", port, "backend", backendAddr)
  		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
  			slog.Error("listen error", "err", err)
  			os.Exit(1)
  		}
  	}()

  	quit := make(chan os.Signal, 1)
  	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
  	<-quit

  	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  	defer cancel()
  	if err := srv.Shutdown(shutCtx); err != nil {
  		slog.Error("shutdown error", "err", err)
  		os.Exit(1)
  	}
  	slog.Info("frontend stopped")
  }
  ```

- [ ] **Step 3: Run all tests**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go test ./...
  ```

  Expected:
  ```
  ok  dice/cmd/backend
  ok  dice/cmd/frontend
  ok  dice/internal/dice
  ok  dice/internal/middleware
  ok  dice/internal/telemetry
  ```

  Note: `dice/internal/middleware` still passes here — it's on disk but no longer imported. It gets deleted in Task 5.

- [ ] **Step 4: Commit**

  ```bash
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    add app/cmd/frontend/main.go
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    commit -m "feat: instrument frontend with OTel traces and logs"
  ```

---

## Task 5: Delete middleware package

The `dice/internal/middleware` package is now unused — both services switched to `otelhttp.NewHandler`. Delete it and verify the build stays clean.

**Files:**
- Delete: `app/internal/middleware/log.go`
- Delete: `app/internal/middleware/log_test.go`

- [ ] **Step 1: Confirm middleware is no longer imported**

  ```bash
  grep -r "dice/internal/middleware" /Users/jingwilliam/workspace/altair/otel-go-observability/app/cmd/
  ```

  Expected: no output (nothing imports it).

- [ ] **Step 2: Delete the package**

  ```bash
  rm -rf /Users/jingwilliam/workspace/altair/otel-go-observability/app/internal/middleware
  ```

- [ ] **Step 3: Verify full build and all tests pass**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability/app && go build ./... && go test ./...
  ```

  Expected:
  ```
  ok  dice/cmd/backend
  ok  dice/cmd/frontend
  ok  dice/internal/dice
  ok  dice/internal/telemetry
  ```

- [ ] **Step 4: Commit**

  ```bash
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    add -A app/internal/middleware
  git -C /Users/jingwilliam/workspace/altair/otel-go-observability \
    commit -m "chore: remove middleware package (superseded by otelhttp)"
  ```

---

## Task 6: End-to-end smoke test

No code changes in this task — verify the full OTel pipeline works against the live kind cluster.

**Pre-condition:** The kind cluster must be running. If not, run:
```bash
cd /Users/jingwilliam/workspace/altair/otel-go-observability && ./scripts/bootstrap-cluster.sh
```

- [ ] **Step 1: Start both services**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability && ./scripts/run-apps.sh
  ```

  Expected output ends with:
  ```
  Services running:
    frontend: http://localhost:8080/rolldice  (PID ...)
    backend:  http://localhost:8081/roll       (PID ...)
  ```

- [ ] **Step 2: Generate a few requests**

  ```bash
  for i in $(seq 1 10); do curl -s localhost:8080/rolldice; done
  ```

  Expected: 10 integers, each 1–6.

- [ ] **Step 3: Verify traces in Tempo**

  Open Grafana at http://localhost:3000 (admin / prom-operator).

  Navigate to **Explore → Tempo (datasource)**.

  Run a TraceQL search:
  ```
  { resource.service.name = "frontend" }
  ```

  Expected: traces with two spans — one for `frontend` and a child for `backend`.

- [ ] **Step 4: Verify metrics in Prometheus**

  In Grafana Explore, switch datasource to **Prometheus**.

  Run:
  ```
  dice_rolls_total
  ```

  Expected: a counter with label `result` for each face value that was rolled.

  Also try:
  ```
  http_server_request_duration_seconds_count
  ```

  Expected: request counts for both frontend and backend.

- [ ] **Step 5: Verify logs in Loki**

  In Grafana Explore, switch datasource to **Loki**.

  Run:
  ```
  {service_name="frontend"} | json
  ```

  Expected: structured log lines. Click one that corresponds to a request.
  Verify the `trace_id` field is present and non-empty.

  Copy the `trace_id` value, switch to Tempo, and paste it in the trace ID search to confirm round-trip log-trace correlation.

- [ ] **Step 6: Stop services**

  ```bash
  cd /Users/jingwilliam/workspace/altair/otel-go-observability && ./scripts/teardown.sh
  ```

  Expected: `Done.`

---

## Verification Checklist

Before declaring complete:

- [ ] `go test ./...` passes with 4 `ok` lines (no middleware)
- [ ] `curl localhost:8080/rolldice` returns 1–6
- [ ] Tempo shows distributed trace: frontend span → backend child span
- [ ] Prometheus has `dice_rolls_total` counter with `result` attribute
- [ ] Loki log lines include `trace_id` field
- [ ] `teardown.sh` exits 0 cleanly
