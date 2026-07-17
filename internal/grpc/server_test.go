package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCPageSizeUsesConfiguredMaximum(t *testing.T) {
	cfg := config.DefaultConfig()
	require.Equal(t, 1000, cfg.MaxPageSize)
	cfg.MaxPageSize = 3
	ctx := config.WithContext(context.Background(), &cfg)

	limit, err := grpcPageSize(ctx, 0, 20)
	require.NoError(t, err)
	require.Equal(t, 3, limit)

	limit, err = grpcPageSize(ctx, 2, 20)
	require.NoError(t, err)
	require.Equal(t, 2, limit)

	_, err = grpcPageSize(ctx, 4, 20)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "page size must be between 1 and 3")
}

func TestMapErrorHidesUntypedInternalErrors(t *testing.T) {
	err := mapError(errors.New("postgres://user:secret@example/internal detail"))

	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "internal server error", status.Convert(err).Message())
}

func TestMapErrorLogsUntypedInternalErrors(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output)
	previous := log.Default()
	log.SetDefault(logger)
	t.Cleanup(func() { log.SetDefault(previous) })

	err := mapError(errors.New("sqlite write failed"))

	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, output.String(), "gRPC request failed")
	require.Contains(t, output.String(), "sqlite write failed")
}

func TestMapErrorPreservesTypedValidationMessage(t *testing.T) {
	err := mapError(&registrystore.ValidationError{Field: "limit", Message: "must be positive"})

	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "must be positive")
}

func TestMapErrorMapsConflictsToAborted(t *testing.T) {
	err := mapError(&registrystore.ConflictError{Message: "revision conflict"})

	require.Equal(t, codes.Aborted, status.Code(err))
	require.Equal(t, "revision conflict", status.Convert(err).Message())
}

func TestMapErrorMapsFailedPreconditionConflictsToAborted(t *testing.T) {
	err := mapError(&registrystore.ConflictError{Message: "state conflict", Code: "failed_precondition"})

	require.Equal(t, codes.Aborted, status.Code(err))
	require.Equal(t, "state conflict", status.Convert(err).Message())
}

func TestEpisodicInternalErrorUsesStablePublicMessage(t *testing.T) {
	err := episodicInternalError("semantic search backend leaked detail", errors.New("postgres://user:secret@example/internal detail"))

	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "internal server error", status.Convert(err).Message())
}

func TestProtoPerQueryLimitCapsDefaultCandidateBudget(t *testing.T) {
	limit, err := protoPerQueryLimit(0, 5000)
	require.NoError(t, err)
	require.Equal(t, 100, limit)

	limit, err = protoPerQueryLimit(0, 50)
	require.NoError(t, err)
	require.Equal(t, 50, limit)

	limit, err = protoPerQueryLimit(100, 5000)
	require.NoError(t, err)
	require.Equal(t, 100, limit)

	_, err = protoPerQueryLimit(101, 5000)
	require.EqualError(t, err, "per_query_limit must be between 1 and 100")
}

func TestEnrichGRPCEventResponseFullKeepsSummaryPayload(t *testing.T) {
	raw := json.RawMessage(`{"conversation":"00000000-0000-0000-0000-000000000001","conversation_group":"00000000-0000-0000-0000-000000000002","recording":"rec-1","status":"failed"}`)
	event := registryeventbus.Event{
		Event: "deleted",
		Kind:  "response",
		Data:  raw,
	}

	enriched, ok, err := (&EventStreamServer{}).enrichGRPCEvent(context.Background(), "alice", nil, "full", event)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, raw, enriched.Data)
}
