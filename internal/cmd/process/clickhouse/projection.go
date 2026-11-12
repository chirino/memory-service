package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"gopkg.in/yaml.v3"
)

const (
	// ProjectionRowsOne writes one row per matching resource.
	ProjectionRowsOne = "one"
	// ProjectionRowsMany writes zero or more rows per matching resource.
	ProjectionRowsMany = "many"
)

const (
	maxProjectionStringBytes = 64 << 10
	maxProjectionArrayItems  = 1024
	maxProjectionJSONBytes   = 1 << 20
	maxProjectionRows        = 1024
	defaultJSONDynamicPaths  = 256
	defaultJSONDynamicTypes  = 16
	maxJSONDynamicPaths      = 1024
	maxJSONDynamicTypes      = 32
)

var (
	projectionNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	projectionColumnPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	portableProjectionTypes = map[string]string{
		"string":       "String",
		"bool":         "UInt8",
		"int64":        "Int64",
		"uint64":       "UInt64",
		"float64":      "Float64",
		"timestamp":    "DateTime64(9, 'UTC')",
		"string_array": "Array(String)",
		"json_string":  "String",
	}
	projectionReservedColumns = map[string]struct{}{
		"exporter_id": {}, "batch_id": {}, "event_id": {}, "resource_id": {}, "ingest_version": {},
		"conversation_id": {}, "conversation_group_id": {}, "observed_at": {}, "is_deleted": {},
		"row_index": {}, "row_count": {},
	}
	projectionReservedTableNames = map[string]struct{}{
		"schema_versions":          {},
		"lifecycle_events":         {},
		"deletion_fences":          {},
		"projection_registry":      {},
		"retention_policy":         {},
		"projection_failures":      {},
		"purge_queue":              {},
		"conversation_memberships": {},
	}
)

type projectionProviderCapabilities struct {
	Name    string
	Variant bool
	JSON    bool
}

var clickHouseProjectionCapabilities = projectionProviderCapabilities{Name: "clickhouse", Variant: true, JSON: true}

type projectionManifest struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   projectionMetadata `yaml:"metadata"`
	Spec       projectionSpec     `yaml:"spec"`
}

type projectionMetadata struct {
	Name string `yaml:"name"`
}

//go:embed defaults/*.yaml
var defaultProjectionFiles embed.FS

type projectionTableOptions struct {
	Ordering string `yaml:"ordering"`
}

type projectionSpec struct {
	ClickHouse             *projectionTableOptions     `yaml:"clickhouse,omitempty"`
	Resource               string                      `yaml:"resource"`
	Selector               projectionSelector          `yaml:"selector"`
	Rows                   string                      `json:",omitempty"           yaml:"rows,omitempty"`
	Columns                map[string]projectionColumn `yaml:"columns"`
	ProjectionRego         string                      `yaml:"projectionRego"`
	Table                  string                      `json:",omitempty"           yaml:"table,omitempty"`
	Previous               string                      `json:",omitempty"           yaml:"previous,omitempty"`
	ProjectionRegoReplaces string                      `json:",omitempty"           yaml:"projectionRegoReplaces,omitempty"`
	ProjectionRegoFrom     string                      `json:",omitempty"           yaml:"projectionRegoFrom,omitempty"`
	AddColumns             map[string]projectionColumn `json:",omitempty"           yaml:"addColumns,omitempty"`
}

type projectionSelector struct {
	ContentType string `yaml:"contentType"`
	Kind        string `yaml:"kind"`
}

type projectionColumn struct {
	Type       string                       `yaml:"type"`
	Nullable   bool                         `yaml:"nullable"`
	Variants   []string                     `yaml:"variants,omitempty"`
	ClickHouse *projectionClickHouseOptions `yaml:"clickhouse,omitempty"`
}

type projectionClickHouseOptions struct {
	LowCardinality  bool `yaml:"lowCardinality,omitempty"`
	MaxDynamicPaths int  `yaml:"maxDynamicPaths,omitempty"`
	MaxDynamicTypes int  `yaml:"maxDynamicTypes,omitempty"`
}

// Projection is one loaded AnalyticsProjection manifest. Manifests form chains: the first
// manifest of a chain defines the table, and each later manifest names the manifest before it.
// After loading, TableName, RowMode, ColumnNames, and Columns describe the chain's shared
// table, so every manifest in a chain writes the same column set.
type Projection struct {
	Name     string
	Digest   string
	Resource string
	Ordering string
	// Selector is the content type or memory kind the manifest adds to its chain. It is
	// empty for later manifests that only replace logic or add columns.
	Selector    string
	TableName   string
	RowMode     string
	ColumnNames []string
	Columns     map[string]projectionColumn
	// Previous names the manifest before this one; it is empty for the first manifest of a chain.
	Previous               string
	ProjectionRegoReplaces string
	ProjectionRegoFrom     string
	AddColumns             map[string]projectionColumn
	prepared               *rego.PreparedEvalQuery
	// logic is the manifest whose projection logic this manifest uses: itself when it
	// defines projectionRego (or attribute mapping), the resolved projectionRegoFrom target,
	// or nil when it only adds columns.
	logic *Projection
}

// ProjectionTable is one projection chain and the table its manifests write to.
type ProjectionTable struct {
	Name     string
	Resource string
	RowMode  string
	Ordering string
	// Manifests lists the chain in order, starting with the manifest that defines the table.
	Manifests []*Projection
	// ColumnNames is sorted; Columns holds the first manifest's columns plus every addColumns.
	ColumnNames []string
	Columns     map[string]projectionColumn
	// ColumnOrder is the physical column order: the first manifest's columns sorted by name,
	// then each manifest's addColumns sorted by name, in chain order.
	ColumnOrder []string
	// ColumnOwners maps each column to the manifest that declared it.
	ColumnOwners map[string]string
	// Logic maps each covered content type or memory kind to the manifest whose logic
	// projects it.
	Logic map[string]*Projection
}

type ProjectionSet struct {
	Items  []*Projection
	Tables []*ProjectionTable
	Digest string
	routes map[string][]*Projection
}

// Match returns the manifests whose logic projects resources of the given kind and
// selector, one per table, ordered by table name.
func (s *ProjectionSet) Match(resource, selector string) []*Projection {
	if s == nil {
		return nil
	}
	matched := append([]*Projection(nil), s.routes[projectionRouteKey(resource, selector)]...)
	if selector != "" {
		matched = append(matched, s.routes[projectionRouteKey(resource, "")]...)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].TableName < matched[j].TableName })
	return matched
}

func projectionRouteKey(resource, selector string) string {
	return resource + "\x00" + selector
}

type ProjectionRow struct {
	Common
	ProjectionName      string
	TableName           string
	ResourceID          string
	ConversationID      string
	ConversationGroupID string
	IsDeleted           bool
	// Multi marks rows for tables created with `rows: many`; RowIndex and RowCount are only written for them.
	Multi       bool
	RowIndex    uint32
	RowCount    uint32
	ColumnNames []string
	Values      []any
}

type ProjectionFailureRow struct {
	Common
	ResourceKind        string
	AnalyticsResourceID string
	ConversationID      string
	ConversationGroupID string
	ProjectionName      string
	ErrorCode           string
	AttemptCount        uint32
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
}

func LoadProjectionSet(ctx context.Context, paths []string) (*ProjectionSet, error) {
	set := &ProjectionSet{}
	seen := map[string]string{}
	files, err := projectionManifestFiles(paths)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		entries, readErr := defaultProjectionFiles.ReadDir("defaults")
		if readErr != nil {
			return nil, readErr
		}
		for _, entry := range entries {
			files = append(files, "defaults/"+entry.Name())
		}
	}
	for _, path := range files {
		var raw []byte
		if len(paths) == 0 {
			raw, err = defaultProjectionFiles.ReadFile(path)
		} else {
			raw, err = os.ReadFile(path)
		}
		if err != nil {
			return nil, fmt.Errorf("read analytics projection %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		for document := 1; ; document++ {
			var node yaml.Node
			err := dec.Decode(&node)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("decode analytics projection %s document %d: %w", path, document, err)
			}
			if len(node.Content) == 0 {
				continue
			}
			if yamlDocumentKind(&node) != "AnalyticsProjection" {
				continue
			}
			documentYAML, err := yaml.Marshal(&node)
			if err != nil {
				return nil, fmt.Errorf("encode analytics projection %s document %d: %w", path, document, err)
			}
			var manifest projectionManifest
			strict := yaml.NewDecoder(bytes.NewReader(documentYAML))
			strict.KnownFields(true)
			if err := strict.Decode(&manifest); err != nil {
				return nil, fmt.Errorf("decode analytics projection %s document %d: %w", path, document, err)
			}
			projection, err := compileProjection(ctx, manifest, clickHouseProjectionCapabilities)
			if err != nil {
				return nil, fmt.Errorf("analytics projection %s: %w", filepath.Base(path), err)
			}
			if digest, ok := seen[projection.Name]; ok && digest != projection.Digest {
				return nil, fmt.Errorf("projection %q has conflicting immutable definitions", projection.Name)
			}
			if _, ok := seen[projection.Name]; !ok {
				seen[projection.Name] = projection.Digest
				set.Items = append(set.Items, projection)
			}
		}
	}
	if len(paths) > 0 && len(set.Items) == 0 {
		return nil, errors.New("no AnalyticsProjection resources found in projection paths")
	}
	sort.Slice(set.Items, func(i, j int) bool { return set.Items[i].Name < set.Items[j].Name })
	if err := set.link(); err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, item := range set.Items {
		_, _ = h.Write([]byte(item.Name + "\x00" + item.Digest + "\n"))
	}
	set.Digest = hex.EncodeToString(h.Sum(nil))
	return set, nil
}

func yamlDocumentKind(document *yaml.Node) string {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return ""
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "kind" && root.Content[i+1].Kind == yaml.ScalarNode {
			return root.Content[i+1].Value
		}
	}
	return ""
}

func projectionManifestFiles(paths []string) ([]string, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect analytics projection path %s: %w", path, err)
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("analytics projection path %s is not a regular file or directory", path)
			}
			files = append(files, path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("read analytics projection directory %s: %w", path, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			extension := filepath.Ext(entry.Name())
			if extension != ".yaml" && extension != ".yml" {
				continue
			}
			candidate := filepath.Join(path, entry.Name())
			candidateInfo, err := os.Stat(candidate)
			if err != nil {
				return nil, fmt.Errorf("inspect analytics projection file %s: %w", candidate, err)
			}
			if !candidateInfo.Mode().IsRegular() {
				return nil, fmt.Errorf("analytics projection file %s is not a regular file", candidate)
			}
			files = append(files, candidate)
		}
	}
	return files, nil
}

func compileProjection(ctx context.Context, manifest projectionManifest, capabilities projectionProviderCapabilities) (*Projection, error) {
	if manifest.APIVersion != "memory-service/v1alpha1" || manifest.Kind != "AnalyticsProjection" {
		return nil, errors.New("apiVersion must be memory-service/v1alpha1 and kind must be AnalyticsProjection")
	}
	if !projectionNamePattern.MatchString(manifest.Metadata.Name) {
		return nil, fmt.Errorf("metadata.name %q must match %s", manifest.Metadata.Name, projectionNamePattern)
	}
	if isReservedProjectionTableName(manifest.Metadata.Name) {
		return nil, fmt.Errorf("metadata.name %q is reserved", manifest.Metadata.Name)
	}
	if manifest.Spec.Resource != "entry" && manifest.Spec.Resource != "memory" && manifest.Spec.Resource != "conversation" {
		return nil, errors.New("spec.resource must be entry, memory, or conversation")
	}
	selector := manifest.Spec.Selector.ContentType
	if manifest.Spec.Resource == "memory" {
		selector = manifest.Spec.Selector.Kind
	}
	hasSelector := manifest.Spec.Selector.ContentType != "" || manifest.Spec.Selector.Kind != ""
	if hasSelector {
		if manifest.Spec.Resource == "conversation" {
			return nil, errors.New("conversation projections cannot declare selectors")
		}
		if strings.TrimSpace(selector) == "" || (manifest.Spec.Selector.ContentType != "" && manifest.Spec.Selector.Kind != "") {
			return nil, errors.New("selector must define exactly one non-empty contentType or kind matching the resource")
		}
		if manifest.Spec.Resource == "entry" && manifest.Spec.Selector.Kind != "" || manifest.Spec.Resource == "memory" && manifest.Spec.Selector.ContentType != "" {
			return nil, errors.New("entry projections select contentType and memory projections select kind")
		}
	}
	hasRego := strings.TrimSpace(manifest.Spec.ProjectionRego) != ""
	if hasRego && manifest.Spec.ProjectionRegoFrom != "" {
		return nil, errors.New("spec.projectionRego and spec.projectionRegoFrom are mutually exclusive")
	}
	var rows string
	if manifest.Spec.Previous == "" {
		if manifest.Spec.ProjectionRegoReplaces != "" || manifest.Spec.ProjectionRegoFrom != "" || len(manifest.Spec.AddColumns) > 0 {
			return nil, errors.New("spec.projectionRegoReplaces, spec.projectionRegoFrom, and spec.addColumns require spec.previous")
		}
		if manifest.Spec.Table != "" {
			if !projectionNamePattern.MatchString(manifest.Spec.Table) {
				return nil, fmt.Errorf("spec.table %q must match %s", manifest.Spec.Table, projectionNamePattern)
			}
			if isReservedProjectionTableName(manifest.Spec.Table) {
				return nil, fmt.Errorf("spec.table %q is reserved", manifest.Spec.Table)
			}
		}
		switch manifest.Spec.Rows {
		case "", ProjectionRowsOne:
			// Normalize the default so an explicit `rows: one` has the same digest as an omitted field.
			manifest.Spec.Rows = ""
		case ProjectionRowsMany:
		default:
			return nil, fmt.Errorf("spec.rows must be %s or %s", ProjectionRowsOne, ProjectionRowsMany)
		}
		if len(manifest.Spec.Columns) == 0 {
			return nil, errors.New("spec.columns must not be empty")
		}
		for name, column := range manifest.Spec.Columns {
			if err := validateProjectionColumnDeclaration(name, column, capabilities); err != nil {
				return nil, err
			}
		}
		rows = manifest.Spec.Rows
		if rows == "" {
			rows = ProjectionRowsOne
		}
		if !hasRego {
			return nil, errors.New("projection roots require projectionRego")
		}
	} else {
		if err := validateLaterProjectionManifest(manifest, hasSelector, hasRego, capabilities); err != nil {
			return nil, err
		}
	}
	ordering := "resource"
	if manifest.Spec.ClickHouse != nil {
		if manifest.Spec.Previous != "" {
			return nil, errors.New("spec.clickhouse belongs to the chain root")
		}
		ordering = manifest.Spec.ClickHouse.Ordering
		if ordering != "resource" && ordering != "conversation" {
			return nil, errors.New("spec.clickhouse.ordering must be resource or conversation")
		}
		if ordering == "conversation" && manifest.Spec.Resource == "memory" {
			return nil, errors.New("memory projections cannot use conversation ordering")
		}
	}
	canonical, _ := json.Marshal(manifest)
	digestBytes := sha256.Sum256(canonical)
	digest := hex.EncodeToString(digestBytes[:])
	table := manifest.Spec.Table
	if table == "" {
		table = manifest.Metadata.Name
	}
	projection := &Projection{
		Name: manifest.Metadata.Name, Digest: digest, Resource: manifest.Spec.Resource, TableName: table, RowMode: rows, Ordering: ordering,
		Previous: manifest.Spec.Previous, ProjectionRegoReplaces: manifest.Spec.ProjectionRegoReplaces, ProjectionRegoFrom: manifest.Spec.ProjectionRegoFrom, AddColumns: manifest.Spec.AddColumns,
	}
	if hasSelector || manifest.Spec.Previous == "" {
		projection.Selector = selector
	}
	if manifest.Spec.Previous == "" {
		projection.setColumns(manifest.Spec.Columns)
	}
	if !hasRego {
		return projection, nil
	}
	if len(manifest.Spec.ProjectionRego) > 256<<10 {
		return nil, errors.New("projectionRego exceeds 256 KiB")
	}
	module, err := ast.ParseModule(manifest.Metadata.Name+".rego", manifest.Spec.ProjectionRego)
	if err != nil {
		return nil, fmt.Errorf("parse projectionRego: %w", err)
	}
	if err := validateProjectionModule(module); err != nil {
		return nil, err
	}
	query, err := rego.New(rego.Query("data.memoryservice.analytics.output"), rego.Module(manifest.Metadata.Name+".rego", manifest.Spec.ProjectionRego)).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile projectionRego: %w", err)
	}
	projection.prepared = &query
	return projection, nil
}

// validateLaterProjectionManifest checks a manifest that sets spec.previous. Such a manifest
// inherits its table, columns, and row mode and must declare at least one change.
func validateLaterProjectionManifest(manifest projectionManifest, hasSelector, hasRego bool, capabilities projectionProviderCapabilities) error {
	spec := manifest.Spec
	if !projectionNamePattern.MatchString(spec.Previous) || spec.Previous == manifest.Metadata.Name {
		return fmt.Errorf("spec.previous %q must name another projection", spec.Previous)
	}
	if len(spec.Columns) > 0 || spec.Rows != "" || spec.Table != "" {
		return errors.New("a manifest with spec.previous inherits its table, columns, and rows; it must not declare spec.columns, spec.rows, or spec.table (use spec.addColumns to add columns)")
	}
	if spec.ProjectionRegoReplaces != "" && !hasRego {
		return errors.New("spec.projectionRegoReplaces requires a complete projectionRego")
	}
	if spec.ProjectionRegoFrom != "" && !hasSelector {
		return errors.New("spec.projectionRegoFrom requires a selector for the content type or kind it adds")
	}
	if hasSelector && !hasRego && spec.ProjectionRegoFrom == "" {
		return errors.New("a selector on a manifest with spec.previous requires projectionRego or projectionRegoFrom")
	}
	if hasRego && !hasSelector && spec.ProjectionRegoReplaces == "" {
		return errors.New("projectionRego on a manifest with spec.previous requires a selector or spec.projectionRegoReplaces")
	}
	if !hasSelector && spec.ProjectionRegoReplaces == "" && len(spec.AddColumns) == 0 {
		return errors.New("a manifest with spec.previous must add a selector, replace logic, or add columns")
	}
	for name, column := range spec.AddColumns {
		if err := validateProjectionColumnDeclaration(name, column, capabilities); err != nil {
			return err
		}
		if !column.Nullable {
			return fmt.Errorf("added column %q must be nullable", name)
		}
	}
	return nil
}

func validateProjectionColumnDeclaration(name string, column projectionColumn, capabilities projectionProviderCapabilities) error {
	if !projectionColumnPattern.MatchString(name) {
		return fmt.Errorf("column %q must match %s", name, projectionColumnPattern)
	}
	if _, reserved := projectionReservedColumns[name]; reserved {
		return fmt.Errorf("column %q is reserved", name)
	}
	return validateProjectionColumn(name, column, capabilities)
}

func (p *Projection) setColumns(columns map[string]projectionColumn) {
	p.Columns = columns
	p.ColumnNames = make([]string, 0, len(columns))
	for name := range columns {
		p.ColumnNames = append(p.ColumnNames, name)
	}
	sort.Strings(p.ColumnNames)
}

// link resolves projection chains, validates them, and builds the selector routes.
func (s *ProjectionSet) link() error {
	byName := make(map[string]*Projection, len(s.Items))
	for _, item := range s.Items {
		byName[item.Name] = item
	}
	children := map[string][]*Projection{}
	for _, item := range s.Items {
		if item.Previous == "" {
			continue
		}
		if byName[item.Previous] == nil {
			return fmt.Errorf("projection %q names previous manifest %q, which is not configured; keep every earlier manifest of a chain in the projection paths", item.Name, item.Previous)
		}
		children[item.Previous] = append(children[item.Previous], item)
	}
	for parent, kids := range children {
		if len(kids) > 1 {
			names := make([]string, len(kids))
			for i, kid := range kids {
				names[i] = kid.Name
			}
			sort.Strings(names)
			return fmt.Errorf("projection chain branches at %q: %s all name it as spec.previous; a chain must stay linear", parent, strings.Join(names, ", "))
		}
	}
	visited := map[string]bool{}
	tablesByName := map[string]*ProjectionTable{}
	s.Tables = nil
	s.routes = map[string][]*Projection{}
	for _, root := range s.Items {
		if root.Previous != "" {
			continue
		}
		chain := []*Projection{root}
		visited[root.Name] = true
		for next := children[root.Name]; len(next) == 1; next = children[next[0].Name] {
			chain = append(chain, next[0])
			visited[next[0].Name] = true
		}
		table, err := linkProjectionChain(chain)
		if err != nil {
			return err
		}
		if existing, ok := tablesByName[table.Name]; ok {
			return fmt.Errorf("projections %q and %q both write to table %q; start a chain with spec.previous to share a table", existing.Manifests[0].Name, root.Name, table.Name)
		}
		tablesByName[table.Name] = table
		s.Tables = append(s.Tables, table)
	}
	var unvisited []string
	for _, item := range s.Items {
		if !visited[item.Name] {
			unvisited = append(unvisited, item.Name)
		}
	}
	if len(unvisited) > 0 {
		return fmt.Errorf("projections %s form a cycle; every chain must start with a manifest without spec.previous", strings.Join(unvisited, ", "))
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	for _, table := range s.Tables {
		selectors := make([]string, 0, len(table.Logic))
		for selector := range table.Logic {
			selectors = append(selectors, selector)
		}
		sort.Strings(selectors)
		for _, selector := range selectors {
			key := projectionRouteKey(table.Resource, selector)
			s.routes[key] = append(s.routes[key], table.Logic[selector])
		}
	}
	return nil
}

func linkProjectionChain(chain []*Projection) (*ProjectionTable, error) {
	root := chain[0]
	table := &ProjectionTable{
		Name: root.TableName, Resource: root.Resource, RowMode: root.RowMode, Ordering: root.Ordering, Manifests: chain,
		Columns: map[string]projectionColumn{}, ColumnOwners: map[string]string{}, Logic: map[string]*Projection{},
	}
	for _, name := range root.ColumnNames {
		table.Columns[name] = root.Columns[name]
		table.ColumnOwners[name] = root.Name
		table.ColumnOrder = append(table.ColumnOrder, name)
	}
	root.logic = root
	table.Logic[root.Selector] = root
	position := map[string]int{root.Name: 0}
	for i, manifest := range chain[1:] {
		index := i + 1
		position[manifest.Name] = index
		if manifest.Resource != table.Resource {
			return nil, fmt.Errorf("projection %q has resource %q, but its chain projects %q", manifest.Name, manifest.Resource, table.Resource)
		}
		if root.Selector == "" && (manifest.Selector != "" || manifest.ProjectionRegoFrom != "") {
			return nil, fmt.Errorf("unconditional chain %q cannot introduce selectors", root.Name)
		}
		resolve := func(field, reference string) (*Projection, error) {
			at, ok := position[reference]
			if !ok || at >= index {
				return nil, fmt.Errorf("projection %q spec.%s %q must name an earlier manifest in the same chain", manifest.Name, field, reference)
			}
			if chain[at].logic == nil {
				return nil, fmt.Errorf("projection %q spec.%s %q names a manifest without projection logic", manifest.Name, field, reference)
			}
			return chain[at].logic, nil
		}
		added := make([]string, 0, len(manifest.AddColumns))
		for name := range manifest.AddColumns {
			if owner, exists := table.ColumnOwners[name]; exists {
				return nil, fmt.Errorf("projection %q adds column %q, which %q already declares", manifest.Name, name, owner)
			}
			added = append(added, name)
		}
		sort.Strings(added)
		for _, name := range added {
			table.Columns[name] = manifest.AddColumns[name]
			table.ColumnOwners[name] = manifest.Name
			table.ColumnOrder = append(table.ColumnOrder, name)
		}
		switch {
		case manifest.ProjectionRegoReplaces != "":
			replaced, err := resolve("projectionRegoReplaces", manifest.ProjectionRegoReplaces)
			if err != nil {
				return nil, err
			}
			manifest.logic = manifest
			if manifest.Selector != "" {
				if table.Logic[manifest.Selector] != replaced {
					return nil, fmt.Errorf("projection %q replaces the logic of %q for %q, but %q is not projected by that logic", manifest.Name, manifest.ProjectionRegoReplaces, manifest.Selector, manifest.Selector)
				}
				table.Logic[manifest.Selector] = manifest
				continue
			}
			taken := 0
			for selector, logic := range table.Logic {
				if logic == replaced {
					table.Logic[selector] = manifest
					taken++
				}
			}
			if taken == 0 {
				return nil, fmt.Errorf("projection %q replaces the logic of %q, but that logic no longer projects any %s", manifest.Name, manifest.ProjectionRegoReplaces, selectorNoun(table.Resource))
			}
		case manifest.ProjectionRegoFrom != "":
			logic, err := resolve("projectionRegoFrom", manifest.ProjectionRegoFrom)
			if err != nil {
				return nil, err
			}
			manifest.logic = logic
			if err := table.cover(manifest, logic); err != nil {
				return nil, err
			}
		case manifest.prepared != nil:
			manifest.logic = manifest
			if err := table.cover(manifest, manifest); err != nil {
				return nil, err
			}
		}
	}
	table.ColumnNames = make([]string, 0, len(table.Columns))
	for name := range table.Columns {
		table.ColumnNames = append(table.ColumnNames, name)
	}
	sort.Strings(table.ColumnNames)
	for _, manifest := range chain {
		manifest.TableName = table.Name
		manifest.RowMode = table.RowMode
		manifest.Ordering = table.Ordering
		manifest.Columns = table.Columns
		manifest.ColumnNames = table.ColumnNames
	}
	return table, nil
}

func (t *ProjectionTable) cover(manifest, logic *Projection) error {
	if existing, ok := t.Logic[manifest.Selector]; ok {
		return fmt.Errorf("projection %q selects %q, which the logic of %q already projects in table %q; use spec.projectionRegoReplaces to replace it", manifest.Name, manifest.Selector, existing.Name, t.Name)
	}
	t.Logic[manifest.Selector] = logic
	return nil
}

func selectorNoun(resource string) string {
	if resource == "memory" {
		return "memory kind"
	}
	return "content type"
}

func isReservedProjectionTableName(name string) bool {
	_, reserved := projectionReservedTableNames[name]
	return reserved || strings.HasSuffix(name, "_current") || strings.HasSuffix(name, "_all")
}

func validateProjectionColumn(name string, column projectionColumn, capabilities projectionProviderCapabilities) error {
	if column.ClickHouse != nil {
		if column.ClickHouse.LowCardinality && column.Type != "string" {
			return fmt.Errorf("column %q lowCardinality requires string type", name)
		}
		if column.Type != "json" && (column.ClickHouse.MaxDynamicPaths != 0 || column.ClickHouse.MaxDynamicTypes != 0) {
			return fmt.Errorf("column %q JSON bounds require json type", name)
		}
	}
	switch column.Type {
	case "variant":
		if !capabilities.Variant {
			return fmt.Errorf("column %q type %q is not supported by analytics provider %q", name, column.Type, capabilities.Name)
		}
		if column.ClickHouse != nil && column.Type != "string" {
			return fmt.Errorf("column %q clickhouse options require json or string type", name)
		}
		if len(column.Variants) < 2 {
			return fmt.Errorf("column %q type variant requires at least two variants", name)
		}
		seen := map[string]string{}
		numericVariants := 0
		for _, variant := range column.Variants {
			sqlType, ok := projectionVariantSQLType(variant)
			if !ok {
				return fmt.Errorf("column %q variant %q is not a portable scalar or array type", name, variant)
			}
			if previous, duplicate := seen[sqlType]; duplicate {
				return fmt.Errorf("column %q variants %q and %q both map to ClickHouse type %s", name, previous, variant, sqlType)
			}
			seen[sqlType] = variant
			if variant == "int64" || variant == "uint64" || variant == "float64" {
				numericVariants++
			}
		}
		if numericVariants > 1 {
			return fmt.Errorf("column %q variant must not contain multiple numeric alternatives", name)
		}
	case "json":
		if !capabilities.JSON {
			return fmt.Errorf("column %q type %q is not supported by analytics provider %q", name, column.Type, capabilities.Name)
		}
		if len(column.Variants) > 0 {
			return fmt.Errorf("column %q variants are only supported for type variant", name)
		}
		options := normalizedJSONOptions(column.ClickHouse)
		if options.MaxDynamicPaths < 1 || options.MaxDynamicPaths > maxJSONDynamicPaths {
			return fmt.Errorf("column %q clickhouse.maxDynamicPaths must be between 1 and %d", name, maxJSONDynamicPaths)
		}
		if options.MaxDynamicTypes < 1 || options.MaxDynamicTypes > maxJSONDynamicTypes {
			return fmt.Errorf("column %q clickhouse.maxDynamicTypes must be between 1 and %d", name, maxJSONDynamicTypes)
		}
	default:
		if _, supported := portableProjectionTypes[column.Type]; !supported {
			return fmt.Errorf("column %q has unsupported type %q", name, column.Type)
		}
		if len(column.Variants) > 0 {
			return fmt.Errorf("column %q variants are only supported for type variant", name)
		}
		if column.ClickHouse != nil && column.Type != "string" {
			return fmt.Errorf("column %q clickhouse options require json or string type", name)
		}
	}
	return nil
}

func normalizedJSONOptions(options *projectionClickHouseOptions) projectionClickHouseOptions {
	result := projectionClickHouseOptions{MaxDynamicPaths: defaultJSONDynamicPaths, MaxDynamicTypes: defaultJSONDynamicTypes}
	if options == nil {
		return result
	}
	if options.MaxDynamicPaths != 0 {
		result.MaxDynamicPaths = options.MaxDynamicPaths
	}
	if options.MaxDynamicTypes != 0 {
		result.MaxDynamicTypes = options.MaxDynamicTypes
	}
	return result
}

func projectionColumnSQLType(column projectionColumn) string {
	switch column.Type {
	case "variant":
		alternatives := make([]string, 0, len(column.Variants))
		for _, variant := range column.Variants {
			sqlType, _ := projectionVariantSQLType(variant)
			alternatives = append(alternatives, sqlType)
		}
		sort.Strings(alternatives)
		return "Variant(" + strings.Join(alternatives, ", ") + ")"
	case "json":
		options := normalizedJSONOptions(column.ClickHouse)
		return fmt.Sprintf("JSON(max_dynamic_paths = %d, max_dynamic_types = %d)", options.MaxDynamicPaths, options.MaxDynamicTypes)
	default:
		return portableProjectionTypes[column.Type]
	}
}

func projectionVariantSQLType(kind string) (string, bool) {
	if kind == "bool" {
		return "Bool", true
	}
	sqlType, ok := portableProjectionTypes[kind]
	return sqlType, ok
}

func validateProjectionModule(module *ast.Module) error {
	if module.Package == nil || module.Package.Path.String() != "data.memoryservice.analytics" {
		return errors.New("projectionRego must declare `package memoryservice.analytics`")
	}
	hasOutput := false
	for _, rule := range module.Rules {
		if rule.Head != nil && rule.Head.Name == "output" {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		return errors.New("projectionRego must define an `output` rule")
	}
	allowedInput := map[ast.String]struct{}{"resource": {}, "record": {}}
	badBuiltins := map[string]struct{}{}
	badInputs := map[string]struct{}{}
	ast.WalkTerms(module, func(term *ast.Term) bool {
		ref, ok := term.Value.(ast.Ref)
		if !ok || len(ref) == 0 {
			return false
		}
		name := ref.String()
		if builtin := ast.BuiltinMap[name]; builtin != nil && builtin.IsNondeterministic() {
			badBuiltins[name] = struct{}{}
		}
		root, ok := ref[0].Value.(ast.Var)
		if !ok || root != "input" {
			return false
		}
		if len(ref) < 2 {
			badInputs[name] = struct{}{}
			return false
		}
		key, ok := ref[1].Value.(ast.String)
		if !ok {
			badInputs[name] = struct{}{}
			return false
		}
		if _, ok := allowedInput[key]; !ok {
			badInputs[name] = struct{}{}
		}
		return false
	})
	if len(badBuiltins) > 0 {
		return fmt.Errorf("projectionRego uses nondeterministic builtins: %s", sortedKeys(badBuiltins))
	}
	if len(badInputs) > 0 {
		return fmt.Errorf("projectionRego references disallowed input fields: %s", sortedKeys(badInputs))
	}
	return nil
}

func sortedKeys(values map[string]struct{}) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// Rows evaluates the projection for one resource snapshot. It returns the rows to write,
// or a payload-free error code when any row is invalid. The base row supplies the
// shared resource identity and event metadata.
func (p *Projection) Rows(ctx context.Context, base ProjectionRow, input map[string]any) ([]ProjectionRow, string) {
	outputs, code := p.evaluate(ctx, input)
	if code != "" {
		return nil, code
	}
	base.ProjectionName = p.Name
	base.TableName = p.TableName
	base.IsDeleted = false
	base.ColumnNames = append([]string(nil), p.ColumnNames...)
	if p.RowMode != ProjectionRowsMany {
		base.Values = outputs[0]
		return []ProjectionRow{base}, ""
	}
	base.Multi = true
	if len(outputs) == 0 {
		// A version without rows still needs a marker so rows from older versions stop being current.
		base.Values = p.tombstoneValues()
		return []ProjectionRow{base}, ""
	}
	rows := make([]ProjectionRow, len(outputs))
	for i, values := range outputs {
		row := base
		row.RowIndex = uint32(i)
		row.RowCount = uint32(len(outputs))
		row.Values = values
		rows[i] = row
	}
	return rows, ""
}

// TombstoneRow returns the row that marks the resource as deleted in the projection table.
func (p *Projection) TombstoneRow(base ProjectionRow) ProjectionRow {
	base.ProjectionName = p.Name
	base.TableName = p.TableName
	base.IsDeleted = true
	base.Multi = p.RowMode == ProjectionRowsMany
	base.RowIndex = 0
	base.RowCount = 0
	base.ColumnNames = append([]string(nil), p.ColumnNames...)
	base.Values = p.tombstoneValues()
	return base
}

func (p *Projection) evaluate(ctx context.Context, input map[string]any) ([][]any, string) {

	results, err := p.prepared.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, "rego_evaluation_failed"
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return nil, "output_undefined"
	}
	result := results[0].Expressions[0].Value
	if p.RowMode != ProjectionRowsMany {
		output, ok := result.(map[string]any)
		if !ok {
			return nil, "output_not_object"
		}
		values, code := p.columnValues(output)
		if code != "" {
			return nil, code
		}
		return [][]any{values}, ""
	}
	items, ok := result.([]any)
	if !ok {
		return nil, "output_not_array"
	}
	if len(items) > maxProjectionRows {
		return nil, "too_many_rows"
	}
	rows := make([][]any, 0, len(items))
	for _, item := range items {
		output, ok := item.(map[string]any)
		if !ok {
			return nil, "output_not_object"
		}
		values, code := p.columnValues(output)
		if code != "" {
			return nil, code
		}
		rows = append(rows, values)
	}
	return rows, ""
}

// columnValues checks output against the table's columns. Unknown keys are rejected and
// omitted non-nullable columns fail, while omitted nullable columns are written as NULL so
// logic written before a chain added columns keeps working.
func (p *Projection) columnValues(output map[string]any) ([]any, string) {
	for name := range output {
		if _, known := p.Columns[name]; !known {
			return nil, "column_set_mismatch"
		}
	}
	values := make([]any, 0, len(p.ColumnNames))
	for _, name := range p.ColumnNames {
		column := p.Columns[name]
		value, present := output[name]
		if !present && !column.Nullable {
			return nil, "column_set_mismatch"
		}
		if value == nil {
			if !column.Nullable {
				return nil, "null_required_column"
			}
			if column.Type == "string_array" {
				values = append(values, []string(nil))
			} else {
				values = append(values, nil)
			}
			continue
		}
		normalized, code := normalizeProjectionValue(column, value)
		if code != "" {
			return nil, code
		}
		values = append(values, normalized)
	}
	return values, ""
}

func (p *Projection) tombstoneValues() []any {
	values := make([]any, len(p.ColumnNames))
	for i, name := range p.ColumnNames {
		if p.Columns[name].Type == "string_array" {
			values[i] = []string(nil)
		}
	}
	return values
}

func normalizeProjectionValue(column projectionColumn, value any) (any, string) {
	switch column.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, "type_mismatch"
		}
		if len(text) > maxProjectionStringBytes {
			return nil, "string_too_large"
		}
		return text, ""
	case "bool":
		value, ok := value.(bool)
		if !ok {
			return nil, "type_mismatch"
		}
		if value {
			return uint8(1), ""
		}
		return uint8(0), ""
	case "int64":
		value, ok := integralNumber(value, true)
		return value, typeCode(ok)
	case "uint64":
		value, ok := integralNumber(value, false)
		return value, typeCode(ok)
	case "float64":
		number, ok := numericFloat64(value)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, "type_mismatch"
		}
		return number, ""
	case "timestamp":
		text, ok := value.(string)
		if !ok {
			return nil, "type_mismatch"
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, "invalid_timestamp"
		}
		return parsed.UTC(), ""
	case "string_array":
		items, ok := value.([]any)
		if !ok || len(items) > maxProjectionArrayItems {
			return nil, "invalid_string_array"
		}
		out := make([]string, len(items))
		for i, item := range items {
			text, ok := item.(string)
			if !ok || len(text) > maxProjectionStringBytes {
				return nil, "invalid_string_array"
			}
			out[i] = text
		}
		return out, ""
	case "json_string":
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > maxProjectionJSONBytes {
			return nil, "invalid_json_string"
		}
		return string(raw), ""
	case "variant":
		tagged, ok := value.(map[string]any)
		if !ok || len(tagged) != 2 {
			return nil, "invalid_variant"
		}
		typeName, ok := tagged["type"].(string)
		if !ok {
			return nil, "invalid_variant"
		}
		variantValue, present := tagged["value"]
		if !present || !containsString(column.Variants, typeName) {
			return nil, "invalid_variant"
		}
		normalized, code := normalizeProjectionValue(projectionColumn{Type: typeName}, variantValue)
		if code != "" {
			return nil, code
		}
		if typeName == "bool" {
			normalized = variantValue
		}
		sqlType, _ := projectionVariantSQLType(typeName)
		return ch.NewVariantWithType(normalized, sqlType), ""
	case "json":
		if _, ok := value.(map[string]any); !ok {
			return nil, "invalid_json"
		}
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > maxProjectionJSONBytes {
			return nil, "invalid_json"
		}
		return string(raw), ""
	default:
		return nil, "unsupported_type"
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func integralNumber(value any, signed bool) (any, bool) {
	switch value.(type) {
	case int, int64, uint64, float64, float32, json.Number:
	default:
		return nil, false
	}
	literal := fmt.Sprint(value)
	// Bound decimal expansion before parsing an untrusted Rego number.
	if len(literal) > 256 {
		return nil, false
	}
	if index := strings.IndexAny(literal, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(literal[index+1:])
		if err != nil || exponent > 512 || exponent < -512 {
			return nil, false
		}
	}
	number, ok := new(big.Rat).SetString(literal)
	if !ok || !number.IsInt() {
		return nil, false
	}
	if signed {
		if !number.Num().IsInt64() {
			return nil, false
		}
		return number.Num().Int64(), true
	}
	if !number.Num().IsUint64() {
		return nil, false
	}
	return number.Num().Uint64(), true
}

func numericFloat64(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint64:
		return float64(value), true
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func typeCode(ok bool) string {
	if ok {
		return ""
	}
	return "type_mismatch"
}
