package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strconv"
)

func declaredPhonePayloadDigest(c identityport.DeclaredPhoneCommand, phone string) [32]byte {
	suffix := ""
	if c.OnlyIfEmpty {
		suffix = "\x00only-if-empty"
	}
	return sha256.Sum256([]byte(strconv.FormatInt(int64(c.CustomerID), 10) + "\x00" + phone + "\x00" + c.Source + "\x00" + c.SourceEventID + suffix))
}
func (s *PostgresStore) ReadDeclaredPhoneReceiptWithin(ctx context.Context, c identityport.DeclaredPhoneCommand) (identityport.DeclaredAttachResult, bool, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return identityport.DeclaredAttachResult{}, false, e
	}
	key := sha256.Sum256([]byte(c.IdempotencyKey))
	var outcome string
	var payload []byte
	var customer, identity *int64
	e = tx.QueryRow(ctx, `SELECT outcome,payload_digest,customer_id,identity_id FROM identity_declared_phone_receipts WHERE key_digest=$1`, key[:]).Scan(&outcome, &payload, &customer, &identity)
	if errors.Is(e, pgx.ErrNoRows) {
		return identityport.DeclaredAttachResult{}, false, nil
	}
	if e != nil {
		return identityport.DeclaredAttachResult{}, false, persistenceFailure(e)
	}
	ref, e := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: c.Phone, Assurance: identitydomain.AssuranceDeclared, Source: c.Source})
	if e != nil {
		return identityport.DeclaredAttachResult{}, true, identityapp.ErrDeclaredPayloadMismatch
	}
	expected := declaredPhonePayloadDigest(c, ref.NormalizedValue)
	if string(payload) != string(expected[:]) {
		return identityport.DeclaredAttachResult{}, true, identityapp.ErrDeclaredPayloadMismatch
	}
	result := identityport.DeclaredAttachResult{Status: identityport.DeclaredAttachStatus(outcome)}
	if customer != nil {
		result.CustomerID = customerdomain.CustomerID(*customer)
	}
	if identity != nil {
		result.IdentityID = *identity
	}
	return result, true, nil
}
