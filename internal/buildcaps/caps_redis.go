//go:build !noredis

package buildcaps

func init() {
	Redis = true
}
