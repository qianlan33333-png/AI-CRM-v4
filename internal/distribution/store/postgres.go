// Package store owns Distribution PostgreSQL persistence. It never reads or
// writes Product, Order, Payment, Identity, Customer or jobqueue tables.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalid = errors.New("invalid distribution persistence request")

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

type rowScanner interface{ Scan(...any) error }

func scanPolicy(row rowScanner) (distributiondomain.Policy, error) {
	var policy distributiondomain.Policy
	var productType string
	err := row.Scan(&policy.ProductID, &productType, &policy.Enabled, &policy.CommissionRateBasisPoints, &policy.WaitDays, &policy.Version, &policy.CreatedAt, &policy.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return distributiondomain.Policy{}, distributionport.ErrNotFound
	}
	if err != nil {
		return distributiondomain.Policy{}, mapError(err)
	}
	policy.ProductType = distributiondomain.ProductType(productType)
	if !policy.Valid() {
		return distributiondomain.Policy{}, distributionport.ErrUnavailable
	}
	return policy, nil
}

func (r *Repository) ReadProductPolicyWithin(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributiondomain.Policy, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	if productID < 1 || !productType.Valid() {
		return distributiondomain.Policy{}, ErrInvalid
	}
	return scanPolicy(tx.QueryRow(ctx, `SELECT product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at FROM distribution_product_policies WHERE product_id=$1 AND product_type=$2`, productID, string(productType)))
}

func (r *Repository) ReadProductPolicyForUpdateWithin(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributiondomain.Policy, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	if productID < 1 || !productType.Valid() {
		return distributiondomain.Policy{}, ErrInvalid
	}
	return scanPolicy(tx.QueryRow(ctx, `SELECT product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at FROM distribution_product_policies WHERE product_id=$1 AND product_type=$2 FOR UPDATE`, productID, string(productType)))
}

func (r *Repository) InsertProductPolicyWithin(ctx context.Context, policy distributiondomain.Policy) (distributiondomain.Policy, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	if !policy.Valid() || policy.Version != 1 {
		return distributiondomain.Policy{}, ErrInvalid
	}
	return scanPolicy(tx.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at`, policy.ProductID, string(policy.ProductType), policy.Enabled, policy.CommissionRateBasisPoints, policy.WaitDays, policy.Version, policy.CreatedAt.UTC(), policy.UpdatedAt.UTC()))
}

func (r *Repository) UpdateProductPolicyWithin(ctx context.Context, policy distributiondomain.Policy, expectedVersion int64) (distributiondomain.Policy, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return distributiondomain.Policy{}, err
	}
	if !policy.Valid() || expectedVersion < 1 || policy.Version != expectedVersion+1 {
		return distributiondomain.Policy{}, ErrInvalid
	}
	updated, err := scanPolicy(tx.QueryRow(ctx, `UPDATE distribution_product_policies SET enabled=$3,commission_rate_basis_points=$4,wait_days=$5,version=$6,updated_at=$7 WHERE product_id=$1 AND product_type=$2 AND version=$8 RETURNING product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at`, policy.ProductID, string(policy.ProductType), policy.Enabled, policy.CommissionRateBasisPoints, policy.WaitDays, policy.Version, policy.UpdatedAt.UTC(), expectedVersion))
	if errors.Is(err, distributionport.ErrNotFound) {
		return distributiondomain.Policy{}, distributionport.ErrConflict
	}
	return updated, err
}

func (r *Repository) AppendPolicyVersionWithin(ctx context.Context, policyID int64, policy distributiondomain.Policy, actorScope string, occurredAt time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if policyID < 1 || !policy.Valid() || actorScope == "" || len(actorScope) > 200 || occurredAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO distribution_policy_versions(policy_id,version,enabled,commission_rate_basis_points,wait_days,actor_scope,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, policyID, policy.Version, policy.Enabled, policy.CommissionRateBasisPoints, policy.WaitDays, actorScope, occurredAt.UTC())
	return mapError(err)
}

func (r *Repository) ProductPolicyIDWithin(ctx context.Context, productID int64, productType distributiondomain.ProductType) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if productID < 1 || !productType.Valid() {
		return 0, ErrInvalid
	}
	return scanPositiveID(tx.QueryRow(ctx, `SELECT id FROM distribution_product_policies WHERE product_id=$1 AND product_type=$2`, productID, string(productType)))
}

func scanPositiveID(row rowScanner) (int64, error) {
	var id int64
	err := row.Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, distributionport.ErrNotFound
	}
	if err != nil {
		return 0, mapError(err)
	}
	if id < 1 {
		return 0, distributionport.ErrUnavailable
	}
	return id, nil
}

func (r *Repository) AppendAuditWithin(ctx context.Context, eventType, aggregateType string, aggregateID int64, actorScope string, payload any, occurredAt time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil || aggregateID < 1 || eventType == "" || aggregateType == "" || actorScope == "" || occurredAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO distribution_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,payload,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, eventType, aggregateType, aggregateID, actorScope, encoded, occurredAt.UTC())
	return mapError(err)
}

func (r *Repository) AppendOutboxWithin(ctx context.Context, eventType, idempotencyKey string, aggregateID int64, payload any, occurredAt time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil || aggregateID < 1 || eventType == "" || idempotencyKey == "" || len(idempotencyKey) > 200 || occurredAt.IsZero() {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO distribution_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING`, eventType, idempotencyKey, aggregateID, encoded, occurredAt.UTC())
	return mapError(err)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return distributionport.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23514", "40001", "40P01":
			return distributionport.ErrConflict
		}
	}
	return err
}
