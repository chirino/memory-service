//go:build site_tests

package sitebdd

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// These tests lint documentation sources and checkpoint metadata directly.
// They need neither Docker nor a site build, so run them with -run to avoid
// starting TestSiteDocs:
//
//	go test -tags='site_tests sqlite_fts5' ./internal/sitebdd/ -run 'TestDocs' -count=1

func TestDocsPythonLockfilesPortable(t *testing.T) {
	root := mustProjectRoot(t)
	cmd := exec.Command("git", "ls-files", "-z", "--", "python/**/uv.lock", "python/uv.lock")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list Python lockfiles: %v", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	if len(paths) == 0 || paths[0] == "" {
		t.Fatal("no tracked Python lockfiles found")
	}
	locations := regexp.MustCompile(`\b(?:registry|path|directory|editable|virtual)\s*=\s*("(?:[^"\\]|\\.)*"|'[^']*')`)
	windowsAbsolute := regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			for _, match := range locations.FindAllStringSubmatch(readFile(t, filepath.Join(root, path)), -1) {
				location := match[1]
				if strings.HasPrefix(location, `"`) {
					decoded, err := strconv.Unquote(location)
					if err != nil {
						t.Fatalf("decode lockfile location: %v", err)
					}
					location = decoded
				} else {
					location = location[1 : len(location)-1]
				}
				if strings.HasPrefix(location, "https://") || strings.HasPrefix(location, "http://") {
					continue
				}
				if filepath.IsAbs(location) || windowsAbsolute.MatchString(location) || strings.HasPrefix(location, "file://") {
					t.Errorf("local lockfile location %q must be relative so checkpoints work in another checkout", location)
				}
			}
		})
	}
}

// TestDocsCurlUUIDsUniqueAcrossScenarios fails when two different doc
// scenarios send the same UUID in a curl request. At runtime the shared
// scenarioUUIDRegistry rejects the second claimant, so this catches the
// conflict without running the suite. Keys match SiteScenario.scenarioKey.
func TestDocsCurlUUIDsUniqueAcrossScenarios(t *testing.T) {
	projectRoot := mustProjectRoot(t)
	pagesDir := filepath.Join(projectRoot, "site", "src", "pages")
	docsDir := filepath.Join(pagesDir, "docs")

	type claim struct {
		key      string
		location string
	}
	owners := map[string]claim{}
	var conflicts []string
	scenarioBlocks := 0

	for _, path := range mdxFiles(t, docsDir) {
		src := readFile(t, path)
		rel := relPath(projectRoot, path)
		sourceFile := routeForPage(pagesDir, path)

		// TestScenario.astro merges blocks that share (checkpoint, sourceFile)
		// and keeps the first description.
		descriptions := map[string]string{}
		for _, block := range findTestScenarios(src) {
			scenarioBlocks++
			checkpoint := block.attrs["checkpoint"]
			if _, seen := descriptions[checkpoint]; !seen {
				descriptions[checkpoint] = block.attrs["description"]
			}
			key := scenarioName(ScenarioData{
				Checkpoint:  checkpoint,
				SourceFile:  sourceFile,
				Description: descriptions[checkpoint],
			})
			for _, fence := range bashFences(block.body) {
				if !containsCurl(fence.code) {
					continue
				}
				requests, err := parseCurlBlock(stripFunctionDefs(fence.code))
				if err != nil {
					t.Errorf("%s:%d: parse curl block: %v", rel, block.line+fence.line, err)
					continue
				}
				location := fmt.Sprintf("%s:%d", rel, block.line+fence.line)
				for _, cr := range requests {
					values := append([]string{cr.URL, cr.Body}, cr.Headers...)
					values = append(values, cr.FormFields...)
					for _, uuid := range extractUUIDs(values...) {
						uuid = strings.ToLower(uuid)
						owner, exists := owners[uuid]
						if !exists {
							owners[uuid] = claim{key: key, location: location}
							continue
						}
						if owner.key != key {
							conflicts = append(conflicts, fmt.Sprintf(
								"uuid %s used by scenario %q (%s) and scenario %q (%s)",
								uuid, owner.key, owner.location, key, location))
						}
					}
				}
			}
		}
	}

	if scenarioBlocks == 0 {
		t.Fatalf("no <TestScenario> blocks found under %s", docsDir)
	}
	t.Logf("checked %d UUID(s) in %d <TestScenario> block(s)", len(owners), scenarioBlocks)
	for _, c := range conflicts {
		t.Error(c)
	}
	if len(conflicts) > 0 {
		t.Log("Each doc scenario must use its own fresh UUIDs (for example from uuidgen).")
	}
}

// TestDocsCodeFromFileReferences checks that every <CodeFromFile file="...">
// points at a git-tracked file and that any lines="N-M" range fits in it.
// Untracked or ignored files may exist locally but break CI site builds.
func TestDocsCodeFromFileReferences(t *testing.T) {
	projectRoot := mustProjectRoot(t)
	pagesDir := filepath.Join(projectRoot, "site", "src", "pages")

	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	tracked := map[string]struct{}{}
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) > 0 {
			tracked[string(name)] = struct{}{}
		}
	}

	linesRe := regexp.MustCompile(`^(\d+)-(\d+)$`)
	checked := 0
	for _, path := range mdxFiles(t, pagesDir) {
		src := readFile(t, path)
		rel := relPath(projectRoot, path)
		for _, tag := range findTags(src, "CodeFromFile") {
			checked++
			where := fmt.Sprintf("%s:%d", rel, tag.line)
			file, ok := tag.attrs["file"]
			if !ok || file == "" {
				t.Errorf("%s: CodeFromFile has no literal file=\"...\" attribute", where)
				continue
			}
			if _, ok := tracked[file]; !ok {
				t.Errorf("%s: CodeFromFile file %q is not tracked by git", where, file)
				continue
			}
			lines, ok := tag.attrs["lines"]
			if !ok {
				continue
			}
			m := linesRe.FindStringSubmatch(lines)
			if m == nil {
				t.Errorf("%s: CodeFromFile lines=%q is not of the form N-M", where, lines)
				continue
			}
			start, _ := strconv.Atoi(m[1])
			end, _ := strconv.Atoi(m[2])
			// CodeFromFile.astro splits on "\n", so a trailing newline adds an
			// empty last element; count the same way.
			total := len(strings.Split(readFile(t, filepath.Join(projectRoot, file)), "\n"))
			if start < 1 || end < start || end > total {
				t.Errorf("%s: CodeFromFile lines=%q is outside %s (%d lines)", where, lines, file, total)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no <CodeFromFile> tags found under %s", pagesDir)
	}
}

// TestDocsMemoriesIdempotencyText checks that the memories documentation covers
// PutMemory idempotency, revision preconditions, conflict codes, and retention window.
func TestDocsMemoriesIdempotencyText(t *testing.T) {
	projectRoot := mustProjectRoot(t)
	memoriesDocPath := filepath.Join(projectRoot, "site", "src", "pages", "docs", "concepts", "memories.md")
	content := readFile(t, memoriesDocPath)

	requiredSnippets := []string{
		"revision_id",
		"expected_revision_id",
		"revision: 1",
		"201 Created",
		"200 OK",
		"memory_revision_already_exists",
		"revision_already_exists",
		"expected_active_revision_id_mismatch",
		"Idempotency Window",
		"superseded-revision eviction",
		// The 409 for a reused revision_id carries the ID under details.revisionId.
		"details.revisionId",
		// Tombstone retention is a separate setting from superseded-row eviction.
		"MEMORY_SERVICE_EPISODIC_TOMBSTONE_RETENTION",
		// Known limitation: durable event delivery depends on the outbox.
		"MEMORY_SERVICE_OUTBOX_ENABLED",
		// gRPC statuses for the same conflicts and validation failures.
		"ABORTED",
		"INVALID_ARGUMENT",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(content, snippet) {
			t.Errorf("memories.md missing required text snippet %q", snippet)
		}
	}

	// Text that contradicts the implemented behavior must not return. A numeric
	// revision conflict has no `code`; a hard-deleted superseded row is treated as a
	// new write rather than 404; superseded rows are evicted after re-indexing, not
	// after a configurable window; and the reused-revision_id 409 body also carries
	// `error` and `details.revisionId`.
	forbiddenSnippets := []string{
		"`code`: `memory revision conflict`",
		"pruned or archived-and-swept revisions return `404",
		"administrator-configurable retention window",
		"contains only the error `code` and the caller's `revision_id`",
	}
	for _, snippet := range forbiddenSnippets {
		if strings.Contains(content, snippet) {
			t.Errorf("memories.md contains text that contradicts the implementation: %q", snippet)
		}
	}
}

// --- MDX scanning helpers ---

type mdxTag struct {
	attrs map[string]string // literal "..." / '...' values only
	line  int               // 1-based line of the opening tag
	end   int               // byte offset just after the opening tag
}

type mdxTestScenario struct {
	attrs map[string]string
	line  int // 1-based line where body starts
	body  string
}

type mdxFence struct {
	code string
	line int // 0-based line offset of the fence opener within the searched text
}

func mustProjectRoot(t *testing.T) string {
	t.Helper()
	root, err := findProjectRoot()
	if err != nil {
		t.Fatalf("find project root: %v", err)
	}
	return root
}

func mdxFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".mdx") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	sort.Strings(files)
	return files
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// routeForPage returns the Astro.url.pathname for an MDX page, matching the
// sourceFile values TestScenario.astro writes (build.format: "directory").
func routeForPage(pagesDir, path string) string {
	rel := filepath.ToSlash(strings.TrimSuffix(relPath(pagesDir, path), ".mdx"))
	if rel == "index" {
		return "/"
	}
	rel = strings.TrimSuffix(rel, "/index")
	return "/" + rel + "/"
}

// findTestScenarios returns each <TestScenario ...>...</TestScenario> block.
func findTestScenarios(src string) []mdxTestScenario {
	var out []mdxTestScenario
	for _, tag := range findTags(src, "TestScenario") {
		rest := src[tag.end:]
		closeIdx := strings.Index(rest, "</TestScenario>")
		if closeIdx < 0 {
			closeIdx = len(rest)
		}
		out = append(out, mdxTestScenario{
			attrs: tag.attrs,
			line:  strings.Count(src[:tag.end], "\n") + 1,
			body:  rest[:closeIdx],
		})
	}
	return out
}

// bashFences returns the contents of ```bash fences in text. TestScenario.astro
// extracts every bash block in the scenario, not only those inside CurlTest.
func bashFences(text string) []mdxFence {
	var out []mdxFence
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		opener := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(opener, "```") {
			continue
		}
		info := strings.Fields(strings.TrimPrefix(opener, "```"))
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
				break
			}
			body = append(body, lines[j])
		}
		if len(info) > 0 && info[0] == "bash" {
			out = append(out, mdxFence{code: strings.Join(body, "\n"), line: i})
		}
		i = j
	}
	return out
}

// findTags scans src for opening <name ...> JSX tags and returns their literal
// string attributes. Expression attributes ({...}, including template literals)
// are skipped but parsed so that '>' inside them does not end the tag.
func findTags(src, name string) []mdxTag {
	var out []mdxTag
	open := "<" + name
	for offset := 0; ; {
		idx := strings.Index(src[offset:], open)
		if idx < 0 {
			return out
		}
		start := offset + idx
		pos := start + len(open)
		if pos < len(src) && !isSpace(src[pos]) && src[pos] != '>' && src[pos] != '/' {
			offset = pos // e.g. <TestScenarioFoo
			continue
		}
		attrs, end := parseTagAttrs(src, pos)
		out = append(out, mdxTag{
			attrs: attrs,
			line:  strings.Count(src[:start], "\n") + 1,
			end:   end,
		})
		offset = end
	}
}

func parseTagAttrs(src string, pos int) (map[string]string, int) {
	attrs := map[string]string{}
	for pos < len(src) {
		for pos < len(src) && isSpace(src[pos]) {
			pos++
		}
		if pos >= len(src) {
			break
		}
		if src[pos] == '>' {
			return attrs, pos + 1
		}
		if strings.HasPrefix(src[pos:], "/>") {
			return attrs, pos + 2
		}
		nameStart := pos
		for pos < len(src) && !isSpace(src[pos]) && src[pos] != '=' && src[pos] != '>' && src[pos] != '/' {
			pos++
		}
		attrName := src[nameStart:pos]
		if pos >= len(src) || src[pos] != '=' {
			if attrName == "" {
				pos++ // stray character; keep scanning
			}
			continue
		}
		pos++ // '='
		if pos >= len(src) {
			break
		}
		switch q := src[pos]; q {
		case '"', '\'':
			endIdx := strings.IndexByte(src[pos+1:], q)
			if endIdx < 0 {
				return attrs, len(src)
			}
			attrs[attrName] = src[pos+1 : pos+1+endIdx]
			pos += endIdx + 2
		case '{':
			pos = skipJSXExpression(src, pos)
		default:
			for pos < len(src) && !isSpace(src[pos]) && src[pos] != '>' {
				pos++
			}
		}
	}
	return attrs, len(src)
}

// skipJSXExpression returns the offset just past the {...} expression that
// starts at pos, honoring nested braces and string/template literals.
func skipJSXExpression(src string, pos int) int {
	depth := 0
	for pos < len(src) {
		switch c := src[pos]; c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return pos + 1
			}
		case '"', '\'', '`':
			pos++
			for pos < len(src) && src[pos] != c {
				if src[pos] == '\\' {
					pos++
				}
				pos++
			}
		}
		pos++
	}
	return pos
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
