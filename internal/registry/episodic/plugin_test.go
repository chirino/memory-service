package episodic

import "testing"

func TestNormalizeAttributeFiltersAcceptsPushdownOperators(t *testing.T) {
	filter, err := NormalizeAttributeFilters(map[string]interface{}{
		"tenant":     "acme",
		"project":    []interface{}{"alpha", "beta"},
		"created_at": map[string]interface{}{"$gte": "2026-01-02T03:04:05Z"},
		"score":      map[string]interface{}{"$lte": 10},
		"tag":        map[string]interface{}{"$exists": true},
	})
	if err != nil {
		t.Fatalf("NormalizeAttributeFilters returned error: %v", err)
	}
	if got, want := len(filter.Conditions), 5; got != want {
		t.Fatalf("condition count = %d, want %d", got, want)
	}
}

func TestNormalizeAttributeFiltersRejectsNonPushdownOperators(t *testing.T) {
	tests := map[string]map[string]interface{}{
		"unknown":      {"tenant": map[string]interface{}{"$unknown": "acme"}},
		"exists false": {"tenant": map[string]interface{}{"$exists": false}},
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeAttributeFilters(raw); err == nil {
				t.Fatalf("NormalizeAttributeFilters returned nil error")
			}
		})
	}
}

func TestNormalizeAttributeFiltersCombinesCallerAndPolicyConstraints(t *testing.T) {
	filter, err := NormalizeAttributeFilters(
		map[string]interface{}{"tenant": "acme"},
		map[string]interface{}{"tenant": map[string]interface{}{"$in": []interface{}{"acme", "beta"}}},
	)
	if err != nil {
		t.Fatalf("NormalizeAttributeFilters returned error: %v", err)
	}
	if got, want := len(filter.Conditions), 2; got != want {
		t.Fatalf("condition count = %d, want %d", got, want)
	}
}
