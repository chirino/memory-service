// Package episodic provides namespace encoding/decoding helpers and OPA policy
// integration for the namespaced episodic memory system.
package episodic

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	// namespaceSep is the Record Separator (ASCII 30) used to join encoded namespace segments.
	// Percent-encoding guarantees no segment ever contains this character.
	namespaceSep = "\x1e"
)

// EncodeNamespace encodes a []string namespace into a single storage string.
// Each segment is percent-encoded (url.PathEscape), then joined with \x1e (RS).
// Returns an error if any segment is empty or if depth > maxDepth.
func EncodeNamespace(segments []string, maxDepth int) (string, error) {
	if len(segments) == 0 {
		return "", fmt.Errorf("namespace must have at least one segment")
	}
	if maxDepth > 0 && len(segments) > maxDepth {
		return "", fmt.Errorf("namespace depth %d exceeds configured limit %d", len(segments), maxDepth)
	}
	encoded := make([]string, len(segments))
	for i, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("namespace segment %d is empty", i)
		}
		encoded[i] = url.PathEscape(seg)
	}
	return strings.Join(encoded, namespaceSep), nil
}

// DecodeNamespace decodes a storage string back into a []string namespace.
func DecodeNamespace(encoded string) ([]string, error) {
	if encoded == "" {
		return nil, fmt.Errorf("encoded namespace is empty")
	}
	parts := strings.Split(encoded, namespaceSep)
	segments := make([]string, len(parts))
	for i, part := range parts {
		seg, err := url.PathUnescape(part)
		if err != nil {
			return nil, fmt.Errorf("failed to decode namespace segment %d %q: %w", i, part, err)
		}
		segments[i] = seg
	}
	return segments, nil
}

// NamespacePrefixPattern returns the SQL LIKE pattern that matches namespaces under the given prefix.
// The pattern matches the prefix exactly or any descendant, using the RS separator as the delimiter
// so "users\x1ealice" never matches "users\x1ealiced".
func NamespacePrefixPattern(prefixEncoded string) string {
	// Escape SQL LIKE metacharacters in the prefix itself.
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefixEncoded)
	// Match the prefix exactly OR any descendant (prefix + RS + anything).
	return escaped + namespaceSep + "%"
}

// NamespaceMatchesExact returns true if encoded equals the encoded prefix exactly.
func NamespaceMatchesExact(encoded, prefixEncoded string) bool {
	return encoded == prefixEncoded
}

// NamespaceHasPrefix returns true if encoded == prefixEncoded OR starts with prefixEncoded + RS.
func NamespaceHasPrefix(encoded, prefixEncoded string) bool {
	return encoded == prefixEncoded || strings.HasPrefix(encoded, prefixEncoded+namespaceSep)
}

// NamespaceTruncate returns the first depth segments of the encoded namespace,
// re-encoded. If depth >= actual depth, returns the encoded namespace unchanged.
func NamespaceTruncate(encoded string, depth int) string {
	parts := strings.SplitN(encoded, namespaceSep, depth+1)
	if len(parts) <= depth {
		return encoded
	}
	return strings.Join(parts[:depth], namespaceSep)
}

// NamespaceDepth returns the number of segments in the encoded namespace.
func NamespaceDepth(encoded string) int {
	return strings.Count(encoded, namespaceSep) + 1
}

// MatchesSuffix returns true if the decoded namespace ends with each segment in suffix.
func MatchesSuffix(encoded string, suffix []string) bool {
	if len(suffix) == 0 {
		return true
	}
	segments, err := DecodeNamespace(encoded)
	if err != nil || len(segments) < len(suffix) {
		return false
	}
	tail := segments[len(segments)-len(suffix):]
	for i, s := range suffix {
		if tail[i] != s {
			return false
		}
	}
	return true
}
