package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"gopkg.in/yaml.v3"
)

const (
	maxProjectionStringBytes = 64 << 10
	maxProjectionArrayItems  = 1024
	maxProjectionJSONBytes   = 1 << 20
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
		"schema_version": {},
	}
	projectionReservedTableNames = map[string]struct{}{
		"schema_versions":       {},
		"lifecycle_events":      {},
		"resources":             {},
		"deletion_fences":       {},
		"deletion_fence_writer": {},
		"conversations":         {},
		"conversation_lineage":  {},
		"entries":               {},
		"memories":              {},
		"projection_registry":   {},
		"retention_policy":      {},
		"projection_failures":   {},
		"purge_subjects":        {},
		"purge_queue":           {},
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

type projectionSpec struct {
	Resource       string                      `yaml:"resource"`
	Selector       projectionSelector          `yaml:"selector"`
	Columns        map[string]projectionColumn `yaml:"columns"`
	ProjectionRego string                      `yaml:"projectionRego"`
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
	MaxDynamicPaths int `yaml:"maxDynamicPaths,omitempty"`
	MaxDynamicTypes int `yaml:"maxDynamicTypes,omitempty"`
}

type Projection struct {
	Name          string
	Digest        string
	Resource      string
	Selector      string
	TableName     string
	ColumnNames   []string
	Columns       map[string]projectionColumn
	prepared      *rego.PreparedEvalQuery
	useAttributes bool
}

type ProjectionSet struct {
	Items  []*Projection
	Digest string
}

type ProjectionRow struct {
	Common
	ProjectionName      string
	TableName           string
	ResourceID          string
	ConversationID      string
	ConversationGroupID string
	IsDeleted           bool
	ColumnNames         []string
	Values              []any
}

type ProjectionFailureRow struct {
	ExporterID          string
	BatchID             string
	EventID             string
	AnalyticsResourceID string
	ConversationID      string
	ConversationGroupID string
	ProjectionName      string
	ErrorCode           string
	AttemptCount        uint32
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	Version             uint64
}

func LoadProjectionSet(ctx context.Context, paths []string) (*ProjectionSet, error) {
	set := &ProjectionSet{}
	seen := map[string]string{}
	files, err := projectionManifestFiles(paths)
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
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
	if manifest.Spec.Resource != "entry" && manifest.Spec.Resource != "memory" {
		return nil, errors.New("spec.resource must be entry or memory")
	}
	selector := manifest.Spec.Selector.ContentType
	if manifest.Spec.Resource == "memory" {
		selector = manifest.Spec.Selector.Kind
	}
	if strings.TrimSpace(selector) == "" || (manifest.Spec.Selector.ContentType != "" && manifest.Spec.Selector.Kind != "") {
		return nil, errors.New("selector must define exactly one non-empty contentType or kind matching the resource")
	}
	if manifest.Spec.Resource == "entry" && manifest.Spec.Selector.Kind != "" || manifest.Spec.Resource == "memory" && manifest.Spec.Selector.ContentType != "" {
		return nil, errors.New("entry projections select contentType and memory projections select kind")
	}
	if len(manifest.Spec.Columns) == 0 {
		return nil, errors.New("spec.columns must not be empty")
	}
	columnNames := make([]string, 0, len(manifest.Spec.Columns))
	for name, column := range manifest.Spec.Columns {
		if !projectionColumnPattern.MatchString(name) {
			return nil, fmt.Errorf("column %q must match %s", name, projectionColumnPattern)
		}
		if _, reserved := projectionReservedColumns[name]; reserved {
			return nil, fmt.Errorf("column %q is reserved", name)
		}
		if err := validateProjectionColumn(name, column, capabilities); err != nil {
			return nil, err
		}
		columnNames = append(columnNames, name)
	}
	sort.Strings(columnNames)
	canonical, _ := json.Marshal(manifest)
	digestBytes := sha256.Sum256(canonical)
	digest := hex.EncodeToString(digestBytes[:])
	projection := &Projection{Name: manifest.Metadata.Name, Digest: digest, Resource: manifest.Spec.Resource, Selector: selector, TableName: manifest.Metadata.Name, ColumnNames: columnNames, Columns: manifest.Spec.Columns}
	if strings.TrimSpace(manifest.Spec.ProjectionRego) == "" {
		if manifest.Spec.Resource != "memory" {
			return nil, errors.New("entry projections require projectionRego")
		}
		projection.useAttributes = true
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

func isReservedProjectionTableName(name string) bool {
	_, reserved := projectionReservedTableNames[name]
	return reserved || strings.HasSuffix(name, "_current") || strings.HasSuffix(name, "_all")
}

func validateProjectionColumn(name string, column projectionColumn, capabilities projectionProviderCapabilities) error {
	switch column.Type {
	case "variant":
		if !capabilities.Variant {
			return fmt.Errorf("column %q type %q is not supported by analytics provider %q", name, column.Type, capabilities.Name)
		}
		if column.ClickHouse != nil {
			return fmt.Errorf("column %q clickhouse options are only supported for type json", name)
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
		if column.ClickHouse != nil {
			return fmt.Errorf("column %q clickhouse options are only supported for type json", name)
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
	allowedInput := map[ast.String]struct{}{
		"resource": {}, "contentType": {}, "channel": {}, "content": {},
		"kind": {}, "revision": {}, "value": {}, "attributes": {},
	}
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

func (p *Projection) evaluate(ctx context.Context, input map[string]any) ([]any, string) {
	var output map[string]any
	if p.useAttributes {
		attributes, ok := input["attributes"].(map[string]any)
		if !ok {
			return nil, "attributes_unavailable"
		}
		output = attributes
	} else {
		results, err := p.prepared.Eval(ctx, rego.EvalInput(input))
		if err != nil {
			return nil, "rego_evaluation_failed"
		}
		if len(results) != 1 || len(results[0].Expressions) != 1 {
			return nil, "output_undefined"
		}
		var ok bool
		output, ok = results[0].Expressions[0].Value.(map[string]any)
		if !ok {
			return nil, "output_not_object"
		}
	}
	if len(output) != len(p.ColumnNames) {
		return nil, "column_set_mismatch"
	}
	values := make([]any, 0, len(p.ColumnNames))
	for _, name := range p.ColumnNames {
		column := p.Columns[name]
		value, present := output[name]
		if !present {
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
		if !ok {
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
	number, ok := numericFloat64(value)
	if !ok || math.Trunc(number) != number {
		return nil, false
	}
	if signed {
		if number < math.MinInt64 || number > math.MaxInt64 {
			return nil, false
		}
		return int64(number), true
	}
	if number < 0 || number > math.MaxUint64 {
		return nil, false
	}
	return uint64(number), true
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
