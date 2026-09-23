package webhook

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQLStore struct{}

func NewPostgreSQLStore() *PostgreSQLStore {
	return &PostgreSQLStore{}
}

func (*PostgreSQLStore) PutIfAbsent(ctx context.Context, delivery Delivery) (Delivery, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Delivery{}, false, err
	}
	stored, scanErr := scanDelivery(tx.QueryRow(ctx, `
		INSERT INTO webhook_inbox (
			provider, idempotency_key, payload_hash, payload, status,
			max_attempts, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider, idempotency_key) DO NOTHING
		RETURNING id, provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at, lease_owner,
			lease_expires_at, last_error_code, received_at, processed_at, updated_at`,
		delivery.Provider, delivery.IdempotencyKey, delivery.PayloadHash[:],
		[]byte(delivery.Payload), delivery.Status, delivery.MaxAttempts, delivery.NextAttemptAt,
	))
	if scanErr == nil {
		return stored, true, nil
	}
	if !errors.Is(scanErr, pgx.ErrNoRows) {
		return Delivery{}, false, scanErr
	}
	stored, err = scanDelivery(tx.QueryRow(ctx, `
		SELECT id, provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at, lease_owner,
			lease_expires_at, last_error_code, received_at, processed_at, updated_at
		FROM webhook_inbox
		WHERE provider = $1 AND idempotency_key = $2
		FOR UPDATE`, delivery.Provider, delivery.IdempotencyKey))
	return stored, false, err
}

func (*PostgreSQLStore) Claim(ctx context.Context, claim Claim) ([]Delivery, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM webhook_inbox
			WHERE provider = $5
			  AND (
				status IN ('received', 'retryable')
				OR (status = 'processing' AND lease_expires_at <= $1)
			)
			  AND attempt_count < max_attempts
			  AND next_attempt_at <= $1
			ORDER BY next_attempt_at, received_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE webhook_inbox AS delivery
		SET status = 'processing',
			attempt_count = delivery.attempt_count + 1,
			lease_owner = $3,
			lease_expires_at = $4,
			updated_at = clock_timestamp()
		FROM candidates
		WHERE delivery.id = candidates.id
		RETURNING delivery.id, delivery.provider, delivery.idempotency_key,
			delivery.payload_hash, delivery.payload, delivery.status,
			delivery.attempt_count, delivery.max_attempts, delivery.next_attempt_at,
			delivery.lease_owner, delivery.lease_expires_at, delivery.last_error_code,
			delivery.received_at, delivery.processed_at, delivery.updated_at`,
		claim.Now, claim.Limit, claim.Owner, claim.Now.Add(claim.LeaseDuration), claim.Provider,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]Delivery, 0, claim.Limit)
	for rows.Next() {
		delivery, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (*PostgreSQLStore) Complete(ctx context.Context, completion Completion) (Delivery, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Delivery{}, err
	}
	var nextAttempt any
	if completion.NextAttemptAt != nil {
		nextAttempt = *completion.NextAttemptAt
	}
	delivery, err := scanDelivery(tx.QueryRow(ctx, `
		UPDATE webhook_inbox
		SET status = $2,
			last_error_code = NULLIF($3, ''),
			next_attempt_at = COALESCE($4, next_attempt_at),
			processed_at = CASE WHEN $2 = 'processed' THEN clock_timestamp() ELSE NULL END,
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'processing' AND attempt_count = $5
		RETURNING id, provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at, lease_owner,
			lease_expires_at, last_error_code, received_at, processed_at, updated_at`,
		completion.ID, completion.Status, completion.LastErrorCode, nextAttempt,
		completion.ExpectedAttempt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrConcurrentUpdate
	}
	return delivery, err
}

func (*PostgreSQLStore) Retry(ctx context.Context, retry Retry) (Delivery, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Delivery{}, err
	}
	delivery, err := scanDelivery(tx.QueryRow(ctx, `
		UPDATE webhook_inbox
		SET status = 'retryable',
			max_attempts = GREATEST(max_attempts, attempt_count + 1),
			next_attempt_at = $4,
			lease_owner = NULL,
			lease_expires_at = NULL,
			processed_at = NULL,
			updated_at = clock_timestamp()
		WHERE id = $1 AND provider = $2 AND status = $5 AND attempt_count = $3
		RETURNING id, provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at, lease_owner,
			lease_expires_at, last_error_code, received_at, processed_at, updated_at`,
		retry.ID, retry.Provider, retry.ExpectedAttempt, retry.Now, retry.ExpectedStatus,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrConcurrentUpdate
	}
	return delivery, err
}

func (*PostgreSQLStore) Get(ctx context.Context, id int64) (Delivery, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Delivery{}, err
	}
	delivery, err := scanDelivery(tx.QueryRow(ctx, `
		SELECT id, provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at, lease_owner,
			lease_expires_at, last_error_code, received_at, processed_at, updated_at
		FROM webhook_inbox WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrInvalidDelivery
	}
	return delivery, err
}

type deliveryScanner interface {
	Scan(...any) error
}

func scanDelivery(scanner deliveryScanner) (Delivery, error) {
	var delivery Delivery
	var key string
	var payloadHash []byte
	var leaseOwner *string
	var lastErrorCode *string
	err := scanner.Scan(
		&delivery.ID, &delivery.Provider, &key, &payloadHash, &delivery.Payload,
		&delivery.Status, &delivery.AttemptCount, &delivery.MaxAttempts,
		&delivery.NextAttemptAt, &leaseOwner, &delivery.LeaseExpiresAt,
		&lastErrorCode, &delivery.ReceivedAt, &delivery.ProcessedAt, &delivery.UpdatedAt,
	)
	if err != nil {
		return Delivery{}, err
	}
	delivery.IdempotencyKey = idempotency.Key(key)
	copy(delivery.PayloadHash[:], payloadHash)
	if leaseOwner != nil {
		delivery.LeaseOwner = *leaseOwner
	}
	if lastErrorCode != nil {
		delivery.LastErrorCode = *lastErrorCode
	}
	return delivery, nil
}
