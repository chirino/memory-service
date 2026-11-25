package attachments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestParseCallerAttachmentID(t *testing.T) {
	t.Parallel()
	valid := uuid.New()
	str := func(s string) *string { return &s }

	t.Run("absent means the caller sent no ID", func(t *testing.T) {
		id, present, err := ParseCallerAttachmentID(nil)
		require.NoError(t, err)
		require.False(t, present)
		require.Equal(t, uuid.Nil, id)
	})

	t.Run("a valid UUID is present", func(t *testing.T) {
		id, present, err := ParseCallerAttachmentID(str(valid.String()))
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, valid, id)
	})

	// A present-but-blank field must never be mistaken for an absent one: that
	// would silently drop the caller's idempotency intent.
	for name, raw := range map[string]string{
		"empty":      "",
		"blank":      "   ",
		"malformed":  "not-a-uuid",
		"nil UUID":   uuid.Nil.String(),
		"truncated":  valid.String()[:20],
		"wrong text": "{" + valid.String() + "}x",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			id, present, err := ParseCallerAttachmentID(&raw)
			require.Error(t, err)
			require.True(t, present, "the caller did send an ID")
			require.Equal(t, uuid.Nil, id)
		})
	}
}

// The JSON body must keep "key absent" apart from "key present": the contract
// makes id optional but not nullable, so only an absent key selects the no-ID
// path. A *string cannot tell the two apart because null decodes to nil.
func TestParseCallerAttachmentIDJSON(t *testing.T) {
	t.Parallel()
	valid := uuid.New()

	t.Run("absent key means the caller sent no ID", func(t *testing.T) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(`{"sourceUrl":"x"}`), &req))
		id, present, err := ParseCallerAttachmentIDJSON(req.ID)
		require.NoError(t, err)
		require.False(t, present)
		require.Equal(t, uuid.Nil, id)
	})

	t.Run("a valid UUID string is present", func(t *testing.T) {
		id, present, err := ParseCallerAttachmentIDJSON(json.RawMessage(`"` + valid.String() + `"`))
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, valid, id)
	})

	// Each value is what the decoder hands over for a present key.
	for name, body := range map[string]string{
		"null":                           `{"id": null}`,
		"null, spaced":                   `{"id":   null  }`,
		"empty string":                   `{"id": ""}`,
		"blank string":                   `{"id": "   "}`,
		"number":                         `{"id": 123}`,
		"boolean":                        `{"id": false}`,
		"object":                         `{"id": {}}`,
		"array":                          `{"id": []}`,
		"not a UUID":                     `{"id": "not-a-uuid"}`,
		"nil UUID":                       `{"id": "` + uuid.Nil.String() + `"}`,
		"key matched case-insensitively": `{"ID": null}`,
		"last duplicate key wins":        `{"id": "` + valid.String() + `", "id": null}`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &req))
			require.NotNil(t, req.ID, "a present key must never decode to the absent value")
			id, present, err := ParseCallerAttachmentIDJSON(req.ID)
			require.Error(t, err)
			require.True(t, present, "the caller did send an ID")
			require.Equal(t, uuid.Nil, id)
		})
	}
}

func TestRecordCallerAttachmentID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	id := uuid.New()
	newCtx := func(event *operationevent.Event) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/attachments", nil)
		if event != nil {
			c.Request = c.Request.WithContext(operationevent.WithContext(c.Request.Context(), event))
		}
		return c
	}

	t.Run("records a present ID", func(t *testing.T) {
		event := operationevent.New("http POST /v1/attachments")
		recordCallerAttachmentID(newCtx(event), id, true)
		require.Equal(t, id.String(), event.Snapshot().AttachmentID)
	})

	t.Run("records nothing when no ID was sent", func(t *testing.T) {
		event := operationevent.New("http POST /v1/attachments")
		recordCallerAttachmentID(newCtx(event), uuid.Nil, false)
		require.Empty(t, event.Snapshot().AttachmentID)
	})

	t.Run("tolerates a request without an operation event", func(t *testing.T) {
		require.NotPanics(t, func() { recordCallerAttachmentID(newCtx(nil), id, true) })
	})
}
