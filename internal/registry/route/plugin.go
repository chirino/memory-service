package route

import (
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
)

// RouterLoader initializes routes on the gin engine.
type RouterLoader func(r *gin.Engine) error

// Plugin represents a management-route plugin with deterministic mount order.
type Plugin struct {
	Order  int
	Loader RouterLoader
}

var (
	plugins  []Plugin
	sortOnce sync.Once
)

// Register adds a route plugin. Called from init() in plugin packages.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

func sorted() []Plugin {
	sortOnce.Do(func() {
		sort.Slice(plugins, func(i, j int) bool { return plugins[i].Order < plugins[j].Order })
	})
	return plugins
}

// ManagementRouteLoaders returns registered loaders sorted by order.
func ManagementRouteLoaders() []RouterLoader {
	var loaders []RouterLoader
	for _, p := range sorted() {
		loaders = append(loaders, p.Loader)
	}
	return loaders
}
