package operationevent

import (
	"reflect"
	"sort"
)

const (
	maxReturnedErrorDetails = 8
	maxTraversedErrors      = 64
)

// ErrorDetailsProvider contains privacy-safe provider metadata.
type ErrorDetailsProvider struct {
	Name          string `json:"name,omitempty"`
	StatusCode    int    `json:"statusCode,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	TransactionID string `json:"transactionID,omitempty"`
}

// ErrorDetails describes a privacy-safe typed error. Diagnostic values must not contain raw errors.
type ErrorDetails struct {
	ErrorType string                `json:"errorType,omitempty"`
	ErrorCode string                `json:"errorCode,omitempty"`
	Reason    string                `json:"reason,omitempty"`
	Provider  *ErrorDetailsProvider `json:"provider,omitempty"`
}

// ErrorDetailsEntry records one typed cause and its position in an unwrap tree.
type ErrorDetailsEntry struct {
	ErrorType string                `json:"errorType,omitempty"`
	ErrorCode string                `json:"errorCode,omitempty"`
	Reason    string                `json:"reason,omitempty"`
	Provider  *ErrorDetailsProvider `json:"provider,omitempty"`
	Branch    string                `json:"branch,omitempty"`
	Depth     int                   `json:"depth"`
}

// ErrorDetailer exposes bounded, privacy-safe operational diagnostics.
type ErrorDetailer interface {
	OperationErrorDetails() ErrorDetails
}

type detailedError struct {
	err     error
	details ErrorDetails
}

func (e *detailedError) Error() string                       { return e.err.Error() }
func (e *detailedError) Unwrap() error                       { return e.err }
func (e *detailedError) OperationErrorDetails() ErrorDetails { return e.details }

// WithErrorDetails wraps err with privacy-safe typed operational diagnostics.
func WithErrorDetails(err error, details ErrorDetails) error {
	if err == nil {
		return nil
	}
	return &detailedError{err: err, details: details}
}

type errorNode struct {
	err    error
	branch string
	depth  int
	order  int
}

// EnrichError collects typed details from a terminal error only.
func (e *Event) EnrichError(err error) {
	if e == nil || isNilError(err) {
		return
	}
	details, compatibility, truncated := collectErrorDetails(err)
	if len(details) == 0 {
		return
	}
	e.mu.Lock()
	// Compatibility fields use the first match in stable traversal order.
	e.fields.ErrorType = compatibility.ErrorType
	e.fields.ErrorCode = compatibility.ErrorCode
	e.fields.Reason = compatibility.Reason
	e.fields.ProviderName = ""
	e.fields.ProviderStatusCode = 0
	e.fields.ProviderErrorCode = ""
	e.fields.ProviderTransactionID = ""
	if compatibility.Provider != nil {
		e.fields.ProviderName = compatibility.Provider.Name
		e.fields.ProviderStatusCode = compatibility.Provider.StatusCode
		e.fields.ProviderErrorCode = compatibility.Provider.ErrorCode
		e.fields.ProviderTransactionID = compatibility.Provider.TransactionID
	}
	e.fields.ErrorDetails = append([]ErrorDetailsEntry(nil), details...)
	e.fields.ErrorDetailsTruncated = e.fields.ErrorDetailsTruncated || truncated
	e.mu.Unlock()
}

func collectErrorDetails(root error) ([]ErrorDetailsEntry, ErrorDetailsEntry, bool) {
	if isNilError(root) {
		return nil, ErrorDetailsEntry{}, false
	}
	stack := []errorNode{{err: root}}
	seen := make(map[errorIdentity]struct{})
	entries := make([]struct {
		ErrorDetailsEntry
		order int
	}, 0, maxReturnedErrorDetails)
	traversed := 0
	truncated := false
	var compatibility ErrorDetailsEntry
	compatibilitySet := false
	order := 0
	for len(stack) > 0 {
		if traversed >= maxTraversedErrors {
			truncated = true
			break
		}
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if isNilError(node.err) {
			continue
		}
		if id, ok := identityOf(node.err); ok {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
		}
		traversed++
		detailer := directDetailer(node.err)
		if !isNilDetailer(detailer) {
			d := sanitizeDetails(detailer.OperationErrorDetails())
			if d.ErrorType != "" || d.ErrorCode != "" || d.Reason != "" || d.Provider != nil {
				entry := ErrorDetailsEntry{
					ErrorType: d.ErrorType,
					ErrorCode: d.ErrorCode,
					Reason:    d.Reason,
					Provider:  d.Provider,
					Branch:    node.branch,
					Depth:     node.depth,
				}
				if !compatibilitySet {
					compatibility = entry
					compatibilitySet = true
				}
				entries = append(entries, struct {
					ErrorDetailsEntry
					order int
				}{ErrorDetailsEntry: entry, order: order})
				order++
			}
		}

		children := unwrapErrors(node.err)
		for i := len(children) - 1; i >= 0; i-- {
			branch := itoa(i)
			if node.branch != "" {
				branch = node.branch + "." + branch
			}
			stack = append(stack, errorNode{err: children[i], branch: branch, depth: node.depth + 1})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Depth != entries[j].Depth {
			return entries[i].Depth > entries[j].Depth
		}
		return entries[i].order < entries[j].order
	})
	if len(entries) > maxReturnedErrorDetails {
		entries = entries[:maxReturnedErrorDetails]
		truncated = true
	}
	result := make([]ErrorDetailsEntry, len(entries))
	for i := range entries {
		result[i] = entries[i].ErrorDetailsEntry
	}
	return result, compatibility, truncated
}

func directDetailer(err error) ErrorDetailer {
	if detailer, ok := err.(ErrorDetailer); ok {
		return detailer
	}
	// Honor a node's custom As implementation without recursively searching its children.
	if aser, ok := err.(interface{ As(any) bool }); ok {
		var detailer ErrorDetailer
		if aser.As(&detailer) {
			return detailer
		}
	}
	return nil
}

func sanitizeDetails(d ErrorDetails) ErrorDetails {
	d.ErrorType = sanitize(d.ErrorType, 128)
	d.ErrorCode = sanitize(d.ErrorCode, 128)
	d.Reason = sanitize(d.Reason, 128)
	if d.Provider != nil {
		copy := *d.Provider
		copy.Name = sanitize(copy.Name, 128)
		copy.ErrorCode = sanitize(copy.ErrorCode, 128)
		copy.TransactionID = sanitize(copy.TransactionID, 256)
		d.Provider = &copy
	}
	return d
}

func unwrapErrors(err error) []error {
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		return many.Unwrap()
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return []error{one.Unwrap()}
	}
	return nil
}

type errorIdentity struct {
	typeName reflect.Type
	pointer  uintptr
}

func identityOf(err error) (errorIdentity, bool) {
	v := reflect.ValueOf(err)
	if !v.IsValid() {
		return errorIdentity{}, false
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return errorIdentity{}, false
		}
		return errorIdentity{typeName: v.Type(), pointer: v.Pointer()}, true
	default:
		return errorIdentity{}, false
	}
}

func isNilError(err error) bool {
	if err == nil {
		return true
	}
	v := reflect.ValueOf(err)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func isNilDetailer(detailer ErrorDetailer) bool {
	if detailer == nil {
		return true
	}
	v := reflect.ValueOf(detailer)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func itoa(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	// Branch indices are bounded by traversed nodes, so this fallback is sufficient.
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
