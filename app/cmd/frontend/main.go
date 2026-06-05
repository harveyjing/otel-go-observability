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
