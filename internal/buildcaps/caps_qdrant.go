//go:build !noqdrant

package buildcaps

func init() {
	Qdrant = true
}
