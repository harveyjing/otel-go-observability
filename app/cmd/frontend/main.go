package main

import (
	"context"
	"encoding/json"
	"errors"
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
			zap.L().Error("build request failed", zap.Any("ctx", ctx), zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "internal error")
		}
		resp, err := backendClient.Do(outReq)
		if err != nil {
			zap.L().Error("backend call failed", zap.Any("ctx", ctx), zap.Error(err), zap.String("backend", backendAddr))
			return echo.NewHTTPError(http.StatusBadGateway, "backend unavailable")
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			zap.L().Error("backend returned non-200", zap.Any("ctx", ctx), zap.Int("status", resp.StatusCode))
			return echo.NewHTTPError(http.StatusBadGateway, "backend error")
		}

		var result struct {
			Result int `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			zap.L().Error("decode failed", zap.Any("ctx", ctx), zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "invalid backend response")
		}

		return c.String(http.StatusOK, fmt.Sprintf("%d\n", result.Result))
	}
}

func main() {
	ctx := context.Background()
	shutdown, err := telemetry.Setup(ctx, "frontend")
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
	e.HidePort = true
	e.Use(otelecho.Middleware("frontend", otelecho.WithOnError(func(c echo.Context, err error) {
		if !c.Response().Committed {
			c.Error(err)
		}
	})))
	e.GET("/rolldice", rolldiceHandler(backendAddr))

	go func() {
		zap.L().Info("frontend starting", zap.String("port", port), zap.String("backend", backendAddr))
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
	zap.L().Info("frontend stopped")
}
