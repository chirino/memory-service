package capabilities

import (
	"runtime/debug"
	"strings"

	"github.com/chirino/memory-service/internal/config"
)

type Summary struct {
	Version  string          `json:"version"`
	Tech     TechSummary     `json:"tech"`
	Features FeatureSummary  `json:"features"`
	Auth     AuthSummary     `json:"auth"`
	Security SecuritySummary `json:"security"`
}

type TechSummary struct {
	Store       string `json:"store"`
	Attachments string `json:"attachments"`
	Cache       string `json:"cache"`
	Vector      string `json:"vector"`
	EventBus    string `json:"event_bus"`
	Embedder    string `json:"embedder"`
}

type FeatureSummary struct {
	OutboxEnabled             bool `json:"outbox_enabled"`
	SemanticSearchEnabled     bool `json:"semantic_search_enabled"`
	FulltextSearchEnabled     bool `json:"fulltext_search_enabled"`
	CorsEnabled               bool `json:"cors_enabled"`
	ManagementListenerEnabled bool `json:"management_listener_enabled"`
	PrivateSourceURLsEnabled  bool `json:"private_source_urls_enabled"`
	S3DirectDownloadEnabled   bool `json:"s3_direct_download_enabled"`
}

type AuthSummary struct {
	OIDCEnabled                bool `json:"oidc_enabled"`
	APIKeyEnabled              bool `json:"api_key_enabled"`
	AdminJustificationRequired bool `json:"admin_justification_required"`
}

type SecuritySummary struct {
	EncryptionEnabled           bool `json:"encryption_enabled"`
	DBEncryptionEnabled         bool `json:"db_encryption_enabled"`
	AttachmentEncryptionEnabled bool `json:"attachment_encryption_enabled"`
}

func Build(cfg *config.Config) Summary {
	info, ok := readBuildInfo()
	return buildSummary(cfg, info, ok)
}

var readBuildInfo = debug.ReadBuildInfo

func buildSummary(cfg *config.Config, info *debug.BuildInfo, ok bool) Summary {
	if cfg == nil {
		cfg = &config.Config{}
	}

	encryptionEnabled := primaryEncryptionProvider(cfg.EncryptionProviders) != "plain"
	vector := normalizedVector(cfg)

	return Summary{
		Version: buildVersion(info, ok),
		Tech: TechSummary{
			Store:       defaultString(cfg.DatastoreType, "unknown"),
			Attachments: normalizedAttachments(cfg),
			Cache:       defaultString(cfg.CacheType, "none"),
			Vector:      vector,
			EventBus:    defaultString(cfg.EventBusType, "local"),
			Embedder:    normalizedEmbedder(cfg),
		},
		Features: FeatureSummary{
			OutboxEnabled:             cfg.OutboxEnabled,
			SemanticSearchEnabled:     cfg.SearchSemanticEnabled && vector != "none",
			FulltextSearchEnabled:     cfg.SearchFulltextEnabled,
			CorsEnabled:               cfg.CORSEnabled,
			ManagementListenerEnabled: cfg.ManagementListenerEnabled,
			PrivateSourceURLsEnabled:  cfg.AllowPrivateSourceURLs,
			S3DirectDownloadEnabled:   cfg.S3DirectDownload,
		},
		Auth: AuthSummary{
			OIDCEnabled:                strings.TrimSpace(cfg.OIDCIssuer) != "",
			APIKeyEnabled:              len(cfg.APIKeys) > 0,
			AdminJustificationRequired: cfg.RequireJustification,
		},
		Security: SecuritySummary{
			EncryptionEnabled:           encryptionEnabled,
			DBEncryptionEnabled:         encryptionEnabled && !cfg.EncryptionDBDisabled,
			AttachmentEncryptionEnabled: encryptionEnabled && !cfg.EncryptionAttachmentsDisabled,
		},
	}
}

func buildVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "dev"
	}
	if version := strings.TrimSpace(info.Main.Version); version != "" && version != "(devel)" {
		return version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && strings.TrimSpace(setting.Value) != "" {
			return setting.Value
		}
	}
	return "dev"
}

func normalizedAttachments(cfg *config.Config) string {
	attach := strings.TrimSpace(cfg.AttachType)
	switch cfg.DatastoreType {
	case "sqlite":
		if attach == "" || attach == "db" {
			return "fs"
		}
	case "mongo":
		if attach == "db" {
			return "mongo"
		}
	default:
		if attach == "db" {
			return "postgres"
		}
	}
	if attach == "" {
		return "none"
	}
	return attach
}

func normalizedVector(cfg *config.Config) string {
	if !cfg.SearchSemanticEnabled {
		return "none"
	}
	vector := strings.TrimSpace(cfg.VectorType)
	if vector == "" || vector == "none" {
		return "none"
	}
	return vector
}

func normalizedEmbedder(cfg *config.Config) string {
	embedder := strings.TrimSpace(cfg.EmbedType)
	if embedder == "" {
		return "none"
	}
	return embedder
}

func primaryEncryptionProvider(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			return value
		}
	}
	return "plain"
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
