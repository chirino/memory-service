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

func TestDecryptHeaderlessPlaintextRequiresExplicitLegacyFlag(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek,plain",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	_, err = svc.Decrypt([]byte("legacy plaintext"))
	require.ErrorContains(t, err, "expected MSEH envelope")

	cfg.EncryptionLegacyPlainReadEnabled = true
	svc, err = dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	got, err := svc.Decrypt([]byte("legacy plaintext"))
	require.NoError(t, err)
	require.Equal(t, []byte("legacy plaintext"), got)
}

func TestDecryptStreamHeaderlessPlaintextRequiresExplicitLegacyFlag(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek,plain",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	_, err = svc.DecryptStream(bytes.NewReader([]byte("legacy plaintext")))
	require.ErrorContains(t, err, "DecryptStream requires a parsed MSEH header")

	cfg.EncryptionLegacyPlainReadEnabled = true
	svc, err = dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	reader, err := svc.DecryptStream(bytes.NewReader([]byte("legacy plaintext")))
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("legacy plaintext"), got)
}

func TestMalformedMSEHNeverFallsBackToPlaintext(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders:              "dek,plain",
		EncryptionKey:                    serviceTestKeyHex,
		EncryptionLegacyPlainReadEnabled: true,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	malformed := append([]byte("MSEH"), 0xff, 0xff, 0xff, 0xff)
	_, err = svc.Decrypt(malformed)
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
	require.True(t, svc.PrimarySupportsFieldEncryption())

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

func TestDecryptFieldReadsLegacyV1Envelope(t *testing.T) {
	cfg := &config.Config{
		EncryptionProviders: "dek",
		EncryptionKey:       serviceTestKeyHex,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	legacy, err := svc.Encrypt([]byte("legacy"))
	require.NoError(t, err)

	got, err := svc.DecryptField(legacy, "conversation.title", "conversation-1")
	require.NoError(t, err)
	require.Equal(t, []byte("legacy"), got)
}

func TestPrimaryPlainCanReadHeaderlessData(t *testing.T) {
	cfg := &config.Config{EncryptionProviders: "plain"}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, "plain", svc.PrimaryProviderID())
	require.False(t, svc.PrimarySupportsFieldEncryption())

	got, err := svc.Decrypt([]byte("plaintext"))
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext"), got)

	reader, err := svc.DecryptStream(bytes.NewReader([]byte("plaintext")))
	require.NoError(t, err)
	streamGot, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext"), streamGot)
}
