package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type curlCapture struct {
	CaptureID   string `json:"captureId"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
}

func main() {
	projectRootFlag := flag.String("project-root", ".", "Project root path")
	apply := flag.Bool("apply", false, "Write changes in place")
	flag.Parse()

	projectRoot, err := filepath.Abs(*projectRootFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve project root: %v\n", err)
		os.Exit(1)
	}

	captures, err := loadCaptures(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load captures: %v\n", err)
		os.Exit(1)
	}
	if len(captures) == 0 {
		fmt.Fprintln(os.Stderr, "No captures found under internal/sitebdd/testdata/curl-examples")
		os.Exit(1)
	}

	docsRoot := filepath.Join(projectRoot, "site", "src", "pages", "docs")
	var files []string
	if err := filepath.WalkDir(docsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".mdx") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "scan docs: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(files)

	totalFiles := 0
	totalMatched := 0
	totalUpdated := 0
	for _, path := range files {
		route, err := routeFromMDX(projectRoot, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "route parse %s: %v\n", path, err)
			os.Exit(1)
		}
		matched, updated, err := syncFile(path, route, captures, *apply)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sync %s: %v\n", path, err)
			os.Exit(1)
		}
		if matched > 0 {
			totalFiles++
			totalMatched += matched
			totalUpdated += updated
			fmt.Printf("%s: matched=%d updated=%d\n", path, matched, updated)
		}
	}

	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("%s: files=%d matched=%d updated=%d\n", mode, totalFiles, totalMatched, totalUpdated)
}

func loadCaptures(projectRoot string) (map[string]curlCapture, error) {
	base := filepath.Join(projectRoot, "internal", "sitebdd", "testdata", "curl-examples")
	captures := map[string]curlCapture{}
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return captures, nil
	}

	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var items []curlCapture
		if err := json.Unmarshal(data, &items); err != nil {
			return nil
		}
		for _, item := range items {
			id := strings.TrimSpace(item.CaptureID)
			if id == "" {
				continue
			}
			captures[id] = item
		}
		return nil
	})
	return captures, err
}

func routeFromMDX(projectRoot, mdxPath string) (string, error) {
	pagesRoot := filepath.Join(projectRoot, "site", "src", "pages")
	rel, err := filepath.Rel(pagesRoot, mdxPath)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "docs/") || !strings.HasSuffix(rel, ".mdx") {
		return "", fmt.Errorf("unexpected docs path %s", mdxPath)
	}
	route := "/" + strings.TrimSuffix(rel, ".mdx")
	if strings.HasSuffix(route, "/index") {
		route = strings.TrimSuffix(route, "/index")
	}
	return route, nil
}

var (
	openTagRe          = regexp.MustCompile(`(?s)<CurlTest\b.*?>`)
	exampleOutputRe    = regexp.MustCompile(`(?s)\s+exampleOutput=\{` + "`" + `.*?` + "`" + `\}`)
	exampleOutputLangR = regexp.MustCompile(`\s+exampleOutputLang="[^"]*"`)
)

func syncFile(path, route string, captures map[string]curlCapture, apply bool) (matched int, updated int, err error) {
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	src := string(srcBytes)

	var out strings.Builder
	last := 0
	ordinal := 0
	matches := openTagRe.FindAllStringIndex(src, -1)
	for _, idx := range matches {
		start, end := idx[0], idx[1]
		out.WriteString(src[last:start])
		tag := src[start:end]
		ordinal++
		captureID := fmt.Sprintf("%s#%d", route, ordinal)
		capture, ok := captures[captureID]
		if !ok {
			captureIDWithSlash := fmt.Sprintf("%s/#%d", route, ordinal)
			capture, ok = captures[captureIDWithSlash]
		}
		if !ok {
			out.WriteString(tag)
			last = end
			continue
		}
		matched++
		newTag := rewriteOpenTag(tag, inferLang(capture), strings.TrimSpace(capture.Body))
		if newTag != tag {
			updated++
		}
		out.WriteString(newTag)
		last = end
	}
	out.WriteString(src[last:])

	newSrc := out.String()
	if apply && newSrc != src {
		if err := os.WriteFile(path, []byte(newSrc), 0o644); err != nil {
			return matched, updated, err
		}
	}
	return matched, updated, nil
}

func inferLang(capture curlCapture) string {
	ct := strings.ToLower(strings.TrimSpace(capture.ContentType))
	if strings.Contains(ct, "json") {
		return "json"
	}
	var anyJSON any
	if json.Unmarshal([]byte(capture.Body), &anyJSON) == nil {
		return "json"
	}
	return "text"
}

func rewriteOpenTag(openTag, lang, body string) string {
	tag := exampleOutputLangR.ReplaceAllString(openTag, "")
	tag = exampleOutputRe.ReplaceAllString(tag, "")
	escaped := escapeTemplateLiteral(strings.Trim(body, "\n"))
	insert := fmt.Sprintf(` exampleOutputLang="%s" exampleOutput={`+"`%s`"+`}`, lang, escaped)
	return strings.TrimSuffix(tag, ">") + insert + ">"
}

func escapeTemplateLiteral(value string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		"`", "\\`",
		`${`, `\${`,
	)
	return replacer.Replace(value)
}
