package telemetry

import (
	"context"
	"log/slog"
	"testing"
)

func TestSetup_returnsShutdown(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
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
