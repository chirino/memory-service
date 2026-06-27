package mcp

import (
	"net/http"
	"net/http/httptest"

	"github.com/chirino/memory-service/internal/generated/apiclient"
	"github.com/chirino/memory-service/internal/security"
)

// handlerTransport routes requests directly through an http.Handler without a network hop.
// RoundTrip stamps the request context with the embedded MCP trust signal before dispatching,
// so AuthMiddleware can recognise the in-process identity. The context key used is unexported
// inside the security package, so remote callers cannot forge the same signal.
type handlerTransport struct {
	h http.Handler
}

func (t *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Stamp the context with the in-process trust signal before the request reaches the router.
	// This is the only path that can set the embeddedMCPContextKey, because that type is
	// unexported. No header is used, so no remote caller can replicate this.
	stamped := req.WithContext(security.WithEmbeddedMCPContext(req.Context()))
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, stamped)
	return rec.Result(), nil
}

// newInProcessClient creates an API client that routes directly through the Gin router.
// Requests are authenticated as CredentialEmbeddedMCP via the unexported context key.
func newInProcessClient(handler http.Handler, _ string) (*apiclient.ClientWithResponses, error) {
	return apiclient.NewClientWithResponses(
		"http://embedded.local",
		apiclient.WithHTTPClient(&http.Client{Transport: &handlerTransport{h: handler}}),
	)
}
