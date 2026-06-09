package main

import (
	"context"
	"errors"
	"fmt"
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

// Context-aware logging with otelzap:
// zap v1.28.0 does NOT have InfoContext/ErrorContext methods on *zap.Logger.
// To attach a request context for trace correlation when using the otelzap bridge,
// pass the context as a zap field using zap.Any("ctx", ctx) — otelzap's core.go
// intercepts any field whose Interface value satisfies context.Context and uses it
// as the emit context, so the active span's trace/span IDs are propagated to the
// OTel log record automatically. Example in a handler:
//
//	ctx := c.Request().Context()
//	zap.L().Info("dice rolled", zap.Any("ctx", ctx), zap.Int("result", n))

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
		fmt.Fprintf(os.Stderr, "telemetry setup failed: %v\n", err)
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
	e.HidePort = true
	e.Use(otelecho.Middleware("backend", otelecho.WithOnError(func(c echo.Context, err error) {
		if !c.Response().Committed {
			c.Error(err)
		}
	})))
	e.GET("/roll", rollHandler(rollCounter))

	go func() {
		zap.L().Info("backend starting", zap.String("port", port))
		if err := e.Start(":" + port); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
