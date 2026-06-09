package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	goruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Setup initialises all three OTel providers (traces, metrics, logs) against
// OTEL_EXPORTER_OTLP_ENDPOINT (default localhost:4317, insecure gRPC), registers
// them globally, and replaces the zap global logger with an otelzap tee core so
// every zap.L() call carries trace_id/span_id when a span is active.
// Call the returned shutdown func (with a deadline context) in main's defer.
func Setup(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attribute.String("service.name", serviceName)),
	)
	if err != nil &&
		!errors.Is(err, resource.ErrPartialResource) &&
		!errors.Is(err, resource.ErrSchemaURLConflict) {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	// --- Traces ---
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	// --- Metrics ---
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	// --- Logs ---
	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		return nil, fmt.Errorf("log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)

	// Register all three providers globally
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	if err := goruntime.Start(goruntime.WithMeterProvider(mp)); err != nil {
		return nil, fmt.Errorf("start runtime metrics: %w", err)
	}
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Route zap through the OTel bridge — must come after SetLoggerProvider
	consoleCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(os.Stdout),
		zap.InfoLevel,
	)
	otelCore := otelzap.NewCore(serviceName)
	logger := zap.New(zapcore.NewTee(consoleCore, otelCore), zap.AddCaller())
	zap.ReplaceGlobals(logger)

	return func(ctx context.Context) error {
		return errors.Join(
			tp.Shutdown(ctx),
			mp.Shutdown(ctx),
			lp.Shutdown(ctx),
		)
	}, nil
}
