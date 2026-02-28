package episodic

import (
	"context"
	"fmt"
	"testing"

	"github.com/open-policy-agent/opa/rego"
)

const defaultPolicyAssertionsRego = `
package memories.tests

import future.keywords.if

# --- authz assertions ---

test_allow_owner_namespace if {
	data.memories.authz.allow with input as {
		"operation": "write",
		"namespace": ["user", "alice", "prefs"],
		"key": "theme",
		"context": {
			"user_id": "alice",
			"client_id": "agent-1",
			"jwt_claims": {"roles": []}
		}
	}
}

test_deny_other_subject if {
	not data.memories.authz.allow with input as {
		"operation": "read",
		"namespace": ["user", "bob", "prefs"],
		"key": "theme",
		"context": {
			"user_id": "alice",
			"client_id": "agent-1",
			"jwt_claims": {"roles": []}
		}
	}
}

test_deny_non_user_namespace if {
	not data.memories.authz.allow with input as {
		"operation": "write",
		"namespace": ["org", "alice", "prefs"],
		"key": "theme",
		"context": {
			"user_id": "alice",
			"client_id": "agent-1",
			"jwt_claims": {"roles": []}
		}
	}
}

# --- attribute extraction assertions ---

test_extracts_namespace_and_sub if {
	data.memories.attributes.attributes with input as {
		"namespace": ["user", "alice", "notes"],
		"key": "k1",
		"value": {"text": "hello"},
		"attributes": {"foo": "bar"}
	} == {"namespace": "user", "sub": "alice"}
}

# --- filter injection assertions ---

test_filter_narrows_prefix_to_subject if {
	data.memories.filter with input as {
		"namespace_prefix": ["user"],
		"filter": {},
		"context": {
			"user_id": "alice",
			"jwt_claims": {"roles": []}
		}
	} == {
		"namespace_prefix": ["user", "alice"],
		"attribute_filter": {"namespace": "user", "sub": "alice"}
	}
}

test_filter_keeps_narrower_prefix if {
	data.memories.filter with input as {
		"namespace_prefix": ["user", "alice", "notes"],
		"filter": {},
		"context": {
			"user_id": "alice",
			"jwt_claims": {"roles": []}
		}
	} == {
		"namespace_prefix": ["user", "alice", "notes"],
		"attribute_filter": {"namespace": "user", "sub": "alice"}
	}
}

test_filter_enforces_namespace_and_sub_attributes if {
	data.memories.filter with input as {
		"namespace_prefix": ["user", "alice"],
		"filter": {"topic": "python"},
		"context": {
			"user_id": "alice",
			"jwt_claims": {"roles": []}
		}
	} == {
		"namespace_prefix": ["user", "alice"],
		"attribute_filter": {"namespace": "user", "sub": "alice"}
	}
}

test_admin_filter_not_restricted if {
	data.memories.filter with input as {
		"namespace_prefix": ["user"],
		"filter": {},
		"context": {
			"user_id": "alice",
			"jwt_claims": {"roles": ["admin"]}
		}
	} == {
		"namespace_prefix": ["user"],
		"attribute_filter": {}
	}
}
`

func TestDefaultPoliciesRegoAssertions(t *testing.T) {
	modules := map[string]string{
		"authz.rego":      defaultAuthzRego,
		"attributes.rego": defaultAttrExtractRego,
		"filter.rego":     defaultFilterInjectRego,
		"tests.rego":      defaultPolicyAssertionsRego,
	}
	testRules := []string{
		"test_allow_owner_namespace",
		"test_deny_other_subject",
		"test_deny_non_user_namespace",
		"test_extracts_namespace_and_sub",
		"test_filter_narrows_prefix_to_subject",
		"test_filter_keeps_narrower_prefix",
		"test_filter_enforces_namespace_and_sub_attributes",
		"test_admin_filter_not_restricted",
	}

	for _, rule := range testRules {
		t.Run(rule, func(t *testing.T) {
			query := fmt.Sprintf("data.memories.tests.%s", rule)
			if !evalRegoBoolean(t, modules, query) {
				t.Fatalf("rego assertion failed: %s", query)
			}
		})
	}
}

func evalRegoBoolean(t *testing.T, modules map[string]string, query string) bool {
	t.Helper()
	opts := []func(*rego.Rego){rego.Query(query)}
	for name, src := range modules {
		opts = append(opts, rego.Module(name, src))
	}

	r := rego.New(opts...)
	results, err := r.Eval(context.Background())
	if err != nil {
		t.Fatalf("eval %s: %v", query, err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		t.Fatalf("eval %s: no result", query)
	}
	v, ok := results[0].Expressions[0].Value.(bool)
	if !ok {
		t.Fatalf("eval %s: expected bool, got %T", query, results[0].Expressions[0].Value)
	}
	return v
}
