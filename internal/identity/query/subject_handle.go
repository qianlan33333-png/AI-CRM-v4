package query

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ identityport.VerifiedIdentitySubjectReader = PostgreSQL{}
var _ identityport.WriteLockedVerifiedIdentitySubjectReader = PostgreSQL{}

func (q PostgreSQL) VerifiedIdentitySubject(ctx context.Context, identityID int64) (identityport.VerifiedIdentitySubject, bool, error) {
	return q.verifiedIdentitySubject(ctx, identityID, false)
}

func (q PostgreSQL) WriteLockedVerifiedIdentitySubject(ctx context.Context, identityID int64) (identityport.VerifiedIdentitySubject, bool, error) {
	return q.verifiedIdentitySubject(ctx, identityID, true)
}

func (q PostgreSQL) verifiedIdentitySubject(ctx context.Context, identityID int64, locked bool) (identityport.VerifiedIdentitySubject, bool, error) {
	if identityID < 1 {
		return identityport.VerifiedIdentitySubject{}, false, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.VerifiedIdentitySubject{}, false, err
	}
	var owner customerdomain.CustomerID
	var version int64
	var active bool
	err = tx.QueryRow(ctx, `SELECT customer_id,version,status='active' AND assurance='verified' AND kind<>'phone' FROM customer_identities WHERE id=$1`, identityID).Scan(&owner, &version, &active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return identityport.VerifiedIdentitySubject{}, false, nil
	}
	if err != nil {
		return identityport.VerifiedIdentitySubject{}, false, err
	}
	// Merge/reversal lock roots before identity rows. Read the handle without a
	// row lock, use the existing ordered lineage lock, then re-read the handle.
	var lineage []customerdomain.CustomerID
	if locked {
		lineage, err = q.WriteLockedCanonicalLineage(ctx, owner)
	} else {
		lineage, err = q.LockedCanonicalLineage(ctx, owner)
	}
	if err != nil {
		return identityport.VerifiedIdentitySubject{}, false, err
	}
	if len(lineage) == 0 {
		return identityport.VerifiedIdentitySubject{}, false, ErrInvalidQuery
	}
	var rootActive bool
	if err = tx.QueryRow(ctx, `SELECT status='active' FROM customers WHERE id=$1`, lineage[0]).Scan(&rootActive); err != nil {
		return identityport.VerifiedIdentitySubject{}, false, err
	}
	if !rootActive {
		return identityport.VerifiedIdentitySubject{}, false, nil
	}
	statement := `SELECT customer_id,version,status='active' AND assurance='verified' AND kind<>'phone' FROM customer_identities WHERE id=$1`
	if locked {
		statement += ` FOR SHARE`
	}
	var currentOwner customerdomain.CustomerID
	var currentVersion int64
	err = tx.QueryRow(ctx, statement, identityID).Scan(&currentOwner, &currentVersion, &active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!active || currentOwner != owner || currentVersion != version) {
		return identityport.VerifiedIdentitySubject{}, false, nil
	}
	if err != nil {
		return identityport.VerifiedIdentitySubject{}, false, err
	}
	return identityport.VerifiedIdentitySubject{CustomerID: lineage[0], Lineage: lineage}, true, nil
}
