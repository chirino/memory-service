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
		return nil, fmt.Errorf("openai embed request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embed: read response: %w", err)
	}

	// Check status code before attempting JSON parse
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("openai embed: authentication failed with status %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai embed: request failed with status %d: %s", resp.StatusCode, redactAPIKey(string(body), e.apiKey))
	}

	var result embeddingResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("openai embed: parse response (status %d): %w, body: %s", resp.StatusCode, err, redactAPIKey(string(body), e.apiKey))
	}
	if result.Error != nil {
		return nil, fmt.Errorf("openai embed error: %s", redactAPIKey(result.Error.Message, e.apiKey))
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("openai embed: expected %d embeddings, got %d", len(texts), len(result.Data))
	}

	// The API may return results in any order; sort by index.
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		embeddings[d.Index] = d.Embedding
	}
	return embeddings, nil
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
