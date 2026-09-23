package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type Agreement struct {
	Version string
	Content string
}

type BrowserSession struct {
	TokenDigest [32]byte
	CustomerID  int64
	IdentityID  int64
	Channel     string
	AppID       string
	AppScope    string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

func scanDistributor(row rowScanner) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	var distributor distributiondomain.Distributor
	var readiness distributionport.ReceiverReadiness
	var checkedAt *time.Time
	err := row.Scan(&distributor.ID, &distributor.CustomerID, &distributor.PublicNo, &distributor.AgreementVersion, &distributor.Enabled, &readiness.Reference, &readiness.AppID, &readiness.Ready, &readiness.Reason, &checkedAt, &distributor.RegisteredAt, &distributor.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrNotFound
	}
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, mapError(err)
	}
	if checkedAt != nil {
		readiness.CheckedAt = checkedAt.UTC()
	}
	if !distributor.Valid() || readiness.Reference != strings.TrimSpace(readiness.Reference) || len(readiness.Reference) > 200 || readiness.AppID != strings.TrimSpace(readiness.AppID) || len(readiness.AppID) > 200 || readiness.Reason != strings.TrimSpace(readiness.Reason) || len(readiness.Reason) > 200 || (readiness.Ready && (readiness.Reference == "" || readiness.AppID == "" || readiness.Reason != "" || readiness.CheckedAt.IsZero())) {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrUnavailable
	}
	return distributor, readiness, nil
}

func (r *Repository) ActiveAgreementWithin(ctx context.Context) (Agreement, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return Agreement{}, err
	}
	return scanActiveAgreement(tx.QueryRow(ctx, `SELECT version,content FROM distribution_agreements WHERE active=TRUE`))
}

func scanActiveAgreement(row rowScanner) (Agreement, error) {
	var agreement Agreement
	err := row.Scan(&agreement.Version, &agreement.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agreement{}, distributionport.ErrUnavailable
	}
	if err != nil {
		return Agreement{}, mapError(err)
	}
	if agreement.Version != strings.TrimSpace(agreement.Version) || len(agreement.Version) < 1 || len(agreement.Version) > 100 || agreement.Content != strings.TrimSpace(agreement.Content) || len(agreement.Content) < 1 || len(agreement.Content) > 20000 {
		return Agreement{}, distributionport.ErrUnavailable
	}
	return agreement, nil
}

func (r *Repository) ReadDistributorByCustomerWithin(ctx context.Context, customerID int64, lock bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
	}
	if customerID < 1 {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ErrInvalid
	}
	query := `SELECT id,customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version FROM distribution_distributors WHERE customer_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanDistributor(tx.QueryRow(ctx, query, customerID))
}

func (r *Repository) ReadDistributorWithin(ctx context.Context, distributorID int64, lock bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
	}
	if distributorID < 1 {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ErrInvalid
	}
	query := `SELECT id,customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version FROM distribution_distributors WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanDistributor(tx.QueryRow(ctx, query, distributorID))
}

func (r *Repository) InsertDistributorWithin(ctx context.Context, customerID int64, publicNo, agreementVersion string, registeredAt time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
	}
	if customerID < 1 || publicNo == "" || agreementVersion == "" || registeredAt.IsZero() {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ErrInvalid
	}
	// A conflicting distributor can be the same customer registering in a
	// concurrent request, or the (rare) generated public-number collision. Do
	// not let either expected conflict abort the surrounding Unit of Work: the
	// registration application service must be able to read the winner's
	// receipt/customer record and replay it in that same transaction.
	distributor, readiness, err := scanDistributor(tx.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,$2,$3,TRUE,$4,1,$4,$4) ON CONFLICT DO NOTHING RETURNING id,customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version`, customerID, publicNo, agreementVersion, registeredAt.UTC()))
	if errors.Is(err, distributionport.ErrNotFound) {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, distributionport.ErrConflict
	}
	return distributor, readiness, err
}

func (r *Repository) UpdateReceiverReadinessWithin(ctx context.Context, distributorID, expectedVersion int64, readiness distributionport.ReceiverReadiness, at time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, err
	}
	if distributorID < 1 || expectedVersion < 1 || readiness.AppID == "" || readiness.Reason == "" && !readiness.Ready || at.IsZero() {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ErrInvalid
	}
	if readiness.Ready && readiness.Reference == "" {
		return distributiondomain.Distributor{}, distributionport.ReceiverReadiness{}, ErrInvalid
	}
	return scanDistributor(tx.QueryRow(ctx, `UPDATE distribution_distributors SET receiver_reference=$2,receiver_app_id=$3,receiver_ready=$4,receiver_reason=$5,receiver_checked_at=$6,version=version+1,updated_at=$6 WHERE id=$1 AND version=$7 RETURNING id,customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version`, distributorID, readiness.Reference, readiness.AppID, readiness.Ready, readiness.Reason, at.UTC(), expectedVersion))
}

func (r *Repository) InsertBrowserSessionWithin(ctx context.Context, session BrowserSession) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if session.CustomerID < 1 || session.IdentityID < 1 || (session.Channel != "mini_program" && session.Channel != "h5_official_account") || session.AppID == "" || session.AppScope == "" || session.ExpiresAt.IsZero() || session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO distribution_browser_sessions(token_digest,customer_id,identity_id,channel,app_id,app_scope,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, session.TokenDigest[:], session.CustomerID, session.IdentityID, session.Channel, session.AppID, session.AppScope, session.ExpiresAt.UTC(), session.CreatedAt.UTC())
	return mapError(err)
}

func (r *Repository) ReadBrowserSessionWithin(ctx context.Context, digest [32]byte, now time.Time) (BrowserSession, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return BrowserSession{}, err
	}
	if now.IsZero() {
		return BrowserSession{}, ErrInvalid
	}
	return scanBrowserSession(tx.QueryRow(ctx, `SELECT token_digest,customer_id,identity_id,channel,app_id,app_scope,expires_at,created_at FROM distribution_browser_sessions WHERE token_digest=$1 AND revoked_at IS NULL AND expires_at>$2`, digest[:], now.UTC()))
}

func scanBrowserSession(row rowScanner) (BrowserSession, error) {
	var session BrowserSession
	var raw []byte
	err := row.Scan(&raw, &session.CustomerID, &session.IdentityID, &session.Channel, &session.AppID, &session.AppScope, &session.ExpiresAt, &session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, distributionport.ErrUnauthorized
	}
	if err != nil {
		return BrowserSession{}, mapError(err)
	}
	if len(raw) != 32 || session.CustomerID < 1 || session.IdentityID < 1 || (session.Channel != "mini_program" && session.Channel != "h5_official_account") || session.AppID != strings.TrimSpace(session.AppID) || len(session.AppID) < 1 || len(session.AppID) > 200 || session.AppScope != strings.TrimSpace(session.AppScope) || len(session.AppScope) < 1 || len(session.AppScope) > 240 || session.CreatedAt.IsZero() || session.ExpiresAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return BrowserSession{}, distributionport.ErrUnavailable
	}
	copy(session.TokenDigest[:], raw)
	return session, nil
}
