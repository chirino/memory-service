package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ConversationArchiveFilter selects candidate conversation groups by archive state.
type ConversationArchiveFilter string

const (
	ConversationArchiveOnly    ConversationArchiveFilter = "only"
	ConversationArchiveExclude ConversationArchiveFilter = "exclude"
	ConversationArchiveInclude ConversationArchiveFilter = "include"
)

// ConversationAgeBasis selects the clock compared with the eviction cutoff.
type ConversationAgeBasis string

const (
	// ConversationAgeArchivedAt uses the candidate group's archive time.
	ConversationAgeArchivedAt ConversationAgeBasis = "archivedAt"
	// ConversationAgeLastActivity uses the newest group creation, conversation
	// creation or update, or entry append in the whole deletion tree: the
	// candidate group plus every started-child descendant group.
	ConversationAgeLastActivity ConversationAgeBasis = "lastActivity"
	// ConversationAgeCreatedAt uses the candidate group's creation time.
	ConversationAgeCreatedAt ConversationAgeBasis = "createdAt"
)

// ConversationEvictionFilter selects conversation groups that eviction may
// delete. A group matches when its selected clock is strictly before Cutoff.
// Deleting a match still removes its whole started-child tree.
type ConversationEvictionFilter struct {
	Archived ConversationArchiveFilter
	AgeBasis ConversationAgeBasis
	Cutoff   time.Time
	// GroupIDs, when non-empty, restricts candidates to these groups.
	GroupIDs []uuid.UUID
	// AfterGroupID, when set, restricts lastActivity candidates to IDs after it.
	// lastActivity results are ordered by ID, so a batch can resume after the
	// last group the previous batch selected.
	AfterGroupID *uuid.UUID
}

// DefaultConversationEvictionFilter selects groups archived before cutoff.
func DefaultConversationEvictionFilter(cutoff time.Time) ConversationEvictionFilter {
	return ConversationEvictionFilter{
		Archived: ConversationArchiveOnly,
		AgeBasis: ConversationAgeArchivedAt,
		Cutoff:   cutoff,
	}
}

// Validate rejects unknown values and combinations that cannot apply.
func (f ConversationEvictionFilter) Validate() error {
	switch f.Archived {
	case ConversationArchiveOnly, ConversationArchiveExclude, ConversationArchiveInclude:
	default:
		return fmt.Errorf("conversationFilter.archived must be one of only, exclude, include")
	}
	switch f.AgeBasis {
	case ConversationAgeArchivedAt:
		if f.Archived != ConversationArchiveOnly {
			return fmt.Errorf("conversationFilter.ageBasis archivedAt requires conversationFilter.archived only")
		}
	case ConversationAgeLastActivity, ConversationAgeCreatedAt:
	default:
		return fmt.Errorf("conversationFilter.ageBasis must be one of archivedAt, lastActivity, createdAt")
	}
	if f.AfterGroupID != nil && f.AgeBasis != ConversationAgeLastActivity {
		return fmt.Errorf("an eviction cursor requires the lastActivity age basis")
	}
	return nil
}

// EvictionScanChunk bounds how many lastActivity candidates have their trees
// walked at once.
const EvictionScanChunk = 500

// ScanIdleEvictionCandidates visits up to limit lastActivity matches in ID
// order, or all of them when limit is zero. page returns up to n candidates
// after the given ID (nil for the first page), ordered by ID; idle returns the
// subset of a page whose trees have no activity at or after the cutoff. Only
// one page of trees is walked at a time, so a limited scan stops early.
func ScanIdleEvictionCandidates(filter ConversationEvictionFilter, limit int, page func(after *uuid.UUID, n int) ([]uuid.UUID, error), idle func([]uuid.UUID) ([]uuid.UUID, error), visit func(uuid.UUID)) error {
	after := filter.AfterGroupID
	visited := 0
	for {
		candidates, err := page(after, EvictionScanChunk)
		if err != nil || len(candidates) == 0 {
			return err
		}
		idleIDs, err := idle(candidates)
		if err != nil {
			return err
		}
		keep := make(map[uuid.UUID]bool, len(idleIDs))
		for _, id := range idleIDs {
			keep[id] = true
		}
		for _, id := range candidates {
			if !keep[id] {
				continue
			}
			visit(id)
			visited++
			if limit > 0 && visited >= limit {
				return nil
			}
		}
		if len(candidates) < EvictionScanChunk {
			return nil
		}
		last := candidates[len(candidates)-1]
		after = &last
	}
}

// EvictionCandidatesSQL builds a SQL query selecting the id of every group
// that matches filter's archive state, candidate restrictions, and own clock.
// For lastActivity that clock is the group's creation, which only prunes
// candidates; IdleEvictionTreesSQL checks their trees. compare renders a
// comparison between a timestamp column and filter.Cutoff and returns its
// bind arguments, so each SQL datastore controls its timestamp encoding.
func EvictionCandidatesSQL(filter ConversationEvictionFilter, compare func(column, op string, at time.Time) (string, []any)) (string, []any) {
	var args []any
	conditions := []string{}
	switch filter.Archived {
	case ConversationArchiveOnly:
		conditions = append(conditions, "g.archived_at IS NOT NULL")
	case ConversationArchiveExclude:
		conditions = append(conditions, "g.archived_at IS NULL")
	}
	column := "g.created_at"
	if filter.AgeBasis == ConversationAgeArchivedAt {
		column = "g.archived_at"
	}
	sql, values := compare(column, "<", filter.Cutoff)
	conditions = append(conditions, sql)
	args = append(args, values...)
	if len(filter.GroupIDs) > 0 {
		conditions = append(conditions, "g.id IN ?")
		args = append(args, filter.GroupIDs)
	}
	if filter.AfterGroupID != nil {
		conditions = append(conditions, "g.id > ?")
		args = append(args, *filter.AfterGroupID)
	}
	return "SELECT g.id FROM conversation_groups g WHERE " + strings.Join(conditions, " AND "), args
}

// IdleEvictionTreesSQL builds a SQL query selecting the groups in ids whose
// whole tree has no group creation, conversation creation or update, or entry
// at or after cutoff. The tree is the one conversation deletion removes: every
// conversation in a group, including forks, can start child groups.
func IdleEvictionTreesSQL(ids []uuid.UUID, cutoff time.Time, compare func(column, op string, at time.Time) (string, []any)) (string, []any) {
	args := []any{ids}
	cmp := func(column string) string {
		sql, values := compare(column, ">=", cutoff)
		args = append(args, values...)
		return sql
	}
	query := `WITH RECURSIVE tree (root_id, group_id) AS (
	SELECT g.id, g.id FROM conversation_groups g WHERE g.id IN ?
	UNION
	SELECT tree.root_id, child.conversation_group_id
	FROM tree
	JOIN conversations parent ON parent.conversation_group_id = tree.group_id
	JOIN conversations child ON child.started_by_conversation_id = parent.id
)
SELECT DISTINCT tree.root_id AS id FROM tree
WHERE NOT EXISTS (
	SELECT 1 FROM tree active
	WHERE active.root_id = tree.root_id AND (
		EXISTS (SELECT 1 FROM conversation_groups ag WHERE ag.id = active.group_id AND ` + cmp("ag.created_at") + `)
		OR EXISTS (SELECT 1 FROM conversations ac WHERE ac.conversation_group_id = active.group_id AND (` + cmp("ac.created_at") + ` OR ` + cmp("ac.updated_at") + `))
		OR EXISTS (SELECT 1 FROM entries ae WHERE ae.conversation_group_id = active.group_id AND ` + cmp("ae.created_at") + `)
	)
)`
	return query, args
}
