package io.github.chirino.memory.grpc;

import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;

public final class GrpcStatusMapper {

    private GrpcStatusMapper() {}

    public static StatusRuntimeException map(Throwable throwable) {
        if (throwable instanceof StatusRuntimeException statusRuntimeException) {
            return statusRuntimeException;
        }
        if (throwable instanceof ResourceNotFoundException notFound) {
            return Status.NOT_FOUND.withDescription(notFound.getMessage()).asRuntimeException();
        }
        if (throwable instanceof AccessDeniedException accessDenied) {
            return Status.PERMISSION_DENIED
                    .withDescription(accessDenied.getMessage())
                    .asRuntimeException();
        }
        if (throwable instanceof ResourceConflictException conflict) {
            return Status.ALREADY_EXISTS
                    .withDescription(conflict.getMessage())
                    .asRuntimeException();
        }
        if (throwable instanceof IllegalArgumentException illegalArgument) {
            return Status.INVALID_ARGUMENT
                    .withDescription(illegalArgument.getMessage())
                    .asRuntimeException();
        }
        return Status.INTERNAL
                .withDescription("Internal server error")
                .withCause(throwable)
                .asRuntimeException();
    }
}
