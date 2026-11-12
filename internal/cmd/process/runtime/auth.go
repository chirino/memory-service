package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// OIDCConfig configures the processor's OAuth2 client-credentials grant.
type OIDCConfig struct {
	Issuer                string
	TokenURL              string
	ClientSecret          string
	ClientSecretFile      string
	Audience              string
	Scopes                []string
	CAFile                string
	TLSInsecureSkipVerify bool
	AllowInsecure         bool
}

// AuthFlags returns authentication flags shared by all processors. Secrets are
// accepted through the environment or a file, never through process arguments.
func AuthFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "client-id", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_CLIENT_ID"), Required: true, Usage: "Processor identity, checkpoint owner, and OAuth client ID"},
		&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("MEMORY_SERVICE_API_KEY"), Usage: "Memory Service API key, mutually exclusive with OIDC"},
		&cli.StringFlag{Name: "oidc-issuer", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_ISSUER"), Usage: "OIDC issuer for token endpoint discovery"},
		&cli.StringFlag{Name: "oidc-token-url", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_TOKEN_URL"), Usage: "Explicit OAuth token endpoint instead of issuer discovery"},
		&cli.StringFlag{Name: "oidc-client-secret-file", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET_FILE"), Usage: "Client secret file instead of MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET"},
		&cli.StringFlag{Name: "oidc-audience", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_AUDIENCE"), Usage: "OAuth audience form parameter"},
		&cli.StringSliceFlag{Name: "oidc-scope", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_SCOPE"), Usage: "OAuth scope, repeat flag or separate environment values with commas"},
		&cli.StringFlag{Name: "oidc-ca-file", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_CA_FILE"), Usage: "PEM CA bundle for discovery and token requests"},
		&cli.BoolFlag{Name: "oidc-tls-insecure-skip-verify", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_OIDC_TLS_INSECURE_SKIP_VERIFY"), Usage: "Skip OIDC TLS verification for development"},
		&cli.BoolFlag{Name: "allow-insecure-oidc", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_ALLOW_INSECURE_OIDC"), Usage: "Allow non-loopback plaintext OIDC for development"},
	}
}

func OIDCConfigFromCommand(cmd *cli.Command) OIDCConfig {
	return OIDCConfig{Issuer: cmd.String("oidc-issuer"), TokenURL: cmd.String("oidc-token-url"),
		ClientSecret: os.Getenv("MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET"), ClientSecretFile: cmd.String("oidc-client-secret-file"),
		Audience: cmd.String("oidc-audience"), Scopes: cmd.StringSlice("oidc-scope"), CAFile: cmd.String("oidc-ca-file"),
		TLSInsecureSkipVerify: cmd.Bool("oidc-tls-insecure-skip-verify"), AllowInsecure: cmd.Bool("allow-insecure-oidc")}
}

// GRPCAuth owns one token cache shared by every RPC on a processor connection.
type GRPCAuth struct {
	clientID    string
	apiKey      string
	grant       *clientcredentials.Config
	httpClient  *http.Client
	mu          sync.Mutex
	token       *oauth2.Token
	renewAt     time.Time
	nextAttempt time.Time
	backoff     time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	changed     chan struct{}
	wake        chan struct{}
}

// NewGRPCAuth validates configuration and obtains the first token before startup.
// ctx controls startup only. The owner must call Close after its final cleanup RPCs.
func NewGRPCAuth(ctx context.Context, clientID, apiKey string, cfg OIDCConfig) (*GRPCAuth, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("client ID is required")
	}
	enabled := cfg.Issuer != "" || cfg.TokenURL != "" || cfg.ClientSecret != "" || cfg.ClientSecretFile != "" || cfg.Audience != "" || len(cfg.Scopes) > 0 || cfg.CAFile != "" || cfg.TLSInsecureSkipVerify || cfg.AllowInsecure
	if apiKey != "" && enabled {
		return nil, errors.New("--api-key and OIDC authentication are mutually exclusive")
	}
	if apiKey == "" && !enabled {
		return nil, errors.New("authentication requires --api-key or OIDC client credentials")
	}
	auth := &GRPCAuth{clientID: clientID, apiKey: apiKey}
	if !enabled {
		return auth, nil
	}
	if (cfg.Issuer == "") == (cfg.TokenURL == "") {
		return nil, errors.New("OIDC requires exactly one of --oidc-issuer or --oidc-token-url")
	}
	if cfg.ClientSecret != "" && cfg.ClientSecretFile != "" {
		return nil, errors.New("set only one of MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET and --oidc-client-secret-file")
	}
	if cfg.ClientSecretFile != "" {
		data, err := os.ReadFile(cfg.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC client secret file: %w", err)
		}
		cfg.ClientSecret = strings.TrimSpace(string(data))
	}
	if cfg.ClientSecret == "" {
		return nil, errors.New("OIDC requires MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET or --oidc-client-secret-file")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecureSkipVerify} // #nosec G402 -- explicit development opt-in
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("OIDC CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	auth.httpClient = &http.Client{Transport: transport, Timeout: 10 * time.Second,
		// Do not forward client credentials to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ready := false
	defer func() {
		if !ready {
			auth.httpClient.CloseIdleConnections()
		}
	}()
	if cfg.Issuer != "" {
		if err := validateOIDCURL(cfg.Issuer, cfg.AllowInsecure); err != nil {
			return nil, fmt.Errorf("OIDC issuer: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Issuer, "/")+"/.well-known/openid-configuration", nil)
		if err != nil {
			return nil, errors.New("invalid OIDC discovery URL")
		}
		resp, err := auth.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("OIDC discovery returned HTTP %d", resp.StatusCode)
		}
		var discovery struct {
			Issuer   string `json:"issuer"`
			TokenURL string `json:"token_endpoint"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery); err != nil {
			return nil, errors.New("invalid OIDC discovery response")
		}
		if discovery.Issuer != cfg.Issuer {
			return nil, errors.New("OIDC discovery issuer does not match --oidc-issuer")
		}
		cfg.TokenURL = discovery.TokenURL
	}
	if err := validateOIDCURL(cfg.TokenURL, cfg.AllowInsecure); err != nil {
		return nil, fmt.Errorf("OIDC token endpoint: %w", err)
	}
	params := url.Values{}
	if cfg.Audience != "" {
		params.Set("audience", cfg.Audience)
	}
	auth.grant = &clientcredentials.Config{ClientID: clientID, ClientSecret: cfg.ClientSecret, TokenURL: cfg.TokenURL, Scopes: cfg.Scopes, EndpointParams: params}
	token, err := auth.fetchToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("obtain initial OIDC access token: %w", err)
	}
	auth.token = token
	auth.renewAt = tokenRenewAt(token)
	auth.ctx, auth.cancel = context.WithCancel(context.WithoutCancel(ctx))
	auth.done, auth.changed, auth.wake = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	ready = true
	go auth.renew()
	return auth, nil
}

func validateOIDCURL(raw string, allowInsecure bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("must be an absolute http or https URL without user info or fragment")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) && !allowInsecure {
		return errors.New("non-loopback HTTP requires --allow-insecure-oidc")
	}
	return nil
}

// Close cancels renewal, waits for its HTTP request to finish, and releases idle connections.
// A valid cached token remains available for final checkpoint and lease-release RPCs.
func (a *GRPCAuth) Close() {
	if a == nil || a.cancel == nil {
		return
	}
	a.cancel()
	<-a.done
	a.httpClient.CloseIdleConnections()
}

func tokenRenewAt(token *oauth2.Token) time.Time {
	return token.Expiry.Add(-min(30*time.Second, time.Until(token.Expiry)/10))
}

func (a *GRPCAuth) fetchToken(ctx context.Context) (*oauth2.Token, error) {
	token, err := a.grant.Token(context.WithValue(ctx, oauth2.HTTPClient, a.httpClient))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil {
		if token.AccessToken == "" || token.Expiry.IsZero() || !token.Expiry.After(time.Now()) {
			return nil, errors.New("OIDC token response requires an access token and positive expires_in")
		}
		return token, nil
	}
	// OAuth and transport errors can contain credentials and provider response bodies.
	// Return only fixed categories and HTTP status, never the original error text.
	var responseErr *oauth2.RetrieveError
	var certificateErr *tls.CertificateVerificationError
	var dnsErr *net.DNSError
	var networkErr net.Error
	switch {
	case errors.As(err, &responseErr) && responseErr.Response != nil:
		return nil, fmt.Errorf("OIDC token request returned HTTP %d", responseErr.Response.StatusCode)
	case errors.As(err, &certificateErr):
		return nil, errors.New("OIDC token request TLS certificate verification failed")
	case errors.As(err, &networkErr) && networkErr.Timeout():
		return nil, errors.New("OIDC token request timed out")
	case errors.As(err, &dnsErr):
		return nil, errors.New("OIDC token request DNS lookup failed")
	case errors.Is(err, syscall.ECONNREFUSED):
		return nil, errors.New("OIDC token request connection refused")
	default:
		return nil, errors.New("OIDC token request failed")
	}
}

// One worker owns all grants. RPCs keep using the current token until hard expiry.
func (a *GRPCAuth) renew() {
	defer close(a.done)
	defer a.cancel()
	defer a.httpClient.CloseIdleConnections()
	defer func() {
		if recovered := recover(); recovered != nil {
			stack := debug.Stack()
			operationevent.LogRecoveredPanic(nil, "job.process.oidc-renewal", recovered, stack)
		}
	}()
	for {
		a.mu.Lock()
		due := a.nextAttempt
		if a.token != nil && a.renewAt.After(due) {
			due = a.renewAt
		}
		a.mu.Unlock()
		timer := time.NewTimer(max(0, time.Until(due)))
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-a.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		token, err := a.fetchToken(a.ctx)
		if a.ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		if err == nil {
			a.token, a.renewAt = token, tokenRenewAt(token)
			a.backoff, a.nextAttempt = 0, time.Time{}
		} else {
			a.backoff = min(30*time.Second, max(100*time.Millisecond, a.backoff*2))
			a.nextAttempt = time.Now().Add(a.backoff)
		}
		close(a.changed)
		a.changed = make(chan struct{})
		a.mu.Unlock()
		if err != nil {
			log.Warn("processor OIDC token request failed; retrying", "err", err)
		}
	}
}

func (a *GRPCAuth) accessToken(ctx context.Context) (string, error) {
	if a.grant == nil {
		return "", nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		a.mu.Lock()
		if a.token != nil && time.Now().Before(a.token.Expiry) {
			token := a.token.AccessToken
			a.mu.Unlock()
			return token, nil
		}
		if a.ctx.Err() != nil {
			a.mu.Unlock()
			return "", a.ctx.Err()
		}
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-a.ctx.Done():
			return "", a.ctx.Err()
		case <-changed:
		}
	}
}

func (a *GRPCAuth) invalidate(rejected string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A concurrent call may already have replaced the rejected token.
	if a.token != nil && a.token.AccessToken == rejected {
		a.token = nil
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
}

func (a *GRPCAuth) requestContext(ctx context.Context) (context.Context, string, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, "", err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-client-id", a.clientID)
	if a.apiKey != "" {
		md.Set("x-api-key", a.apiKey)
	}
	if token != "" {
		md.Set("authorization", "Bearer "+token)
	}
	return metadata.NewOutgoingContext(ctx, md), token, nil
}

func (a *GRPCAuth) unaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	for attempt := 0; ; attempt++ {
		authCtx, token, err := a.requestContext(ctx)
		if err != nil {
			return err
		}
		err = invoke(authCtx, method, req, reply, cc, opts...)
		if status.Code(err) != codes.Unauthenticated || a.grant == nil {
			return err
		}
		a.invalidate(token)
		if attempt == 1 {
			return err
		}
	}
}

func (a *GRPCAuth) dialOptions() []grpc.DialOption {
	if a == nil {
		return nil
	}
	return []grpc.DialOption{grpc.WithUnaryInterceptor(a.unaryInterceptor), grpc.WithStreamInterceptor(a.streamInterceptor)}
}

// Processor RPCs use server streams with one request. Retry rejection before
// the first response. Once events have arrived, reconnect from the runtime's
// saved cursor instead of replaying the original request.
func (a *GRPCAuth) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	open := func() (grpc.ClientStream, string, error) {
		authCtx, token, err := a.requestContext(ctx)
		if err != nil {
			return nil, "", err
		}
		stream, err := streamer(authCtx, desc, cc, method, opts...)
		return stream, token, err
	}
	stream, token, err := open()
	retried := false
	if status.Code(err) == codes.Unauthenticated && a.grant != nil {
		a.invalidate(token)
		retried = true
		stream, token, err = open()
	}
	if err != nil {
		return nil, err
	}
	if a.grant == nil || desc.ClientStreams {
		return stream, nil
	}
	return &authStream{ClientStream: stream, auth: a, token: token, open: open, retried: retried}, nil
}

type authStream struct {
	grpc.ClientStream
	auth     *GRPCAuth
	token    string
	open     func() (grpc.ClientStream, string, error)
	request  any
	received bool
	retried  bool
}

func (s *authStream) SendMsg(msg any) error { s.request = msg; return s.ClientStream.SendMsg(msg) }
func (s *authStream) RecvMsg(msg any) error {
	err := s.ClientStream.RecvMsg(msg)
	if status.Code(err) == codes.Unauthenticated {
		s.auth.invalidate(s.token)
		if s.received {
			return io.EOF
		}
		if s.retried {
			return err
		}
		s.retried = true
		s.ClientStream, s.token, err = s.open()
		if err != nil {
			return err
		}
		if err = s.ClientStream.SendMsg(s.request); err != nil {
			return err
		}
		if err = s.ClientStream.CloseSend(); err != nil {
			return err
		}
		err = s.ClientStream.RecvMsg(msg)
		if status.Code(err) == codes.Unauthenticated {
			s.auth.invalidate(s.token)
		}
	}
	if err == nil {
		s.received = true
	}
	return err
}
