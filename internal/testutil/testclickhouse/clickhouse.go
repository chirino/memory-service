// Package testclickhouse starts disposable ClickHouse servers for tests.
package testclickhouse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is the ClickHouse server image used by tests. It must match the clickhouse service
// image in compose.yaml; TestImageMatchesCompose enforces that.
const Image = "clickhouse/clickhouse-server:26.8.8.8"

// AdminUser and AdminPassword authenticate the server's administrative user.
const (
	AdminUser     = "clickhouse"
	AdminPassword = "clickhouse"
)

// Server is a running ClickHouse container.
type Server struct {
	NativeAddress string
	HTTPAddress   string
	container     testcontainers.Container
}

// Start starts ClickHouse and applies deploy/clickhouse/init.sql, which creates the
// memory_service database and the memory_service_analytics ingest user.
func Start(ctx context.Context) (*Server, error) {
	initSQL, err := readInitSQL()
	if err != nil {
		return nil, err
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        Image,
			Env:          map[string]string{"CLICKHOUSE_DB": "default", "CLICKHOUSE_USER": AdminUser, "CLICKHOUSE_PASSWORD": AdminPassword, "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"},
			ExposedPorts: []string{"8123/tcp", "9000/tcp"},
			WaitingFor:   wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start ClickHouse container: %w", err)
	}
	server := &Server{container: container}
	if err := server.resolveAddresses(ctx); err != nil {
		return nil, errors.Join(err, server.Terminate(context.Background()))
	}
	if err := server.exec(ctx, initSQL); err != nil {
		return nil, errors.Join(err, server.Terminate(context.Background()))
	}
	return server, nil
}

// StartTB starts a server that is terminated when the test finishes.
func StartTB(tb testing.TB) *Server {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	server, err := Start(ctx)
	if err != nil {
		tb.Fatalf("start ClickHouse: %v", err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Terminate(ctx); err != nil {
			tb.Errorf("terminate ClickHouse: %v", err)
		}
	})
	return server
}

// Terminate stops and removes the container.
func (s *Server) Terminate(ctx context.Context) error {
	if s == nil || s.container == nil {
		return nil
	}
	return s.container.Terminate(ctx)
}

func (s *Server) resolveAddresses(ctx context.Context) error {
	host, err := s.container.Host(ctx)
	if err != nil {
		return fmt.Errorf("get ClickHouse host: %w", err)
	}
	native, err := s.container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		return fmt.Errorf("get ClickHouse native port: %w", err)
	}
	http, err := s.container.MappedPort(ctx, "8123/tcp")
	if err != nil {
		return fmt.Errorf("get ClickHouse HTTP port: %w", err)
	}
	s.NativeAddress = host + ":" + native.Port()
	s.HTTPAddress = host + ":" + http.Port()
	return nil
}

func (s *Server) exec(ctx context.Context, script string) error {
	conn, err := ch.Open(&ch.Options{Addr: []string{s.NativeAddress}, Auth: ch.Auth{Database: "default", Username: AdminUser, Password: AdminPassword}})
	if err != nil {
		return fmt.Errorf("open ClickHouse admin connection: %w", err)
	}
	defer conn.Close()
	for statement := range strings.SplitSeq(script, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply deploy/clickhouse/init.sql: %w", err)
		}
	}
	return nil
}

func readInitSQL() (string, error) {
	root, err := repositoryRoot()
	if err != nil {
		return "", err
	}
	script, err := os.ReadFile(filepath.Join(root, "deploy", "clickhouse", "init.sql"))
	if err != nil {
		return "", fmt.Errorf("read ClickHouse init script: %w", err)
	}
	return string(script), nil
}

// repositoryRoot finds the module root from the test's working directory, which is the
// directory of the package under test.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("find repository root: go.mod not found")
		}
		dir = parent
	}
}
