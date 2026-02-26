package s3store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/chirino/memory-service/internal/tempfiles"
	"github.com/google/uuid"
)

func init() {
	registryattach.Register(registryattach.Plugin{
		Name:   "s3",
		Loader: load,
	})
}

func load(ctx context.Context) (registryattach.AttachmentStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.S3Bucket == "" {
		return nil, fmt.Errorf("s3store: S3_BUCKET is required")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("s3store: load AWS config: %w", err)
	}
	usePathStyle := cfg.S3UsePathStyle
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = usePathStyle
	})
	presigner := s3.NewPresignClient(client)
	return &S3AttachmentStore{
		client:           client,
		presigner:        presigner,
		bucket:           cfg.S3Bucket,
		prefix:           strings.Trim(strings.TrimSpace(cfg.S3Prefix), "/"),
		externalEndpoint: strings.TrimSpace(cfg.S3ExternalEndpoint),
		tempDir:          cfg.ResolvedTempDir(),
	}, nil
}

type S3AttachmentStore struct {
	client           *s3.Client
	presigner        *s3.PresignClient
	bucket           string
	prefix           string
	externalEndpoint string
	tempDir          string
}

// s3Key returns the actual S3 object key for a storage key, applying the prefix if set.
// This matches Java's S3FileStore.key(storageKey) behaviour: the storage_key column holds the
// bare UUID; the prefix is applied at access time and never persisted.
func (s *S3AttachmentStore) s3Key(storageKey string) string {
	if s.prefix != "" {
		return s.prefix + "/" + storageKey
	}
	return storageKey
}

func (s *S3AttachmentStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	storageKey := uuid.New().String()
	s3Key := s.s3Key(storageKey)
	hasher := sha256.New()
	limited := io.LimitReader(data, maxSize+1)
	counting := &countingWriter{h: hasher}

	tmp, err := tempfiles.Create(s.tempDir, "memory-service-s3-upload-*")
	if err != nil {
		return nil, fmt.Errorf("s3store: create temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if _, err := io.Copy(tmp, io.TeeReader(limited, counting)); err != nil {
		return nil, fmt.Errorf("s3store: buffer upload stream: %w", err)
	}
	if counting.n > maxSize {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("s3store: rewind temp file: %w", err)
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &s3Key,
		Body:          tmp,
		ContentLength: aws.Int64(counting.n),
		ContentType:   &contentType,
	}, func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
	})
	if err != nil {
		return nil, fmt.Errorf("s3store: put object: %w", err)
	}

	return &registryattach.FileStoreResult{
		StorageKey: storageKey,
		Size:       counting.n,
		SHA256:     fmt.Sprintf("%x", hasher.Sum(nil)),
	}, nil
}

func (s *S3AttachmentStore) Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	s3Key := s.s3Key(storageKey)
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &s3Key,
	})
	if err != nil {
		return nil, fmt.Errorf("s3store: get object: %w", err)
	}
	return resp.Body, nil
}

func (s *S3AttachmentStore) Delete(ctx context.Context, storageKey string) error {
	s3Key := s.s3Key(storageKey)
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &s3Key,
	})
	return err
}

func (s *S3AttachmentStore) GetSignedURL(ctx context.Context, storageKey string, expiry time.Duration) (*url.URL, error) {
	s3Key := s.s3Key(storageKey)
	resp, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &s3Key,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return nil, fmt.Errorf("s3store: presign: %w", err)
	}
	parsed, err := url.Parse(resp.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.externalEndpoint) == "" {
		return parsed, nil
	}
	external, err := url.Parse(s.externalEndpoint)
	if err != nil {
		return nil, fmt.Errorf("s3store: parse external endpoint: %w", err)
	}
	parsed.Scheme = external.Scheme
	parsed.Host = external.Host
	if strings.TrimSpace(external.Path) != "" && external.Path != "/" {
		parsed.Path = strings.TrimRight(external.Path, "/") + parsed.Path
	}
	return parsed, nil
}

type countingWriter struct {
	h hash.Hash
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	w.n += int64(n)
	if _, err := w.h.Write(p); err != nil {
		return 0, err
	}
	return n, nil
}
