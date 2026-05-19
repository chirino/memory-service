package turntraces

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
)

type Processor struct {
	cfg   Config
	sink  SpanSink
	state checkpointState
	nowFn func() time.Time
}

type checkpointState struct {
	Version            int                  `json:"version"`
	RuntimeID          string               `json:"runtimeId"`
	RuntimeVersion     string               `json:"runtimeVersion"`
	LastEventCursor    string               `json:"lastEventCursor"`
	UpdatedAt          time.Time            `json:"updatedAt"`
	OpenTurns          map[string]*openTurn `json:"openTurns"`
	ConversationOwners map[string]string    `json:"conversationOwners,omitempty"`
}

type openTurn struct {
	TurnID          string            `json:"turnId"`
	ConversationID  string            `json:"conversationId"`
	StartCursor     string            `json:"startCursor"`
	LatestCursor    string            `json:"latestCursor"`
	UserEntryID     string            `json:"userEntryId"`
	AgentEntryID    string            `json:"agentEntryId,omitempty"`
	ContextEntryIDs []string          `json:"contextEntryIds,omitempty"`
	ContextEntries  []contextEntry    `json:"contextEntries,omitempty"`
	StartedAt       time.Time         `json:"startedAt"`
	LatestEventAt   time.Time         `json:"latestEventAt"`
	UserID          string            `json:"userId,omitempty"`
	AgentID         string            `json:"agentId,omitempty"`
	ClientID        string            `json:"clientId,omitempty"`
	Input           string            `json:"input,omitempty"`
	Output          string            `json:"output,omitempty"`
	SeenCursors     map[string]bool   `json:"seenCursors,omitempty"`
	SeenEntryIDs    map[string]bool   `json:"seenEntryIds,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type contextEntry struct {
	ID          string    `json:"id"`
	Cursor      string    `json:"cursor,omitempty"`
	ContentType string    `json:"contentType,omitempty"`
	Text        string    `json:"text,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

type entryEvent struct {
	ID                  string            `json:"id"`
	ConversationID      string            `json:"conversationId"`
	ConversationGroupID string            `json:"conversationGroupId"`
	UserID              string            `json:"userId"`
	ClientID            string            `json:"clientId"`
	AgentID             string            `json:"agentId"`
	Channel             string            `json:"channel"`
	ContentType         string            `json:"contentType"`
	Content             []json.RawMessage `json:"content"`
	CreatedAt           time.Time         `json:"createdAt"`
}

type conversationEvent struct {
	ID                  string     `json:"id"`
	ConversationGroupID string     `json:"conversationGroupId"`
	OwnerUserID         string     `json:"ownerUserId"`
	AgentID             string     `json:"agentId"`
	ArchivedAt          *time.Time `json:"archivedAt"`
}

// NewProcessor creates a turn-trace event processor.
func NewProcessor(cfg Config, sink SpanSink) *Processor {
	if cfg.MaxOpenTurns <= 0 {
		cfg.MaxOpenTurns = 1000
	}
	if cfg.SessionIDMode == "" {
		cfg.SessionIDMode = "conversation"
	}
	if cfg.LangfuseName == "" {
		cfg.LangfuseName = defaultSpanName
	}
	if cfg.RuntimeVersion == "" {
		cfg.RuntimeVersion = "dev"
	}
	return &Processor{
		cfg:  cfg,
		sink: sink,
		state: checkpointState{
			Version:            1,
			RuntimeID:          "turn-traces",
			RuntimeVersion:     cfg.RuntimeVersion,
			OpenTurns:          map[string]*openTurn{},
			ConversationOwners: map[string]string{},
		},
		nowFn: func() time.Time { return time.Now().UTC() },
	}
}

func (p *Processor) ContentType() string {
	return contentType
}

func (p *Processor) Load(state json.RawMessage) error {
	if len(state) == 0 || string(state) == "null" {
		return nil
	}
	var loaded checkpointState
	if err := json.Unmarshal(state, &loaded); err != nil {
		return err
	}
	if loaded.Version != 1 {
		return fmt.Errorf("unsupported turn-traces checkpoint version %d", loaded.Version)
	}
	if loaded.OpenTurns == nil {
		loaded.OpenTurns = map[string]*openTurn{}
	}
	if loaded.ConversationOwners == nil {
		loaded.ConversationOwners = map[string]string{}
	}
	p.state = loaded
	return nil
}

func (p *Processor) Snapshot() (json.RawMessage, error) {
	raw, err := json.Marshal(p.state)
	return raw, err
}

func (p *Processor) Flush(ctx context.Context) error {
	now := p.nowFn()
	for _, turn := range p.openTurnsByAge() {
		switch {
		case p.cfg.MaxTurnAge > 0 && now.Sub(turn.StartedAt) >= p.cfg.MaxTurnAge:
			if err := p.closeTurn(ctx, turn, "max_turn_age", turn.LatestCursor, "", now, ""); err != nil {
				return err
			}
		case p.cfg.IdleTimeout > 0 && now.Sub(turn.LatestEventAt) >= p.cfg.IdleTimeout:
			if err := p.closeTurn(ctx, turn, "idle_timeout", turn.LatestCursor, "", now, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Processor) Handle(ctx context.Context, event processruntime.EventEnvelope) error {
	switch event.Kind {
	case "entry":
		return p.handleEntry(ctx, event)
	case "conversation":
		return p.handleConversation(ctx, event)
	default:
		if event.Cursor != "" {
			p.state.LastEventCursor = event.Cursor
		}
		return nil
	}
}

func (p *Processor) handleEntry(ctx context.Context, event processruntime.EventEnvelope) error {
	var entry entryEvent
	if err := json.Unmarshal(event.Data, &entry); err != nil {
		return fmt.Errorf("parse entry event: %w", err)
	}
	if entry.ConversationID == "" {
		return nil
	}
	eventTime := coalesceTime(entry.CreatedAt, event.Time, p.nowFn())
	role := historyRole(entry)
	log.Debug("turn-traces entry event received",
		"cursor", event.Cursor,
		"event", event.Event,
		"entryID", entry.ID,
		"conversationID", entry.ConversationID,
		"conversationGroupID", entry.ConversationGroupID,
		"channel", entry.Channel,
		"contentType", entry.ContentType,
		"role", role,
		"contentItems", len(entry.Content),
		"hasOpenTurn", p.state.OpenTurns[entry.ConversationID] != nil,
	)

	if turn := p.state.OpenTurns[entry.ConversationID]; turn != nil {
		if seen(turn, event.Cursor, entry.ID) {
			return nil
		}
	}

	switch {
	case isHistory(entry) && role == "USER":
		if existing := p.state.OpenTurns[entry.ConversationID]; existing != nil {
			if err := p.closeTurn(ctx, existing, "new_user_input", event.Cursor, "", eventTime, ""); err != nil {
				return err
			}
		}
		if err := p.ensureWindow(ctx); err != nil {
			return err
		}
		p.state.OpenTurns[entry.ConversationID] = &openTurn{
			TurnID:         makeTurnID(entry.ConversationID, event.Cursor, entry.ID),
			ConversationID: entry.ConversationID,
			StartCursor:    event.Cursor,
			LatestCursor:   event.Cursor,
			UserEntryID:    entry.ID,
			StartedAt:      eventTime,
			LatestEventAt:  eventTime,
			UserID:         entry.UserID,
			AgentID:        entry.AgentID,
			ClientID:       entry.ClientID,
			Input:          historyText(entry),
			SeenCursors:    map[string]bool{},
			SeenEntryIDs:   map[string]bool{},
			Metadata:       conversationMetadata(entry.ConversationGroupID),
		}
		markSeen(p.state.OpenTurns[entry.ConversationID], event.Cursor, entry.ID)
		p.state.LastEventCursor = event.Cursor
	case isHistory(entry) && role == "AI":
		turn := p.state.OpenTurns[entry.ConversationID]
		if turn == nil {
			p.state.LastEventCursor = event.Cursor
			return nil
		}
		turn.AgentEntryID = entry.ID
		if entry.AgentID != "" {
			turn.AgentID = entry.AgentID
		}
		if entry.ClientID != "" {
			turn.ClientID = entry.ClientID
		}
		turn.Output = historyText(entry)
		markSeen(turn, event.Cursor, entry.ID)
		if err := p.closeTurn(ctx, turn, "agent_history_entry", event.Cursor, entry.ID, eventTime, ""); err != nil {
			return err
		}
	case strings.EqualFold(entry.Channel, "context"):
		turn := p.state.OpenTurns[entry.ConversationID]
		if turn == nil {
			p.state.LastEventCursor = event.Cursor
			return nil
		}
		if entry.ID != "" && !turn.SeenEntryIDs[entry.ID] {
			turn.ContextEntryIDs = append(turn.ContextEntryIDs, entry.ID)
			turn.ContextEntries = append(turn.ContextEntries, contextEntry{
				ID:          entry.ID,
				Cursor:      event.Cursor,
				ContentType: entry.ContentType,
				Text:        entryText(entry),
				CreatedAt:   eventTime,
			})
		}
		if entry.AgentID != "" {
			turn.AgentID = entry.AgentID
		}
		if entry.ClientID != "" {
			turn.ClientID = entry.ClientID
		}
		if entry.ConversationGroupID != "" {
			turn.Metadata["conversation_group_id"] = entry.ConversationGroupID
		}
		turn.LatestCursor = event.Cursor
		turn.LatestEventAt = eventTime
		markSeen(turn, event.Cursor, entry.ID)
		p.state.LastEventCursor = event.Cursor
	default:
		p.state.LastEventCursor = event.Cursor
	}
	return nil
}

func (p *Processor) handleConversation(ctx context.Context, event processruntime.EventEnvelope) error {
	var conversation conversationEvent
	if err := json.Unmarshal(event.Data, &conversation); err != nil {
		return fmt.Errorf("parse conversation event: %w", err)
	}
	if conversation.ID == "" {
		return nil
	}
	if conversation.OwnerUserID != "" {
		p.state.ConversationOwners[conversation.ID] = conversation.OwnerUserID
	}
	if turn := p.state.OpenTurns[conversation.ID]; turn != nil {
		if conversation.AgentID != "" {
			turn.AgentID = conversation.AgentID
		}
		if event.Cursor != "" {
			turn.LatestCursor = event.Cursor
		}
		if conversation.ConversationGroupID != "" {
			if turn.Metadata == nil {
				turn.Metadata = map[string]string{}
			}
			turn.Metadata["conversation_group_id"] = conversation.ConversationGroupID
		}
		turn.LatestEventAt = coalesceTime(time.Time{}, event.Time, p.nowFn())
		if conversation.ArchivedAt != nil {
			if err := p.closeTurn(ctx, turn, "conversation_archived", event.Cursor, "", *conversation.ArchivedAt, ""); err != nil {
				return err
			}
		}
	}
	if event.Cursor != "" {
		p.state.LastEventCursor = event.Cursor
	}
	return nil
}

func (p *Processor) ensureWindow(ctx context.Context) error {
	for len(p.state.OpenTurns) >= p.cfg.MaxOpenTurns {
		oldest := p.openTurnsByAge()[0]
		if err := p.closeTurn(ctx, oldest, "checkpoint_window_limit", oldest.LatestCursor, "", p.nowFn(), ""); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) closeTurn(ctx context.Context, turn *openTurn, reason, endCursor, agentEntryID string, endTime time.Time, statusMessage string) error {
	if endCursor == "" {
		endCursor = turn.LatestCursor
	}
	if agentEntryID != "" {
		turn.AgentEntryID = agentEntryID
	}
	level := "DEFAULT"
	if reason != "agent_history_entry" {
		level = "WARNING"
	}
	userID := p.state.ConversationOwners[turn.ConversationID]
	userSource := "conversation_owner"
	if userID == "" {
		userID = turn.UserID
		userSource = "entry_user_id"
	}
	span := SpanData{
		Name:           p.cfg.LangfuseName,
		TurnID:         turn.TurnID,
		ConversationID: turn.ConversationID,
		SessionID:      turn.ConversationID,
		UserID:         userID,
		AgentID:        turn.AgentID,
		ClientID:       turn.ClientID,
		UserEntryID:    turn.UserEntryID,
		AgentEntryID:   turn.AgentEntryID,
		Input:          turn.Input,
		Output:         turn.Output,
		StartCursor:    turn.StartCursor,
		EndCursor:      endCursor,
		StartTime:      turn.StartedAt,
		EndTime:        coalesceTime(endTime, p.nowFn(), p.nowFn()),
		EndReason:      reason,
		ContextCount:   contextCount(turn),
		ContextEntries: contextEntries(turn),
		Level:          level,
		StatusMessage:  statusMessage,
		Tags:           []string{"memory-service", "turn-trace", "end:" + reason},
		Metadata: map[string]string{
			"conversation_id":     turn.ConversationID,
			"turn_id":             turn.TurnID,
			"turn_end_reason":     reason,
			"start_cursor":        turn.StartCursor,
			"end_cursor":          endCursor,
			"user_entry_id":       turn.UserEntryID,
			"context_entry_count": fmt.Sprintf("%d", contextCount(turn)),
			"user_source":         userSource,
		},
	}
	if p.cfg.SessionIDMode == "conversation-group" {
		if groupID := turn.Metadata["conversation_group_id"]; groupID != "" {
			span.SessionID = groupID
			span.Metadata["conversation_group_id"] = groupID
		}
	}
	if turn.AgentEntryID != "" {
		span.Metadata["agent_entry_id"] = turn.AgentEntryID
	}
	if turn.AgentID != "" {
		span.Tags = append(span.Tags, "agent:"+turn.AgentID)
		span.Metadata["agent_id"] = turn.AgentID
	}
	if turn.ClientID != "" {
		span.Metadata["client_id"] = turn.ClientID
	}
	if p.sink != nil {
		if err := p.sink.EmitTurnSpan(ctx, span); err != nil {
			if !p.cfg.DropOnExportFailure {
				return err
			}
		}
	}
	delete(p.state.OpenTurns, turn.ConversationID)
	if endCursor != "" {
		p.state.LastEventCursor = endCursor
	}
	p.state.UpdatedAt = p.nowFn()
	return nil
}

func (p *Processor) openTurnsByAge() []*openTurn {
	turns := make([]*openTurn, 0, len(p.state.OpenTurns))
	for _, turn := range p.state.OpenTurns {
		turns = append(turns, turn)
	}
	sort.Slice(turns, func(i, j int) bool {
		return turns[i].StartedAt.Before(turns[j].StartedAt)
	})
	return turns
}

func isHistory(entry entryEvent) bool {
	return strings.EqualFold(entry.Channel, "history") && (entry.ContentType == "history" || strings.HasPrefix(entry.ContentType, "history/"))
}

func conversationMetadata(groupID string) map[string]string {
	if groupID == "" {
		return map[string]string{}
	}
	return map[string]string{"conversation_group_id": groupID}
}

func historyRole(entry entryEvent) string {
	if !isHistory(entry) || len(entry.Content) == 0 {
		return ""
	}
	var block struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(entry.Content[0], &block); err != nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(block.Role))
}

func historyText(entry entryEvent) string {
	if !isHistory(entry) {
		return ""
	}
	return entryText(entry)
}

func entryText(entry entryEvent) string {
	parts := make([]string, 0, len(entry.Content))
	for _, raw := range entry.Content {
		var block struct {
			Text   string          `json:"text"`
			Events json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(raw, &block); err == nil && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
			continue
		}
		if text := lc4jEventText(block.Events); text != "" {
			parts = append(parts, text)
			continue
		}
		if len(raw) > 0 && string(raw) != "null" {
			parts = append(parts, string(raw))
		}
	}
	return strings.Join(parts, "\n")
}

func lc4jEventText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var events []struct {
		EventType string `json:"eventType"`
		Chunk     string `json:"chunk"`
		AIMessage struct {
			Text string `json:"text"`
		} `json:"aiMessage"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		if strings.TrimSpace(events[i].AIMessage.Text) != "" {
			return events[i].AIMessage.Text
		}
	}
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Chunk) != "" {
			parts = append(parts, event.Chunk)
		}
	}
	return strings.Join(parts, "")
}

func contextCount(turn *openTurn) int {
	if len(turn.ContextEntries) > 0 {
		return len(turn.ContextEntries)
	}
	return len(turn.ContextEntryIDs)
}

func contextEntries(turn *openTurn) []ContextEntryData {
	if len(turn.ContextEntries) == 0 {
		out := make([]ContextEntryData, 0, len(turn.ContextEntryIDs))
		for _, id := range turn.ContextEntryIDs {
			out = append(out, ContextEntryData{ID: id})
		}
		return out
	}
	out := make([]ContextEntryData, 0, len(turn.ContextEntries))
	for _, entry := range turn.ContextEntries {
		out = append(out, ContextEntryData{
			ID:          entry.ID,
			Cursor:      entry.Cursor,
			ContentType: entry.ContentType,
			Text:        entry.Text,
			CreatedAt:   entry.CreatedAt,
		})
	}
	return out
}

func markSeen(turn *openTurn, cursor, entryID string) {
	if turn.SeenCursors == nil {
		turn.SeenCursors = map[string]bool{}
	}
	if turn.SeenEntryIDs == nil {
		turn.SeenEntryIDs = map[string]bool{}
	}
	if cursor != "" {
		turn.SeenCursors[cursor] = true
	}
	if entryID != "" {
		turn.SeenEntryIDs[entryID] = true
	}
}

func seen(turn *openTurn, cursor, entryID string) bool {
	return (cursor != "" && turn.SeenCursors[cursor]) || (entryID != "" && turn.SeenEntryIDs[entryID])
}

func makeTurnID(conversationID, cursor, entryID string) string {
	h := sha1.Sum([]byte(conversationID + "\x00" + cursor + "\x00" + entryID))
	return conversationID + ":" + hex.EncodeToString(h[:8])
}

func coalesceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Now().UTC()
}
