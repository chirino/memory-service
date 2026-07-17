package turntraces

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type otelSink struct {
	tracer trace.Tracer
	tp     *sdktrace.TracerProvider
	cfg    Config
}

func newOTELSink(ctx context.Context, cfg Config) (*otelSink, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "memory-service-turn-traces"
	}
	attrs := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if cfg.Environment != "" {
		attrs = append(attrs,
			attribute.String("langfuse.environment", cfg.Environment),
			attribute.String("deployment.environment.name", cfg.Environment),
		)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes("", attrs...))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return &otelSink{
		tracer: tp.Tracer("memory-service/process/turn-traces"),
		tp:     tp,
		cfg:    cfg,
	}, nil
}

func (s *otelSink) EmitTurnSpan(ctx context.Context, span SpanData) error {
	if span.StartTime.IsZero() {
		span.StartTime = span.EndTime
	}
	spanCtx, otelSpan := s.tracer.Start(ctx, span.Name, trace.WithTimestamp(span.StartTime))
	defer otelSpan.End(trace.WithTimestamp(span.EndTime))
	otelSpan.SetAttributes(s.attributes(span)...)
	if span.Level == "ERROR" {
		otelSpan.SetStatus(codes.Error, span.StatusMessage)
	}
	log.Info("otel span sent",
		"name", span.Name,
		"type", "span",
		"conversationID", span.ConversationID,
		"sessionID", span.SessionID,
		"turnID", span.TurnID,
		"endReason", span.EndReason,
		"traceID", otelSpan.SpanContext().TraceID().String(),
		"spanID", otelSpan.SpanContext().SpanID().String(),
	)
	if len(span.ContextEntries) > 0 {
		s.emitLLMSpan(spanCtx, span)
	}
	log.Debug("turn span emitted",
		"name", span.Name,
		"conversationID", span.ConversationID,
		"sessionID", span.SessionID,
		"turnID", span.TurnID,
		"endReason", span.EndReason,
		"contextEntries", span.ContextCount,
		"input", span.Input,
		"output", span.Output,
	)
	return nil
}

func (s *otelSink) emitLLMSpan(ctx context.Context, span SpanData) {
	start := span.StartTime
	if start.IsZero() {
		start = span.EndTime
	}
	parent := trace.SpanContextFromContext(ctx)
	_, child := s.tracer.Start(ctx, "memory-service.llm", trace.WithTimestamp(start))
	defer child.End(trace.WithTimestamp(span.EndTime))
	inputValue := llmInputValue(span)
	outputValue := llmOutputValue(span)
	child.SetAttributes(s.llmAttributes(span, inputValue, outputValue)...)
	log.Info("otel span sent",
		"name", "memory-service.llm",
		"type", "generation",
		"conversationID", span.ConversationID,
		"sessionID", span.SessionID,
		"turnID", span.TurnID,
		"contextEntries", len(span.ContextEntries),
		"traceID", child.SpanContext().TraceID().String(),
		"spanID", child.SpanContext().SpanID().String(),
		"parentSpanID", parent.SpanID().String(),
	)
	log.Debug("llm span emitted",
		"name", "memory-service.llm",
		"conversationID", span.ConversationID,
		"sessionID", span.SessionID,
		"turnID", span.TurnID,
		"contextEntries", len(span.ContextEntries),
		"input", inputValue,
		"output", outputValue,
		"startTime", start,
		"endTime", span.EndTime,
	)
}

func (s *otelSink) Shutdown(ctx context.Context) error {
	if s == nil || s.tp == nil {
		return nil
	}
	return s.tp.Shutdown(ctx)
}

func (s *otelSink) attributes(span SpanData) []attribute.KeyValue {
	version := "turn-traces-v1"
	attrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.name", span.Name),
		attribute.String("langfuse.session.id", span.SessionID),
		attribute.String("session.id", span.SessionID),
		attribute.String("langfuse.version", version),
		attribute.String("langfuse.observation.type", "span"),
		attribute.String("langfuse.observation.level", span.Level),
		attribute.String("langfuse.trace.metadata.conversation_id", span.ConversationID),
		attribute.String("langfuse.trace.metadata.turn_id", span.TurnID),
		attribute.String("langfuse.trace.metadata.turn_end_reason", span.EndReason),
		attribute.String("langfuse.trace.metadata.start_cursor", span.StartCursor),
		attribute.String("langfuse.trace.metadata.end_cursor", span.EndCursor),
		attribute.String("langfuse.trace.metadata.user_entry_id", span.UserEntryID),
		attribute.Int("langfuse.trace.metadata.context_entry_count", span.ContextCount),
		attribute.String("langfuse.observation.metadata.conversation_id", span.ConversationID),
		attribute.String("langfuse.observation.metadata.turn_id", span.TurnID),
		attribute.String("langfuse.observation.metadata.turn_end_reason", span.EndReason),
		attribute.Int("langfuse.observation.metadata.context_entry_count", span.ContextCount),
	}
	if span.UserID != "" {
		attrs = append(attrs,
			attribute.String("langfuse.user.id", span.UserID),
			attribute.String("user.id", span.UserID),
		)
	}
	if span.AgentEntryID != "" {
		attrs = append(attrs, attribute.String("langfuse.trace.metadata.agent_entry_id", span.AgentEntryID))
	}
	if span.AgentID != "" {
		attrs = append(attrs, attribute.String("langfuse.trace.metadata.agent_id", span.AgentID))
	}
	if span.ClientID != "" {
		attrs = append(attrs, attribute.String("langfuse.trace.metadata.client_id", span.ClientID))
	}
	if span.Input != "" {
		attrs = append(attrs,
			attribute.String("langfuse.trace.input", span.Input),
			attribute.String("langfuse.observation.input", span.Input),
			attribute.String("input.value", span.Input),
		)
	}
	if span.Output != "" {
		attrs = append(attrs,
			attribute.String("langfuse.trace.output", span.Output),
			attribute.String("langfuse.observation.output", span.Output),
			attribute.String("output.value", span.Output),
		)
	}
	if span.StatusMessage != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.status_message", span.StatusMessage))
	}
	if s.cfg.RuntimeVersion != "" {
		attrs = append(attrs, attribute.String("langfuse.release", s.cfg.RuntimeVersion))
	}
	if s.cfg.Environment != "" {
		attrs = append(attrs, attribute.String("langfuse.environment", s.cfg.Environment))
	}
	if len(span.Tags) > 0 {
		attrs = append(attrs, attribute.StringSlice("langfuse.trace.tags", span.Tags))
	}
	for key, value := range span.Metadata {
		if value == "" {
			continue
		}
		attrs = append(attrs, attribute.String("langfuse.trace.metadata."+key, value))
	}
	return attrs
}

func (s *otelSink) llmAttributes(span SpanData, inputValue, outputValue string) []attribute.KeyValue {
	contextText := contextInput(span.ContextEntries)
	contextIDs := contextEntryIDs(span.ContextEntries)
	attrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.name", span.Name),
		attribute.String("langfuse.session.id", span.SessionID),
		attribute.String("session.id", span.SessionID),
		attribute.String("langfuse.version", "turn-traces-v1"),
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("langfuse.observation.level", span.Level),
		attribute.String("langfuse.observation.metadata.conversation_id", span.ConversationID),
		attribute.String("langfuse.observation.metadata.turn_id", span.TurnID),
		attribute.Int("langfuse.observation.metadata.context_entry_count", len(span.ContextEntries)),
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", "memory-service"),
	}
	if span.UserID != "" {
		attrs = append(attrs,
			attribute.String("langfuse.user.id", span.UserID),
			attribute.String("user.id", span.UserID),
		)
	}
	if s.cfg.RuntimeVersion != "" {
		attrs = append(attrs, attribute.String("langfuse.release", s.cfg.RuntimeVersion))
	}
	if s.cfg.Environment != "" {
		attrs = append(attrs, attribute.String("langfuse.environment", s.cfg.Environment))
	}
	if len(span.Tags) > 0 {
		attrs = append(attrs, attribute.StringSlice("langfuse.trace.tags", span.Tags))
	}
	for key, value := range span.Metadata {
		if value == "" {
			continue
		}
		attrs = append(attrs, attribute.String("langfuse.trace.metadata."+key, value))
	}
	if len(contextIDs) > 0 {
		attrs = append(attrs, attribute.StringSlice("langfuse.observation.metadata.context_entry_ids", contextIDs))
	}
	if span.UserEntryID != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.user_entry_id", span.UserEntryID))
	}
	if span.AgentEntryID != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata.agent_entry_id", span.AgentEntryID))
	}
	if inputValue != "" {
		attrs = append(attrs,
			attribute.String("langfuse.observation.input", inputValue),
			attribute.String("input.value", inputValue),
		)
	}
	if contextText != "" {
		attrs = append(attrs, attribute.String("gen_ai.prompt", contextText))
	}
	if outputValue != "" {
		attrs = append(attrs,
			attribute.String("langfuse.observation.output", outputValue),
			attribute.String("output.value", outputValue),
		)
	}
	if span.Output != "" {
		attrs = append(attrs, attribute.String("gen_ai.completion", span.Output))
	}
	return attrs
}

func llmInputValue(span SpanData) string {
	messages, _ := llmMessages(span)
	if len(messages) == 0 {
		text := strings.TrimSpace(contextInput(span.ContextEntries))
		if text == "" {
			return ""
		}
		messages = []LLMMessage{{Role: "system", Content: text}}
	}
	return marshalJSONValue(messages)
}

func llmOutputValue(span SpanData) string {
	_, output := llmMessages(span)
	if strings.TrimSpace(output.Content) == "" {
		return ""
	}
	return marshalJSONValue(output)
}

func llmMessages(span SpanData) ([]LLMMessage, LLMMessage) {
	messages := make([]LLMMessage, 0)
	for _, entry := range span.ContextEntries {
		messages = append(messages, entry.Messages...)
	}
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		output := messages[len(messages)-1]
		return append([]LLMMessage(nil), messages[:len(messages)-1]...), output
	}
	if strings.TrimSpace(span.Output) == "" {
		return messages, LLMMessage{}
	}
	return messages, LLMMessage{Role: "assistant", Content: span.Output}
}

func marshalJSONValue(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func contextInput(entries []ContextEntryData) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		if entry.ID == "" {
			parts = append(parts, entry.Text)
			continue
		}
		parts = append(parts, entry.ID+": "+entry.Text)
	}
	return strings.Join(parts, "\n")
}

func contextEntryIDs(entries []ContextEntryData) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.ID != "" {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

type dryRunSink struct{}

func (dryRunSink) EmitTurnSpan(_ context.Context, span SpanData) error {
	log.Info("turn span", "name", span.Name, "conversationID", span.ConversationID, "turnID", span.TurnID, "endReason", span.EndReason, "contextEntries", span.ContextCount)
	return nil
}
