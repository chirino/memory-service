//go:build site_tests

package sitebdd

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const diagnosticLogTailBytes = 8 * 1024

func siteDiagnosticsEnabled() bool {
	if raw := strings.TrimSpace(os.Getenv("SITE_TEST_DIAGNOSTICS")); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on", "debug":
			return true
		default:
			return false
		}
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true")
}

func siteDiagnosticf(format string, args ...any) {
	if !siteDiagnosticsEnabled() {
		return
	}
	fmt.Printf("[sitebdd] "+format+"\n", args...)
}

func readFileTail(path string, maxBytes int64) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	buf := make([]byte, size-start)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
