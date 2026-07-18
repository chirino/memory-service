package infinispan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/operationevent"
)

func TestProviderErrorOmitsResponseAndExposesTypedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", "provider-request-2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("private upstream response and credentials"))
	}))
	defer server.Close()

	client := &InfinispanClient{baseURL: server.URL, httpClient: server.Client()}
	_, err := client.Search(context.Background(), "cache", "from private", 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "private upstream response") || strings.Contains(err.Error(), "credentials") {
		t.Fatalf("response content leaked: %v", err)
	}
	var detailer operationevent.ErrorDetailer
	if !errors.As(err, &detailer) {
		t.Fatalf("error does not expose typed details: %T", err)
	}
	details := detailer.OperationErrorDetails()
	if details.Provider == nil || details.Provider.Name != "infinispan" || details.Provider.StatusCode != http.StatusServiceUnavailable || details.Provider.TransactionID != "provider-request-2" || details.Reason != "request_rejected" {
		t.Fatalf("unexpected details: %#v", details)
	}
}
