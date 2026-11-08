package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultConversationProjectionLifecycleOnly(t *testing.T) {
	ctx := context.Background()
	set, err := LoadProjectionSet(ctx, nil)
	require.NoError(t, err)
	projections := set.Match("conversation", "")
	require.Len(t, projections, 1)
	projection := projections[0]
	wantColumns := []string{"archived", "created_at", "updated_at"}
	t.Run("declared_columns", func(t *testing.T) {
		require.Equal(t, wantColumns, projection.ColumnNames)
	})
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	for _, withDetails := range []bool{false, true} {
		t.Run(fmt.Sprintf("reporting_fields_present_%t", withDetails), func(t *testing.T) {
			record := map[string]any{
				"createdAt": created.Format(time.RFC3339),
				"updatedAt": updated.Format(time.RFC3339),
				"archived":  true,
			}
			if withDetails {
				for _, key := range []string{"ownerUserId", "clientId", "agentId", "forkedAtConversationId", "forkedAtEntryId", "startedByConversationId", "startedByEntryId"} {
					record[key] = "not-exported"
				}
			}
			rows, code := projection.Rows(ctx, ProjectionRow{ResourceID: "conversation-1"}, map[string]any{"resource": "conversation", "record": record})
			require.Empty(t, code)
			require.Len(t, rows, 1)
			require.Equal(t, wantColumns, rows[0].ColumnNames)
			require.Equal(t, []any{uint8(1), created, updated}, rows[0].Values)
		})
	}
}

func TestOptionalConversationDetailsProjection(t *testing.T) {
	ctx := context.Background()
	set, err := LoadProjectionSet(ctx, []string{"defaults", filepath.Join("..", "..", "..", "..", "deploy", "clickhouse", "projections", "conversation-details.yaml")})
	require.NoError(t, err)
	require.Len(t, set.Tables, 4)
	projections := set.Match("conversation", "")
	require.Len(t, projections, 2)
	projection := projections[0]
	require.Equal(t, "conversation_details", projection.Name)
	require.Equal(t, "conversation_details", projection.TableName)
	wantDetails := map[string]any{
		"owner_user_id": "owner", "client_id": "client", "agent_id": "agent",
		"forked_at_conversation_id": "fork-parent", "forked_at_entry_id": "fork-entry",
		"started_by_conversation_id": "starter", "started_by_entry_id": "start-entry",
	}
	event := conversationEvent("1")
	var data map[string]any
	require.NoError(t, json.Unmarshal(event.Data, &data))
	for field, value := range map[string]any{
		"ownerUserId": "owner", "clientId": "client", "agentId": "agent",
		"forkedAtConversationId": "fork-parent", "forkedAtEntryId": "fork-entry",
		"startedByConversationId": "starter", "startedByEntryId": "start-entry",
	} {
		data[field] = value
	}
	event.Data, err = json.Marshal(data)
	require.NoError(t, err)
	cfg := testConfig()
	cfg.MaxRecordBytes = len(event.Data)
	observation, err := decodeObservation(cfg, event)
	require.NoError(t, err)
	rows, code := projection.Rows(ctx, ProjectionRow{ResourceID: observation.ID}, map[string]any{"resource": observation.Kind, "record": observation.Record})
	require.Empty(t, code)
	require.Len(t, rows, 1)
	values := map[string]any{}
	for i, column := range rows[0].ColumnNames {
		values[column] = rows[0].Values[i]
	}
	for column, expected := range wantDetails {
		require.Equal(t, expected, values[column], column)
	}
	require.Len(t, values, 10)
	require.Equal(t, uint8(0), values["archived"])
	for _, column := range []string{"created_at", "updated_at"} {
		require.IsType(t, time.Time{}, values[column], column)
	}
}

func TestCurrentProjectionDefaultsAndClosedInput(t *testing.T) {
	set, err := LoadProjectionSet(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, set.Tables, 3)
	for _, kind := range []string{"conversation", "entry", "memory"} {
		require.Len(t, set.Match(kind, "any-type"), 1)
	}
	manifest := testProjectionManifest("closed_input", "type")
	for _, expression := range []string{"input.content", "input.attributes", "input[dynamic]"} {
		manifest.Spec.ProjectionRego = "package memoryservice.analytics\noutput := {\"outcome\": " + expression + "}"
		_, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
		require.Error(t, err)
	}
	manifest.Spec.ProjectionRego = "package memoryservice.analytics\noutput := {\"outcome\": input.record.ownerUserId}"
	manifest.Spec.Resource = "conversation"
	manifest.Spec.Selector = projectionSelector{}
	p, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)
	rows, code := p.Rows(context.Background(), ProjectionRow{}, map[string]any{"resource": "conversation", "record": map[string]any{"ownerUserId": "owner"}})
	require.Empty(t, code)
	require.Equal(t, []any{"owner"}, rows[0].Values)
	explicit, err := loadTestProjectionDocuments(t, testProjectionYAML("custom", "type"))
	require.NoError(t, err)
	require.Len(t, explicit.Tables, 1)
	require.Empty(t, explicit.Match("conversation", ""))
}
func TestProjectionLayoutsEncodingAndUnconditionalChains(t *testing.T) {
	root := strings.ReplaceAll(testChainRoot, "  selector: {contentType: support-ticket/v1}\n", "")
	root = strings.Replace(root, "  rows: many", "  clickhouse: {ordering: conversation}\n  rows: many", 1)
	root = strings.Replace(root, "step: {type: string, nullable: false}", "step: {type: string, nullable: false, clickhouse: {lowCardinality: true}}", 1)
	set, err := loadTestProjectionDocuments(t, root)
	require.NoError(t, err)
	table := set.Tables[0]
	require.Equal(t, "conversation", table.Ordering)
	require.Equal(t, "exporter_id, conversation_id, resource_id, row_index", projectionSortingKey(table.RowMode, table.Ordering))
	require.Contains(t, projectionCreateStatement("analytics", table, false), "LowCardinality(Nullable(String))")
	require.Contains(t, projectionAllViewStatement("analytics", table.Name, table.RowMode, table.Ordering), "PARTITION BY r.exporter_id,r.conversation_id,r.resource_id")
	for _, suffix := range []string{testChainReuse, strings.Replace(testChainReplace, "previous: support_ticket_v2_steps", "previous: support_ticket_v1_steps", 1)} {
		_, err := loadTestProjectionDocuments(t, root, suffix)
		if suffix == testChainReuse {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	for _, bad := range []string{strings.Replace(root, "ordering: conversation", "ordering: arbitrary", 1), strings.Replace(root, "resource: entry", "resource: memory", 1), strings.Replace(root, "type: string", "type: uint64", 1), strings.Replace(root, "ordering: conversation", "ordering: conversation, codecs: zstd", 1)} {
		_, err := loadTestProjectionDocuments(t, bad)
		require.Error(t, err)
	}
}
func TestExactProjectionIntegers(t *testing.T) {
	for _, tc := range []struct {
		value  any
		signed bool
		want   any
		ok     bool
	}{
		{json.Number("9007199254740993"), true, int64(9007199254740993), true},
		{json.Number("9223372036854775807"), true, int64(math.MaxInt64), true},
		{json.Number("18446744073709551615"), false, uint64(math.MaxUint64), true},
		{json.Number("-9223372036854775808"), true, int64(math.MinInt64), true},
		{json.Number("1e3"), false, uint64(1000), true},
		{json.Number("9223372036854775808"), true, nil, false},
		{json.Number("18446744073709551616"), false, nil, false},
		{json.Number("0.1"), false, nil, false},
		{float64(math.MaxInt64), true, nil, false},
		{math.Inf(1), false, nil, false},
		{json.Number("1e1000000000"), false, nil, false},
	} {
		value, ok := integralNumber(tc.value, tc.signed)
		require.Equal(t, tc.ok, ok)
		require.Equal(t, tc.want, value)
	}
}
