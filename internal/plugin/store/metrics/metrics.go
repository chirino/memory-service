package metrics

import (
	"context"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
)

// Wrap returns a MemoryStore that records StoreLatency for every operation.
func Wrap(inner store.MemoryStore) store.MemoryStore {
	return &metricsStore{inner: inner}
}

type metricsStore struct {
	inner store.MemoryStore
}

func observe(op string, start time.Time) {
	security.StoreLatency.WithLabelValues(op).Observe(time.Since(start).Seconds())
}

func (m *metricsStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	defer observe("in_read_tx", time.Now())
	return m.inner.InReadTx(ctx, fn)
}

func (m *metricsStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	defer observe("in_write_tx", time.Now())
	return m.inner.InWriteTx(ctx, fn)
}

func (m *metricsStore) CreateConversation(ctx context.Context, userID string, clientID string, title string, metadata map[string]interface{}, agentID *string, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*store.ConversationDetail, error) {
	defer observe("create_conversation", time.Now())
	return m.inner.CreateConversation(ctx, userID, clientID, title, metadata, agentID, forkedAtConversationID, forkedAtEntryID)
}

func (m *metricsStore) CreateConversationWithID(ctx context.Context, userID string, clientID string, convID uuid.UUID, title string, metadata map[string]interface{}, agentID *string, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*store.ConversationDetail, error) {
	defer observe("create_conversation", time.Now())
	return m.inner.CreateConversationWithID(ctx, userID, clientID, convID, title, metadata, agentID, forkedAtConversationID, forkedAtEntryID)
}

func (m *metricsStore) ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode, ancestry model.ConversationAncestryFilter) ([]store.ConversationSummary, *string, error) {
	defer observe("list_conversations", time.Now())
	return m.inner.ListConversations(ctx, userID, query, afterCursor, limit, mode, ancestry)
}

func (m *metricsStore) GetConversation(ctx context.Context, userID string, conversationID uuid.UUID) (*store.ConversationDetail, error) {
	defer observe("get_conversation", time.Now())
	return m.inner.GetConversation(ctx, userID, conversationID)
}

func (m *metricsStore) UpdateConversation(ctx context.Context, userID string, conversationID uuid.UUID, title *string, metadata map[string]interface{}) (*store.ConversationDetail, error) {
	defer observe("update_conversation", time.Now())
	return m.inner.UpdateConversation(ctx, userID, conversationID, title, metadata)
}

func (m *metricsStore) DeleteConversation(ctx context.Context, userID string, conversationID uuid.UUID) error {
	defer observe("delete_conversation", time.Now())
	return m.inner.DeleteConversation(ctx, userID, conversationID)
}

func (m *metricsStore) ListMemberships(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	defer observe("list_memberships", time.Now())
	return m.inner.ListMemberships(ctx, userID, conversationID, afterCursor, limit)
}

func (m *metricsStore) ShareConversation(ctx context.Context, userID string, conversationID uuid.UUID, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	defer observe("share_conversation", time.Now())
	return m.inner.ShareConversation(ctx, userID, conversationID, targetUserID, accessLevel)
}

func (m *metricsStore) UpdateMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	defer observe("update_membership", time.Now())
	return m.inner.UpdateMembership(ctx, userID, conversationID, memberUserID, accessLevel)
}

func (m *metricsStore) DeleteMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string) error {
	defer observe("delete_membership", time.Now())
	return m.inner.DeleteMembership(ctx, userID, conversationID, memberUserID)
}

func (m *metricsStore) GetGroupMemberUserIDs(ctx context.Context, conversationGroupID uuid.UUID) ([]string, error) {
	defer observe("get_group_member_user_ids", time.Now())
	return m.inner.GetGroupMemberUserIDs(ctx, conversationGroupID)
}

func (m *metricsStore) ListForks(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]store.ConversationForkSummary, *string, error) {
	defer observe("list_forks", time.Now())
	return m.inner.ListForks(ctx, userID, conversationID, afterCursor, limit)
}

func (m *metricsStore) ListChildConversations(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]store.ConversationSummary, *string, error) {
	defer observe("list_child_conversations", time.Now())
	return m.inner.ListChildConversations(ctx, userID, conversationID, afterCursor, limit)
}

func (m *metricsStore) ListPendingTransfers(ctx context.Context, userID string, role string, afterCursor *string, limit int) ([]store.OwnershipTransferDto, *string, error) {
	defer observe("list_pending_transfers", time.Now())
	return m.inner.ListPendingTransfers(ctx, userID, role, afterCursor, limit)
}

func (m *metricsStore) GetTransfer(ctx context.Context, userID string, transferID uuid.UUID) (*store.OwnershipTransferDto, error) {
	defer observe("get_transfer", time.Now())
	return m.inner.GetTransfer(ctx, userID, transferID)
}

func (m *metricsStore) CreateOwnershipTransfer(ctx context.Context, userID string, conversationID uuid.UUID, toUserID string) (*store.OwnershipTransferDto, error) {
	defer observe("create_ownership_transfer", time.Now())
	return m.inner.CreateOwnershipTransfer(ctx, userID, conversationID, toUserID)
}

func (m *metricsStore) AcceptTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	defer observe("accept_transfer", time.Now())
	return m.inner.AcceptTransfer(ctx, userID, transferID)
}

func (m *metricsStore) DeleteTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	defer observe("delete_transfer", time.Now())
	return m.inner.DeleteTransfer(ctx, userID, transferID)
}

func (m *metricsStore) GetEntries(ctx context.Context, userID string, conversationID uuid.UUID, afterEntryID *string, limit int, channel *model.Channel, epochFilter *store.MemoryEpochFilter, clientID *string, agentID *string, allForks bool) (*store.PagedEntries, error) {
	defer observe("get_entries", time.Now())
	return m.inner.GetEntries(ctx, userID, conversationID, afterEntryID, limit, channel, epochFilter, clientID, agentID, allForks)
}

func (m *metricsStore) AppendEntries(ctx context.Context, userID string, conversationID uuid.UUID, entries []store.CreateEntryRequest, clientID *string, agentID *string, epoch *int64) ([]model.Entry, error) {
	defer observe("append_entries", time.Now())
	return m.inner.AppendEntries(ctx, userID, conversationID, entries, clientID, agentID, epoch)
}

func (m *metricsStore) GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error) {
	defer observe("get_entry_group_id", time.Now())
	return m.inner.GetEntryGroupID(ctx, entryID)
}

func (m *metricsStore) SyncAgentEntry(ctx context.Context, userID string, conversationID uuid.UUID, entry store.CreateEntryRequest, clientID string, agentID *string) (*store.SyncResult, error) {
	defer observe("sync_agent_entry", time.Now())
	return m.inner.SyncAgentEntry(ctx, userID, conversationID, entry, clientID, agentID)
}

func (m *metricsStore) IndexEntries(ctx context.Context, entries []store.IndexEntryRequest) (*store.IndexConversationsResponse, error) {
	defer observe("index_entries", time.Now())
	return m.inner.IndexEntries(ctx, entries)
}

func (m *metricsStore) ListUnindexedEntries(ctx context.Context, limit int, afterCursor *string) ([]model.Entry, *string, error) {
	defer observe("list_unindexed_entries", time.Now())
	return m.inner.ListUnindexedEntries(ctx, limit, afterCursor)
}

func (m *metricsStore) FindEntriesPendingVectorIndexing(ctx context.Context, limit int) ([]model.Entry, error) {
	defer observe("find_entries_pending_vector_indexing", time.Now())
	return m.inner.FindEntriesPendingVectorIndexing(ctx, limit)
}

func (m *metricsStore) SetIndexedAt(ctx context.Context, entryID uuid.UUID, conversationGroupID uuid.UUID, indexedAt time.Time) error {
	defer observe("set_indexed_at", time.Now())
	return m.inner.SetIndexedAt(ctx, entryID, conversationGroupID, indexedAt)
}

func (m *metricsStore) ListConversationGroupIDs(ctx context.Context, userID string) ([]uuid.UUID, error) {
	defer observe("list_conversation_group_ids", time.Now())
	return m.inner.ListConversationGroupIDs(ctx, userID)
}

func (m *metricsStore) FetchSearchResultDetails(ctx context.Context, userID string, entryIDs []uuid.UUID, includeEntry bool) ([]store.SearchResult, error) {
	defer observe("fetch_search_result_details", time.Now())
	return m.inner.FetchSearchResultDetails(ctx, userID, entryIDs, includeEntry)
}

func (m *metricsStore) SearchEntries(ctx context.Context, userID string, query string, afterCursor *string, limit int, includeEntry bool, groupByConversation bool) (*store.SearchResults, error) {
	defer observe("search_entries", time.Now())
	return m.inner.SearchEntries(ctx, userID, query, afterCursor, limit, includeEntry, groupByConversation)
}

func (m *metricsStore) AdminListConversations(ctx context.Context, query store.AdminConversationQuery) ([]store.ConversationSummary, *string, error) {
	defer observe("admin_list_conversations", time.Now())
	return m.inner.AdminListConversations(ctx, query)
}

func (m *metricsStore) AdminGetConversation(ctx context.Context, conversationID uuid.UUID) (*store.ConversationDetail, error) {
	defer observe("admin_get_conversation", time.Now())
	return m.inner.AdminGetConversation(ctx, conversationID)
}

func (m *metricsStore) AdminDeleteConversation(ctx context.Context, conversationID uuid.UUID) error {
	defer observe("admin_delete_conversation", time.Now())
	return m.inner.AdminDeleteConversation(ctx, conversationID)
}

func (m *metricsStore) AdminRestoreConversation(ctx context.Context, conversationID uuid.UUID) error {
	defer observe("admin_restore_conversation", time.Now())
	return m.inner.AdminRestoreConversation(ctx, conversationID)
}

func (m *metricsStore) AdminGetEntries(ctx context.Context, conversationID uuid.UUID, query store.AdminMessageQuery) (*store.PagedEntries, error) {
	defer observe("admin_get_entries", time.Now())
	return m.inner.AdminGetEntries(ctx, conversationID, query)
}

func (m *metricsStore) AdminListMemberships(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	defer observe("admin_list_memberships", time.Now())
	return m.inner.AdminListMemberships(ctx, conversationID, afterCursor, limit)
}

func (m *metricsStore) AdminListForks(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]store.ConversationForkSummary, *string, error) {
	defer observe("admin_list_forks", time.Now())
	return m.inner.AdminListForks(ctx, conversationID, afterCursor, limit)
}

func (m *metricsStore) AdminListChildConversations(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]store.ConversationSummary, *string, error) {
	defer observe("admin_list_child_conversations", time.Now())
	return m.inner.AdminListChildConversations(ctx, conversationID, afterCursor, limit)
}

func (m *metricsStore) AdminSearchEntries(ctx context.Context, query store.AdminSearchQuery) (*store.SearchResults, error) {
	defer observe("admin_search_entries", time.Now())
	return m.inner.AdminSearchEntries(ctx, query)
}

func (m *metricsStore) AdminListAttachments(ctx context.Context, query store.AdminAttachmentQuery) ([]store.AdminAttachment, *string, error) {
	defer observe("admin_list_attachments", time.Now())
	return m.inner.AdminListAttachments(ctx, query)
}

func (m *metricsStore) AdminGetAttachment(ctx context.Context, attachmentID uuid.UUID) (*store.AdminAttachment, error) {
	defer observe("admin_get_attachment", time.Now())
	return m.inner.AdminGetAttachment(ctx, attachmentID)
}

func (m *metricsStore) AdminDeleteAttachment(ctx context.Context, attachmentID uuid.UUID) error {
	defer observe("admin_delete_attachment", time.Now())
	return m.inner.AdminDeleteAttachment(ctx, attachmentID)
}

func (m *metricsStore) CreateAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachment model.Attachment) (*model.Attachment, error) {
	defer observe("create_attachment", time.Now())
	return m.inner.CreateAttachment(ctx, userID, conversationID, attachment)
}

func (m *metricsStore) UpdateAttachment(ctx context.Context, userID string, attachmentID uuid.UUID, update store.AttachmentUpdate) (*model.Attachment, error) {
	defer observe("update_attachment", time.Now())
	return m.inner.UpdateAttachment(ctx, userID, attachmentID, update)
}

func (m *metricsStore) ListAttachments(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.Attachment, *string, error) {
	defer observe("list_attachments", time.Now())
	return m.inner.ListAttachments(ctx, userID, conversationID, afterCursor, limit)
}

func (m *metricsStore) GetAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) (*model.Attachment, error) {
	defer observe("get_attachment", time.Now())
	return m.inner.GetAttachment(ctx, userID, conversationID, attachmentID)
}

func (m *metricsStore) DeleteAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) error {
	defer observe("delete_attachment", time.Now())
	return m.inner.DeleteAttachment(ctx, userID, conversationID, attachmentID)
}

func (m *metricsStore) FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	defer observe("find_evictable_group_ids", time.Now())
	return m.inner.FindEvictableGroupIDs(ctx, cutoff, limit)
}

func (m *metricsStore) CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error) {
	defer observe("count_evictable_groups", time.Now())
	return m.inner.CountEvictableGroups(ctx, cutoff)
}

func (m *metricsStore) HardDeleteConversationGroups(ctx context.Context, groupIDs []uuid.UUID) error {
	defer observe("hard_delete_conversation_groups", time.Now())
	return m.inner.HardDeleteConversationGroups(ctx, groupIDs)
}

func (m *metricsStore) CreateTask(ctx context.Context, taskType string, taskBody map[string]interface{}) error {
	defer observe("create_task", time.Now())
	return m.inner.CreateTask(ctx, taskType, taskBody)
}

func (m *metricsStore) ClaimReadyTasks(ctx context.Context, limit int) ([]model.Task, error) {
	defer observe("claim_ready_tasks", time.Now())
	return m.inner.ClaimReadyTasks(ctx, limit)
}

func (m *metricsStore) DeleteTask(ctx context.Context, taskID uuid.UUID) error {
	defer observe("delete_task", time.Now())
	return m.inner.DeleteTask(ctx, taskID)
}

func (m *metricsStore) FailTask(ctx context.Context, taskID uuid.UUID, errMsg string, retryDelay time.Duration) error {
	defer observe("fail_task", time.Now())
	return m.inner.FailTask(ctx, taskID, errMsg, retryDelay)
}

func (m *metricsStore) AdminGetAttachmentByStorageKey(ctx context.Context, storageKey string) (*store.AdminAttachment, error) {
	defer observe("admin_get_attachment_by_storage_key", time.Now())
	return m.inner.AdminGetAttachmentByStorageKey(ctx, storageKey)
}

func (m *metricsStore) AppendOutboxEvents(ctx context.Context, events []store.OutboxWrite) ([]store.OutboxEvent, error) {
	outbox, ok := m.inner.(store.EventOutboxStore)
	if !ok {
		return nil, nil
	}
	defer observe("append_outbox_events", time.Now())
	return outbox.AppendOutboxEvents(ctx, events)
}

func (m *metricsStore) ListOutboxEvents(ctx context.Context, query store.OutboxQuery) (*store.OutboxPage, error) {
	outbox, ok := m.inner.(store.EventOutboxStore)
	if !ok {
		return nil, nil
	}
	defer observe("list_outbox_events", time.Now())
	return outbox.ListOutboxEvents(ctx, query)
}

func (m *metricsStore) EvictOutboxEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	outbox, ok := m.inner.(store.EventOutboxStore)
	if !ok {
		return 0, nil
	}
	defer observe("evict_outbox_events_before", time.Now())
	return outbox.EvictOutboxEventsBefore(ctx, before, limit)
}

func (m *metricsStore) OutboxEnabled() bool {
	provider, ok := m.inner.(store.OutboxEnabledProvider)
	return ok && provider.OutboxEnabled()
}

var _ store.EventOutboxStore = (*metricsStore)(nil)
var _ store.OutboxEnabledProvider = (*metricsStore)(nil)
