package operationevent

import (
	"errors"
	"fmt"
	"testing"
)

type customAsError struct{ target ErrorDetailer }

func (e customAsError) Error() string { return "private" }
func (e customAsError) As(target any) bool {
	p, ok := target.(*ErrorDetailer)
	if ok {
		*p = e.target
	}
	return ok
}

type detailError struct{ details ErrorDetails }

func (e *detailError) Error() string                       { return "private" }
func (e *detailError) OperationErrorDetails() ErrorDetails { return e.details }

type cycleError struct{ next error }

func (e *cycleError) Error() string { return "cycle" }
func (e *cycleError) Unwrap() error { return e.next }

func TestErrorDetailsWrappingJoinOrderingAndCompatibility(t *testing.T) {
	left := WithErrorDetails(errors.New("secret left"), ErrorDetails{ErrorType: "provider", ErrorCode: "left"})
	right := fmt.Errorf("outer: %w", WithErrorDetails(errors.New("secret right"), ErrorDetails{
		ErrorType: "provider", ErrorCode: "right", Reason: "safe",
		Provider: &ErrorDetailsProvider{Name: "vector", StatusCode: 503, TransactionID: stringsOf("x", 300)},
	}))
	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	event.EnrichError(errors.Join(left, right))
	snapshot := event.Snapshot()
	if len(snapshot.ErrorDetails) != 2 {
		t.Fatalf("got %d details: %#v", len(snapshot.ErrorDetails), snapshot.ErrorDetails)
	}
	if snapshot.ErrorDetails[0].ErrorCode != "right" || snapshot.ErrorDetails[0].Depth <= snapshot.ErrorDetails[1].Depth {
		t.Fatalf("details not deepest-first: %#v", snapshot.ErrorDetails)
	}
	if snapshot.ErrorCode != "left" {
		t.Fatalf("compatibility did not select first shallow traversal match: %q", snapshot.ErrorCode)
	}
	if snapshot.ErrorDetails[0].Provider == nil || len(snapshot.ErrorDetails[0].Provider.TransactionID) != 256 {
		t.Fatal("provider transaction ID was not bounded")
	}
}

func TestWithErrorDetailsPreservesOriginalErrorClassification(t *testing.T) {
	sentinel := errors.New("sentinel")
	err := WithErrorDetails(fmt.Errorf("outer: %w", sentinel), ErrorDetails{ErrorType: "provider", ErrorCode: "wrapped"})
	if !errors.Is(err, sentinel) {
		t.Fatal("wrapped details hid the original sentinel")
	}

	var detailer ErrorDetailer
	if !errors.As(err, &detailer) {
		t.Fatal("wrapped details did not expose ErrorDetailer")
	}
	if got := detailer.OperationErrorDetails().ErrorCode; got != "wrapped" {
		t.Fatalf("detail error code = %q", got)
	}
}

func TestErrorDetailsCustomAsTypedNilCycleAndBounds(t *testing.T) {
	custom := customAsError{target: &detailError{details: ErrorDetails{ErrorType: "custom", ErrorCode: "matched"}}}
	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	event.EnrichError(custom)
	if event.Snapshot().ErrorCode != "matched" {
		t.Fatal("custom As was not honored")
	}

	var typedNil *detailError
	event.EnrichError(typedNil)

	cycle := &cycleError{}
	cycle.next = cycle
	event.EnrichError(cycle)

	causes := make([]error, 10)
	for i := range causes {
		causes[i] = &detailError{details: ErrorDetails{ErrorType: "bounded", ErrorCode: fmt.Sprintf("%d", i)}}
	}
	event2 := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	event2.EnrichError(errors.Join(causes...))
	snapshot := event2.Snapshot()
	if len(snapshot.ErrorDetails) != 8 || !snapshot.ErrorDetailsTruncated {
		t.Fatalf("unexpected bounds: len=%d truncated=%v", len(snapshot.ErrorDetails), snapshot.ErrorDetailsTruncated)
	}
	// Truncation is monotonic across later enrichment.
	event2.EnrichError(&detailError{details: ErrorDetails{ErrorType: "one"}})
	if !event2.Snapshot().ErrorDetailsTruncated {
		t.Fatal("truncation flag regressed")
	}
}

func TestLaterErrorEnrichmentClearsStaleCompatibilityProvider(t *testing.T) {
	event := New("job.test", WithEmitter(func(string, Level, Snapshot) {}))
	event.EnrichError(&detailError{details: ErrorDetails{
		ErrorType: "provider",
		Provider:  &ErrorDetailsProvider{Name: "first", StatusCode: 503},
	}})
	event.EnrichError(&detailError{details: ErrorDetails{ErrorType: "store", ErrorCode: "write_failed"}})
	snapshot := event.Snapshot()
	if snapshot.ErrorType != "store" || snapshot.ErrorCode != "write_failed" || snapshot.ProviderName != "" || snapshot.ProviderStatusCode != 0 {
		t.Fatalf("stale compatibility fields: %#v", snapshot)
	}
}

func stringsOf(value string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += value
	}
	return result
}
