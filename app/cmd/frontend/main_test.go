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
