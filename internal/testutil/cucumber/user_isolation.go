package cucumber

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

func (s *TestScenario) RegisterCanonicalUsers(names ...string) {
	if s.userAliases == nil {
		s.userAliases = map[string]string{}
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := s.userAliases[name]; !ok {
			s.userAliases[name] = name + "-" + s.ScenarioUID
		}
	}
}

func (s *TestScenario) IsolatedUser(name string) string {
	if isolated, ok := s.userAliases[name]; ok {
		return isolated
	}
	return name
}

func (s *TestScenario) NormalizeUsers(text string) string {
	for canonical, isolated := range s.userAliases {
		text = strings.ReplaceAll(text, isolated, canonical)
	}
	return text
}

func (s *TestScenario) RewriteQuotedUsers(text string) string {
	for canonical, isolated := range s.userAliases {
		text = strings.ReplaceAll(text, `"`+canonical+`"`, `"`+isolated+`"`)
		text = strings.ReplaceAll(text, `'`+canonical+`'`, `'`+isolated+`'`)
		text = strings.ReplaceAll(text, "Bearer "+canonical, "Bearer "+isolated)
	}
	return text
}

func (s *TestScenario) RewriteRequestPath(path string) string {
	parsed, err := url.Parse(path)
	if err != nil {
		return path
	}

	segments := strings.Split(parsed.Path, "/")
	for i, segment := range segments {
		if segment == "" {
			continue
		}
		segments[i] = s.IsolatedUser(segment)
	}
	parsed.Path = strings.Join(segments, "/")

	values := parsed.Query()
	for key, vals := range values {
		for i, val := range vals {
			vals[i] = s.IsolatedUser(val)
		}
		values[key] = vals
	}
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func (s *TestScenario) RewriteRequestBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		parsed = s.rewriteStructuredUsers(parsed)
		if out, err := json.Marshal(parsed); err == nil {
			return string(out)
		}
	}

	return s.RewriteQuotedUsers(body)
}

func (s *TestScenario) NormalizeValue(value interface{}) interface{} {
	return s.normalizeStructuredUsers(value)
}

func (s *TestScenario) NormalizeResponseBody(requestURL, contentType string, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var parsed interface{}
	if strings.Contains(contentType, "json") || json.Valid(body) {
		if err := json.Unmarshal(body, &parsed); err == nil {
			parsed = s.filterAdminCollectionResponse(requestURL, parsed)
			parsed = s.normalizeStructuredUsers(parsed)
			return json.Marshal(parsed)
		}
	}

	if strings.Contains(contentType, "text/") || utf8.Valid(body) {
		return []byte(s.NormalizeUsers(string(body))), nil
	}

	return body, nil
}

func (s *TestScenario) rewriteStructuredUsers(value interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		return s.IsolatedUser(typed)
	case []map[string]interface{}:
		if typed == nil {
			return typed
		}
		out := make([]map[string]interface{}, len(typed))
		for i := range typed {
			out[i] = s.rewriteStructuredUsers(typed[i]).(map[string]interface{})
		}
		return out
	case []interface{}:
		if typed == nil {
			return typed
		}
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = s.rewriteStructuredUsers(typed[i])
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, val := range typed {
			out[key] = s.rewriteStructuredUsers(val)
		}
		return out
	default:
		return value
	}
}

func (s *TestScenario) normalizeStructuredUsers(value interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		return s.NormalizeUsers(typed)
	case []map[string]interface{}:
		if typed == nil {
			return typed
		}
		out := make([]map[string]interface{}, len(typed))
		for i := range typed {
			out[i] = s.normalizeStructuredUsers(typed[i]).(map[string]interface{})
		}
		return out
	case []interface{}:
		if typed == nil {
			return typed
		}
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = s.normalizeStructuredUsers(typed[i])
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, val := range typed {
			out[key] = s.normalizeStructuredUsers(val)
		}
		return out
	default:
		return value
	}
}

func (s *TestScenario) filterAdminCollectionResponse(requestURL string, value interface{}) interface{} {
	parsedURL, err := url.Parse(requestURL)
	if err != nil || !isAdminCollectionPath(parsedURL.Path) {
		return value
	}

	root, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	items, ok := root["data"].([]interface{})
	if !ok {
		return value
	}

	filtered := make([]interface{}, 0, len(items))
	for _, item := range items {
		if s.keepAdminCollectionItem(item) {
			filtered = append(filtered, item)
		}
	}
	root["data"] = filtered
	return root
}

func isAdminCollectionPath(path string) bool {
	switch path {
	case "/v1/admin/conversations", "/v1/admin/conversations/search", "/v1/admin/attachments":
		return true
	default:
		return false
	}
}

func (s *TestScenario) keepAdminCollectionItem(value interface{}) bool {
	return s.containsCurrentScenarioUser(value) || !s.containsAnyKnownIsolatedUser(value)
}

func (s *TestScenario) containsCurrentScenarioUser(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		for _, isolated := range s.userAliases {
			if typed == isolated {
				return true
			}
		}
		return false
	case []interface{}:
		for _, item := range typed {
			if s.containsCurrentScenarioUser(item) {
				return true
			}
		}
		return false
	case map[string]interface{}:
		for _, item := range typed {
			if s.containsCurrentScenarioUser(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *TestScenario) containsAnyKnownIsolatedUser(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		for canonical := range s.userAliases {
			if isIsolatedCanonicalUser(typed, canonical) {
				return true
			}
		}
		return false
	case []interface{}:
		for _, item := range typed {
			if s.containsAnyKnownIsolatedUser(item) {
				return true
			}
		}
		return false
	case map[string]interface{}:
		for _, item := range typed {
			if s.containsAnyKnownIsolatedUser(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func isIsolatedCanonicalUser(value, canonical string) bool {
	if !strings.HasPrefix(value, canonical+"-") {
		return false
	}
	suffix := strings.TrimPrefix(value, canonical+"-")
	if len(suffix) != 8 {
		return false
	}
	for _, ch := range suffix {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (s *TestScenario) FilterQueryRows(rows []map[string]interface{}) []map[string]interface{} {
	if rows == nil {
		return nil
	}

	knownValues := map[string]struct{}{}
	for _, isolated := range s.userAliases {
		knownValues[isolated] = struct{}{}
	}
	for _, value := range s.Variables {
		if text, ok := value.(string); ok && text != "" {
			knownValues[text] = struct{}{}
		}
	}
	if len(knownValues) == 0 {
		return rows
	}

	filtered := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		if rowContainsKnownValue(row, knownValues) {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) == 0 {
		return rows
	}
	return filtered
}

func rowContainsKnownValue(value interface{}, knownValues map[string]struct{}) bool {
	switch typed := value.(type) {
	case string:
		_, ok := knownValues[typed]
		return ok
	case []interface{}:
		for _, item := range typed {
			if rowContainsKnownValue(item, knownValues) {
				return true
			}
		}
		return false
	case map[string]interface{}:
		for _, item := range typed {
			if rowContainsKnownValue(item, knownValues) {
				return true
			}
		}
		return false
	default:
		if value == nil {
			return false
		}
		_, ok := knownValues[fmt.Sprintf("%v", value)]
		return ok
	}
}
