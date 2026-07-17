package dataencryption_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/plain"
	"github.com/stretchr/testify/require"
)

const serviceTestKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptedPrimaryRejectsHeaderlessPlaintext(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek,plain",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	_, err = svc.DecryptField([]byte("plaintext"), "entry.content", "entry-1")
	require.ErrorContains(t, err, "expected MSEH v4 field envelope")
}

func TestEncryptedPrimaryRejectsHeaderlessStream(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek,plain",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	_, err = svc.DecryptStream(bytes.NewReader([]byte("legacy plaintext")))
	require.ErrorContains(t, err, "expected MSEH v3 attachment stream")
}

func TestMalformedMSEHNeverFallsBackToPlaintext(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek,plain",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	malformed := append([]byte("MSEH"), 0xff, 0xff, 0xff, 0xff)
	_, err = svc.DecryptField(malformed, "entry.content", "entry-1")
	require.Error(t, err)

	_, err = svc.DecryptStream(bytes.NewReader(malformed))
	require.Error(t, err)
}

func TestEncryptFieldBindsDomainAndIdentity(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)
	ct, err := svc.EncryptField([]byte("secret title"), "conversation.title", "conversation-1")
	require.NoError(t, err)
	require.True(t, dataencryption.HasMagic(ct))

	got, err := svc.DecryptField(ct, "conversation.title", "conversation-1")
	require.NoError(t, err)
	require.Equal(t, []byte("secret title"), got)

	_, err = svc.DecryptField(ct, "entry.content", "conversation-1")
	require.Error(t, err)
	_, err = svc.DecryptField(ct, "conversation.title", "conversation-2")
	require.Error(t, err)

	ct[len(ct)-1] ^= 0x01
	_, err = svc.DecryptField(ct, "conversation.title", "conversation-1")
	require.Error(t, err)
}

func TestPrimaryPlainCanReadHeaderlessData(t *testing.T) {
	cfg := &config.Config{EncryptionProviders: "plain"}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)
	fieldGot, err := svc.EncryptField([]byte("plaintext field"), "entry.content", "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext field"), fieldGot)
	require.False(t, dataencryption.HasMagic(fieldGot))

	fieldGot, err = svc.DecryptField(fieldGot, "entry.content", "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext field"), fieldGot)

	reader, err := svc.DecryptStream(bytes.NewReader([]byte("plaintext")))
	require.NoError(t, err)
	streamGot, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext"), streamGot)
}
