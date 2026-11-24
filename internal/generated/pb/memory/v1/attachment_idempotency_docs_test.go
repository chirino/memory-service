package pb_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Enhancement 529-03: the gRPC contract documents its outcomes in proto comments, which are not
// part of the compiled descriptor. Read the source of truth so the documented status mapping
// cannot drift from the design (INVALID_ARGUMENT / NOT_FOUND / ABORTED).

func protoMessageComment(t *testing.T, message string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..", "contracts", "protobuf", "memory", "v1", "memory_service.proto")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	re := regexp.MustCompile(`(?s)message ` + regexp.QuoteMeta(message) + ` \{(.*?)\n\}`)
	match := re.FindSubmatch(raw)
	require.NotNil(t, match, "message %s not found in %s", message, path)

	var comments []string
	for _, line := range strings.Split(string(match[1]), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
			comments = append(comments, strings.TrimSpace(strings.TrimPrefix(trimmed, "//")))
		}
	}
	return strings.Join(strings.Fields(strings.Join(comments, " ")), " ")
}

func TestAttachmentIDFieldCommentsDocumentOutcomes(t *testing.T) {
	t.Parallel()
	for message, extra := range map[string][]string{
		"UploadMetadata": {
			// The attachment exists only after the metadata message arrives.
			"NOT_FOUND is inconclusive",
			`status "uploading"`,
		},
		"CreateAttachmentFromUrlRequest": {
			`returns immediately with status "downloading"`,
			"the download has not finished",
			"No download is started",
		},
	} {
		t.Run(message, func(t *testing.T) {
			comment := protoMessageComment(t, message)
			for _, fragment := range append([]string{
				"not a content hash",
				"INVALID_ARGUMENT",
				"ABORTED",
				"NOT_FOUND",
				"GetAttachment",
				"DeleteAttachment",
				"actually deleted",
				"not a guaranteed deadline",
				`linked to an entry only when it is "ready"`,
			}, extra...) {
				require.Contains(t, comment, fragment)
			}
			// Same-owner existing IDs are ABORTED and another user's are NOT_FOUND, never the reverse.
			require.Contains(t, comment, "already belongs to the caller returns ABORTED")
			require.Contains(t, comment, "belongs to another user returns NOT_FOUND")
		})
	}
}
