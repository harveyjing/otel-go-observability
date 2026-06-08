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
			slog.ErrorContext(r.Context(), "encode response", "err", err)
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
		metric.WithUnit("1"),
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
