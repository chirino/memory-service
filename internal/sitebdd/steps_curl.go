//go:build site_tests

package sitebdd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerCurlSteps registers all curl-related godog steps for the given scenario.
func registerCurlSteps(ctx *godog.ScenarioContext, s *SiteScenario) {
	ctx.Step(`^I execute curl command:$`, s.executeCurlCommand)
	ctx.Step(`^the response status should be (\d+)$`, s.responseStatusShouldBe)
	ctx.Step(`^the response should contain "([^"]*)"$`, s.responseShouldContain)
	ctx.Step(`^the response should not contain "([^"]*)"$`, s.responseShouldNotContain)
	ctx.Step(`^the response should be json with items array$`, s.responseShouldBeJsonWithItemsArray)
	ctx.Step(`^the response body should be json:$`, s.responseBodyShouldBeJson)
	ctx.Step(`^the response body should be text:$`, s.responseBodyShouldBeText)
	ctx.Step(`^set "([^"]*)" to the json response field "([^"]*)"$`, s.setContextVariableToJsonResponseField)
	ctx.Step(`^the response should match pattern "([^"]*)"$`, s.responseShouldMatchPattern)
}

// executeCurlCommand parses a bash block containing one or more curl commands,
// applies port and user substitutions, executes each as a Go HTTP request,
// and stores the last response.
func (s *SiteScenario) executeCurlCommand(block *godog.DocString) error {
	bash := block.Content

	// Execute any setup commands (e.g., echo "..." > /tmp/file)
	s.executeSetupCommands(bash)

	// Strip bash function definitions so their bodies are not parsed as curl commands.
	bash = stripFunctionDefs(bash)

	// Replace $(get-token ...) substitutions with real RS256 JWTs signed by the mock.
	bash = s.resolveGetToken(bash)

	// Apply port substitution: docs use localhost:9090; we use the allocated port
	bash = strings.ReplaceAll(bash, "localhost:9090", fmt.Sprintf("localhost:%d", s.CheckpointPort))

	// Some forking/sharing docs call the memory service directly at port 8082 (Docker dev convention).
	// Substitute to reach our in-process memory service.
	memServiceHost := strings.TrimPrefix(s.MemServiceURL, "http://")
	bash = strings.ReplaceAll(bash, "localhost:8082", memServiceHost)

	// Apply user isolation substitutions
	bash = s.rewriteUsers(bash)

	// Expand context variables: ${VAR}
	bash = s.expandContextVars(bash)

	// Parse and execute all curl commands in the block
	requests, err := parseCurlBlock(bash)
	if err != nil {
		return fmt.Errorf("parse curl block: %w", err)
	}

	if len(requests) == 0 {
		fmt.Println("  [curl] No curl commands found in block, skipping")
		return nil
	}

	client := &http.Client{Timeout: 30 * time.Second}

	for i, cr := range requests {
		fmt.Printf("  [curl %d/%d] %s %s\n", i+1, len(requests), cr.Method, cr.URL)
		if err := s.claimRequestUUIDs(cr); err != nil {
			return fmt.Errorf("uuid registry conflict on curl %d: %w", i+1, err)
		}

		req, err := cr.toHTTPRequest()
		if err != nil {
			return fmt.Errorf("build request %d: %w", i+1, err)
		}

		// Retry on transient errors (startup transients)
		var respBody string
		var statusCode int
		for attempt := 1; attempt <= 5; attempt++ {
			resp, err := client.Do(req.Clone(context.Background()))
			if err != nil {
				if attempt == 5 {
					return fmt.Errorf("execute curl %d: %w", i+1, err)
				}
				fmt.Printf("    [curl] attempt %d error: %v, retrying in 3s\n", attempt, err)
				time.Sleep(3 * time.Second)
				continue
			}

			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			respBody = string(bodyBytes)
			statusCode = resp.StatusCode

			if statusCode != 404 && statusCode != 500 && statusCode != 503 {
				break
			}
			if attempt < 5 {
				fmt.Printf("    [curl] got %d (attempt %d/5), retrying in 3s\n", statusCode, attempt)
				time.Sleep(3 * time.Second)
				// Rebuild request (body already consumed)
				req, _ = cr.toHTTPRequest()
			}
		}

		fmt.Printf("    [curl] status=%d body-len=%d\n", statusCode, len(respBody))
		s.LastStatusCode = statusCode
		s.LastRespBody = s.normalizeUsers(respBody)
		crCopy := cr
		s.lastCurlReq = &crCopy
	}

	return nil
}

func (s *SiteScenario) claimRequestUUIDs(cr curlRequest) error {
	values := []string{cr.URL, cr.Body}
	values = append(values, cr.Headers...)
	values = append(values, cr.FormFields...)
	uuids := extractUUIDs(values...)
	if len(uuids) == 0 {
		return nil
	}
	sort.Strings(uuids)
	if err := globalScenarioUUIDRegistry.ClaimScenarioUUIDs(s.scenarioKey(), uuids); err != nil {
		return fmt.Errorf("%w (current scenario=%q, command=%q)", err, s.scenarioKey(), cr.Method+" "+cr.URL)
	}
	return nil
}

// rewriteUsers replaces canonical user names with scenario-isolated names.
// Applied in Authorization headers, "userId" JSON fields, and URL path segments.
func (s *SiteScenario) rewriteUsers(text string) string {
	for _, base := range []string{"alice", "bob", "charlie"} {
		isolated := s.isolatedUser(base)
		// Bearer token in Authorization header
		text = strings.ReplaceAll(text, `Bearer `+base, `Bearer `+isolated)
		// "userId": "alice" in JSON payloads
		text = strings.ReplaceAll(text, `"userId": "`+base+`"`, `"userId": "`+isolated+`"`)
		text = strings.ReplaceAll(text, `"userId":"`+base+`"`, `"userId":"`+isolated+`"`)
		// URL path segments: /alice/ → /alice-<uid>/
		text = strings.ReplaceAll(text, "/"+base+"/", "/"+isolated+"/")
	}
	return text
}

// expandContextVars replaces ${VAR} with values from ContextVars.
func (s *SiteScenario) expandContextVars(text string) string {
	for k, v := range s.ContextVars {
		text = strings.ReplaceAll(text, "${"+k+"}", fmt.Sprintf("%v", v))
	}
	return text
}

// executeSetupCommands runs non-curl shell setup lines (e.g., echo ... > /tmp/file).
// For echo-to-file commands, the file is created so that -F @file curl uploads work.
func (s *SiteScenario) executeSetupCommands(bash string) {
	reEchoToFile := regexp.MustCompile(`^echo\s+"([^"]+)"\s*>\s*(\S+)$`)
	for _, line := range strings.Split(bash, "\n") {
		t := strings.TrimSpace(line)
		m := reEchoToFile.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		content, path := m[1], m[2]
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Printf("  [setup] mkdir %s: %v\n", filepath.Dir(path), err)
			continue
		}
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			fmt.Printf("  [setup] write %s: %v\n", path, err)
		} else {
			fmt.Printf("  [setup] wrote %s (%d bytes)\n", path, len(content)+1)
		}
	}
}

// --- Assertions ---

func (s *SiteScenario) responseStatusShouldBe(expected int) error {
	if s.LastStatusCode != expected {
		return fmt.Errorf("expected status %d, got %d\nbody: %s", expected, s.LastStatusCode, s.LastRespBody)
	}
	return nil
}

func (s *SiteScenario) responseShouldContain(expected string) error {
	if !strings.Contains(s.LastRespBody, expected) {
		return fmt.Errorf("expected response to contain %q\ngot: %s", expected, s.LastRespBody)
	}
	return nil
}

func (s *SiteScenario) responseShouldNotContain(unexpected string) error {
	if strings.Contains(s.LastRespBody, unexpected) {
		return fmt.Errorf("expected response NOT to contain %q\ngot: %s", unexpected, s.LastRespBody)
	}
	return nil
}

func (s *SiteScenario) responseShouldBeJsonWithItemsArray() error {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s.LastRespBody), &parsed); err != nil {
		return fmt.Errorf("response is not valid JSON: %w\nbody: %s", err, s.LastRespBody)
	}
	dataVal, ok := parsed["data"]
	if !ok {
		return fmt.Errorf("expected JSON with 'data' field\ngot: %s", s.LastRespBody)
	}
	if _, ok := dataVal.([]any); !ok {
		return fmt.Errorf("expected 'data' to be an array\ngot: %s", s.LastRespBody)
	}
	return nil
}

func (s *SiteScenario) responseBodyShouldBeJson(expected *godog.DocString) error {
	exp := expected.Content
	// Expand %{response.body.field} placeholders
	exp = s.renderTemplate(exp)

	// Parse both as generic JSON and compare with subset semantics
	var actualParsed, expectedParsed any
	if err := json.Unmarshal([]byte(s.LastRespBody), &actualParsed); err != nil {
		return fmt.Errorf("actual is not valid JSON: %w\nbody: %s", err, s.LastRespBody)
	}
	if err := json.Unmarshal([]byte(exp), &expectedParsed); err != nil {
		return fmt.Errorf("expected is not valid JSON: %w\nexpected: %s", err, exp)
	}
	lastErr := jsonSubset(expectedParsed, actualParsed, "")
	if lastErr == nil {
		return nil
	}
	if s.lastCurlReq == nil {
		return lastErr
	}
	lastReq, err := s.lastCurlReq.toHTTPRequest()
	if err != nil {
		return lastErr
	}
	if strings.ToUpper(lastReq.Method) != http.MethodGet {
		return lastErr
	}

	// Some checkpoint writes are eventually consistent; for GETs, re-read a few
	// times before failing strict JSON assertions.
	for attempt := 1; attempt <= 4; attempt++ {
		time.Sleep(750 * time.Millisecond)
		if err := s.replayLastCurlRequest(); err != nil {
			return fmt.Errorf("%v (replay failed: %w)", lastErr, err)
		}
		if err := json.Unmarshal([]byte(s.LastRespBody), &actualParsed); err != nil {
			return fmt.Errorf("actual is not valid JSON after replay: %w\nbody: %s", err, s.LastRespBody)
		}
		if err := jsonSubset(expectedParsed, actualParsed, ""); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (s *SiteScenario) replayLastCurlRequest() error {
	if s.lastCurlReq == nil {
		return fmt.Errorf("no previous curl request to replay")
	}

	req, err := s.lastCurlReq.toHTTPRequest()
	if err != nil {
		return fmt.Errorf("build replay request: %w", err)
	}
	if strings.ToUpper(req.Method) != http.MethodGet {
		return fmt.Errorf("last request method %s is not replay-safe", req.Method)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute replay request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	s.LastStatusCode = resp.StatusCode
	s.LastRespBody = s.normalizeUsers(string(bodyBytes))
	return nil
}

func (s *SiteScenario) responseBodyShouldBeText(expected *godog.DocString) error {
	exp := normalizeText(strings.TrimSpace(expected.Content))
	act := normalizeText(strings.TrimSpace(s.LastRespBody))
	if !strings.Contains(act, exp) {
		return fmt.Errorf("expected response to contain:\n%s\n\ngot:\n%s", exp, act)
	}
	return nil
}

func (s *SiteScenario) setContextVariableToJsonResponseField(varName, path string) error {
	val, err := jsonGet(s.LastRespBody, path)
	if err != nil {
		return fmt.Errorf("field %q not found in JSON response: %w\nbody: %s", path, err, s.LastRespBody)
	}
	s.ContextVars[varName] = val
	return nil
}

func (s *SiteScenario) responseShouldMatchPattern(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	if !re.MatchString(s.LastRespBody) {
		return fmt.Errorf("response did not match pattern %q\ngot: %s", pattern, s.LastRespBody)
	}
	return nil
}

// renderTemplate replaces %{response.body.field} and %{context.var} placeholders.
func (s *SiteScenario) renderTemplate(tmpl string) string {
	re := regexp.MustCompile(`%\{([^}]+)}`)
	return re.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := match[2 : len(match)-1]

		if expr == "response.body" {
			return s.LastRespBody
		}
		if strings.HasPrefix(expr, "response.body.") {
			path := strings.TrimPrefix(expr, "response.body.")
			val, err := jsonGet(s.LastRespBody, path)
			if err != nil {
				return match // leave unexpanded
			}
			return fmt.Sprintf("%v", val)
		}
		if strings.HasPrefix(expr, "context.") {
			key := strings.TrimPrefix(expr, "context.")
			if v, ok := s.ContextVars[key]; ok {
				return fmt.Sprintf("%v", v)
			}
		}
		if v, ok := s.ContextVars[expr]; ok {
			return fmt.Sprintf("%v", v)
		}
		return match
	})
}

// jsonGet extracts a value from a JSON string using a dot-separated path.
// Supports array index syntax: "data[0].id", "data[1].createdAt".
func jsonGet(jsonStr, path string) (any, error) {
	var parsed any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, err
	}
	return jsonNavigate(parsed, strings.Split(path, "."))
}

// jsonNavigate walks a parsed JSON value using dot-split path segments.
// Each segment may have an array index suffix: "data[0]".
func jsonNavigate(current any, parts []string) (any, error) {
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, idx, hasIdx := parseJSONPathSegment(part)
		if key != "" {
			m, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("not an object at key %q", key)
			}
			val, ok := m[key]
			if !ok {
				return nil, fmt.Errorf("key %q not found", key)
			}
			current = val
		}
		if hasIdx {
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array at index %d, got %T", idx, current)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of bounds (len=%d)", idx, len(arr))
			}
			current = arr[idx]
		}
	}
	return current, nil
}

// parseJSONPathSegment splits a segment like "data[0]" into key="data", idx=0, hasIdx=true.
// A plain segment like "id" returns key="id", idx=0, hasIdx=false.
func parseJSONPathSegment(seg string) (key string, idx int, hasIdx bool) {
	i := strings.IndexByte(seg, '[')
	if i < 0 || !strings.HasSuffix(seg, "]") {
		return seg, 0, false
	}
	key = seg[:i]
	idxStr := seg[i+1 : len(seg)-1]
	n, err := strconv.Atoi(idxStr)
	if err != nil {
		return seg, 0, false // treat as plain key if not a valid index
	}
	return key, n, true
}

// normalizeText collapses whitespace and normalizes Unicode punctuation.
func normalizeText(text string) string {
	text = strings.NewReplacer(
		"\u2018", "'", "\u2019", "'",
		"\u201C", `"`, "\u201D", `"`,
		"\u2014", "-", "\u2013", "-",
		"\u00A0", " ",
	).Replace(text)
	// Collapse runs of whitespace
	re := regexp.MustCompile(`\s+`)
	return re.ReplaceAllString(text, " ")
}

// jsonSubset checks that every field in expected exists in actual with a matching value.
func jsonSubset(expected, actual any, path string) error {
	if expected == nil {
		if actual != nil {
			return fmt.Errorf("at %s: expected null, got %v", pathOrRoot(path), actual)
		}
		return nil
	}
	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("at %s: expected object, got %T", pathOrRoot(path), actual)
		}
		for k, ev := range exp {
			av, exists := act[k]
			if !exists {
				return fmt.Errorf("at %s: missing key %q", pathOrRoot(path), k)
			}
			if err := jsonSubset(ev, av, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		act, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("at %s: expected array, got %T", pathOrRoot(path), actual)
		}
		if len(exp) != len(act) {
			return fmt.Errorf("at %s: array length %d != %d", pathOrRoot(path), len(exp), len(act))
		}
		for i := range exp {
			if err := jsonSubset(exp[i], act[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		if fmt.Sprintf("%v", expected) != fmt.Sprintf("%v", actual) {
			return fmt.Errorf("at %s: expected %v, got %v", pathOrRoot(path), expected, actual)
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

// --- Curl parser ---

type curlRequest struct {
	Method     string
	URL        string
	Headers    []string
	Body       string
	FormFields []string // -F "name=@filepath" or "name=value"
}

func (cr *curlRequest) toHTTPRequest() (*http.Request, error) {
	method := cr.Method
	if method == "" {
		method = "GET"
		if cr.Body != "" || len(cr.FormFields) > 0 {
			method = "POST"
		}
	}

	var bodyReader io.Reader
	var contentType string

	if len(cr.FormFields) > 0 {
		// Build a multipart/form-data body from -F fields.
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, field := range cr.FormFields {
			name, val, _ := strings.Cut(field, "=")
			if strings.HasPrefix(val, "@") {
				// File upload: "name=@/path/to/file"
				filePath := val[1:]
				data, err := os.ReadFile(filePath)
				if err != nil {
					return nil, fmt.Errorf("form field %q: read file %s: %w", name, filePath, err)
				}
				// Detect MIME type from extension; fall back to application/octet-stream.
				// Strip parameters (e.g., charset=utf-8) — the server stores the base type.
				fileContentType := mime.TypeByExtension(filepath.Ext(filePath))
				if fileContentType == "" {
					fileContentType = "application/octet-stream"
				} else if mt, _, err := mime.ParseMediaType(fileContentType); err == nil {
					fileContentType = mt
				}
				// Use CreatePart to set the correct Content-Type (CreateFormFile always uses octet-stream).
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, name, filepath.Base(filePath)))
				h.Set("Content-Type", fileContentType)
				fw, err := w.CreatePart(h)
				if err != nil {
					return nil, fmt.Errorf("form field %q: create form file: %w", name, err)
				}
				if _, err = fw.Write(data); err != nil {
					return nil, fmt.Errorf("form field %q: write data: %w", name, err)
				}
			} else {
				if err := w.WriteField(name, val); err != nil {
					return nil, fmt.Errorf("form field %q: write field: %w", name, err)
				}
			}
		}
		_ = w.Close()
		bodyReader = &buf
		contentType = w.FormDataContentType()
	} else if cr.Body != "" {
		bodyReader = strings.NewReader(cr.Body)
	}

	req, err := http.NewRequest(method, cr.URL, bodyReader)
	if err != nil {
		return nil, err
	}

	for _, h := range cr.Headers {
		parts := strings.SplitN(h, ": ", 2)
		if len(parts) == 2 {
			req.Header.Set(parts[0], parts[1])
		}
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if req.Header.Get("Content-Type") == "" && cr.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

// parseCurlBlock parses a bash block into individual curl requests.
// Handles multi-line single-quoted bodies (e.g. -d '{\n  "key": "value"\n}').
func parseCurlBlock(bash string) ([]curlRequest, error) {
	// Join line-continuation pairs (\<newline> → space) so flag values
	// spread across lines (e.g. -H "..." \<newline>  -d "...") merge.
	bash = strings.ReplaceAll(bash, "\\\n", " ")

	var requests []curlRequest
	lines := strings.Split(bash, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "curl") {
			// Accumulate continuation lines until single/double quotes are balanced.
			// This handles multi-line -d '{\n  "key": "value"\n}' bodies.
			var buf strings.Builder
			buf.WriteString(line)
			for !shellQuoteBalanced(buf.String()) && i+1 < len(lines) {
				i++
				buf.WriteByte('\n')
				buf.WriteString(lines[i])
			}
			cmd := buf.String()
			// Trim trailing pipeline (e.g. "| jq") which is not part of the curl command.
			cmd = trimTrailingPipe(cmd)

			tokens := shellTokenize(strings.TrimSpace(cmd))
			cr, err := parseCurlTokens(tokens)
			if err != nil {
				return nil, err
			}
			if cr.URL != "" {
				requests = append(requests, cr)
			}
		}
		i++
	}
	return requests, nil
}

// shellQuoteBalanced reports whether all single and double quotes in s are balanced.
func shellQuoteBalanced(s string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else if c == '\\' {
				i++ // skip escaped char
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		}
	}
	return !inSingle && !inDouble
}

// trimTrailingPipe removes a trailing unquoted pipe and everything after it
// (e.g. "curl ... | jq" → "curl ...").
func trimTrailingPipe(cmd string) string {
	inSingle := false
	inDouble := false
	lastPipe := -1
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else if c == '\\' {
				i++
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '|':
			lastPipe = i
		}
	}
	if lastPipe >= 0 {
		return strings.TrimSpace(cmd[:lastPipe])
	}
	return cmd
}

// shellTokenize splits a shell command string into tokens, respecting quoting.
func shellTokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else if c == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// parseCurlTokens converts a token slice (starting with "curl") into a curlRequest.
func parseCurlTokens(tokens []string) (curlRequest, error) {
	// Pre-expand combined short flags (e.g. -NsSfX → ["-N","-s","-S","-f","-X"])
	// so the main loop can handle each flag individually.
	tokens = expandCombinedFlags(tokens)

	var cr curlRequest
	i := 0
	for i < len(tokens) {
		tok := tokens[i]
		switch tok {
		case "curl":
			// skip
		case "-X", "--request":
			i++
			if i < len(tokens) {
				cr.Method = tokens[i]
			}
		case "-H", "--header":
			i++
			if i < len(tokens) {
				cr.Headers = append(cr.Headers, tokens[i])
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			i++
			if i < len(tokens) {
				cr.Body = tokens[i]
			}
		case "-F", "--form":
			i++
			if i < len(tokens) {
				cr.FormFields = append(cr.FormFields, tokens[i])
			}
		case "-s", "--silent", "-v", "--verbose", "-L", "--location",
			"-k", "--insecure", "-f", "--fail", "-g", "--globoff",
			"--compressed", "-i", "--include", "-o", "--output",
			"--no-buffer", "-N", "-S":
			// boolean flags — no argument
		case "--connect-timeout", "--max-time", "-m", "--retry",
			"--retry-delay", "-u", "--user", "--proxy", "-x",
			"--cacert", "--cert", "--key", "-A", "--user-agent",
			"-e", "--referer", "-b", "--cookie", "-c", "--cookie-jar":
			i++ // skip the argument too
		default:
			if !strings.HasPrefix(tok, "-") && cr.URL == "" {
				cr.URL = tok
			}
			// ignore unrecognised flags
		}
		i++
	}
	return cr, nil
}

// reGetToken matches bash command-substitution calls to the get-token helper function.
// Supported forms: $(get-token)  and  $(get-token <username> <password>)
var reGetToken = regexp.MustCompile(`\$\(get-token(?:\s+(\w+)\s+\w+)?\)`)

// resolveGetToken replaces $(get-token [user pass]) occurrences in bash with a
// real RS256 JWT issued by the mock server for the scenario-isolated user name.
// When no username argument is given the docs convention of "bob" is assumed.
func (s *SiteScenario) resolveGetToken(bash string) string {
	return reGetToken.ReplaceAllStringFunc(bash, func(match string) string {
		sub := reGetToken.FindStringSubmatch(match)
		username := "bob"
		if len(sub) > 1 && sub[1] != "" {
			username = sub[1]
		}
		isolated := s.isolatedUser(username)
		token, err := s.Mock.IssueToken(isolated)
		if err != nil {
			fmt.Printf("[resolveGetToken] error issuing token for %s: %v\n", isolated, err)
			return match // leave unexpanded so the test fails with a clearer error
		}
		return token
	})
}

// stripFunctionDefs removes bash function definition blocks (function name() { ... })
// from bash text so that curl commands inside function bodies are not parsed and
// executed as test steps.
func stripFunctionDefs(bash string) string {
	var out strings.Builder
	depth := 0
	for _, line := range strings.Split(bash, "\n") {
		trimmed := strings.TrimSpace(line)
		if depth == 0 {
			// Detect "function name() {" style definitions.
			if strings.HasPrefix(trimmed, "function ") && strings.Contains(trimmed, "()") {
				depth = strings.Count(line, "{") - strings.Count(line, "}")
				if depth < 1 {
					depth = 1
				}
				continue // skip the function definition line itself
			}
			out.WriteString(line + "\n")
		} else {
			// Track brace depth to find the closing }.
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				depth = 0
			}
			// Skip all lines inside the function body.
		}
	}
	return out.String()
}

// expandCombinedFlags splits combined short-flag tokens like "-NsSfX" into
// individual flags ["-N", "-s", "-S", "-f", "-X"]. Tokens that begin with "--"
// or are already single flags ("-X") are returned unchanged.
//
// Special case: if the last character of a combined group is a flag that
// consumes its value inline (e.g. "-XPOST"), the value is split off as its
// own token so the main parser receives ["-X", "POST"].
func expandCombinedFlags(tokens []string) []string {
	// Short flags that consume the next argument.
	takesArg := map[byte]bool{'H': true, 'X': true, 'd': true}

	var out []string
	for _, tok := range tokens {
		if len(tok) <= 2 || tok[0] != '-' || tok[1] == '-' {
			out = append(out, tok)
			continue
		}
		// Combined short flags: "-NsSfX", "-NsSfXPOST", etc.
		chars := tok[1:] // e.g. "NsSfX" or "NsSfXPOST"
		for ci := 0; ci < len(chars); ci++ {
			c := chars[ci]
			flag := "-" + string(c)
			if takesArg[c] {
				// Remaining chars after this flag letter are the inline value, if any.
				inline := chars[ci+1:]
				if inline != "" {
					// e.g. -XPOST → ["-X", "POST"]
					out = append(out, flag, inline)
				} else {
					// Value is the next separate token; let the main loop handle it.
					out = append(out, flag)
				}
				break // everything after arg-taking flag is consumed
			}
			out = append(out, flag)
		}
	}
	return out
}
