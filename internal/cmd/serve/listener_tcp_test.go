//go:build !notcp

package serve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestPrepareTCPListenerDefaultsToLoopback(t *testing.T) {
	prepared, err := prepareTCPListener(config.ListenerConfig{Port: 0})
	require.NoError(t, err)
	t.Cleanup(func() { _ = prepared.Listener.Close() })

	addr, ok := prepared.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.True(t, addr.IP.IsLoopback(), "expected loopback bind, got %s", addr.IP)
}

func TestPrepareTCPListenerUsesConfiguredHost(t *testing.T) {
	prepared, err := prepareTCPListener(config.ListenerConfig{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	t.Cleanup(func() { _ = prepared.Listener.Close() })

	addr, ok := prepared.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.Equal(t, "127.0.0.1", addr.IP.String())
}

func TestStartSinglePortHTTPAndGRPCDecodesPathParametersOverTCP(t *testing.T) {
	router := newPathParameterTestRouter()

	running, err := StartSinglePortHTTPAndGRPC(context.Background(), config.ListenerConfig{
		Host:            "127.0.0.1",
		Port:            0,
		EnablePlainText: true,
	}, router, grpc.NewServer())
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, running.Close(ctx))
	}()

	resp, err := http.Get("http://" + running.Endpoint + "/resources/run%2Fbranch")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "run/branch", string(body))

	conn, err := net.Dial("tcp", running.Endpoint)
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "GET /resources/bad%%ZZ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", running.Endpoint)
	require.NoError(t, err)
	malformedResp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	defer malformedResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, malformedResp.StatusCode)
}
