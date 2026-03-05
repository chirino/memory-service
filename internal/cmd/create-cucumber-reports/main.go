package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	var (
		inputDir   string
		outputDir  string
		outputStem string
		maxItems   int
	)

	flag.StringVar(&inputDir, "input-dir", ".", "Directory to scan for cucumber json reports")
	flag.StringVar(&outputDir, "output-dir", ".artifacts", "Directory where combined reports are written")
	flag.StringVar(&outputStem, "output-stem", "cucumber-report", "Output file stem (chunk suffix is added automatically)")
	flag.IntVar(&maxItems, "max-items", 50, "Maximum number of top-level cucumber features per output file")
	flag.Parse()

	if maxItems <= 0 {
		exitErr(errors.New("--max-items must be greater than zero"))
	}

	inputFiles, err := findReportFiles(inputDir)
	if err != nil {
		exitErr(err)
	}

	features, err := loadFeatures(inputFiles)
	if err != nil {
		exitErr(err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		exitErr(fmt.Errorf("create output dir %q: %w", outputDir, err))
	}

	chunks, err := chunk(features, maxItems)
	if err != nil {
		exitErr(err)
	}
	if len(chunks) == 0 {
		chunks = []json.RawMessage{json.RawMessage("[]")}
	}

	for i, part := range chunks {
		name := fmt.Sprintf("%s-%03d.json", outputStem, i+1)
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, part, 0o644); err != nil {
			exitErr(fmt.Errorf("write %q: %w", path, err))
		}
	}

	fmt.Printf("created %d file(s) in %s from %d feature(s) across %d input file(s)\n",
		len(chunks), outputDir, len(features), len(inputFiles))
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func findReportFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".json") {
			return nil
		}
		normalized := filepath.ToSlash(path)
		if strings.Contains(normalized, "/reports/") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan reports under %q: %w", root, err)
	}

	sort.Strings(files)
	return files, nil
}

func loadFeatures(files []string) ([]json.RawMessage, error) {
	var features []json.RawMessage

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", path, err)
		}

		var root any
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, fmt.Errorf("parse %q: %w", path, err)
		}

		switch v := root.(type) {
		case []any:
			for _, item := range v {
				encoded, err := json.Marshal(item)
				if err != nil {
					return nil, fmt.Errorf("encode array entry from %q: %w", path, err)
				}
				features = append(features, json.RawMessage(encoded))
			}
		case map[string]any:
			encoded, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("encode object from %q: %w", path, err)
			}
			features = append(features, json.RawMessage(encoded))
		default:
			return nil, fmt.Errorf("unsupported top-level json in %q: expected array/object", path)
		}
	}

	return features, nil
}

func chunk(features []json.RawMessage, maxItems int) ([]json.RawMessage, error) {
	if len(features) == 0 {
		return nil, nil
	}

	var out []json.RawMessage
	for start := 0; start < len(features); start += maxItems {
		end := start + maxItems
		if end > len(features) {
			end = len(features)
		}
		payload, err := json.MarshalIndent(features[start:end], "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode combined chunk %d: %w", len(out)+1, err)
		}
		out = append(out, json.RawMessage(payload))
	}
	return out, nil
}
