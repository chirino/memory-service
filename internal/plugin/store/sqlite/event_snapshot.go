package sqlite

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
)

func (s *SQLiteStore) AdminListEventSnapshotConversations(ctx context.Context, after *registrystore.EventSnapshotConversationCursor, limit int) ([]registrystore.ConversationSummary, *registrystore.EventSnapshotConversationCursor, error) {
	if limit <= 0 {
		limit = 100
	}
	q := s.dbFor(ctx).Model(&model.Conversation{})
	if after != nil {
		q = q.Where("created_at > ? OR (created_at = ? AND id > ?)", after.CreatedAt, after.CreatedAt, after.ID)
	}
	var rows []model.Conversation
	if err := q.Order("created_at ASC, id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("list event snapshot conversations: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if err := s.hydrateEventSnapshotConversationForks(ctx, rows); err != nil {
		return nil, nil, err
	}
	result := make([]registrystore.ConversationSummary, 0, len(rows))
	for i := range rows {
		summary, err := s.eventSnapshotConversationSummary(&rows[i])
		if err != nil {
			return nil, nil, err
		}
		result = append(result, summary)
	}
	if !hasMore || len(rows) == 0 {
		return result, nil, nil
	}
	last := rows[len(rows)-1]
	return result, &registrystore.EventSnapshotConversationCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (s *SQLiteStore) hydrateEventSnapshotConversationForks(ctx context.Context, rows []model.Conversation) error {
	if len(rows) == 0 {
		return nil
	}
	conversationIDs := make([]string, 0, len(rows))
	groupIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		conversationIDs = append(conversationIDs, rows[i].ID)
		groupIDs = append(groupIDs, rows[i].ConversationGroupID)
	}
	var ancestry []model.ConversationAncestry
	if err := s.dbFor(ctx).Where("depth = 1 AND descendant_conversation_id IN ?", conversationIDs).Find(&ancestry).Error; err != nil {
		return fmt.Errorf("load event snapshot conversation ancestry: %w", err)
	}
	directByConversation := make(map[string]model.ConversationAncestry, len(ancestry))
	for _, direct := range ancestry {
		directByConversation[direct.DescendantConversationID] = direct
	}
	type lineageRow struct {
		ConversationGroupID     uuid.UUID  `gorm:"column:conversation_group_id"`
		StartedByConversationID *string    `gorm:"column:started_by_conversation_id"`
		StartedByEntryID        *uuid.UUID `gorm:"column:started_by_entry_id"`
	}
	var lineage []lineageRow
	if err := s.dbFor(ctx).Model(&model.Conversation{}).
		Select("conversation_group_id, started_by_conversation_id, started_by_entry_id").
		Where("conversation_group_id IN ? AND started_by_conversation_id IS NOT NULL", groupIDs).
		Find(&lineage).Error; err != nil {
		return fmt.Errorf("load event snapshot conversation lineage: %w", err)
	}
	lineageByGroup := make(map[uuid.UUID]lineageRow, len(lineage))
	for _, item := range lineage {
		lineageByGroup[item.ConversationGroupID] = item
	}
	for i := range rows {
		if direct, ok := directByConversation[rows[i].ID]; ok {
			parentID := direct.AncestorConversationID
			rows[i].ForkedAtConversationID = &parentID
			rows[i].ForkedAtEntryID = direct.ForkedAtEntryID
		}
		if item, ok := lineageByGroup[rows[i].ConversationGroupID]; ok {
			rows[i].StartedByConversationID = item.StartedByConversationID
			rows[i].StartedByEntryID = item.StartedByEntryID
		}
	}
	return nil
}

func (s *SQLiteStore) eventSnapshotConversationSummary(row *model.Conversation) (registrystore.ConversationSummary, error) {
	title, err := s.decryptConversationTitle(row.ID, row.Title)
	if err != nil {
		return registrystore.ConversationSummary{}, err
	}
	return registrystore.ConversationSummary{ID: row.ID, Title: title, OwnerUserID: row.OwnerUserID, ClientID: row.ClientID, AgentID: row.AgentID, Metadata: row.Metadata, ConversationGroupID: row.ConversationGroupID, ForkedAtConversationID: row.ForkedAtConversationID, ForkedAtEntryID: row.ForkedAtEntryID, StartedByConversationID: row.StartedByConversationID, StartedByEntryID: row.StartedByEntryID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt, AccessLevel: model.AccessLevelOwner}, nil
}

func (s *SQLiteStore) AdminListEventSnapshotEntries(ctx context.Context, after *registrystore.EventSnapshotEntryCursor, limit int) ([]model.Entry, *registrystore.EventSnapshotEntryCursor, error) {
	if limit <= 0 {
		limit = 100
	}
	q := s.dbFor(ctx).Model(&model.Entry{})
	if after != nil {
		q = q.Where("created_at > ? OR (created_at = ? AND id > ?)", after.CreatedAt, after.CreatedAt, after.ID)
	}
	var rows []model.Entry
	if err := q.Order("created_at ASC, id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("list event snapshot entries: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if err := decryptEntries(s, rows); err != nil {
		return nil, nil, err
	}
	if !hasMore || len(rows) == 0 {
		return rows, nil, nil
	}
	last := rows[len(rows)-1]
	return rows, &registrystore.EventSnapshotEntryCursor{CreatedAt: last.CreatedAt, ID: last.ID.String()}, nil
}
