// Package postgres provides the shared PostgreSQL pool and explicit
// transaction boundary for the modular monolith.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrInvalidConfig     = errors.New("invalid postgres configuration")
	ErrNestedTransaction = errors.New("nested transaction is not allowed")
	ErrTransactionNeeded = errors.New("transaction is required")
)

type Config struct {
	URL             string
	MaxConnections  int32
	MinConnections  int32
	ConnectTimeout  time.Duration
	HealthTimeout   time.Duration
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type Pool struct {
	pool          *pgxpool.Pool
	healthTimeout time.Duration
}

func Open(ctx context.Context, cfg Config) (*Pool, error) {
	if cfg.URL == "" || cfg.MaxConnections < 0 || cfg.MinConnections < 0 ||
		(cfg.MaxConnections > 0 && cfg.MinConnections > cfg.MaxConnections) {
		return nil, ErrInvalidConfig
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.HealthTimeout <= 0 {
		cfg.HealthTimeout = 2 * time.Second
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres configuration: %w", ErrInvalidConfig)
	}
	if cfg.MaxConnections > 0 {
		poolConfig.MaxConns = cfg.MaxConnections
	}
	if cfg.MinConnections > 0 {
		poolConfig.MinConns = cfg.MinConnections
	}
	if cfg.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	native, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("open postgres pool")
	}
	pool := &Pool{pool: native, healthTimeout: cfg.HealthTimeout}
	if err = pool.Check(ctx); err != nil {
		native.Close()
		return nil, err
	}
	return pool, nil
}

// Wrap exposes an already-created pgx pool through the platform abstraction.
// It is useful when a caller needs pgx connection hooks such as a test schema.
func Wrap(native *pgxpool.Pool, healthTimeout time.Duration) (*Pool, error) {
	if native == nil {
		return nil, ErrInvalidConfig
	}
	if healthTimeout <= 0 {
		healthTimeout = 2 * time.Second
	}
	return &Pool{pool: native, healthTimeout: healthTimeout}, nil
}

func (pool *Pool) Close() {
	if pool != nil && pool.pool != nil {
		pool.pool.Close()
	}
}

func (pool *Pool) Check(ctx context.Context) error {
	if pool == nil || pool.pool == nil {
		return errors.New("postgres readiness check: pool is not configured")
	}
	checkCtx, cancel := context.WithTimeout(ctx, pool.healthTimeout)
	defer cancel()
	if err := pool.pool.Ping(checkCtx); err != nil {
		return errors.New("postgres readiness check failed")
	}
	return nil
}

// Native returns the underlying pool for composition-root and migration-tool
// wiring. Business packages should use ports and UnitOfWork instead.
func (pool *Pool) Native() *pgxpool.Pool {
	if pool == nil {
		return nil
	}
	return pool.pool
}

type transactionKey struct{}

// BindTransaction exposes an already-open infrastructure transaction to a
// stable domain port without asking that domain to import pgx.
func BindTransaction(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, transactionKey{}, tx)
}

func transactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(transactionKey{}).(pgx.Tx)
	return tx, ok
}

// RequireTransaction returns the transaction bound by UnitOfWork. Platform
// stores call this before every write, making accidental autocommit writes fail.
func RequireTransaction(ctx context.Context) (pgx.Tx, error) {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx, nil
	}
	return nil, ErrTransactionNeeded
}

type UnitOfWork struct {
	pool    *Pool
	options pgx.TxOptions
}

var _ platformport.UnitOfWork = (*UnitOfWork)(nil)

func NewUnitOfWork(pool *Pool) (*UnitOfWork, error) {
	if pool == nil || pool.pool == nil {
		return nil, ErrInvalidConfig
	}
	return &UnitOfWork{pool: pool}, nil
}

// NewReadOnlyRepeatableReadUnitOfWork creates the narrow snapshot boundary
// used by composed, cross-owner reporting reads. It is deliberately separate
// from the default UoW so command paths retain their existing transaction
// semantics.
func NewReadOnlyRepeatableReadUnitOfWork(pool *Pool) (*UnitOfWork, error) {
	if pool == nil || pool.pool == nil {
		return nil, ErrInvalidConfig
	}
	return &UnitOfWork{pool: pool, options: pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}}, nil
}

func (unit *UnitOfWork) Within(ctx context.Context, callback func(context.Context) error) (err error) {
	if callback == nil {
		return errors.New("unit of work callback is required")
	}
	if _, nested := transactionFromContext(ctx); nested {
		return ErrNestedTransaction
	}
	tx, err := unit.pool.pool.BeginTx(ctx, unit.options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			rollback(tx)
			panic(recovered)
		}
	}()

	txContext := context.WithValue(ctx, transactionKey{}, tx)
	if err = callback(txContext); err != nil {
		rollback(tx)
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		rollback(tx)
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
