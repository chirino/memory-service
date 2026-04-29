//go:build !notcp

package buildcaps

func init() {
	TCPListener = true
}
