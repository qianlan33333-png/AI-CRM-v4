package app

import (
	"strconv"

	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

// mutationActor keeps the legacy positive staff field compatible with the
// stable subject Port. A caller may not pair a machine subject with any staff
// value, and an explicit human subject must agree with its compatibility ID.
func mutationActor(legacyStaffID int64, explicit segmentport.MutationActor) (segmentport.MutationActor, error) {
	if explicit.Valid() {
		if legacyStaffID != 0 && (explicit.Kind != segmentport.MutationActorAdmin || explicit.StaffID != legacyStaffID) {
			return segmentport.MutationActor{}, ErrInvalid
		}
		return explicit, nil
	}
	if explicit.Kind != "" || explicit.Reference != "" || explicit.StaffID != 0 {
		return segmentport.MutationActor{}, ErrInvalid
	}
	actor, err := segmentport.AdminMutationActor(legacyStaffID)
	if err != nil {
		return segmentport.MutationActor{}, ErrInvalid
	}
	return actor, nil
}

// storedMutationActor accepts historical human rows that predate 0097 only
// for in-memory test fixtures. PostgreSQL 0097 backfills every persisted row
// with the canonical kind/reference pair.
func storedMutationActor(staffID int64, kind, reference string) (segmentport.MutationActor, error) {
	if kind == "" && reference == "" {
		actor, err := segmentport.AdminMutationActor(staffID)
		if err != nil {
			return segmentport.MutationActor{}, ErrInvalid
		}
		return actor, nil
	}
	actor := segmentport.MutationActor{Kind: segmentport.MutationActorKind(kind), Reference: reference, StaffID: staffID}
	if !actor.Valid() {
		return segmentport.MutationActor{}, ErrInvalid
	}
	return actor, nil
}

func storeActor(actor segmentport.MutationActor) segmentstore.Actor {
	return segmentstore.Actor{Kind: string(actor.Kind), Reference: actor.Reference, StaffID: actor.StaffID}
}

func actorScope(actor segmentport.MutationActor) string { return actor.Reference }

func actorReferenceForStaff(staffID int64) string { return "admin:" + strconv.FormatInt(staffID, 10) }
