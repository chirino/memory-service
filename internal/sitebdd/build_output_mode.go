//go:build site_tests

package sitebdd

import (
	"os"
	"strings"
)

const (
	buildOutputOnFail = "on-fail"
	buildOutputStream = "stream"
	buildOutputQuiet  = "quiet"
)

// siteTestBuildOutputMode controls subprocess output handling for site BDD.
// Supported values:
// - on-fail (default): capture and replay only when a command fails
// - stream: stream output live
// - quiet: suppress replay/captured output even on failures
func siteTestBuildOutputMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SITE_TEST_BUILD_OUTPUT")))
	switch mode {
	case "", buildOutputOnFail:
		return buildOutputOnFail
	case buildOutputStream:
		return buildOutputStream
	case buildOutputQuiet:
		return buildOutputQuiet
	default:
		return buildOutputOnFail
	}
}

func shouldStreamBuildOutput() bool {
	return siteTestBuildOutputMode() == buildOutputStream
}

func shouldReplayCapturedBuildOutput() bool {
	return siteTestBuildOutputMode() == buildOutputOnFail
}
