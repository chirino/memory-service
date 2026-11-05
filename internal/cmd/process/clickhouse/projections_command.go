package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

// ProjectionsCommand shows each projection table's manifest chain, combined columns, and
// logic routing. A chain's definition is spread across several manifests, so this reads
// the configured manifests without connecting to ClickHouse.
func ProjectionsCommand() *cli.Command {
	return &cli.Command{
		Name:  "clickhouse-projections",
		Usage: "Show each ClickHouse analytics projection table's manifest chain and combined columns",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "projection-path", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_PATH"), Required: true, Usage: "projection manifest file or directory"},
			&cli.StringFlag{Name: "format", Value: "text", Usage: "output format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			format := cmd.String("format")
			if format != "text" && format != "json" {
				return fmt.Errorf("--format must be text or json")
			}
			set, err := LoadProjectionSet(ctx, cmd.StringSlice("projection-path"))
			if err != nil {
				return err
			}
			if format == "json" {
				return writeProjectionTablesJSON(cmd.Root().Writer, set)
			}
			return writeProjectionTablesText(cmd.Root().Writer, set)
		},
	}
}

type projectionTableDescription struct {
	Table     string                          `json:"table"`
	Resource  string                          `json:"resource"`
	Rows      string                          `json:"rows"`
	Manifests []projectionManifestDescription `json:"manifests"`
	Columns   []projectionColumnDescription   `json:"columns"`
	Logic     map[string]string               `json:"logic"`
}

type projectionManifestDescription struct {
	Name                   string   `json:"name"`
	Digest                 string   `json:"digest"`
	Previous               string   `json:"previous,omitempty"`
	Selector               string   `json:"selector,omitempty"`
	ProjectionRegoReplaces string   `json:"projectionRegoReplaces,omitempty"`
	ProjectionRegoFrom     string   `json:"projectionRegoFrom,omitempty"`
	AddColumns             []string `json:"addColumns,omitempty"`
}

type projectionColumnDescription struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	ClickHouseType string `json:"clickhouseType"`
	Nullable       bool   `json:"nullable"`
	DeclaredBy     string `json:"declaredBy"`
}

func describeProjectionTables(set *ProjectionSet) []projectionTableDescription {
	result := make([]projectionTableDescription, 0, len(set.Tables))
	for _, table := range set.Tables {
		description := projectionTableDescription{Table: table.Name, Resource: table.Resource, Rows: table.RowMode, Logic: map[string]string{}}
		for _, manifest := range table.Manifests {
			added := make([]string, 0, len(manifest.AddColumns))
			for name := range manifest.AddColumns {
				added = append(added, name)
			}
			sort.Strings(added)
			description.Manifests = append(description.Manifests, projectionManifestDescription{
				Name: manifest.Name, Digest: manifest.Digest, Previous: manifest.Previous, Selector: manifest.Selector,
				ProjectionRegoReplaces: manifest.ProjectionRegoReplaces, ProjectionRegoFrom: manifest.ProjectionRegoFrom, AddColumns: added,
			})
		}
		for _, name := range table.ColumnOrder {
			column := table.Columns[name]
			description.Columns = append(description.Columns, projectionColumnDescription{
				Name: name, Type: column.Type, ClickHouseType: projectionColumnSQLType(column), Nullable: column.Nullable, DeclaredBy: table.ColumnOwners[name],
			})
		}
		for selector, logic := range table.Logic {
			description.Logic[selector] = logic.Name
		}
		result = append(result, description)
	}
	return result
}

func writeProjectionTablesJSON(out io.Writer, set *ProjectionSet) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(describeProjectionTables(set))
}

func writeProjectionTablesText(out io.Writer, set *ProjectionSet) error {
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for i, table := range describeProjectionTables(set) {
		if i > 0 {
			fmt.Fprintln(writer)
		}
		fmt.Fprintf(writer, "table %s (resource %s, rows %s)\n", table.Table, table.Resource, table.Rows)
		fmt.Fprintln(writer, "  manifests:")
		for position, manifest := range table.Manifests {
			var actions []string
			if manifest.Previous != "" {
				actions = append(actions, "previous "+manifest.Previous)
			}
			if manifest.Selector != "" {
				actions = append(actions, "selects "+manifest.Selector)
			}
			if manifest.ProjectionRegoFrom != "" {
				actions = append(actions, "reuses logic of "+manifest.ProjectionRegoFrom)
			}
			if manifest.ProjectionRegoReplaces != "" {
				actions = append(actions, "replaces logic of "+manifest.ProjectionRegoReplaces)
			}
			if len(manifest.AddColumns) > 0 {
				actions = append(actions, "adds "+strings.Join(manifest.AddColumns, ", "))
			}
			fmt.Fprintf(writer, "    %d.\t%s\t%s\n", position+1, manifest.Name, strings.Join(actions, "; "))
		}
		fmt.Fprintln(writer, "  columns:")
		for _, column := range table.Columns {
			nullability := "not null"
			if column.Nullable {
				nullability = "nullable"
			}
			fmt.Fprintf(writer, "    %s\t%s\t%s\tdeclared by %s\n", column.Name, column.Type, nullability, column.DeclaredBy)
		}
		fmt.Fprintln(writer, "  logic:")
		selectors := make([]string, 0, len(table.Logic))
		for selector := range table.Logic {
			selectors = append(selectors, selector)
		}
		sort.Strings(selectors)
		for _, selector := range selectors {
			fmt.Fprintf(writer, "    %s\t%s\n", selector, table.Logic[selector])
		}
	}
	return writer.Flush()
}
