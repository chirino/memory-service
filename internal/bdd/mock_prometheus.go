package bdd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// MockPrometheus is a controllable mock Prometheus server for BDD tests.
type MockPrometheus struct {
	Server    *httptest.Server
	mu        sync.Mutex
	available bool
}

// NewMockPrometheus creates a mock Prometheus that returns canned time-series responses.
func NewMockPrometheus(t *testing.T) *MockPrometheus {
	t.Helper()
	mp := &MockPrometheus{available: true}
	mp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mp.mu.Lock()
		available := mp.available
		mp.mu.Unlock()

		if !available {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"error","error":"unavailable"}`))
			return
		}

		query := r.URL.Query().Get("query")
		if strings.Contains(query, "by (") {
			_, _ = w.Write([]byte(`{
  "status":"success",
  "data":{
    "resultType":"matrix",
    "result":[
      {"metric":{"operation":"createConversation"},"values":[[1704067200,"0.025"],[1704067260,"0.028"]]},
      {"metric":{"operation":"appendMemoryEntries"},"values":[[1704067200,"0.045"],[1704067260,"0.052"]]}
    ]
  }
}`))
			return
		}
		_, _ = w.Write([]byte(`{
  "status":"success",
  "data":{
    "resultType":"matrix",
    "result":[{"metric":{},"values":[[1704067200,"42.5"],[1704067260,"45.2"],[1704067320,"47.8"]]}]
  }
}`))
	}))
	t.Cleanup(mp.Server.Close)
	return mp
}

// SetAvailable toggles whether mock Prometheus returns success or 503.
func (m *MockPrometheus) SetAvailable(value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available = value
}
