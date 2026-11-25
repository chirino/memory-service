package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestTrustedUserAssertionRunsBeforeIdentityRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.RateLimitIdentity = "1/1h,burst=1"
	cfg.APIKeys = map[string]string{"key-1": "trusted-client"}
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	limiter, err := NewRateLimiter(&cfg)
	require.NoError(t, err)

	router := gin.New()
	router.GET(
		"/v1/test",
		AuthMiddlewareWithAuthFailureRateLimiter(resolver, limiter),
		NewUserIDAsserter("trusted-client").HTTPMiddleware(),
		AuthenticatedRateLimitMiddleware(limiter),
		RequireUser(),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"user": GetUserID(c)}) },
	)

	request := func(userID string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		req.Header.Set("X-API-Key", "key-1")
		req.Header.Set(HeaderUserID, userID)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// The same authenticated client gets a distinct identity bucket for each
	// effective user, proving assertion ran before the rate limiter.
	require.Equal(t, http.StatusOK, request("alice"))
	require.Equal(t, http.StatusOK, request("bob"))
	require.Equal(t, http.StatusTooManyRequests, request("alice"))
}

func TestRateLimitErrorsEnrichOperationalEvent(t *testing.T) {
	classes := []RateLimitClass{
		RateLimitSource,
		RateLimitIdentity,
		RateLimitAuthFailure,
		RateLimitExpensive,
		RateLimitStreamOpen,
	}

	for _, class := range classes {
		t.Run(string(class)+"/http", func(t *testing.T) {
			event := operationevent.New("http GET /v1/test", operationevent.WithEmitter(func(string, operationevent.Level, operationevent.Snapshot) {}))
			request := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
			request = request.WithContext(operationevent.WithContext(request.Context(), event))
			response := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Request = request

			rejectHTTPRateLimited(ginContext, class, 1)

			require.Equal(t, http.StatusTooManyRequests, response.Code)
			require.Equal(t, string(class), event.Snapshot().RateLimiter)
		})

		t.Run(string(class)+"/grpc", func(t *testing.T) {
			event := operationevent.New("grpc /memory.v1.TestService/Test", operationevent.WithEmitter(func(string, operationevent.Level, operationevent.Snapshot) {}))
			ctx := operationevent.WithContext(context.Background(), event)

			err := grpcRateLimitError(ctx, class, 1)

			require.Equal(t, codes.ResourceExhausted, status.Code(err))
			require.Equal(t, string(class), event.Snapshot().RateLimiter)
		})
	}
}

// The attachment metadata lookup is a cheap read that clients poll while they
// recover the outcome of an upload, so it stays in the default identity limit.
// Uploads and content or signed-URL reads stay in the expensive class.
func TestAttachmentRoutesRateLimitClassification(t *testing.T) {
	cases := []struct {
		method, route string
		expensive     bool
	}{
		{http.MethodPost, "/v1/attachments", true},
		{http.MethodGet, "/v1/attachments/:id/download-url", true},
		{http.MethodGet, "/v1/attachments/:id/metadata", false},
		{http.MethodGet, "/v1/attachments/:id", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.expensive, isHTTPExpensive(tc.method, tc.route, ""), "%s %s", tc.method, tc.route)
	}
}
