package routetx

import (
	"context"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/gin-gonic/gin"
)

func MemoryRead(c *gin.Context, store registrystore.MemoryStore, fn func(context.Context) error) error {
	return store.InReadTx(c.Request.Context(), func(ctx context.Context) error {
		return withContext(c, ctx, fn)
	})
}

func MemoryWrite(c *gin.Context, store registrystore.MemoryStore, fn func(context.Context) error) error {
	return store.InWriteTx(c.Request.Context(), func(ctx context.Context) error {
		return withContext(c, ctx, fn)
	})
}

func EpisodicRead(c *gin.Context, store registryepisodic.EpisodicStore, fn func(context.Context) error) error {
	return store.InReadTx(c.Request.Context(), func(ctx context.Context) error {
		return withContext(c, ctx, fn)
	})
}

func EpisodicWrite(c *gin.Context, store registryepisodic.EpisodicStore, fn func(context.Context) error) error {
	return store.InWriteTx(c.Request.Context(), func(ctx context.Context) error {
		return withContext(c, ctx, fn)
	})
}

func withContext(c *gin.Context, ctx context.Context, fn func(context.Context) error) error {
	original := c.Request
	c.Request = c.Request.WithContext(ctx)
	defer func() {
		c.Request = original
	}()
	return fn(ctx)
}
