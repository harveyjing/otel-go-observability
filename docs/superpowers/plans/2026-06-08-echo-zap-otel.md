# Echo + Zap OTel Instrumentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the dice roller from `net/http` + `log/slog` to `labstack/echo` + `uber-go/zap` while preserving all three OTel signals (traces, metrics, logs) through the same OTLP collector pipeline.

**Architecture:** `otelecho.Middleware` replaces `otelhttp.NewHandler` for inbound span/metric instrumentation on both services; `otelhttp.NewTransport` stays on the frontend's `http.Client` for outbound W3C propagation; `otelzap.NewCore` teed with a console JSON core replaces `otelslog.NewHandler` in `telemetry.Setup`, with handlers calling `zap.L().InfoContext(ctx, ...)` so the bridge auto-attaches `trace_id`/`span_id`.

**Tech Stack:** `github.com/labstack/echo/v4`, `go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho`, `go.uber.org/zap` (≥ v1.27 for `InfoContext`), `go.opentelemetry.io/contrib/bridges/otelzap`

---

## File Map

| File | Action | What changes |
|------|--------|-------------|
| `app/go.mod` | Modify | Add echo, otelecho, zap, otelzap; remove otelslog |
| `app/internal/telemetry/setup.go` | Modify | Swap `otelslog.NewHandler` for `otelzap.NewCore` teed with console core; `zap.ReplaceGlobals` |
| `app/internal/telemetry/setup_test.go` | Modify | Save/restore zap global instead of slog default; remove slog import |
| `app/cmd/backend/main.go` | Modify | Replace stdlib mux+server with Echo; handler signature `http.HandlerFunc` → `echo.HandlerFunc`; `slog.*` → `zap.L().*Context` |
| `app/cmd/backend/main_test.go` | Modify | Use `echo.New().NewContext(req, rw)` instead of bare `rw` calls |
| `app/cmd/frontend/main.go` | Modify | Same Echo migration; keep `otelhttp.NewTransport` on client |
| `app/cmd/frontend/main_test.go` | Modify | Use `echo.New().NewContext(req, rw)` |

---

## Task 1: Add and remove dependencies

**Files:**
- Modify: `app/go.mod` (via `go get` / `go mod tidy`)

- [ ] **Step 1: Add new packages**

```bash
cd app
go get github.com/labstack/echo/v4
go get go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho
go get go.uber.org/zap@latest
go get go.opentelemetry.io/contrib/bridges/otelzap
```

- [ ] **Step 2: Tidy — remove unused otelslog transitive deps**

```bash
go mod tidy
```

- [ ] **Step 3: Verify both new and old OTel packages are present**

```bash
grep -E "echo|zap|otelslog|otelhttp" go.mod
```

Expected output includes `labstack/echo`, `zap`, `otelzap`, `otelhttp` (still needed for frontend transport), and **no** `bridges/otelslog`.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add echo, zap, otelzap; remove otelslog"
```

---

## Task 2: Migrate telemetry/setup.go

**Files:**
- Modify: `app/internal/telemetry/setup.go`
- Modify: `app/internal/telemetry/setup_test.go`

`telemetry.Setup` owns the global logger. After this task, all callers use `zap.L()` instead of `slog.*`.

- [ ] **Step 1: Update the test to save/restore the zap global**

Replace all of `app/internal/telemetry/setup_test.go`:

```go
package telemetry

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestSetup_returnsShutdown(t *testing.T) {
	original := zap.L()
	t.Cleanup(func() { zap.ReplaceGlobals(original) })
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

- [ ] **Step 2: Run the test — expect compile error (setup.go still uses slog)**

```bash
cd app && go test ./internal/telemetry/... -v -run TestSetup_returnsShutdown
```

Expected: compile error referencing `otelslog` or `slog`.

- [ ] **Step 3: Rewrite the slog bridge section of setup.go**

In `app/internal/telemetry/setup.go`, make these changes:

Replace the import block — remove `"log/slog"` and `"go.opentelemetry.io/contrib/bridges/otelslog"`, add:
```go
"os"

"go.opentelemetry.io/contrib/bridges/otelzap"
"go.uber.org/zap"
"go.uber.org/zap/zapcore"
```

Replace the slog line (currently `slog.SetDefault(slog.New(otelslog.NewHandler(serviceName)))`) with:

```go
consoleCore := zapcore.NewCore(
    zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
    zapcore.AddSync(os.Stdout),
    zap.InfoLevel,
)
otelCore := otelzap.NewCore(serviceName)
logger := zap.New(zapcore.NewTee(consoleCore, otelCore), zap.AddCaller())
zap.ReplaceGlobals(logger)
```

- [ ] **Step 4: Run the test — expect PASS**

```bash
cd app && go test ./internal/telemetry/... -v -run TestSetup_returnsShutdown
```

Expected:
```
--- PASS: TestSetup_returnsShutdown
PASS
```

- [ ] **Step 5: Run the full suite — expect failures only in cmd/ (not yet migrated)**

```bash
cd app && go test ./...
```

Expected: `internal/telemetry` and `internal/dice` PASS; `cmd/backend` and `cmd/frontend` fail to compile (still reference `slog`).

- [ ] **Step 6: Commit**

```bash
cd app
git add internal/telemetry/setup.go internal/telemetry/setup_test.go
git commit -m "feat: replace slog bridge with otelzap tee core in telemetry.Setup"
```

---

## Task 3: Migrate cmd/backend

**Files:**
- Modify: `app/cmd/backend/main.go`
- Modify: `app/cmd/backend/main_test.go`

- [ ] **Step 1: Rewrite the backend tests for Echo**

Replace all of `app/cmd/backend/main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
)

func newEchoContext(method, target string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, nil)
	rw := httptest.NewRecorder()
	return e.NewContext(req, rw), rw
}

func TestRollHandler_statusOK(t *testing.T) {
	counter, _ := otel.Meter("test").Int64Counter("test")
	c, rw := newEchoContext(http.MethodGet, "/roll")

	if err := rollHandler(counter)(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
}

func TestRollHandler_contentType(t *testing.T) {
	counter, _ := otel.Meter("test").Int64Counter("test")
	c, rw := newEchoContext(http.MethodGet, "/roll")

	if err := rollHandler(counter)(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	ct := rw.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("Content-Type = %q, want \"application/json\"", ct)
	}
}

func TestRollHandler_resultInRange(t *testing.T) {
	counter, _ := otel.Meter("test").Int64Counter("test")
	for range 100 {
		c, rw := newEchoContext(http.MethodGet, "/roll")
		if err := rollHandler(counter)(c); err != nil {
			t.Fatalf("handler error: %v", err)
		}
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

- [ ] **Step 2: Run backend tests — expect compile failure (main.go still uses net/http)**

```bash
cd app && go test ./cmd/backend/... -v
```

Expected: compile error — `rollHandler` still returns `http.HandlerFunc`, not `echo.HandlerFunc`.

- [ ] **Step 3: Rewrite cmd/backend/main.go**

Replace all of `app/cmd/backend/main.go`:

```go
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"dice/internal/dice"
	"dice/internal/telemetry"
)

type rollResponse struct {
	Result int `json:"result"`
}

func rollHandler(counter metric.Int64Counter) echo.HandlerFunc {
	return func(c echo.Context) error {
		n := dice.Roll()
		counter.Add(c.Request().Context(), 1, metric.WithAttributes(attribute.Int("result", n)))
		return c.JSON(http.StatusOK, rollResponse{Result: n})
	}
}

func main() {
	ctx := context.Background()
	shutdown, err := telemetry.Setup(ctx, "backend")
	if err != nil {
		zap.L().Error("telemetry setup", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutCtx); err != nil {
			zap.L().Error("telemetry shutdown", zap.Error(err))
		}
	}()

	port := os.Getenv("BACKEND_PORT")
	if port == "" {
		port = "8081"
	}

	meter := otel.Meter("dice/backend")
	rollCounter, err := meter.Int64Counter("dice.rolls",
		metric.WithDescription("Number of dice rolls by face value"),
		metric.WithUnit("1"),
	)
	if err != nil {
		zap.L().Error("create roll counter", zap.Error(err))
		os.Exit(1)
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(otelecho.Middleware("backend"))
	e.GET("/roll", rollHandler(rollCounter))

	go func() {
		zap.L().Info("backend starting", zap.String("port", port))
		if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
			zap.L().Error("listen error", zap.Error(err))
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		zap.L().Error("shutdown error", zap.Error(err))
		os.Exit(1)
	}
	zap.L().Info("backend stopped")
}
```

- [ ] **Step 4: Run backend tests — expect PASS**

```bash
cd app && go test ./cmd/backend/... -v
```

Expected:
```
--- PASS: TestRollHandler_statusOK
--- PASS: TestRollHandler_contentType
--- PASS: TestRollHandler_resultInRange
PASS
```

- [ ] **Step 5: Commit**

```bash
cd app
git add cmd/backend/main.go cmd/backend/main_test.go
git commit -m "feat: migrate backend to Echo + zap with otelecho middleware"
```

---

## Task 4: Migrate cmd/frontend

**Files:**
- Modify: `app/cmd/frontend/main.go`
- Modify: `app/cmd/frontend/main_test.go`

- [ ] **Step 1: Rewrite the frontend tests for Echo**

Replace all of `app/cmd/frontend/main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func newEchoContext(method, target string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, nil)
	rw := httptest.NewRecorder()
	return e.NewContext(req, rw), rw
}

func TestRolldiceHandler_returnsBackendResult(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Result int `json:"result"`
		}{Result: 4})
	}))
	defer backend.Close()

	c, rw := newEchoContext(http.MethodGet, "/rolldice")
	if err := rolldiceHandler(backend.URL)(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	body := strings.TrimSpace(rw.Body.String())
	n, err := strconv.Atoi(body)
	if err != nil {
		t.Fatalf("body %q is not an integer: %v", body, err)
	}
	if n != 4 {
		t.Fatalf("result = %d, want 4", n)
	}
}

func TestRolldiceHandler_backendDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	c, rw := newEchoContext(http.MethodGet, "/rolldice")
	// Echo returns the error to the framework rather than writing directly;
	// call the error handler to flush status into rw.
	if err := rolldiceHandler(dead.URL)(c); err != nil {
		echo.New().HTTPErrorHandler(err, c)
	}
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rw.Code)
	}
}
```

- [ ] **Step 2: Run frontend tests — expect compile failure**

```bash
cd app && go test ./cmd/frontend/... -v
```

Expected: compile error — `rolldiceHandler` still returns `http.HandlerFunc`.

- [ ] **Step 3: Rewrite cmd/frontend/main.go**

Replace all of `app/cmd/frontend/main.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"dice/internal/telemetry"
)

var backendClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: otelhttp.NewTransport(http.DefaultTransport),
}

func rolldiceHandler(backendAddr string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		outReq, err := http.NewRequestWithContext(ctx, http.MethodGet, backendAddr+"/roll", nil)
		if err != nil {
			zap.L().ErrorContext(ctx, "build request failed", zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "internal error")
		}
		resp, err := backendClient.Do(outReq)
		if err != nil {
			zap.L().ErrorContext(ctx, "backend call failed", zap.Error(err), zap.String("backend", backendAddr))
			return echo.NewHTTPError(http.StatusBadGateway, "backend unavailable")
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			zap.L().ErrorContext(ctx, "backend returned non-200", zap.Int("status", resp.StatusCode))
			return echo.NewHTTPError(http.StatusBadGateway, "backend error")
		}

		var result struct {
			Result int `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			zap.L().ErrorContext(ctx, "decode failed", zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "invalid backend response")
		}

		return c.String(http.StatusOK, fmt.Sprintf("%d\n", result.Result))
	}
}

func main() {
	ctx := context.Background()
	shutdown, err := telemetry.Setup(ctx, "frontend")
	if err != nil {
		zap.L().Error("telemetry setup", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutCtx); err != nil {
			zap.L().Error("telemetry shutdown", zap.Error(err))
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

	e := echo.New()
	e.HideBanner = true
	e.Use(otelecho.Middleware("frontend"))
	e.GET("/rolldice", rolldiceHandler(backendAddr))

	go func() {
		zap.L().Info("frontend starting", zap.String("port", port), zap.String("backend", backendAddr))
		if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
			zap.L().Error("listen error", zap.Error(err))
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		zap.L().Error("shutdown error", zap.Error(err))
		os.Exit(1)
	}
	zap.L().Info("frontend stopped")
}
```

- [ ] **Step 4: Run frontend tests — expect PASS**

```bash
cd app && go test ./cmd/frontend/... -v
```

Expected:
```
--- PASS: TestRolldiceHandler_returnsBackendResult
--- PASS: TestRolldiceHandler_backendDown
PASS
```

- [ ] **Step 5: Commit**

```bash
cd app
git add cmd/frontend/main.go cmd/frontend/main_test.go
git commit -m "feat: migrate frontend to Echo + zap with otelecho middleware"
```

---

## Task 5: Full verification

**Files:** none (read-only verification)

- [ ] **Step 1: Run the complete test suite**

```bash
cd app && go test ./... -v
```

Expected: all packages PASS, zero failures.

- [ ] **Step 2: Build both binaries**

```bash
cd app
go build -o /tmp/dice-backend ./cmd/backend
go build -o /tmp/dice-frontend ./cmd/frontend
echo "build OK"
```

Expected: `build OK`, no errors.

- [ ] **Step 3: Smoke-test the services end to end**

In one terminal:
```bash
./scripts/run-apps.sh
```

In another:
```bash
curl -s localhost:8080/rolldice
```

Expected: a single integer 1–6.

- [ ] **Step 4: Confirm zap JSON logs appear on stdout**

The `run-apps.sh` output from backend/frontend should show JSON lines like:
```json
{"level":"info","ts":1234567890,"caller":"backend/main.go:59","msg":"backend starting","port":"8081"}
```

- [ ] **Step 5: (Optional, requires running kind cluster) Verify Grafana signals**

```bash
./scripts/load.sh   # send a few hundred requests
```

Open Grafana at `localhost:3000` and confirm:
- **Tempo**: distributed trace with two spans (frontend → backend)
- **Prometheus**: `http_server_request_duration_milliseconds` and `dice_rolls_total` present
- **Loki**: log lines with `traceID` field matching Tempo trace IDs
