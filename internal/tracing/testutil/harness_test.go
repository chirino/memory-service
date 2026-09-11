package testutil_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/stretchr/testify/require"
)

func TestHarness_SmokeTest(t *testing.T) {
	harness := testutil.NewTestHarness()
	defer func() { _ = harness.Shutdown(context.Background()) }()

	downstream := testutil.NewDownstreamRecorder()
	defer downstream.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downstream.Server.URL, nil)
	require.NoError(t, err)

	req.Header.Set("X-Custom", "test-val")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	header := downstream.LastHeader()
	require.NotNil(t, header)
	require.Equal(t, "test-val", header.Get("X-Custom"))
}
