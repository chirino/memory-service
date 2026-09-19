package eventstream

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalDeliveryJSONDoesNotEscapeHTMLCharacters(t *testing.T) {
	data, err := MarshalDeliveryJSON(map[string]string{"content": "<>&"})
	require.NoError(t, err)
	require.JSONEq(t, `{"content":"<>&"}`, string(data))
	require.NotContains(t, string(data), `\u003c`)
	require.NotContains(t, string(data), `\u003e`)
	require.NotContains(t, string(data), `\u0026`)
}
