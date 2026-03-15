package infinispan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/icholy/digest"
)

// InfinispanClient handles HTTP communication with Infinispan REST API v3.
type InfinispanClient struct {
	baseURL    string
	httpClient *http.Client
}

// Close closes the HTTP client.
func (c *InfinispanClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// CacheExists checks if a cache exists using HEAD request.
func (c *InfinispanClient) CacheExists(ctx context.Context, cacheName string) (bool, error) {
	url := fmt.Sprintf("%s/rest/v3/caches/%s", c.baseURL, cacheName)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("unexpected status checking cache: %d", resp.StatusCode)
}

// RegisterSchema registers a Protobuf schema with Infinispan.
func (c *InfinispanClient) RegisterSchema(ctx context.Context, dimension int) error {
	schemaName := fmt.Sprintf("vector_chunk_%d.proto", dimension)
	schemaContent := strings.ReplaceAll(vectorChunkProtoTemplate, "{DIMENSION}", fmt.Sprintf("%d", dimension))

	url := fmt.Sprintf("%s/rest/v3/caches/___protobuf_metadata/entries/%s", c.baseURL, schemaName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(schemaContent))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to register schema: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// CreateCache creates a new cache with the given configuration.
func (c *InfinispanClient) CreateCache(ctx context.Context, cacheName string, dimension int) error {
	entityName := fmt.Sprintf("VectorItem%d", dimension)
	cacheConfig := strings.ReplaceAll(cacheConfigXMLTemplate, "{CACHE_NAME}", cacheName)
	cacheConfig = strings.ReplaceAll(cacheConfig, "{ENTITY_NAME}", entityName)

	url := fmt.Sprintf("%s/rest/v3/caches/%s", c.baseURL, cacheName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(cacheConfig))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create cache: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// PutEntry inserts or updates an entry in the cache.
func (c *InfinispanClient) PutEntry(ctx context.Context, cacheName, key string, value map[string]interface{}) error {
	url := fmt.Sprintf("%s/rest/v3/caches/%s/entries/%s", c.baseURL, cacheName, key)

	jsonData, err := json.Marshal(value)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to put entry: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// SearchRequest represents an Infinispan search request.
type SearchRequest struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
	QueryMode  string `json:"query_mode"`
}

// SearchResponse represents an Infinispan search response.
type SearchResponse struct {
	HitCount int         `json:"hit_count"`
	Hits     []SearchHit `json:"hits"`
}

// SearchHit represents a single search result.
type SearchHit struct {
	Hit   map[string]interface{} `json:"hit"`
	Score interface{}            `json:"score()"`
}

// Search executes an Ickle query and returns results.
func (c *InfinispanClient) Search(ctx context.Context, cacheName, query string, maxResults int) (*SearchResponse, error) {
	url := fmt.Sprintf("%s/rest/v3/caches/%s/_search", c.baseURL, cacheName)

	searchReq := SearchRequest{
		Query:      query,
		MaxResults: maxResults,
		QueryMode:  "INDEXED",
	}

	jsonData, err := json.Marshal(searchReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search failed: %d - %s", resp.StatusCode, string(body))
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, err
	}

	return &searchResp, nil
}

// DeleteByQuery deletes entries matching an Ickle query.
func (c *InfinispanClient) DeleteByQuery(ctx context.Context, cacheName, query string) error {
	url := fmt.Sprintf("%s/rest/v3/caches/%s/_delete-by-query", c.baseURL, cacheName)

	searchReq := SearchRequest{
		Query:     query,
		QueryMode: "INDEXED",
	}

	jsonData, err := json.Marshal(searchReq)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete by query failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// authTransport implements HTTP authentication (Basic or Digest).
type authTransport struct {
	username string
	password string
	authType string
	base     http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.authType == "basic" {
		// Clone request to avoid modifying original
		req2 := req.Clone(req.Context())
		req2.SetBasicAuth(t.username, t.password)
		return t.base.RoundTrip(req2)
	}

	// For digest auth, use the digest library which properly handles challenge-response
	digestTransport := &digest.Transport{
		Username:  t.username,
		Password:  t.password,
		Transport: t.base,
	}
	return digestTransport.RoundTrip(req)
}
