package domain

import (
	"strings"
	"time"
)

// Update applies a group rename/reorder under an explicit compare-and-swap
// version. Group deletion remains a Store operation because package membership
// must be checked transactionally.
func (g *Group) Update(name string, sortOrder int, expectedVersion, actor int64, now time.Time) error {
	kind, reference := adminActor(actor)
	return g.UpdateWithActor(name, sortOrder, expectedVersion, actor, kind, reference, now)
}

func (g *Group) UpdateWithActor(name string, sortOrder int, expectedVersion, actorID int64, actorKind, actorReference string, now time.Time) error {
	if g == nil || expectedVersion != g.Version {
		return ErrConflict
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 || sortOrder < 0 || !validActor(actorID, actorKind, actorReference) || now.IsZero() {
		return ErrInvalid
	}
	g.Name, g.SortOrder, g.UpdatedBy, g.UpdatedActorKind, g.UpdatedActorRef, g.UpdatedAt = name, sortOrder, actorID, actorKind, actorReference, now
	g.Version++
	return nil
}
