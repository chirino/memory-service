package clickhouse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
)

func TestBundledExampleAppProjections(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	projectionPath := filepath.Join(filepath.Dir(testFile), "..", "..", "..", "..", "deploy", "clickhouse", "projections")
	set, err := LoadProjectionSet(context.Background(), []string{projectionPath})
	require.NoError(t, err)

	selectors := make(map[string]string, len(set.Items))
	rowModes := make(map[string]string, len(set.Items))
	for _, projection := range set.Items {
		selectors[projection.Name] = projection.Resource + ":" + projection.Selector
		rowModes[projection.Name] = projection.RowMode
	}
	require.Equal(t, map[string]string{
		"history_v1":                 "entry:history",
		"history_lc4j_v1":            "entry:history/lc4j",
		"history_lc4j_events_v1":     "entry:history/lc4j",
		"history_vercelai_v1":        "entry:history/vercelai",
		"history_vercelai_events_v1": "entry:history/vercelai",
		"lc4j_messages_v1":           "entry:LC4J",
		"spring_ai_messages_v1":      "entry:SpringAI",
		"langgraph_checkpoint_v1":    "entry:LangGraph/checkpoint",
		"vercelai_messages_v1":       "entry:vercelai",
		"default_memory_v1":          "memory:default/v1",
		"cognition_memory_v1":        "memory:cognition/v1",
	}, selectors)
	for _, name := range []string{"history_lc4j_events_v1", "history_vercelai_events_v1", "lc4j_messages_v1", "spring_ai_messages_v1", "vercelai_messages_v1"} {
		require.Equal(t, ProjectionRowsMany, rowModes[name], name)
	}
	selected, err := LoadProjectionSet(context.Background(), []string{filepath.Join(projectionPath, "history-lc4j.yaml")})
	require.NoError(t, err)
	require.Len(t, selected.Items, 1)
	require.Equal(t, "history_lc4j_v1", selected.Items[0].Name)

	historyItem := map[string]any{
		"role": "AI",
		"text": "Which orders are delayed?",
		"events": []any{
			map[string]any{"eventType": "BeforeToolExecution", "id": "call-1", "toolName": "orderStatus", "arguments": `{"orderId":"1042"}`, "input": map[string]any{"orderId": "1042"}},
			map[string]any{"eventType": "ToolExecuted", "id": "call-1", "toolName": "orderStatus", "output": `{"delayDays":1}`},
		},
		"attachments": []any{map[string]any{"contentType": "application/pdf", "attachmentId": "attachment-1"}},
	}
	entryInputs := map[string]map[string]any{
		"history": {"content": []any{historyItem}},
		"history/lc4j": {"content": []any{map[string]any{
			"role": "AI",
			"text": "Which orders are delayed?",
			"events": []any{
				map[string]any{"eventType": "PartialThinking", "text": "Check the order."},
				map[string]any{"eventType": "BeforeToolExecution", "id": "call-1", "toolName": "orderStatus", "arguments": `{"orderId":"1042"}`},
				map[string]any{"eventType": "ToolExecuted", "id": "call-1", "toolName": "orderStatus", "output": "not json"},
				map[string]any{"eventType": "PartialResponse", "chunk": "Order 1042 is late."},
				map[string]any{"eventType": "ChatCompleted", "metadata": map[string]any{"modelName": "gpt-test", "finishReason": "STOP", "tokenUsage": map[string]any{"inputTokenCount": float64(12), "outputTokenCount": float64(5), "totalTokenCount": float64(17)}}},
			},
			"attachments": []any{map[string]any{"contentType": "application/pdf", "attachmentId": "attachment-1"}},
		}}},
		"history/vercelai": {"content": []any{map[string]any{
			"role": "AI",
			"text": "Which orders are delayed?",
			"events": []any{
				map[string]any{"eventType": "BeforeToolExecution", "id": "call-1", "toolName": "orderStatus", "input": map[string]any{"orderId": "1042"}},
				map[string]any{"eventType": "ToolExecuted", "id": "call-1", "toolName": "orderStatus", "output": map[string]any{"delayDays": float64(1)}},
				map[string]any{"eventType": "PartialResponse", "chunk": "Order 1042 is late."},
				map[string]any{"eventType": "ChatCompleted", "finishReason": "stop", "response": map[string]any{"modelId": "gpt-test"}, "usage": map[string]any{"inputTokens": float64(3)}, "totalUsage": map[string]any{"inputTokens": float64(12), "outputTokens": float64(5), "totalTokens": float64(17)}},
			},
			"attachments": []any{map[string]any{"contentType": "application/pdf", "attachmentId": "attachment-1"}},
		}}},
		"LangGraph/checkpoint": {"content": []any{map[string]any{
			"checkpoint_id":        "checkpoint-1",
			"checkpoint_ns":        "customer-support",
			"parent_checkpoint_id": nil,
			"checkpoint":           map[string]any{"channel_values": map[string]any{"status": "open"}},
			"metadata":             map[string]any{"step": float64(1)},
		}}},
		"LC4J": {"content": []any{
			map[string]any{"type": "SYSTEM", "text": "Answer briefly."},
			map[string]any{"type": "USER", "contents": []any{map[string]any{"type": "TEXT", "text": "Which orders "}, map[string]any{"type": "TEXT", "text": "are delayed?"}}},
			map[string]any{"type": "AI", "toolExecutionRequests": []any{map[string]any{"id": "call-1", "name": "orderStatus", "arguments": "{}"}}},
			map[string]any{"type": "TOOL_EXECUTION_RESULT", "id": "call-1", "toolName": "orderStatus", "text": "late"},
		}},
		"SpringAI": {"content": []any{
			map[string]any{"role": "system", "text": "Answer from the supplied research."},
			map[string]any{"role": "user", "text": "Summarize the latest research notes."},
			map[string]any{"role": "assistant", "text": "Three sources agree on the main result."},
		}},
		"vercelai": {"content": []any{
			map[string]any{"role": "user", "content": "Which orders are delayed?"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Checking. "}, map[string]any{"type": "tool-call", "toolCallId": "call-1", "toolName": "orderStatus", "input": map[string]any{}}}},
			map[string]any{"role": "tool", "content": []any{map[string]any{"type": "tool-result", "toolCallId": "call-1", "toolName": "orderStatus", "output": map[string]any{"type": "text", "value": "late"}}}},
		}},
	}
	memoryInput := map[string]any{
		"attributes": map[string]any{
			"namespace":      "support",
			"sub":            "customer-1",
			"memoryKind":     "preference",
			"runtimeId":      "runtime-1",
			"runtimeVersion": "1",
			"confidence":     "high",
			"observedAt":     "2026-09-18T12:00:00Z",
			"effectiveAt":    "2026-09-18T12:00:00Z",
		},
		"value": map[string]any{"preferredLanguage": "en"},
	}
	evaluated := map[string][]map[string]any{}
	for _, projection := range set.Items {
		t.Run("evaluates_"+projection.Name, func(t *testing.T) {
			input := memoryInput
			if projection.Resource == "entry" {
				input = entryInputs[projection.Selector]
				require.NotNil(t, input, projection.Selector)
			}
			rows, code := projection.Rows(context.Background(), ProjectionRow{ResourceID: "resource-1"}, input)
			require.Empty(t, code)
			byName := make([]map[string]any, len(rows))
			for i, row := range rows {
				require.Equal(t, projection.RowMode == ProjectionRowsMany, row.Multi)
				require.Len(t, row.Values, len(projection.ColumnNames))
				byName[i] = make(map[string]any, len(row.Values))
				for index, name := range projection.ColumnNames {
					byName[i][name] = row.Values[index]
				}
			}
			evaluated[projection.Name] = byName
		})
	}

	for _, name := range []string{"history_v1", "history_lc4j_v1", "history_vercelai_v1"} {
		rows := evaluated[name]
		require.Len(t, rows, 1, name)
		require.Equal(t, "AI", rows[0]["role"])
		require.Equal(t, "Which orders are delayed?", rows[0]["text"])
		require.Equal(t, uint64(1), rows[0]["attachment_count"])
		require.Equal(t, []string{"application/pdf"}, rows[0]["attachment_content_types"])
	}
	require.Equal(t, []string{"BeforeToolExecution", "ToolExecuted"}, evaluated["history_v1"][0]["event_types"])
	require.Equal(t, uint64(1), evaluated["history_lc4j_v1"][0]["tool_call_count"])
	require.Equal(t, uint64(1), evaluated["history_vercelai_v1"][0]["tool_result_count"])

	lc4jEvents := evaluated["history_lc4j_events_v1"]
	require.Len(t, lc4jEvents, 5)
	require.Equal(t, "Check the order.", lc4jEvents[0]["text"])
	require.Equal(t, "call-1", lc4jEvents[1]["tool_call_id"])
	require.Equal(t, "orderStatus", lc4jEvents[1]["tool_name"])
	require.JSONEq(t, `{"orderId":"1042"}`, lc4jEvents[1]["tool_input"].(string))
	require.Equal(t, `"not json"`, lc4jEvents[2]["tool_output"])
	require.Equal(t, "Order 1042 is late.", lc4jEvents[3]["text"])
	require.Equal(t, "ChatCompleted", lc4jEvents[4]["event_type"])
	require.Equal(t, "gpt-test", lc4jEvents[4]["model_name"])
	require.Equal(t, "STOP", lc4jEvents[4]["finish_reason"])
	require.Equal(t, uint64(12), lc4jEvents[4]["input_tokens"])
	require.Equal(t, uint64(17), lc4jEvents[4]["total_tokens"])

	vercelEvents := evaluated["history_vercelai_events_v1"]
	require.Len(t, vercelEvents, 4)
	require.JSONEq(t, `{"orderId":"1042"}`, vercelEvents[0]["tool_input"].(string))
	require.JSONEq(t, `{"delayDays":1}`, vercelEvents[1]["tool_output"].(string))
	require.Equal(t, "Order 1042 is late.", vercelEvents[2]["text"])
	require.Equal(t, "gpt-test", vercelEvents[3]["model_name"])
	require.Equal(t, uint64(12), vercelEvents[3]["input_tokens"])
	require.Equal(t, uint64(5), vercelEvents[3]["output_tokens"])
	require.Nil(t, vercelEvents[3]["text"])

	lc4jMessages := evaluated["lc4j_messages_v1"]
	require.Len(t, lc4jMessages, 4)
	require.Equal(t, "SYSTEM", lc4jMessages[0]["message_type"])
	require.Equal(t, "Which orders are delayed?", lc4jMessages[1]["text"])
	require.Nil(t, lc4jMessages[2]["text"])
	require.Equal(t, []string{"orderStatus"}, lc4jMessages[2]["tool_names"])
	require.Equal(t, []string{"orderStatus"}, lc4jMessages[3]["tool_names"])
	require.Equal(t, []string{}, lc4jMessages[0]["tool_names"])

	springMessages := evaluated["spring_ai_messages_v1"]
	require.Len(t, springMessages, 3)
	require.Equal(t, "system", springMessages[0]["role"])
	require.Equal(t, "Three sources agree on the main result.", springMessages[2]["text"])

	vercelMessages := evaluated["vercelai_messages_v1"]
	require.Len(t, vercelMessages, 3)
	require.Equal(t, "Which orders are delayed?", vercelMessages[0]["text"])
	require.Equal(t, "Checking. ", vercelMessages[1]["text"])
	require.Equal(t, []string{"orderStatus"}, vercelMessages[1]["tool_names"])
	require.Equal(t, "tool", vercelMessages[2]["role"])
	require.Nil(t, vercelMessages[2]["text"])
}

func TestLoadProjectionSetFromDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.yml"), []byte(testProjectionYAML("second_v1", "second/v1")), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(testProjectionYAML("first_v1", "first/v1")), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory-kind.yaml"), []byte("apiVersion: memory-service/v1\nkind: MemoryKind\nmetadata:\n  name: ignored/v1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "list.yml"), []byte("- unrelated\n- document\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("not yaml"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "nested.yaml"), []byte(testProjectionYAML("nested_v1", "nested/v1")), 0o600))

	set, err := LoadProjectionSet(context.Background(), []string{dir})
	require.NoError(t, err)
	require.Len(t, set.Items, 2)
	require.Equal(t, "first_v1", set.Items[0].Name)
	require.Equal(t, "second_v1", set.Items[1].Name)
}

func TestLoadProjectionSetRequiresAProjectionResource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("kind: OtherResource\n"), 0o600))
	_, err := LoadProjectionSet(context.Background(), []string{dir})
	require.ErrorContains(t, err, "no AnalyticsProjection resources")
}

func TestLoadProjectionSetStrictlyDecodesProjectionResources(t *testing.T) {
	dir := t.TempDir()
	manifest := testProjectionYAML("strict_v1", "strict/v1") + "unknownField: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "strict.yaml"), []byte(manifest), 0o600))
	_, err := LoadProjectionSet(context.Background(), []string{dir})
	require.ErrorContains(t, err, "field unknownField not found")
}

func TestProjectionNameIsExactTableName(t *testing.T) {
	projection, err := compileProjection(context.Background(), testProjectionManifest("history_lc4j_events_v1", "history/lc4j"), clickHouseProjectionCapabilities)
	require.NoError(t, err)
	require.Equal(t, "history_lc4j_events_v1", projection.Name)
	require.Equal(t, projection.Name, projection.TableName)
}

func TestProjectionNameValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		wantErr string
	}{
		{name: "history/lc4j-events/v1", wantErr: "must match"},
		{name: "entries", wantErr: "reserved"},
		{name: "history_current", wantErr: "reserved"},
		{name: "history_all", wantErr: "reserved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileProjection(context.Background(), testProjectionManifest(test.name, "history/lc4j"), clickHouseProjectionCapabilities)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestProjectionSystemTableNamesAreReserved(t *testing.T) {
	for name := range projectionReservedTableNames {
		t.Run(name, func(t *testing.T) {
			_, err := compileProjection(context.Background(), testProjectionManifest(name, "history/lc4j"), clickHouseProjectionCapabilities)
			require.ErrorContains(t, err, "reserved")
		})
	}
}

func TestDifferentProjectionTablesMayUseSameSelector(t *testing.T) {
	first, err := compileProjection(context.Background(), testProjectionManifest("history_events_v1", "history/lc4j"), clickHouseProjectionCapabilities)
	require.NoError(t, err)
	second, err := compileProjection(context.Background(), testProjectionManifest("history_event_outcomes_v1", "history/lc4j"), clickHouseProjectionCapabilities)
	require.NoError(t, err)
	require.Equal(t, first.Selector, second.Selector)
	require.NotEqual(t, first.TableName, second.TableName)
}

func TestProjectionProviderSpecificTypes(t *testing.T) {
	variant := projectionColumn{Type: "variant", Variants: []string{"string", "int64", "bool"}}
	jsonColumn := projectionColumn{Type: "json", ClickHouse: &projectionClickHouseOptions{MaxDynamicPaths: 128, MaxDynamicTypes: 8}}
	require.NoError(t, validateProjectionColumn("result", variant, clickHouseProjectionCapabilities))
	require.NoError(t, validateProjectionColumn("content", jsonColumn, clickHouseProjectionCapabilities))
	require.Equal(t, "Variant(Bool, Int64, String)", projectionColumnSQLType(variant))
	require.Equal(t, "JSON(max_dynamic_paths = 128, max_dynamic_types = 8)", projectionColumnSQLType(jsonColumn))

	unsupported := projectionProviderCapabilities{Name: "portable"}
	require.ErrorContains(t, validateProjectionColumn("result", variant, unsupported), "not supported")
	require.ErrorContains(t, validateProjectionColumn("content", jsonColumn, unsupported), "not supported")

	require.ErrorContains(t, validateProjectionColumn("result", projectionColumn{Type: "variant", Variants: []string{"string"}}, clickHouseProjectionCapabilities), "at least two")
	require.ErrorContains(t, validateProjectionColumn("result", projectionColumn{Type: "variant", Variants: []string{"string", "json_string"}}, clickHouseProjectionCapabilities), "both map")
	require.ErrorContains(t, validateProjectionColumn("result", projectionColumn{Type: "variant", Variants: []string{"int64", "float64"}}, clickHouseProjectionCapabilities), "multiple numeric")
	require.ErrorContains(t, validateProjectionColumn("content", projectionColumn{Type: "json", ClickHouse: &projectionClickHouseOptions{MaxDynamicPaths: maxJSONDynamicPaths + 1}}, clickHouseProjectionCapabilities), "maxDynamicPaths")
}

func TestNormalizeProjectionVariantAndJSON(t *testing.T) {
	value, code := normalizeProjectionValue(projectionColumn{Type: "variant", Variants: []string{"string", "int64"}}, map[string]any{"type": "string", "value": "resolved"})
	require.Empty(t, code)
	variant, ok := value.(ch.Variant)
	require.True(t, ok)
	require.Equal(t, "String", variant.Type())
	require.Equal(t, "resolved", variant.Any())

	value, code = normalizeProjectionValue(projectionColumn{Type: "json"}, map[string]any{"context": map[string]any{"region": "us"}})
	require.Empty(t, code)
	require.JSONEq(t, `{"context":{"region":"us"}}`, value.(string))

	_, code = normalizeProjectionValue(projectionColumn{Type: "variant", Variants: []string{"string", "int64"}}, map[string]any{"type": "bool", "value": true})
	require.Equal(t, "invalid_variant", code)
	_, code = normalizeProjectionValue(projectionColumn{Type: "json"}, []any{"not", "an", "object"})
	require.Equal(t, "invalid_json", code)
}

func TestExporterOwnedSchemaUsesPortableTypes(t *testing.T) {
	ddl := strings.Join(schemaStatements("analytics"), "\n")
	require.NotContains(t, ddl, "Variant(")
	require.NotContains(t, ddl, " JSON(")
	require.NotContains(t, ddl, " Nullable(JSON")
}

func TestCanonicalViewsUseLogicalKeysWithoutBatchCommitMarkers(t *testing.T) {
	ddl := strings.Join(schemaStatements("analytics"), "\n")
	require.NotContains(t, ddl, "ingest_batches")
	require.Contains(t, ddl, "PARTITION BY e.exporter_id, e.event_id")
	require.Contains(t, ddl, "PARTITION BY r.exporter_id, r.resource_type, r.resource_id")

	projectionDDL := projectionAllViewStatement("analytics", "support_ticket_v1", ProjectionRowsOne)
	require.NotContains(t, projectionDDL, "ingest_batches")
	require.Contains(t, projectionDDL, "PARTITION BY r.exporter_id, r.resource_id")

	multiRowDDL := projectionAllViewStatement("analytics", "support_ticket_events_v1", ProjectionRowsMany)
	require.Contains(t, multiRowDDL, "dense_rank() OVER (PARTITION BY r.exporter_id, r.resource_id ORDER BY r.ingest_version DESC, r.event_id DESC) version_rank")
	require.Contains(t, multiRowDDL, "PARTITION BY r.exporter_id, r.resource_id, r.row_index")
	require.Contains(t, multiRowDDL, "r.row_index < r.row_count")
}

func TestProjectionRowsModeValidation(t *testing.T) {
	manifest := testProjectionManifest("support_ticket_v1", "support-ticket/v1")
	omitted, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)
	require.Equal(t, ProjectionRowsOne, omitted.RowMode)

	manifest.Spec.Rows = ProjectionRowsOne
	explicit, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)
	require.Equal(t, omitted.Digest, explicit.Digest, "rows: one must keep the digest of an omitted rows field")

	manifest.Spec.Rows = ProjectionRowsMany
	many, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)
	require.Equal(t, ProjectionRowsMany, many.RowMode)
	require.NotEqual(t, omitted.Digest, many.Digest)

	manifest.Spec.Rows = "some"
	_, err = compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.ErrorContains(t, err, "spec.rows must be one or many")

	memory := projectionManifest{
		APIVersion: "memory-service/v1alpha1",
		Kind:       "AnalyticsProjection",
		Metadata:   projectionMetadata{Name: "memory_attributes_v1"},
		Spec: projectionSpec{
			Resource: "memory",
			Selector: projectionSelector{Kind: "support/v1"},
			Rows:     ProjectionRowsMany,
			Columns:  map[string]projectionColumn{"outcome": {Type: "string"}},
		},
	}
	_, err = compileProjection(context.Background(), memory, clickHouseProjectionCapabilities)
	require.ErrorContains(t, err, "requires projectionRego")

	for _, column := range []string{"row_index", "row_count"} {
		reserved := testProjectionManifest("support_ticket_v1", "support-ticket/v1")
		reserved.Spec.Columns = map[string]projectionColumn{column: {Type: "uint64"}}
		_, err = compileProjection(context.Background(), reserved, clickHouseProjectionCapabilities)
		require.ErrorContains(t, err, "reserved")
	}
}

func TestMultiRowProjectionRows(t *testing.T) {
	projection := testMultiRowProjection(t, `output := [{"step": step} | some step in input.content]`)
	base := ProjectionRow{Common: Common{EventID: "event-1", IngestVersion: 7}, ResourceID: "entry-1", ConversationID: "conversation-1"}

	rows, code := projection.Rows(context.Background(), base, map[string]any{"content": []any{"plan", "act", "check"}})
	require.Empty(t, code)
	require.Len(t, rows, 3)
	for i, row := range rows {
		require.True(t, row.Multi)
		require.False(t, row.IsDeleted)
		require.Equal(t, uint32(i), row.RowIndex)
		require.Equal(t, uint32(3), row.RowCount)
		require.Equal(t, "entry-1", row.ResourceID)
		require.Equal(t, uint64(7), row.IngestVersion)
		require.Equal(t, "support_steps_v1", row.TableName)
		require.Equal(t, []string{"step"}, row.ColumnNames)
	}
	require.Equal(t, []any{"act"}, rows[1].Values)

	rows, code = projection.Rows(context.Background(), base, map[string]any{"content": []any{}})
	require.Empty(t, code)
	require.Len(t, rows, 1, "a version without rows writes one marker row")
	require.True(t, rows[0].Multi)
	require.False(t, rows[0].IsDeleted)
	require.Equal(t, uint32(0), rows[0].RowCount)
	require.Equal(t, []any{nil}, rows[0].Values)

	_, code = projection.Rows(context.Background(), base, map[string]any{"content": []any{"plan", float64(1)}})
	require.Equal(t, "type_mismatch", code, "one invalid row fails the whole resource")

	tooMany := make([]any, maxProjectionRows+1)
	for i := range tooMany {
		tooMany[i] = "step"
	}
	_, code = projection.Rows(context.Background(), base, map[string]any{"content": tooMany})
	require.Equal(t, "too_many_rows", code)

	tombstone := projection.TombstoneRow(base)
	require.True(t, tombstone.Multi)
	require.True(t, tombstone.IsDeleted)
	require.Equal(t, uint32(0), tombstone.RowCount)

	objectOutput := testMultiRowProjection(t, `output := {"step": "plan"}`)
	_, code = objectOutput.Rows(context.Background(), base, map[string]any{})
	require.Equal(t, "output_not_array", code)

	nonObjectItem := testMultiRowProjection(t, `output := ["plan"]`)
	_, code = nonObjectItem.Rows(context.Background(), base, map[string]any{})
	require.Equal(t, "output_not_object", code)
}

func testMultiRowProjection(t *testing.T, rule string) *Projection {
	t.Helper()
	manifest := testProjectionManifest("support_steps_v1", "support-steps/v1")
	manifest.Spec.Rows = ProjectionRowsMany
	manifest.Spec.Columns = map[string]projectionColumn{"step": {Type: "string", Nullable: true}}
	manifest.Spec.ProjectionRego = "package memoryservice.analytics\n" + rule
	projection, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)
	return projection
}

func testProjectionManifest(name, selector string) projectionManifest {
	return projectionManifest{
		APIVersion: "memory-service/v1alpha1",
		Kind:       "AnalyticsProjection",
		Metadata:   projectionMetadata{Name: name},
		Spec: projectionSpec{
			Resource: "entry",
			Selector: projectionSelector{ContentType: selector},
			Columns:  map[string]projectionColumn{"outcome": {Type: "string"}},
			ProjectionRego: fmt.Sprintf(`package memoryservice.analytics
output := {"outcome": %q}`, "resolved"),
		},
	}
}

func testProjectionYAML(name, selector string) string {
	return fmt.Sprintf(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: %s
spec:
  resource: entry
  selector:
    contentType: %s
  columns:
    outcome: {type: string}
  projectionRego: |
    package memoryservice.analytics
    output := {"outcome": "resolved"}
`, name, selector)
}
