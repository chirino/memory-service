package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

const (
	MinCheckpointLeaseTTL = 15 * time.Second
	MaxCheckpointLeaseTTL = 5 * time.Minute
)

var ErrCheckpointConflict = errors.New("checkpoint revision or lease ownership changed")

func EncodeCheckpointRevision(revision uint64) string {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], revision)
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func ParseCheckpointRevision(value string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 8 {
		return 0, &ValidationError{Field: "expectedRevision", Message: "expectedRevision is invalid"}
	}
	return binary.BigEndian.Uint64(raw), nil
}

func HashCheckpointLeaseToken(token string) ([32]byte, error) {
	if token == "" {
		return [32]byte{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, &ValidationError{Field: "leaseToken", Message: "leaseToken is invalid"}
	}
	return sha256.Sum256(raw), nil
}

func ValidateCheckpointLeaseTTL(ttl time.Duration) error {
	if ttl < MinCheckpointLeaseTTL || ttl > MaxCheckpointLeaseTTL {
		return &ValidationError{Field: "ttl", Message: "ttl must be between 15 seconds and 5 minutes"}
	}
	return nil
}

func NewCheckpointConflict() error {
	return &ConflictError{Message: ErrCheckpointConflict.Error(), Code: "checkpoint_conflict"}
}
