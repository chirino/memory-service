package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// SequencedAppendRequest is the normalized identity and epoch context used to
// compare a failed sequenced append with entries that already exist.
type SequencedAppendRequest struct {
	Entries  []CreateEntryRequest
	UserID   string
	ClientID *string
	AgentID  *string
	Epoch    *int64
}

type SequencedAppendMatch struct {
	Entries     []model.Entry
	Exact       bool
	AnyExisting bool
}

func FindSequencedAppendMatch(ctx context.Context, store MemoryStore, conversationID string, request SequencedAppendRequest) (SequencedAppendMatch, error) {
	sequences, eligible := request.Sequences()
	if !eligible {
		return SequencedAppendMatch{}, nil
	}
	stored, err := store.GetEntriesBySequence(ctx, request.UserID, conversationID, sequences)
	if err != nil {
		return SequencedAppendMatch{}, err
	}
	matched, exact := request.MatchExistingEntries(stored)
	if !exact && len(stored) == len(request.Entries) {
		matched, exact, err = request.matchExistingEntriesWithAttachmentIdentity(ctx, store, stored)
		if err != nil {
			return SequencedAppendMatch{}, err
		}
	}
	return SequencedAppendMatch{Entries: matched, Exact: exact, AnyExisting: len(stored) > 0}, nil
}

// RepairStoredEntryAttachmentLinks completes attachment linking for an entry that
// was durably inserted before a later append step failed. Only direct history
// content attachments are considered.
func RepairStoredEntryAttachmentLinks(ctx context.Context, store MemoryStore, userID string, entry model.Entry) error {
	if entry.Channel != model.ChannelHistory {
		return nil
	}
	var content []struct {
		Attachments []struct {
			AttachmentID string `json:"attachmentId"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(entry.Content, &content); err != nil {
		return nil
	}
	for _, item := range content {
		for _, ref := range item.Attachments {
			attachmentID, err := uuid.Parse(ref.AttachmentID)
			if err != nil {
				continue
			}
			if _, err := store.LinkAttachmentToEntry(ctx, userID, attachmentID, entry.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Sequences returns the request sequence values when every entry is sequenced.
// It returns false for unsequenced, mixed, or internally duplicated requests.
func (r SequencedAppendRequest) Sequences() ([]uint32, bool) {
	sequences := make([]uint32, 0, len(r.Entries))
	seen := make(map[uint32]struct{}, len(r.Entries))
	for _, entry := range r.Entries {
		if entry.Seq == nil {
			return nil, false
		}
		if _, found := seen[*entry.Seq]; found {
			return nil, false
		}
		seen[*entry.Seq] = struct{}{}
		sequences = append(sequences, *entry.Seq)
	}
	return sequences, len(sequences) > 0
}

// MatchExistingEntries returns the stored entries in request order when every
// persisted field matches. Server-assigned IDs and timestamps are not compared.
func (r SequencedAppendRequest) MatchExistingEntries(stored []model.Entry) ([]model.Entry, bool) {
	sequences, eligible := r.Sequences()
	if !eligible || len(stored) != len(sequences) {
		return nil, false
	}

	bySequence := make(map[uint32]model.Entry, len(stored))
	for _, entry := range stored {
		if entry.Seq == nil {
			return nil, false
		}
		bySequence[*entry.Seq] = entry
	}

	matched := make([]model.Entry, 0, len(r.Entries))
	for _, requested := range r.Entries {
		existing, found := bySequence[*requested.Seq]
		if !found || !r.entryMatches(requested, existing) {
			return nil, false
		}
		matched = append(matched, existing)
	}
	return matched, true
}

func (r SequencedAppendRequest) entryMatches(requested CreateEntryRequest, existing model.Entry) bool {
	channel := model.Channel(strings.ToLower(requested.Channel))
	if channel == "" {
		channel = model.ChannelHistory
	}
	if existing.Seq == nil || requested.Seq == nil || *existing.Seq != *requested.Seq ||
		existing.Channel != channel || existing.ContentType != requested.ContentType ||
		!stringPointersEqual(existing.UserID, &r.UserID) ||
		!stringPointersEqual(existing.ClientID, r.ClientID) ||
		!stringPointersEqual(existing.AgentID, r.AgentID) ||
		!int64PointersEqual(existing.Epoch, EpochForChannel(channel, r.Epoch)) ||
		!stringPointersEqual(existing.IndexedContent, requested.IndexedContent) {
		return false
	}
	return jsonValuesEqual(existing.Content, requested.Content)
}

func jsonValuesEqual(left, right []byte) bool {
	if bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right)) {
		return true
	}
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	leftErr := leftDecoder.Decode(&leftValue)
	rightErr := rightDecoder.Decode(&rightValue)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return jsonSemanticEqual(leftValue, rightValue)
}

func jsonSemanticEqual(left, right any) bool {
	switch leftValue := left.(type) {
	case json.Number:
		rightValue, ok := right.(json.Number)
		if !ok {
			return false
		}
		leftNumber, leftOK := normalizeJSONNumber(leftValue)
		rightNumber, rightOK := normalizeJSONNumber(rightValue)
		return leftOK && rightOK && leftNumber == rightNumber
	case []any:
		rightValue, ok := right.([]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for i := range leftValue {
			if !jsonSemanticEqual(leftValue[i], rightValue[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		rightValue, ok := right.(map[string]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for key, value := range leftValue {
			rightItem, found := rightValue[key]
			if !found || !jsonSemanticEqual(value, rightItem) {
				return false
			}
		}
		return true
	default:
		return left == right
	}
}

const (
	maxJSONNumberDigits   = 4096
	maxJSONNumberExponent = 10000
)

type normalizedJSONNumber struct {
	negative bool
	digits   string
	exponent int
}

// normalizeJSONNumber produces an exact decimal representation without
// materializing the number's magnitude. Inputs outside the bounded lexical
// limits fail closed instead of allocating powers of ten.
func normalizeJSONNumber(number json.Number) (normalizedJSONNumber, bool) {
	value := string(number)
	negative := false
	if strings.HasPrefix(value, "-") {
		negative = true
		value = value[1:]
	}

	mantissa := value
	exponent := 0
	if exponentAt := strings.IndexAny(value, "eE"); exponentAt >= 0 {
		mantissa = value[:exponentAt]
		exponentText := value[exponentAt+1:]
		exponentNegative := false
		if strings.HasPrefix(exponentText, "+") || strings.HasPrefix(exponentText, "-") {
			exponentNegative = exponentText[0] == '-'
			exponentText = exponentText[1:]
		}
		if exponentText == "" {
			return normalizedJSONNumber{}, false
		}
		for _, digit := range exponentText {
			if digit < '0' || digit > '9' {
				return normalizedJSONNumber{}, false
			}
			exponent = exponent*10 + int(digit-'0')
			if exponent > maxJSONNumberExponent {
				return normalizedJSONNumber{}, false
			}
		}
		if exponentNegative {
			exponent = -exponent
		}
	}

	integerPart := mantissa
	fractionPart := ""
	if decimalAt := strings.IndexByte(mantissa, '.'); decimalAt >= 0 {
		integerPart = mantissa[:decimalAt]
		fractionPart = mantissa[decimalAt+1:]
	}
	if integerPart == "" || len(integerPart)+len(fractionPart) > maxJSONNumberDigits {
		return normalizedJSONNumber{}, false
	}
	digits := integerPart + fractionPart
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return normalizedJSONNumber{}, false
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return normalizedJSONNumber{digits: "0"}, true
	}
	exponent -= len(fractionPart)
	for strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		exponent++
	}
	return normalizedJSONNumber{negative: negative, digits: digits, exponent: exponent}, true
}

func (r SequencedAppendRequest) matchExistingEntriesWithAttachmentIdentity(ctx context.Context, store MemoryStore, stored []model.Entry) ([]model.Entry, bool, error) {
	matched, eligible := r.MatchExistingEntriesIgnoringContent(stored)
	if !eligible {
		return nil, false, nil
	}
	for i, requested := range r.Entries {
		channel := model.Channel(strings.ToLower(requested.Channel))
		if channel == "" {
			channel = model.ChannelHistory
		}
		if channel != model.ChannelHistory {
			return nil, false, nil
		}
		equal, err := jsonValuesEqualByAttachmentIdentity(ctx, store, r.UserID, requested.Content, matched[i].Content)
		if err != nil || !equal {
			return nil, false, err
		}
	}
	return matched, true, nil
}

func (r SequencedAppendRequest) MatchExistingEntriesIgnoringContent(stored []model.Entry) ([]model.Entry, bool) {
	r.Entries = append([]CreateEntryRequest(nil), r.Entries...)
	for i := range r.Entries {
		r.Entries[i].Content = storedContentPlaceholder
	}
	clonedStored := append([]model.Entry(nil), stored...)
	for i := range clonedStored {
		clonedStored[i].Content = storedContentPlaceholder
	}
	_, exact := r.MatchExistingEntries(clonedStored)
	if !exact {
		return nil, false
	}
	bySequence := make(map[uint32]model.Entry, len(stored))
	for _, entry := range stored {
		bySequence[*entry.Seq] = entry
	}
	matched := make([]model.Entry, 0, len(r.Entries))
	for _, entry := range r.Entries {
		matched = append(matched, bySequence[*entry.Seq])
	}
	return matched, true
}

var storedContentPlaceholder = json.RawMessage(`null`)

func jsonValuesEqualByAttachmentIdentity(ctx context.Context, store MemoryStore, userID string, left, right []byte) (bool, error) {
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	leftErr := leftDecoder.Decode(&leftValue)
	rightErr := rightDecoder.Decode(&rightValue)
	if leftErr != nil || rightErr != nil {
		return false, nil
	}
	leftContent, leftIsContent := leftValue.([]any)
	rightContent, rightIsContent := rightValue.([]any)
	if !leftIsContent || !rightIsContent || len(leftContent) != len(rightContent) {
		return false, nil
	}
	for contentIndex := range leftContent {
		leftBlock, leftIsBlock := leftContent[contentIndex].(map[string]any)
		rightBlock, rightIsBlock := rightContent[contentIndex].(map[string]any)
		if !leftIsBlock || !rightIsBlock {
			continue
		}
		leftAttachments, leftHasAttachments := leftBlock["attachments"].([]any)
		rightAttachments, rightHasAttachments := rightBlock["attachments"].([]any)
		if !leftHasAttachments || !rightHasAttachments || len(leftAttachments) != len(rightAttachments) {
			continue
		}
		for attachmentIndex := range leftAttachments {
			leftAttachmentObject, leftIsAttachment := leftAttachments[attachmentIndex].(map[string]any)
			rightAttachmentObject, rightIsAttachment := rightAttachments[attachmentIndex].(map[string]any)
			if !leftIsAttachment || !rightIsAttachment {
				continue
			}
			leftID, leftHasID := leftAttachmentObject["attachmentId"].(string)
			rightID, rightHasID := rightAttachmentObject["attachmentId"].(string)
			if !leftHasID || !rightHasID || leftID == rightID {
				continue
			}
			leftUUID, leftErr := uuid.Parse(leftID)
			rightUUID, rightErr := uuid.Parse(rightID)
			if leftErr != nil || rightErr != nil {
				return false, nil
			}
			leftAttachment, err := store.GetAttachment(ctx, userID, "", leftUUID)
			if err != nil {
				return false, err
			}
			rightAttachment, err := store.GetAttachment(ctx, userID, "", rightUUID)
			if err != nil {
				return false, err
			}
			if leftAttachment.StorageKey == nil || rightAttachment.StorageKey == nil || *leftAttachment.StorageKey != *rightAttachment.StorageKey {
				return false, nil
			}
			leftAttachmentObject["attachmentId"] = rightID
		}
	}
	return jsonSemanticEqual(leftValue, rightValue), nil
}

func stringPointersEqual(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func int64PointersEqual(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// EpochForChannel applies epoch semantics to an entry. Epochs belong only to

func uuidPointersEqual(left, right *uuid.UUID) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// context entries; context defaults to epoch 1 when the request omits one.
func EpochForChannel(channel model.Channel, requested *int64) *int64 {
	if channel != model.ChannelContext {
		return nil
	}
	if requested != nil {
		return requested
	}
	one := int64(1)
	return &one
}

// ValidateEntryEpochChannels rejects request-level epochs on batches containing
// entries from channels where epochs have no meaning.
func ValidateEntryEpochChannels(entries []CreateEntryRequest, epoch *int64) error {
	if epoch == nil {
		return nil
	}
	for _, entry := range entries {
		channel := model.Channel(strings.ToLower(entry.Channel))
		if channel == "" {
			channel = model.ChannelHistory
		}
		if channel != model.ChannelContext {
			return fmt.Errorf("epoch can only be set for context entries; entry channel %q does not support epochs", channel)
		}
	}
	return nil
}

// EntryLookupQuery builds the bounded listing query used when an entry must be
// loaded through normal conversation visibility rules.
func EntryLookupQuery(entryID uuid.UUID, channel *model.Channel, clientID *string) EntryListQuery {
	entryIDString := entryID.String()
	return EntryListQuery{
		Limit:       1,
		Channel:     channel,
		ClientID:    clientID,
		AllForks:    true,
		UpToEntryID: &entryIDString,
		Tail:        true,
	}
}

// AdminEntryLookupQuery builds the equivalent bounded admin lookup.
func AdminEntryLookupQuery(entryID uuid.UUID) AdminMessageQuery {
	entryIDString := entryID.String()
	return AdminMessageQuery{
		Limit:       1,
		AllForks:    true,
		UpToEntryID: &entryIDString,
		Tail:        true,
	}
}

// TrimEntriesToVisiblePrefix keeps entries that are part of the visible prefix
// ending at upToEntryID. The visible slice should already reflect the caller's
// fork visibility, while entries may have additional channel or epoch filters.
func TrimEntriesToVisiblePrefix(entries []model.Entry, visible []model.Entry, upToEntryID *string) ([]model.Entry, error) {
	if upToEntryID == nil || *upToEntryID == "" {
		return entries, nil
	}

	visibleIDs := make(map[string]struct{})
	found := false
	for _, entry := range visible {
		id := entry.ID.String()
		visibleIDs[id] = struct{}{}
		if id == *upToEntryID {
			found = true
			break
		}
	}
	if !found {
		return nil, &NotFoundError{Resource: "entry", ID: *upToEntryID}
	}

	filtered := entries[:0]
	for _, entry := range entries {
		if _, ok := visibleIDs[entry.ID.String()]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

// PaginateEntries applies bidirectional pagination to a fully-filtered ascending
// entry slice and returns (page, afterCursor, beforeCursor, err).
//
//   - tail=true: return the last limit entries (page[len-limit:]).
//   - beforeCursor set: return up to limit entries strictly before the anchor.
//   - afterCursor set: return the first limit entries strictly after the anchor.
//   - otherwise: return the first limit entries.
//
// The returned page is always in ascending (chronological) order.
// afterCursor is the ID of the last entry when a newer entry exists, nil otherwise.
// beforeCursor is the ID of the first entry when an older entry exists, nil otherwise.
func PaginateEntries(
	entries []model.Entry,
	afterEntryID *string,
	beforeEntryID *string,
	tail bool,
	limit int,
) (page []model.Entry, afterCursor, beforeCursor *string, err error) {
	if limit <= 0 {
		limit = 50
	}

	n := len(entries)
	if n == 0 {
		return []model.Entry{}, nil, nil, nil
	}

	if tail {
		// Return the last `limit` entries.
		start := n - limit
		if start < 0 {
			start = 0
		}
		page = entries[start:]
		if start > 0 {
			c := entries[start].ID.String()
			beforeCursor = &c
		}
		// afterCursor is nil (this is the newest page).
		return page, nil, beforeCursor, nil
	}

	if beforeEntryID != nil {
		// Find the anchor position.
		anchorIdx := -1
		for i, e := range entries {
			if e.ID.String() == *beforeEntryID {
				anchorIdx = i
				break
			}
		}
		if anchorIdx < 0 {
			return nil, nil, nil, fmt.Errorf("beforeCursor entry not found in visible results")
		}
		// Take the `limit` entries ending just before the anchor.
		end := anchorIdx // exclusive
		start := end - limit
		if start < 0 {
			start = 0
		}
		page = entries[start:end]
		if len(page) == 0 {
			return []model.Entry{}, nil, nil, nil
		}
		// beforeCursor: there are older entries if start > 0.
		if start > 0 {
			c := entries[start].ID.String()
			beforeCursor = &c
		}
		// afterCursor: there are newer entries (the anchor page and beyond).
		if anchorIdx < n {
			c := entries[end-1].ID.String()
			afterCursor = &c
		}
		return page, afterCursor, beforeCursor, nil
	}

	// Forward pagination (afterCursor or from the beginning).
	start := 0
	if afterEntryID != nil {
		found := false
		for i, e := range entries {
			if e.ID.String() == *afterEntryID {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, nil, nil, fmt.Errorf("afterCursor entry not found in visible results")
		}
	}
	if start >= n {
		return []model.Entry{}, nil, nil, nil
	}
	end := start + limit
	if end > n {
		end = n
	}
	page = entries[start:end]
	// afterCursor: there are newer entries if end < n.
	if end < n && len(page) > 0 {
		c := page[len(page)-1].ID.String()
		afterCursor = &c
	}
	// beforeCursor: there are older entries if start > 0.
	if start > 0 && len(page) > 0 {
		c := page[0].ID.String()
		beforeCursor = &c
	}
	return page, afterCursor, beforeCursor, nil
}
