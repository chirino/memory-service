//go:build site_tests

package sitebdd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	clickhouseprocessor "github.com/chirino/memory-service/internal/cmd/process/clickhouse"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type siteClickHouse struct {
	address string
}

type analyticsFixture struct {
	Conversations      []fixtureConversation      `json:"conversations"`
	Memories           []fixtureMemory            `json:"memories"`
	DeletedResources   []fixtureDeletedResource   `json:"deletedResources"`
	ProjectionFailures []fixtureProjectionFailure `json:"projectionFailures"`
}

type fixtureConversation struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	CreatedAgo string         `json:"createdAgo"`
	Archived   bool           `json:"archived"`
	Entries    []fixtureEntry `json:"entries"`
}

type fixtureEntry struct {
	ID          string           `json:"id"`
	CreatedAgo  string           `json:"createdAgo"`
	ContentType string           `json:"contentType"`
	Content     []map[string]any `json:"content"`
}

type fixtureMemory struct {
	ID              string         `json:"id"`
	LogicalMemoryID string         `json:"logicalMemoryId"`
	Kind            string         `json:"kind"`
	CreatedAgo      string         `json:"createdAgo"`
	Attributes      map[string]any `json:"attributes"`
	Value           map[string]any `json:"value"`
}

type fixtureDeletedResource struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	OccurredAgo string `json:"occurredAgo"`
}

type fixtureProjectionFailure struct {
	EventID        string `json:"eventId"`
	ProjectionName string `json:"projectionName"`
	ErrorCode      string `json:"errorCode"`
	AttemptCount   uint32 `json:"attemptCount"`
	LastSeenAgo    string `json:"lastSeenAgo"`
}

func startSiteClickHouse(t testing.TB) *siteClickHouse {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "clickhouse/clickhouse-server:26.8.2.7",
			Env:          map[string]string{"CLICKHOUSE_DB": "default", "CLICKHOUSE_USER": "clickhouse", "CLICKHOUSE_PASSWORD": "clickhouse", "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"},
			ExposedPorts: []string{"8123/tcp", "9000/tcp"},
			WaitingFor:   wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start ClickHouse for site docs: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate ClickHouse for site docs: %v", err)
		}
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get ClickHouse host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("get ClickHouse native port: %v", err)
	}
	return &siteClickHouse{address: host + ":" + port.Port()}
}

func registerClickHouseSteps(ctx *godog.ScenarioContext, s *SiteScenario) {
	ctx.Step(`^the ClickHouse analytics fixture is loaded:$`, s.loadClickHouseFixture)
	ctx.Step(`^I execute the ClickHouse query:$`, s.executeClickHouseQuery)
	ctx.Step(`^the ClickHouse columns should be "([^"]*)"$`, s.clickHouseColumnsShouldBe)
	ctx.Step(`^the ClickHouse query should return at least (\d+) rows$`, s.clickHouseQueryShouldReturnAtLeast)
	ctx.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.clickHouseConn != nil {
			_ = s.clickHouseConn.Close()
			s.clickHouseConn = nil
		}
		return ctx, nil
	})
}

func (s *SiteScenario) loadClickHouseFixture(doc *godog.DocString) error {
	if s.ClickHouse == nil {
		return fmt.Errorf("ClickHouse test service is not running")
	}
	var fixture analyticsFixture
	if err := json.Unmarshal([]byte(doc.Content), &fixture); err != nil {
		return fmt.Errorf("decode ClickHouse analytics fixture: %w", err)
	}
	if s.ScenarioUID == "" {
		s.ScenarioUID = strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	}
	s.clickHouseDatabase = "sitebdd_" + s.ScenarioUID
	projectionPath := s.ProjectRoot + "/deploy/clickhouse/projections"
	admin, err := ch.Open(&ch.Options{
		Addr: []string{s.ClickHouse.address},
		Auth: ch.Auth{Database: "default", Username: "clickhouse", Password: "clickhouse"},
	})
	if err != nil {
		return fmt.Errorf("open ClickHouse admin connection: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS `"+s.clickHouseDatabase+"`"); err != nil {
		_ = admin.Close()
		return fmt.Errorf("create ClickHouse fixture database: %w", err)
	}
	_ = admin.Close()
	cfg := clickhouseprocessor.Config{
		ExporterID:              "site-docs",
		Addresses:               []string{s.ClickHouse.address},
		Database:                s.clickHouseDatabase,
		Username:                "clickhouse",
		Password:                "clickhouse",
		AllowInsecureClickHouse: true,
		PayloadMode:             clickhouseprocessor.PayloadProjected,
		ProjectionPaths:         []string{projectionPath},
	}
	sink, err := clickhouseprocessor.OpenSink(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create ClickHouse analytics schema: %w", err)
	}
	defer sink.Close()
	projections, err := clickhouseprocessor.LoadProjectionSet(ctx, []string{projectionPath})
	if err != nil {
		return fmt.Errorf("load ClickHouse fixture projections: %w", err)
	}
	batch, err := fixtureBatch(ctx, fixture, projections.Items, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := sink.WriteBatch(ctx, batch); err != nil {
		return fmt.Errorf("load ClickHouse analytics fixture: %w", err)
	}
	conn, err := ch.Open(&ch.Options{
		Addr: []string{s.ClickHouse.address},
		Auth: ch.Auth{Database: s.clickHouseDatabase, Username: "clickhouse", Password: "clickhouse"},
	})
	if err != nil {
		return fmt.Errorf("open ClickHouse fixture query connection: %w", err)
	}
	s.clickHouseConn = conn
	return nil
}

func (s *SiteScenario) executeClickHouseQuery(doc *godog.DocString) error {
	if s.clickHouseConn == nil {
		return fmt.Errorf("ClickHouse fixture is not loaded")
	}
	query := strings.ReplaceAll(doc.Content, "memory_service.", "`"+s.clickHouseDatabase+"`.")
	query = strings.ReplaceAll(query, "database = 'memory_service'", "database = '"+s.clickHouseDatabase+"'")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rows, err := s.clickHouseConn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("execute ClickHouse query: %w", err)
	}
	defer rows.Close()
	s.clickHouseColumns = rows.Columns()
	s.clickHouseRowCount = 0
	columnTypes := rows.ColumnTypes()
	values := make([]any, len(columnTypes))
	for i := range columnTypes {
		values[i] = reflect.New(columnTypes[i].ScanType()).Interface()
	}
	for rows.Next() {
		if err := rows.Scan(values...); err != nil {
			return fmt.Errorf("scan ClickHouse query result: %w", err)
		}
		s.clickHouseRowCount++
	}
	return rows.Err()
}

func (s *SiteScenario) clickHouseColumnsShouldBe(expected string) error {
	want := strings.Split(expected, ",")
	if strings.Join(s.clickHouseColumns, ",") != strings.Join(want, ",") {
		return fmt.Errorf("ClickHouse columns were %v, want %v", s.clickHouseColumns, want)
	}
	return nil
}

func (s *SiteScenario) clickHouseQueryShouldReturnAtLeast(minimum int) error {
	if s.clickHouseRowCount < minimum {
		return fmt.Errorf("ClickHouse query returned %d rows, want at least %d", s.clickHouseRowCount, minimum)
	}
	return nil
}

func fixtureBatch(ctx context.Context, fixture analyticsFixture, projections []*clickhouseprocessor.Projection, anchor time.Time) (clickhouseprocessor.Batch, error) {
	const exporterID = "site-docs"
	batchID := fixtureID("batch")
	version := uint64(1)
	batch := clickhouseprocessor.Batch{ID: batchID, ExporterID: exporterID, Version: version, ObservedAt: anchor}
	addLifecycle := func(kind, resourceID, action, conversationID, groupID, contentType, memoryKind string, occurredAt time.Time) {
		common := clickhouseprocessor.Common{ExporterID: exporterID, BatchID: batchID, EventID: fixtureID(kind + ":" + resourceID + ":" + action), SourceCursor: "fixture", IngestVersion: version, ObservedAt: occurredAt.Add(2 * time.Second), SchemaVersion: 1}
		batch.Lifecycle = append(batch.Lifecycle, clickhouseprocessor.LifecycleRow{Common: common, OccurredAt: occurredAt, ResourceKind: kind, AnalyticsResourceID: resourceID, Action: action, Change: action, ConversationID: conversationID, ConversationGroupID: groupID, ContentType: contentType, MemoryKind: memoryKind, SnapshotAvailable: action != "deleted", SummaryJSON: "{}"})
	}
	for _, conversation := range fixture.Conversations {
		createdAt, err := relativeTime(anchor, conversation.CreatedAgo)
		if err != nil {
			return batch, err
		}
		groupID := "group-" + conversation.ID
		common := clickhouseprocessor.Common{ExporterID: exporterID, BatchID: batchID, EventID: fixtureID("conversation:" + conversation.ID), SourceCursor: "fixture", IngestVersion: version, ObservedAt: createdAt.Add(2 * time.Second), SchemaVersion: 1}
		batch.Resources = append(batch.Resources, clickhouseprocessor.ResourceRow{Common: common, ResourceID: conversation.ID, ConversationID: conversation.ID, ConversationGroupID: groupID, ResourceType: "conversation", CreatedAt: createdAt, UpdatedAt: createdAt, IsArchived: conversation.Archived, PayloadJSON: "{}"})
		addLifecycle("conversation", conversation.ID, "created", conversation.ID, groupID, "", "", createdAt)
		for _, entry := range conversation.Entries {
			entryAt, err := relativeTime(anchor, entry.CreatedAgo)
			if err != nil {
				return batch, err
			}
			entryCommon := clickhouseprocessor.Common{ExporterID: exporterID, BatchID: batchID, EventID: fixtureID("entry:" + entry.ID), SourceCursor: "fixture", IngestVersion: version, ObservedAt: entryAt.Add(2 * time.Second), SchemaVersion: 1}
			batch.Resources = append(batch.Resources, clickhouseprocessor.ResourceRow{Common: entryCommon, ResourceID: entry.ID, ConversationID: conversation.ID, ConversationGroupID: groupID, ResourceType: "entry", CreatedAt: entryAt, UpdatedAt: entryAt, PayloadJSON: "{}"})
			addLifecycle("entry", entry.ID, "created", conversation.ID, groupID, entry.ContentType, "", entryAt)
			content := make([]any, len(entry.Content))
			for i, block := range entry.Content {
				content[i] = block
			}
			input := map[string]any{"resource": "entry", "contentType": entry.ContentType, "channel": "HISTORY", "content": content}
			base := clickhouseprocessor.ProjectionRow{Common: entryCommon, ResourceID: entry.ID, ConversationID: conversation.ID, ConversationGroupID: groupID}
			rows, err := fixtureProjectionRows(ctx, projections, "entry", entry.ContentType, base, input)
			if err != nil {
				return batch, fmt.Errorf("project fixture entry %s: %w", entry.ID, err)
			}
			batch.Projections = append(batch.Projections, rows...)
		}
	}
	for _, memory := range fixture.Memories {
		createdAt, err := relativeTime(anchor, memory.CreatedAgo)
		if err != nil {
			return batch, err
		}
		common := clickhouseprocessor.Common{ExporterID: exporterID, BatchID: batchID, EventID: fixtureID("memory:" + memory.ID), SourceCursor: "fixture", IngestVersion: version, ObservedAt: createdAt.Add(2 * time.Second), SchemaVersion: 1}
		batch.Resources = append(batch.Resources, clickhouseprocessor.ResourceRow{Common: common, ResourceID: memory.ID, ResourceType: "memory", CreatedAt: createdAt, UpdatedAt: createdAt, PayloadJSON: "{}"})
		addLifecycle("memory", memory.ID, "created", "", "", "", memory.Kind, createdAt)
		input := map[string]any{"resource": "memory", "kind": memory.Kind, "value": memory.Value, "attributes": memory.Attributes}
		rows, err := fixtureProjectionRows(ctx, projections, "memory", memory.Kind, clickhouseprocessor.ProjectionRow{Common: common, ResourceID: memory.ID}, input)
		if err != nil {
			return batch, fmt.Errorf("project fixture memory %s: %w", memory.ID, err)
		}
		batch.Projections = append(batch.Projections, rows...)
	}
	for _, deleted := range fixture.DeletedResources {
		occurredAt, err := relativeTime(anchor, deleted.OccurredAgo)
		if err != nil {
			return batch, err
		}
		addLifecycle(deleted.Kind, deleted.ID, "deleted", "", "", "", "", occurredAt)
	}
	for _, failure := range fixture.ProjectionFailures {
		lastSeenAt, err := relativeTime(anchor, failure.LastSeenAgo)
		if err != nil {
			return batch, err
		}
		batch.ProjectionFailures = append(batch.ProjectionFailures, clickhouseprocessor.ProjectionFailureRow{ExporterID: exporterID, BatchID: batchID, EventID: failure.EventID, AnalyticsResourceID: "fixture-resource", ProjectionName: failure.ProjectionName, ErrorCode: failure.ErrorCode, AttemptCount: failure.AttemptCount, FirstSeenAt: lastSeenAt, LastSeenAt: lastSeenAt, Version: version})
	}
	return batch, nil
}

// fixtureProjectionRows evaluates the bundled projection manifests so documentation queries
// run against the same rows the processor writes.
func fixtureProjectionRows(ctx context.Context, projections []*clickhouseprocessor.Projection, resource, selector string, base clickhouseprocessor.ProjectionRow, input map[string]any) ([]clickhouseprocessor.ProjectionRow, error) {
	var result []clickhouseprocessor.ProjectionRow
	for _, projection := range projections {
		if projection.Resource != resource || projection.Selector != selector {
			continue
		}
		rows, code := projection.Rows(ctx, base, input)
		if code != "" {
			return nil, fmt.Errorf("projection %s failed with %s", projection.Name, code)
		}
		result = append(result, rows...)
	}
	return result, nil
}

var dayDurationPattern = regexp.MustCompile(`^(\d+)d(.*)$`)

func relativeTime(anchor time.Time, ago string) (time.Time, error) {
	normalized := ago
	if match := dayDurationPattern.FindStringSubmatch(ago); match != nil {
		days, _ := strconv.Atoi(match[1])
		normalized = strconv.Itoa(days*24) + "h" + match[2]
	}
	duration, err := time.ParseDuration(normalized)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse fixture relative time %q: %w", ago, err)
	}
	return anchor.Add(-duration), nil
}

func fixtureID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
