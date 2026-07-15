package security

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	rateLimitModeLocal = "local"
	rateLimitModeOff   = "off"

	rateLimitIdleTTL = 15 * time.Minute
	rateLimitMaxKeys = 100000
)

// RateLimitClass is the bounded metric label and bucket namespace for one limiter class.
type RateLimitClass string

const (
	RateLimitSource      RateLimitClass = "source"
	RateLimitIdentity    RateLimitClass = "identity"
	RateLimitAuthFailure RateLimitClass = "auth_failure"
	RateLimitExpensive   RateLimitClass = "expensive"
	RateLimitStreamOpen  RateLimitClass = "stream_open"
)

type rateLimitSpec struct {
	Tokens int
	Every  time.Duration
	Burst  int
}

// RateLimiter enforces process-local token buckets for main-listener HTTP and gRPC traffic.
type RateLimiter struct {
	enabled bool
	classes map[RateLimitClass]*rateLimitClassState
}

type rateLimitClassState struct {
	spec    rateLimitSpec
	mu      sync.Mutex
	buckets map[string]*rateLimitBucket
	recency list.List
	idleTTL time.Duration
	maxKeys int
}

type rateLimitBucket struct {
	limiter        *rate.Limiter
	lastSeen       time.Time
	recencyElement *list.Element
}

type rateLimitDecision struct {
	Allowed           bool
	RetryAfterSeconds int
}

// ValidateRateLimitConfig validates the operator-facing rate-limit flags.
func ValidateRateLimitConfig(cfg *config.Config) error {
	_, err := NewRateLimiter(cfg)
	return err
}

// NewRateLimiter builds a process-local limiter from config. It returns a disabled limiter when mode=off.
func NewRateLimiter(cfg *config.Config) (*RateLimiter, error) {
	if cfg == nil {
		return nil, fmt.Errorf("missing rate limit config")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.RateLimitMode))
	if mode == "" {
		mode = rateLimitModeLocal
	}
	if mode == rateLimitModeOff {
		log.Warn("process-local rate limiting is disabled", "reason", "rate_limits_off")
		recordUnsafeConfig("rate_limits_off")
		return &RateLimiter{enabled: false}, nil
	}
	if mode != rateLimitModeLocal {
		return nil, fmt.Errorf("MEMORY_SERVICE_RATE_LIMIT_MODE must be local or off")
	}

	specs := map[RateLimitClass]string{
		RateLimitSource:      cfg.RateLimitSource,
		RateLimitIdentity:    cfg.RateLimitIdentity,
		RateLimitAuthFailure: cfg.RateLimitAuthFailure,
		RateLimitExpensive:   cfg.RateLimitExpensive,
		RateLimitStreamOpen:  cfg.RateLimitStreamOpen,
	}
	limiter := &RateLimiter{
		enabled: true,
		classes: make(map[RateLimitClass]*rateLimitClassState, len(specs)),
	}
	for class, raw := range specs {
		spec, err := parseRateLimitSpec(raw)
		if err != nil {
			return nil, fmt.Errorf("MEMORY_SERVICE_RATE_LIMIT_%s: %w", envClassName(class), err)
		}
		limiter.classes[class] = newRateLimitClassState(spec)
	}
	return limiter, nil
}

func envClassName(class RateLimitClass) string {
	switch class {
	case RateLimitAuthFailure:
		return "AUTH_FAILURE"
	case RateLimitStreamOpen:
		return "STREAM_OPEN"
	default:
		return strings.ToUpper(string(class))
	}
}

func parseRateLimitSpec(raw string) (rateLimitSpec, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return rateLimitSpec{}, errors.New("must not be empty")
	}
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return rateLimitSpec{}, errors.New("must use <tokens>/<duration>,burst=<tokens>")
	}
	ratePart := strings.TrimSpace(parts[0])
	burstPart := strings.TrimSpace(parts[1])
	slash := strings.IndexByte(ratePart, '/')
	if slash <= 0 || slash == len(ratePart)-1 {
		return rateLimitSpec{}, errors.New("must use <tokens>/<duration>,burst=<tokens>")
	}
	tokens, err := strconv.Atoi(ratePart[:slash])
	if err != nil || tokens <= 0 {
		return rateLimitSpec{}, errors.New("tokens must be positive")
	}
	every, err := time.ParseDuration(ratePart[slash+1:])
	if err != nil {
		return rateLimitSpec{}, fmt.Errorf("invalid duration: %w", err)
	}
	if every < time.Second || every > time.Hour {
		return rateLimitSpec{}, errors.New("duration must be between 1s and 1h")
	}
	if !strings.HasPrefix(burstPart, "burst=") {
		return rateLimitSpec{}, errors.New("burst must use burst=<tokens>")
	}
	burst, err := strconv.Atoi(strings.TrimPrefix(burstPart, "burst="))
	if err != nil || burst <= 0 {
		return rateLimitSpec{}, errors.New("burst must be positive")
	}
	return rateLimitSpec{Tokens: tokens, Every: every, Burst: burst}, nil
}

func (l *RateLimiter) allow(class RateLimitClass, key string) rateLimitDecision {
	if l == nil || !l.enabled {
		return rateLimitDecision{Allowed: true}
	}
	state := l.classes[class]
	if state == nil {
		return rateLimitDecision{Allowed: true}
	}
	if strings.TrimSpace(key) == "" {
		key = "unknown"
	}
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	bucket := state.bucketLocked(key, now)
	if bucket.limiter.AllowN(now, 1) {
		recordRateLimit(class, "accepted")
		return rateLimitDecision{Allowed: true}
	}
	reservation := bucket.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		recordRateLimit(class, "rejected")
		return rateLimitDecision{Allowed: false, RetryAfterSeconds: 1}
	}
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)
	recordRateLimit(class, "rejected")
	return rateLimitDecision{Allowed: false, RetryAfterSeconds: retryAfterSeconds(delay)}
}

func (l *RateLimiter) available(class RateLimitClass, key string) rateLimitDecision {
	if l == nil || !l.enabled {
		return rateLimitDecision{Allowed: true}
	}
	state := l.classes[class]
	if state == nil {
		return rateLimitDecision{Allowed: true}
	}
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	bucket := state.bucketLocked(key, now)
	if bucket.limiter.TokensAt(now) >= 1 {
		return rateLimitDecision{Allowed: true}
	}
	reservation := bucket.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return rateLimitDecision{Allowed: false, RetryAfterSeconds: 1}
	}
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)
	return rateLimitDecision{Allowed: false, RetryAfterSeconds: retryAfterSeconds(delay)}
}

func newRateLimitClassState(spec rateLimitSpec) *rateLimitClassState {
	return &rateLimitClassState{
		spec:    spec,
		buckets: map[string]*rateLimitBucket{},
		idleTTL: rateLimitIdleTTL,
		maxKeys: rateLimitMaxKeys,
	}
}

func (s *rateLimitClassState) bucketLocked(key string, now time.Time) *rateLimitBucket {
	s.pruneExpiredLocked(now)
	if bucket := s.buckets[key]; bucket != nil {
		bucket.lastSeen = now
		s.recency.MoveToBack(bucket.recencyElement)
		return bucket
	}
	if len(s.buckets) >= s.maxKeys {
		s.removeOldestLocked()
	}
	bucket := &rateLimitBucket{
		limiter:        rate.NewLimiter(rate.Limit(float64(s.spec.Tokens)/s.spec.Every.Seconds()), s.spec.Burst),
		lastSeen:       now,
		recencyElement: s.recency.PushBack(key),
	}
	s.buckets[key] = bucket
	return bucket
}

func (s *rateLimitClassState) pruneExpiredLocked(now time.Time) {
	for oldest := s.recency.Front(); oldest != nil; oldest = s.recency.Front() {
		key := oldest.Value.(string)
		bucket := s.buckets[key]
		if now.Sub(bucket.lastSeen) <= s.idleTTL {
			return
		}
		s.removeOldestLocked()
	}
}

func (s *rateLimitClassState) removeOldestLocked() {
	oldest := s.recency.Front()
	if oldest == nil {
		return
	}
	delete(s.buckets, oldest.Value.(string))
	s.recency.Remove(oldest)
}

func retryAfterSeconds(delay time.Duration) int {
	if delay <= 0 {
		return 1
	}
	seconds := int(delay.Round(time.Second) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// SourceRateLimitMiddleware enforces pre-auth source admission and auth-failure throttling.
func SourceRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil || !limiter.enabled {
			c.Next()
			return
		}
		key := httpSourceKey(c)
		if decision := limiter.available(RateLimitAuthFailure, key); !decision.Allowed {
			recordRateLimit(RateLimitAuthFailure, "rejected")
			rejectHTTPRateLimited(c, RateLimitAuthFailure, decision.RetryAfterSeconds)
			return
		}
		if decision := limiter.allow(RateLimitSource, key); !decision.Allowed {
			rejectHTTPRateLimited(c, RateLimitSource, decision.RetryAfterSeconds)
			return
		}
		c.Next()
	}
}

func consumeHTTPAuthFailure(c *gin.Context, limiter *RateLimiter) {
	if limiter == nil || !limiter.enabled {
		return
	}
	_ = limiter.allow(RateLimitAuthFailure, httpSourceKey(c))
}

func consumeGRPCAuthFailure(ctx context.Context, limiter *RateLimiter) {
	if limiter == nil || !limiter.enabled {
		return
	}
	_ = limiter.allow(RateLimitAuthFailure, grpcSourceKey(ctx))
}

func applyHTTPAuthenticatedRateLimits(c *gin.Context, limiter *RateLimiter) bool {
	if limiter == nil || !limiter.enabled {
		return true
	}
	id := GetIdentity(c)
	if id == nil {
		return true
	}
	key := identityKey(id)
	if decision := limiter.allow(RateLimitIdentity, key); !decision.Allowed {
		rejectHTTPRateLimited(c, RateLimitIdentity, decision.RetryAfterSeconds)
		return false
	}
	if isHTTPExpensive(c.Request.Method, c.FullPath(), c.Request.URL.Path) {
		if decision := limiter.allow(RateLimitExpensive, key); !decision.Allowed {
			rejectHTTPRateLimited(c, RateLimitExpensive, decision.RetryAfterSeconds)
			return false
		}
	}
	if isHTTPStream(c.Request.Method, c.FullPath(), c.Request.URL.Path) {
		if decision := limiter.allow(RateLimitStreamOpen, key); !decision.Allowed {
			rejectHTTPRateLimited(c, RateLimitStreamOpen, decision.RetryAfterSeconds)
			return false
		}
	}
	return true
}

func rejectHTTPRateLimited(c *gin.Context, class RateLimitClass, retryAfter int) {
	if retryAfter < 1 {
		retryAfter = 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"code":  "rate_limited",
		"error": "rate limit exceeded",
		"details": gin.H{
			"retryAfterSeconds": retryAfter,
		},
	})
}

func httpSourceKey(c *gin.Context) string {
	if c == nil {
		return "source:unknown"
	}
	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		ip = "unknown"
	}
	return "source:" + ip
}

func identityKey(id *Identity) string {
	if id == nil {
		return "identity:unknown"
	}
	return "identity:" + string(id.CredentialKind) + ":user=" + id.UserID + ":client=" + id.ClientID
}

func isHTTPExpensive(method, route, path string) bool {
	if method == "" {
		method = http.MethodGet
	}
	p := route
	if p == "" {
		p = path
	}
	switch {
	case method == http.MethodPost && (p == "/v1/conversations/search" || p == "/v1/conversations/index" || p == "/v1/memories/search" || p == "/admin/v1/memories/search" || p == "/v1/admin/conversations/search"):
		return true
	case method == http.MethodPost && p == "/v1/attachments":
		return true
	case method == http.MethodGet && (strings.HasSuffix(p, "/download-url") || p == "/v1/admin/attachments/:id/content"):
		return true
	case method == http.MethodPost && p == "/v1/admin/evict":
		return true
	default:
		return false
	}
}

func isHTTPStream(method, route, path string) bool {
	if method != http.MethodGet {
		return false
	}
	p := route
	if p == "" {
		p = path
	}
	return p == "/v1/events" || p == "/v1/admin/events"
}

// GRPCSourceRateLimitUnaryInterceptor enforces source admission before auth resolution.
func GRPCSourceRateLimitUnaryInterceptor(limiter *RateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := applyGRPCSourceRateLimit(ctx, limiter); err != nil {
			return nil, err
		}
		resp, err := handler(ctx, req)
		if status.Code(err) == codes.Unauthenticated {
			_ = limiter.allow(RateLimitAuthFailure, grpcSourceKey(ctx))
		}
		return resp, err
	}
}

// GRPCIdentityRateLimitUnaryInterceptor enforces identity and expensive-operation limits after auth resolution.
func GRPCIdentityRateLimitUnaryInterceptor(limiter *RateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := applyGRPCAuthenticatedRateLimits(ctx, limiter, info.FullMethod, false); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// GRPCSourceRateLimitStreamInterceptor enforces source admission before stream auth resolution.
func GRPCSourceRateLimitStreamInterceptor(limiter *RateLimiter) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := applyGRPCSourceRateLimit(ss.Context(), limiter); err != nil {
			return err
		}
		err := handler(srv, ss)
		if status.Code(err) == codes.Unauthenticated {
			_ = limiter.allow(RateLimitAuthFailure, grpcSourceKey(ss.Context()))
		}
		return err
	}
}

// GRPCIdentityRateLimitStreamInterceptor enforces identity and stream-open limits after stream auth resolution.
func GRPCIdentityRateLimitStreamInterceptor(limiter *RateLimiter) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := applyGRPCAuthenticatedRateLimits(ss.Context(), limiter, info.FullMethod, true); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func applyGRPCSourceRateLimit(ctx context.Context, limiter *RateLimiter) error {
	if limiter == nil || !limiter.enabled {
		return nil
	}
	key := grpcSourceKey(ctx)
	if decision := limiter.available(RateLimitAuthFailure, key); !decision.Allowed {
		recordRateLimit(RateLimitAuthFailure, "rejected")
		return grpcRateLimitError(RateLimitAuthFailure, decision.RetryAfterSeconds)
	}
	if decision := limiter.allow(RateLimitSource, key); !decision.Allowed {
		return grpcRateLimitError(RateLimitSource, decision.RetryAfterSeconds)
	}
	return nil
}

func applyGRPCAuthenticatedRateLimits(ctx context.Context, limiter *RateLimiter, fullMethod string, stream bool) error {
	if limiter == nil || !limiter.enabled {
		return nil
	}
	id := IdentityFromContext(ctx)
	if id == nil {
		return nil
	}
	key := identityKey(id)
	if decision := limiter.allow(RateLimitIdentity, key); !decision.Allowed {
		return grpcRateLimitError(RateLimitIdentity, decision.RetryAfterSeconds)
	}
	if isGRPCExpensive(fullMethod) {
		if decision := limiter.allow(RateLimitExpensive, key); !decision.Allowed {
			return grpcRateLimitError(RateLimitExpensive, decision.RetryAfterSeconds)
		}
	}
	if stream {
		if decision := limiter.allow(RateLimitStreamOpen, key); !decision.Allowed {
			return grpcRateLimitError(RateLimitStreamOpen, decision.RetryAfterSeconds)
		}
	}
	return nil
}

func grpcSourceKey(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		host, _, err := net.SplitHostPort(p.Addr.String())
		if err == nil && host != "" {
			return "source:" + host
		}
		return "source:" + p.Addr.String()
	}
	return "source:unknown"
}

func isGRPCExpensive(fullMethod string) bool {
	return strings.HasSuffix(fullMethod, "/SearchConversations") ||
		strings.HasSuffix(fullMethod, "/IndexConversations") ||
		strings.HasSuffix(fullMethod, "/SearchMemories") ||
		strings.HasSuffix(fullMethod, "/UploadAttachment") ||
		strings.HasSuffix(fullMethod, "/DownloadAttachment") ||
		strings.HasSuffix(fullMethod, "/AdminEvict")
}

func grpcRateLimitError(class RateLimitClass, retryAfter int) error {
	if retryAfter < 1 {
		retryAfter = 1
	}
	st := status.New(codes.ResourceExhausted, "rate limit exceeded")
	withDetails, err := st.WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(time.Duration(retryAfter) * time.Second),
	})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func recordRateLimit(class RateLimitClass, outcome string) {
	if RateLimitRequestsTotal != nil {
		RateLimitRequestsTotal.WithLabelValues(string(class), outcome).Inc()
	}
}

func recordUnsafeConfig(reason string) {
	if SecurityUnsafeConfig != nil {
		SecurityUnsafeConfig.WithLabelValues(reason).Set(1)
	}
}
