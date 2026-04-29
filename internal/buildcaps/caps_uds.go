//go:build !nouds

package buildcaps

func init() {
	UDSListener = true
}
