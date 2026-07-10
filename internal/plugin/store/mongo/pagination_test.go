package mongo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEntryDocOrderLessDistinguishesNullSeqFromZero(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	zero := uint32(0)
	nullSeq := entryDoc{ID: "00000000-0000-0000-0000-0000000000ff", CreatedAt: createdAt}
	seqZero := entryDoc{ID: "00000000-0000-0000-0000-000000000001", CreatedAt: createdAt, Seq: &zero}

	assert.True(t, entryDocOrderLess(nullSeq, seqZero))
	assert.False(t, entryDocOrderLess(seqZero, nullSeq))
}
