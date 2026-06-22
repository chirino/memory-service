package version

import (
	"bytes"
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/runtimeversion"
)

func TestCommandPrintsRuntimeVersion(t *testing.T) {
	runtimeversion.Set("1.2.3")
	t.Cleanup(func() { runtimeversion.Set("") })

	cmd := Command()
	var out bytes.Buffer
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{"memory-service", "version"}); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	if got := out.String(); got != "1.2.3\n" {
		t.Fatalf("expected version output, got %q", got)
	}
}
