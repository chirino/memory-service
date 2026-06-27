//go:build auth_testfixtures

package security

import "strings"

func resolveClientIDHeader(r *TokenResolver, currentClientID, clientIDHeader string) string {
	if !r.testingMode || currentClientID != "" {
		return currentClientID
	}
	if hdr := strings.TrimSpace(clientIDHeader); hdr != "" {
		return hdr
	}
	return currentClientID
}
