//go:build !noinfinispan

package buildcaps

func init() {
	Infinispan = true
}
