//go:build site_tests

package sitebdd

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"
)

// IssueToken creates and signs an RS256 JWT for the given username.
// The token has a 1-hour expiry and uses the mock server's URL as the issuer.
func (m *MockServer) IssueToken(username string) (string, error) {
	if m.jwtKey == nil {
		return "", fmt.Errorf("mock server not started (jwtKey is nil)")
	}

	headerB64 := base64url(mustJSON(map[string]any{
		"alg": "RS256",
		"typ": "JWT",
		"kid": "test-1",
	}))

	now := time.Now()
	payloadB64 := base64url(mustJSON(map[string]any{
		"sub":                username,
		"preferred_username": username,
		"iss":                m.server.URL,
		"iat":                now.Unix(),
		"exp":                now.Add(1 * time.Hour).Unix(),
	}))

	sigInput := headerB64 + "." + payloadB64
	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.jwtKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// jwkPublicKey returns the RSA public key as a JWK map for the JWKS endpoint.
func (m *MockServer) jwkPublicKey() map[string]any {
	pub := &m.jwtKey.PublicKey

	// Encode modulus N as big-endian bytes, base64url (no padding)
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())

	// Encode exponent E as minimal big-endian bytes, base64url
	eBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(eBuf, uint32(pub.E))
	for len(eBuf) > 1 && eBuf[0] == 0 {
		eBuf = eBuf[1:]
	}
	e := base64.RawURLEncoding.EncodeToString(eBuf)

	return map[string]any{
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"kid": "test-1",
		"n":   n,
		"e":   e,
	}
}

// base64url returns the base64url-encoded (no padding) representation of data.
func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// mustJSON marshals v to JSON or panics.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return b
}
