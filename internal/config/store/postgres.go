// Package store owns PostgreSQL persistence for local non-secret Config facts.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalid = errors.New("config store dependencies are required")

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

func (r *Repository) LockKey(ctx context.Context, key configport.Key) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "config.setting:"+string(key))
	return err
}

func (r *Repository) Get(ctx context.Context, key configport.Key) (configport.Setting, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.Setting{}, false, err
	}
	var value []byte
	var updatedBy string
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `SELECT value,updated_by,updated_at FROM config_settings WHERE setting_key=$1`, key).Scan(&value, &updatedBy, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return configport.Setting{}, false, nil
	}
	if err != nil {
		return configport.Setting{}, false, err
	}
	return configport.Setting{Key: key, Value: value, UpdatedBy: updatedBy, UpdatedAt: updatedAt}, true, nil
}

func (r *Repository) InsertAudit(ctx context.Context, old []byte, command configport.SetCommand, next []byte, now time.Time) (configport.Audit, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.Audit{}, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO config_audits(request_id,setting_key,old_value,new_value,updated_by,updated_at)
VALUES($1,$2,$3::jsonb,$4::jsonb,$5,$6) ON CONFLICT(request_id) DO NOTHING RETURNING id`, command.RequestID, command.Key, nullableJSON(old), next, command.Actor, now).Scan(&id)
	if err == nil {
		return configport.Audit{ID: id, Key: command.Key, OldValue: old, NewValue: next, UpdatedBy: command.Actor, RequestID: command.RequestID, UpdatedAt: now}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return configport.Audit{}, false, err
	}
	audit, err := r.GetAuditByRequestID(ctx, command.RequestID)
	return audit, false, err
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func (r *Repository) GetAuditByRequestID(ctx context.Context, requestID string) (configport.Audit, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.Audit{}, err
	}
	var out configport.Audit
	err = tx.QueryRow(ctx, `SELECT id,setting_key,COALESCE(old_value::text,''),new_value,updated_by,request_id,updated_at FROM config_audits WHERE request_id=$1`, requestID).Scan(&out.ID, &out.Key, &out.OldValue, &out.NewValue, &out.UpdatedBy, &out.RequestID, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return configport.Audit{}, fmt.Errorf("config audit missing")
	}
	return out, err
}

func (r *Repository) Upsert(ctx context.Context, command configport.SetCommand, value []byte, now time.Time) (configport.Setting, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.Setting{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_settings(setting_key,value,updated_by,updated_at) VALUES($1,$2::jsonb,$3,$4)
ON CONFLICT(setting_key) DO UPDATE SET value=excluded.value,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, command.Key, value, command.Actor, now)
	if err != nil {
		return configport.Setting{}, err
	}
	return configport.Setting{Key: command.Key, Value: value, UpdatedBy: command.Actor, UpdatedAt: now}, nil
}

func (r *Repository) Append(ctx context.Context, event configport.Event) (configport.EventID, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO config_outbox(event_type,idempotency_key,payload,occurred_at) VALUES($1,$2,$3::jsonb,$4)
ON CONFLICT(idempotency_key) DO NOTHING RETURNING id`, event.Type, event.IdempotencyKey, event.Payload, event.OccurredAt).Scan(&id)
	if err == nil {
		return configport.EventID(id), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT id,payload FROM config_outbox WHERE idempotency_key=$1`, event.IdempotencyKey).Scan(&id, &payload)
	if err != nil {
		return 0, err
	}
	if string(payload) != string(event.Payload) {
		return 0, configport.ErrIdempotencyConflict
	}
	return configport.EventID(id), nil
}

func (r *Repository) ReserveCommandRequest(ctx context.Context, action, actor, requestID string, payloadDigest []byte, now time.Time) (configport.RequestReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return configport.RequestReceipt{}, false, err
	}
	var out configport.RequestReceipt
	err = tx.QueryRow(ctx, `INSERT INTO config_command_receipts(action,actor,request_id,payload_digest,state,created_at)
VALUES($1,$2,$3,$4,'reserved',$5)
ON CONFLICT(action,actor,request_id) DO NOTHING
RETURNING id,payload_digest,state`, action, actor, requestID, payloadDigest, now).Scan(&out.ID, &out.PayloadDigest, &out.State)
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return configport.RequestReceipt{}, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id,payload_digest,state FROM config_command_receipts
WHERE action=$1 AND actor=$2 AND request_id=$3`, action, actor, requestID).Scan(&out.ID, &out.PayloadDigest, &out.State)
	return out, false, err
}

func (r *Repository) CompleteCommandRequest(ctx context.Context, id int64, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE config_command_receipts
SET state='completed',completed_at=$2 WHERE id=$1 AND state='reserved'`, id, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("config settings batch receipt state conflict")
	}
	return nil
}

func (r *Repository) ListAppSettings(ctx context.Context) ([]configport.ProjectionSetting, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT s.setting_key,s.value,s.updated_at,COALESCE(a.updated_by,s.updated_by),COALESCE(a.updated_at,s.updated_at)
FROM config_settings s LEFT JOIN LATERAL (SELECT updated_by,updated_at FROM config_audits WHERE setting_key=s.setting_key ORDER BY id DESC LIMIT 1) a ON true ORDER BY s.setting_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []configport.ProjectionSetting{}
	for rows.Next() {
		var x configport.ProjectionSetting
		var lm time.Time
		if err = rows.Scan(&x.Key, &x.Value, &x.UpdatedAt, &x.LastModifiedBy, &lm); err != nil {
			return nil, err
		}
		x.LastModifiedAt = &lm
		x.LastActionType = "setting.updated"
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Repository) ListAppSettingsAudit(ctx context.Context) ([]configport.ProjectionAudit, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,updated_by,'setting.updated',setting_key,updated_at FROM config_audits ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []configport.ProjectionAudit{}
	for rows.Next() {
		var x configport.ProjectionAudit
		if err = rows.Scan(&x.ID, &x.Operator, &x.ActionType, &x.TargetID, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Compile-time boundaries: Config app uses only this local repository and
// transaction-bound appender; it never owns runtime/provider configuration.
var _ configport.EventAppender = (*Repository)(nil)
var _ interface {
	LockKey(context.Context, configport.Key) error
	Get(context.Context, configport.Key) (configport.Setting, bool, error)
	InsertAudit(context.Context, []byte, configport.SetCommand, []byte, time.Time) (configport.Audit, bool, error)
	GetAuditByRequestID(context.Context, string) (configport.Audit, error)
	Upsert(context.Context, configport.SetCommand, []byte, time.Time) (configport.Setting, error)
	ListAppSettings(context.Context) ([]configport.ProjectionSetting, error)
	ListAppSettingsAudit(context.Context) ([]configport.ProjectionAudit, error)
} = (*Repository)(nil)

func MarshalDetails(value any) (json.RawMessage, error) { return json.Marshal(value) }
