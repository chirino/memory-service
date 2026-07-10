package serve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsStreamingRequest(t *testing.T) {
	t.Run("multipart attachment upload is streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader("abcdef"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
		require.True(t, isStreamingRequest(req))
	})

	t.Run("json attachment create is not streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader(`{"sourceUrl":"https://example.com/file"}`))
		req.Header.Set("Content-Type", "application/json")
		require.False(t, isStreamingRequest(req))
	})

	t.Run("non-attachment endpoint is not streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/evict", strings.NewReader(`{"retentionPeriod":"P90D"}`))
		req.Header.Set("Content-Type", "application/json")
		require.False(t, isStreamingRequest(req))
	})
}

func TestMaxBodySizeMiddleware_SkipsForMultipartAttachmentUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(maxBodySizeMiddleware(4))
	router.POST("/v1/attachments", readBodyLengthHandler)

	req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader("0123456789"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "10", rec.Body.String())
}

func TestMaxBodySizeMiddleware_EnforcesForNonStreamingEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(maxBodySizeMiddleware(4))
	router.POST("/v1/admin/evict", readBodyLengthHandler)

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/evict", strings.NewReader("0123456789"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestMaxPageSizeMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := config.DefaultConfig()
	cfg.MaxPageSize = 3
	router.Use(maxPageSizeMiddleware(&cfg))
	router.GET("/items", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"defaultPageSize": config.ClampPageSize(c.Request.Context(), 50)})
	})

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?limit=3", http.StatusOK},
		{"?limit=4", http.StatusBadRequest},
		{"?limit=invalid", http.StatusBadRequest},
		{"?limit=0", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items"+tc.query, nil))
		require.Equal(t, tc.want, rec.Code, tc.query)
		if tc.query == "?limit=3" {
			require.JSONEq(t, `{"defaultPageSize":3}`, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"defaultPageSize":3}`, rec.Body.String())
}

func TestMaxPageSizeUnaryInterceptor(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxPageSize = 3
	interceptor := maxPageSizeUnaryInterceptor(&cfg)
	handler := func(ctx context.Context, _ any) (any, error) {
		return config.ClampPageSize(ctx, 20), nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/memory.v1.EntriesService/ListEntries"}

	_, err := interceptor(context.Background(), &pb.ListEntriesRequest{
		Page: &pb.PageRequest{PageSize: 4},
	}, info, handler)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	result, err := interceptor(context.Background(), &pb.ListEntriesRequest{
		Page: &pb.PageRequest{PageSize: 3},
	}, info, handler)
	require.NoError(t, err)
	require.Equal(t, 3, result)

	_, err = interceptor(context.Background(), &pb.SearchEntriesRequest{Limit: 4}, info, handler)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func readBodyLengthHandler(c *gin.Context) {
	n, err := io.Copy(io.Discard, c.Request.Body)
	if err != nil {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	c.String(http.StatusOK, "%d", n)
}

func TestLoadServerCertificate_RequiresExplicitFiles(t *testing.T) {
	_, err := loadServerCertificate("", "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "automatic self-signed certificates are disabled")

	_, err = loadServerCertificate("cert.pem", "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires both certificate and key files")
}

func TestLoadServerCertificate_SelfSigned(t *testing.T) {
	cert, err := loadServerCertificate("", "", true)
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate)
}

func TestFlagsIncludeOIDCTLSSkipVerify(t *testing.T) {
	cfg := config.DefaultConfig()
	flags := Flags(&cfg, NewFlagState(&cfg))

	flagNames := make(map[string]bool, len(flags))
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	require.True(t, flagNames["oidc-tls-insecure-skip-verify"])
	require.True(t, flagNames["max-page-size"])
}

func TestMaxPageSizeFlagAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want int
	}{
		{name: "flag", args: []string{"test", "--max-page-size", "37"}, want: 37},
		{name: "environment", args: []string{"test"}, env: "41", want: 41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			var maxPageSizeFlag cli.Flag
			for _, flag := range serverFlags(&cfg, NewFlagState(&cfg)) {
				if slices.Contains(flag.Names(), "max-page-size") {
					maxPageSizeFlag = flag
					break
				}
			}
			require.NotNil(t, maxPageSizeFlag)
			if tc.env != "" {
				t.Setenv("MEMORY_SERVICE_MAX_PAGE_SIZE", tc.env)
			}
			cmd := &cli.Command{
				Name:  "test",
				Flags: []cli.Flag{maxPageSizeFlag},
				Action: func(context.Context, *cli.Command) error {
					require.Equal(t, tc.want, cfg.MaxPageSize)
					return nil
				},
			}
			require.NoError(t, cmd.Run(context.Background(), tc.args))
		})
	}
}
