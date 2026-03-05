package search

import (
	"testing"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSearchTypes(t *testing.T) {
	t.Run("nil defaults to auto", func(t *testing.T) {
		got, err := normalizeSearchTypes(nil)
		require.NoError(t, err)
		require.Equal(t, []string{searchTypeAuto}, got)
	})

	t.Run("single value", func(t *testing.T) {
		got, err := normalizeSearchTypes("semantic")
		require.NoError(t, err)
		require.Equal(t, []string{searchTypeSemantic}, got)
	})

	t.Run("array deduplicates", func(t *testing.T) {
		got, err := normalizeSearchTypes([]any{"semantic", "fulltext", "semantic"})
		require.NoError(t, err)
		require.Equal(t, []string{searchTypeSemantic, searchTypeFulltext}, got)
	})

	t.Run("auto cannot be combined", func(t *testing.T) {
		_, err := normalizeSearchTypes([]any{"auto", "fulltext"})
		require.ErrorContains(t, err, "cannot be combined")
	})

	t.Run("non string array value rejected", func(t *testing.T) {
		_, err := normalizeSearchTypes([]any{"semantic", 42})
		require.ErrorContains(t, err, "must be strings")
	})
}

func TestDecodeAfterCursor(t *testing.T) {
	t.Run("decode typed cursor token", func(t *testing.T) {
		encoded := encodeAfterCursor(
			[]string{searchTypeSemantic, searchTypeFulltext},
			map[string]string{
				searchTypeSemantic: "11111111-1111-1111-1111-111111111111",
				searchTypeFulltext: "22222222-2222-2222-2222-222222222222",
			},
		)
		require.NotNil(t, encoded)

		got, types, err := decodeAfterCursor(encoded, []string{searchTypeSemantic, searchTypeFulltext})
		require.NoError(t, err)
		require.Equal(t, []string{searchTypeSemantic, searchTypeFulltext}, types)
		require.Equal(t, "11111111-1111-1111-1111-111111111111", got[searchTypeSemantic])
		require.Equal(t, "22222222-2222-2222-2222-222222222222", got[searchTypeFulltext])
	})

	t.Run("legacy auto cursor maps to fulltext", func(t *testing.T) {
		raw := "33333333-3333-3333-3333-333333333333"
		got, types, err := decodeAfterCursor(&raw, []string{searchTypeAuto})
		require.NoError(t, err)
		require.Nil(t, types)
		require.Equal(t, raw, got[searchTypeFulltext])
	})

	t.Run("malformed cursor rejected for multi search", func(t *testing.T) {
		raw := "not-a-valid-token"
		_, _, err := decodeAfterCursor(&raw, []string{searchTypeSemantic, searchTypeFulltext})
		require.ErrorContains(t, err, errInvalidAfterCursor.Error())
	})
}

func TestSemanticCandidateLimit(t *testing.T) {
	require.Equal(t, 11, semanticCandidateLimit(10, false, nil))
	require.Equal(t, 31, semanticCandidateLimit(10, true, nil))

	cursor := "44444444-4444-4444-4444-444444444444"
	require.Equal(t, 1000, semanticCandidateLimit(10, false, &cursor))
	require.Equal(t, 5000, semanticCandidateLimit(4000, true, nil))
}

func TestPaginateSearchResults(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	results := []registrystore.SearchResult{
		{EntryID: id1},
		{EntryID: id2},
		{EntryID: id3},
	}

	page, next, err := paginateSearchResults(results, nil, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.NotNil(t, next)
	require.Equal(t, id2.String(), *next)

	page2, next2, err := paginateSearchResults(results, next, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, id3, page2[0].EntryID)
	require.Nil(t, next2)
}
