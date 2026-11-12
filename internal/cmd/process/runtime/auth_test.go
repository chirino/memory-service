package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func tokenResponse(w http.ResponseWriter, token string, lifetime int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": lifetime})
}

func TestOIDCConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		cfg       OIDCConfig
		want      string
	}{
		{"missing credentials", "", OIDCConfig{}, "authentication requires"},
		{"mixed modes", "key", OIDCConfig{Issuer: "https://issuer"}, "mutually exclusive"},
		{"both endpoints", "", OIDCConfig{Issuer: "https://issuer", TokenURL: "https://token"}, "exactly one"},
		{"neither endpoint", "", OIDCConfig{ClientSecret: "secret"}, "exactly one"},
		{"both secrets", "", OIDCConfig{TokenURL: "https://token", ClientSecret: "secret", ClientSecretFile: "file"}, "set only one"},
		{"missing secret", "", OIDCConfig{TokenURL: "https://token"}, "OIDC requires MEMORY_SERVICE"},
		{"remote plaintext", "", OIDCConfig{TokenURL: "http://issuer/token", ClientSecret: "secret"}, "--allow-insecure-oidc"},
		{"plaintext discovery", "", OIDCConfig{Issuer: "http://issuer", ClientSecret: "secret"}, "--allow-insecure-oidc"},
		{"malformed URL", "", OIDCConfig{TokenURL: "://token", ClientSecret: "secret"}, "absolute http"},
		{"URL credentials", "", OIDCConfig{TokenURL: "https://secret:password@issuer/token", ClientSecret: "secret"}, "without user info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGRPCAuth(t.Context(), "processor", tc.key, tc.cfg)
			require.ErrorContains(t, err, tc.want)
		})
	}
	for _, raw := range []string{"http://localhost/token", "http://127.0.0.1/token", "http://[::1]/token", "https://issuer/token"} {
		require.NoError(t, validateOIDCURL(raw, false))
	}
	require.NoError(t, validateOIDCURL("http://issuer/token", true))
}

func TestOIDCDiscoveryAndGrant(t *testing.T) {
	var grants atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realm/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": server.URL + "/realm", "token_endpoint": server.URL + "/token"})
			return
		}
		require.Equal(t, "/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		id, secret, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "processor", id)
		require.Equal(t, "secret", secret)
		require.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		require.Equal(t, "memory-service", r.Form.Get("audience"))
		require.Equal(t, "openid profile email", r.Form.Get("scope"))
		grants.Add(1)
		tokenResponse(w, "first", 300)
	}))
	defer server.Close()
	secretFile := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secretFile, []byte("secret\n"), 0600))
	auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{Issuer: server.URL + "/realm", ClientSecretFile: secretFile, Audience: "memory-service", Scopes: []string{"openid", "profile", "email"}})
	require.NoError(t, err)
	defer auth.Close()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			token, err := auth.accessToken(t.Context())
			require.NoError(t, err)
			require.Equal(t, "first", token)
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, grants.Load())
}

func TestOIDCTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tokenResponse(w, "token", 300) }))
	defer server.Close()
	_, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
	require.Error(t, err)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	for _, cfg := range []OIDCConfig{
		{TokenURL: server.URL, ClientSecret: "secret", CAFile: caFile},
		{TokenURL: server.URL, ClientSecret: "secret", TLSInsecureSkipVerify: true},
	} {
		auth, err := NewGRPCAuth(t.Context(), "processor", "", cfg)
		require.NoError(t, err)
		auth.Close()
	}
}

func TestOIDCStartupFailureAndRetry(t *testing.T) {
	var failures atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if failures.Load() > 0 {
			failures.Add(-1)
			http.Error(w, "secret-do-not-log", http.StatusServiceUnavailable)
			return
		}
		tokenResponse(w, "token", 300)
	}))
	defer server.Close()
	cfg := OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"}
	failures.Store(100)
	_, err := NewGRPCAuth(t.Context(), "processor", "", cfg)
	require.ErrorContains(t, err, "obtain initial OIDC access token")
	require.ErrorContains(t, err, "503")
	require.NotContains(t, err.Error(), "secret-do-not-log")
	failures.Store(0)
	auth, err := NewGRPCAuth(t.Context(), "processor", "", cfg)
	require.NoError(t, err)
	defer auth.Close()
	auth.invalidate("token")
	failures.Store(2)
	before := requests.Load()
	start := time.Now()
	_, err = auth.accessToken(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	require.GreaterOrEqual(t, requests.Load()-before, int32(3))
	auth.invalidate("token")
	failures.Store(100)
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = auth.accessToken(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestOIDCFlags(t *testing.T) {
	t.Setenv("MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("MEMORY_SERVICE_PROCESS_OIDC_SCOPE", "openid,profile,email")
	t.Setenv("MEMORY_SERVICE_OIDC_ISSUER", "https://server-only")
	cmd := &cli.Command{Flags: AuthFlags(), Action: func(ctx context.Context, cmd *cli.Command) error {
		cfg := OIDCConfigFromCommand(cmd)
		require.Equal(t, "secret", cfg.ClientSecret)
		require.Equal(t, []string{"openid", "profile", "email"}, cfg.Scopes)
		require.Empty(t, cfg.Issuer)
		return nil
	}}
	require.NoError(t, cmd.Run(t.Context(), []string{"test", "--client-id", "processor", "--api-key", "key"}))
}

// Exercise real unary and server-streaming RPCs so authentication failures
// delivered on the first Recv, rather than stream creation, are covered.
func TestGRPCOIDCRefreshAndRejection(t *testing.T) {
	var grants atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tokenResponse(w, fmt.Sprint(grants.Add(1)), 1) }))
	defer tokens.Close()
	auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: tokens.URL, ClientSecret: "secret"})
	require.NoError(t, err)
	defer auth.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var calls atomic.Int32
	var reject atomic.Bool
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		require.Equal(t, []string{"processor"}, md.Get("x-client-id"))
		authorization := md.Get("authorization")
		require.Len(t, authorization, 1)
		issued, err := strconv.Atoi(strings.TrimPrefix(authorization[0], "Bearer "))
		require.NoError(t, err)
		require.Greater(t, issued, 0)
		require.LessOrEqual(t, issued, int(grants.Load()))
		calls.Add(1)
		if reject.Load() {
			return nil, status.Error(codes.Unauthenticated, "rejected")
		}
		return next(ctx, req)
	}))
	streamServer := &authEventServer{}
	pb.RegisterEventStreamServiceServer(server, streamServer)
	pb.RegisterSystemServiceServer(server, authSystemServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := DialGRPCWithConfig(listener.Addr().String(), GRPCDialConfig{Auth: auth})
	require.NoError(t, err)
	defer conn.Close()
	client := pb.NewSystemServiceClient(conn)
	for range 4 {
		_, err := client.GetCapabilities(t.Context(), &emptypb.Empty{})
		require.NoError(t, err)
		time.Sleep(950 * time.Millisecond)
	}
	require.GreaterOrEqual(t, grants.Load(), int32(4))
	reject.Store(true)
	before := calls.Load()
	_, err = client.GetCapabilities(t.Context(), &emptypb.Empty{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.EqualValues(t, 2, calls.Load()-before)
	reject.Store(false)
	streamServer.rejects.Store(1)
	stream, err := (GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn)}).Subscribe(t.Context(), SubscribeRequest{AfterCursor: "cursor"})
	require.NoError(t, err)
	event, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "cursor", event.Cursor)
	require.EqualValues(t, 2, streamServer.opens.Load())
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF) // Mid-stream rejection reconnects using the runtime's cursor.
	streamServer.rejects.Store(10)
	stream, err = (GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn)}).Subscribe(t.Context(), SubscribeRequest{})
	require.NoError(t, err)
	before = streamServer.opens.Load()
	_, err = stream.Recv()
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.LessOrEqual(t, streamServer.opens.Load()-before, int32(2))
}

type authSystemServer struct {
	pb.UnimplementedSystemServiceServer
}

func (authSystemServer) GetCapabilities(context.Context, *emptypb.Empty) (*pb.CapabilitiesResponse, error) {
	return &pb.CapabilitiesResponse{}, nil
}

type authEventServer struct {
	pb.UnimplementedEventStreamServiceServer
	rejects atomic.Int32
	opens   atomic.Int32
}

func (s *authEventServer) SubscribeEvents(req *pb.SubscribeEventsRequest, stream pb.EventStreamService_SubscribeEventsServer) error {
	s.opens.Add(1)
	md, _ := metadata.FromIncomingContext(stream.Context())
	if len(md.Get("authorization")) != 1 {
		return status.Error(codes.Unauthenticated, "missing token")
	}
	if s.rejects.Add(-1) >= 0 {
		return status.Error(codes.Unauthenticated, "rejected")
	}
	cursor := req.GetAfterCursor()
	if err := stream.Send(&pb.EventNotification{Event: "snapshot", Cursor: &cursor}); err != nil {
		return err
	}
	return status.Error(codes.Unauthenticated, "expired")
}

func TestGRPCAPIKeyAuthentication(t *testing.T) {
	auth, err := NewGRPCAuth(t.Context(), "processor", "key", OIDCConfig{})
	require.NoError(t, err)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-request-id", "request")
	calls := 0
	err = auth.unaryInterceptor(ctx, "/service/method", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls++
		md, _ := metadata.FromOutgoingContext(ctx)
		require.Equal(t, []string{"key"}, md.Get("x-api-key"))
		require.Equal(t, []string{"processor"}, md.Get("x-client-id"))
		require.Equal(t, []string{"request"}, md.Get("x-request-id"))
		require.Empty(t, md.Get("authorization"))
		return status.Error(codes.Unauthenticated, "invalid key")
	})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Equal(t, 1, calls)
}

func TestOIDCDiscoveryRejectsInvalidMetadata(t *testing.T) {
	for _, tc := range []struct{ name, tokenURL, issuer, want string }{
		{"remote plaintext", "http://remote/token", "", "--allow-insecure-oidc"},
		{"missing token endpoint", "", "", "absolute http"},
		{"issuer mismatch", "https://remote/token", "https://another-issuer", "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				issuer := tc.issuer
				if issuer == "" {
					issuer = server.URL
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "token_endpoint": tc.tokenURL})
			}))
			defer server.Close()
			_, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{Issuer: server.URL, ClientSecret: "secret"})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestOIDCRenewalWaitCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var grants atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if grants.Add(1) == 2 {
					close(started)
					<-release
				}
				tokenResponse(w, "token", 300)
			}))
			defer server.Close()
			auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
			require.NoError(t, err)
			defer auth.Close()
			auth.invalidate("token")
			leader := make(chan error, 1)
			go func() { _, err := auth.accessToken(t.Context()); leader <- err }()
			defer func() { close(release); require.NoError(t, <-leader) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("renewal did not start")
			}
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			waiter := make(chan error, 1)
			go func() { _, err := auth.accessToken(ctx); waiter <- err }()
			select {
			case err := <-waiter:
				require.ErrorIs(t, err, want)
			case <-time.After(time.Second):
				t.Error("canceled caller waited for an unrelated token request")
			}
		})
	}
}

type oidcRoundTripper func(*http.Request) (*http.Response, error)

func (f oidcRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOIDCTransportDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"TLS", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "TLS certificate verification failed"},
		{"DNS", &net.DNSError{Name: "secret-host", Err: "secret-provider-detail"}, "DNS lookup failed"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "connection refused"},
		{"timeout", &net.DNSError{IsTimeout: true, Err: "secret-provider-detail"}, "timed out"},
		{"unknown", fmt.Errorf("secret-provider-detail"), "OIDC token request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &GRPCAuth{
				grant:      &clientcredentials.Config{ClientID: "processor", ClientSecret: "secret-client", TokenURL: "https://secret-host/token?credential=secret-query", AuthStyle: oauth2.AuthStyleInHeader},
				httpClient: &http.Client{Transport: oidcRoundTripper(func(*http.Request) (*http.Response, error) { return nil, tc.err })},
			}
			_, err := auth.fetchToken(t.Context())
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "secret-")
			require.NotContains(t, err.Error(), "https://")
		})
	}
}

func TestOIDCAsyncRenewalKeepsValidToken(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var grants atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if grants.Add(1) == 1 {
			tokenResponse(w, "old", 1)
			return
		}
		close(started)
		<-release
		tokenResponse(w, "new", 300)
	}))
	defer server.Close()
	auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
	require.NoError(t, err)
	defer auth.Close()
	defer close(release)
	// Keep the early-renewal schedule, but give assertions a generous validity window.
	auth.mu.Lock()
	auth.token.Expiry = time.Now().Add(time.Minute)
	auth.mu.Unlock()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("token did not renew without an RPC")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			token, err := auth.accessToken(ctx)
			require.NoError(t, err)
			require.Equal(t, "old", token)
		})
	}
	wg.Wait()
	require.EqualValues(t, 2, grants.Load(), "concurrent callers must share one renewal")
}

func TestOIDCConcurrentRenewalWaiters(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var grants, active, maximum atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := active.Add(1)
				defer active.Add(-1)
				for previous := maximum.Load(); count > previous && !maximum.CompareAndSwap(previous, count); previous = maximum.Load() {
				}
				if grants.Add(1) == 1 {
					tokenResponse(w, "old", 300)
					return
				}
				close(started)
				<-release
				tokenResponse(w, "new", 300)
			}))
			defer server.Close()
			auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
			require.NoError(t, err)
			defer auth.Close()
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			if expired {
				auth.mu.Lock()
				auth.token.Expiry = time.Now().Add(-time.Second)
				auth.renewAt = auth.token.Expiry
				auth.mu.Unlock()
				auth.wake <- struct{}{}
			} else {
				auth.invalidate("old")
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("renewal did not start")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = auth.accessToken(ctx)
			require.ErrorIs(t, err, context.Canceled)
			results, ready := make(chan string, 20), make(chan struct{}, 20)
			for range 20 {
				go func() {
					ready <- struct{}{}
					token, err := auth.accessToken(t.Context())
					if err != nil {
						results <- err.Error()
						return
					}
					results <- token
				}()
			}
			for range 20 {
				<-ready
			}
			select {
			case result := <-results:
				t.Fatalf("expired/rejected token returned before renewal: %s", result)
			default:
			}
			releaseOnce.Do(func() { close(release) })
			for range 20 {
				select {
				case token := <-results:
					require.Equal(t, "new", token)
				case <-time.After(time.Second):
					t.Fatal("renewal waiter did not resume")
				}
			}
			auth.invalidate("old") // Late rejection of the old token must preserve its replacement.
			token, err := auth.accessToken(t.Context())
			require.NoError(t, err)
			require.Equal(t, "new", token)
			require.EqualValues(t, 2, grants.Load())
			require.EqualValues(t, 1, maximum.Load())
		})
	}
}

func TestOIDCRenewalFailureKeepsValidToken(t *testing.T) {
	failed, retrying, release := make(chan time.Time, 1), make(chan time.Time, 1), make(chan struct{})
	var grants atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch grants.Add(1) {
		case 1:
			tokenResponse(w, "old", 300)
		case 2:
			failed <- time.Now()
			http.Error(w, "secret-provider-body", http.StatusServiceUnavailable)
		default:
			retrying <- time.Now()
			<-release
			tokenResponse(w, "new", 300)
		}
	}))
	defer server.Close()
	auth, err := NewGRPCAuth(t.Context(), "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
	require.NoError(t, err)
	defer auth.Close()
	defer close(release)
	// Make early renewal due while the access token remains valid.
	auth.mu.Lock()
	auth.renewAt = time.Now()
	auth.mu.Unlock()
	auth.wake <- struct{}{}
	var failureTime, retryTime time.Time
	select {
	case failureTime = <-failed:
	case <-time.After(time.Second):
		t.Fatal("renewal did not fail")
	}
	select {
	case retryTime = <-retrying:
	case <-time.After(time.Second):
		t.Fatal("renewal did not retry")
	}
	require.GreaterOrEqual(t, retryTime.Sub(failureTime), 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	token, err := auth.accessToken(ctx)
	require.NoError(t, err)
	require.Equal(t, "old", token)
}

func TestOIDCRenewalShutdown(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parentCanceled=%v", cancelParent), func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			var grants atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if grants.Add(1) == 1 {
					tokenResponse(w, "old", 300)
					return
				}
				require.NoError(t, r.ParseForm())
				close(started)
				<-r.Context().Done()
				close(canceled)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			auth, err := NewGRPCAuth(ctx, "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
			require.NoError(t, err)
			defer auth.Close()
			auth.invalidate("old")
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("renewal did not start")
			}
			if cancelParent {
				cancel()
			}
			auth.Close()
			select {
			case <-auth.done:
			case <-time.After(time.Second):
				t.Fatal("renewal worker did not stop")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("shutdown did not cancel the in-flight grant")
			}
			_, err = auth.accessToken(t.Context())
			require.ErrorIs(t, err, context.Canceled)
			require.EqualValues(t, 2, grants.Load())
		})
	}
}

func TestOIDCHTTPClientTimeoutDiagnostics(t *testing.T) {
	auth := &GRPCAuth{
		grant: &clientcredentials.Config{ClientID: "processor", ClientSecret: "secret-client", TokenURL: "https://secret-host/token?credential=secret-query", AuthStyle: oauth2.AuthStyleInHeader},
		httpClient: &http.Client{Timeout: 10 * time.Millisecond, Transport: oidcRoundTripper(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
	}
	_, err := auth.fetchToken(t.Context())
	require.EqualError(t, err, "OIDC token request timed out")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = auth.fetchToken(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestOIDCShutdownKeepsValidTokenForFinalRPCs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tokenResponse(w, "valid", 300) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	auth, err := NewGRPCAuth(ctx, "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
	require.NoError(t, err)
	defer auth.Close()
	cancel()
	auth.Close()
	// Runtime shutdown flushes checkpoints and releases leases with fresh contexts.
	err = auth.unaryInterceptor(t.Context(), "/memory.v1.AdminCheckpointService/ReleaseLease", nil, nil, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			require.Equal(t, []string{"Bearer valid"}, md.Get("authorization"))
			return nil
		})
	require.NoError(t, err)
}

func TestOIDCParentCancellationAllowsRenewedCleanupRPCs(t *testing.T) {
	var grants atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenResponse(w, fmt.Sprint(grants.Add(1)), 300)
	}))
	defer server.Close()
	startupCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	auth, err := NewGRPCAuth(startupCtx, "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
	require.NoError(t, err)
	defer auth.Close()
	cancel()
	// Simulate expiry during cleanup, after the processor's parent was canceled.
	auth.mu.Lock()
	auth.token.Expiry = time.Now().Add(-time.Second)
	auth.renewAt = auth.token.Expiry
	auth.mu.Unlock()
	auth.wake <- struct{}{}
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	for _, method := range []string{pb.AdminCheckpointService_PutCheckpoint_FullMethodName, pb.AdminCheckpointService_ReleaseLease_FullMethodName} {
		err := auth.unaryInterceptor(ctx, method, nil, nil, nil,
			func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				md, _ := metadata.FromOutgoingContext(ctx)
				require.Equal(t, []string{"Bearer 2"}, md.Get("authorization"))
				return nil
			})
		require.NoError(t, err)
	}
	auth.Close()
	auth.invalidate("2")
	_, err = auth.accessToken(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 2, grants.Load())
}

func TestOIDCStartupGrantHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		auth, err := NewGRPCAuth(ctx, "processor", "", OIDCConfig{TokenURL: server.URL, ClientSecret: "secret"})
		if auth != nil {
			auth.Close()
		}
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup grant did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("startup grant ignored cancellation")
	}
}
