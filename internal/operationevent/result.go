package operationevent

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
)

// ResultFromHTTP maps a final HTTP status and context state to a semantic result.
func ResultFromHTTP(status int, ctxErr error) Result {
	if errors.Is(ctxErr, context.Canceled) {
		return ResultCanceled
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) || status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout {
		return ResultTimedOut
	}
	switch {
	case status >= 200 && status < 400:
		return ResultSuccess
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ResultInvalid
	case status == http.StatusUnauthorized:
		return ResultUnauthenticated
	case status == http.StatusForbidden:
		return ResultForbidden
	case status == http.StatusNotFound:
		return ResultNotFound
	case status == http.StatusConflict:
		return ResultConflict
	case status == http.StatusTooManyRequests:
		return ResultRateLimited
	case status >= 500:
		return ResultFailed
	default:
		return ResultRejected
	}
}

// ResultFromGRPC maps a final gRPC code and context state to a semantic result.
func ResultFromGRPC(code codes.Code, ctxErr error) Result {
	if errors.Is(ctxErr, context.Canceled) || code == codes.Canceled {
		return ResultCanceled
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) || code == codes.DeadlineExceeded {
		return ResultTimedOut
	}
	switch code {
	case codes.OK:
		return ResultSuccess
	case codes.InvalidArgument, codes.OutOfRange:
		return ResultInvalid
	case codes.Unauthenticated:
		return ResultUnauthenticated
	case codes.PermissionDenied:
		return ResultForbidden
	case codes.NotFound:
		return ResultNotFound
	case codes.AlreadyExists, codes.Aborted, codes.FailedPrecondition:
		return ResultConflict
	case codes.ResourceExhausted:
		return ResultRateLimited
	case codes.Internal, codes.Unavailable, codes.DataLoss, codes.Unknown:
		return ResultFailed
	default:
		return ResultRejected
	}
}
