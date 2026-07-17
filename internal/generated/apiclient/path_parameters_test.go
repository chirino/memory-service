package apiclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratedClientEscapesPathParameters(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		escaped string
	}{
		{name: "slash", value: "run/branch", escaped: "run%2Fbranch"},
		{name: "percent", value: "run%branch", escaped: "run%25branch"},
		{name: "space", value: "run branch", escaped: "run%20branch"},
		{name: "unicode", value: "café☕", escaped: "caf%C3%A9%E2%98%95"},
		{name: "already encoded", value: "run%2Fbranch", escaped: "run%252Fbranch"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := NewGetConversationRequest("http://memory-service.example", tc.value)
			require.NoError(t, err)
			require.Equal(t, "/v1/conversations/"+tc.escaped, req.URL.EscapedPath())
		})
	}
}

func TestGeneratedClientEscapesEachPathParameter(t *testing.T) {
	req, err := NewUpdateConversationMembershipRequest(
		"http://memory-service.example",
		"run/branch",
		"user/name",
		UpdateConversationMembershipJSONRequestBody{},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"/v1/conversations/run%2Fbranch/memberships/user%2Fname",
		req.URL.EscapedPath(),
	)
}
