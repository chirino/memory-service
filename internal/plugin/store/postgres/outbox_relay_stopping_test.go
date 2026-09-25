//go:build !nopostgresql

package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// relayStopping classifies shutdown and connection-loss errors as normal so the
// relay does not log them as unexpected "relay stopped" warnings.
func TestRelayStoppingClassifiesNormalShutdownErrors(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "nil error", ctx: context.Background(), err: nil, want: true},
		{name: "context already canceled", ctx: canceledCtx, err: errors.New("anything"), want: true},
		{name: "wrapped context canceled", ctx: context.Background(), err: fmt.Errorf("receive: %w", context.Canceled), want: true},
		{name: "wrapped deadline exceeded", ctx: context.Background(), err: fmt.Errorf("receive: %w", context.DeadlineExceeded), want: true},
		{name: "connection refused", ctx: context.Background(), err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED), want: true},
		{name: "unexpected EOF", ctx: context.Background(), err: fmt.Errorf("read: %w", io.ErrUnexpectedEOF), want: true},
		{name: "broken pipe", ctx: context.Background(), err: fmt.Errorf("write: %w", syscall.EPIPE), want: true},
		{name: "closed network connection", ctx: context.Background(), err: fmt.Errorf("read: %w", net.ErrClosed), want: true},
		{name: "unrelated error", ctx: context.Background(), err: errors.New("permission denied for publication"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, relayStopping(tc.ctx, tc.err))
		})
	}
}
