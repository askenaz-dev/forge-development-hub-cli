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

	reg.MustRegister(
		requestDuration, requestsInFlight,
		refreshTotal, refreshDuration, registryCacheSize,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &metricsRegistry{
		reg: reg, requestDuration: requestDuration, requestsInFlight: requestsInFlight,
		refreshTotal: refreshTotal, refreshDuration: refreshDuration,
		registryCacheSize: registryCacheSize,
	}
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
