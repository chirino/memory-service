package bdd

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/mark3labs/mcp-go/mcp"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		m := &mcpSteps{s: s}
		ctx.Step("^`memory-service mcp embedded` is running with sqlite database \"([^\"]*)\"$", m.memoryServiceMCPEmbeddedIsRunningWithSQLiteDatabase)
		ctx.Step("^`memory-service mcp remote` is running against the scenario server with API key \"([^\"]*)\" and bearer token \"([^\"]*)\"$", m.memoryServiceMCPRemoteIsRunningAgainstTheScenarioServerWithAPIKeyAndBearerToken)
		ctx.Step("^`memory-service mcp remote` is running against the scenario server with API key \"([^\"]*)\"$", m.memoryServiceMCPRemoteIsRunningAgainstTheScenarioServerWithAPIKey)
		ctx.Step(`^I call the MCP tool "([^"]*)" with arguments:$`, m.iCallTheMCPToolWithArguments)
		ctx.Step(`^the MCP tool response should contain "([^"]*)"$`, m.theMCPToolResponseShouldContain)
		ctx.Step(`^the MCP tool call should succeed$`, m.theMCPToolCallShouldSucceed)
		ctx.Step(`^the sqlite database "([^"]*)" should contain (\d+) conversations?$`, m.theSQLiteDatabaseShouldContainConversations)
		ctx.Step(`^the sqlite database "([^"]*)" should contain an entry with text "([^"]*)"$`, m.theSQLiteDatabaseShouldContainAnEntryWithText)
		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			m.close()
			return ctx, nil
		})
	})
}

type mcpSteps struct {
	s             *cucumber.TestScenario
	proc          *mcpProcess
	currentSQLite *SQLiteTestDB
	tempDir       string
	lastToolText  string
	lastToolError bool
}

type mcpProcess struct {
	cmd    *exec.Cmd
	stdin  ioWriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	mu     sync.Mutex
	nextID int
}

type ioWriteCloser interface {
	Write([]byte) (int, error)
	Close() error
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (m *mcpSteps) memoryServiceMCPEmbeddedIsRunningWithSQLiteDatabase(name string) error {
	tempDir, err := os.MkdirTemp("", "memory-service-mcp-embedded-"+m.s.ScenarioUID+"-")
	if err != nil {
		return err
	}
	dbPath := filepath.Join(tempDir, name)
	m.tempDir = tempDir
	m.currentSQLite = &SQLiteTestDB{DBURL: dbPath}

	return m.startProcess(
		[]string{
			"mcp", "embedded",
			"--db-url", dbPath,
		},
		[]string{
			"MEMORY_SERVICE_SEARCH_SEMANTIC_ENABLED=false",
		},
	)
}

func (m *mcpSteps) memoryServiceMCPRemoteIsRunningAgainstTheScenarioServerWithAPIKeyAndBearerToken(apiKey, bearerToken string) error {
	sqliteDB, ok := m.s.TestDB().(*SQLiteTestDB)
	if !ok {
		return fmt.Errorf("remote MCP sqlite scenarios require SQLiteTestDB, got %T", m.s.TestDB())
	}
	m.currentSQLite = sqliteDB

	args := []string{
		"mcp", "remote",
		"--url", m.s.APIBaseURL(),
		"--api-key", apiKey,
	}
	if bearerToken != "" {
		args = append(args, "--bearer-token", m.s.IsolatedUser(bearerToken))
	}
	return m.startProcess(args, nil)
}

func (m *mcpSteps) memoryServiceMCPRemoteIsRunningAgainstTheScenarioServerWithAPIKey(apiKey string) error {
	return m.memoryServiceMCPRemoteIsRunningAgainstTheScenarioServerWithAPIKeyAndBearerToken(apiKey, "")
}

func (m *mcpSteps) startProcess(args []string, extraEnv []string) error {
	m.close()

	projectDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}

	goArgs := []string{"run"}
	if tags := currentBuildTags(); tags != "" {
		goArgs = append(goArgs, "-tags", tags)
	}
	goArgs = append(goArgs, projectDir)
	goArgs = append(goArgs, args...)

	cmd := exec.CommandContext(context.Background(), "go", goArgs...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"MEMORY_SERVICE_LOG_LEVEL=error",
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	proc := &mcpProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		stderr: stderr,
		nextID: 1,
	}

	if err := proc.initialize(); err != nil {
		_ = proc.close()
		return err
	}

	m.proc = proc
	return nil
}

func currentBuildTags() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == "-tags" {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}

func (m *mcpSteps) iCallTheMCPToolWithArguments(toolName string, body *godog.DocString) error {
	if m.proc == nil {
		return fmt.Errorf("MCP process is not running")
	}

	expanded, err := m.s.Expand(body.Content)
	if err != nil {
		return err
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(expanded), &args); err != nil {
		return fmt.Errorf("parse MCP tool arguments: %w", err)
	}

	resp, err := m.proc.callTool(toolName, args)
	if err != nil {
		return err
	}
	m.lastToolError = resp.Result.IsError

	texts := make([]string, 0, len(resp.Result.Content))
	for _, item := range resp.Result.Content {
		if item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	m.lastToolText = strings.Join(texts, "\n")
	m.s.Variables["mcp.lastToolResponse"] = m.lastToolText
	return nil
}

func (m *mcpSteps) theMCPToolResponseShouldContain(expected string) error {
	expanded, err := m.s.Expand(expected)
	if err != nil {
		return err
	}
	if !strings.Contains(m.lastToolText, expanded) {
		return fmt.Errorf("expected MCP tool response to contain %q, got %q", expanded, m.lastToolText)
	}
	return nil
}

func (m *mcpSteps) theMCPToolCallShouldSucceed() error {
	if m.lastToolError {
		return fmt.Errorf("expected MCP tool call to succeed, got error response: %s", m.lastToolText)
	}
	return nil
}

func (m *mcpSteps) theSQLiteDatabaseShouldContainConversations(_ string, count int) error {
	db, err := m.currentSQLiteConn()
	if err != nil {
		return err
	}
	defer db.Close()

	var actual int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM conversations").Scan(&actual); err != nil {
		return err
	}
	if actual != count {
		return fmt.Errorf("expected %d conversations, got %d", count, actual)
	}
	return nil
}

func (m *mcpSteps) theSQLiteDatabaseShouldContainAnEntryWithText(_ string, text string) error {
	db, err := m.currentSQLiteConn()
	if err != nil {
		return err
	}
	defer db.Close()

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM entries WHERE CAST(content AS TEXT) LIKE ?`,
		"%"+text+"%",
	).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("expected sqlite entries to contain %q", text)
	}
	return nil
}

func (m *mcpSteps) currentSQLiteConn() (*sql.DB, error) {
	if m.currentSQLite == nil {
		return nil, fmt.Errorf("no current sqlite database configured")
	}
	return m.currentSQLite.conn(context.Background())
}

func (m *mcpSteps) close() {
	if m.proc != nil {
		_ = m.proc.close()
		m.proc = nil
	}
	if m.tempDir != "" {
		_ = os.RemoveAll(m.tempDir)
		m.tempDir = ""
	}
}

func (p *mcpProcess) initialize() error {
	if _, err := p.request("initialize", map[string]any{
		"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
		"clientInfo": map[string]any{
			"name":    "bdd-client",
			"version": "1.0.0",
		},
	}); err != nil {
		return err
	}
	return p.notify("notifications/initialized", map[string]any{})
}

func (p *mcpProcess) callTool(name string, arguments map[string]any) (*jsonRPCResponse, error) {
	return p.request("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
}

func (p *mcpProcess) request(method string, params any) (*jsonRPCResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID++
	id := p.nextID
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := p.writeMessage(msg); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for MCP response to %s; stderr: %s", method, p.stderr.String())
		}
		line, err := p.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read MCP response for %s: %w; stderr: %s", method, err, p.stderr.String())
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue
		}
		switch v := resp.ID.(type) {
		case float64:
			if int(v) != id {
				continue
			}
		case int:
			if v != id {
				continue
			}
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP %s error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return &resp, nil
	}
}

func (p *mcpProcess) notify(method string, params any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (p *mcpProcess) writeMessage(msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = p.stdin.Write(data)
	return err
}

func (p *mcpProcess) close() error {
	if p == nil {
		return nil
	}
	_ = p.stdin.Close()
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "signal: killed") {
			return err
		}
		return nil
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		return nil
	}
}
