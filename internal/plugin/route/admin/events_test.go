package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/operationevent"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnrichAdminEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"completed"}`)
	event := registryeventbus.Event{
		Event: "deleted",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok := enrichAdminEvent(context.Background(), nil, "full", event)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}

func TestHandleAdminSSEEventsMarksSubscribeFailureAfterCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	streamErr := operationevent.WithErrorDetails(errors.New("private event bus failure"), operationevent.ErrorDetails{
		ErrorType: "event_bus",
		ErrorCode: "subscribe_failed",
	})
	bus := &failingSubscribeEventBus{err: streamErr}
	router := gin.New()
	router.Use(security.OperationEventMiddleware())
	var event *operationevent.Event
	router.GET("/v1/admin/events", func(c *gin.Context) {
		c.Set(security.ContextKeyUserID, "admin-1")
		event = security.OperationEventFromGin(c)
		HandleAdminSSEEvents(c, nil, bus, &cfg)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.NotNil(t, event)
	snapshot := event.Snapshot()
	require.Equal(t, operationevent.ResultFailed, snapshot.Result)
	require.Equal(t, "subscribe_failed", snapshot.Reason)
	require.Equal(t, "event_bus", snapshot.ErrorType)
	require.Equal(t, "subscribe_failed", snapshot.ErrorCode)
}

type failingSubscribeEventBus struct {
	err error
}

func (b *failingSubscribeEventBus) Publish(context.Context, registryeventbus.Event) error {
	return nil
}

func (b *failingSubscribeEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, b.err
}

func (b *failingSubscribeEventBus) Close() error { return nil }
