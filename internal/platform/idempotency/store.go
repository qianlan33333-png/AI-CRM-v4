package idempotency

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrConcurrentUpdate = errors.New("idempotency receipt changed concurrently")

type PostgreSQLStore struct{}

func NewPostgreSQLStore() *PostgreSQLStore {
	return &PostgreSQLStore{}
}

func (*PostgreSQLStore) PutIfAbsent(ctx context.Context, receipt Receipt) (Receipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Receipt{}, false, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO idempotency_receipts (
			idempotency_key, payload_hash, status, max_attempts, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING idempotency_key, payload_hash, status, response, attempt_count,
			max_attempts, next_attempt_at, lease_owner, lease_expires_at,
			last_error_code, created_at, updated_at`,
		receipt.Key, receipt.PayloadHash[:], receipt.Status, receipt.MaxAttempts, receipt.NextAttemptAt,
	)
	stored, scanErr := scanReceipt(row)
	if scanErr == nil {
		return stored, true, nil
	}
	if !errors.Is(scanErr, pgx.ErrNoRows) {
		return Receipt{}, false, scanErr
	}
	stored, err = scanReceipt(tx.QueryRow(ctx, `
		SELECT idempotency_key, payload_hash, status, response, attempt_count,
			max_attempts, next_attempt_at, lease_owner, lease_expires_at,
			last_error_code, created_at, updated_at
		FROM idempotency_receipts
		WHERE idempotency_key = $1
		FOR UPDATE`, receipt.Key))
	return stored, false, err
}

func (*PostgreSQLStore) Claim(ctx context.Context, claim Claim) ([]Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	leaseExpiresAt := claim.Now.Add(claim.LeaseDuration)
	rows, err := tx.Query(ctx, `
		WITH candidates AS (
			SELECT idempotency_key
			FROM idempotency_receipts
			WHERE (
				status IN ('accepted', 'queued')
				OR (status = 'attempted' AND lease_expires_at <= $1)
			)
			  AND attempt_count < max_attempts
			  AND next_attempt_at <= $1
			ORDER BY next_attempt_at, created_at, idempotency_key
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE idempotency_receipts AS receipt
		SET status = 'attempted',
			attempt_count = receipt.attempt_count + 1,
			lease_owner = $3,
			lease_expires_at = $4,
			updated_at = clock_timestamp()
		FROM candidates
		WHERE receipt.idempotency_key = candidates.idempotency_key
		RETURNING receipt.idempotency_key, receipt.payload_hash, receipt.status,
			receipt.response, receipt.attempt_count, receipt.max_attempts,
			receipt.next_attempt_at, receipt.lease_owner, receipt.lease_expires_at,
			receipt.last_error_code, receipt.created_at, receipt.updated_at`,
		claim.Now, claim.Limit, claim.Owner, leaseExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := make([]Receipt, 0, claim.Limit)
	for rows.Next() {
		receipt, scanErr := scanReceipt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func (*PostgreSQLStore) RecordOutcome(ctx context.Context, outcome Outcome) (Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Receipt{}, err
	}
	var nextAttempt any
	if outcome.NextAttemptAt != nil {
		nextAttempt = *outcome.NextAttemptAt
	}
	receipt, err := scanReceipt(tx.QueryRow(ctx, `
		UPDATE idempotency_receipts
		SET status = $2,
			response = $3,
			last_error_code = NULLIF($4, ''),
			next_attempt_at = COALESCE($5, next_attempt_at),
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = clock_timestamp()
		WHERE idempotency_key = $1
		  AND status IN ('attempted', 'outcome_unknown')
		  AND attempt_count = $6
		RETURNING idempotency_key, payload_hash, status, response, attempt_count,
			max_attempts, next_attempt_at, lease_owner, lease_expires_at,
			last_error_code, created_at, updated_at`,
		outcome.Key, outcome.Status, nullableJSON(outcome.Response), outcome.LastErrorCode,
		nextAttempt, outcome.ExpectedAttempt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrConcurrentUpdate
	}
	return receipt, err
}

type receiptScanner interface {
	Scan(...any) error
}

func scanReceipt(scanner receiptScanner) (Receipt, error) {
	var receipt Receipt
	var payloadHash []byte
	var key string
	var leaseOwner *string
	var lastErrorCode *string
	err := scanner.Scan(
		&key, &payloadHash, &receipt.Status, &receipt.Response, &receipt.AttemptCount,
		&receipt.MaxAttempts, &receipt.NextAttemptAt, &leaseOwner,
		&receipt.LeaseExpiresAt, &lastErrorCode, &receipt.CreatedAt, &receipt.UpdatedAt,
	)
	if err != nil {
		return Receipt{}, err
	}
	receipt.Key = Key(key)
	copy(receipt.PayloadHash[:], payloadHash)
	if leaseOwner != nil {
		receipt.LeaseOwner = *leaseOwner
	}
	if lastErrorCode != nil {
		receipt.LastErrorCode = *lastErrorCode
	}
	return receipt, nil
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
