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
