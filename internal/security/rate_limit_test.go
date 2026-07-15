package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseRateLimitSpecRejectsInvalidValues(t *testing.T) {
	tests := []string{
		"",
		"0/1m,burst=1",
		"1/500ms,burst=1",
		"1/2h,burst=1",
		"1/1m,burst=0",
		"1/1m",
		"1/1m,capacity=1",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			_, err := parseRateLimitSpec(raw)
			require.Error(t, err)
		})
	}
}

func TestParseRateLimitSpecAcceptsExpectedGrammar(t *testing.T) {
	spec, err := parseRateLimitSpec("30/1m,burst=10")
	require.NoError(t, err)
	require.Equal(t, 30, spec.Tokens)
	require.Equal(t, time.Minute, spec.Every)
	require.Equal(t, 10, spec.Burst)
}

func TestRateLimitClassStateEvictsLeastRecentlyUsedBucket(t *testing.T) {
	state := newRateLimitClassState(rateLimitSpec{Tokens: 1, Every: time.Minute, Burst: 1})
	state.maxKeys = 2
	state.idleTTL = time.Hour
	start := time.Now()

	state.bucketLocked("first", start)
	state.bucketLocked("second", start.Add(time.Second))
	state.bucketLocked("first", start.Add(2*time.Second))
	state.bucketLocked("third", start.Add(3*time.Second))

	require.Contains(t, state.buckets, "first")
	require.NotContains(t, state.buckets, "second")
	require.Contains(t, state.buckets, "third")
	require.Equal(t, 2, state.recency.Len())
}

func TestRateLimitClassStatePrunesExpiredBucketsFromOldest(t *testing.T) {
	state := newRateLimitClassState(rateLimitSpec{Tokens: 1, Every: time.Minute, Burst: 1})
	state.maxKeys = 3
	state.idleTTL = time.Minute
	start := time.Now()

	state.bucketLocked("first", start)
	state.bucketLocked("second", start.Add(30*time.Second))
	state.bucketLocked("first", start.Add(45*time.Second))
	state.bucketLocked("third", start.Add(91*time.Second))

	require.Contains(t, state.buckets, "first")
	require.NotContains(t, state.buckets, "second")
	require.Contains(t, state.buckets, "third")
	require.Equal(t, 2, state.recency.Len())
}

func TestSourceRateLimitMiddlewareRejectsWithStableEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.RateLimitSource = "1/1h,burst=1"
	limiter, err := NewRateLimiter(&cfg)
	require.NoError(t, err)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.Use(SourceRateLimitMiddleware(limiter))
	router.GET("/v1/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.RemoteAddr = "192.0.2.10:12346"
	req.Header.Set(HeaderRequestID, "req-rate")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "3600", rec.Header().Get("Retry-After"))
	require.JSONEq(t, `{"code":"rate_limited","error":"rate limit exceeded","details":{"retryAfterSeconds":3600},"requestId":"req-rate"}`, rec.Body.String())
}

func TestAuthMiddlewareAppliesIdentityLimitAfterAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.RateLimitIdentity = "1/1h,burst=1"
	cfg.APIKeys = map[string]string{"key-1": "client-1"}
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	limiter, err := NewRateLimiter(&cfg)
	require.NoError(t, err)

	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/v1/test", AuthMiddlewareWithRateLimiter(resolver, limiter), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		req.Header.Set("X-API-Key", "key-1")
		req.Header.Set(HeaderRequestID, "req-identity")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if i == 0 {
			require.Equal(t, http.StatusOK, rec.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, rec.Code)
			require.JSONEq(t, `{"code":"rate_limited","error":"rate limit exceeded","details":{"retryAfterSeconds":3600},"requestId":"req-identity"}`, rec.Body.String())
		}
	}
}
