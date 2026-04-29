package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/chirino/memory-service/internal/generated/apiclient"
)

type handlerTransport struct {
	h http.Handler
}

func (t *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func newInProcessClient(handler http.Handler) (*apiclient.ClientWithResponses, error) {
	return apiclient.NewClientWithResponses(
		"http://embedded.local",
		apiclient.WithHTTPClient(&http.Client{Transport: &handlerTransport{h: handler}}),
		apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-API-Key", embeddedAPIKey)
			req.Header.Set("Authorization", "Bearer "+embeddedBearerToken)
			return nil
		}),
	)
}
