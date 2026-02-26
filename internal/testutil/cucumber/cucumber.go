// Package cucumber provides a godog-based BDD test framework with HTTP API testing support.
//
// Variables are scoped to the scenario. HTTP response state is stored in the user's session.
// Switching users switches the session. Scenarios are executed concurrently.
//
// Variable resolution supports:
//   - ${variableName}           → scenario variable lookup
//   - ${response.body}          → full HTTP response body (Java feature file compat)
//   - ${response.body.field}    → response body field via gojq (Java feature file compat)
//   - ${response.field}         → response body field via gojq
//   - ${variable.field}         → nested field access
//   - ${variable | pipe}        → pipe transformations (json, json_escape, string)
package cucumber

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
	"github.com/google/uuid"
	"github.com/itchyny/gojq"
	"github.com/pmezard/go-difflib/difflib"
)

func NewTestSuite() *TestSuite {
	return &TestSuite{
		APIURL: "http://localhost:8080",
		Extra:  map[string]interface{}{},
	}
}

func DefaultOptions() godog.Options {
	return godog.Options{
		Output:      colors.Colored(os.Stdout),
		Format:      "progress",
		Paths:       []string{"features"},
		Randomize:   time.Now().UTC().UnixNano(),
		Concurrency: 10,
	}
}

// ApplyReportOptions configures junit XML output when GODOG_REPORT_DIR is set.
// Pass t.Name() as testName; slashes are replaced with dashes to form the filename.
// Returns a cleanup function that must be called (or deferred) after the test runs.
func ApplyReportOptions(opts *godog.Options, testName string) func() {
	reportDir := os.Getenv("GODOG_REPORT_DIR")
	if reportDir == "" {
		return func() {}
	}
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return func() {}
	}
	safeName := strings.ReplaceAll(testName, "/", "-")
	path := filepath.Join(reportDir, safeName+".xml")
	f, err := os.Create(path)
	if err != nil {
		return func() {}
	}
	opts.Output = f
	opts.Format = "junit"
	return func() { _ = f.Close() }
}

// TestDB abstracts direct database operations needed by BDD steps,
// so different backends (Postgres, MongoDB) can be injected by the test runner.
type TestDB interface {
	// ClearAll wipes all data (called before each scenario).
	ClearAll(ctx context.Context) error
	// ResolveGroupID returns the conversation_group_id for a given conversation ID.
	ResolveGroupID(ctx context.Context, conversationID string) (string, error)
	// ExecSQL runs a raw SQL query and returns rows as maps. Non-SQL backends return (nil, nil) to skip assertions.
	ExecSQL(ctx context.Context, query string) ([]map[string]interface{}, error)
	// SoftDeleteConversation sets deleted_at on the conversation and its group to `days` ago.
	SoftDeleteConversation(ctx context.Context, conversationID string, days int) error
	// Task queue operations for task-queue BDD feature.
	DeleteAllTasks(ctx context.Context) error
	CreateTask(ctx context.Context, id, taskType, body string) error
	CreateFailedTask(ctx context.Context, id, taskType, body string) error
	ClaimReadyTasks(ctx context.Context, limit int) ([]TaskRow, error)
	DeleteTask(ctx context.Context, id string) error
	FailTask(ctx context.Context, id, errMsg string) error
	GetTask(ctx context.Context, id string) (*TaskRow, error)
	CountTasks(ctx context.Context) (int, error)
}

// TaskRow represents a row from the tasks table/collection.
type TaskRow struct {
	ID         string
	TaskType   string
	TaskBody   string
	RetryAt    time.Time
	RetryCount int
	LastError  *string
}

// TestSuite holds state global to all test scenarios.
// Accessed concurrently from all test scenarios.
type TestSuite struct {
	Context  interface{} // opaque application context
	APIURL   string
	Mu       sync.Mutex
	TestingT *testing.T
	Extra    map[string]interface{} // additional test-scoped objects (e.g. mock servers)
	DB       TestDB                 // injected by test runner for store-specific operations
}

// TestUser represents a user that can interact with the API.
type TestUser struct {
	Name    string
	Subject string // Bearer token value
	Mu      sync.Mutex
}

// TestScenario holds state for a single scenario. Not accessed concurrently.
type TestScenario struct {
	Suite           *TestSuite
	CurrentUser     string
	PathPrefix      string
	sessions        map[string]*TestSession
	Variables       map[string]interface{}
	Users           map[string]*TestUser
	hasTestCaseLock bool
}

func (s *TestScenario) Logf(format string, args ...any) {
	s.Suite.TestingT.Logf(format, args...)
}

func (s *TestScenario) User() *TestUser {
	s.Suite.Mu.Lock()
	defer s.Suite.Mu.Unlock()
	return s.Users[s.CurrentUser]
}

func (s *TestScenario) Session() *TestSession {
	result := s.sessions[s.CurrentUser]
	if result == nil {
		result = &TestSession{
			TestUser: s.User(),
			Client:   &http.Client{},
			Header:   http.Header{},
		}
		s.sessions[s.CurrentUser] = result
	}
	return result
}

// Encoding wraps marshal/unmarshal for a specific format.
type Encoding struct {
	Name      string
	Marshal   func(any) ([]byte, error)
	Unmarshal func([]byte, any) error
}

var JSONEncoding = Encoding{
	Name: "json",
	Marshal: func(a any) ([]byte, error) {
		return json.MarshalIndent(a, "", "  ")
	},
	Unmarshal: json.Unmarshal,
}

func (s *TestScenario) EncodingMustMatch(encoding Encoding, actual, expected string, expandExpected bool) error {
	var actualParsed interface{}
	err := encoding.Unmarshal([]byte(actual), &actualParsed)
	if err != nil {
		return fmt.Errorf("error parsing actual %s: %w\n%s was:\n%s", encoding.Name, err, encoding.Name, actual)
	}

	expanded := expected
	if expandExpected {
		expanded, err = s.Expand(expected)
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(expanded) == "" {
		actual, _ := encoding.Marshal(actualParsed)
		return fmt.Errorf("expected %s not specified, actual %s was:\n%s", encoding.Name, encoding.Name, actual)
	}

	var expectedParsed interface{}
	if err := encoding.Unmarshal([]byte(expanded), &expectedParsed); err != nil {
		return fmt.Errorf("error parsing expected %s: %w\n%s was:\n%s", encoding.Name, err, encoding.Name, expanded)
	}

	if !reflect.DeepEqual(expectedParsed, actualParsed) {
		expected, _ := encoding.Marshal(expectedParsed)
		actual, _ := encoding.Marshal(actualParsed)
		diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(string(expected)),
			B:        difflib.SplitLines(string(actual)),
			FromFile: "Expected",
			ToFile:   "Actual",
			Context:  1,
		})
		return fmt.Errorf("actual does not match expected, diff:\n%s", diff)
	}
	return nil
}

func (s *TestScenario) JSONMustMatch(actual, expected string, expandExpected bool) error {
	return s.EncodingMustMatch(JSONEncoding, actual, expected, expandExpected)
}

func (s *TestScenario) JSONMustContain(actual, expected string, expand bool) error {
	var actualParsed interface{}
	err := json.Unmarshal([]byte(actual), &actualParsed)
	if err != nil {
		return fmt.Errorf("error parsing actual json: %w\njson was:\n%s", err, actual)
	}

	if expand {
		expected, err = s.Expand(expected)
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(expected) == "" {
		actual, _ := JSONEncoding.Marshal(actualParsed)
		return fmt.Errorf("expected json not specified, actual json was:\n%s", actual)
	}

	var expectedParsed interface{}
	if err := json.Unmarshal([]byte(expected), &expectedParsed); err != nil {
		return fmt.Errorf("error parsing expected json: %w\njson was:\n%s", err, expected)
	}

	if err := jsonSubset(expectedParsed, actualParsed, ""); err != nil {
		expectedIndented, _ := JSONEncoding.Marshal(expectedParsed)
		actualIndented, _ := JSONEncoding.Marshal(actualParsed)
		return fmt.Errorf("actual does not contain expected.\n  mismatch: %s\n  expected:\n%s\n  actual:\n%s",
			err, expectedIndented, actualIndented)
	}
	return nil
}

// jsonSubset checks that every field in expected exists in actual with a matching value.
// For objects: all keys in expected must exist in actual with matching values (extra keys in actual are OK).
// For arrays: arrays must have the same length, and each element is compared with subset semantics.
// For primitives: exact equality.
func jsonSubset(expected, actual interface{}, path string) error {
	if expected == nil {
		if actual != nil {
			return fmt.Errorf("at %s: expected null, got %v", pathOrRoot(path), actual)
		}
		return nil
	}

	switch exp := expected.(type) {
	case map[string]interface{}:
		act, ok := actual.(map[string]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected object, got %T", pathOrRoot(path), actual)
		}
		for key, expVal := range exp {
			actVal, exists := act[key]
			if !exists {
				return fmt.Errorf("at %s: missing key %q", pathOrRoot(path), key)
			}
			if err := jsonSubset(expVal, actVal, path+"."+key); err != nil {
				return err
			}
		}
	case []interface{}:
		act, ok := actual.([]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected array, got %T", pathOrRoot(path), actual)
		}
		if len(exp) != len(act) {
			return fmt.Errorf("at %s: expected array length %d, got %d", pathOrRoot(path), len(exp), len(act))
		}
		for i := range exp {
			if err := jsonSubset(exp[i], act[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		if !reflect.DeepEqual(expected, actual) {
			return fmt.Errorf("at %s: expected %v (%T), got %v (%T)", pathOrRoot(path), expected, expected, actual, actual)
		}
	}
	return nil
}

func pathOrRoot(path string) string {
	if path == "" {
		return "$"
	}
	return "$" + path
}

// Expand replaces ${var} in the string based on scenario variables.
func (s *TestScenario) Expand(value string, skippedVars ...string) (result string, rerr error) {
	return os.Expand(value, func(name string) string {
		if contains(skippedVars, name) {
			return "$" + name
		}
		res, err := s.ResolveString(name)
		if err != nil {
			rerr = err
			return ""
		}
		return res
	}), rerr
}

func (s *TestScenario) ResolveString(name string) (string, error) {
	value, err := s.Resolve(name)
	if err != nil {
		return "", err
	}
	return ToString(value, name, JSONEncoding)
}

func ToString(value interface{}, name string, encoding Encoding) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case bool:
		if value {
			return "true", nil
		}
		return "false", nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", value), nil
	case float32, float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", value), "0"), "."), nil
	case nil:
		return "", nil
	case error:
		return "", fmt.Errorf("failed to evaluate selection: %s: %w", name, value)
	}

	bytes, err := encoding.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func (s *TestScenario) Resolve(name string) (interface{}, error) {
	pipes := strings.Split(name, "|")
	for i := range pipes {
		pipes[i] = strings.TrimSpace(pipes[i])
	}
	name = pipes[0]
	pipes = pipes[1:]

	// Handle quoted string literals like ${"value" | pipe_func}
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		return pipeline(pipes, name[1:len(name)-1], nil)
	}

	// Bridge Java ${response.body.xxx} → ${response.xxx}
	if name == "response.body" {
		name = "response"
	} else if strings.HasPrefix(name, "response.body.") {
		name = "response." + strings.TrimPrefix(name, "response.body.")
	} else if strings.HasPrefix(name, "response.body[") {
		name = "response" + strings.TrimPrefix(name, "response.body")
	}

	session := s.Session()
	if name == "response" {
		value, err := session.RespJSON()
		return pipeline(pipes, value, err)
	} else if strings.HasPrefix(name, "response.") || strings.HasPrefix(name, "response[") {
		selector := "." + name
		query, err := gojq.Parse(selector)
		if err != nil {
			return pipeline(pipes, nil, err)
		}

		j, err := session.RespJSON()
		if err != nil {
			return pipeline(pipes, nil, err)
		}

		j = map[string]interface{}{
			"response": j,
		}

		iter := query.Run(j)
		if next, found := iter.Next(); found {
			return pipeline(pipes, next, nil)
		}
		return pipeline(pipes, nil, fmt.Errorf("field ${%s} not found in json response:\n%s", name, string(session.RespBytes)))
	}

	parts := strings.Split(name, ".")
	name = parts[0]

	value, found := s.Variables[name]
	if !found {
		return pipeline(pipes, nil, fmt.Errorf("variable ${%s} not defined yet", name))
	}

	if len(parts) > 1 {
		var err error
		for _, part := range parts[1:] {
			value, err = s.SelectChild(value, part)
			if err != nil {
				return pipeline(pipes, nil, err)
			}
		}
		return pipeline(pipes, value, nil)
	}

	return pipeline(pipes, value, nil)
}

func (s *TestScenario) SelectChild(value any, path string) (any, error) {
	v := reflect.ValueOf(value)

	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(path)
		if v.Type().Key() != key.Type() {
			return nil, fmt.Errorf("cannot select map key %s from %s", path, v.Type())
		}
		v = v.MapIndex(key)
		if !v.IsValid() {
			return nil, fmt.Errorf("map key %s not found", path)
		}
	case reflect.Slice:
		index, err := strconv.Atoi(path)
		if err != nil {
			return nil, fmt.Errorf("cannot select slice index %s from %s", path, v.Type())
		}
		if index < 0 || index >= v.Len() {
			return nil, fmt.Errorf("slice index %s out of range", path)
		}
		v = v.Index(index)
	case reflect.Struct:
		f := v.FieldByName(path)
		if f.IsValid() {
			v = f
		} else {
			return nil, fmt.Errorf("struct field %s not found", path)
		}
	default:
		return nil, fmt.Errorf("can't navigate to '%s' on type of %s", path, v.Type())
	}
	return v.Interface(), nil
}

func pipeline(pipes []string, value any, err error) (any, error) {
	for _, pipe := range pipes {
		fn := PipeFunctions[pipe]
		if fn == nil {
			return nil, fmt.Errorf("unknown pipe: %s", pipe)
		}
		value, err = fn(value, err)
	}
	return value, err
}

var PipeFunctions = map[string]func(any, error) (any, error){
	"json": func(value any, err error) (any, error) {
		if err != nil {
			return value, err
		}
		buf := bytes.NewBuffer(nil)
		encoder := json.NewEncoder(buf)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(value)
		if err != nil {
			return value, err
		}
		return buf.String(), err
	},
	"json_escape": func(value any, err error) (any, error) {
		if err != nil {
			return value, err
		}
		data, err := json.Marshal(fmt.Sprintf("%v", value))
		if err != nil {
			return value, err
		}
		return strings.TrimSuffix(strings.TrimPrefix(string(data), `"`), `"`), nil
	},
	"string": func(value any, err error) (any, error) {
		if err != nil {
			return value, err
		}
		return fmt.Sprintf("%v", value), nil
	},
	"uuid_to_hex_string": func(value any, err error) (any, error) {
		if err != nil {
			return value, err
		}
		s := fmt.Sprintf("%v", value)
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("uuid_to_hex_string: invalid UUID %q: %w", s, err)
		}
		var sb strings.Builder
		for _, b := range id[:] {
			fmt.Fprintf(&sb, "\\x%02x", b)
		}
		return sb.String(), nil
	},
	"base64_to_hex_string": func(value any, err error) (any, error) {
		if err != nil {
			return value, err
		}
		s := fmt.Sprintf("%v", value)
		decoded, decodeErr := base64.StdEncoding.DecodeString(s)
		if decodeErr != nil {
			// Go gRPC test plumbing may already normalize UUID bytes to UUID strings.
			// Accept UUID input for Java feature compatibility.
			if id, parseErr := uuid.Parse(s); parseErr == nil {
				decoded = id[:]
			} else {
				return nil, fmt.Errorf("base64_to_hex_string: invalid base64 %q: %w", s, decodeErr)
			}
		}
		var sb strings.Builder
		for _, b := range decoded {
			fmt.Fprintf(&sb, "\\x%02x", b)
		}
		return sb.String(), nil
	},
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// TestSession holds the HTTP context for a user, like a browser.
type TestSession struct {
	TestUser          *TestUser
	Client            *http.Client
	Resp              *http.Response
	RespBytes         []byte
	respJSON          interface{}
	Header            http.Header
	EventStream       bool
	EventStreamEvents chan interface{}
}

// RespJSON returns the last HTTP response body as parsed JSON.
func (s *TestSession) RespJSON() (interface{}, error) {
	if s.respJSON == nil {
		if s.RespBytes == nil {
			return nil, fmt.Errorf("no response body")
		}
		err := json.Unmarshal(s.RespBytes, &s.respJSON)
		if err != nil {
			return nil, fmt.Errorf("error parsing response json: %w\njson was:\n%s", err, s.RespBytes)
		}
	}
	return s.respJSON, nil
}

func (s *TestSession) SetRespBytes(bytes []byte) {
	s.RespBytes = bytes
	s.respJSON = nil
}

// StepModules is the list of functions used to register steps with a godog.ScenarioContext.
var StepModules []func(ctx *godog.ScenarioContext, s *TestScenario)

func (suite *TestSuite) InitializeScenario(ctx *godog.ScenarioContext) {
	s := &TestScenario{
		Suite:     suite,
		Users:     map[string]*TestUser{},
		sessions:  map[string]*TestSession{},
		Variables: map[string]interface{}{},
	}

	for _, module := range StepModules {
		module(ctx, s)
	}
}
