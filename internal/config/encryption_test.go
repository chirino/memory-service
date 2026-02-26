package config

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeEncryptionKey_HexAndBase64(t *testing.T) {
	hexKey := "00112233445566778899aabbccddeeff"
	key, err := DecodeEncryptionKey(hexKey)
	require.NoError(t, err)
	require.Len(t, key, 16)

	raw := []byte("0123456789abcdef0123456789abcdef")
	b64 := base64.StdEncoding.EncodeToString(raw)
	key, err = DecodeEncryptionKey(b64)
	require.NoError(t, err)
	require.Equal(t, raw, key)
}
