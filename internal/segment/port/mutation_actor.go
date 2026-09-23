package port

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalidMutationActor = errors.New("invalid segment mutation actor")

// MutationActor is the auditable subject of a Segment mutation. Existing
// administrative commands retain their positive int64 staff fields while the
// owner migrates those columns to nullable compatibility projections. Machine
// callers carry no staff/admin ID: their reference is always derived from an
// authenticated Access client ID as machine:<client_id>.
type MutationActor struct {
	Kind      MutationActorKind
	Reference string
	StaffID   int64
}

type MutationActorKind string

const (
	MutationActorAdmin   MutationActorKind = "admin"
	MutationActorMachine MutationActorKind = "machine"
)

func AdminMutationActor(id int64) (MutationActor, error) {
	if id < 1 {
		return MutationActor{}, ErrInvalidMutationActor
	}
	return MutationActor{Kind: MutationActorAdmin, Reference: "admin:" + strconv.FormatInt(id, 10), StaffID: id}, nil
}

func MachineMutationActor(clientID string) (MutationActor, error) {
	if !validMachineClientReference(clientID) {
		return MutationActor{}, ErrInvalidMutationActor
	}
	return MutationActor{Kind: MutationActorMachine, Reference: "machine:" + clientID}, nil
}

func (actor MutationActor) Valid() bool {
	switch actor.Kind {
	case MutationActorAdmin:
		return actor.StaffID > 0 && actor.Reference == "admin:"+strconv.FormatInt(actor.StaffID, 10)
	case MutationActorMachine:
		return actor.StaffID == 0 && strings.HasPrefix(actor.Reference, "machine:") && validMachineClientReference(strings.TrimPrefix(actor.Reference, "machine:"))
	default:
		return false
	}
}

func validMachineClientReference(value string) bool {
	if value == "" || len(value) > 160 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
