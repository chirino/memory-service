package clickhouse

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRegisteredProjection(t *testing.T) {
	t.Parallel()

	projection, err := validateRegisteredProjection("history_v1", "history_v1", "entry:input.resource")
	require.NoError(t, err)
	require.Equal(t, registeredProjection{Name: "history_v1", TableName: "history_v1", Resource: "entry"}, projection)

	for _, test := range []struct {
		name, table, selector string
	}{
		{"unsafe", "safe; DROP TABLE users", "entry:input.resource"},
		{"resources", "resources", "entry:input.resource"},
		{"mismatched-name", "safe", "entry:input.resource"},
		{"bad-resource", "safe", "conversation:input.resource"},
		{"missing-expression", "safe", "entry:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateRegisteredProjection(test.name, test.table, test.selector)
			require.Error(t, err)
		})
	}
}
