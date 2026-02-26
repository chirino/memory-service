// Package dataencryption provides the MSEH envelope format and DataEncryptionService.
//
// Wire format (Java-compatible):
//
//	[4 bytes: 0x4D 0x53 0x45 0x48]  "MSEH" magic
//	[varint32: proto byte length]
//	[EncryptionHeader proto bytes]   see dataencryption/v1/encryption_header.proto
//	[ciphertext bytes]
package dataencryption

import (
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	pbv1 "github.com/chirino/memory-service/internal/generated/pb/dataencryption/v1"
)

var magic = [4]byte{0x4D, 0x53, 0x45, 0x48} // "MSEH"

// Header is the decoded MSEH envelope header.
type Header struct {
	Version    uint32
	ProviderID string
	Nonce      []byte
}

// HasMagic reports whether b starts with the MSEH magic bytes.
func HasMagic(b []byte) bool {
	return len(b) >= 4 &&
		b[0] == magic[0] && b[1] == magic[1] && b[2] == magic[2] && b[3] == magic[3]
}

// WriteHeader encodes h as an MSEH envelope prefix and writes it to w.
func WriteHeader(w io.Writer, h Header) error {
	protoBytes, err := proto.Marshal(&pbv1.EncryptionHeader{
		Version:    h.Version,
		ProviderId: h.ProviderID,
		Nonce:      h.Nonce,
	})
	if err != nil {
		return fmt.Errorf("mseh: encoding header: %w", err)
	}
	buf := make([]byte, 4+varintLen(uint32(len(protoBytes)))+len(protoBytes))
	copy(buf[:4], magic[:])
	n := putVarint32(buf[4:], uint32(len(protoBytes)))
	copy(buf[4+n:], protoBytes)
	_, err = w.Write(buf)
	return err
}

// ReadHeader reads the MSEH magic + varint + proto fields from r.
// Returns (header, true, nil) on success, (nil, false, nil) if magic is absent,
// or (nil, true, err) on a read error after the magic has been confirmed present.
func ReadHeader(r io.Reader) (*Header, bool, error) {
	var mgc [4]byte
	if _, err := io.ReadFull(r, mgc[:]); err != nil {
		return nil, false, nil // not enough bytes — treat as no magic
	}
	if mgc != magic {
		return nil, false, nil
	}
	protoLen, err := readVarint32(r)
	if err != nil {
		return nil, true, fmt.Errorf("mseh: reading proto length: %w", err)
	}
	// Guard against a crafted header advertising a huge proto length.
	// Current providers write: version uint32 + provider-ID string + 12-byte AES-GCM IV,
	// which is well under 64 bytes. 4 KiB is orders of magnitude above any legitimate value.
	const maxProtoLen = 4096
	if protoLen > maxProtoLen {
		return nil, true, fmt.Errorf("mseh: proto length %d exceeds maximum %d", protoLen, maxProtoLen)
	}
	protoBytes := make([]byte, protoLen)
	if _, err := io.ReadFull(r, protoBytes); err != nil {
		return nil, true, fmt.Errorf("mseh: reading proto bytes: %w", err)
	}
	var msg pbv1.EncryptionHeader
	if err := proto.Unmarshal(protoBytes, &msg); err != nil {
		return nil, true, fmt.Errorf("mseh: decoding header: %w", err)
	}
	return &Header{
		Version:    msg.Version,
		ProviderID: msg.ProviderId,
		Nonce:      msg.Nonce,
	}, true, nil
}

// ── varint32 helpers (outer MSEH framing only; proto field encoding is handled by proto.Marshal) ──

func putVarint32(b []byte, v uint32) int {
	n := 0
	for v >= 0x80 {
		b[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	b[n] = byte(v)
	return n + 1
}

func varintLen(v uint32) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

func readVarint32(r io.Reader) (uint32, error) {
	var v uint32
	var buf [1]byte
	for i := range 5 {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		v |= uint32(buf[0]&0x7F) << (7 * uint(i))
		if buf[0]&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("mseh: varint32 overflow")
}
