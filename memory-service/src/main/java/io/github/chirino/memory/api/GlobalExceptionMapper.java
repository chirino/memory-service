package io.github.chirino.memory.api;

import io.github.chirino.memory.client.model.ErrorResponse;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.ConstraintViolationException;
import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.util.List;
import java.util.Map;
import org.jboss.logging.Logger;
import org.jboss.resteasy.reactive.server.ServerExceptionMapper;

/**
 * Global exception mapper that ensures all unhandled exceptions are logged with full stack traces
 * and returned as structured JSON error responses.
 */
public class GlobalExceptionMapper {

    private static final Logger LOG = Logger.getLogger(GlobalExceptionMapper.class);

    @ServerExceptionMapper
    public Response handleConstraintViolation(ConstraintViolationException e) {
        List<Map<String, String>> violations =
                e.getConstraintViolations().stream()
                        .map(
                                v ->
                                        Map.of(
                                                "field", extractFieldName(v),
                                                "message", v.getMessage()))
                        .toList();
        ErrorResponse error = new ErrorResponse();
        error.setError("Validation failed");
        error.setCode("validation_error");
        error.setDetails(Map.of("violations", violations));
        return Response.status(Response.Status.BAD_REQUEST)
                .type(MediaType.APPLICATION_JSON)
                .entity(error)
                .build();
    }

    private String extractFieldName(ConstraintViolation<?> violation) {
        String path = violation.getPropertyPath().toString();
        int lastDot = path.lastIndexOf('.');
        return lastDot >= 0 ? path.substring(lastDot + 1) : path;
    }

    @ServerExceptionMapper
    public Response handleException(Exception e) {
        // For WebApplicationException (includes JAX-RS responses), preserve the original status
        if (e instanceof WebApplicationException wae) {
            int status = wae.getResponse().getStatus();
            if (status >= 500) {
                LOG.errorf(e, "Server error %d", status);
            }
            return wae.getResponse();
        }

        // All other unhandled exceptions → 500 with full stack trace
        LOG.errorf(e, "Unhandled exception");
        ErrorResponse error = new ErrorResponse();
        error.setError("Internal server error");
        error.setCode("internal_error");
        error.setDetails(
                Map.of(
                        "message",
                        e.getMessage() != null ? e.getMessage() : e.getClass().getName()));
        return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                .type(MediaType.APPLICATION_JSON)
                .entity(error)
                .build();
    }
}
