//go:build !novault

package buildcaps

func init() {
	Vault = true
}
