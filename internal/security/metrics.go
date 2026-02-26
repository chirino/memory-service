package security

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec

	// StoreLatency can be used by store implementations to record operation latency.
	StoreLatency *prometheus.HistogramVec

	CacheHitsTotal   prometheus.Counter
	CacheMissesTotal prometheus.Counter

	// DBPoolOpenConnections tracks the number of currently open database connections.
	DBPoolOpenConnections prometheus.Gauge

	// DBPoolMaxConnections tracks the configured maximum database connections.
	DBPoolMaxConnections prometheus.Gauge
)

var validLabelKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ParseMetricsLabels parses a comma-separated list of key=value pairs into
// Prometheus labels. Values support ${VAR} / $VAR environment variable expansion.
// Label values may not contain commas. Returns nil for an empty string.
func ParseMetricsLabels(s string) (prometheus.Labels, error) {
	s = os.Expand(s, os.Getenv)
	if s == "" {
		return nil, nil
	}
	labels := prometheus.Labels{}
	for _, pair := range strings.Split(s, ",") {
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid label %q: expected key=value", pair)
		}
		k, v := pair[:idx], pair[idx+1:]
		if !validLabelKey.MatchString(k) {
			return nil, fmt.Errorf("invalid label key %q: must match [a-zA-Z_][a-zA-Z0-9_]*", k)
		}
		labels[k] = v
	}
	return labels, nil
}

var initMetricsOnce sync.Once

// InitMetrics registers all Prometheus metrics with the given constant labels.
// Must be called before starting the HTTP server or any store/cache initialization
// that records metrics. Safe to call multiple times; only the first call registers.
func InitMetrics(constLabels prometheus.Labels) {
	initMetricsOnce.Do(func() {
		initMetricsInner(constLabels)
	})
}

func initMetricsInner(constLabels prometheus.Labels) {
	reg := prometheus.WrapRegistererWith(constLabels, prometheus.DefaultRegisterer)
	f := promauto.With(reg)

	httpRequestsTotal = f.NewCounterVec(
		prometheus.CounterOpts{
			Name: "memory_service_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "status"},
	)

	httpRequestDuration = f.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "memory_service_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	StoreLatency = f.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "memory_service_store_latency_seconds",
			Help:    "Store operation latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	CacheHitsTotal = f.NewCounter(prometheus.CounterOpts{
		Name: "memory_service_cache_hits_total",
		Help: "Total cache hits",
	})

	CacheMissesTotal = f.NewCounter(prometheus.CounterOpts{
		Name: "memory_service_cache_misses_total",
		Help: "Total cache misses",
	})

	DBPoolOpenConnections = f.NewGauge(prometheus.GaugeOpts{
		Name: "memory_service_db_pool_open_connections",
		Help: "Number of open database connections",
	})

	DBPoolMaxConnections = f.NewGauge(prometheus.GaugeOpts{
		Name: "memory_service_db_pool_max_connections",
		Help: "Maximum number of database connections",
	})
}

// MetricsMiddleware records HTTP request metrics for Prometheus.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if httpRequestsTotal == nil {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		duration := time.Since(start)

		httpRequestsTotal.WithLabelValues(c.Request.Method, strconv.Itoa(c.Writer.Status())).Inc()
		httpRequestDuration.WithLabelValues(c.Request.Method).Observe(duration.Seconds())
	}
}
