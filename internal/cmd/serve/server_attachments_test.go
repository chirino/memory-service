package serve

import (
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveAttachmentStoreNameRejectsMongoDatabaseStorage(t *testing.T) {
	for _, attachType := range []string{"db", "mongo"} {
		t.Run(attachType, func(t *testing.T) {
			name, err := resolveAttachmentStoreName(&config.Config{
				DatastoreType: "mongo",
				AttachType:    attachType,
			})

			require.Empty(t, name)
			require.ErrorContains(t, err, "not supported")
			require.ErrorContains(t, err, "attachments-kind=s3")
		})
	}
}

func TestResolveAttachmentStoreNameAllowsExternalMongoStorage(t *testing.T) {
	for _, attachType := range []string{"s3", "fs"} {
		t.Run(attachType, func(t *testing.T) {
			name, err := resolveAttachmentStoreName(&config.Config{
				DatastoreType: "mongo",
				AttachType:    attachType,
			})

			require.NoError(t, err)
			require.Equal(t, attachType, name)
		})
	}
}
