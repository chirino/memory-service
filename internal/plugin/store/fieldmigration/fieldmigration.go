package fieldmigration

import (
	"bytes"
	"fmt"

	"github.com/chirino/memory-service/internal/dataencryption"
)

// ValueResult describes the migration decision for one encrypted field value.
type ValueResult struct {
	Ciphertext []byte
	AlreadyV4  bool
	Headerless bool
}

// MigrateValue rewrites a single persisted encrypted field to MSEH v4 when it
// is legacy MSEH or explicitly allowed headerless data.
func MigrateValue(svc *dataencryption.Service, ciphertext []byte, domain string, identity string) (*ValueResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("missing encryption service")
	}
	header, hasMagic, err := dataencryption.ReadHeader(bytes.NewReader(ciphertext))
	if err != nil {
		return nil, err
	}
	if hasMagic && header != nil && header.Version == dataencryption.VersionFieldAESGCM {
		return &ValueResult{AlreadyV4: true}, nil
	}
	plaintext, err := svc.DecryptField(ciphertext, domain, identity)
	if err != nil {
		return nil, err
	}
	rewritten, err := svc.EncryptField(plaintext, domain, identity)
	if err != nil {
		return nil, err
	}
	return &ValueResult{
		Ciphertext: rewritten,
		Headerless: !hasMagic,
	}, nil
}
