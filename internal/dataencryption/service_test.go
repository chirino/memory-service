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

func TestPrimaryPlainCanReadHeaderlessData(t *testing.T) {
	cfg := &config.Config{EncryptionProviders: "plain"}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	got, err := svc.Decrypt([]byte("plaintext"))
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext"), got)

	reader, err := svc.DecryptStream(bytes.NewReader([]byte("plaintext")))
	require.NoError(t, err)
	streamGot, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("plaintext"), streamGot)
}
