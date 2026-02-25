package local

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	registryembed "github.com/chirino/memory-service/internal/registry/embed"
)

const (
	modelName = "all-minilm-l6-v2"
	dimension = 384
)

func init() {
	registryembed.Register(registryembed.Plugin{
		Name: "local",
		Loader: func(_ context.Context) (registryembed.Embedder, error) {
			return &LocalEmbedder{}, nil
		},
	})
}

type LocalEmbedder struct{}

func (e *LocalEmbedder) ModelName() string {
	return modelName
}

func (e *LocalEmbedder) Dimension() int {
	return dimension
}

func (e *LocalEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		results[i] = embedOne(text)
	}
	return results, nil
}

func embedOne(text string) []float32 {
	vector := make([]float32, dimension)
	tokens := tokenize(text)
	for _, tok := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		i := int(h.Sum64() % uint64(dimension))
		vector[i] += 1
	}
	norm := float32(0)
	for _, v := range vector {
		norm += v * v
	}
	if norm == 0 {
		return vector
	}
	inv := 1 / float32(math.Sqrt(float64(norm)))
	for i := range vector {
		vector[i] *= inv
	}
	return vector
}

func tokenize(text string) []string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	})
	return fields
}

var _ registryembed.Embedder = (*LocalEmbedder)(nil)
