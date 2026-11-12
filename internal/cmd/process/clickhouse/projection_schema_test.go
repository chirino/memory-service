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

	// Chain manifests write to their chain's table, and manifests that only replace logic
	// or add columns register an empty selector expression.
	projection, err = validateRegisteredProjection("support_ticket_v2_duration", "support_ticket_steps", "entry:")
	require.NoError(t, err)
	require.Equal(t, registeredProjection{Name: "support_ticket_v2_duration", TableName: "support_ticket_steps", Resource: "entry"}, projection)

	for _, test := range []struct {
		name, projection, table, selector string
	}{
		{"unsafe-table", "safe", "safe; DROP TABLE users", "entry:input.resource"},
		{"unsafe-name", "safe; DROP TABLE users", "safe", "entry:input.resource"},
		{"fixed-memberships", "safe", "conversation_memberships", "entry:input.resource"},
		{"generated-view", "safe", "safe_current", "entry:input.resource"},
		{"bad-resource", "safe", "safe", "membership:input.resource"},
		{"missing-resource", "safe", "safe", "input.resource"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateRegisteredProjection(test.projection, test.table, test.selector)
			require.Error(t, err)
		})
	}
}
