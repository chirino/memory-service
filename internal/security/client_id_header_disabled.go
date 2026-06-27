//go:build !auth_testfixtures

package security

func resolveClientIDHeader(_ *TokenResolver, currentClientID, _ string) string {
	return currentClientID
}
