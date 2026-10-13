package store

import (
	"context"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ identityport.VerifiedIdentityKeyLocker = (*PostgresStore)(nil)

func (store *PostgresStore) LockVerifiedIdentityWithin(ctx context.Context, fact identitydomain.VerifiedFact) error {
	if !fact.Valid() || fact.Reference().Kind == identitydomain.KindPhone {
		return identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if err = lockIdentityKey(ctx, tx, fact.Reference()); err != nil {
		return persistenceFailure(err)
	}
	return nil
}
