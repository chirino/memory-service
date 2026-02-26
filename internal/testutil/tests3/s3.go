package tests3

import (
	"context"
	"fmt"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const testBucket = "test-attachments"

// StartS3 starts a disposable LocalStack container, creates a test bucket,
// and sets AWS env vars so that aws-sdk-go-v2 LoadDefaultConfig points at it.
// Returns the bucket name.
func StartS3(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "localstack/localstack:latest",
			ExposedPorts: []string{"4566/tcp"},
			Env: map[string]string{
				"SERVICES": "s3",
			},
			WaitingFor: wait.ForListeningPort("4566/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start localstack container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate localstack container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get localstack host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "4566")
	if err != nil {
		tb.Fatalf("get localstack mapped port: %v", err)
	}

	endpoint := fmt.Sprintf("http://%s:%s", host, mappedPort.Port())

	// Set env vars so aws-sdk-go-v2 LoadDefaultConfig picks up LocalStack.
	tb.Setenv("AWS_ENDPOINT_URL", endpoint)
	tb.Setenv("AWS_ACCESS_KEY_ID", "test")
	tb.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	tb.Setenv("AWS_REGION", "us-east-1")

	// Create the test bucket.
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithRegion("us-east-1"),
	)
	if err != nil {
		tb.Fatalf("load aws config for bucket creation: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = &endpoint
		o.UsePathStyle = true
	})
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: strPtr(testBucket),
	})
	if err != nil {
		tb.Fatalf("create test bucket: %v", err)
	}

	return testBucket
}

func strPtr(s string) *string {
	return &s
}
