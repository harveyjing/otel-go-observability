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
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // port is now closed; guaranteed not listening

	req := httptest.NewRequest(http.MethodGet, "/rolldice", nil)
	rw := httptest.NewRecorder()
	rolldiceHandler(dead.URL)(rw, req)

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rw.Code)
	}
}
