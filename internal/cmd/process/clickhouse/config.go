package clickhouse

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PayloadMode string

const (
	PayloadMetadata  PayloadMode = "metadata"
	PayloadProjected PayloadMode = "projected"
	PayloadFull      PayloadMode = "full"

	RetentionManaged  = "managed"
	RetentionExternal = "external"

	PurgeManaged    = "managed"
	PurgeRecordOnly = "record-only"
	PurgeExternal   = "external"

	defaultMaxRecordBytes = 48 << 20
	maxRecordBytesLimit   = 256 << 20
)

const (
	disableLifecycleEvents = "lifecycle_events"
	disableLineage         = "lineage"
	disableProjections     = "projections"
)

var disableFeatureResources = map[string]map[string]struct{}{
	disableLifecycleEvents: {"conversations": {}, "entries": {}, "memories": {}},
	disableLineage:         {"conversations": {}},
	disableProjections:     {"entries": {}, "memories": {}},
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var replayIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// Config controls one analytics export.
type Config struct {
	ExporterID                 string
	Addresses                  []string
	Protocol                   string
	Database                   string
	Username                   string
	Password                   string
	PasswordFile               string
	TLS                        bool
	CAFile                     string
	AllowInsecureClickHouse    bool
	SchemaMode                 string
	PayloadMode                PayloadMode
	AllowDecryptedContent      bool
	MetadataKeys               []string
	ProjectionPaths            []string
	ProjectionFailurePolicy    string
	ProjectionReplayID         string
	Disable                    []string
	RetentionMode              string
	PurgeMode                  string
	LifecycleRetention         time.Duration
	TombstoneRetention         time.Duration
	GenericPayloadRetention    time.Duration
	ProjectionRetention        time.Duration
	ProjectionFailureRetention time.Duration
	TailOnlyDevelopment        bool
	BatchEvents                int
	BatchRows                  int
	BatchBytes                 int
	MaxRecordBytes             int
	BatchDelay                 time.Duration
	RetryMin                   time.Duration
	RetryMax                   time.Duration
}

func (c *Config) setDefaults() {
	if c.Protocol == "" {
		c.Protocol = "native"
	}
	if c.Database == "" {
		c.Database = "memory_service"
	}
	if c.SchemaMode == "" {
		c.SchemaMode = "manage"
	}
	if c.PayloadMode == "" {
		c.PayloadMode = PayloadMetadata
	}
	if c.ProjectionFailurePolicy == "" {
		c.ProjectionFailurePolicy = "continue-generic"
	}
	if c.RetentionMode == "" {
		c.RetentionMode = RetentionManaged
	}
	if c.PurgeMode == "" {
		c.PurgeMode = PurgeManaged
	}
	if c.BatchEvents == 0 {
		c.BatchEvents = 10000
	}
	if c.BatchRows == 0 {
		c.BatchRows = 100000
	}
	if c.BatchBytes == 0 {
		c.BatchBytes = 8 << 20
	}
	if c.MaxRecordBytes == 0 {
		// A 20 MiB service request dominated by U+2028/U+2029 can double when
		// encoding/json emits those runes as six-byte escapes. Keep bounded
		// headroom above that worst case for the normalized resource wrapper.
		c.MaxRecordBytes = defaultMaxRecordBytes
	}
	if c.BatchDelay == 0 {
		c.BatchDelay = time.Second
	}
	if c.RetryMin == 0 {
		c.RetryMin = time.Second
	}
	if c.RetryMax == 0 {
		c.RetryMax = 30 * time.Second
	}
}

func (c *Config) Validate() error {
	c.setDefaults()
	disable, err := normalizeDisableSelectors(c.Disable)
	if err != nil {
		return err
	}
	c.Disable = disable
	if strings.TrimSpace(c.ExporterID) == "" {
		return errors.New("exporter ID is required")
	}
	if len(c.Addresses) == 0 {
		return errors.New("at least one ClickHouse address is required")
	}
	if c.Protocol != "native" && c.Protocol != "http" {
		return errors.New("ClickHouse protocol must be native or http")
	}
	if !identifierPattern.MatchString(c.Database) {
		return errors.New("ClickHouse database must be a fixed SQL identifier")
	}
	if c.SchemaMode != "manage" && c.SchemaMode != "validate" {
		return errors.New("schema mode must be manage or validate")
	}
	if c.RetentionMode != RetentionManaged && c.RetentionMode != RetentionExternal {
		return errors.New("retention mode must be managed or external")
	}
	if c.PurgeMode != PurgeManaged && c.PurgeMode != PurgeRecordOnly && c.PurgeMode != PurgeExternal {
		return errors.New("purge mode must be managed, record-only, or external")
	}
	if c.Password != "" && c.PasswordFile != "" {
		return errors.New("ClickHouse password and password file are mutually exclusive")
	}
	switch c.PayloadMode {
	case PayloadMetadata:
		if len(c.ProjectionPaths) > 0 {
			return errors.New("projection paths require projected or full payload mode")
		}
	case PayloadProjected:
		if len(c.ProjectionPaths) == 0 {
			return errors.New("projected payload mode requires at least one --projection-path")
		}
	case PayloadFull:
		if !c.AllowDecryptedContent {
			return errors.New("full payload mode requires --allow-decrypted-content")
		}
	default:
		return errors.New("payload mode must be metadata, projected, or full")
	}
	if c.ProjectionFailurePolicy != "continue-generic" && c.ProjectionFailurePolicy != "stop" {
		return errors.New("projection failure policy must be continue-generic or stop")
	}
	if c.ProjectionReplayID != "" {
		if len(c.ProjectionPaths) == 0 {
			return errors.New("projection replay ID requires at least one --projection-path")
		}
		if !replayIDPattern.MatchString(c.ProjectionReplayID) {
			return errors.New("projection replay ID must contain only letters, digits, dots, underscores, or hyphens and be at most 128 characters")
		}
		if c.featureDisabled("entry", disableProjections) && c.featureDisabled("memory", disableProjections) {
			return errors.New("projection replay requires entry or memory projection output to be enabled")
		}
	}
	if len(c.resourceKinds()) == 0 {
		return errors.New("at least one ClickHouse resource kind must be enabled")
	}
	for name, value := range map[string]time.Duration{
		"lifecycle retention": c.LifecycleRetention, "tombstone retention": c.TombstoneRetention,
		"generic payload retention": c.GenericPayloadRetention, "projection retention": c.ProjectionRetention,
		"projection failure retention": c.ProjectionFailureRetention,
	} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	if c.RetentionMode == RetentionExternal && (c.LifecycleRetention != 0 || c.TombstoneRetention != 0 || c.GenericPayloadRetention != 0 || c.ProjectionRetention != 0 || c.ProjectionFailureRetention != 0) {
		return errors.New("ClickHouse retention durations require managed retention mode")
	}
	if c.TombstoneRetention > 0 {
		if c.GenericPayloadRetention == 0 || c.GenericPayloadRetention > c.TombstoneRetention {
			return errors.New("generic payload retention must be positive and no greater than tombstone retention when tombstone retention is enabled")
		}
		if len(c.ProjectionPaths) > 0 && (c.ProjectionRetention == 0 || c.ProjectionRetention > c.TombstoneRetention) {
			return errors.New("projection retention must be positive and no greater than tombstone retention when tombstone retention is enabled")
		}
	}
	if c.BatchEvents < 1 || c.BatchEvents > 100000 {
		return errors.New("batch events must be between 1 and 100000")
	}
	if c.BatchRows < 1 || c.BatchRows > 1000000 {
		return errors.New("batch rows must be between 1 and 1000000")
	}
	if c.BatchBytes < 1024 || c.BatchBytes > 256<<20 {
		return errors.New("batch bytes must be between 1KiB and 256MiB")
	}
	if c.MaxRecordBytes < 1024 || c.MaxRecordBytes > maxRecordBytesLimit {
		return errors.New("maximum record bytes must be between 1KiB and 256MiB")
	}
	if c.RetryMin < 100*time.Millisecond || c.RetryMax < c.RetryMin {
		return errors.New("retry delays must be at least 100ms and retry-max must be greater than or equal to retry-min")
	}
	for _, address := range c.Addresses {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("invalid ClickHouse address %q: %w", address, err)
		}
		if !c.TLS && !isLoopbackHost(host) && !c.AllowInsecureClickHouse {
			return fmt.Errorf("ClickHouse TLS is required for non-loopback address %q; use --allow-insecure-clickhouse only for development", address)
		}
	}
	return nil
}

func (c Config) resourceKinds() []string {
	kinds := make([]string, 0, 3)
	if !c.resourceDisabled("conversation") {
		kinds = append(kinds, "conversation")
	}
	if !c.resourceDisabled("entry") {
		kinds = append(kinds, "entry")
	}
	if !c.resourceDisabled("memory") {
		kinds = append(kinds, "memory")
	}
	return kinds
}

func (c Config) subscriptionKinds() []string {
	if c.PurgeMode != PurgeExternal {
		// Disabled resources can still have older rows that a later hard delete
		// must remove. Conversation deletes can also cover dependent entry rows.
		return []string{"conversation", "entry", "memory"}
	}
	return c.resourceKinds()
}

func (c Config) exportsResourceKind(kind string) bool {
	return (kind == "conversation" || kind == "entry" || kind == "memory") && !c.resourceDisabled(kind)
}

func (c Config) resourceDisabled(kind string) bool {
	resource, ok := externalResourceName(kind)
	if !ok {
		return true
	}
	return stringSliceContains(c.Disable, resource)
}

func (c Config) featureDisabled(kind, feature string) bool {
	resource, ok := externalResourceName(kind)
	if !ok || c.resourceDisabled(kind) {
		return true
	}
	return stringSliceContains(c.Disable, resource+":"+feature) || stringSliceContains(c.Disable, "*:"+feature)
}

func (c Config) enabledOutputs() []string {
	outputs := make([]string, 0, 10)
	for _, resource := range []string{"conversation", "entry", "memory"} {
		if c.resourceDisabled(resource) {
			continue
		}
		plural, _ := externalResourceName(resource)
		outputs = append(outputs, plural)
		for _, feature := range []string{disableLifecycleEvents, disableLineage, disableProjections} {
			if _, applies := disableFeatureResources[feature][plural]; applies && !c.featureDisabled(resource, feature) {
				outputs = append(outputs, plural+":"+feature)
			}
		}
	}
	return outputs
}

func normalizeDisableSelectors(values []string) ([]string, error) {
	selectors := map[string]struct{}{}
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			selector := strings.TrimSpace(raw)
			if selector == "" {
				continue
			}
			resource, feature, hasFeature := strings.Cut(selector, ":")
			if !hasFeature {
				if _, ok := internalResourceName(resource); !ok {
					return nil, fmt.Errorf("invalid ClickHouse disable selector %q; resources are conversations, entries, or memories", selector)
				}
			} else {
				applicable, ok := disableFeatureResources[feature]
				if !ok || feature == "" {
					return nil, fmt.Errorf("invalid ClickHouse disable selector %q; features are lifecycle_events, lineage, or projections", selector)
				}
				if resource != "*" {
					if _, ok := internalResourceName(resource); !ok {
						return nil, fmt.Errorf("invalid ClickHouse disable selector %q; resources are conversations, entries, memories, or *", selector)
					}
					if _, ok := applicable[resource]; !ok {
						return nil, fmt.Errorf("ClickHouse disable selector %q is not supported", selector)
					}
				}
			}
			selectors[selector] = struct{}{}
		}
	}
	result := make([]string, 0, len(selectors))
	for selector := range selectors {
		result = append(result, selector)
	}
	sort.Strings(result)
	return result, nil
}

func externalResourceName(kind string) (string, bool) {
	switch kind {
	case "conversation", "conversations":
		return "conversations", true
	case "entry", "entries":
		return "entries", true
	case "memory", "memories":
		return "memories", true
	default:
		return "", false
	}
}

func internalResourceName(resource string) (string, bool) {
	switch resource {
	case "conversations":
		return "conversation", true
	case "entries":
		return "entry", true
	case "memories":
		return "memory", true
	default:
		return "", false
	}
}

func stringSliceContains(values []string, match string) bool {
	for _, value := range values {
		if value == match {
			return true
		}
	}
	return false
}

func (c Config) readPassword() (string, error) {
	password := c.Password
	if c.PasswordFile != "" {
		value, readErr := os.ReadFile(c.PasswordFile)
		if readErr != nil {
			return "", fmt.Errorf("read ClickHouse password file: %w", readErr)
		}
		password = strings.TrimSpace(string(value))
	}
	return password, nil
}

func (c Config) tlsConfig() (*tls.Config, error) {
	if !c.TLS {
		return nil, nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile == "" {
		return config, nil
	}
	pem, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read ClickHouse CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("ClickHouse CA file contains no certificates")
	}
	config.RootCAs = roots
	return config, nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
