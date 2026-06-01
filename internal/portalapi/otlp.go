package portalapi

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// otlpSink exports product events as OTLP log records carrying a well-known
// `event.name` attribute. The application emits OTLP only — there is no
// backend-specific exporter compiled in; the collector routes `event.*`
// records to the analytics store and switches/adds backends in its own config.
type otlpSink struct {
	logger otellog.Logger
}

func (s otlpSink) Consume(ev Event) {
	var rec otellog.Record
	rec.SetTimestamp(ev.OccurredAt)
	rec.SetObservedTimestamp(ev.OccurredAt)
	rec.SetBody(otellog.StringValue(ev.EventName))
	rec.SetSeverity(otellog.SeverityInfo)

	attrs := make([]otellog.KeyValue, 0, len(ev.Attributes)+4)
	attrs = append(attrs,
		otellog.String("event.name", ev.EventName),
		otellog.String("event.tier", tierOf(ev.EventName)),
	)
	if ev.InstallID != "" {
		attrs = append(attrs, otellog.String("install_id", ev.InstallID))
	}
	if ev.WizardSessionID != "" {
		attrs = append(attrs, otellog.String("wizard_session_id", ev.WizardSessionID))
	}
	for k, v := range ev.Attributes {
		attrs = append(attrs, otellog.String(k, v))
	}
	rec.AddAttributes(attrs...)

	// Emit is best-effort; the batch processor buffers and the caller is the
	// async emitter goroutine, so this never touches a request path.
	s.logger.Emit(context.Background(), rec)
}

// newOTLPSink builds an OTLP log exporter + batch processor + provider and
// returns a sink plus the provider's shutdown func. Endpoint and transport
// options are read from the standard OTEL_EXPORTER_OTLP_* environment
// variables by the exporter itself. Returns (nil, nil, nil) when no endpoint
// is configured (OTLP export disabled, slog + store still run).
func newOTLPSink(ctx context.Context, build BuildInfo, endpoint string, logger *slog.Logger) (EventSink, func(context.Context) error, error) {
	if endpoint == "" {
		return nil, nil, nil
	}
	exporter, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("fdh-portal-api"),
		semconv.ServiceVersion(build.Version),
	))
	if err != nil {
		res = resource.Default()
	}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)
	logger.Info("telemetry: OTLP log export enabled", "endpoint", endpoint)
	return otlpSink{logger: provider.Logger("fdh-portal-api")}, provider.Shutdown, nil
}
