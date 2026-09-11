package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// MetadataPatch is a decoded top-level metadata merge-patch. A present key with
// a nil value deletes that key; a non-nil value replaces the complete top-level
// value; absent keys remain unchanged.
type MetadataPatch map[string]interface{}

// ChangesAgainst removes patch keys that already have the requested value.
func (p MetadataPatch) ChangesAgainst(current map[string]interface{}) MetadataPatch {
	if len(p) == 0 {
		return nil
	}
	changes := make(MetadataPatch)
	for key, requested := range p {
		existing, found := current[key]
		if requested == nil {
			if found {
				changes[key] = nil
			}
			continue
		}
		if !found || !metadataValuesEqual(existing, requested) {
			changes[key] = requested
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

// metadataValuesEqual compares JSON-like metadata semantically across datastore
// representations. MongoDB decodes nested documents and arrays into named BSON
// slice types, while JSON requests and SQL stores use maps and []interface{}.
func metadataValuesEqual(left, right interface{}) bool {
	leftValue, leftOK := normalizeMetadataJSONValue(reflect.ValueOf(left))
	rightValue, rightOK := normalizeMetadataJSONValue(reflect.ValueOf(right))
	return leftOK && rightOK && jsonSemanticEqual(leftValue, rightValue)
}

func normalizeMetadataJSONValue(value reflect.Value) (interface{}, bool) {
	if !value.IsValid() {
		return nil, true
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, true
		}
		value = value.Elem()
	}
	if value.CanInterface() {
		if number, ok := value.Interface().(json.Number); ok {
			return number, true
		}
	}

	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), true
	case reflect.String:
		return value.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Number(strconv.FormatInt(value.Int(), 10)), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return json.Number(strconv.FormatUint(value.Uint(), 10)), true
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		return json.Number(strconv.FormatFloat(number, 'g', -1, value.Type().Bits())), true
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		result := make(map[string]interface{}, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			normalized, ok := normalizeMetadataJSONValue(iterator.Value())
			if !ok {
				return nil, false
			}
			result[iterator.Key().String()] = normalized
		}
		return result, true
	case reflect.Slice, reflect.Array:
		if document, recognized, ok := normalizeMetadataOrderedDocument(value); recognized {
			return document, ok
		}
		result := make([]interface{}, value.Len())
		for i := 0; i < value.Len(); i++ {
			normalized, ok := normalizeMetadataJSONValue(value.Index(i))
			if !ok {
				return nil, false
			}
			result[i] = normalized
		}
		return result, true
	default:
		return nil, false
	}
}

// normalizeMetadataOrderedDocument recognizes BSON's []E representation without
// coupling the datastore-neutral registry package to the MongoDB driver.
func normalizeMetadataOrderedDocument(value reflect.Value) (map[string]interface{}, bool, bool) {
	elementType := value.Type().Elem()
	if elementType.Kind() != reflect.Struct {
		return nil, false, false
	}
	keyField, hasKey := elementType.FieldByName("Key")
	valueField, hasValue := elementType.FieldByName("Value")
	if !hasKey || !hasValue || keyField.Type.Kind() != reflect.String {
		return nil, false, false
	}

	result := make(map[string]interface{}, value.Len())
	for i := 0; i < value.Len(); i++ {
		element := value.Index(i)
		key := element.FieldByIndex(keyField.Index).String()
		if _, duplicate := result[key]; duplicate {
			return nil, true, false
		}
		normalized, ok := normalizeMetadataJSONValue(element.FieldByIndex(valueField.Index))
		if !ok {
			return nil, true, false
		}
		result[key] = normalized
	}
	return result, true, true
}

// DecodeMetadataPatch converts a JSON top-level metadata patch into a map
// where explicit JSON null values are represented as Go nil, and absent keys are omitted.
// This enables store-level atomic merge-patch operations.
func DecodeMetadataPatch(patch []byte) (MetadataPatch, error) {
	if len(patch) == 0 || string(bytes.TrimSpace(patch)) == "null" {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(patch)
	if len(trimmed) > 0 && trimmed[0] != '{' {
		return nil, &BadRequestError{Message: "metadata must be an object"}
	}
	var patchMap map[string]json.RawMessage
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata patch: %w", err)
	}
	result := make(MetadataPatch, len(patchMap))
	for k, v := range patchMap {
		trimmed := bytes.TrimSpace(v)
		if string(trimmed) == "null" {
			result[k] = nil
		} else {
			var val interface{}
			if err := json.Unmarshal(v, &val); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata value for key %q: %w", k, err)
			}
			result[k] = val
		}
	}
	return result, nil
}

// ValidateConversationMetadataPredicates validates that the number of predicates does
// not exceed MaxConversationMetadataPredicates and each predicate has a valid key and operator.
func ValidateConversationMetadataPredicates(predicates []ConversationMetadataPredicate) error {
	if len(predicates) > MaxConversationMetadataPredicates {
		return fmt.Errorf("at most %d metadata filters are allowed", MaxConversationMetadataPredicates)
	}
	for i, p := range predicates {
		if !IsValidMetadataKey(p.Key) {
			return fmt.Errorf("invalid metadata filter key at index %d", i)
		}
		if p.Operator != ConversationMetadataEqual && p.Operator != ConversationMetadataNotEqual {
			return fmt.Errorf("invalid metadata filter operator %q at index %d", p.Operator, i)
		}
	}
	return nil
}

// MetadataFilterFromOptionalPair converts a legacy optional key/value pair into a predicate slice.
// Returns nil, nil if neither field is present. Returns an error if exactly one field is present
// or if the key is invalid.
func MetadataFilterFromOptionalPair(key, value *string) ([]ConversationMetadataPredicate, error) {
	keyPresent := key != nil
	valuePresent := value != nil

	switch {
	case !keyPresent && !valuePresent:
		return nil, nil
	case !keyPresent:
		return nil, fmt.Errorf("metadata filter key is required when metadata filter value is set")
	case !valuePresent:
		return nil, fmt.Errorf("metadata filter value is required when metadata filter key is set")
	}

	if !IsValidMetadataKey(*key) {
		return nil, fmt.Errorf("invalid metadata filter key")
	}

	return []ConversationMetadataPredicate{
		{
			Key:      *key,
			Operator: ConversationMetadataEqual,
			Value:    *value,
		},
	}, nil
}

// ParseMetadataExpression parses a single REST expression string of the form key=value or key!=value.
// It consumes the longest valid key prefix [A-Za-z0-9_-]+, then expects != before =.
func ParseMetadataExpression(expr string, index int) (ConversationMetadataPredicate, error) {
	if expr == "" {
		return ConversationMetadataPredicate{}, fmt.Errorf("invalid metadata filter expression at index %d: empty expression", index)
	}

	// Consume longest valid key prefix
	var keyLen int
	for keyLen < len(expr) {
		r := expr[keyLen]
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			keyLen++
		} else {
			break
		}
	}

	if keyLen == 0 {
		return ConversationMetadataPredicate{}, fmt.Errorf("invalid metadata filter key at index %d", index)
	}

	key := expr[:keyLen]
	rest := expr[keyLen:]

	var op ConversationMetadataOperator
	var val string
	if strings.HasPrefix(rest, "!=") {
		op = ConversationMetadataNotEqual
		val = rest[2:]
	} else if strings.HasPrefix(rest, "=") {
		op = ConversationMetadataEqual
		val = rest[1:]
	} else {
		return ConversationMetadataPredicate{}, fmt.Errorf("invalid metadata filter operator at index %d", index)
	}

	return ConversationMetadataPredicate{
		Key:      key,
		Operator: op,
		Value:    val,
	}, nil
}

// ParseMetadataFilterQuery parses REST query parameters for metadata filters.
// Supports:
// 1. New repeated metadata=expression parameter (e.g. metadata=status=waiting&metadata=agent!=worker)
// 2. Legacy deprecated deepObject metadata[key]=value parameter (equality only)
//
// Mixing legacy and new parameters returns an error.
// More than MaxConversationMetadataPredicates (5) returns an error.
// Malformed query parameters beginning with "metadata" return an error.
func ParseMetadataFilterQuery(rawQuery string) ([]ConversationMetadataPredicate, error) {
	if strings.TrimSpace(rawQuery) == "" {
		return nil, nil
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid metadata filter query: %w", err)
	}

	hasEquals, err := metadataQueryEqualsPresence(rawQuery)
	if err != nil {
		return nil, err
	}

	hasNew := false
	hasLegacy := false
	var legacyParams []string

	for rawKey := range values {
		if rawKey == "metadata" {
			hasNew = true
		} else if strings.HasPrefix(rawKey, "metadata") {
			hasLegacy = true
			legacyParams = append(legacyParams, rawKey)
		}
	}

	if hasNew && hasLegacy {
		return nil, fmt.Errorf("cannot mix repeated metadata filters with legacy metadata[key]=value parameters")
	}

	if hasNew {
		expressions := values["metadata"]
		if len(expressions) > MaxConversationMetadataPredicates {
			return nil, fmt.Errorf("at most %d metadata filters are allowed", MaxConversationMetadataPredicates)
		}
		// Also verify that bare 'metadata' without equals was not provided
		if present, ok := hasEquals["metadata"]; !ok || !present {
			return nil, fmt.Errorf("metadata filter value is required")
		}

		predicates := make([]ConversationMetadataPredicate, 0, len(expressions))
		for i, expr := range expressions {
			pred, err := ParseMetadataExpression(expr, i)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, pred)
		}
		return predicates, nil
	}

	if hasLegacy {
		var predicates []ConversationMetadataPredicate
		for _, rawKey := range legacyParams {
			vals := values[rawKey]
			if !strings.HasPrefix(rawKey, "metadata[") || !strings.HasSuffix(rawKey, "]") {
				return nil, fmt.Errorf("invalid metadata filter parameter %q", rawKey)
			}

			key := rawKey[len("metadata[") : len(rawKey)-1]
			if strings.ContainsAny(key, "[]") || !IsValidMetadataKey(key) {
				return nil, fmt.Errorf("invalid metadata filter key")
			}
			if len(vals) != 1 {
				return nil, fmt.Errorf("metadata filter must have exactly one value")
			}
			if present, ok := hasEquals[rawKey]; !ok || !present {
				return nil, fmt.Errorf("metadata filter value is required when metadata filter key is set")
			}
			if len(predicates) > 0 {
				return nil, fmt.Errorf("metadata filter must specify exactly one key")
			}

			predicates = append(predicates, ConversationMetadataPredicate{
				Key:      key,
				Operator: ConversationMetadataEqual,
				Value:    vals[0],
			})
		}
		return predicates, nil
	}

	return nil, nil
}

func metadataQueryEqualsPresence(rawQuery string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, fragment := range strings.Split(rawQuery, "&") {
		if fragment == "" {
			continue
		}
		name := fragment
		present := false
		if index := strings.IndexByte(fragment, '='); index >= 0 {
			name = fragment[:index]
			present = true
		}
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata filter parameter %q: %w", name, err)
		}
		result[decoded] = present
	}
	return result, nil
}
