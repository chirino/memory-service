package api_test

import (
	"sort"
	"strings"
	"testing"

	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

// Enhancement 529-03 contract: caller-supplied attachment IDs and the metadata lookup.

func attachmentSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := generatedapi.GetSwagger()
	require.NoError(t, err)
	return spec
}

// flat collapses the line wrapping of YAML block scalars so fragments can span lines.
func flat(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func uploadAttachmentOperation(t *testing.T, spec *openapi3.T) *openapi3.Operation {
	t.Helper()
	item := spec.Paths.Find("/v1/attachments")
	require.NotNil(t, item)
	require.NotNil(t, item.Post)
	return item.Post
}

func requireOptionalUUIDProperty(t *testing.T, schema *openapi3.Schema, name string) {
	t.Helper()
	prop := schema.Properties[name]
	require.NotNil(t, prop, name)
	require.True(t, prop.Value.Type.Is("string"), name)
	require.Equal(t, "uuid", prop.Value.Format, name)
	require.False(t, prop.Value.Nullable, name)
	require.NotEmpty(t, prop.Value.Description, name)
	// The ID is opt-in: omitting it must keep today's behavior.
	require.NotContains(t, schema.Required, name)
}

func TestUploadAttachmentMultipartDeclaresOptionalID(t *testing.T) {
	t.Parallel()
	op := uploadAttachmentOperation(t, attachmentSpec(t))
	media := op.RequestBody.Value.Content.Get("multipart/form-data")
	require.NotNil(t, media)
	schema := media.Schema.Value

	requireOptionalUUIDProperty(t, schema, "id")
	require.ElementsMatch(t, []string{"file"}, schema.Required)
	require.NotNil(t, schema.Properties["file"])
	for _, fragment := range []string{"identifies the upload operation", "not a content hash", "omit"} {
		require.Contains(t, flat(schema.Properties["id"].Value.Description), fragment)
	}
}

func TestCreateFromURLRequestDeclaresOptionalID(t *testing.T) {
	t.Parallel()
	schema := attachmentSpec(t).Components.Schemas["CreateFromUrlRequest"].Value

	requireOptionalUUIDProperty(t, schema, "id")
	require.ElementsMatch(t, []string{"sourceUrl"}, schema.Required)
	for _, name := range []string{"sourceUrl", "contentType", "name"} {
		require.NotNil(t, schema.Properties[name], name)
	}
	for _, fragment := range []string{"identifies the creation operation", "not a content hash", "immediately", "downloading"} {
		require.Contains(t, flat(schema.Properties["id"].Value.Description), fragment)
	}
}

func TestUploadAttachmentDocumentsIDOutcomes(t *testing.T) {
	t.Parallel()
	op := uploadAttachmentOperation(t, attachmentSpec(t))

	codes := make([]string, 0)
	for code := range op.Responses.Map() {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	require.Equal(t, []string{"201", "400", "404", "409", "413", "default"}, codes)

	// OpenAPI 3.x ignores siblings of $ref, so the operation-specific outcomes must be inline.
	for code, mustMention := range map[string][]string{
		"400": {"not a valid UUID"},
		"404": {"another user", "Nothing was stored"},
		"409": {"already exists", "GET /v1/attachments/{id}/metadata"},
	} {
		resp := op.Responses.Value(code)
		require.Empty(t, resp.Ref, "%s must be inline so its description is not discarded", code)
		for _, fragment := range mustMention {
			require.Contains(t, flat(*resp.Value.Description), fragment, code)
		}
		media := resp.Value.Content.Get("application/json")
		require.NotNil(t, media, code)
		require.Equal(t, "#/components/schemas/ErrorResponse", media.Schema.Ref, code)
	}

	// A multipart upload also waits for its own finalize. If the row was deleted, expired, or
	// replaced while the body streamed, the stored content is discarded and the request is a 409,
	// not a 404: the caller's own ID is no longer the attempt that was in flight.
	conflict := flat(*op.Responses.Value("409").Value.Description)
	for _, fragment := range []string{"deleted", "expired", "replaced", "while the content was being stored", "content is discarded"} {
		require.Contains(t, conflict, fragment)
	}

	created := flat(*op.Responses.Value("201").Value.Description)
	require.Contains(t, created, "returns immediately with status `downloading`")
	require.Contains(t, created, "download has not finished")
}

func TestUploadAttachmentDescriptionCoversRecoveryAndWindow(t *testing.T) {
	t.Parallel()
	description := flat(uploadAttachmentOperation(t, attachmentSpec(t)).Description)
	for _, fragment := range []string{
		"Caller-supplied attachment ID",
		"identifies the upload operation",
		"never compared",
		"GET /v1/attachments/{id}/metadata",
		"`ready`",
		"`uploading`",
		"`failed`",
		"inconclusive",
		"buffered",
		"Idempotency window",
		"actually deleted",
		"not a guaranteed deadline",
	} {
		require.Contains(t, description, fragment)
	}
}

func TestAttachmentMetadataOperation(t *testing.T) {
	t.Parallel()
	spec := attachmentSpec(t)
	item := spec.Paths.Find("/v1/attachments/{id}/metadata")
	require.NotNil(t, item, "GET /v1/attachments/{id}/metadata")
	op := item.Get
	require.NotNil(t, op)
	// oapi-codegen upper-camel-cases operationId in the embedded spec (contract: getAttachmentMetadata).
	require.Equal(t, "GetAttachmentMetadata", op.OperationID)
	require.Equal(t, []string{"Attachments"}, op.Tags)
	require.Nil(t, item.Post)
	require.Nil(t, item.Delete)

	require.Len(t, op.Parameters, 1)
	param := op.Parameters[0].Value
	require.Equal(t, "id", param.Name)
	require.Equal(t, "path", param.In)
	require.True(t, param.Required)
	require.Equal(t, "uuid", param.Schema.Value.Format)

	codes := make([]string, 0)
	for code := range op.Responses.Map() {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	require.Equal(t, []string{"200", "400", "403", "404", "default"}, codes)

	ok := op.Responses.Value("200").Value.Content.Get("application/json")
	require.NotNil(t, ok)
	require.Equal(t, "#/components/schemas/AttachmentUploadResponse", ok.Schema.Ref)

	for code, mustMention := range map[string][]string{
		"400": {"not a valid UUID"},
		// GetAttachment also returns 403 for a linked attachment whose conversation the caller
		// cannot read, so the 403 text must not claim it is only about unlinked rows.
		"403": {"another user", "linked", "cannot read"},
		// GetAttachment hides archived rows and a non-owner's linked row whose entry is gone, so 404
		// must not claim it only means "no such ID".
		"404": {"inconclusive", "deleted", "archived"},
	} {
		resp := op.Responses.Value(code)
		require.Empty(t, resp.Ref, "%s must be inline so its description is not discarded", code)
		for _, fragment := range mustMention {
			require.Contains(t, flat(*resp.Value.Description), fragment, code)
		}
	}

	// Same credentials as the rest of the attachment surface.
	require.NotNil(t, op.Security)
	schemes := make([]string, 0)
	for _, req := range *op.Security {
		for name := range req {
			schemes = append(schemes, name)
		}
	}
	require.ElementsMatch(t, []string{"BearerAuth", "ApiKeyAuth"}, schemes)

	for _, fragment := range []string{
		"Same access rules as `GET /v1/attachments/{id}`",
		"does not return the content",
		"Another user gets 403",
		"`ready`", "`uploading`", "`downloading`", "`failed`",
		"DELETE /v1/attachments/{id}",
		"inconclusive",
		"actually deleted",
		"Only a `ready` attachment can be referenced by an entry",
	} {
		require.Contains(t, flat(op.Description), fragment)
	}
}

func TestAttachmentStatusDescriptionMatchesStateMachine(t *testing.T) {
	t.Parallel()
	status := attachmentSpec(t).Components.Schemas["AttachmentUploadResponse"].Value.Properties["status"].Value
	require.Equal(t, []any{"uploading", "downloading", "ready", "failed"}, status.Enum)
	for _, fragment := range []string{
		"`uploading`", "`downloading`", "`ready`", "`failed`",
		"caller-supplied `id`",
		"can be linked to an entry only when it is `ready`",
	} {
		require.Contains(t, flat(status.Description), fragment)
	}
}

// No admin surface creates attachments (API parity does not apply to create), so the admin
// contract must not gain an upload operation or a caller-supplied attachment ID.
func TestAdminAPIHasNoAttachmentCreateOrMetadataOperation(t *testing.T) {
	t.Parallel()
	admin, err := generatedadmin.GetSwagger()
	require.NoError(t, err)

	list := admin.Paths.Find("/v1/admin/attachments")
	require.NotNil(t, list, "the admin attachment list must still exist")
	require.NotNil(t, list.Get)
	require.Nil(t, list.Post, "no admin operation creates attachments")

	for path, item := range admin.Paths.Map() {
		if !strings.Contains(path, "/attachments") {
			continue
		}
		require.False(t, strings.HasSuffix(path, "/metadata"), "%s", path)
		require.Nil(t, item.Post, "%s", path)
		require.Nil(t, item.Put, "%s", path)
	}
	for _, name := range []string{"CreateFromUrlRequest", "AttachmentUploadResponse"} {
		require.Nil(t, admin.Components.Schemas[name], "admin spec must not define %s", name)
	}
}

// LinkAttachmentToEntry only links a `ready` attachment, so appending an entry that references an
// unfinished one returns 409. OpenAPI ignores siblings of $ref, so the text must be on an inline
// response or generated clients never see it.
func TestAppendConversationEntryDocumentsNotReadyAttachmentConflict(t *testing.T) {
	t.Parallel()
	item := attachmentSpec(t).Paths.Find("/v1/conversations/{conversationId}/entries")
	require.NotNil(t, item)
	require.NotNil(t, item.Post)
	resp := item.Post.Responses.Value("409")
	require.NotNil(t, resp)
	require.Empty(t, resp.Ref, "409 must be inline so its description is not discarded")
	description := flat(*resp.Value.Description)
	for _, fragment := range []string{"`ready`", "uploading", "downloading", "failed", "nothing is appended"} {
		require.Contains(t, description, fragment)
	}
	media := resp.Value.Content.Get("application/json")
	require.NotNil(t, media)
	require.Equal(t, "#/components/schemas/ErrorResponse", media.Schema.Ref)
}
