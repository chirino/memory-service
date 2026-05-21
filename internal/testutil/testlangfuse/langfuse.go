package testlangfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	PublicKey = "lf_pk_test_turn_traces"
	SecretKey = "lf_sk_test_turn_traces"
)

// Stack is a disposable Langfuse v3 self-hosted stack for integration tests.
type Stack struct {
	BaseURL string
}

func (s Stack) OTLPEndpoint() string {
	return s.BaseURL + "/api/public/otel"
}

func (s Stack) OTLPHeaders() string {
	auth := base64.StdEncoding.EncodeToString([]byte(PublicKey + ":" + SecretKey))
	return "Authorization=Basic " + auth + ",x-langfuse-ingestion-version=4"
}

// Start starts Langfuse Web, Worker, Postgres, ClickHouse, Redis, and MinIO.
func Start(tb testing.TB) Stack {
	tb.Helper()

	ctx := context.Background()
	net, err := network.New(ctx)
	if err != nil {
		tb.Fatalf("create Langfuse network: %v", err)
	}
	tb.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := net.Remove(cleanupCtx); err != nil {
			tb.Errorf("remove Langfuse network: %v", err)
		}
	})

	containers := make([]testcontainers.Container, 0, 6)
	start := func(name string, req testcontainers.ContainerRequest, opts ...testcontainers.CustomizeRequestOption) testcontainers.Container {
		tb.Helper()
		genericReq := testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		}
		for _, opt := range opts {
			if err := opt(&genericReq); err != nil {
				tb.Fatalf("configure Langfuse %s container: %v", name, err)
			}
		}
		c, err := testcontainers.GenericContainer(ctx, genericReq)
		if err != nil {
			tb.Fatalf("start Langfuse %s container: %v", name, err)
		}
		containers = append(containers, c)
		return c
	}
	tb.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := len(containers) - 1; i >= 0; i-- {
			if err := containers[i].Terminate(cleanupCtx); err != nil {
				tb.Errorf("terminate Langfuse container: %v", err)
			}
		}
	})

	commonWait := 120 * time.Second
	start("postgres", testcontainers.ContainerRequest{
		Image:        "postgres:17",
		Env:          map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "postgres", "POSTGRES_DB": "postgres", "TZ": "UTC", "PGTZ": "UTC"},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("5432/tcp"),
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		).WithStartupTimeout(commonWait),
	}, network.WithNetwork([]string{"postgres"}, net))

	start("clickhouse", testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server",
		Env:          map[string]string{"CLICKHOUSE_DB": "default", "CLICKHOUSE_USER": "clickhouse", "CLICKHOUSE_PASSWORD": "clickhouse"},
		ExposedPorts: []string{"8123/tcp", "9000/tcp"},
		WaitingFor:   wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(commonWait),
	}, network.WithNetwork([]string{"clickhouse"}, net))

	start("redis", testcontainers.ContainerRequest{
		Image:        "redis:7",
		Cmd:          []string{"--requirepass", "myredissecret", "--maxmemory-policy", "noeviction"},
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(commonWait),
	}, network.WithNetwork([]string{"redis"}, net))

	start("minio", testcontainers.ContainerRequest{
		Image:        "cgr.dev/chainguard/minio",
		Entrypoint:   []string{"sh"},
		Cmd:          []string{"-c", `mkdir -p /data/langfuse && minio server --address ":9000" --console-address ":9001" /data`},
		Env:          map[string]string{"MINIO_ROOT_USER": "minio", "MINIO_ROOT_PASSWORD": "miniosecret"},
		ExposedPorts: []string{"9000/tcp", "9001/tcp"},
		WaitingFor:   wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(commonWait),
	}, network.WithNetwork([]string{"minio"}, net))

	env := commonEnv()
	start("worker", testcontainers.ContainerRequest{
		Image:        "langfuse/langfuse-worker:3",
		Env:          env,
		ExposedPorts: []string{"3030/tcp"},
		WaitingFor:   wait.ForHTTP("/api/health").WithPort("3030/tcp").WithStartupTimeout(3 * time.Minute),
	}, network.WithNetwork([]string{"langfuse-worker"}, net))

	webEnv := commonEnv()
	for k, v := range headlessInitEnv() {
		webEnv[k] = v
	}
	web := start("web", testcontainers.ContainerRequest{
		Image:        "langfuse/langfuse:3",
		Env:          webEnv,
		ExposedPorts: []string{"3000/tcp"},
		WaitingFor:   wait.ForHTTP("/api/public/health").WithPort("3000/tcp").WithStartupTimeout(3 * time.Minute),
	}, network.WithNetwork([]string{"langfuse-web"}, net))

	host, err := web.Host(ctx)
	if err != nil {
		tb.Fatalf("get Langfuse web host: %v", err)
	}
	port, err := web.MappedPort(ctx, "3000")
	if err != nil {
		tb.Fatalf("get Langfuse web port: %v", err)
	}
	stack := Stack{BaseURL: fmt.Sprintf("http://%s:%s", host, port.Port())}
	if err := stack.waitReady(ctx); err != nil {
		tb.Fatalf("Langfuse web is not ready: %v", err)
	}
	return stack
}

func commonEnv() map[string]string {
	return map[string]string{
		"NEXTAUTH_URL":                                    "http://localhost:3000",
		"DATABASE_URL":                                    "postgresql://postgres:postgres@postgres:5432/postgres",
		"SALT":                                            "memory-service-test-salt",
		"ENCRYPTION_KEY":                                  "0000000000000000000000000000000000000000000000000000000000000000",
		"TELEMETRY_ENABLED":                               "false",
		"CLICKHOUSE_MIGRATION_URL":                        "clickhouse://clickhouse:9000",
		"CLICKHOUSE_URL":                                  "http://clickhouse:8123",
		"CLICKHOUSE_USER":                                 "clickhouse",
		"CLICKHOUSE_PASSWORD":                             "clickhouse",
		"CLICKHOUSE_CLUSTER_ENABLED":                      "false",
		"LANGFUSE_S3_EVENT_UPLOAD_BUCKET":                 "langfuse",
		"LANGFUSE_S3_EVENT_UPLOAD_REGION":                 "auto",
		"LANGFUSE_S3_EVENT_UPLOAD_ACCESS_KEY_ID":          "minio",
		"LANGFUSE_S3_EVENT_UPLOAD_SECRET_ACCESS_KEY":      "miniosecret",
		"LANGFUSE_S3_EVENT_UPLOAD_ENDPOINT":               "http://minio:9000",
		"LANGFUSE_S3_EVENT_UPLOAD_FORCE_PATH_STYLE":       "true",
		"LANGFUSE_S3_EVENT_UPLOAD_PREFIX":                 "events/",
		"LANGFUSE_S3_MEDIA_UPLOAD_BUCKET":                 "langfuse",
		"LANGFUSE_S3_MEDIA_UPLOAD_REGION":                 "auto",
		"LANGFUSE_S3_MEDIA_UPLOAD_ACCESS_KEY_ID":          "minio",
		"LANGFUSE_S3_MEDIA_UPLOAD_SECRET_ACCESS_KEY":      "miniosecret",
		"LANGFUSE_S3_MEDIA_UPLOAD_ENDPOINT":               "http://localhost:9090",
		"LANGFUSE_S3_MEDIA_UPLOAD_FORCE_PATH_STYLE":       "true",
		"LANGFUSE_S3_MEDIA_UPLOAD_PREFIX":                 "media/",
		"LANGFUSE_S3_BATCH_EXPORT_ENABLED":                "false",
		"LANGFUSE_S3_BATCH_EXPORT_BUCKET":                 "langfuse",
		"LANGFUSE_S3_BATCH_EXPORT_PREFIX":                 "exports/",
		"LANGFUSE_S3_BATCH_EXPORT_REGION":                 "auto",
		"LANGFUSE_S3_BATCH_EXPORT_ENDPOINT":               "http://minio:9000",
		"LANGFUSE_S3_BATCH_EXPORT_EXTERNAL_ENDPOINT":      "http://localhost:9090",
		"LANGFUSE_S3_BATCH_EXPORT_ACCESS_KEY_ID":          "minio",
		"LANGFUSE_S3_BATCH_EXPORT_SECRET_ACCESS_KEY":      "miniosecret",
		"LANGFUSE_S3_BATCH_EXPORT_FORCE_PATH_STYLE":       "true",
		"LANGFUSE_INGESTION_QUEUE_DELAY_MS":               "100",
		"LANGFUSE_INGESTION_CLICKHOUSE_WRITE_INTERVAL_MS": "100",
		"REDIS_HOST":                                      "redis",
		"REDIS_PORT":                                      "6379",
		"REDIS_AUTH":                                      "myredissecret",
		"REDIS_TLS_ENABLED":                               "false",
		"NEXTAUTH_SECRET":                                 "memory-service-test-nextauth-secret",
	}
}

func headlessInitEnv() map[string]string {
	return map[string]string{
		"LANGFUSE_INIT_ORG_ID":             "memory-service-tests",
		"LANGFUSE_INIT_ORG_NAME":           "Memory Service Tests",
		"LANGFUSE_INIT_PROJECT_ID":         "turn-traces",
		"LANGFUSE_INIT_PROJECT_NAME":       "Turn Traces",
		"LANGFUSE_INIT_PROJECT_PUBLIC_KEY": PublicKey,
		"LANGFUSE_INIT_PROJECT_SECRET_KEY": SecretKey,
		"LANGFUSE_INIT_USER_EMAIL":         "memory-service-tests@example.com",
		"LANGFUSE_INIT_USER_NAME":          "Memory Service Tests",
		"LANGFUSE_INIT_USER_PASSWORD":      "memory-service-tests",
	}
}

func (s Stack) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/public/ready", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func (s Stack) FetchTraces(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/public/traces?limit=100", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(PublicKey, SecretKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Langfuse traces: status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
