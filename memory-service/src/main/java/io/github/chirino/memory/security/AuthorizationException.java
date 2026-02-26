package io.github.chirino.memory.security;

/**
 * Thrown when an authorization operation (permission check or relationship write) fails due to a
 * communication or service error with the authorization backend.
 */
public class AuthorizationException extends RuntimeException {

    public AuthorizationException(String message, Throwable cause) {
        super(message, cause);
    }
}
