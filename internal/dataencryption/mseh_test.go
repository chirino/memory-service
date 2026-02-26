package dataencryption_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/stretchr/testify/require"
)

// TestRoundTrip verifies that WriteHeader and ReadHeader are inverses.
func TestRoundTrip(t *testing.T) {
	headers := []dataencryption.Header{
		{Version: 1, ProviderID: "dek", Nonce: make([]byte, 12)},
		{Version: 1, ProviderID: "vault", Nonce: make([]byte, 12)},
		{Version: 1, ProviderID: "kms", Nonce: bytes.Repeat([]byte{0xAB}, 12)},
	}
	for _, h := range headers {
		var buf bytes.Buffer
		require.NoError(t, dataencryption.WriteHeader(&buf, h))

		got, hasMagic, err := dataencryption.ReadHeader(&buf)
		require.NoError(t, err)
		require.True(t, hasMagic)
		require.Equal(t, h.Version, got.Version)
		require.Equal(t, h.ProviderID, got.ProviderID)
		require.Equal(t, h.Nonce, got.Nonce)
	}
}

// TestHasMagic checks that HasMagic correctly identifies MSEH-prefixed data.
func TestHasMagic(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, dataencryption.WriteHeader(&buf, dataencryption.Header{
		Version: 1, ProviderID: "dek", Nonce: make([]byte, 12),
	}))
	ciphertext := append(buf.Bytes(), []byte("payload")...)

	require.True(t, dataencryption.HasMagic(ciphertext))
	require.False(t, dataencryption.HasMagic([]byte("not MSEH")))
	require.False(t, dataencryption.HasMagic(nil))
	require.False(t, dataencryption.HasMagic([]byte{0x4D, 0x53})) // too short
}

// TestReadHeaderNoMagic verifies that ReadHeader returns (nil, false, nil) for non-MSEH data.
func TestReadHeaderNoMagic(t *testing.T) {
	h, hasMagic, err := dataencryption.ReadHeader(bytes.NewReader([]byte("plaintext data")))
	require.NoError(t, err)
	require.False(t, hasMagic)
	require.Nil(t, h)
}

// TestWireFormat verifies the exact byte layout expected by Java's EncryptionHeader proto.
// Layout: [4 magic][varint proto_len][field1: version][field2: provider_id][field3: iv]
func TestWireFormat(t *testing.T) {
	iv := make([]byte, 12) // all zeros

	var buf bytes.Buffer
	require.NoError(t, dataencryption.WriteHeader(&buf, dataencryption.Header{
		Version:    1,
		ProviderID: "dek",
		Nonce:      iv,
	}))
	b := buf.Bytes()

	// Magic
	require.Equal(t, []byte{0x4D, 0x53, 0x45, 0x48}, b[:4])

	// Proto bytes start at offset 4 (after varint length)
	// varint32 for proto length: single byte since proto is small
	protoLen := int(b[4]) // should be < 128
	proto := b[5 : 5+protoLen]

	// Field 1: version=1 → tag 0x08, value 0x01
	require.Equal(t, byte(0x08), proto[0])
	require.Equal(t, byte(0x01), proto[1])

	// Field 2: provider_id="dek" → tag 0x12, len 0x03, "dek"
	require.Equal(t, byte(0x12), proto[2])
	require.Equal(t, byte(0x03), proto[3])
	require.Equal(t, []byte("dek"), proto[4:7])

	// Field 3: iv (12 zero bytes) → tag 0x1A, len 0x0C, 12×0x00
	require.Equal(t, byte(0x1A), proto[7])
	require.Equal(t, byte(0x0C), proto[8])
	require.Equal(t, make([]byte, 12), proto[9:21])

	// Total length sanity check
	// 2 (field1) + 2 + 3 (field2) + 2 + 12 (field3) = 21 bytes
	require.Equal(t, 21, protoLen)
}

// TestBigEndianUnused ensures encoding/binary big-endian is available for test use.
func TestBigEndianUnused(t *testing.T) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, 0x4D534548)
	require.Equal(t, []byte{0x4D, 0x53, 0x45, 0x48}, b)
}
