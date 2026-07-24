package dek

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	gcmNonceSize                = 12
	streamKeyIDSize             = 8
	streamV3SaltSize            = 16
	streamV3HeaderNonceSize     = streamKeyIDSize + streamV3SaltSize
	streamV3RecordPlaintextSize = 64 * 1024
	streamV3RecordHeaderSize    = 5
	streamV3RecordTypeData      = byte(0x00)
	streamV3RecordTypeFinal     = byte(0x01)
	streamV3AADDomain           = "memory-service/mseh/v3/attachment-stream"
	streamV3KeyInfo             = "memory-service/mseh/v3/attachment-stream-key"
)

// NewGCMStreamNonce generates a v3 attachment stream nonce. The leading bytes
// encode a stable key ID so rotated providers can select the correct master key
// before streaming; the trailing bytes are the per-stream salt.
func NewGCMStreamNonce(key []byte) ([]byte, error) {
	nonce := make([]byte, streamV3HeaderNonceSize)
	copy(nonce[:streamKeyIDSize], streamKeyID(key))
	if _, err := rand.Read(nonce[streamKeyIDSize:]); err != nil {
		return nil, fmt.Errorf("dek: generating GCM stream nonce: %w", err)
	}
	return nonce, nil
}

// SelectGCMStreamKey chooses the master key encoded into a v3 stream nonce.
func SelectGCMStreamKey(keys [][]byte, nonce []byte) ([]byte, error) {
	if len(nonce) != streamV3HeaderNonceSize {
		return nil, fmt.Errorf("dek: MSEH v3 stream nonce must be %d bytes, got %d", streamV3HeaderNonceSize, len(nonce))
	}
	want := nonce[:streamKeyIDSize]
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		if bytes.Equal(streamKeyID(key), want) {
			return key, nil
		}
	}
	return nil, fmt.Errorf("dek: no matching key for MSEH v3 stream nonce")
}

// NewGCMStreamEncryptWriter returns a WriteCloser that writes MSEH v3
// authenticated 64-KiB AES-GCM records to dst.
func NewGCMStreamEncryptWriter(dst io.Writer, key []byte, providerID string, headerNonce []byte) (io.WriteCloser, error) {
	aead, err := newStreamV3AEAD(key, headerNonce)
	if err != nil {
		return nil, err
	}
	return &gcmStreamWriter{
		dst:         dst,
		aead:        aead,
		providerID:  providerID,
		headerNonce: append([]byte(nil), headerNonce...),
		salt:        append([]byte(nil), headerNonce[streamKeyIDSize:]...),
		buf:         make([]byte, 0, streamV3RecordPlaintextSize),
	}, nil
}

// NewGCMStreamDecryptReader returns a Reader over plaintext from an MSEH v3
// authenticated record stream.
func NewGCMStreamDecryptReader(src io.Reader, key []byte, providerID string, headerNonce []byte) (io.Reader, error) {
	aead, err := newStreamV3AEAD(key, headerNonce)
	if err != nil {
		return nil, err
	}
	return &gcmStreamReader{
		src:         src,
		aead:        aead,
		providerID:  providerID,
		headerNonce: append([]byte(nil), headerNonce...),
		salt:        append([]byte(nil), headerNonce[streamKeyIDSize:]...),
	}, nil
}

func streamKeyID(key []byte) []byte {
	sum := sha256.Sum256(key)
	return sum[:streamKeyIDSize]
}

func newStreamV3AEAD(masterKey []byte, headerNonce []byte) (cipher.AEAD, error) {
	if len(headerNonce) != streamV3HeaderNonceSize {
		return nil, fmt.Errorf("dek: MSEH v3 stream nonce must be %d bytes, got %d", streamV3HeaderNonceSize, len(headerNonce))
	}
	streamKey, err := hkdf.Key(sha256.New, masterKey, headerNonce[streamKeyIDSize:], streamV3KeyInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("dek: deriving MSEH v3 stream key: %w", err)
	}
	block, err := aes.NewCipher(streamKey)
	if err != nil {
		return nil, fmt.Errorf("dek: AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("dek: AES-GCM: %w", err)
	}
	return aead, nil
}

type gcmStreamWriter struct {
	dst         io.Writer
	aead        cipher.AEAD
	providerID  string
	headerNonce []byte
	salt        []byte
	buf         []byte
	recordIndex uint64
	total       uint64
	closed      bool
}

func (w *gcmStreamWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("dek: write after MSEH v3 stream close")
	}
	written := 0
	for len(p) > 0 {
		space := streamV3RecordPlaintextSize - len(w.buf)
		if space > len(p) {
			space = len(p)
		}
		w.buf = append(w.buf, p[:space]...)
		p = p[space:]
		written += space
		if len(w.buf) == streamV3RecordPlaintextSize {
			if err := w.writeDataRecord(w.buf); err != nil {
				return written, err
			}
			w.buf = w.buf[:0]
		}
	}
	return written, nil
}

func (w *gcmStreamWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.buf) > 0 {
		if err := w.writeDataRecord(w.buf); err != nil {
			return err
		}
		w.buf = nil
	}
	return w.writeFinalRecord()
}

func (w *gcmStreamWriter) writeDataRecord(plaintext []byte) error {
	if len(plaintext) == 0 || len(plaintext) > streamV3RecordPlaintextSize {
		return fmt.Errorf("dek: invalid MSEH v3 DATA plaintext length %d", len(plaintext))
	}
	if err := checkStreamV3RecordIndex(w.recordIndex); err != nil {
		return err
	}
	length := uint32(len(plaintext))
	aad := streamV3AAD(w.providerID, w.headerNonce, streamV3RecordTypeData, w.recordIndex, length, 0)
	sealed := w.aead.Seal(nil, streamV3RecordNonce(w.salt, w.recordIndex), plaintext, aad)
	if err := writeStreamV3Record(w.dst, streamV3RecordTypeData, length, sealed); err != nil {
		return err
	}
	w.recordIndex++
	w.total += uint64(len(plaintext))
	return nil
}

func (w *gcmStreamWriter) writeFinalRecord() error {
	if err := checkStreamV3RecordIndex(w.recordIndex); err != nil {
		return err
	}
	aad := streamV3AAD(w.providerID, w.headerNonce, streamV3RecordTypeFinal, w.recordIndex, 0, w.total)
	sealed := w.aead.Seal(nil, streamV3RecordNonce(w.salt, w.recordIndex), nil, aad)
	if err := writeStreamV3Record(w.dst, streamV3RecordTypeFinal, 0, sealed); err != nil {
		return err
	}
	w.recordIndex++
	return nil
}

type gcmStreamReader struct {
	src         io.Reader
	aead        cipher.AEAD
	providerID  string
	headerNonce []byte
	salt        []byte
	recordIndex uint64
	total       uint64
	buf         []byte
	final       bool
	shortData   bool
}

func (r *gcmStreamReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.final {
			return 0, io.EOF
		}
		if err := r.readNextRecord(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *gcmStreamReader) readNextRecord() error {
	var header [streamV3RecordHeaderSize]byte
	if _, err := io.ReadFull(r.src, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("dek: missing MSEH v3 FINAL record: %w", err)
		}
		return err
	}
	recordType := header[0]
	length := binary.BigEndian.Uint32(header[1:])
	if err := checkStreamV3RecordIndex(r.recordIndex); err != nil {
		return err
	}

	switch recordType {
	case streamV3RecordTypeData:
		return r.readDataRecord(length)
	case streamV3RecordTypeFinal:
		return r.readFinalRecord(length)
	default:
		return fmt.Errorf("dek: unknown MSEH v3 record type %d", recordType)
	}
}

func (r *gcmStreamReader) readDataRecord(length uint32) error {
	if length == 0 || length > streamV3RecordPlaintextSize {
		return fmt.Errorf("dek: invalid MSEH v3 DATA plaintext length %d", length)
	}
	if r.shortData {
		return fmt.Errorf("dek: MSEH v3 DATA record found after short DATA record")
	}
	payload := make([]byte, int(length)+r.aead.Overhead())
	if _, err := io.ReadFull(r.src, payload); err != nil {
		return fmt.Errorf("dek: reading MSEH v3 DATA record: %w", err)
	}
	aad := streamV3AAD(r.providerID, r.headerNonce, streamV3RecordTypeData, r.recordIndex, length, 0)
	plain, err := r.aead.Open(nil, streamV3RecordNonce(r.salt, r.recordIndex), payload, aad)
	if err != nil {
		return fmt.Errorf("dek: authenticating MSEH v3 DATA record: %w", err)
	}
	if length < streamV3RecordPlaintextSize {
		r.shortData = true
	}
	r.recordIndex++
	r.total += uint64(length)
	r.buf = plain
	return nil
}

func (r *gcmStreamReader) readFinalRecord(length uint32) error {
	if length != 0 {
		return fmt.Errorf("dek: invalid MSEH v3 FINAL plaintext length %d", length)
	}
	payload := make([]byte, r.aead.Overhead())
	if _, err := io.ReadFull(r.src, payload); err != nil {
		return fmt.Errorf("dek: reading MSEH v3 FINAL record: %w", err)
	}
	aad := streamV3AAD(r.providerID, r.headerNonce, streamV3RecordTypeFinal, r.recordIndex, 0, r.total)
	if _, err := r.aead.Open(nil, streamV3RecordNonce(r.salt, r.recordIndex), payload, aad); err != nil {
		return fmt.Errorf("dek: authenticating MSEH v3 FINAL record: %w", err)
	}
	var trailing [1]byte
	n, err := r.src.Read(trailing[:])
	if n > 0 {
		return fmt.Errorf("dek: trailing bytes after MSEH v3 FINAL record")
	}
	if err != nil && err != io.EOF {
		return fmt.Errorf("dek: checking MSEH v3 stream EOF: %w", err)
	}
	r.recordIndex++
	r.final = true
	return nil
}

func writeStreamV3Record(dst io.Writer, recordType byte, length uint32, payload []byte) error {
	var header [streamV3RecordHeaderSize]byte
	header[0] = recordType
	binary.BigEndian.PutUint32(header[1:], length)
	if _, err := dst.Write(header[:]); err != nil {
		return fmt.Errorf("dek: writing MSEH v3 record header: %w", err)
	}
	if _, err := dst.Write(payload); err != nil {
		return fmt.Errorf("dek: writing MSEH v3 record payload: %w", err)
	}
	return nil
}

func streamV3RecordNonce(salt []byte, recordIndex uint64) []byte {
	nonce := make([]byte, gcmNonceSize)
	copy(nonce[:8], salt[:8])
	binary.BigEndian.PutUint32(nonce[8:], uint32(recordIndex))
	return nonce
}

func streamV3AAD(providerID string, headerNonce []byte, recordType byte, recordIndex uint64, plaintextLength uint32, totalPlaintextLength uint64) []byte {
	var b bytes.Buffer
	writeLengthPrefixedString(&b, streamV3AADDomain)
	writeLengthPrefixedString(&b, providerID)
	writeUint32(&b, 3)
	writeLengthPrefixedBytes(&b, headerNonce)
	b.WriteByte(recordType)
	writeUint64(&b, recordIndex)
	writeUint32(&b, plaintextLength)
	writeUint64(&b, totalPlaintextLength)
	return b.Bytes()
}

func writeLengthPrefixedString(dst *bytes.Buffer, value string) {
	writeLengthPrefixedBytes(dst, []byte(value))
}

func writeLengthPrefixedBytes(dst *bytes.Buffer, value []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(value)))
	dst.Write(lenBuf[:])
	dst.Write(value)
}

func writeUint32(dst *bytes.Buffer, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	dst.Write(buf[:])
}

func writeUint64(dst *bytes.Buffer, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	dst.Write(buf[:])
}

func checkStreamV3RecordIndex(recordIndex uint64) error {
	if recordIndex > math.MaxUint32 {
		return fmt.Errorf("dek: MSEH v3 stream record index overflow")
	}
	return nil
}
