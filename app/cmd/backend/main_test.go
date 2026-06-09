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
