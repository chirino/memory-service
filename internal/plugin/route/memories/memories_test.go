package memories

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectivePerQueryLimit(t *testing.T) {
	t.Run("defaults to overall limit below cap", func(t *testing.T) {
		limit, err := effectivePerQueryLimit(50, nil)
		require.NoError(t, err)
		require.Equal(t, 50, limit)
	})

	t.Run("caps default when overall limit exceeds candidate budget", func(t *testing.T) {
		limit, err := effectivePerQueryLimit(5000, nil)
		require.NoError(t, err)
		require.Equal(t, maxPerQueryLimit, limit)
	})

	t.Run("accepts explicit value at cap", func(t *testing.T) {
		requested := maxPerQueryLimit
		limit, err := effectivePerQueryLimit(5000, &requested)
		require.NoError(t, err)
		require.Equal(t, maxPerQueryLimit, limit)
	})

	for _, requested := range []int{0, maxPerQueryLimit + 1} {
		t.Run("rejects explicit value outside cap", func(t *testing.T) {
			_, err := effectivePerQueryLimit(5000, &requested)
			require.EqualError(t, err, "per_query_limit must be between 1 and 100")
		})
	}
}
