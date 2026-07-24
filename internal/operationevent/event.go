package operationevent

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/log"
)

const (
	maxFieldLength   = 128
	maxCursorLength  = 128
	maxMessageLength = 256
)

// Result is the transport-independent outcome of an operation.
type Result string

const (
	ResultSuccess         Result = "success"
	ResultInvalid         Result = "invalid"
	ResultUnauthenticated Result = "unauthenticated"
	ResultForbidden       Result = "forbidden"
	ResultNotFound        Result = "not_found"
	ResultConflict        Result = "conflict"
	ResultRateLimited     Result = "rate_limited"
	ResultTimedOut        Result = "timed_out"
	ResultCanceled        Result = "canceled"
	ResultRejected        Result = "rejected"
	ResultFailed          Result = "failed"
	ResultRetrying        Result = "retrying"
)

// Level is the severity used for a canonical event.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Snapshot is an immutable copy of an event's sparse canonical fields.
type Snapshot struct {
	Phase                 string              `json:"phase,omitempty"`
	RequestID             string              `json:"requestID,omitempty"`
	Status                any                 `json:"status,omitempty"`
	Duration              time.Duration       `json:"duration,omitempty"`
	Result                Result              `json:"result,omitempty"`
	Reason                string              `json:"reason,omitempty"`
	ErrorCode             string              `json:"errorCode,omitempty"`
	ErrorType             string              `json:"errorType,omitempty"`
	UserID                string              `json:"userID,omitempty"`
	ClientID              string              `json:"clientID,omitempty"`
	AgentID               string              `json:"agentID,omitempty"`
	ConversationID        string              `json:"conversationID,omitempty"`
	EntryID               string              `json:"entryID,omitempty"`
	AttachmentID          string              `json:"attachmentID,omitempty"`
	MemoryID              string              `json:"memoryID,omitempty"`
	TaskID                string              `json:"taskID,omitempty"`
	ConnectionID          string              `json:"connectionID,omitempty"`
	Cursor                string              `json:"cursor,omitempty"`
	ProviderName          string              `json:"providerName,omitempty"`
	ProviderStatusCode    int                 `json:"providerStatusCode,omitempty"`
	ProviderErrorCode     string              `json:"providerErrorCode,omitempty"`
	ProviderTransactionID string              `json:"providerTransactionID,omitempty"`
	RetryAttempt          int                 `json:"retryAttempt,omitempty"`
	WorkCount             int64               `json:"workCount,omitempty"`
	FailureCount          int64               `json:"failureCount,omitempty"`
	ErrorDetails          []ErrorDetailsEntry `json:"errorDetails,omitempty"`
	ErrorDetailsTruncated bool                `json:"errorDetailsTruncated,omitempty"`
}

// Emitter receives an immutable event record. It is primarily useful for focused tests.
type Emitter func(message string, level Level, snapshot Snapshot)

type Option func(*Event)

// WithEmitter overrides the default structured logger sink.
func WithEmitter(emitter Emitter) Option {
	return func(e *Event) {
		if emitter != nil {
			e.emitter = emitter
		}
	}
}

// Event is a concurrency-safe canonical operation event.
type Event struct {
	mu              sync.Mutex
	message         string
	startedAt       time.Time
	fields          Snapshot
	startEmitted    bool
	terminalEmitted bool
	emitter         Emitter
}

// New constructs an event. Call EmitTerminal exactly once at the operation boundary.
func New(message string, options ...Option) *Event {
	e := &Event{
		message:   sanitize(message, maxMessageLength),
		startedAt: time.Now(),
		emitter:   emitLog,
	}
	for _, option := range options {
		option(e)
	}
	return e
}

// Message returns the stable operation name.
func (e *Event) Message() string {
	if e == nil {
		return ""
	}
	return e.message
}

type contextKey struct{}

// WithContext attaches an event to a context.
func WithContext(ctx context.Context, event *Event) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, event)
}

// FromContext returns the event attached to ctx, if any.
func FromContext(ctx context.Context) *Event {
	if ctx == nil {
		return nil
	}
	event, _ := ctx.Value(contextKey{}).(*Event)
	return event
}

// Snapshot returns a deep, immutable copy of the current fields.
func (e *Event) Snapshot() Snapshot {
	if e == nil {
		return Snapshot{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneSnapshot(e.fields)
}

func cloneSnapshot(source Snapshot) Snapshot {
	copy := source
	if source.ErrorDetails != nil {
		copy.ErrorDetails = append([]ErrorDetailsEntry(nil), source.ErrorDetails...)
		for i := range copy.ErrorDetails {
			if source.ErrorDetails[i].Provider != nil {
				provider := *source.ErrorDetails[i].Provider
				copy.ErrorDetails[i].Provider = &provider
			}
		}
	}
	return copy
}

func (e *Event) SetRequestID(value string) { e.setString(&e.fields.RequestID, value, maxFieldLength) }
func (e *Event) SetReason(value string)    { e.setString(&e.fields.Reason, value, maxFieldLength) }
func (e *Event) SetErrorCode(value string) { e.setString(&e.fields.ErrorCode, value, maxFieldLength) }
func (e *Event) SetErrorType(value string) { e.setString(&e.fields.ErrorType, value, maxFieldLength) }
func (e *Event) SetUserID(value string)    { e.setString(&e.fields.UserID, value, maxFieldLength) }
func (e *Event) SetClientID(value string)  { e.setString(&e.fields.ClientID, value, maxFieldLength) }
func (e *Event) SetAgentID(value string)   { e.setString(&e.fields.AgentID, value, maxFieldLength) }
func (e *Event) SetConversationID(value string) {
	e.setString(&e.fields.ConversationID, value, maxFieldLength)
}
func (e *Event) SetEntryID(value string) { e.setString(&e.fields.EntryID, value, maxFieldLength) }
func (e *Event) SetAttachmentID(value string) {
	e.setString(&e.fields.AttachmentID, value, maxFieldLength)
}
func (e *Event) SetMemoryID(value string) { e.setString(&e.fields.MemoryID, value, maxFieldLength) }
func (e *Event) SetTaskID(value string)   { e.setString(&e.fields.TaskID, value, maxFieldLength) }
func (e *Event) SetConnectionID(value string) {
	e.setString(&e.fields.ConnectionID, value, maxFieldLength)
}
func (e *Event) SetCursor(value string) { e.setString(&e.fields.Cursor, value, maxCursorLength) }

func (e *Event) setString(target *string, value string, limit int) {
	if e == nil {
		return
	}
	e.mu.Lock()
	*target = sanitize(value, limit)
	e.mu.Unlock()
}

func (e *Event) SetHTTPStatus(status int) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.fields.Status = status
	e.mu.Unlock()
}

func (e *Event) SetGRPCStatus(status string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.fields.Status = sanitize(status, maxFieldLength)
	e.mu.Unlock()
}

func (e *Event) SetRetryAttempt(value int) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if value >= 0 {
		e.fields.RetryAttempt = value
	}
	e.mu.Unlock()
}

func (e *Event) SetWorkCount(value int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if value >= 0 {
		e.fields.WorkCount = value
	}
	e.mu.Unlock()
}

func (e *Event) SetFailureCount(value int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if value >= 0 {
		e.fields.FailureCount = value
	}
	e.mu.Unlock()
}

func (e *Event) SetProvider(details ErrorDetailsProvider) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.fields.ProviderName = sanitize(details.Name, maxFieldLength)
	e.fields.ProviderStatusCode = details.StatusCode
	e.fields.ProviderErrorCode = sanitize(details.ErrorCode, maxFieldLength)
	e.fields.ProviderTransactionID = sanitize(details.TransactionID, 256)
	e.mu.Unlock()
}

// EmitStart emits at most one start record. Use it only for substantial or streaming work.
func (e *Event) EmitStart() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.startEmitted || e.terminalEmitted {
		e.mu.Unlock()
		return false
	}
	e.startEmitted = true
	e.fields.Phase = "start"
	snapshot := cloneSnapshot(e.fields)
	emitter := e.emitter
	message := e.message
	e.mu.Unlock()
	emitter(message, LevelInfo, snapshot)
	return true
}

// EmitTerminal emits at most one completion record.
func (e *Event) EmitTerminal(result Result) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.terminalEmitted {
		e.mu.Unlock()
		return false
	}
	e.terminalEmitted = true
	e.fields.Phase = "complete"
	e.fields.Duration = time.Since(e.startedAt)
	e.fields.Result = result
	snapshot := cloneSnapshot(e.fields)
	emitter := e.emitter
	message := e.message
	e.mu.Unlock()
	emitter(message, LevelForResult(result), snapshot)
	return true
}

func sanitize(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	runes := []rune(clean)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}

func emitLog(message string, level Level, snapshot Snapshot) {
	args := snapshotLogArgs(snapshot)
	switch level {
	case LevelError:
		log.Error(message, args...)
	case LevelWarn:
		log.Warn(message, args...)
	default:
		log.Info(message, args...)
	}
}

func snapshotLogArgs(s Snapshot) []any {
	args := make([]any, 0, 48)
	add := func(name string, value any, present bool) {
		if present {
			args = append(args, name, value)
		}
	}
	add("phase", s.Phase, s.Phase != "")
	add("requestID", s.RequestID, s.RequestID != "")
	add("status", s.Status, s.Status != nil)
	add("duration", s.Duration, s.Duration != 0)
	add("result", s.Result, s.Result != "")
	add("reason", s.Reason, s.Reason != "")
	add("errorCode", s.ErrorCode, s.ErrorCode != "")
	add("errorType", s.ErrorType, s.ErrorType != "")
	add("userID", s.UserID, s.UserID != "")
	add("clientID", s.ClientID, s.ClientID != "")
	add("agentID", s.AgentID, s.AgentID != "")
	add("conversationID", s.ConversationID, s.ConversationID != "")
	add("entryID", s.EntryID, s.EntryID != "")
	add("attachmentID", s.AttachmentID, s.AttachmentID != "")
	add("memoryID", s.MemoryID, s.MemoryID != "")
	add("taskID", s.TaskID, s.TaskID != "")
	add("connectionID", s.ConnectionID, s.ConnectionID != "")
	add("cursor", s.Cursor, s.Cursor != "")
	add("providerName", s.ProviderName, s.ProviderName != "")
	add("providerStatusCode", s.ProviderStatusCode, s.ProviderStatusCode != 0)
	add("providerErrorCode", s.ProviderErrorCode, s.ProviderErrorCode != "")
	add("providerTransactionID", s.ProviderTransactionID, s.ProviderTransactionID != "")
	add("retryAttempt", s.RetryAttempt, s.RetryAttempt != 0)
	add("workCount", s.WorkCount, s.WorkCount != 0)
	add("failureCount", s.FailureCount, s.FailureCount != 0)
	add("errorDetails", s.ErrorDetails, len(s.ErrorDetails) != 0)
	add("errorDetailsTruncated", s.ErrorDetailsTruncated, s.ErrorDetailsTruncated)
	return args
}

// LevelForResult returns the canonical severity for result.
func LevelForResult(result Result) Level {
	switch result {
	case ResultFailed:
		return LevelError
	case ResultInvalid, ResultUnauthenticated, ResultForbidden, ResultNotFound,
		ResultConflict, ResultRateLimited, ResultTimedOut, ResultRejected, ResultRetrying:
		return LevelWarn
	default:
		return LevelInfo
	}
}
