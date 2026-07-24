//go:build !noopenai

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/operationevent"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	"github.com/urfave/cli/v3"
)

func init() {
	registryembed.Register(registryembed.Plugin{
		Name:   "openai",
		Loader: load,
		Flags: func(cfg *config.Config) []cli.Flag {
			return []cli.Flag{
				&cli.StringFlag{
					Name:        "embedding-openai-api-key",
					Category:    "Embedding:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY", "MEMORY_SERVICE_OPENAI_API_KEY", "OPENAI_API_KEY"),
					Destination: &cfg.OpenAIAPIKey,
					Usage:       "OpenAI API key",
				},
			}
		},
	})
}

func load(ctx context.Context) (registryembed.Embedder, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.OpenAIAPIKey == "" {
		return nil, fmt.Errorf("openai embedder: MEMORY_SERVICE_OPENAI_API_KEY is required")
	}

	embedder := &OpenAIEmbedder{
		apiKey:     cfg.OpenAIAPIKey,
		model:      cfg.OpenAIModelName,
		baseURL:    strings.TrimRight(cfg.OpenAIBaseURL, "/"),
		dimensions: cfg.OpenAIDimensions,
		defaultDim: cfg.OpenAIDimensions,
	}

	// If dimensions not configured, auto-detect by doing a test embedding
	if cfg.OpenAIDimensions <= 0 {
		embeddings, err := embedder.EmbedTexts(ctx, []string{"test"})
		if err != nil {
			// If auto-detect fails, fall back to a common default (1536)
			// This allows the service to start even when the embedding API is unreachable
			embedder.defaultDim = 1536
		} else if len(embeddings) > 0 && len(embeddings[0]) > 0 {
			embedder.defaultDim = len(embeddings[0])
		}
	}

	return embedder, nil
}

type OpenAIEmbedder struct {
	apiKey     string
	model      string
	baseURL    string
	dimensions int
	defaultDim int
}

func (e *OpenAIEmbedder) ModelName() string {
	return e.model
}

func (e *OpenAIEmbedder) Dimension() int {
	return e.defaultDim
}

type embeddingRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody, err := json.Marshal(embeddingRequest{
		Input:      texts,
		Model:      e.model,
		Dimensions: ptrIfPositive(e.dimensions),
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, openAIProviderError(fmt.Errorf("embedding request failed: %w", err), 0, "request_failed", "")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openAIProviderError(fmt.Errorf("embedding response read failed: %w", err), resp.StatusCode, "response_read_failed", resp.Header.Get("X-Request-ID"))
	}

	// Check status code before attempting JSON parse
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, openAIProviderError(fmt.Errorf("embedding authentication failed with status %d", resp.StatusCode), resp.StatusCode, "authentication_failed", resp.Header.Get("X-Request-ID"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, openAIProviderError(fmt.Errorf("embedding request failed with status %d", resp.StatusCode), resp.StatusCode, "request_rejected", resp.Header.Get("X-Request-ID"))
	}

	var result embeddingResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, openAIProviderError(fmt.Errorf("embedding response was invalid: %w", err), resp.StatusCode, "invalid_response", resp.Header.Get("X-Request-ID"))
	}
	if result.Error != nil {
		return nil, openAIProviderError(fmt.Errorf("embedding provider returned an error"), resp.StatusCode, "provider_error", resp.Header.Get("X-Request-ID"))
	}
	if len(result.Data) != len(texts) {
		return nil, openAIProviderError(fmt.Errorf("embedding response count mismatch"), resp.StatusCode, "invalid_response", resp.Header.Get("X-Request-ID"))
	}

	// The API may return results in any order; sort by index.
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		embeddings[d.Index] = d.Embedding
	}
	return embeddings, nil
}

func openAIProviderError(err error, statusCode int, reason, transactionID string) error {
	return operationevent.WithErrorDetails(err, operationevent.ErrorDetails{
		ErrorType: "provider",
		ErrorCode: "embedding_provider_error",
		Reason:    reason,
		Provider: &operationevent.ErrorDetailsProvider{
			Name:          "openai",
			StatusCode:    statusCode,
			TransactionID: transactionID,
		},
	})
}

func ptrIfPositive(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func redactAPIKey(msg, apiKey string) string {
	if apiKey == "" {
		return msg
	}
	return strings.ReplaceAll(msg, apiKey, "[REDACTED_OPENAI_API_KEY]")
}
