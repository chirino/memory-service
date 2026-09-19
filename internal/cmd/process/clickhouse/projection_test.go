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
	for _, projection := range set.Items {
		selectors[projection.Resource+":"+projection.Selector] = projection.Name
	}
	require.Equal(t, map[string]string{
		"entry:history":              "history_v1",
		"entry:history/lc4j":         "history_lc4j_v1",
		"entry:history/vercelai":     "history_vercelai_v1",
		"entry:LC4J":                 "lc4j_v1",
		"entry:SpringAI":             "spring_ai_v1",
		"entry:LangGraph/checkpoint": "langgraph_checkpoint_v1",
		"entry:vercelai":             "vercelai_v1",
		"memory:default/v1":          "default_memory_v1",
		"memory:cognition/v1":        "cognition_memory_v1",
	}, selectors)
	selected, err := LoadProjectionSet(context.Background(), []string{filepath.Join(projectionPath, "history-lc4j.yaml")})
	require.NoError(t, err)
	require.Len(t, selected.Items, 1)
	require.Equal(t, "history_lc4j_v1", selected.Items[0].Name)

	entryInput := map[string]any{
		"content": []any{
			map[string]any{
				"role":                 "AI",
				"text":                 "Which orders are delayed?",
				"events":               []any{map[string]any{"eventType": "BeforeToolExecution", "toolName": "orderStatus"}, map[string]any{"eventType": "ToolExecuted", "toolName": "orderStatus"}},
				"attachments":          []any{map[string]any{"contentType": "application/pdf", "attachmentId": "attachment-1"}},
				"checkpoint_id":        "checkpoint-1",
				"checkpoint_ns":        "customer-support",
				"parent_checkpoint_id": nil,
				"checkpoint":           map[string]any{"channel_values": map[string]any{"status": "open"}},
				"metadata":             map[string]any{"step": float64(1)},
			},
		},
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
	springAIInput := map[string]any{
		"content": []any{
			map[string]any{"role": "system", "text": "Answer from the supplied research."},
			map[string]any{"role": "user", "text": "Summarize the latest research notes."},
			map[string]any{"role": "assistant", "text": "Three sources agree on the main result."},
			map[string]any{"role": "tool", "text": "Three matching sources."},
		},
	}
	for _, projection := range set.Items {
		t.Run("evaluates_"+projection.Name, func(t *testing.T) {
			input := entryInput
			if projection.Resource == "memory" {
				input = memoryInput
			} else if projection.Name == "spring_ai_v1" {
				input = springAIInput
			}
			values, code := projection.evaluate(context.Background(), input)
			require.Empty(t, code)
			require.Len(t, values, len(projection.ColumnNames))
			if projection.Name == "history_v1" || projection.Name == "history_lc4j_v1" || projection.Name == "history_vercelai_v1" {
				byName := make(map[string]any, len(values))
				for index, name := range projection.ColumnNames {
					byName[name] = values[index]
				}
				require.Equal(t, "AI", byName["role"])
				require.Equal(t, "Which orders are delayed?", byName["text"])
				require.Equal(t, uint64(2), byName["event_count"])
				require.Equal(t, []string{"BeforeToolExecution", "ToolExecuted"}, byName["event_types"])
				require.Equal(t, []string{"orderStatus", "orderStatus"}, byName["tool_names"])
				require.Equal(t, uint64(1), byName["attachment_count"])
				require.Equal(t, []string{"application/pdf"}, byName["attachment_content_types"])
			}
			if projection.Name == "history_lc4j_v1" || projection.Name == "history_vercelai_v1" {
				byName := make(map[string]any, len(values))
				for index, name := range projection.ColumnNames {
					byName[name] = values[index]
				}
				require.Equal(t, uint64(1), byName["tool_call_count"])
				require.Equal(t, uint64(1), byName["tool_result_count"])
				require.Equal(t, uint64(0), byName["response_event_count"])
			}
			if projection.Name == "spring_ai_v1" {
				byName := make(map[string]any, len(values))
				for index, name := range projection.ColumnNames {
					byName[name] = values[index]
				}
				require.Equal(t, uint64(4), byName["message_count"])
				require.Equal(t, []string{"system", "user", "assistant", "tool"}, byName["roles"])
				require.Equal(t, []string{"Answer from the supplied research.", "Summarize the latest research notes.", "Three sources agree on the main result.", "Three matching sources."}, byName["texts"])
				require.Equal(t, uint64(1), byName["user_message_count"])
				require.Equal(t, uint64(1), byName["assistant_message_count"])
				require.Equal(t, uint64(1), byName["system_message_count"])
				require.Equal(t, uint64(1), byName["tool_message_count"])
			}
		})
	}
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

	projectionDDL := projectionAllViewStatement("analytics", "support_ticket_v1")
	require.NotContains(t, projectionDDL, "ingest_batches")
	require.Contains(t, projectionDDL, "PARTITION BY r.exporter_id, r.resource_id")
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
