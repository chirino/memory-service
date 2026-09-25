package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
)

// gin.New() trusts every proxy until SetTrustedProxies is called, so both the
// public and management routers must ignore X-Forwarded-For when no trusted
// proxy CIDRs are configured.
func TestConfiguredRoutersIgnoreForwardedForWithoutTrustedProxies(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TrustedProxyCIDRs = ""
	tp := noop.NewTracerProvider()
	prop := propagation.NewCompositeTextMapPropagator()

	cases := map[string]routerOptions{
		"public":     {includePublic: true, trustedProxies: cfg.TrustedProxyCIDRs},
		"management": {includePublic: false},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			router, err := newConfiguredRouter(&cfg, tp, prop, prop, opts)
			require.NoError(t, err)
			router.GET("/client-ip", func(c *gin.Context) {
				c.String(http.StatusOK, c.ClientIP())
			})

			req := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
			req.RemoteAddr = "192.0.2.10:4321"
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			req.Header.Set("X-Real-IP", "203.0.113.8")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "192.0.2.10", w.Body.String())
		})
	}
}
