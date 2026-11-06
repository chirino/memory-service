package episodic

import (
	"testing"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestNormalizeAttributeFiltersTemporalFields verifies that the temporal policy
// attributes emitted by the cognition projection.rego policy (observedAt,
// effectiveAt) are accepted as valid filter fields with range operators, making
// them queryable through the search API.
func TestNormalizeAttributeFiltersTemporalFields(t *testing.T) {
	t.Run("canonicalizes_equivalent_rfc3339_range_bounds", func(t *testing.T) {
		tests := map[string]struct {
			input string
			want  string
		}{
			"utc_without_fraction": {
				input: "2025-06-10T13:30:00Z",
				want:  "2025-06-10T13:30:00.000000000Z",
			},
			"timezone_offset": {
				input: "2025-06-10T09:30:00-04:00",
				want:  "2025-06-10T13:30:00.000000000Z",
			},
			"short_fraction": {
				input: "2025-06-10T13:30:00.5Z",
				want:  "2025-06-10T13:30:00.500000000Z",
			},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				filter, err := NormalizeAttributeFilters(map[string]interface{}{
					"observedAt": map[string]interface{}{"$gte": tt.input},
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got := len(filter.Conditions); got != 1 {
					t.Fatalf("condition count = %d, want 1", got)
				}
				value := filter.Conditions[0].Values[0]
				if value.Text != tt.want {
					t.Errorf("text value = %q, want %q", value.Text, tt.want)
				}
				if value.Raw != tt.want {
					t.Errorf("raw value = %v, want %q", value.Raw, tt.want)
				}
			})
		}
	})

	t.Run("observedAt_gte", func(t *testing.T) {
		filter, err := NormalizeAttributeFilters(map[string]interface{}{
			"observedAt": map[string]interface{}{"$gte": "2025-06-01T00:00:00Z"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(filter.Conditions); got != 1 {
			t.Fatalf("condition count = %d, want 1", got)
		}
		cond := filter.Conditions[0]
		if cond.Field != "observedAt" {
			t.Errorf("field = %q, want %q", cond.Field, "observedAt")
		}
		if cond.Op != AttributeFilterOpGte {
			t.Errorf("op = %q, want %q", cond.Op, AttributeFilterOpGte)
		}
		if cond.RangeKind != AttributeFilterRangeTime {
			t.Errorf("rangeKind = %q, want %q", cond.RangeKind, AttributeFilterRangeTime)
		}
	})

	t.Run("effectiveAt_lte", func(t *testing.T) {
		filter, err := NormalizeAttributeFilters(map[string]interface{}{
			"effectiveAt": map[string]interface{}{"$lte": "2025-12-31T23:59:59Z"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(filter.Conditions); got != 1 {
			t.Fatalf("condition count = %d, want 1", got)
		}
		cond := filter.Conditions[0]
		if cond.Field != "effectiveAt" {
			t.Errorf("field = %q, want %q", cond.Field, "effectiveAt")
		}
		if cond.Op != AttributeFilterOpLte {
			t.Errorf("op = %q, want %q", cond.Op, AttributeFilterOpLte)
		}
		if cond.RangeKind != AttributeFilterRangeTime {
			t.Errorf("rangeKind = %q, want %q", cond.RangeKind, AttributeFilterRangeTime)
		}
	})

	t.Run("observedAt_range_window", func(t *testing.T) {
		filter, err := NormalizeAttributeFilters(map[string]interface{}{
			"observedAt": map[string]interface{}{
				"$gte": "2025-01-01T00:00:00Z",
				"$lte": "2025-06-30T23:59:59Z",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(filter.Conditions); got != 2 {
			t.Fatalf("condition count = %d, want 2", got)
		}
		for _, cond := range filter.Conditions {
			if cond.RangeKind != AttributeFilterRangeTime {
				t.Errorf("rangeKind = %q, want %q", cond.RangeKind, AttributeFilterRangeTime)
			}
		}
	})

	t.Run("rejects_non_rfc3339_string", func(t *testing.T) {
		_, err := NormalizeAttributeFilters(map[string]interface{}{
			"observedAt": map[string]interface{}{"$gte": "not-a-timestamp"},
		})
		if err == nil {
			t.Fatal("expected error for non-RFC3339 string, got nil")
		}
	})

	t.Run("observedAt_eq_string", func(t *testing.T) {
		filter, err := NormalizeAttributeFilters(map[string]interface{}{
			"observedAt": "2025-06-10T13:30:00Z",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(filter.Conditions); got != 1 {
			t.Fatalf("condition count = %d, want 1", got)
		}
		if filter.Conditions[0].Op != AttributeFilterOpEq {
			t.Errorf("op = %q, want %q", filter.Conditions[0].Op, AttributeFilterOpEq)
		}
	})
}

func TestValidateAttributeFilterFieldAcceptsValidNames(t *testing.T) {
	valid := []string{
		"sub",
		"observedAt",
		"score",
		"my_field",
		"field-name",
		"field.name",
		"a",
		"field123",
	}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAttributeFilterField(name); err != nil {
				t.Fatalf("ValidateAttributeFilterField(%q) returned unexpected error: %v", name, err)
			}
		})
	}
}

func TestValidateAttributeFilterFieldRejectsInvalidNames(t *testing.T) {
	invalid := []string{
		"",
		"$eq",
		"a b",
		"x'--",
		"field\n",
		"col; DROP",
	}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAttributeFilterField(name); err == nil {
				t.Fatalf("ValidateAttributeFilterField(%q) expected error, got nil", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestMemoriesMatch
// ---------------------------------------------------------------------------

// helpers used only by MemoriesMatch tests

func ptrInt64(i int64) *int64 { return &i }

// baseMemoryExisting returns a fully-populated MemoryExistingFields whose
// ExpiresAt is exactly createdAt + 60 s, matching a TTLSeconds of 60.
func baseMemoryExisting() MemoryExistingFields {
	created := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	expires := created.Add(60 * time.Second)
	return MemoryExistingFields{
		Namespace:  "user\x1ealice\x1ens",
		Key:        "k1",
		Value:      map[string]interface{}{"x": float64(1)},
		Index:      map[string]string{"text": "hello"},
		CreatedAt:  created,
		ExpiresAt:  &expires,
		MemoryKind: "default/v1",
	}
}

// baseMemoryRequest returns a PutMemoryRequest that exactly matches baseMemoryExisting.
func baseMemoryRequest() PutMemoryRequest {
	return PutMemoryRequest{
		Namespace:  []string{"user", "alice", "ns"},
		Key:        "k1",
		Value:      map[string]interface{}{"x": float64(1)},
		Index:      map[string]string{"text": "hello"},
		TTLSeconds: 60,
		MemoryKind: "default/v1",
	}
}

// baseEncoded is the encoded namespace that matches baseMemoryExisting.Namespace.
const baseEncoded = "user\x1ealice\x1ens"

// TestMemoriesMatch verifies that MemoriesMatch returns true only when every
// compared field matches, and false when exactly one field is changed.
// One sub-case per compared field ensures no field can be silently dropped
// from the comparison without failing at least one case.
// Fields excluded from comparison are also exercised to confirm they are ignored.
func TestMemoriesMatch(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		mutateFlds func(*MemoryExistingFields)
		mutateReq  func(*PutMemoryRequest)
		encodedNS  string
		wantMatch  bool
	}

	cases := []testCase{
		// ----------------------------------------------------------------
		// Positive: everything identical
		// ----------------------------------------------------------------
		{
			name:      "exact match returns true",
			encodedNS: baseEncoded,
			wantMatch: true,
		},

		// ----------------------------------------------------------------
		// Namespace (compared via the encodedNS argument vs stored Namespace)
		// ----------------------------------------------------------------
		{
			name:      "different encodedNS argument does not match stored namespace",
			encodedNS: "user\x1ebob\x1ens", // encodedNS != flds.Namespace
			wantMatch: false,
		},
		{
			name:       "stored Namespace changed does not match encodedNS argument",
			mutateFlds: func(f *MemoryExistingFields) { f.Namespace = "user\x1ebob\x1ens" },
			encodedNS:  baseEncoded, // encodedNS == alice, stored == bob
			wantMatch:  false,
		},

		// ----------------------------------------------------------------
		// Key
		// ----------------------------------------------------------------
		{
			name:       "key differs in stored",
			mutateFlds: func(f *MemoryExistingFields) { f.Key = "other-key" },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:      "key differs in request",
			mutateReq: func(r *PutMemoryRequest) { r.Key = "other-key" },
			encodedNS: baseEncoded,
			wantMatch: false,
		},

		// ----------------------------------------------------------------
		// Value (reflect.DeepEqual over decrypted map)
		// ----------------------------------------------------------------
		{
			name:       "top-level value field differs in stored",
			mutateFlds: func(f *MemoryExistingFields) { f.Value = map[string]interface{}{"x": float64(2)} },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:      "top-level value field differs in request",
			mutateReq: func(r *PutMemoryRequest) { r.Value = map[string]interface{}{"x": float64(99)} },
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			name: "nested value object differs",
			mutateFlds: func(f *MemoryExistingFields) {
				f.Value = map[string]interface{}{"x": float64(1), "n": map[string]interface{}{"a": "b"}}
			},
			mutateReq: func(r *PutMemoryRequest) {
				r.Value = map[string]interface{}{"x": float64(1), "n": map[string]interface{}{"a": "DIFFERENT"}}
			},
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			name:       "extra key in stored value",
			mutateFlds: func(f *MemoryExistingFields) { f.Value = map[string]interface{}{"x": float64(1), "extra": "surplus"} },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:      "extra key in request value",
			mutateReq: func(r *PutMemoryRequest) { r.Value = map[string]interface{}{"x": float64(1), "extra": "surplus"} },
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			name: "value array field equal",
			mutateFlds: func(f *MemoryExistingFields) {
				f.Value = map[string]interface{}{"arr": []interface{}{"a", "b"}}
			},
			mutateReq: func(r *PutMemoryRequest) {
				r.Value = map[string]interface{}{"arr": []interface{}{"a", "b"}}
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			name: "value array field differs",
			mutateFlds: func(f *MemoryExistingFields) {
				f.Value = map[string]interface{}{"arr": []interface{}{"a", "b"}}
			},
			mutateReq: func(r *PutMemoryRequest) {
				r.Value = map[string]interface{}{"arr": []interface{}{"a", "c"}}
			},
			encodedNS: baseEncoded,
			wantMatch: false,
		},

		// ----------------------------------------------------------------
		// Index — spec: "normalize nil to {} on both sides"
		// ----------------------------------------------------------------
		{
			name:       "index value differs",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = map[string]string{"text": "different"} },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:       "extra key in stored index",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = map[string]string{"text": "hello", "extra": "val"} },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:      "extra key in request index",
			mutateReq: func(r *PutMemoryRequest) { r.Index = map[string]string{"text": "hello", "extra": "val"} },
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			// stored nil, request empty → match (both normalize to {})
			name:       "stored nil index equals request empty index",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = nil },
			mutateReq:  func(r *PutMemoryRequest) { r.Index = map[string]string{} },
			encodedNS:  baseEncoded,
			wantMatch:  true,
		},
		{
			// stored empty, request nil → match
			name:       "stored empty index equals request nil index",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = map[string]string{} },
			mutateReq:  func(r *PutMemoryRequest) { r.Index = nil },
			encodedNS:  baseEncoded,
			wantMatch:  true,
		},
		{
			name:       "both nil index",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = nil },
			mutateReq:  func(r *PutMemoryRequest) { r.Index = nil },
			encodedNS:  baseEncoded,
			wantMatch:  true,
		},
		{
			name:       "both empty index",
			mutateFlds: func(f *MemoryExistingFields) { f.Index = map[string]string{} },
			mutateReq:  func(r *PutMemoryRequest) { r.Index = map[string]string{} },
			encodedNS:  baseEncoded,
			wantMatch:  true,
		},

		// ----------------------------------------------------------------
		// TTL — spec: storedExpiresAt - storedCreatedAt == ttlSeconds * second;
		//             ttlSeconds == 0 means ExpiresAt == nil
		// ----------------------------------------------------------------
		{
			// Baseline: expiresAt = createdAt + 60 s, TTL = 60 → match
			name:      "TTL matches stored duration exactly",
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			name:      "TTL differs from stored duration",
			mutateReq: func(r *PutMemoryRequest) { r.TTLSeconds = 120 },
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			// Off by 1 second — must not match
			name:      "TTL off by 1 second does not match",
			mutateReq: func(r *PutMemoryRequest) { r.TTLSeconds = 59 },
			encodedNS: baseEncoded,
			wantMatch: false,
		},
		{
			// TTLSeconds == 0 and stored ExpiresAt == nil → match
			name:       "TTL zero and stored ExpiresAt nil match",
			mutateFlds: func(f *MemoryExistingFields) { f.ExpiresAt = nil },
			mutateReq:  func(r *PutMemoryRequest) { r.TTLSeconds = 0 },
			encodedNS:  baseEncoded,
			wantMatch:  true,
		},
		{
			// TTLSeconds == 0 but stored ExpiresAt is set → must NOT match
			name:      "TTL zero but stored ExpiresAt set does not match",
			mutateReq: func(r *PutMemoryRequest) { r.TTLSeconds = 0 },
			encodedNS: baseEncoded,
			// baseMemoryExisting has ExpiresAt set to createdAt+60s
			wantMatch: false,
		},
		{
			// TTLSeconds > 0 but stored ExpiresAt is nil → must NOT match
			name:       "TTL set but stored ExpiresAt nil does not match",
			mutateFlds: func(f *MemoryExistingFields) { f.ExpiresAt = nil },
			encodedNS:  baseEncoded,
			// baseMemoryRequest has TTLSeconds = 60
			wantMatch: false,
		},

		// ----------------------------------------------------------------
		// MemoryKind
		// ----------------------------------------------------------------
		{
			name:       "memory kind differs in stored",
			mutateFlds: func(f *MemoryExistingFields) { f.MemoryKind = "profile/v1" },
			encodedNS:  baseEncoded,
			wantMatch:  false,
		},
		{
			name:      "memory kind differs in request",
			mutateReq: func(r *PutMemoryRequest) { r.MemoryKind = "profile/v1" },
			encodedNS: baseEncoded,
			wantMatch: false,
		},

		// ----------------------------------------------------------------
		// Fields NOT compared (spec: "Not compared: PolicyAttributes, Kind,
		// revision, expected_revision, expected_revision_id, AuthorizedPredecessor")
		// ----------------------------------------------------------------
		{
			// Changing PolicyAttributes must not affect the result.
			name: "PolicyAttributes differ — not compared",
			mutateReq: func(r *PutMemoryRequest) {
				r.PolicyAttributes = map[string]interface{}{"tenant": "completely-different"}
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// CreateOnly is a handler-side gate, not a stored field.
			name:      "CreateOnly true — not compared",
			mutateReq: func(r *PutMemoryRequest) { r.CreateOnly = true },
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// ExpectedRevision applies to the original attempt only.
			name:      "ExpectedRevision set — not compared",
			mutateReq: func(r *PutMemoryRequest) { r.ExpectedRevision = ptrInt64(7) },
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// AuthorizedPredecessor applies to the original attempt only.
			name: "AuthorizedPredecessor set — not compared",
			mutateReq: func(r *PutMemoryRequest) {
				r.AuthorizedPredecessor = &MemoryPredecessorExpectation{Exists: true}
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// RevisionID is the ID being looked up, not a comparison field.
			name: "RevisionID set — not compared",
			mutateReq: func(r *PutMemoryRequest) {
				id := uuid.MustParse("11111111-0000-4000-8000-000000000001")
				r.RevisionID = &id
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// ExpectedRevisionID applies to the original attempt only.
			name: "ExpectedRevisionID set — not compared",
			mutateReq: func(r *PutMemoryRequest) {
				id := uuid.MustParse("22222222-0000-4000-8000-000000000002")
				r.ExpectedRevisionID = &id
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
		{
			// Spec: "Never compare absolute ExpiresAt".
			// Two rows with the same TTL but written at different wall-clock times
			// must match. Mutate both CreatedAt and ExpiresAt by the same offset —
			// duration is preserved, absolute times differ.
			name: "same TTL duration but different wall-clock CreatedAt matches",
			mutateFlds: func(f *MemoryExistingFields) {
				shift := 24 * time.Hour
				f.CreatedAt = f.CreatedAt.Add(shift)
				shifted := (*f.ExpiresAt).Add(shift)
				f.ExpiresAt = &shifted
			},
			encodedNS: baseEncoded,
			wantMatch: true,
		},
	}

	for _, tc := range cases {
		tc := tc // capture
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			flds := baseMemoryExisting()
			req := baseMemoryRequest()
			if tc.mutateFlds != nil {
				tc.mutateFlds(&flds)
			}
			if tc.mutateReq != nil {
				tc.mutateReq(&req)
			}
			got := MemoriesMatch(flds, req, tc.encodedNS)
			assert.Equal(t, tc.wantMatch, got,
				"MemoriesMatch(%q) = %v, want %v", tc.name, got, tc.wantMatch)
		})
	}
}

// ---------------------------------------------------------------------------
// TestMemoryErrorTypes — error construction and errors.As chain
// These tests live here (package episodic) because they reference both
// registrystore error types and the episodic package constants, letting a
// single file prove both the error chain and the expected code values.
// ---------------------------------------------------------------------------

// TestMemoryRevisionIDConflictError_ErrorChain verifies:
//   - errors.As finds *MemoryRevisionIDConflictError
//   - errors.As finds *ConflictError via Unwrap (unwraps-to, not same type)
//   - direct type assertion to *ConflictError returns false (different concrete type)
//   - Code and RevisionID fields are preserved
func TestMemoryRevisionIDConflictError_ErrorChain(t *testing.T) {
	t.Parallel()

	const revID = "6f1c1d0a-0000-4000-8000-000000000001"
	err := &registrystore.MemoryRevisionIDConflictError{
		ConflictError: &registrystore.ConflictError{
			Message: "memory revision ID already in use with different content",
			Code:    registrystore.MemoryRevisionAlreadyExistsCode,
		},
		RevisionID: revID,
	}

	// errors.As must find *MemoryRevisionIDConflictError.
	var ownTarget *registrystore.MemoryRevisionIDConflictError
	require.ErrorAs(t, err, &ownTarget, "errors.As must find *MemoryRevisionIDConflictError")
	assert.Equal(t, revID, ownTarget.RevisionID)
	assert.Equal(t, registrystore.MemoryRevisionAlreadyExistsCode, ownTarget.Code)

	// errors.As must also find *ConflictError via the Unwrap chain.
	var conflict *registrystore.ConflictError
	require.ErrorAs(t, err, &conflict,
		"errors.As must find *ConflictError via Unwrap chain (unwraps-to, not same type)")
	assert.Equal(t, registrystore.MemoryRevisionAlreadyExistsCode, conflict.Code)

	// Direct type assertion must fail — the outer type is NOT *ConflictError.
	_, isDirectConflict := any(err).(*registrystore.ConflictError)
	assert.False(t, isDirectConflict,
		"*MemoryRevisionIDConflictError must not be directly assertable to *ConflictError")
}

// TestExpectedActiveRevisionIDMismatchError_ErrorChain mirrors the chain test
// for ExpectedActiveRevisionIDMismatchError.
func TestExpectedActiveRevisionIDMismatchError_ErrorChain(t *testing.T) {
	t.Parallel()

	const expectedID = "aaaa1d0a-0000-4000-8000-000000000001"
	const actualID = "bbbb1d0a-0000-4000-8000-000000000002"
	err := &registrystore.ExpectedActiveRevisionIDMismatchError{
		ConflictError: &registrystore.ConflictError{
			Message: "active revision ID does not match expected_revision_id",
			Code:    registrystore.ExpectedActiveRevisionIDMismatchCode,
		},
		ExpectedID: expectedID,
		ActualID:   actualID,
	}

	var ownTarget *registrystore.ExpectedActiveRevisionIDMismatchError
	require.ErrorAs(t, err, &ownTarget,
		"errors.As must find *ExpectedActiveRevisionIDMismatchError")
	assert.Equal(t, expectedID, ownTarget.ExpectedID)
	assert.Equal(t, actualID, ownTarget.ActualID)
	assert.Equal(t, registrystore.ExpectedActiveRevisionIDMismatchCode, ownTarget.Code)

	var conflict *registrystore.ConflictError
	require.ErrorAs(t, err, &conflict,
		"errors.As must find *ConflictError via Unwrap chain")
	assert.Equal(t, registrystore.ExpectedActiveRevisionIDMismatchCode, conflict.Code)

	_, isDirectConflict := any(err).(*registrystore.ConflictError)
	assert.False(t, isDirectConflict,
		"*ExpectedActiveRevisionIDMismatchError must not be directly assertable to *ConflictError")
}

// TestMemoryErrorCodesDistinct verifies no accidental code reuse across all
// conflict codes defined in the store package.
func TestMemoryErrorCodesDistinct(t *testing.T) {
	t.Parallel()

	codes := []string{
		registrystore.MemoryRevisionAlreadyExistsCode,
		registrystore.ExpectedActiveRevisionIDMismatchCode,
		registrystore.ConversationAlreadyExistsCode,
		registrystore.DuplicateSequenceConflictCode,
	}
	for i := 0; i < len(codes); i++ {
		for j := i + 1; j < len(codes); j++ {
			assert.NotEqual(t, codes[i], codes[j],
				"error codes[%d]=%q and codes[%d]=%q must be distinct", i, codes[i], j, codes[j])
		}
	}
}
