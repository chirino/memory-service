package route

import (
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
)

// RouterLoader initializes routes on the gin engine.
type RouterLoader func(r *gin.Engine) error

// RouteType distinguishes which server a plugin's routes belong to.
type RouteType int

const (
	// RouteTypeMain registers routes on the main API server.
	RouteTypeMain RouteType = iota
	// RouteTypeManagement registers routes on the management server (health, metrics).
	// When no dedicated management port is configured, these are mounted on the main server.
	RouteTypeManagement
)

// Plugin represents a route plugin with an order for deterministic mount sequence.
type Plugin struct {
	Order  int
	Type   RouteType
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

// MainRouteLoaders returns loaders for RouteTypeMain plugins, sorted by order.
func MainRouteLoaders() []RouterLoader {
	var loaders []RouterLoader
	for _, p := range sorted() {
		if p.Type == RouteTypeMain {
			loaders = append(loaders, p.Loader)
		}
	}
	return loaders
}

// ManagementRouteLoaders returns loaders for RouteTypeManagement plugins, sorted by order.
func ManagementRouteLoaders() []RouterLoader {
	var loaders []RouterLoader
	for _, p := range sorted() {
		if p.Type == RouteTypeManagement {
			loaders = append(loaders, p.Loader)
		}
	}
	return loaders
}
