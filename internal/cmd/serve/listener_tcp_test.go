//go:build !notcp

package serve

import (
	"net"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
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
