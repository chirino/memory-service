package io.github.chirino.memory.api.dto;

import java.util.Map;

public class ErrorResponse {

    private String error;
    private String code;
    private Map<String, Object> details;

    public ErrorResponse() {}

    public ErrorResponse(String error, String code) {
        this.error = error;
        this.code = code;
    }

    public ErrorResponse(String error, String code, Map<String, Object> details) {
        this.error = error;
        this.code = code;
        this.details = details;
    }

    public String getError() {
        return error;
    }

    public void setError(String error) {
        this.error = error;
    }

    public String getCode() {
        return code;
    }

    public void setCode(String code) {
        this.code = code;
    }

    public Map<String, Object> getDetails() {
        return details;
    }

    public void setDetails(Map<String, Object> details) {
        this.details = details;
    }
}
