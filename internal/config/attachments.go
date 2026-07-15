package config

import (
	"fmt"
	"strings"
)

// ResolveAttachmentStoreName resolves Java-parity attachment aliases against the
// configured datastore. The "db" attachment kind maps to the backing database
// store where supported.
func ResolveAttachmentStoreName(cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("missing config")
	}
	attachStoreName := cfg.AttachType
	if cfg.DatastoreType == "sqlite" {
		switch strings.TrimSpace(attachStoreName) {
		case "", "db":
			if cfg.AttachTypeExplicit {
				return "", fmt.Errorf("attachments-kind=%q is not supported with db-kind=sqlite; use --attachments-kind=fs", cfg.AttachType)
			}
			attachStoreName = "fs"
		case "fs":
			// explicit, supported
		default:
			return "", fmt.Errorf("attachments-kind=%q is not supported with db-kind=sqlite; use --attachments-kind=fs", cfg.AttachType)
		}
		if _, err := cfg.ResolvedAttachmentsFSDir(); err != nil {
			return "", err
		}
	} else if attachStoreName == "db" {
		switch cfg.DatastoreType {
		case "mongo":
			return "", fmt.Errorf("attachments-kind=%q is not supported with db-kind=mongo; use --attachments-kind=s3 or --attachments-kind=fs with --attachments-fs-dir", cfg.AttachType)
		default:
			attachStoreName = "postgres"
		}
	} else if cfg.DatastoreType == "mongo" && attachStoreName == "mongo" {
		return "", fmt.Errorf("attachments-kind=%q is not supported; Mongo GridFS attachment storage has been removed, use --attachments-kind=s3 or --attachments-kind=fs with --attachments-fs-dir", cfg.AttachType)
	}
	return attachStoreName, nil
}
