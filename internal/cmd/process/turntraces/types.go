package turntraces

import (
	"context"
	"time"
)

const (
	contentType     = "application/vnd.memory-service.turn-trace-checkpoint+json;v=1"
	defaultSpanName = "memory-service.turn"
)

type Config struct {
	IdleTimeout          time.Duration
	MaxTurnAge           time.Duration
	MaxOpenTurns         int
	LangfuseName         string
	SessionIDMode        string
	ServiceName          string
	RuntimeVersion       string
	Environment          string
	DryRun               bool
	DropOnExportFailure  bool
	CloseOpenOnShutdown  bool
	CheckpointWindowName string
}

type ContextFetcher interface {
	FetchContextEntries(ctx context.Context, conversationID string, upToEntryID string) ([]ContextEntryData, error)
}

type SpanData struct {
	Name           string
	TurnID         string
	ConversationID string
	SessionID      string
	UserID         string
	AgentID        string
	ClientID       string
	UserEntryID    string
	AgentEntryID   string
	Input          string
	Output         string
	StartCursor    string
	EndCursor      string
	StartTime      time.Time
	EndTime        time.Time
	EndReason      string
	ContextCount   int
	ContextEntries []ContextEntryData
	Level          string
	StatusMessage  string
	Tags           []string
	Metadata       map[string]string
}

type ContextEntryData struct {
	ID          string
	Cursor      string
	ContentType string
	Epoch       int64
	Text        string
	Messages    []LLMMessage
	CreatedAt   time.Time
}

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type SpanSink interface {
	EmitTurnSpan(ctx context.Context, span SpanData) error
}
