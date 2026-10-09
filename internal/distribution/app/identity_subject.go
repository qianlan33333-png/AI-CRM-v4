package app

import (
	"context"
	"errors"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type distributionIdentitySubjects interface {
	identityport.VerifiedIdentitySubjectReader
	identityport.WriteLockedVerifiedIdentitySubjectReader
}

func distributionSubjects(reader identityport.VerifiedIdentitySubjectReader) (distributionIdentitySubjects, error) {
	value, ok := reader.(distributionIdentitySubjects)
	if !ok || value == nil {
		return nil, distributionport.ErrUnavailable
	}
	return value, nil
}

// Resolve the immutable handle in the business UoW. An earlier session read
// cannot pin a later mutation against merge, reversal or handle retirement.
func distributionActorLineageWithin(ctx context.Context, reader identityport.VerifiedIdentitySubjectReader, actor distributionport.TrustedSessionActor, write bool) ([]int64, error) {
	if reader == nil || !actor.Valid() {
		return nil, distributionport.ErrUnauthorized
	}
	var subject identityport.VerifiedIdentitySubject
	var found bool
	var err error
	if write {
		locked, ok := reader.(identityport.WriteLockedVerifiedIdentitySubjectReader)
		if !ok {
			return nil, distributionport.ErrUnavailable
		}
		subject, found, err = locked.WriteLockedVerifiedIdentitySubject(ctx, actor.IdentityID)
	} else {
		subject, found, err = reader.VerifiedIdentitySubject(ctx, actor.IdentityID)
	}
	if err != nil {
		return nil, distributionport.ErrUnavailable
	}
	if !found || subject.CustomerID < 1 || len(subject.Lineage) == 0 || subject.Lineage[0] != subject.CustomerID {
		return nil, distributionport.ErrUnauthorized
	}
	lineage := make([]int64, len(subject.Lineage))
	seen := make(map[int64]struct{}, len(lineage))
	for i, id := range subject.Lineage {
		if id < 1 {
			return nil, distributionport.ErrUnavailable
		}
		if _, duplicate := seen[int64(id)]; duplicate {
			return nil, distributionport.ErrUnavailable
		}
		seen[int64(id)] = struct{}{}
		lineage[i] = int64(id)
	}
	return lineage, nil
}

type distributorSubjectStore interface {
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
}

// Keep the unique immutable business owner in the current lineage. Two
// historical distributors require a business decision; selecting either one
// would grant authority over an ambiguous financial history.
func distributionActorWithin(ctx context.Context, reader identityport.VerifiedIdentitySubjectReader, store distributorSubjectStore, actor distributionport.TrustedSessionActor, write bool) (distributionport.TrustedSessionActor, error) {
	lineage, err := distributionActorLineageWithin(ctx, reader, actor, write)
	if err != nil {
		return distributionport.TrustedSessionActor{}, err
	}
	actor.CustomerID = lineage[0]
	var selected distributiondomain.Distributor
	for _, customerID := range lineage {
		candidate, _, e := store.ReadDistributorByCustomerWithin(ctx, customerID, false)
		if errors.Is(e, distributionport.ErrNotFound) {
			continue
		}
		if e != nil {
			return distributionport.TrustedSessionActor{}, e
		}
		if candidate.CustomerID != customerID || candidate.ID < 1 {
			return distributionport.TrustedSessionActor{}, distributionport.ErrUnavailable
		}
		if selected.ID != 0 && selected.ID != candidate.ID {
			return distributionport.TrustedSessionActor{}, distributionport.ErrConflict
		}
		selected = candidate
	}
	if selected.ID > 0 {
		actor.CustomerID = selected.CustomerID
	}
	return actor, nil
}
