package session

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

func (PostgreSQL) Insert(ctx context.Context, record Record) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	var beneficiary any
	if record.BeneficiaryCustomerID > 0 {
		beneficiary = int64(record.BeneficiaryCustomerID)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_sessions(
			token_digest,payer_identity_id,payer_customer_id,beneficiary_customer_id,
			beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,unionid_verified,expires_at,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id`,
		record.TokenDigest[:], record.PayerIdentityID, record.PayerCustomerID,
		beneficiary, record.BeneficiarySelection, record.BeneficiarySelectedAt, record.AppScopeDigest[:], record.Channel, record.UnionIDVerified,
		record.ExpiresAt, record.CreatedAt,
	).Scan(&record.ID)
	return record, err
}

func (PostgreSQL) Consume(ctx context.Context, digest [32]byte, now time.Time) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	record, err := scanRecord(tx.QueryRow(ctx, `
		UPDATE payment_sessions
		SET consumed_at=$2
		WHERE token_digest=$1 AND consumed_at IS NULL AND expires_at>$2 AND beneficiary_customer_id IS NOT NULL
		RETURNING id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,unionid_verified,expires_at,consumed_at,created_at`,
		digest[:], now,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrExpired
	}
	return record, err
}

func (PostgreSQL) Lookup(ctx context.Context, digest [32]byte, now time.Time) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	record, err := scanRecord(tx.QueryRow(ctx, `
		SELECT id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,unionid_verified,expires_at,consumed_at,created_at
		FROM payment_sessions WHERE token_digest=$1 AND expires_at>$2`, digest[:], now))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrExpired
	}
	return record, err
}

func (PostgreSQL) SelectPayerSelf(ctx context.Context, digest [32]byte, now time.Time) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	record, err := scanRecord(tx.QueryRow(ctx, `
		SELECT id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,unionid_verified,expires_at,consumed_at,created_at
		FROM payment_sessions WHERE token_digest=$1 AND expires_at>$2 FOR UPDATE`, digest[:], now))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrExpired
	}
	if err != nil {
		return Record{}, err
	}
	if record.BeneficiarySelection == "payer_self" && record.BeneficiaryCustomerID == record.PayerCustomerID {
		return record, nil
	}
	if record.BeneficiarySelection != "unresolved" || record.BeneficiaryCustomerID != 0 || record.ConsumedAt != nil {
		return Record{}, ErrInvalid
	}
	record, err = scanRecord(tx.QueryRow(ctx, `
		UPDATE payment_sessions
		SET beneficiary_customer_id=$2, beneficiary_selection='payer_self', beneficiary_selected_at=$3
		WHERE id=$1 AND beneficiary_selection='unresolved' AND beneficiary_customer_id IS NULL AND consumed_at IS NULL
		RETURNING id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,unionid_verified,expires_at,consumed_at,created_at`,
		record.ID, record.PayerCustomerID, now,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrInvalid
	}
	return record, err
}

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	var payer int64
	var beneficiary *int64
	var tokenDigest, scopeDigest []byte
	err := row.Scan(
		&record.ID, &tokenDigest, &record.PayerIdentityID, &payer, &beneficiary,
		&record.BeneficiarySelection, &record.BeneficiarySelectedAt, &scopeDigest, &record.Channel, &record.UnionIDVerified, &record.ExpiresAt, &record.ConsumedAt, &record.CreatedAt,
	)
	if err != nil {
		return Record{}, err
	}
	if len(tokenDigest) != 32 || len(scopeDigest) != 32 || payer < 1 {
		return Record{}, ErrInvalid
	}
	copy(record.TokenDigest[:], tokenDigest)
	copy(record.AppScopeDigest[:], scopeDigest)
	record.PayerCustomerID = customerdomain.CustomerID(payer)
	if beneficiary != nil {
		record.BeneficiaryCustomerID = customerdomain.CustomerID(*beneficiary)
	}
	return validateRecord(record)
}

func validateRecord(record Record) (Record, error) {
	if record.Channel == "h5_official_account" && !record.UnionIDVerified {
		return Record{}, ErrExpired
	}
	if record.ID < 1 || record.PayerIdentityID < 1 || record.PayerCustomerID < 1 || !record.ExpiresAt.After(record.CreatedAt) || (record.ConsumedAt != nil && record.ConsumedAt.Before(record.CreatedAt)) {
		return Record{}, ErrInvalid
	}
	switch record.BeneficiarySelection {
	case "legacy_prebound":
		if record.BeneficiaryCustomerID < 1 || record.BeneficiarySelectedAt != nil {
			return Record{}, ErrInvalid
		}
	case "unresolved":
		if record.BeneficiaryCustomerID != 0 || record.BeneficiarySelectedAt != nil {
			return Record{}, ErrInvalid
		}
	case "payer_self":
		if record.BeneficiaryCustomerID != record.PayerCustomerID || record.BeneficiarySelectedAt == nil {
			return Record{}, ErrInvalid
		}
	case "admin_assisted":
		if record.BeneficiaryCustomerID < 1 || record.BeneficiarySelectedAt == nil {
			return Record{}, ErrInvalid
		}
	default:
		return Record{}, ErrInvalid
	}
	return record, nil
}
