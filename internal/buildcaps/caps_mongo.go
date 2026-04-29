//go:build !nomongo

package buildcaps

func init() {
	MongoDB = true
}
