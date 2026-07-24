package operationevent

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestResultMappings(t *testing.T) {
	if got := ResultFromHTTP(http.StatusUnprocessableEntity, nil); got != ResultInvalid {
		t.Fatalf("HTTP result = %q", got)
	}
	if got := ResultFromHTTP(http.StatusOK, context.Canceled); got != ResultCanceled {
		t.Fatalf("canceled HTTP result = %q", got)
	}
	if got := ResultFromGRPC(codes.ResourceExhausted, nil); got != ResultRateLimited {
		t.Fatalf("gRPC result = %q", got)
	}
	if got := ResultFromGRPC(codes.OK, context.DeadlineExceeded); got != ResultTimedOut {
		t.Fatalf("deadline gRPC result = %q", got)
	}
	if got := ResultFromHTTP(http.StatusOK, fmt.Errorf("request closed: %w", context.Canceled)); got != ResultCanceled {
		t.Fatalf("wrapped canceled HTTP result = %q", got)
	}
	if got := ResultFromHTTP(http.StatusOK, fmt.Errorf("request expired: %w", context.DeadlineExceeded)); got != ResultTimedOut {
		t.Fatalf("wrapped deadline HTTP result = %q", got)
	}
	if got := ResultFromGRPC(codes.OK, fmt.Errorf("request closed: %w", context.Canceled)); got != ResultCanceled {
		t.Fatalf("wrapped canceled gRPC result = %q", got)
	}
	if got := ResultFromGRPC(codes.OK, fmt.Errorf("request expired: %w", context.DeadlineExceeded)); got != ResultTimedOut {
		t.Fatalf("wrapped deadline gRPC result = %q", got)
	}
}
