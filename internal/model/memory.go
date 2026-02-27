package model

import (
	"time"

	"github.com/google/uuid"
)

// Memory is a single namespaced episodic memory item.
// Each row in the memories table represents one write event.
// The active value of a (namespace, key) pair is the row where DeletedAt IS NULL.
type Memory struct {
	// ID is the primary key (UUID).
	ID uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`

	// Namespace is the RS-encoded namespace string (percent-encoded segments joined with \x1e).
	// Clients always work with []string; this field is for storage only.
	Namespace string `json:"-" gorm:"not null"`

	// Key is the memory key, unique within a namespace (active rows only).
	Key string `json:"key" gorm:"not null"`

	// ValueEncrypted is the AES-256-GCM encrypted JSON value. Decrypted on read.
	// NULL for tombstones (deleted/expired rows after eviction).
	ValueEncrypted []byte `json:"-" gorm:"column:value_encrypted"`

	// Attributes is the AES-256-GCM encrypted user-supplied attributes JSON.
	// Decrypted on read and returned to clients.
	Attributes []byte `json:"-" gorm:"column:attributes"`

	// PolicyAttributes contains plaintext OPA-extracted attributes for server-side filtering.
	// Never returned to clients.
	PolicyAttributes map[string]interface{} `json:"-" gorm:"type:jsonb;serializer:json;column:policy_attributes"`

	// IndexFields optionally restricts which value fields are embedded.
	IndexFields []string `json:"-" gorm:"type:jsonb;serializer:json;column:index_fields"`

	// IndexDisabled disables vector indexing for this memory when true.
	IndexDisabled bool `json:"-" gorm:"column:index_disabled"`

	// Kind records whether this row was a first write (0=add) or a subsequent write (1=update).
	// Set at write time; never changed.
	Kind int16 `json:"-" gorm:"not null;default:0;column:kind"`

	// CreatedAt is when this row was written.
	CreatedAt time.Time `json:"createdAt" gorm:"not null;default:now()"`

	// ExpiresAt is the optional TTL expiry time. NULL means no expiry.
	ExpiresAt *time.Time `json:"expiresAt" gorm:"column:expires_at"`

	// DeletedAt is set when the row is soft-deleted (superseded or key deleted).
	DeletedAt *time.Time `json:"-" gorm:"column:deleted_at"`

	// DeletedReason records why the row was deleted. NULL=active, 0=updated, 1=deleted, 2=expired.
	DeletedReason *int16 `json:"-" gorm:"column:deleted_reason"`

	// IndexedAt tracks vector index sync state. NULL means pending indexing.
	IndexedAt *time.Time `json:"-" gorm:"column:indexed_at"`
}

// TableName implements gorm.Tabler.
func (Memory) TableName() string { return "memories" }

// MemoryVector stores the embedding for a single (memory_id, field_name) pair.
// One Memory can produce multiple MemoryVector rows (one per indexed field).
type MemoryVector struct {
	// MemoryID references the memories.id.
	MemoryID uuid.UUID `gorm:"not null;primaryKey;column:memory_id"`

	// FieldName is the JSON field name within the memory value that was embedded.
	FieldName string `gorm:"not null;primaryKey;column:field_name"`

	// Namespace is a redundant copy of the memory's encoded namespace for prefix filtering.
	Namespace string `gorm:"not null;column:namespace"`

	// PolicyAttributes is a redundant copy of the memory's policy_attributes for filtering.
	PolicyAttributes map[string]interface{} `gorm:"type:jsonb;serializer:json;column:policy_attributes"`
}

// TableName implements gorm.Tabler.
func (MemoryVector) TableName() string { return "memory_vectors" }
