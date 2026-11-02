package api_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	"github.com/getkin/kin-openapi/openapi3"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/require"
)

// Enhancement 529-02 contract: PutMemory idempotency fields on the user and admin REST surfaces.

func putMemoryOperation(t *testing.T, spec *openapi3.T, path string) *openapi3.Operation {
	t.Helper()
	item := spec.Paths.Find(path)
	require.NotNil(t, item, path)
	require.NotNil(t, item.Put, "PUT %s", path)
	return item.Put
}

func putMemoryRequestSchema(t *testing.T, spec *openapi3.T) *openapi3.Schema {
	t.Helper()
	ref := spec.Components.Schemas["PutMemoryRequest"]
	require.NotNil(t, ref)
	require.NotNil(t, ref.Value)
	return ref.Value
}

type putMemorySurface struct {
	name string
	spec func(t *testing.T) *openapi3.T
	path string
}

func putMemorySurfaces() []putMemorySurface {
	return []putMemorySurface{
		{"user", func(t *testing.T) *openapi3.T {
			s, err := generatedapi.GetSwagger()
			require.NoError(t, err)
			return s
		}, "/v1/memories"},
		{"admin", func(t *testing.T) *openapi3.T {
			s, err := generatedadmin.GetSwagger()
			require.NoError(t, err)
			return s
		}, "/admin/v1/memories"},
	}
}

func TestPutMemoryRequestSchemaDeclaresIdempotencyFields(t *testing.T) {
	t.Parallel()
	for _, surface := range putMemorySurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			schema := putMemoryRequestSchema(t, surface.spec(t))

			for _, name := range []string{"revision_id", "expected_revision_id"} {
				prop := schema.Properties[name]
				require.NotNil(t, prop, name)
				require.True(t, prop.Value.Type.Is("string"), name)
				require.Equal(t, "uuid", prop.Value.Format, name)
				require.False(t, prop.Value.Nullable, name)
				require.NotEmpty(t, prop.Value.Description, name)
			}
			revision := schema.Properties["revision"]
			require.NotNil(t, revision)
			require.True(t, revision.Value.Type.Is("integer"))
			require.Equal(t, "int64", revision.Value.Format)
			require.NotEmpty(t, revision.Value.Description)

			// Idempotency is opt-in: none of the new fields may be required.
			require.ElementsMatch(t, []string{"namespace", "key", "value"}, schema.Required)

			// Existing fields must be untouched.
			for _, name := range []string{"namespace", "key", "value", "ttl_seconds", "index", "expected_revision", "kind"} {
				require.NotNil(t, schema.Properties[name], name)
			}
		})
	}
}

func TestPutMemoryRequestFieldDescriptionsStateRequirements(t *testing.T) {
	t.Parallel()
	for _, surface := range putMemorySurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			schema := putMemoryRequestSchema(t, surface.spec(t))
			require.Contains(t, schema.Properties["expected_revision_id"].Value.Description, "Requires `revision_id`")
			require.Contains(t, schema.Properties["expected_revision_id"].Value.Description, "expected_active_revision_id_mismatch")
			revision := schema.Properties["revision"].Value.Description
			require.Contains(t, revision, "Requires `revision_id`")
			require.Contains(t, revision, "revision_already_exists")
			require.Contains(t, revision, "any other value returns 400")
			require.Contains(t, schema.Properties["revision_id"].Value.Description, "no stored namespace, key, value, kind, or")
		})
	}
}

func TestPutMemoryOperationDocumentsResponses(t *testing.T) {
	t.Parallel()
	for _, surface := range putMemorySurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			op := putMemoryOperation(t, surface.spec(t), surface.path)

			codes := make([]string, 0)
			for code := range op.Responses.Map() {
				codes = append(codes, code)
			}
			sort.Strings(codes)
			require.Equal(t, []string{"200", "201", "400", "404", "409", "default"}, codes)

			for _, code := range []string{"200", "201"} {
				media := op.Responses.Value(code).Value.Content.Get("application/json")
				require.NotNil(t, media, code)
				require.Equal(t, "#/components/schemas/MemoryWriteResult", media.Schema.Ref, code)
			}

			// OpenAPI 3.0 ignores siblings of $ref, so 400/404 must be inline responses for their
			// operation-specific descriptions to reach consumers.
			for code, mustMention := range map[string][]string{
				"400": {"revision_id", "expected_revision_id", "other than 1"},
				"404": {"revision_id", "no longer accepts retries"},
			} {
				resp := op.Responses.Value(code)
				require.Empty(t, resp.Ref, "%s must be inline so its description is not discarded", code)
				for _, fragment := range mustMention {
					require.Contains(t, *resp.Value.Description, fragment, code)
				}
				media := resp.Value.Content.Get("application/json")
				require.NotNil(t, media, code)
				require.Equal(t, "#/components/schemas/ErrorResponse", media.Schema.Ref, code)
			}

			conflict := op.Responses.Value("409").Value
			for _, fragment := range []string{
				"memory_revision_already_exists", "revision_already_exists",
				"expected_active_revision_id_mismatch", "expected_revision",
			} {
				require.Contains(t, *conflict.Description, fragment)
			}
			media := conflict.Content.Get("application/json")
			require.NotNil(t, media)
			require.Equal(t, "#/components/schemas/ErrorResponse", media.Schema.Ref)
			for _, code := range []string{"memory_revision_already_exists", "revision_already_exists", "expected_active_revision_id_mismatch"} {
				example := media.Examples[code]
				require.NotNil(t, example, code)
				require.Equal(t, code, example.Value.Value.(map[string]any)["code"], code)
			}
			// The cross-namespace disclosure rule: the conflict example carries only the caller's ID.
			conflictExample := media.Examples["memory_revision_already_exists"].Value.Value.(map[string]any)
			require.Equal(t, map[string]any{"revisionId": "6f1c1d0a-0000-4000-8000-000000000001"}, conflictExample["details"])
		})
	}
}

func TestPutMemoryOperationDescriptionCoversModesAndBoundaries(t *testing.T) {
	t.Parallel()
	for _, surface := range putMemorySurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			description := putMemoryOperation(t, surface.spec(t), surface.path).Description
			for _, fragment := range []string{
				"Idempotency modes", "revision: 1", "expected_revision_id",
				"Exact retry", "namespace, key, value, index, TTL, and kind",
				"Not compared", "Replay detection runs before kind-writability",
				"Idempotency window",
			} {
				require.Contains(t, description, fragment)
			}
		})
	}
}

// The user and admin surfaces must carry identical request fields and response codes (API parity rule).
func TestPutMemoryUserAndAdminContractsAreAligned(t *testing.T) {
	t.Parallel()
	surfaces := putMemorySurfaces()
	user, admin := surfaces[0].spec(t), surfaces[1].spec(t)
	userSchema, adminSchema := putMemoryRequestSchema(t, user), putMemoryRequestSchema(t, admin)
	for _, name := range []string{"revision_id", "expected_revision_id", "revision"} {
		u, a := userSchema.Properties[name].Value, adminSchema.Properties[name].Value
		require.Equal(t, u.Type, a.Type, name)
		require.Equal(t, u.Format, a.Format, name)
	}
	userCodes, adminCodes := []string{}, []string{}
	for code := range putMemoryOperation(t, user, surfaces[0].path).Responses.Map() {
		userCodes = append(userCodes, code)
	}
	for code := range putMemoryOperation(t, admin, surfaces[1].path).Responses.Map() {
		adminCodes = append(adminCodes, code)
	}
	require.ElementsMatch(t, userCodes, adminCodes)
}

func TestPutMemoryRequestGoBindings(t *testing.T) {
	t.Parallel()
	for name, typ := range map[string]reflect.Type{
		"api":   reflect.TypeOf(generatedapi.PutMemoryRequest{}),
		"admin": reflect.TypeOf(generatedadmin.PutMemoryRequest{}),
	} {
		t.Run(name, func(t *testing.T) {
			for field, want := range map[string]reflect.Type{
				"RevisionId":         reflect.TypeOf((*openapi_types.UUID)(nil)),
				"ExpectedRevisionId": reflect.TypeOf((*openapi_types.UUID)(nil)),
				"Revision":           reflect.TypeOf((*int64)(nil)),
			} {
				f, ok := typ.FieldByName(field)
				require.True(t, ok, field)
				require.Equal(t, want, f.Type, field)
			}
			for field, tag := range map[string]string{
				"RevisionId": "revision_id,omitempty", "ExpectedRevisionId": "expected_revision_id,omitempty", "Revision": "revision,omitempty",
			} {
				f, _ := typ.FieldByName(field)
				require.Equal(t, tag, f.Tag.Get("json"), field)
			}
		})
	}
}

// REST rejects malformed UUIDs at bind time (Gin ShouldBindJSON uses encoding/json), so the
// generated types must fail Unmarshal for them and distinguish "absent" from "present".
func TestPutMemoryRequestJSONBinding(t *testing.T) {
	t.Parallel()
	base := `"namespace":["u"],"key":"k","value":{}`

	var ok generatedapi.PutMemoryRequest
	require.NoError(t, json.Unmarshal([]byte(`{`+base+`,"revision_id":"6f1c1d0a-0000-4000-8000-000000000001","expected_revision_id":"6f1c1d0a-0000-4000-8000-000000000002","revision":1}`), &ok))
	require.NotNil(t, ok.RevisionId)
	require.Equal(t, "6f1c1d0a-0000-4000-8000-000000000001", ok.RevisionId.String())
	require.Equal(t, "6f1c1d0a-0000-4000-8000-000000000002", ok.ExpectedRevisionId.String())
	require.NotNil(t, ok.Revision)
	require.EqualValues(t, 1, *ok.Revision)

	var absent generatedapi.PutMemoryRequest
	require.NoError(t, json.Unmarshal([]byte(`{`+base+`}`), &absent))
	require.Nil(t, absent.RevisionId)
	require.Nil(t, absent.ExpectedRevisionId)
	require.Nil(t, absent.Revision)

	// revision: 0 is present (and must later be rejected as != 1), not absent.
	var zero generatedapi.PutMemoryRequest
	require.NoError(t, json.Unmarshal([]byte(`{`+base+`,"revision":0}`), &zero))
	require.NotNil(t, zero.Revision)
	require.EqualValues(t, 0, *zero.Revision)

	for _, field := range []string{"revision_id", "expected_revision_id"} {
		for _, bad := range []string{`"not-a-uuid"`, `""`, `123`} {
			var req generatedapi.PutMemoryRequest
			require.Error(t, json.Unmarshal([]byte(`{`+base+`,"`+field+`":`+bad+`}`), &req), "%s=%s", field, bad)
			var adminReq generatedadmin.PutMemoryRequest
			require.Error(t, json.Unmarshal([]byte(`{`+base+`,"`+field+`":`+bad+`}`), &adminReq), "admin %s=%s", field, bad)
		}
	}
}
