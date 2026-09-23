// Package store owns Automation's PostgreSQL records.  It contains only
// local configuration, idempotency, audit, and outbox facts.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInvalid  = errors.New("invalid automation persistence command")
	ErrNotFound = errors.New("automation agent not found")
	ErrConflict = errors.New("automation agent conflict")
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

var _ automationapp.Store = (*Repository)(nil)
var _ automationport.EventAppender = (*Repository)(nil)

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}
func tx(ctx context.Context) (pgx.Tx, error) { return platformpostgres.RequireTransaction(ctx) }

const agentColumns = `id,agent_name,agent_code,automation_type,status,execution_enabled,
draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt,draft_version,published_version,
fixed_content_package,legacy_configuration,created_by,updated_by,created_at,updated_at`

func scanAgent(row pgx.Row) (automationport.Agent, error) {
	var a automationport.Agent
	var kind, status string
	var fixed, legacy []byte
	err := row.Scan(&a.ID, &a.AgentName, &a.AgentCode, &kind, &status, &a.ExecutionEnabled,
		&a.DraftRolePrompt, &a.DraftTaskPrompt, &a.PublishedRolePrompt, &a.PublishedTaskPrompt, &a.DraftVersion, &a.PublishedVersion,
		&fixed, &legacy, &a.CreatedBy, &a.UpdatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return automationport.Agent{}, err
	}
	a.AutomationType, a.Status = automationport.AutomationType(kind), automationport.AgentStatus(status)
	if json.Unmarshal(fixed, &a.FixedContentPackage) != nil || json.Unmarshal(legacy, &a.LegacyConfiguration) != nil {
		return automationport.Agent{}, ErrConflict
	}
	return a, nil
}
func (r *Repository) List(ctx context.Context, kind automationport.AutomationType) ([]automationport.Agent, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + agentColumns + ` FROM automation_agents WHERE archived_at IS NULL`
	args := []any{}
	if kind != "" {
		query += ` AND automation_type=$1`
		args = append(args, string(kind))
	}
	query += ` ORDER BY updated_at DESC,id DESC`
	rows, err := t.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []automationport.Agent{}
	for rows.Next() {
		a, e := scanAgent(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *Repository) Get(ctx context.Context, id automationport.AgentID) (automationport.Agent, error) {
	return r.get(ctx, id, false, false)
}
func (r *Repository) Lock(ctx context.Context, id automationport.AgentID) (automationport.Agent, error) {
	return r.get(ctx, id, false, true)
}
func (r *Repository) get(ctx context.Context, id automationport.AgentID, archived, lock bool) (automationport.Agent, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationport.Agent{}, e
	}
	q := `SELECT ` + agentColumns + ` FROM automation_agents WHERE id=$1`
	if !archived {
		q += ` AND archived_at IS NULL`
	}
	if lock {
		q += ` FOR UPDATE`
	}
	a, e := scanAgent(t.QueryRow(ctx, q, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return automationport.Agent{}, ErrNotFound
	}
	return a, e
}
func (r *Repository) Create(ctx context.Context, a automationport.Agent, now time.Time) (automationport.Agent, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationport.Agent{}, e
	}
	fixed, e := json.Marshal(a.FixedContentPackage)
	if e != nil {
		return automationport.Agent{}, ErrInvalid
	}
	legacy, e := json.Marshal(a.LegacyConfiguration)
	if e != nil {
		return automationport.Agent{}, ErrInvalid
	}
	q := `INSERT INTO automation_agents(agent_name,agent_code,automation_type,status,execution_enabled,draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt,draft_version,published_version,fixed_content_package,legacy_configuration,created_by,updated_by,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13::jsonb,$14,$15,$16,$16) RETURNING ` + agentColumns
	out, e := scanAgent(t.QueryRow(ctx, q, a.AgentName, a.AgentCode, string(a.AutomationType), string(a.Status), a.ExecutionEnabled, a.DraftRolePrompt, a.DraftTaskPrompt, a.PublishedRolePrompt, a.PublishedTaskPrompt, a.DraftVersion, a.PublishedVersion, fixed, legacy, a.CreatedBy, a.UpdatedBy, now))
	if unique(e) {
		// This is a user-visible uniqueness conflict. Return the stable App
		// sentinel rather than the Store-private one so the transaction boundary
		// preserves it and the unchanged HTTP adapter maps it to 409.
		return automationport.Agent{}, automationapp.ErrAgentConflict
	}
	return out, e
}
func (r *Repository) Update(ctx context.Context, a automationport.Agent, now time.Time) (automationport.Agent, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationport.Agent{}, e
	}
	fixed, e := json.Marshal(a.FixedContentPackage)
	if e != nil {
		return automationport.Agent{}, ErrInvalid
	}
	legacy, e := json.Marshal(a.LegacyConfiguration)
	if e != nil {
		return automationport.Agent{}, ErrInvalid
	}
	q := `UPDATE automation_agents SET agent_name=$2,automation_type=$3,status=$4,execution_enabled=$5,draft_role_prompt=$6,draft_task_prompt=$7,published_role_prompt=$8,published_task_prompt=$9,draft_version=$10,published_version=$11,fixed_content_package=$12::jsonb,legacy_configuration=$13::jsonb,updated_by=$14,updated_at=$15::timestamptz,archived_at=CASE WHEN $4='archived' THEN COALESCE(archived_at,$15::timestamptz) ELSE NULL::timestamptz END WHERE id=$1 AND (archived_at IS NULL OR $4='archived') RETURNING ` + agentColumns
	out, e := scanAgent(t.QueryRow(ctx, q, a.ID, a.AgentName, string(a.AutomationType), string(a.Status), a.ExecutionEnabled, a.DraftRolePrompt, a.DraftTaskPrompt, a.PublishedRolePrompt, a.PublishedTaskPrompt, a.DraftVersion, a.PublishedVersion, fixed, legacy, a.UpdatedBy, now))
	if errors.Is(e, pgx.ErrNoRows) {
		return automationport.Agent{}, ErrNotFound
	}
	return out, e
}
func (r *Repository) NextCopyCode(ctx context.Context, base string) (string, error) {
	t, e := tx(ctx)
	if e != nil {
		return "", e
	}
	for n := 1; n < 10000; n++ {
		suffix := "-copy"
		if n > 1 {
			suffix += "-" + strconv.Itoa(n)
		}
		candidate := base + suffix
		var exists bool
		e = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_agents WHERE agent_code=$1)`, candidate).Scan(&exists)
		if e != nil {
			return "", e
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", ErrConflict
}
func (r *Repository) Reserve(ctx context.Context, in automationapp.Reservation) (automationapp.Receipt, bool, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationapp.Receipt{}, false, e
	}
	var id int64
	e = t.QueryRow(ctx, `INSERT INTO automation_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'reserved',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id`, in.Operation, in.ActorScope, in.KeyDigest[:], in.PayloadDigest[:], in.CreatedAt).Scan(&id)
	if e == nil {
		return automationapp.Receipt{ID: id, Operation: in.Operation, ActorScope: in.ActorScope, State: "reserved", KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest}, true, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return automationapp.Receipt{}, false, e
	}
	var key, payload []byte
	var state string
	var result []byte
	e = t.QueryRow(ctx, `SELECT id,key_digest,payload_digest,state,result_snapshot FROM automation_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, in.Operation, in.ActorScope, in.KeyDigest[:]).Scan(&id, &key, &payload, &state, &result)
	if e != nil {
		return automationapp.Receipt{}, false, e
	}
	if len(key) != 32 || len(payload) != 32 {
		return automationapp.Receipt{}, false, ErrConflict
	}
	var kd, pd [32]byte
	copy(kd[:], key)
	copy(pd[:], payload)
	return automationapp.Receipt{ID: id, Operation: in.Operation, ActorScope: in.ActorScope, State: state, KeyDigest: kd, PayloadDigest: pd, ResultSnapshot: result}, false, nil
}
func (r *Repository) Complete(ctx context.Context, id int64, snapshot json.RawMessage, now time.Time) (automationapp.Receipt, error) {
	t, e := tx(ctx)
	if e != nil {
		return automationapp.Receipt{}, e
	}
	var operation, scope, state string
	var key, payload, result []byte
	e = t.QueryRow(ctx, `UPDATE automation_operation_receipts SET state='completed',result_snapshot=$2::jsonb,completed_at=$3 WHERE id=$1 AND state='reserved' RETURNING operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, id, snapshot, now).Scan(&operation, &scope, &key, &payload, &state, &result)
	if errors.Is(e, pgx.ErrNoRows) {
		return automationapp.Receipt{}, ErrConflict
	}
	if e != nil {
		return automationapp.Receipt{}, e
	}
	var kd, pd [32]byte
	copy(kd[:], key)
	copy(pd[:], payload)
	return automationapp.Receipt{ID: id, Operation: operation, ActorScope: scope, State: state, KeyDigest: kd, PayloadDigest: pd, ResultSnapshot: result}, nil
}
func (r *Repository) Append(ctx context.Context, event automationport.Event) (automationport.EventID, error) {
	t, e := tx(ctx)
	if e != nil {
		return 0, e
	}
	var p struct {
		AgentID automationport.AgentID `json:"agent_id"`
		Actor   int64                  `json:"actor"`
	}
	if json.Unmarshal(event.Payload, &p) != nil || p.AgentID < 1 || p.Actor < 1 || event.Type == "" || event.IdempotencyKey == "" {
		return 0, ErrInvalid
	}
	d := sha256.Sum256([]byte(event.IdempotencyKey))
	var id int64
	e = t.QueryRow(ctx, `INSERT INTO automation_audit_events(agent_id,operation,actor_id,occurred_at,payload_digest) VALUES($1,$2,$3,$4,$5) RETURNING id`, p.AgentID, event.Type, p.Actor, event.OccurredAt, d[:]).Scan(&id)
	if e != nil {
		return 0, e
	}
	_, e = t.Exec(ctx, `INSERT INTO automation_outbox(event_type,agent_id,payload,idempotency_digest,occurred_at) VALUES($1,$2,$3::jsonb,$4,$5)`, event.Type, p.AgentID, event.Payload, d[:], event.OccurredAt)
	return automationport.EventID(id), e
}
func unique(e error) bool {
	if e == nil {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(e, &pgErr) && pgErr.Code == "23505" || strings.Contains(strings.ToLower(e.Error()), "unique")
}
