package txscope

import "context"

// Intent describes the transaction intent attached to a request context.
type Intent string

const (
	IntentRead  Intent = "read"
	IntentWrite Intent = "write"
)

type contextKey struct{}

// WithIntent annotates ctx with the requested transaction intent.
func WithIntent(ctx context.Context, intent Intent) context.Context {
	return context.WithValue(ctx, contextKey{}, intent)
}

// FromContext returns the transaction intent recorded on ctx, if any.
func FromContext(ctx context.Context) (Intent, bool) {
	intent, ok := ctx.Value(contextKey{}).(Intent)
	return intent, ok
}
