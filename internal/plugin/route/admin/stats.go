package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
)

const (
	requestRateQuery       = `sum(rate(memory_service_requests_total[5m]))`
	errorRateQuery         = `sum(rate(memory_service_requests_total{status=~"5.."}[5m])) / sum(rate(memory_service_requests_total[5m])) * 100`
	latencyP95Query        = `histogram_quantile(0.95, sum(rate(memory_service_request_duration_seconds_bucket[5m])) by (le))`
	cacheHitRateQuery      = `sum(rate(memory_service_cache_hits_total[5m])) / (sum(rate(memory_service_cache_hits_total[5m])) + sum(rate(memory_service_cache_misses_total[5m]))) * 100`
	dbPoolUtilizationQuery = `sum(memory_service_db_pool_open_connections) / sum(memory_service_db_pool_max_connections) * 100`
	storeLatencyP95Query   = `histogram_quantile(0.95, sum(rate(memory_service_store_latency_seconds_bucket[5m])) by (le, operation))`
	storeThroughputQuery   = `sum(rate(memory_service_store_latency_seconds_count[5m])) by (operation)`
)

var errPrometheusNotConfigured = errors.New("prometheus not configured")

type prometheusStatsHandler struct {
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

type timeSeriesPoint struct {
	Timestamp string   `json:"timestamp"`
	Value     *float64 `json:"value"`
}

type timeSeriesResponse struct {
	Metric string            `json:"metric"`
	Unit   string            `json:"unit"`
	Data   []timeSeriesPoint `json:"data"`
}

type labeledSeries struct {
	Label string            `json:"label"`
	Data  []timeSeriesPoint `json:"data"`
}

type multiSeriesResponse struct {
	Metric string          `json:"metric"`
	Unit   string          `json:"unit"`
	Series []labeledSeries `json:"series"`
}

type prometheusRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []prometheusRangeResult `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
}

type prometheusRangeResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"`
}

func newPrometheusStatsHandler(cfg *config.Config) *prometheusStatsHandler {
	baseURL := ""
	if cfg != nil {
		baseURL = strings.TrimSpace(cfg.PrometheusURL)
	}
	return &prometheusStatsHandler{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		now: time.Now,
	}
}

func (h *prometheusStatsHandler) rangeHandler(promQL, metric, unit string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start, end, step := h.resolveRange(c)
		resp, err := h.queryRange(c.Request.Context(), promQL, start, end, step)
		if err != nil {
			h.writePrometheusError(c, err)
			return
		}
		c.JSON(http.StatusOK, convertToTimeSeries(resp, metric, unit))
	}
}

func (h *prometheusStatsHandler) multiSeriesHandler(promQL, metric, unit, labelKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start, end, step := h.resolveRange(c)
		resp, err := h.queryRange(c.Request.Context(), promQL, start, end, step)
		if err != nil {
			h.writePrometheusError(c, err)
			return
		}
		c.JSON(http.StatusOK, convertToMultiSeries(resp, metric, unit, labelKey))
	}
}

func (h *prometheusStatsHandler) resolveRange(c *gin.Context) (string, string, string) {
	start := strings.TrimSpace(c.Query("start"))
	end := strings.TrimSpace(c.Query("end"))
	step := strings.TrimSpace(c.DefaultQuery("step", "60s"))
	now := h.now().UTC()
	if start == "" {
		start = now.Add(-1 * time.Hour).Format(time.RFC3339)
	}
	if end == "" {
		end = now.Format(time.RFC3339)
	}
	if step == "" {
		step = "60s"
	}
	return start, end, step
}

func (h *prometheusStatsHandler) queryRange(ctx context.Context, promQL, start, end, step string) (*prometheusRangeResponse, error) {
	if h.baseURL == "" {
		return nil, errPrometheusNotConfigured
	}
	endpoint, err := url.Parse(strings.TrimRight(h.baseURL, "/") + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("invalid Prometheus URL: %w", err)
	}
	values := endpoint.Query()
	values.Set("query", promQL)
	values.Set("start", start)
	values.Set("end", end)
	values.Set("step", step)
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Prometheus request: %w", err)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not connect to Prometheus server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus query failed with status %d", resp.StatusCode)
	}

	var payload prometheusRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode Prometheus response: %w", err)
	}
	if !strings.EqualFold(payload.Status, "success") {
		msg := strings.TrimSpace(payload.Error)
		if msg == "" {
			msg = "prometheus query failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return &payload, nil
}

func (h *prometheusStatsHandler) writePrometheusError(c *gin.Context, err error) {
	if errors.Is(err, errPrometheusNotConfigured) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "Prometheus not configured",
			"code":  "prometheus_not_configured",
			"details": gin.H{
				"message": "Prometheus is not configured. Set memory-service.prometheus.url to enable admin stats.",
			},
		})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "Prometheus unavailable",
		"code":  "prometheus_unavailable",
		"details": gin.H{
			"message": err.Error(),
		},
	})
}

func convertToTimeSeries(prometheus *prometheusRangeResponse, metric, unit string) timeSeriesResponse {
	response := timeSeriesResponse{
		Metric: metric,
		Unit:   unit,
		Data:   []timeSeriesPoint{},
	}
	if prometheus == nil || len(prometheus.Data.Result) == 0 {
		return response
	}
	for _, raw := range prometheus.Data.Result[0].Values {
		point, ok := parsePrometheusPoint(raw)
		if ok {
			response.Data = append(response.Data, point)
		}
	}
	return response
}

func convertToMultiSeries(prometheus *prometheusRangeResponse, metric, unit, labelKey string) multiSeriesResponse {
	response := multiSeriesResponse{
		Metric: metric,
		Unit:   unit,
		Series: []labeledSeries{},
	}
	if prometheus == nil {
		return response
	}
	for _, result := range prometheus.Data.Result {
		label := "unknown"
		if value, ok := result.Metric[labelKey]; ok && strings.TrimSpace(value) != "" {
			label = value
		}
		series := labeledSeries{Label: label, Data: []timeSeriesPoint{}}
		for _, raw := range result.Values {
			point, ok := parsePrometheusPoint(raw)
			if ok {
				series.Data = append(series.Data, point)
			}
		}
		response.Series = append(response.Series, series)
	}
	return response
}

func parsePrometheusPoint(raw []any) (timeSeriesPoint, bool) {
	if len(raw) < 2 {
		return timeSeriesPoint{}, false
	}
	timestamp, ok := parsePrometheusTimestamp(raw[0])
	if !ok {
		return timeSeriesPoint{}, false
	}
	value, ok := parsePrometheusValue(raw[1])
	if !ok {
		return timeSeriesPoint{}, false
	}
	return timeSeriesPoint{
		Timestamp: timestamp.UTC().Format(time.RFC3339),
		Value:     value,
	}, true
}

func parsePrometheusTimestamp(v any) (time.Time, bool) {
	toFloat := func(raw any) (float64, bool) {
		switch value := raw.(type) {
		case float64:
			return value, true
		case json.Number:
			f, err := value.Float64()
			return f, err == nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err == nil {
				return f, true
			}
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return 0, false
			}
			return float64(parsed.Unix()), true
		default:
			return 0, false
		}
	}
	seconds, ok := toFloat(v)
	if !ok {
		return time.Time{}, false
	}
	sec, frac := math.Modf(seconds)
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), true
}

func parsePrometheusValue(v any) (*float64, bool) {
	switch value := v.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, true
		}
		out := value
		return &out, true
	case json.Number:
		f, err := value.Float64()
		if err != nil {
			return nil, false
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, true
		}
		return &f, true
	case string:
		s := strings.TrimSpace(value)
		switch s {
		case "NaN", "+Inf", "-Inf":
			return nil, true
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, false
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, true
		}
		return &f, true
	default:
		return nil, false
	}
}
