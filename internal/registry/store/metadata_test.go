package store

import (
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeMetadataPatch_ExplicitNull(t *testing.T) {
	patch := []byte(`{"key1": null, "key2": "value"}`)
	result, err := DecodeMetadataPatch(patch)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Nil(t, result["key1"])
	assert.Equal(t, "value", result["key2"])
}

func TestMetadataPatchChangesAgainstDropsNoOps(t *testing.T) {
	patch := MetadataPatch{
		"same":   "value",
		"change": "new",
		"delete": nil,
		"absent": nil,
	}
	changes := patch.ChangesAgainst(map[string]interface{}{
		"same":   "value",
		"change": "old",
		"delete": "present",
	})

	require.Equal(t, MetadataPatch{"change": "new", "delete": nil}, changes)
}

func TestMetadataPatchChangesAgainstDropsNestedBSONNoOps(t *testing.T) {
	patch, err := DecodeMetadataPatch([]byte(`{
		"state":{"status":"done","steps":[1.0,2e0]},
		"reviewers":[{"name":"alice","roles":["owner","editor"]}]
	}`))
	require.NoError(t, err)

	current := map[string]interface{}{
		"state": bson.D{
			{Key: "status", Value: "done"},
			{Key: "steps", Value: bson.A{int32(1), int64(2)}},
		},
		"reviewers": bson.A{
			bson.D{
				{Key: "name", Value: "alice"},
				{Key: "roles", Value: bson.A{"owner", "editor"}},
			},
		},
	}

	require.Nil(t, patch.ChangesAgainst(current))
}

func TestDecodeMetadataPatch_NestedReplacement(t *testing.T) {
	patch := []byte(`{"nested": {"inner": "value", "num": 42}}`)
	result, err := DecodeMetadataPatch(patch)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	nested, ok := result["nested"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "value", nested["inner"])
	assert.Equal(t, float64(42), nested["num"])
}

func TestDecodeMetadataPatch_NullPatch(t *testing.T) {
	patch := []byte(`null`)
	result, err := DecodeMetadataPatch(patch)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestDecodeMetadataPatch_EmptyPatch(t *testing.T) {
	patch := []byte(``)
	result, err := DecodeMetadataPatch(patch)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestDecodeMetadataPatch_MalformedJSON(t *testing.T) {
	patch := []byte(`{invalid}`)
	_, err := DecodeMetadataPatch(patch)
	assert.Error(t, err)
}

func TestDecodeMetadataPatch_NonObjectInput(t *testing.T) {
	patch := []byte(`["array"]`)
	_, err := DecodeMetadataPatch(patch)
	assert.Error(t, err)
}

func TestDecodeMetadataPatch_MalformedValue(t *testing.T) {
	// Invalid JSON in a value should still error
	patch := []byte(`{"key": invalid}`)
	_, err := DecodeMetadataPatch(patch)
	assert.Error(t, err)
}

func TestMetadataFilterFromOptionalPair_BothNil(t *testing.T) {
	filters, err := MetadataFilterFromOptionalPair(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, filters)
}

func TestMetadataFilterFromOptionalPair_KeyOnly(t *testing.T) {
	key := "status"
	filters, err := MetadataFilterFromOptionalPair(&key, nil)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "value is required")
}

func TestMetadataFilterFromOptionalPair_ValueOnly(t *testing.T) {
	value := "waiting"
	filters, err := MetadataFilterFromOptionalPair(nil, &value)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "key is required")
}

func TestMetadataFilterFromOptionalPair_EmptyKey(t *testing.T) {
	key := ""
	value := "waiting"
	filters, err := MetadataFilterFromOptionalPair(&key, &value)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "invalid")
}

func TestMetadataFilterFromOptionalPair_InvalidKey(t *testing.T) {
	key := "bad key!"
	value := "waiting"
	filters, err := MetadataFilterFromOptionalPair(&key, &value)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "invalid")
}

func TestMetadataFilterFromOptionalPair_ValidPair(t *testing.T) {
	key := "status"
	value := "waiting"
	filters, err := MetadataFilterFromOptionalPair(&key, &value)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "status", filters[0].Key)
	assert.Equal(t, ConversationMetadataEqual, filters[0].Operator)
	assert.Equal(t, "waiting", filters[0].Value)
}

func TestMetadataFilterFromOptionalPair_EmptyValue(t *testing.T) {
	key := "status"
	value := ""
	filters, err := MetadataFilterFromOptionalPair(&key, &value)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "status", filters[0].Key)
	assert.Equal(t, ConversationMetadataEqual, filters[0].Operator)
	assert.Equal(t, "", filters[0].Value)
}

func TestParseMetadataFilterQuery_RepeatedNewFilters(t *testing.T) {
	rawQuery := "metadata=status=waiting&metadata=agent-id!=worker-1&metadata=tag==special&metadata=empty=&metadata=bang!=a!b=c"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	require.NoError(t, err)
	require.Len(t, filters, 5)

	assert.Equal(t, ConversationMetadataPredicate{Key: "status", Operator: ConversationMetadataEqual, Value: "waiting"}, filters[0])
	assert.Equal(t, ConversationMetadataPredicate{Key: "agent-id", Operator: ConversationMetadataNotEqual, Value: "worker-1"}, filters[1])
	assert.Equal(t, ConversationMetadataPredicate{Key: "tag", Operator: ConversationMetadataEqual, Value: "=special"}, filters[2])
	assert.Equal(t, ConversationMetadataPredicate{Key: "empty", Operator: ConversationMetadataEqual, Value: ""}, filters[3])
	assert.Equal(t, ConversationMetadataPredicate{Key: "bang", Operator: ConversationMetadataNotEqual, Value: "a!b=c"}, filters[4])
}

func TestParseMetadataFilterQuery_MaxSixRejection(t *testing.T) {
	rawQuery := "metadata=a=1&metadata=b=2&metadata=c=3&metadata=d=4&metadata=e=5&metadata=f=6"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "at most 5")
}

func TestParseMetadataFilterQuery_MixedLegacyAndNewRejection(t *testing.T) {
	rawQuery := "metadata=status=waiting&metadata[agent]=worker"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "cannot mix")
}

func TestParseMetadataFilterQuery_MalformedExpression(t *testing.T) {
	_, err := ParseMetadataFilterQuery("metadata=status!waiting")
	assert.Error(t, err)

	_, err = ParseMetadataFilterQuery("metadata=status")
	assert.Error(t, err)

	_, err = ParseMetadataFilterQuery("metadata==waiting")
	assert.Error(t, err)
}

func TestParseMetadataFilterQuery_PercentEncoding(t *testing.T) {
	rawQuery := "metadata=tag=a%2Bb"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "a+b", filters[0].Value)

	rawQuerySpace := "metadata=tag=a+b"
	filtersSpace, err := ParseMetadataFilterQuery(rawQuerySpace)
	require.NoError(t, err)
	require.Len(t, filtersSpace, 1)
	assert.Equal(t, "a b", filtersSpace[0].Value)
}

func TestOpenAPIRegexValidation(t *testing.T) {
	pattern := `^[A-Za-z0-9_-]+(?:!=|=)[\s\S]*$`
	matched, err := regexp.MatchString(pattern, "a=x")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "a=")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "a!=x")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "a!=")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "key_1-2=foo!=bar=baz")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "key=hello\nworld")
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = regexp.MatchString(pattern, "invalid key=x")
	require.NoError(t, err)
	assert.False(t, matched)

	matched, err = regexp.MatchString(pattern, "=x")
	require.NoError(t, err)
	assert.False(t, matched)

	matched, err = regexp.MatchString(pattern, "key!x")
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestParseMetadataFilterQuery_NoFilter(t *testing.T) {
	values := url.Values{}
	values.Set("mode", "all")
	values.Set("limit", "20")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	require.NoError(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_ValidLegacyFilter(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[status]", "waiting")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "status", filters[0].Key)
	assert.Equal(t, ConversationMetadataEqual, filters[0].Operator)
	assert.Equal(t, "waiting", filters[0].Value)
}

func TestParseMetadataFilterQuery_LegacyEmptyValue(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[status]", "")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "status", filters[0].Key)
	assert.Equal(t, ConversationMetadataEqual, filters[0].Operator)
	assert.Equal(t, "", filters[0].Value)
}

func TestParseMetadataFilterQuery_MultipleLegacyKeys(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[status]", "waiting")
	values.Set("metadata[priority]", "high")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "exactly one key")
}

func TestParseMetadataFilterQuery_RepeatedLegacyValue(t *testing.T) {
	values := url.Values{}
	values.Add("metadata[status]", "waiting")
	values.Add("metadata[status]", "running")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "exactly one value")
}

func TestParseMetadataFilterQuery_InvalidLegacyKey(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[bad key!]", "value")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
	assert.Contains(t, err.Error(), "invalid")
}

func TestParseMetadataFilterQuery_EmptyLegacyKey(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[]", "value")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_MalformedBracket(t *testing.T) {
	values := url.Values{}
	values.Set("metadata[missing-close", "value")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_MalformedMetadataPrefix(t *testing.T) {
	values := url.Values{}
	values.Set("metadataFoo", "bar")
	values.Set("mode", "all")
	filters, err := ParseMetadataFilterQuery(values.Encode())
	assert.Error(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_MissingEquals(t *testing.T) {
	const rawQuery = "metadata[status]"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	assert.Error(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_ExplicitEmptyValue(t *testing.T) {
	const rawQuery = "metadata[status]="
	filters, err := ParseMetadataFilterQuery(rawQuery)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "status", filters[0].Key)
	assert.Empty(t, filters[0].Value)
}

func TestParseMetadataFilterQuery_EncodedMissingEquals(t *testing.T) {
	const rawQuery = "metadata%5Bstatus%5D"
	filters, err := ParseMetadataFilterQuery(rawQuery)
	assert.Error(t, err)
	assert.Nil(t, filters)
}

func TestParseMetadataFilterQuery_MalformedEncodedValue(t *testing.T) {
	filters, err := ParseMetadataFilterQuery("metadata%5Bstatus%5D=%ZZ")
	assert.Error(t, err)
	assert.Nil(t, filters)
}
