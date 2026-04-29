//go:build !noawskms

package buildcaps

func init() {
	AWSKMS = true
}
