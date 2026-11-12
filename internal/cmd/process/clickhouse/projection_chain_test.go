package clickhouse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testChainRoot = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_v1_steps}
spec:
  resource: entry
  table: support_ticket_steps
  selector: {contentType: support-ticket/v1}
  rows: many
  columns:
    step: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s} | some s in input.record.content]
`

const testChainReuse = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_v2_steps}
spec:
  resource: entry
  previous: support_ticket_v1_steps
  projectionRegoFrom: support_ticket_v1_steps
  selector: {contentType: support-ticket/v2}
`

const testChainReplace = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_steps_duration}
spec:
  resource: entry
  previous: support_ticket_v2_steps
  projectionRegoReplaces: support_ticket_v1_steps
  addColumns:
    duration_ms: {type: uint64, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s, "duration_ms": 1} | some s in input.record.content]
`

func loadTestProjectionDocuments(t *testing.T, documents ...string) (*ProjectionSet, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "projections.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(documents, "---\n")), 0o600))
	return LoadProjectionSet(context.Background(), []string{path})
}

func TestProjectionChainSharesOneTable(t *testing.T) {
	ctx := context.Background()
	// Document order does not matter; the chain is ordered by spec.previous.
	set, err := loadTestProjectionDocuments(t, testChainReplace, testChainRoot, testChainReuse)
	require.NoError(t, err)
	require.Len(t, set.Items, 3)
	require.Len(t, set.Tables, 1)
	table := set.Tables[0]
	require.Equal(t, "support_ticket_steps", table.Name)
	require.Equal(t, ProjectionRowsMany, table.RowMode)
	require.Equal(t, []string{"support_ticket_v1_steps", "support_ticket_v2_steps", "support_ticket_steps_duration"}, manifestNames(table.Manifests))
	require.Equal(t, []string{"step", "duration_ms"}, table.ColumnOrder)
	require.Equal(t, []string{"duration_ms", "step"}, table.ColumnNames)
	require.Equal(t, "support_ticket_steps_duration", table.ColumnOwners["duration_ms"])
	for _, item := range set.Items {
		require.Equal(t, "support_ticket_steps", item.TableName)
		require.Equal(t, ProjectionRowsMany, item.RowMode)
		require.Equal(t, table.ColumnNames, item.ColumnNames)
	}

	// One replacing manifest without a selector takes over every content type that
	// shared the replaced logic, including the one added with projectionRegoFrom.
	for _, contentType := range []string{"support-ticket/v1", "support-ticket/v2"} {
		matches := set.Match("entry", contentType)
		require.Len(t, matches, 1)
		require.Equal(t, "support_ticket_steps_duration", matches[0].Name)
		rows, code := matches[0].Rows(ctx, ProjectionRow{ResourceID: "entry-1"}, map[string]any{"record": map[string]any{"content": []any{"plan"}}})
		require.Empty(t, code)
		require.Len(t, rows, 1)
		require.Equal(t, "support_ticket_steps", rows[0].TableName)
		require.Equal(t, "support_ticket_steps_duration", rows[0].ProjectionName)
		require.Equal(t, []string{"duration_ms", "step"}, rows[0].ColumnNames)
		require.Equal(t, []any{uint64(1), "plan"}, rows[0].Values)
	}
	require.Empty(t, set.Match("entry", "support-ticket/v3"))
	require.Empty(t, set.Match("memory", "support-ticket/v1"))
}

func TestProjectionChainEarlierLogicWritesNullForAddedColumns(t *testing.T) {
	ctx := context.Background()
	addColumns := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_steps_duration_column}
spec:
  resource: entry
  previous: support_ticket_v2_steps
  addColumns:
    duration_ms: {type: uint64, nullable: true}
`
	set, err := loadTestProjectionDocuments(t, testChainRoot, testChainReuse, addColumns)
	require.NoError(t, err)
	for _, contentType := range []string{"support-ticket/v1", "support-ticket/v2"} {
		matches := set.Match("entry", contentType)
		require.Len(t, matches, 1)
		require.Equal(t, "support_ticket_v1_steps", matches[0].Name, "projectionRegoFrom shares the earlier manifest's logic")
		rows, code := matches[0].Rows(ctx, ProjectionRow{}, map[string]any{"record": map[string]any{"content": []any{"plan", "act"}}})
		require.Empty(t, code)
		require.Len(t, rows, 2)
		require.Equal(t, []any{nil, "act"}, rows[1].Values)
	}
}

func TestProjectionRegoFromResolvesToTheDefiningManifest(t *testing.T) {
	third := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_v3_steps}
spec:
  resource: entry
  previous: support_ticket_v2_steps
  projectionRegoFrom: support_ticket_v2_steps
  selector: {contentType: support-ticket/v3}
`
	set, err := loadTestProjectionDocuments(t, testChainRoot, testChainReuse, third)
	require.NoError(t, err)
	matches := set.Match("entry", "support-ticket/v3")
	require.Len(t, matches, 1)
	require.Equal(t, "support_ticket_v1_steps", matches[0].Name)
}

func TestProjectionRegoReplacesWithSelectorSplitsOneContentType(t *testing.T) {
	split := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_v2_steps_split}
spec:
  resource: entry
  previous: support_ticket_v2_steps
  projectionRegoReplaces: support_ticket_v2_steps
  selector: {contentType: support-ticket/v2}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": "split"}]
`
	set, err := loadTestProjectionDocuments(t, testChainRoot, testChainReuse, split)
	require.NoError(t, err)
	require.Equal(t, "support_ticket_v1_steps", set.Match("entry", "support-ticket/v1")[0].Name)
	require.Equal(t, "support_ticket_v2_steps_split", set.Match("entry", "support-ticket/v2")[0].Name)
}

func TestProjectionChainValidation(t *testing.T) {
	later := func(name, body string) string {
		return "apiVersion: memory-service/v1alpha1\nkind: AnalyticsProjection\nmetadata: {name: " + name + "}\nspec:\n  resource: entry\n" + body
	}
	rego := "  projectionRego: |\n    package memoryservice.analytics\n    output := [{\"step\": \"x\"}]\n"
	for _, test := range []struct {
		name      string
		documents []string
		wantErr   string
	}{
		{
			name:      "later declares columns",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: support_ticket_v1_steps\n  columns:\n    step: {type: string}\n")},
			wantErr:   "must not declare spec.columns, spec.rows, or spec.table",
		},
		{
			name:      "later declares rows",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: support_ticket_v1_steps\n  rows: many\n")},
			wantErr:   "must not declare spec.columns, spec.rows, or spec.table",
		},
		{
			name:      "later declares table",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: support_ticket_v1_steps\n  table: support_ticket_steps\n")},
			wantErr:   "must not declare spec.columns, spec.rows, or spec.table",
		},
		{
			name:      "missing previous manifest",
			documents: []string{testChainReuse},
			wantErr:   `names previous manifest "support_ticket_v1_steps", which is not configured`,
		},
		{
			name:      "branch",
			documents: []string{testChainRoot, testChainReuse, later("support_ticket_v2b_steps", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: support_ticket_v1_steps\n")},
			wantErr:   `projection chain branches at "support_ticket_v1_steps"`,
		},
		{
			name: "cycle",
			documents: []string{
				later("cycle_a", "  previous: cycle_b\n  addColumns:\n    a: {type: string, nullable: true}\n"),
				later("cycle_b", "  previous: cycle_a\n  addColumns:\n    b: {type: string, nullable: true}\n"),
			},
			wantErr: "form a cycle",
		},
		{
			name:      "overlapping selector",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: support-ticket/v1}\n"+rego)},
			wantErr:   "use spec.projectionRegoReplaces to replace it",
		},
		{
			name:      "rego and rego from",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: support_ticket_v1_steps\n"+rego)},
			wantErr:   "mutually exclusive",
		},
		{
			name:      "rego from another chain",
			documents: []string{testChainRoot, testProjectionYAML("other_v1", "other/v1"), later("later_v1", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: other_v1\n")},
			wantErr:   `spec.projectionRegoFrom "other_v1" must name an earlier manifest in the same chain`,
		},
		{
			name:      "rego from a later manifest",
			documents: []string{testChainRoot, later("later_a", "  previous: support_ticket_v1_steps\n  selector: {contentType: other}\n  projectionRegoFrom: later_b\n"), later("later_b", "  previous: later_a\n  selector: {contentType: third}\n"+rego)},
			wantErr:   `spec.projectionRegoFrom "later_b" must name an earlier manifest in the same chain`,
		},
		{
			name:      "rego from a manifest without logic",
			documents: []string{testChainRoot, later("later_a", "  previous: support_ticket_v1_steps\n  addColumns:\n    note: {type: string, nullable: true}\n"), later("later_b", "  previous: later_a\n  selector: {contentType: other}\n  projectionRegoFrom: later_a\n")},
			wantErr:   "names a manifest without projection logic",
		},
		{
			name:      "replace without rego",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  projectionRegoReplaces: support_ticket_v1_steps\n  addColumns:\n    note: {type: string, nullable: true}\n")},
			wantErr:   "spec.projectionRegoReplaces requires a complete projectionRego",
		},
		{
			name:      "replaced selector not covered",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  projectionRegoReplaces: support_ticket_v1_steps\n  selector: {contentType: other}\n"+rego)},
			wantErr:   `"other" is not projected by that logic`,
		},
		{
			name:      "replaced logic already replaced",
			documents: []string{testChainRoot, later("later_a", "  previous: support_ticket_v1_steps\n  projectionRegoReplaces: support_ticket_v1_steps\n"+rego), later("later_b", "  previous: later_a\n  projectionRegoReplaces: support_ticket_v1_steps\n"+rego)},
			wantErr:   "no longer projects any content type",
		},
		{
			name:      "required added column",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  addColumns:\n    note: {type: string, nullable: false}\n")},
			wantErr:   `added column "note" must be nullable`,
		},
		{
			name:      "existing added column",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n  addColumns:\n    step: {type: string, nullable: true}\n")},
			wantErr:   `adds column "step", which "support_ticket_v1_steps" already declares`,
		},
		{
			name:      "resource mismatch",
			documents: []string{testChainRoot, "apiVersion: memory-service/v1alpha1\nkind: AnalyticsProjection\nmetadata: {name: later_v1}\nspec:\n  resource: memory\n  previous: support_ticket_v1_steps\n  addColumns:\n    note: {type: string, nullable: true}\n"},
			wantErr:   `but its chain projects "entry"`,
		},
		{
			name:      "no change",
			documents: []string{testChainRoot, later("later_v1", "  previous: support_ticket_v1_steps\n")},
			wantErr:   "must add a selector, replace logic, or add columns",
		},
		{
			name:      "first manifest replaces logic",
			documents: []string{strings.Replace(testChainRoot, "  table: support_ticket_steps\n", "  table: support_ticket_steps\n  projectionRegoReplaces: other\n", 1)},
			wantErr:   "require spec.previous",
		},
		{
			name:      "two chains share a table",
			documents: []string{testChainRoot, strings.Replace(testChainRoot, "support_ticket_v1_steps", "support_ticket_other_steps", 1)},
			wantErr:   `both write to table "support_ticket_steps"`,
		},
		{
			name:      "reserved table",
			documents: []string{strings.Replace(testChainRoot, "table: support_ticket_steps", "table: conversation_memberships", 1)},
			wantErr:   `spec.table "conversation_memberships" is reserved`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadTestProjectionDocuments(t, test.documents...)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestProjectionOutputIsCheckedAgainstTableColumns(t *testing.T) {
	manifest := testProjectionManifest("output_check_v1", "output-check/v1")
	manifest.Spec.Columns = map[string]projectionColumn{
		"outcome": {Type: "string"},
		"note":    {Type: "string", Nullable: true},
		"tags":    {Type: "string_array", Nullable: true},
	}
	projection, err := compileProjection(context.Background(), manifest, clickHouseProjectionCapabilities)
	require.NoError(t, err)

	values, code := projection.columnValues(map[string]any{"outcome": "resolved"})
	require.Empty(t, code)
	require.Equal(t, []any{nil, "resolved", []string(nil)}, values, "missing nullable columns are written as NULL")

	_, code = projection.columnValues(map[string]any{"note": "only optional"})
	require.Equal(t, "column_set_mismatch", code, "missing non-nullable columns fail")

	_, code = projection.columnValues(map[string]any{"outcome": "resolved", "unexpected": "value"})
	require.Equal(t, "column_set_mismatch", code, "unknown keys fail")
}

func manifestNames(manifests []*Projection) []string {
	names := make([]string, len(manifests))
	for i, manifest := range manifests {
		names[i] = manifest.Name
	}
	return names
}

func TestProjectionsCommandShowsChainsAndCombinedColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projections.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{testChainRoot, testChainReuse, testChainReplace}, "---\n")), 0o600))
	var out strings.Builder
	command := ProjectionsCommand()
	command.Writer = &out
	require.NoError(t, command.Run(context.Background(), []string{"clickhouse-projections", "--projection-path", path}))
	for _, fragment := range []string{"table support_ticket_steps (resource entry, rows many)", "ordering: resource", "Nullable(String)", "Nullable(UInt64)", "support-ticket/v2  support_ticket_steps_duration", "proposed DDL:", "ReplacingMergeTree(ingest_version)"} {
		require.Contains(t, out.String(), fragment)
	}

	out.Reset()
	require.NoError(t, command.Run(context.Background(), []string{"clickhouse-projections", "--projection-path", path, "--format", "json"}))
	var tables []projectionTableDescription
	require.NoError(t, json.Unmarshal([]byte(out.String()), &tables))
	require.Len(t, tables, 1)
	require.Equal(t, "support_ticket_steps", tables[0].Table)
	require.Equal(t, []string{"support_ticket_v1_steps", "support_ticket_v2_steps", "support_ticket_steps_duration"}, []string{tables[0].Manifests[0].Name, tables[0].Manifests[1].Name, tables[0].Manifests[2].Name})
	require.Equal(t, projectionColumnDescription{Name: "duration_ms", Type: "uint64", ClickHouseType: "Nullable(UInt64)", Nullable: true, DeclaredBy: "support_ticket_steps_duration"}, tables[0].Columns[1])
	require.Equal(t, map[string]string{"support-ticket/v1": "support_ticket_steps_duration", "support-ticket/v2": "support_ticket_steps_duration"}, tables[0].Logic)
}

const testMemoryChain = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: notes_v1}
spec:
  resource: memory
  table: notes
  selector: {kind: notes/v1}
  columns:
    subject: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := input.record.attributes
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: notes_v2}
spec:
  resource: memory
  previous: notes_v1
  projectionRegoFrom: notes_v1
  selector: {kind: notes/v2}
  addColumns:
    topic: {type: string, nullable: true}
`

const testMemoryChainSplit = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: notes_v1_upper}
spec:
  resource: memory
  previous: notes_v2
  projectionRegoReplaces: notes_v1
  selector: {kind: notes/v1}
  projectionRego: |
    package memoryservice.analytics
    output := {"subject": upper(input.record.attributes.subject)}
`

// Memory chains use explicit Rego for attribute extraction and reuse.
func TestMemoryProjectionChainReusesExplicitRego(t *testing.T) {
	ctx := context.Background()
	set, err := loadTestProjectionDocuments(t, testMemoryChain, testMemoryChainSplit)
	require.NoError(t, err)
	require.Len(t, set.Tables, 1)
	require.Equal(t, "notes", set.Tables[0].Name)
	require.Equal(t, []string{"subject", "topic"}, set.Tables[0].ColumnOrder)

	shared := set.Match("memory", "notes/v2")
	require.Len(t, shared, 1)
	require.Equal(t, "notes_v1", shared[0].Name)
	rows, code := shared[0].Rows(ctx, ProjectionRow{}, map[string]any{"record": map[string]any{"attributes": map[string]any{"subject": "billing", "topic": "invoices"}}})
	require.Empty(t, code)
	require.Equal(t, []any{"billing", "invoices"}, rows[0].Values)
	rows, code = shared[0].Rows(ctx, ProjectionRow{}, map[string]any{"record": map[string]any{"attributes": map[string]any{"subject": "billing"}}})
	require.Empty(t, code)
	require.Equal(t, []any{"billing", nil}, rows[0].Values, "attributes without an added column write NULL")
	_, code = shared[0].Rows(ctx, ProjectionRow{}, map[string]any{"record": map[string]any{"attributes": map[string]any{"subject": "billing", "unknown": "x"}}})
	require.Equal(t, "column_set_mismatch", code)

	split := set.Match("memory", "notes/v1")
	require.Len(t, split, 1)
	require.Equal(t, "notes_v1_upper", split[0].Name)
	rows, code = split[0].Rows(ctx, ProjectionRow{}, map[string]any{"record": map[string]any{"attributes": map[string]any{"subject": "billing"}}})
	require.Empty(t, code)
	require.Equal(t, "notes", rows[0].TableName)
	require.Equal(t, []any{"BILLING", nil}, rows[0].Values)
	require.Empty(t, set.Match("entry", "notes/v1"), "selectors are scoped to the chain's resource")
}
