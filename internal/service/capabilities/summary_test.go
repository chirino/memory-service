package capabilities

import (
	"runtime/debug"
	"testing"

	"github.com/chirino/memory-service/internal/config"
)

func TestBuildSummaryMapsConfiguredCapabilities(t *testing.T) {
	cfg := &config.Config{
		DatastoreType:                 "postgres",
		AttachType:                    "db",
		CacheType:                     "infinispan",
		VectorType:                    "pgvector",
		EmbedType:                     "local",
		EventBusType:                  "postgres",
		OutboxEnabled:                 true,
		SearchSemanticEnabled:         true,
		SearchFulltextEnabled:         true,
		CORSEnabled:                   true,
		ManagementListenerEnabled:     true,
		AllowPrivateSourceURLs:        true,
		S3DirectDownload:              true,
		OIDCIssuer:                    "https://issuer.example",
		APIKeys:                       map[string]string{"key": "client"},
		RequireJustification:          true,
		EncryptionProviders:           "dek,plain",
		EncryptionDBDisabled:          false,
		EncryptionAttachmentsDisabled: false,
	}

	summary := buildSummary(cfg, &debug.BuildInfo{
		Main: debug.Module{Version: "1.2.3"},
	}, true)

	if summary.Version != "1.2.3" {
		t.Fatalf("expected version 1.2.3, got %q", summary.Version)
	}
	if summary.Tech.Store != "postgres" || summary.Tech.Attachments != "postgres" {
		t.Fatalf("unexpected tech summary: %+v", summary.Tech)
	}
	if summary.Tech.Vector != "pgvector" || summary.Tech.EventBus != "postgres" || summary.Tech.Embedder != "local" {
		t.Fatalf("unexpected tech summary: %+v", summary.Tech)
	}
	if !summary.Features.OutboxEnabled || !summary.Features.SemanticSearchEnabled || !summary.Features.FulltextSearchEnabled {
		t.Fatalf("unexpected features summary: %+v", summary.Features)
	}
	if !summary.Auth.OIDCEnabled || !summary.Auth.APIKeyEnabled || !summary.Auth.AdminJustificationRequired {
		t.Fatalf("unexpected auth summary: %+v", summary.Auth)
	}
	if !summary.Security.EncryptionEnabled || !summary.Security.DBEncryptionEnabled || !summary.Security.AttachmentEncryptionEnabled {
		t.Fatalf("unexpected security summary: %+v", summary.Security)
	}
}

func TestBuildSummaryNormalizesDisabledVectorAndEncryptionFlags(t *testing.T) {
	cfg := &config.Config{
		DatastoreType:                 "sqlite",
		AttachType:                    "db",
		VectorType:                    "pgvector",
		EmbedType:                     "",
		SearchSemanticEnabled:         false,
		SearchFulltextEnabled:         false,
		EncryptionProviders:           "plain",
		EncryptionDBDisabled:          true,
		EncryptionAttachmentsDisabled: true,
	}

	summary := buildSummary(cfg, &debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}},
	}, true)

	if summary.Version != "abc123" {
		t.Fatalf("expected vcs revision fallback, got %q", summary.Version)
	}
	if summary.Tech.Vector != "none" {
		t.Fatalf("expected vector none, got %q", summary.Tech.Vector)
	}
	if summary.Tech.Attachments != "fs" {
		t.Fatalf("expected sqlite db attachment alias to resolve to fs, got %q", summary.Tech.Attachments)
	}
	if summary.Tech.Embedder != "none" {
		t.Fatalf("expected embedder none, got %q", summary.Tech.Embedder)
	}
	if summary.Features.SemanticSearchEnabled {
		t.Fatalf("expected semantic search disabled, got %+v", summary.Features)
	}
	if summary.Security.EncryptionEnabled || summary.Security.DBEncryptionEnabled || summary.Security.AttachmentEncryptionEnabled {
		t.Fatalf("expected encryption disabled, got %+v", summary.Security)
	}
}

func TestBuildSummaryFallsBackToDevVersion(t *testing.T) {
	summary := buildSummary(&config.Config{}, nil, false)
	if summary.Version != "dev" {
		t.Fatalf("expected dev version fallback, got %q", summary.Version)
	}
}
