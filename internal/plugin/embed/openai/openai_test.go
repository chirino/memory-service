//go:build !noopenai

package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/operationevent"
)

func TestRedactAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		msg    string
	}{
		{
			name:   "classic openai key",
			apiKey: "sk-1234567890abcdefghijklmnop",
			msg:    "Incorrect API key provided: sk-1234567890abcdefghijklmnop.",
		},
		{
			name:   "project openai key",
			apiKey: "sk-proj-1234567890_abcdefghijklmnop",
			msg:    "Incorrect API key provided: sk-proj-1234567890_abcdefghijklmnop.",
		},
		{
			name:   "compatible provider key with punctuation",
			apiKey: "provider/key:with+symbols=and.dots",
			msg:    "Incorrect API key provided: provider/key:with+symbols=and.dots.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactAPIKey(tt.msg, tt.apiKey)
			if strings.Contains(got, tt.apiKey) {
				t.Fatalf("redacted message still contains API key: %q", got)
			}
			if !strings.Contains(got, "[REDACTED_OPENAI_API_KEY]") {
				t.Fatalf("redacted message missing marker: %q", got)
			}
		})
	}
}

func TestEmbedTextsUsesGenericErrorForProviderAuthFailure(t *testing.T) {
	apiKey := "provider/key:with+symbols=and.dots"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer "+apiKey; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "provider-request-1")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s."}}`, apiKey)
	}))
	defer server.Close()

	embedder := &OpenAIEmbedder{
		apiKey:  apiKey,
		model:   "text-embedding-3-small",
		baseURL: server.URL,
	}

	_, err := embedder.EmbedTexts(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if strings.Contains(got, apiKey) {
		t.Fatalf("error still contains API key: %q", got)
	}
	if strings.Contains(got, "Incorrect API key provided") {
		t.Fatalf("error still contains upstream auth message: %q", got)
	}
	if !strings.Contains(got, "authentication failed with status 401") {
		t.Fatalf("error missing generic auth failure: %q", got)
	}
	var detailer operationevent.ErrorDetailer
	if !errors.As(err, &detailer) {
		t.Fatalf("error does not expose typed provider details: %T", err)
	}
	details := detailer.OperationErrorDetails()
	if details.Provider == nil || details.Provider.Name != "openai" || details.Provider.StatusCode != http.StatusUnauthorized || details.Provider.TransactionID != "provider-request-1" || details.Reason != "authentication_failed" {
		t.Fatalf("unexpected provider details: %#v", details)
	}
}

func TestEmbedTextsOmitsProviderBodyFromNonAuthProviderError(t *testing.T) {
	apiKey := "provider/key:with+symbols=and.dots"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"Bad request included key %s."}}`, apiKey)
	}))
	defer server.Close()

	embedder := &OpenAIEmbedder{
		apiKey:  apiKey,
		model:   "text-embedding-3-small",
		baseURL: server.URL,
	}

	_, err := embedder.EmbedTexts(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if strings.Contains(got, apiKey) {
		t.Fatalf("error still contains API key: %q", got)
	}
	if strings.Contains(got, "Bad request included key") || strings.Contains(got, "[REDACTED_OPENAI_API_KEY]") {
		t.Fatalf("error included provider response content: %q", got)
	}
}
