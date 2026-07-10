package postgres

import (
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestEntryOrderLessDistinguishesNullSeqFromZero(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	zero := uint32(0)
	nullSeq := model.Entry{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000ff"), CreatedAt: createdAt}
	seqZero := model.Entry{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), CreatedAt: createdAt, Seq: &zero}

	assert.True(t, entryOrderLess(nullSeq, seqZero))
	assert.False(t, entryOrderLess(seqZero, nullSeq))
}
