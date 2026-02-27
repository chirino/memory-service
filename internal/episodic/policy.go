package episodic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/open-policy-agent/opa/rego"
)

// PolicyContext contains the caller's identity for OPA policy evaluation.
type PolicyContext struct {
	UserID    string                 `json:"user_id"`
	ClientID  string                 `json:"client_id"`
	JWTClaims map[string]interface{} `json:"jwt_claims"`
}

// PolicyEngine evaluates the three OPA policies for episodic memory:
//  1. Authz policy — controls read/write/delete access per (namespace, key).
//  2. Attribute extraction policy — extracts plaintext policy_attributes from value+attributes.
//  3. Search filter injection policy — narrows namespace_prefix + adds attribute_filter constraints.
type PolicyEngine struct {
	mu           sync.RWMutex
	authz        *rego.PreparedEvalQuery
	attrExtract  *rego.PreparedEvalQuery
	filterInject *rego.PreparedEvalQuery
	authzSrc     string
	attrSrc      string
	filterSrc    string
}

// PolicyBundle contains source text for the three episodic Rego policies.
type PolicyBundle struct {
	Authz      string `json:"authz"`
	Attributes string `json:"attributes"`
	Filter     string `json:"filter"`
}

// Default built-in Rego policies (used when no policy directory is configured).

const defaultAuthzRego = `
package memories.authz

import future.keywords.if
import future.keywords.in

default allow = false

# Users may access their own namespace subtree
allow if {
    input.namespace[0] == "user"
    input.namespace[1] == input.context.user_id
}

`

const defaultAttrExtractRego = `
package memories.attributes

import future.keywords.if

# Persist namespace root + owner as plaintext attributes for search filtering.
default attributes = {}

attributes = {"namespace": input.namespace[0], "sub": input.namespace[1]} if {
    count(input.namespace) >= 2
}
`

const defaultFilterInjectRego = `
package memories.filter

import future.keywords.if
import future.keywords.in

# Non-admin callers are constrained to their own user subtree.
# If the request is already narrower under user/<user>, keep it.
namespace_prefix := input.namespace_prefix if {
    is_admin
}
namespace_prefix := input.namespace_prefix if {
    not is_admin
    starts_with(input.namespace_prefix, user_prefix)
}
namespace_prefix := user_prefix if {
    not is_admin
    not starts_with(input.namespace_prefix, user_prefix)
}

user_prefix := ["user", input.context.user_id]

starts_with(ns, prefix) if {
    count(prefix) == 0
}
starts_with(ns, prefix) if {
    count(ns) >= count(prefix)
    not mismatch(ns, prefix)
}
mismatch(ns, prefix) if {
    some i
    i < count(prefix)
    ns[i] != prefix[i]
}

is_admin if {
    "admin" in input.context.jwt_claims.roles
}

attribute_filter := {} if {
    is_admin
}
attribute_filter := {"namespace": "user", "sub": input.context.user_id} if {
    not is_admin
}
`

// NewPolicyEngine creates a PolicyEngine. If policyDir is non-empty, policies are
// loaded from that directory; otherwise the built-in defaults are used.
func NewPolicyEngine(ctx context.Context, policyDir string) (*PolicyEngine, error) {
	e := &PolicyEngine{}
	if err := e.load(ctx, policyDir); err != nil {
		return nil, err
	}
	return e, nil
}

func regoSource(policyDir, filename, fallback string) string {
	if policyDir == "" {
		return fallback
	}
	data, err := os.ReadFile(filepath.Join(policyDir, filename))
	if err != nil {
		log.Warn("Policy file not found, using built-in default", "file", filename, "err", err)
		return fallback
	}
	return string(data)
}

func (e *PolicyEngine) load(ctx context.Context, policyDir string) error {
	authzSrc := regoSource(policyDir, "authz.rego", defaultAuthzRego)
	attrSrc := regoSource(policyDir, "attributes.rego", defaultAttrExtractRego)
	filterSrc := regoSource(policyDir, "filter.rego", defaultFilterInjectRego)

	var err error

	e.authz, err = prepareQuery(ctx, authzSrc, "data.memories.authz.allow")
	if err != nil {
		return fmt.Errorf("episodic: load authz policy: %w", err)
	}
	e.attrExtract, err = prepareQuery(ctx, attrSrc, "data.memories.attributes.attributes")
	if err != nil {
		return fmt.Errorf("episodic: load attribute extraction policy: %w", err)
	}
	e.filterInject, err = prepareQuery(ctx, filterSrc, "data.memories.filter")
	if err != nil {
		return fmt.Errorf("episodic: load filter injection policy: %w", err)
	}
	e.authzSrc = authzSrc
	e.attrSrc = attrSrc
	e.filterSrc = filterSrc
	return nil
}

// Reload hot-reloads policies from policyDir. Thread-safe.
func (e *PolicyEngine) Reload(ctx context.Context, policyDir string) error {
	next := &PolicyEngine{}
	if err := next.load(ctx, policyDir); err != nil {
		return err
	}
	e.mu.Lock()
	e.authz = next.authz
	e.attrExtract = next.attrExtract
	e.filterInject = next.filterInject
	e.mu.Unlock()
	return nil
}

// Bundle returns the currently active policy sources.
func (e *PolicyEngine) Bundle() PolicyBundle {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return PolicyBundle{
		Authz:      e.authzSrc,
		Attributes: e.attrSrc,
		Filter:     e.filterSrc,
	}
}

// ReplaceBundle validates and hot-swaps policies from source text.
func (e *PolicyEngine) ReplaceBundle(ctx context.Context, bundle PolicyBundle) error {
	authzSrc := strings.TrimSpace(bundle.Authz)
	attrSrc := strings.TrimSpace(bundle.Attributes)
	filterSrc := strings.TrimSpace(bundle.Filter)
	if authzSrc == "" || attrSrc == "" || filterSrc == "" {
		return fmt.Errorf("authz, attributes, and filter policies are required")
	}

	authz, err := prepareQuery(ctx, authzSrc, "data.memories.authz.allow")
	if err != nil {
		return fmt.Errorf("episodic: compile authz policy: %w", err)
	}
	attr, err := prepareQuery(ctx, attrSrc, "data.memories.attributes.attributes")
	if err != nil {
		return fmt.Errorf("episodic: compile attribute extraction policy: %w", err)
	}
	filter, err := prepareQuery(ctx, filterSrc, "data.memories.filter")
	if err != nil {
		return fmt.Errorf("episodic: compile filter injection policy: %w", err)
	}

	e.mu.Lock()
	e.authz = authz
	e.attrExtract = attr
	e.filterInject = filter
	e.authzSrc = authzSrc
	e.attrSrc = attrSrc
	e.filterSrc = filterSrc
	e.mu.Unlock()
	return nil
}

func prepareQuery(ctx context.Context, src, query string) (*rego.PreparedEvalQuery, error) {
	r := rego.New(
		rego.Query(query),
		rego.Module("policy.rego", src),
	)
	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		return nil, err
	}
	return &pq, nil
}

// IsAllowed evaluates the authz policy and returns true if the operation is allowed.
func (e *PolicyEngine) IsAllowed(ctx context.Context, operation string, namespace []string, key string, pc PolicyContext) (bool, error) {
	e.mu.RLock()
	q := *e.authz
	e.mu.RUnlock()

	input := map[string]interface{}{
		"operation": operation,
		"namespace": namespace,
		"key":       key,
		"context":   policyContextToMap(pc),
	}
	results, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, fmt.Errorf("episodic authz eval: %w", err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return false, nil
	}
	allow, _ := results[0].Expressions[0].Value.(bool)
	return allow, nil
}

// ExtractAttributes evaluates the attribute extraction policy and returns the
// plaintext policy_attributes to store alongside the memory.
func (e *PolicyEngine) ExtractAttributes(ctx context.Context, namespace []string, key string, value, attributes map[string]interface{}) (map[string]interface{}, error) {
	e.mu.RLock()
	q := *e.attrExtract
	e.mu.RUnlock()

	input := map[string]interface{}{
		"namespace":  namespace,
		"key":        key,
		"value":      value,
		"attributes": attributes,
	}
	results, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("episodic attr extract eval: %w", err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return map[string]interface{}{}, nil
	}
	extracted, _ := results[0].Expressions[0].Value.(map[string]interface{})
	if extracted == nil {
		return map[string]interface{}{}, nil
	}
	return extracted, nil
}

// InjectFilter evaluates the search filter injection policy and returns the
// effective namespace_prefix and merged attribute_filter to use for search.
func (e *PolicyEngine) InjectFilter(ctx context.Context, nsPrefix []string, filter map[string]interface{}, pc PolicyContext) ([]string, map[string]interface{}, error) {
	e.mu.RLock()
	q := *e.filterInject
	e.mu.RUnlock()

	input := map[string]interface{}{
		"namespace_prefix": nsPrefix,
		"filter":           filter,
		"context":          policyContextToMap(pc),
	}
	results, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nsPrefix, filter, fmt.Errorf("episodic filter inject eval: %w", err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return nsPrefix, filter, nil
	}
	m, _ := results[0].Expressions[0].Value.(map[string]interface{})
	if m == nil {
		return nsPrefix, filter, nil
	}

	// Extract effective namespace_prefix.
	effectivePrefix := nsPrefix
	if raw, ok := m["namespace_prefix"]; ok {
		effectivePrefix = toStringSlice(raw)
	}

	// Merge attribute_filter into caller-supplied filter.
	merged := make(map[string]interface{})
	for k, v := range filter {
		merged[k] = v
	}
	if af, ok := m["attribute_filter"].(map[string]interface{}); ok {
		for k, v := range af {
			merged[k] = v
		}
	}
	return effectivePrefix, merged, nil
}

func policyContextToMap(pc PolicyContext) map[string]interface{} {
	claims := pc.JWTClaims
	if claims == nil {
		claims = map[string]interface{}{}
	}
	return map[string]interface{}{
		"user_id":    pc.UserID,
		"client_id":  pc.ClientID,
		"jwt_claims": claims,
	}
}

func toStringSlice(v interface{}) []string {
	switch t := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

// ParseAttributeFilter parses a flat JSON attribute filter map from the request.
// Returns it as-is; validation happens at query time.
func ParseAttributeFilter(raw json.RawMessage) (map[string]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid attribute filter: %w", err)
	}
	return m, nil
}

// BuildSQLFilter builds a parameterized SQL WHERE clause fragment and args for
// the given attribute filter. Keys match JSONB fields in policy_attributes.
// Supported forms: bare scalar, {"in": [...]}, {"gt"|"gte"|"lt"|"lte": value}.
func BuildSQLFilter(filter map[string]interface{}) (string, []interface{}) {
	if len(filter) == 0 {
		return "", nil
	}
	var clauses []string
	var args []interface{}

	for key, val := range filter {
		switch v := val.(type) {
		case map[string]interface{}:
			// Range / set membership expressions
			if members, ok := v["in"]; ok {
				list := toInterfaceSlice(members)
				if len(list) > 0 {
					placeholders := make([]string, len(list))
					for i, m := range list {
						args = append(args, jsonScalar(m))
						placeholders[i] = fmt.Sprintf("$%d", len(args))
					}
					clauses = append(clauses,
						fmt.Sprintf("policy_attributes->>'%s' = ANY(ARRAY[%s])",
							escapeSQLIdent(key), strings.Join(placeholders, ",")))
				}
			}
			for op, rhs := range v {
				var sqlOp string
				switch op {
				case "gt":
					sqlOp = ">"
				case "gte":
					sqlOp = ">="
				case "lt":
					sqlOp = "<"
				case "lte":
					sqlOp = "<="
				default:
					continue
				}
				args = append(args, rhs)
				clauses = append(clauses,
					fmt.Sprintf("(policy_attributes->>'%s')::numeric %s $%d",
						escapeSQLIdent(key), sqlOp, len(args)))
			}
		default:
			// Bare scalar: equality
			args = append(args, jsonScalar(v))
			clauses = append(clauses,
				fmt.Sprintf("policy_attributes->>'%s' = $%d", escapeSQLIdent(key), len(args)))
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}

func jsonScalar(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(t)
		return strings.Trim(string(b), `"`)
	}
}

func toInterfaceSlice(v interface{}) []interface{} {
	switch t := v.(type) {
	case []interface{}:
		return t
	}
	return nil
}

func escapeSQLIdent(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
