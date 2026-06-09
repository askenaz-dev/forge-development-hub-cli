package portalapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsRegistry holds the portal API's Prometheus collectors. The package
// owns its registry rather than using the default global, so multiple
// portal API instances in the same process (during tests) do not collide.
type metricsRegistry struct {
	reg               *prometheus.Registry
	requestDuration   *prometheus.HistogramVec
	requestsInFlight  prometheus.Gauge
	refreshTotal      *prometheus.CounterVec
	refreshDuration   prometheus.Histogram
	registryCacheSize prometheus.Gauge

	// --- Business / telemetry metrics (capability hub-usage-telemetry, task
	// 6.1). These feed both /metrics (scraped by the existing ServiceMonitor)
	// and the first-party admin observability surface. They carry ONLY coarse,
	// non-identifying labels — never an install_id or any identity.

	// telemetryEventsTotal counts ingested telemetry events by event type
	// (install|download|resolve|activation|feedback) and result (accepted|
	// dropped|rejected) — the ingest health signal.
	telemetryEventsTotal *prometheus.CounterVec
	// componentEventsTotal counts per-component install/download/resolve events
	// by (event, kind, name). Bounded cardinality: the catalog is small and the
	// labels are catalog identifiers, never user identity.
	componentEventsTotal *prometheus.CounterVec
	// telemetryStoreUp reflects store health (1 available, 0 degraded).
	telemetryStoreUp prometheus.Gauge
}

func newMetrics() *metricsRegistry {
	reg := prometheus.NewRegistry()

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "fdh_portal_api",
		Name:      "request_duration_seconds",
		Help:      "Duration of HTTP requests handled by the portal API.",
		Buckets:   prometheus.ExponentialBucketsRange(0.001, 10, 10),
	}, []string{"route", "method", "status"})

	requestsInFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "fdh_portal_api",
		Name:      "requests_in_flight",
		Help:      "Number of HTTP requests currently being served.",
	})

	refreshTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fdh_portal_api",
		Name:      "registry_refresh_total",
		Help:      "Number of registry refreshes attempted, labeled by result.",
	}, []string{"result"})

	refreshDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "fdh_portal_api",
		Name:      "registry_refresh_duration_seconds",
		Help:      "Time spent refreshing the registry cache.",
		Buckets:   prometheus.ExponentialBucketsRange(0.01, 60, 8),
	})

	registryCacheSize := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "fdh_portal_api",
		Name:      "registry_cache_size",
		Help:      "Number of skills in the in-memory snapshot.",
	})

	telemetryEventsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fdh_portal_api",
		Name:      "telemetry_events_total",
		Help:      "Telemetry ingest events, labeled by event type and result.",
	}, []string{"event", "result"})

	componentEventsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fdh_portal_api",
		Name:      "component_events_total",
		Help:      "Per-component install/download/resolve events (no identity labels).",
	}, []string{"event", "kind", "name"})

	telemetryStoreUp := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "fdh_portal_api",
		Name:      "telemetry_store_up",
		Help:      "Telemetry store health: 1 when a live database is reachable, else 0.",
	})

	reg.MustRegister(
		requestDuration, requestsInFlight,
		refreshTotal, refreshDuration, registryCacheSize,
		telemetryEventsTotal, componentEventsTotal, telemetryStoreUp,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &metricsRegistry{
		reg: reg, requestDuration: requestDuration, requestsInFlight: requestsInFlight,
		refreshTotal: refreshTotal, refreshDuration: refreshDuration,
		registryCacheSize:    registryCacheSize,
		telemetryEventsTotal: telemetryEventsTotal,
		componentEventsTotal: componentEventsTotal,
		telemetryStoreUp:     telemetryStoreUp,
	}
}

// recordIngest updates the business metrics for one ingested telemetry event.
// result is "accepted" | "dropped" | "rejected". Component-scoped events
// (install/download/resolve) also bump the per-component counter. Labels are
// coarse catalog identifiers only — NEVER an install_id or identity (design D4).
func (m *metricsRegistry) recordIngest(event, kind, name, result string) {
	if m == nil {
		return
	}
	if event == "" {
		event = "unknown"
	}
	m.telemetryEventsTotal.WithLabelValues(event, result).Inc()
	if result == "accepted" {
		switch event {
		case "install", "download", "resolve":
			if kind == "" {
				kind = "unknown"
			}
			if name == "" {
				name = "unknown"
			}
			m.componentEventsTotal.WithLabelValues(event, kind, name).Inc()
		}
	}
}

// setStoreUp reflects the telemetry store's health on the gauge.
func (m *metricsRegistry) setStoreUp(up bool) {
	if m == nil {
		return
	}
	if up {
		m.telemetryStoreUp.Set(1)
		return
	}
	m.telemetryStoreUp.Set(0)
}

// handler returns the http.Handler that exposes /metrics for Prometheus.
func (m *metricsRegistry) handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// observeRequest records duration for a single request.
func (m *metricsRegistry) observeRequest(route, method string, status int, dur time.Duration) {
	m.requestDuration.WithLabelValues(route, method, strconv.Itoa(status)).Observe(dur.Seconds())
}
