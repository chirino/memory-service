package disabled

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/registry/embed"
)

func init() {
	embed.Register(embed.Plugin{
		Name: "none",
		Loader: func(ctx context.Context) (embed.Embedder, error) {
			return &disabledEmbedder{}, nil
		},
	})
}

type disabledEmbedder struct{}

func (d *disabledEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("embedding is disabled")
}

func (d *disabledEmbedder) ModelName() string { return "none" }
func (d *disabledEmbedder) Dimension() int    { return 0 }

var _ embed.Embedder = (*disabledEmbedder)(nil)
