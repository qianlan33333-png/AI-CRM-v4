// Package domain contains the channel-neutral tag catalog model.
//
// This package owns catalog and group metadata only.  A customer-to-tag
// relationship is deliberately outside this domain and must not be modelled
// here as a shortcut around the outbound boundary.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const TagLimit = 1000

var (
	ErrInvalidCatalog = errors.New("invalid tag catalog")
	ErrInvalidCommand = errors.New("invalid tag catalog command")
)

// Group is one active local catalog group.  SortOrder is zero-based and is
// owned by this domain; Provider identifiers are intentionally not part of
// this local management projection.
type Group struct {
	ID                    int64      `json:"group_id"`
	Name                  string     `json:"group_name"`
	SortOrder             int32      `json:"sort_order"`
	ProviderMutationState string     `json:"-"`
	ProviderReadbackAt    *time.Time `json:"-"`
}

// Tag is one active local catalog tag. ProviderTagID is a read-only binding;
// management commands continue to use the local ID.
type Tag struct {
	ProviderTagID         string     `json:"provider_tag_id,omitempty"`
	ID                    int64      `json:"tag_id"`
	GroupID               int64      `json:"group_id"`
	GroupName             string     `json:"group_name"`
	Name                  string     `json:"tag_name"`
	SortOrder             int32      `json:"sort_order"`
	ProviderMutationState string     `json:"-"`
	ProviderReadbackAt    *time.Time `json:"-"`
}

// Catalog is a bounded, local snapshot. SyncedAt is the local observation
// time, not proof that a Provider request executed.
type Catalog struct {
	Groups   []Group   `json:"groups"`
	Tags     []Tag     `json:"tags"`
	SyncedAt time.Time `json:"synced_at"`
}

// Command contains server-side actor facts and the fields used by one
// catalog mutation. Actor and IdempotencyKey never come from a provider.
type Command struct {
	Actor                                                     int64
	IdempotencyKey, TraceID, GroupName, FirstTagName, TagName string
	GroupID, TagID                                            int64
	IDs                                                       []int64
}

// ValidateCatalog checks the stable order and cross-record group invariants
// expected by the frozen tags page. It does not sort silently: an adapter
// returning an unstable order fails closed.
func ValidateCatalog(c Catalog) error {
	if c.Groups == nil || c.Tags == nil || len(c.Groups) > TagLimit || len(c.Tags) > TagLimit {
		return ErrInvalidCatalog
	}
	groups := make(map[int64]Group, len(c.Groups))
	for index, group := range c.Groups {
		if !validGroup(group) || (index > 0 && groupLess(group, c.Groups[index-1])) {
			return ErrInvalidCatalog
		}
		if _, exists := groups[group.ID]; exists {
			return ErrInvalidCatalog
		}
		groups[group.ID] = group
	}
	tags := make(map[int64]struct{}, len(c.Tags))
	for index, tag := range c.Tags {
		if !validTag(tag) {
			return ErrInvalidCatalog
		}
		group, exists := groups[tag.GroupID]
		if !exists || group.Name != tag.GroupName {
			return ErrInvalidCatalog
		}
		if index > 0 && tagLess(tag, c.Tags[index-1], groups) {
			return ErrInvalidCatalog
		}
		if _, exists := tags[tag.ID]; exists {
			return ErrInvalidCatalog
		}
		tags[tag.ID] = struct{}{}
	}
	return nil
}

func validGroup(group Group) bool {
	return group.ID > 0 && group.SortOrder >= 0 && validText(group.Name)
}

func validTag(tag Tag) bool {
	return tag.ID > 0 && tag.GroupID > 0 && tag.SortOrder >= 0 && validText(tag.GroupName) && validText(tag.Name)
}

func validText(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 200
}

func groupLess(left, right Group) bool {
	if left.SortOrder != right.SortOrder {
		return left.SortOrder < right.SortOrder
	}
	return left.ID < right.ID
}

func tagLess(left, right Tag, groups map[int64]Group) bool {
	leftGroup, leftExists := groups[left.GroupID]
	rightGroup, rightExists := groups[right.GroupID]
	if leftExists && rightExists && leftGroup.SortOrder != rightGroup.SortOrder {
		return leftGroup.SortOrder < rightGroup.SortOrder
	}
	if left.GroupID != right.GroupID {
		return left.GroupID < right.GroupID
	}
	if left.SortOrder != right.SortOrder {
		return left.SortOrder < right.SortOrder
	}
	return left.ID < right.ID
}

// ValidText is exported for adapters that need to validate request/response
// text without duplicating the frozen 200-rune limit.
func ValidText(value string) bool { return validText(value) }

// ValidIDs verifies a complete reorder list. Partial reorder requests are
// rejected so a stale browser cannot silently scramble a catalog.
func ValidIDs(ids []int64) bool {
	if len(ids) == 0 {
		return false
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

// ValidResultIDs checks mutation receipt identifiers. A group-create result
// intentionally contains one group ID and one tag ID, and those IDs may be
// numerically equal because they come from different sequences; unlike a
// reorder request, duplicate values are therefore allowed here.
func ValidResultIDs(ids []int64) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if id <= 0 {
			return false
		}
	}
	return true
}

// ValidCommand checks fields shared by every local catalog mutation.
func ValidCommand(command Command, values ...string) bool {
	if command.Actor < 1 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 ||
		command.IdempotencyKey != strings.TrimSpace(command.IdempotencyKey) ||
		command.TraceID != strings.TrimSpace(command.TraceID) || len(command.TraceID) > 200 {
		return false
	}
	for _, value := range values {
		if !validText(value) {
			return false
		}
	}
	return true
}

func GroupIDs(groups []Group) []int64 {
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	return ids
}

func TagIDs(tags []Tag) []int64 {
	ids := make([]int64, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
	}
	return ids
}

func SameIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// SameIDSet verifies that two reorder requests contain the same complete set
// of IDs while intentionally ignoring order.  SameIDs remains the ordered
// comparison used for replay receipts; reordering needs both contracts.
func SameIDSet(left, right []int64) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	seen := make(map[int64]struct{}, len(left))
	for _, id := range left {
		if id <= 0 {
			return false
		}
		seen[id] = struct{}{}
	}
	if len(seen) != len(left) {
		return false
	}
	rightSeen := make(map[int64]struct{}, len(right))
	for _, id := range right {
		if _, ok := seen[id]; !ok {
			return false
		}
		if _, duplicate := rightSeen[id]; duplicate {
			return false
		}
		rightSeen[id] = struct{}{}
	}
	return true
}
