# The Init-Registration Plugin Pattern

Each component registers itself via Go's `init()` mechanism. The entry point pulls all registrations together via a blank import — the same pattern used by `database/sql` drivers and `image` decoders.

---

## Three Roles

| Role | Responsibility | Package |
|---|---|---|
| **Registry** | Defines the plugin interface, holds the global slice, exposes `Register()` and `Loaders()` | `internal/registry/route` |
| **Plugin** | Implements the interface; calls `Register()` in `init()` | `internal/plugin/route`, `internal/system/route` |
| **Consumer** | Blank-imports plugins to trigger `init()`, then calls `Loaders()` | `internal/system` |

The registry must be a leaf package — importing it must not create cycles. Plugins import the registry. The consumer blank-imports the plugins.

---

## Package Structure

```
internal/
  registry/
    route/
      plugin.go     ← REGISTRY
    workflow/
      plugin.go     ← REGISTRY
  system/
    router.go       ← CONSUMER: blank imports system/route and plugin/route
    worker.go       ← CONSUMER: blank imports plugin/workflow
    route/
      auth.go       ← PLUGIN
      static.go     ← PLUGIN
  plugin/
    route/
      account.go    ← PLUGIN
      automation.go ← PLUGIN
    workflow/
      instagram_direct_message.go ← PLUGIN
      instagram_poll_next_post.go ← PLUGIN
```

---

## Registry

```go
// internal/registry/route/plugin.go
package route

type RouterLoader func(ctx context.Context, config config.Server, router *gin.Engine) error

type Plugin struct {
    Order  int
    Loader RouterLoader
}

var plugins []Plugin
var sortOnce sync.Once

func Register(p Plugin) {
    plugins = append(plugins, p)
}

func RouteLoaders() []RouterLoader {
    sortOnce.Do(func() {
        sort.Slice(plugins, func(i, j int) bool { return plugins[i].Order < plugins[j].Order })
    })
    loaders := make([]RouterLoader, len(plugins))
    for i, p := range plugins { loaders[i] = p.Loader }
    return loaders
}
```

`Order` controls sequencing declaratively. `sync.Once` ensures sorting happens exactly once.

---

## Plugin

```go
// internal/plugin/route/account.go
package route

import (
    . "github.com/chirino/memory-service/internal/registry/route"
    // ...
)

func init() {
    Register(Plugin{
        Order: 100,
        Loader: func(ctx context.Context, o config.Server, r *gin.Engine) error {
            g := r.Group("/api/accounts", Log, RequireAuthenticatedUser)
            g.GET("/", account.ListAccounts)
            g.PUT("/:account_id", RequireAccountID, account.UpdateAccount)
            return nil
        },
    })
}
```

The dot-import (`. "...plugin/route"`) makes `Register` and `Plugin` read as local — use it only in plugin files.

---

## Consumer

```go
// internal/system/router.go
import (
    "github.com/chirino/memory-service/internal/registry/route"
    _ "github.com/chirino/memory-service/internal/plugin/route"  // trigger init()
    _ "github.com/chirino/memory-service/internal/system/route"  // trigger init()
)

func NewRouter(ctx context.Context, o config.Server) (http.Handler, error) {
    router := gin.New()
    for _, loader := range registry.RouteLoaders() {
        if err := loader(ctx, o, router); err != nil {
            return nil, fmt.Errorf("unable to load handler: %w", err)
        }
    }
    return router, nil
}
```

Adding a new plugin package = one blank import here. Nothing else changes.

---

## When to Use

**Good fit**: A set of same-kind components (routes, workers, event handlers) that should be addable without modifying a central file.

**Poor fit**: Plugins need runtime load/unload; plugins need to communicate with each other; the plugin interface has multiple methods (consider `fx` instead).

---

## Testing

Because `init()` is permanent for the process lifetime, workflow plugins can branch on `testing.Testing()`:

```go
func init() {
    Register(Plugin{Loader: func(ctx context.Context, o config.Server, w worker.Worker) error {
        w.RegisterWorkflow(myWorkflow)
        if testing.Testing() {
            w.RegisterActivity(MockMyActivity)
        } else {
            w.RegisterActivity(MyActivity)
        }
        return nil
    }})
}
```

Route plugins typically don't need this — tests compose their own minimal router.
