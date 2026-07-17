package transfers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleErrorCopiesConflictDetailsIntoStableDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handleError(c, &registrystore.ConflictError{
		Message: "a transfer is already pending for this conversation",
		Code:    "TRANSFER_ALREADY_PENDING",
		Details: map[string]interface{}{
			"existingTransferId": "transfer-1",
		},
	})

	require.Equal(t, http.StatusConflict, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "TRANSFER_ALREADY_PENDING", body["code"])
	require.Equal(t, "a transfer is already pending for this conversation", body["error"])
	require.Equal(t, map[string]any{"existingTransferId": "transfer-1"}, body["details"])
}
