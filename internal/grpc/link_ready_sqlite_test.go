//go:build !nosqlite && sqlite_fts5

package grpc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// linkReadyGRPCEnv runs the real AppendEntry handler against the real SQLite
// store. The mock-based tests cannot show that a rejected link takes the entry
// with it.
type linkReadyGRPCEnv struct {
	ctx         context.Context
	store       registrystore.MemoryStore
	interceptor gogrpc.UnaryServerInterceptor
	server      *EntriesServer
}

func newLinkReadyGRPCEnv(t *testing.T) *linkReadyGRPCEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.LocalUserID = "alice"
	cfg.LocalClientID = "test-client"
	cfg.UnixSocketAuth = "local"
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "link-ready-grpc.db")
	cfg.DatastoreMigrateAtStart = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	resolver, err := security.NewTokenResolver(&cfg)
	require.NoError(t, err)
	return &linkReadyGRPCEnv{
		ctx:         ctx,
		store:       store,
		interceptor: security.GRPCUnaryInterceptorWithRateLimiter(resolver, nil),
		server:      &EntriesServer{Store: store},
	}
}

func (e *linkReadyGRPCEnv) conversation(t *testing.T) string {
	t.Helper()
	var id string
	require.NoError(t, e.store.InWriteTx(e.ctx, func(txCtx context.Context) error {
		conv, err := e.store.CreateConversation(txCtx, "alice", "", "grpc link ready", nil, nil, nil, nil)
		if err != nil {
			return err
		}
		id = conv.ID
		return nil
	}))
	return id
}

func (e *linkReadyGRPCEnv) attachment(t *testing.T, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, e.store.InWriteTx(e.ctx, func(txCtx context.Context) error {
		key := "key-" + uuid.NewString()
		a, err := e.store.CreateAttachment(txCtx, "alice", "", model.Attachment{ContentType: "text/plain", Status: status, StorageKey: &key})
		if err != nil {
			return err
		}
		id = a.ID
		return nil
	}))
	return id
}

func (e *linkReadyGRPCEnv) append(t *testing.T, convID string, attachmentIDs ...uuid.UUID) (*pb.Entry, error) {
	t.Helper()
	refs := make([]any, 0, len(attachmentIDs))
	for _, id := range attachmentIDs {
		refs = append(refs, map[string]any{"attachmentId": id.String(), "contentType": "text/plain"})
	}
	block, err := structpb.NewStruct(map[string]any{"role": "USER", "text": "see attached", "attachments": refs})
	require.NoError(t, err)
	req := &pb.AppendEntryRequest{
		ConversationId: convID,
		Entry: &pb.CreateEntryRequest{
			Channel: pb.Channel_HISTORY, ContentType: "history",
			Content: []*structpb.Value{structpb.NewStructValue(block)},
		},
	}
	resp, err := e.interceptor(context.Background(), req, &gogrpc.UnaryServerInfo{}, func(ctx context.Context, raw any) (any, error) {
		return e.server.AppendEntry(ctx, raw.(*pb.AppendEntryRequest))
	})
	if err != nil {
		return nil, err
	}
	return resp.(*pb.Entry), nil
}

func (e *linkReadyGRPCEnv) entryCount(t *testing.T, convID string) int {
	t.Helper()
	var n int
	require.NoError(t, e.store.InReadTx(e.ctx, func(txCtx context.Context) error {
		page, err := e.store.GetEntries(txCtx, "alice", convID, registrystore.EntryListQuery{Limit: 50})
		if err != nil {
			return err
		}
		n = len(page.Data)
		return nil
	}))
	return n
}

func (e *linkReadyGRPCEnv) linked(t *testing.T, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var entryID *uuid.UUID
	require.NoError(t, e.store.InReadTx(e.ctx, func(txCtx context.Context) error {
		a, err := e.store.GetAttachment(txCtx, "alice", "", id)
		if err != nil {
			return err
		}
		entryID = a.EntryID
		return nil
	}))
	return entryID
}

func TestGRPCAppendEntryLinksReadyAttachment(t *testing.T) {
	env := newLinkReadyGRPCEnv(t)
	conv := env.conversation(t)
	id := env.attachment(t, "ready")

	entry, err := env.append(t, conv, id)

	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, 1, env.entryCount(t, conv))
	require.NotNil(t, env.linked(t, id))
}

func TestGRPCAppendEntryRejectsAttachmentThatIsNotReady(t *testing.T) {
	for _, attachmentStatus := range []string{"uploading", "downloading", "failed"} {
		t.Run(attachmentStatus, func(t *testing.T) {
			env := newLinkReadyGRPCEnv(t)
			conv := env.conversation(t)
			id := env.attachment(t, attachmentStatus)

			_, err := env.append(t, conv, id)

			require.Equal(t, codes.Aborted, status.Code(err), "%v", err)
			require.Contains(t, status.Convert(err).Message(), attachmentStatus)
			require.Zero(t, env.entryCount(t, conv), "a rejected link must not leave an entry behind")
			require.Nil(t, env.linked(t, id))
		})
	}
}

func TestGRPCAppendEntryRejectedLinkRollsBackEarlierLinks(t *testing.T) {
	env := newLinkReadyGRPCEnv(t)
	conv := env.conversation(t)
	readyID := env.attachment(t, "ready")
	pendingID := env.attachment(t, "downloading")

	_, err := env.append(t, conv, readyID, pendingID)

	require.Equal(t, codes.Aborted, status.Code(err), "%v", err)
	require.Zero(t, env.entryCount(t, conv))
	require.Nil(t, env.linked(t, readyID), "the ready attachment's link must roll back with the entry")
	require.Nil(t, env.linked(t, pendingID))

	// Once the pending attachment is ready, the same request succeeds.
	ready := "ready"
	require.NoError(t, env.store.InWriteTx(env.ctx, func(txCtx context.Context) error {
		_, err := env.store.UpdateAttachment(txCtx, "alice", pendingID, registrystore.AttachmentUpdate{Status: &ready})
		return err
	}))
	_, err = env.append(t, conv, readyID, pendingID)
	require.NoError(t, err)
	require.Equal(t, 1, env.entryCount(t, conv))
	require.NotNil(t, env.linked(t, readyID))
	require.NotNil(t, env.linked(t, pendingID))
}

func TestGRPCAppendEntryMissingAttachmentIsStillNotFound(t *testing.T) {
	env := newLinkReadyGRPCEnv(t)
	conv := env.conversation(t)

	_, err := env.append(t, conv, uuid.New())

	require.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	require.Zero(t, env.entryCount(t, conv))
}
