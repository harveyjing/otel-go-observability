# Go Dice Roller — Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a two-service Go HTTP dice roller (frontend + backend) with `log/slog` structured logging and no OTel instrumentation, as the clean baseline for a before/after OTel comparison.

**Architecture:** A `frontend` service (`:8080`) accepts `GET /rolldice` from clients and calls a `backend` service (`:8081`) at `GET /roll` via HTTP. The backend rolls a die using `internal/dice.Roll()` and returns JSON; the frontend echoes the integer as plain text. Both services share a logging middleware from `internal/middleware`. No external Go dependencies — stdlib only.

**Tech Stack:** Go 1.22+, `net/http`, `log/slog`, `math/rand/v2`, `httptest` (tests)

---

## File Map

| Path | Action | Responsibility |
|------|--------|----------------|
| `app/go.mod` | Create | Module declaration (`dice`, go 1.24) |
| `app/internal/dice/roll.go` | Create | `Roll() int` — random 1–6 |
| `app/internal/dice/roll_test.go` | Create | 1000-iteration range test |
| `app/internal/middleware/log.go` | Create | `Log(next http.Handler) http.Handler` — slog request logging |
| `app/internal/middleware/log_test.go` | Create | Status capture test via httptest |
| `app/cmd/backend/main.go` | Create | HTTP server on `:8081`, `GET /roll` handler |
| `app/cmd/backend/main_test.go` | Create | Handler unit test via httptest |
| `app/cmd/frontend/main.go` | Create | HTTP server on `:8080`, `GET /rolldice` handler |
| `app/cmd/frontend/main_test.go` | Create | Handler unit test with mock backend |
| `scripts/run-apps.sh` | Create | Build + start both binaries, write PIDs, wait for ready |
| `scripts/load.sh` | Create | Loop `curl /rolldice` every 0.5s |
| `scripts/teardown.sh` | Create | Kill app processes; optionally delete kind cluster |

---

## Task 1: Initialize Go module

**Files:**
- Create: `app/go.mod`

- [ ] **Step 1: Create the module file**

  Create `otel-go-observability/app/go.mod`:

  ```
  module dice

  go 1.24.1
  ```

- [ ] **Step 2: Verify the module parses**

  ```bash
  cd otel-go-observability/app && go env GOMOD
  ```

  Expected: absolute path ending in `app/go.mod`

- [ ] **Step 3: Commit**

  ```bash
  git add otel-go-observability/app/go.mod
  git commit -m "feat: initialize dice Go module"
  ```

---

## Task 2: Dice package (TDD)

**Files:**
- Create: `app/internal/dice/roll_test.go`
- Create: `app/internal/dice/roll.go`

- [ ] **Step 1: Write the failing test**

  Create `app/internal/dice/roll_test.go`:

  ```go
  package dice

  import "testing"

  func TestRoll(t *testing.T) {
      for range 1000 {
          got := Roll()
          if got < 1 || got > 6 {
              t.Fatalf("Roll() = %d, want value in [1, 6]", got)
          }
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd otel-go-observability/app && go test ./internal/dice/...
  ```

  Expected: compile error — `undefined: Roll`

- [ ] **Step 3: Implement Roll()**

  Create `app/internal/dice/roll.go`:

  ```go
  package dice

  import "math/rand/v2"

  func Roll() int {
      return rand.IntN(6) + 1
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  cd otel-go-observability/app && go test ./internal/dice/...
  ```

  Expected: `ok  dice/internal/dice`

- [ ] **Step 5: Commit**

  ```bash
  git add otel-go-observability/app/internal/dice/
  git commit -m "feat: add dice.Roll() returning random 1-6"
  ```

---

## Task 3: Logging middleware (TDD)

**Files:**
- Create: `app/internal/middleware/log_test.go`
- Create: `app/internal/middleware/log.go`

- [ ] **Step 1: Write the failing test**

  Create `app/internal/middleware/log_test.go`:

  ```go
  package middleware

  import (
      "net/http"
      "net/http/httptest"
      "testing"
  )

  func TestLog_passesStatusThrough(t *testing.T) {
      inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusTeapot)
      })
      handler := Log(inner)

      req := httptest.NewRequest(http.MethodGet, "/test", nil)
      rw := httptest.NewRecorder()
      handler.ServeHTTP(rw, req)

      if rw.Code != http.StatusTeapot {
          t.Fatalf("status = %d, want 418", rw.Code)
      }
  }

  func TestLog_defaultStatus200(t *testing.T) {
      inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          // WriteHeader never called — should default to 200
      })
      handler := Log(inner)

      req := httptest.NewRequest(http.MethodGet, "/test", nil)
      rw := httptest.NewRecorder()
      handler.ServeHTTP(rw, req)

      if rw.Code != http.StatusOK {
          t.Fatalf("status = %d, want 200", rw.Code)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd otel-go-observability/app && go test ./internal/middleware/...
  ```

  Expected: compile error — `undefined: Log`

- [ ] **Step 3: Implement the middleware**

  Create `app/internal/middleware/log.go`:

  ```go
  package middleware

  import (
      "log/slog"
      "net/http"
      "time"
  )

  type statusWriter struct {
      http.ResponseWriter
      status int
  }

  func (sw *statusWriter) WriteHeader(code int) {
      sw.status = code
      sw.ResponseWriter.WriteHeader(code)
  }

  func Log(next http.Handler) http.Handler {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          start := time.Now()
          sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
          next.ServeHTTP(sw, r)
          slog.Info("request",
              "method", r.Method,
              "path", r.URL.Path,
              "status", sw.status,
              "duration_ms", time.Since(start).Milliseconds(),
          )
      })
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd otel-go-observability/app && go test ./internal/middleware/...
  ```

  Expected: `ok  dice/internal/middleware`

- [ ] **Step 5: Commit**

  ```bash
  git add otel-go-observability/app/internal/middleware/
  git commit -m "feat: add request logging middleware"
  ```

---

## Task 4: Backend service (TDD)

**Files:**
- Create: `app/cmd/backend/main_test.go`
- Create: `app/cmd/backend/main.go`

- [ ] **Step 1: Write the failing test**

  Create `app/cmd/backend/main_test.go`:

  ```go
  package main

  import (
      "encoding/json"
      "net/http"
      "net/http/httptest"
      "testing"
  )

  func TestRollHandler_statusOK(t *testing.T) {
      req := httptest.NewRequest(http.MethodGet, "/roll", nil)
      rw := httptest.NewRecorder()
      rollHandler(rw, req)

      if rw.Code != http.StatusOK {
          t.Fatalf("status = %d, want 200", rw.Code)
      }
  }

  func TestRollHandler_contentType(t *testing.T) {
      req := httptest.NewRequest(http.MethodGet, "/roll", nil)
      rw := httptest.NewRecorder()
      rollHandler(rw, req)

      ct := rw.Header().Get("Content-Type")
      if ct != "application/json" {
          t.Fatalf("Content-Type = %q, want \"application/json\"", ct)
      }
  }

  func TestRollHandler_resultInRange(t *testing.T) {
      for range 100 {
          req := httptest.NewRequest(http.MethodGet, "/roll", nil)
          rw := httptest.NewRecorder()
          rollHandler(rw, req)

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

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd otel-go-observability/app && go test ./cmd/backend/...
  ```

  Expected: compile error — `undefined: rollHandler`, `undefined: rollResponse`

- [ ] **Step 3: Implement backend**

  Create `app/cmd/backend/main.go`:

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

      "dice/internal/dice"
      "dice/internal/middleware"
  )

  type rollResponse struct {
      Result int `json:"result"`
  }

  func rollHandler(w http.ResponseWriter, r *http.Request) {
      w.Header().Set("Content-Type", "application/json")
      json.NewEncoder(w).Encode(rollResponse{Result: dice.Roll()})
  }

  func main() {
      slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

      port := os.Getenv("BACKEND_PORT")
      if port == "" {
          port = "8081"
      }

      mux := http.NewServeMux()
      mux.HandleFunc("GET /roll", rollHandler)

      srv := &http.Server{
          Addr:    ":" + port,
          Handler: middleware.Log(mux),
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

      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      defer cancel()
      if err := srv.Shutdown(ctx); err != nil {
          slog.Error("shutdown error", "err", err)
          os.Exit(1)
      }
      slog.Info("backend stopped")
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd otel-go-observability/app && go test ./cmd/backend/...
  ```

  Expected: `ok  dice/cmd/backend`

- [ ] **Step 5: Commit**

  ```bash
  git add otel-go-observability/app/cmd/backend/
  git commit -m "feat: add backend /roll HTTP service"
  ```

---

## Task 5: Frontend service (TDD)

**Files:**
- Create: `app/cmd/frontend/main_test.go`
- Create: `app/cmd/frontend/main.go`

- [ ] **Step 1: Write the failing test**

  Create `app/cmd/frontend/main_test.go`:

  ```go
  package main

  import (
      "encoding/json"
      "net/http"
      "net/http/httptest"
      "strconv"
      "strings"
      "testing"
  )

  func TestRolldiceHandler_returnsBackendResult(t *testing.T) {
      backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.Header().Set("Content-Type", "application/json")
          json.NewEncoder(w).Encode(struct {
              Result int `json:"result"`
          }{Result: 4})
      }))
      defer backend.Close()

      req := httptest.NewRequest(http.MethodGet, "/rolldice", nil)
      rw := httptest.NewRecorder()
      rolldiceHandler(backend.URL)(rw, req)

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
      req := httptest.NewRequest(http.MethodGet, "/rolldice", nil)
      rw := httptest.NewRecorder()
      rolldiceHandler("http://localhost:19999")(rw, req) // nothing listening

      if rw.Code != http.StatusBadGateway {
          t.Fatalf("status = %d, want 502", rw.Code)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd otel-go-observability/app && go test ./cmd/frontend/...
  ```

  Expected: compile error — `undefined: rolldiceHandler`

- [ ] **Step 3: Implement frontend**

  Create `app/cmd/frontend/main.go`:

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

      "dice/internal/middleware"
  )

  var backendClient = &http.Client{Timeout: 10 * time.Second}

  func rolldiceHandler(backendAddr string) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          resp, err := backendClient.Get(backendAddr + "/roll")
          if err != nil {
              slog.Error("backend call failed", "err", err, "backend", backendAddr)
              http.Error(w, "backend unavailable", http.StatusBadGateway)
              return
          }
          defer resp.Body.Close()

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
      slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

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
          Handler: middleware.Log(mux),
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

      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      defer cancel()
      if err := srv.Shutdown(ctx); err != nil {
          slog.Error("shutdown error", "err", err)
          os.Exit(1)
      }
      slog.Info("frontend stopped")
  }
  ```

- [ ] **Step 4: Run all tests**

  ```bash
  cd otel-go-observability/app && go test ./...
  ```

  Expected:
  ```
  ok  dice/cmd/backend
  ok  dice/cmd/frontend
  ok  dice/internal/dice
  ok  dice/internal/middleware
  ```

- [ ] **Step 5: Commit**

  ```bash
  git add otel-go-observability/app/cmd/frontend/
  git commit -m "feat: add frontend /rolldice HTTP service"
  ```

---

## Task 6: Supporting scripts and smoke test

**Files:**
- Create: `scripts/run-apps.sh`
- Create: `scripts/load.sh`
- Create: `scripts/teardown.sh`

- [ ] **Step 1: Create run-apps.sh**

  Create `otel-go-observability/scripts/run-apps.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail

  HERE="$(cd "$(dirname "$0")/.." && pwd)"
  APP="$HERE/app"

  echo "==> Building binaries"
  (cd "$APP" && go build -o /tmp/dice-backend ./cmd/backend)
  (cd "$APP" && go build -o /tmp/dice-frontend ./cmd/frontend)

  echo "==> Starting backend (:8081)"
  /tmp/dice-backend &
  BACKEND_PID=$!

  echo "==> Starting frontend (:8080)"
  /tmp/dice-frontend &
  FRONTEND_PID=$!

  printf 'BACKEND_PID=%s\nFRONTEND_PID=%s\n' "$BACKEND_PID" "$FRONTEND_PID" > /tmp/dice-pids

  wait_for() {
      local url=$1 name=$2
      for i in $(seq 1 10); do
          if curl -sf "$url" >/dev/null 2>&1; then
              echo "  $name ready"
              return 0
          fi
          sleep 0.5
      done
      echo "  WARNING: $name did not become ready in time"
  }

  echo "==> Waiting for services"
  wait_for "http://localhost:8081/roll"      "backend"
  wait_for "http://localhost:8080/rolldice"  "frontend"

  echo ""
  echo "Services running:"
  echo "  frontend: http://localhost:8080/rolldice  (PID $FRONTEND_PID)"
  echo "  backend:  http://localhost:8081/roll       (PID $BACKEND_PID)"
  echo ""
  echo "Test:  curl localhost:8080/rolldice"
  echo "Load:  ./scripts/load.sh"
  echo "Stop:  ./scripts/teardown.sh"
  ```

- [ ] **Step 2: Create load.sh**

  Create `otel-go-observability/scripts/load.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail

  FRONTEND="${FRONTEND_ADDR:-http://localhost:8080}"

  echo "Generating load against $FRONTEND/rolldice (Ctrl-C to stop)"
  while true; do
      result=$(curl -sf "$FRONTEND/rolldice" || echo "ERROR")
      echo "roll: $result"
      sleep 0.5
  done
  ```

- [ ] **Step 3: Create teardown.sh**

  Create `otel-go-observability/scripts/teardown.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail

  echo "==> Stopping app processes"
  if [ -f /tmp/dice-pids ]; then
      # shellcheck source=/dev/null
      source /tmp/dice-pids
      kill "${BACKEND_PID:-}" "${FRONTEND_PID:-}" 2>/dev/null || true
      rm /tmp/dice-pids
      echo "  Stopped (PIDs from /tmp/dice-pids)"
  else
      pkill -f dice-backend  2>/dev/null || true
      pkill -f dice-frontend 2>/dev/null || true
      echo "  Stopped (matched by name)"
  fi

  if [[ "${1:-}" == "--cluster" ]]; then
      echo "==> Deleting kind cluster otelpoc"
      kind delete cluster --name otelpoc
  else
      echo "  Tip: pass --cluster to also delete the kind cluster"
  fi

  echo "Done."
  ```

- [ ] **Step 4: Make scripts executable**

  ```bash
  chmod +x otel-go-observability/scripts/run-apps.sh \
           otel-go-observability/scripts/load.sh \
           otel-go-observability/scripts/teardown.sh
  ```

- [ ] **Step 5: Smoke test**

  Start the services:
  ```bash
  cd otel-go-observability && ./scripts/run-apps.sh
  ```

  Expected output ends with:
  ```
  Services running:
    frontend: http://localhost:8080/rolldice  (PID ...)
    backend:  http://localhost:8081/roll       (PID ...)
  ```

  Hit the endpoint:
  ```bash
  curl -s localhost:8080/rolldice
  ```

  Expected: a single integer between 1 and 6 (e.g., `3`)

  Check logs from frontend process — should contain JSON like:
  ```json
  {"time":"...","level":"INFO","msg":"request","method":"GET","path":"/rolldice","status":200,"duration_ms":1}
  ```

  Stop services:
  ```bash
  cd otel-go-observability && ./scripts/teardown.sh
  ```

  Expected: `Done.` with no errors

- [ ] **Step 6: Commit**

  ```bash
  git add otel-go-observability/scripts/
  git commit -m "feat: add run-apps, load, and teardown scripts"
  ```

---

## Verification Checklist

Before declaring this complete, confirm:

- [ ] `go test ./...` passes with 4 `ok` lines (backend, frontend, dice, middleware)
- [ ] `curl localhost:8080/rolldice` returns a number 1–6
- [ ] Both services emit JSON logs to stdout on each request
- [ ] `teardown.sh` exits 0 and leaves no zombie processes (`ps aux | grep dice`)
