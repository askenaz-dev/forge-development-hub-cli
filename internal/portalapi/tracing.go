package portalapi

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// newTracerProvider initializes OTLP trace export and installs the global
// TracerProvider + W3C TraceContext propagator. It returns a shutdown func.
// When no endpoint is configured it returns (nil, nil) and the global no-op
// tracer is used — withTracing still extracts inbound context so logs stay
// correlated, but nothing is exported. Export is degradable: a failed init
// is reported by the caller and the portal keeps serving.
func newTracerProvider(ctx context.Context, build BuildInfo, endpoint string, logger *slog.Logger) (func(context.Context) error, error) {
	// Always install the propagator so inbound traceparent is honored even
	// without an exporter.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if endpoint == "" {
		return nil, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("fdh-portal-api"),
		semconv.ServiceVersion(build.Version),
	))
	if err != nil {
		res = resource.Default()
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	logger.Info("telemetry: OTLP trace export enabled", "endpoint", endpoint)
	return tp.Shutdown, nil
}

// withTracing extracts inbound W3C trace context and starts a server span per
// request. Because the span is started from the extracted context, an exported
// span carries the inbound trace id, keeping client → portal traces stitched.
func (s *Server) withTracing(next http.Handler) http.Handler {
	tracer := otel.Tracer("fdh-portal-api")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, routeLabel(r.URL.Path),
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(
			semconv.HTTPRequestMethodKey.String(r.Method),
			semconv.HTTPResponseStatusCode(rec.status),
		)
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}
