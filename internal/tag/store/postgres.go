// Package store owns PostgreSQL persistence for the local WeCom tag catalog.
// It deliberately contains no customer identity, external_userid, credential,
// or provider-call data.  Sync records are durable local intent/receipt facts;
// outbound execution is coordinated outside this owner.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

var (
	ErrInvalid  = errors.New("invalid tag catalog persistence command")
	ErrNotFound = tagport.ErrNotFound
	ErrConflict = tagport.ErrConflict
)

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

func (r *Repository) ListGroups(ctx context.Context) ([]domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT g.id,g.group_name,g.sort_order,COALESCE(m.state,''),m.readback_at
		FROM tag_groups g
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE group_id=g.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE g.archived_at IS NULL ORDER BY g.sort_order,g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Group{}
	for rows.Next() {
		var v domain.Group
		if err = rows.Scan(&v.ID, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) ListTags(ctx context.Context) ([]domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order,COALESCE(m.state,''),m.readback_at,COALESCE(b.provider_tag_id,'')
		FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id
		LEFT JOIN tag_provider_tag_bindings b ON b.tag_id=t.id
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE tag_id=t.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE t.archived_at IS NULL AND g.archived_at IS NULL ORDER BY g.sort_order,g.id,t.sort_order,t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Tag{}
	for rows.Next() {
		var v domain.Tag
		if err = rows.Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt, &v.ProviderTagID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) ProviderTagNames(ctx context.Context, providerIDs []string) ([]tagport.ProviderTagName, error) {
	if len(providerIDs) == 0 {
		return []tagport.ProviderTagName{}, nil
	}
	t, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT binding.provider_tag_id,tag.tag_name,group_item.group_name
		FROM tag_provider_tag_bindings binding
		JOIN tag_catalog_tags tag ON tag.id=binding.tag_id AND tag.archived_at IS NULL
		JOIN tag_groups group_item ON group_item.id=tag.group_id AND group_item.archived_at IS NULL
		WHERE binding.provider_tag_id=ANY($1::text[]) ORDER BY binding.provider_tag_id`, providerIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []tagport.ProviderTagName{}
	for rows.Next() {
		var item tagport.ProviderTagName
		if err = rows.Scan(&item.ProviderTagID, &item.Name, &item.GroupName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ProviderTagID(ctx context.Context, localTagID int64) (string, bool, error) {
	if localTagID < 1 {
		return "", false, ErrInvalid
	}
	t, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	var providerID string
	err = t.QueryRow(ctx, `SELECT binding.provider_tag_id FROM tag_provider_tag_bindings binding JOIN tag_catalog_tags tag ON tag.id=binding.tag_id WHERE binding.tag_id=$1 AND tag.archived_at IS NULL`, localTagID).Scan(&providerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return providerID, true, nil
}
func (r *Repository) LocalTagID(ctx context.Context, providerTagID string) (int64, bool, error) {
	if providerTagID == "" {
		return 0, false, ErrInvalid
	}
	t, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var localID int64
	err = t.QueryRow(ctx, `SELECT binding.tag_id FROM tag_provider_tag_bindings binding
		JOIN tag_catalog_tags tag ON tag.id=binding.tag_id AND tag.archived_at IS NULL
		WHERE binding.provider_tag_id=$1`, providerTagID).Scan(&localID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return localID, err == nil, err
}

func catalogMutationSource(id int64) string {
	sum := sha256.Sum256([]byte("tag.catalog.mutation.source.v1\x00" + strconv.FormatInt(id, 10)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// GuardCatalogMutation acquires the parent group row before a local catalog
// write. That gives group writes and child writes one lock order, then checks
// the durable mutation receipts while the lock is held. It treats a provider
// outcome as unresolved until it has a terminal receipt; in particular,
// outcome_unknown never permits a new idempotency key to overtake it.
func (r *Repository) GuardCatalogMutation(ctx context.Context, scope tagport.CatalogMutationScope) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	groupID, err := catalogMutationScopeGroupID(ctx, tx, scope)
	if err != nil {
		return err
	}
	return guardCatalogMutationScope(ctx, tx, groupID, scope.TagID)
}

func catalogMutationScopeGroupID(ctx context.Context, tx pgx.Tx, scope tagport.CatalogMutationScope) (int64, error) {
	switch scope.Operation {
	case tagport.CatalogGroupCreate, tagport.CatalogGroupUpdate, tagport.CatalogGroupArchive, tagport.CatalogTagCreate:
		if scope.GroupID < 1 {
			return 0, ErrInvalid
		}
		return scope.GroupID, nil
	case tagport.CatalogTagUpdate, tagport.CatalogTagArchive:
		if scope.TagID < 1 {
			return 0, ErrInvalid
		}
		var groupID int64
		err := tx.QueryRow(ctx, `SELECT group_id FROM tag_catalog_tags WHERE id=$1`, scope.TagID).Scan(&groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrConflict
		}
		if err != nil {
			return 0, err
		}
		return groupID, nil
	default:
		return 0, ErrInvalid
	}
}

func guardCatalogMutationScope(ctx context.Context, tx pgx.Tx, groupID, tagID int64) error {
	if groupID < 1 || tagID < 0 {
		return ErrInvalid
	}
	var lockedGroupID int64
	err := tx.QueryRow(ctx, `SELECT id FROM tag_groups WHERE id=$1 FOR UPDATE`, groupID).Scan(&lockedGroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil || lockedGroupID != groupID {
		return err
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM tag_catalog_mutation_receipts
		WHERE state IN ('reserved','queued','outcome_unknown','retryable_failed')
		  AND (group_id=$1 OR ($2 > 0 AND tag_id=$2))
	)`, groupID, tagID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return ErrConflict
	}
	return nil
}

func (r *Repository) ReserveCatalogMutation(ctx context.Context, plan tagport.CatalogMutationPlan) (tagport.CatalogMutationIntent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	if plan.Actor < 1 || plan.IdempotencyKey == "" || !validCatalogMutationPlan(plan) {
		return tagport.CatalogMutationIntent{}, ErrInvalid
	}
	intent := tagport.CatalogMutationIntent{Operation: plan.Operation, Actor: plan.Actor, GroupID: plan.GroupID, TagID: plan.TagID, GroupName: plan.GroupName, TagName: plan.TagName}
	scope := tagport.CatalogMutationScope{Operation: plan.Operation, GroupID: plan.GroupID, TagID: plan.TagID}
	var scopeGroupID int64
	switch plan.Operation {
	case tagport.CatalogTagCreate:
		err = tx.QueryRow(ctx, `SELECT provider_group_id FROM tag_provider_group_bindings WHERE group_id=$1`, plan.GroupID).Scan(&intent.ProviderGroupID)
	case tagport.CatalogGroupUpdate, tagport.CatalogGroupArchive:
		err = tx.QueryRow(ctx, `SELECT provider_group_id FROM tag_provider_group_bindings WHERE group_id=$1`, plan.GroupID).Scan(&intent.ProviderGroupID)
	case tagport.CatalogTagUpdate, tagport.CatalogTagArchive:
		err = tx.QueryRow(ctx, `SELECT binding.provider_tag_id,tag.group_id FROM tag_provider_tag_bindings binding JOIN tag_catalog_tags tag ON tag.id=binding.tag_id WHERE binding.tag_id=$1`, plan.TagID).Scan(&intent.ProviderTagID, &scope.GroupID)
	case tagport.CatalogGroupCreate:
		// The Provider allocates both identifiers and returns them in its
		// confirmed response; no name lookup is permitted.
	default:
		return tagport.CatalogMutationIntent{}, ErrInvalid
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.CatalogMutationIntent{}, ErrConflict
	}
	if err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	scopeGroupID, err = catalogMutationScopeGroupID(ctx, tx, scope)
	if err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	if err = guardCatalogMutationScope(ctx, tx, scopeGroupID, plan.TagID); err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO tag_catalog_mutation_receipts(operation,actor_admin_user_id,group_id,tag_id,group_name,tag_name,provider_group_id,provider_tag_id,state)
		VALUES($1,$2,NULLIF($3,0),NULLIF($4,0),$5,$6,$7,$8,'reserved') RETURNING id`,
		intent.Operation, intent.Actor, intent.GroupID, intent.TagID, intent.GroupName, intent.TagName, intent.ProviderGroupID, intent.ProviderTagID).Scan(&intent.ID)
	if err != nil {
		return tagport.CatalogMutationIntent{}, err
	}
	source := catalogMutationSource(intent.ID)
	result, err := tx.Exec(ctx, `UPDATE tag_catalog_mutation_receipts SET source_ref_digest=$2 WHERE id=$1 AND state='reserved'`, intent.ID, source)
	if err != nil || result.RowsAffected() != 1 {
		if err != nil {
			return tagport.CatalogMutationIntent{}, err
		}
		return tagport.CatalogMutationIntent{}, ErrConflict
	}
	return intent, nil
}

func validCatalogMutationPlan(plan tagport.CatalogMutationPlan) bool {
	if plan.GroupName != strings.TrimSpace(plan.GroupName) || plan.TagName != strings.TrimSpace(plan.TagName) {
		return false
	}
	switch plan.Operation {
	case tagport.CatalogGroupCreate:
		return plan.GroupID > 0 && plan.TagID > 0 && domain.ValidText(plan.GroupName) && domain.ValidText(plan.TagName)
	case tagport.CatalogTagCreate:
		return plan.GroupID > 0 && plan.TagID > 0 && domain.ValidText(plan.TagName) && plan.GroupName == ""
	case tagport.CatalogGroupUpdate:
		return plan.GroupID > 0 && plan.TagID == 0 && domain.ValidText(plan.GroupName) && plan.TagName == ""
	case tagport.CatalogTagUpdate:
		return plan.GroupID == 0 && plan.TagID > 0 && plan.GroupName == "" && domain.ValidText(plan.TagName)
	case tagport.CatalogGroupArchive:
		return plan.GroupID > 0 && plan.TagID == 0 && plan.GroupName == "" && plan.TagName == ""
	case tagport.CatalogTagArchive:
		return plan.GroupID == 0 && plan.TagID > 0 && plan.GroupName == "" && plan.TagName == ""
	default:
		return false
	}
}

func (r *Repository) AcceptCatalogMutation(ctx context.Context, id int64, effect tagport.CatalogMutationEffectReceipt) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if id < 1 || effect.EffectID < 1 || effect.QueueJobID < 1 || effect.EffectRef == "" || effect.EffectState != "queued" || effect.AcceptReceiptID == "" || effect.QueueReceiptID == "" {
		return ErrInvalid
	}
	result, err := tx.Exec(ctx, `UPDATE tag_catalog_mutation_receipts SET effect_ref=$2,accept_receipt_ref=$3,queue_receipt_ref=$4,state='queued',updated_at=clock_timestamp() WHERE id=$1 AND state='reserved'`, id, effect.EffectRef, effect.AcceptReceiptID, effect.QueueReceiptID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) ReadCatalogMutationDispatch(ctx context.Context, source string) (tagport.CatalogMutationDispatch, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.CatalogMutationDispatch{}, err
	}
	var out tagport.CatalogMutationDispatch
	var operation string
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_admin_user_id,COALESCE(group_id,0),COALESCE(tag_id,0),group_name,tag_name,provider_group_id,provider_tag_id,effect_ref,source_ref_digest
		FROM tag_catalog_mutation_receipts WHERE source_ref_digest=$1 AND state IN ('queued','outcome_unknown','retryable_failed')`, source).Scan(&out.ID, &operation, &out.Actor, &out.GroupID, &out.TagID, &out.GroupName, &out.TagName, &out.ProviderGroupID, &out.ProviderTagID, &out.EffectRef, &out.SourceRefDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.CatalogMutationDispatch{}, ErrNotFound
	}
	if err != nil {
		return tagport.CatalogMutationDispatch{}, err
	}
	out.Operation = tagport.CatalogMutationOperation(operation)
	if out.SourceRefDigest != catalogMutationSource(out.ID) || !validCatalogMutationPlan(tagport.CatalogMutationPlan{Operation: out.Operation, Actor: out.Actor, GroupID: out.GroupID, TagID: out.TagID, GroupName: out.GroupName, TagName: out.TagName, IdempotencyKey: "accepted-intent-key"}) {
		return tagport.CatalogMutationDispatch{}, ErrConflict
	}
	return out, nil
}

func (r *Repository) CompleteCatalogMutation(ctx context.Context, c tagport.CatalogMutationCompletion) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if c.EffectRef == "" || c.Attempt < 1 || c.Generation < 1 || c.Fence < 1 || c.CompletedAt.IsZero() || !validCatalogMutationState(c.State) {
		return ErrInvalid
	}
	var intent tagport.CatalogMutationIntent
	var state, oldDigest string
	var attempts int32
	var generation, fence int64
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_admin_user_id,COALESCE(group_id,0),COALESCE(tag_id,0),group_name,tag_name,provider_group_id,provider_tag_id,state,COALESCE(result_digest,''),attempt_count,completion_generation,completion_fence
		FROM tag_catalog_mutation_receipts WHERE effect_ref=$1 FOR UPDATE`, c.EffectRef).Scan(&intent.ID, &intent.Operation, &intent.Actor, &intent.GroupID, &intent.TagID, &intent.GroupName, &intent.TagName, &intent.ProviderGroupID, &intent.ProviderTagID, &state, &oldDigest, &attempts, &generation, &fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if generation > c.Generation || (generation == c.Generation && fence > c.Fence) {
		return ErrConflict
	}
	if state == c.State && oldDigest == c.ResultDigest && generation == c.Generation && fence == c.Fence && attempts >= c.Attempt {
		return nil
	}
	// A queued completion is only the fenced EER retry of the same immutable
	// effect. Final/unknown writes cannot be reopened by a generic completion.
	if c.State == "queued" {
		if (state != "final_failed" && state != "retryable_failed") || c.Generation != generation+1 || c.Fence != fence || c.Attempt != attempts {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE tag_catalog_mutation_receipts SET state='queued',result_digest=$2,completion_generation=$3,completed_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, intent.ID, c.ResultDigest, c.Generation)
		return err
	}
	if state != "queued" && state != "attempted" && state != "outcome_unknown" && state != "retryable_failed" {
		return ErrConflict
	}
	if c.State == "executed" {
		if err = applyCatalogMutationBinding(ctx, tx, intent, c); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE tag_catalog_mutation_receipts SET state=$2,result_digest=$3,readback_at=$4,attempt_count=GREATEST(attempt_count,$5),completion_generation=$6,completion_fence=$7,completed_at=$8,updated_at=clock_timestamp() WHERE id=$1`, intent.ID, c.State, c.ResultDigest, c.ReadbackAt, c.Attempt, c.Generation, c.Fence, c.CompletedAt.UTC())
	return err
}

func validCatalogMutationState(value string) bool {
	switch value {
	case "queued", "executed", "outcome_unknown", "retryable_failed", "final_failed", "reconciled", "cancelled":
		return true
	default:
		return false
	}
}

func applyCatalogMutationBinding(ctx context.Context, tx pgx.Tx, intent tagport.CatalogMutationIntent, c tagport.CatalogMutationCompletion) error {
	switch intent.Operation {
	case tagport.CatalogGroupCreate:
		if c.ProviderGroupID == "" || c.ProviderTagID == "" {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tag_provider_group_bindings(provider_group_id,group_id) VALUES($1,$2) ON CONFLICT(provider_group_id) DO UPDATE SET group_id=EXCLUDED.group_id,updated_at=clock_timestamp()`, c.ProviderGroupID, intent.GroupID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES($1,$2) ON CONFLICT(provider_tag_id) DO UPDATE SET tag_id=EXCLUDED.tag_id,updated_at=clock_timestamp()`, c.ProviderTagID, intent.TagID)
		return err
	case tagport.CatalogTagCreate:
		if c.ProviderTagID == "" {
			return ErrInvalid
		}
		_, err := tx.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES($1,$2) ON CONFLICT(provider_tag_id) DO UPDATE SET tag_id=EXCLUDED.tag_id,updated_at=clock_timestamp()`, c.ProviderTagID, intent.TagID)
		return err
	default:
		return nil
	}
}

// ListArchiveMutationOperations keeps provider outcomes visible after the
// local catalog row has been archived and therefore no longer appears in the
// normal catalog list. The caller gets local IDs and durable receipt state,
// never provider identifiers or a synthetic delivery result.
func (r *Repository) ListArchiveMutationOperations(ctx context.Context, limit int) ([]tagport.ArchiveMutationOperation, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT operation,group_id,tag_id,state,updated_at,readback_at
		FROM tag_catalog_mutation_receipts
		WHERE operation IN ('group_archive','tag_archive')
		  AND state NOT IN ('executed','reconciled')
		ORDER BY updated_at DESC,id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := []tagport.ArchiveMutationOperation{}
	for rows.Next() {
		var operation string
		var groupID, tagID *int64
		var value tagport.ArchiveMutationOperation
		if err := rows.Scan(&operation, &groupID, &tagID, &value.State, &value.RecordedAt, &value.ReadbackAt); err != nil {
			return nil, err
		}
		value.Operation = tagport.CatalogMutationOperation(operation)
		switch value.Operation {
		case tagport.CatalogGroupArchive:
			if groupID == nil || *groupID < 1 || tagID != nil {
				return nil, ErrConflict
			}
			value.LocalID = *groupID
		case tagport.CatalogTagArchive:
			if tagID == nil || *tagID < 1 || groupID != nil {
				return nil, ErrConflict
			}
			value.LocalID = *tagID
		default:
			return nil, ErrConflict
		}
		operations = append(operations, value)
	}
	return operations, rows.Err()
}

func (r *Repository) GetGroup(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `SELECT g.id,g.group_name,g.sort_order,COALESCE(m.state,''),m.readback_at
		FROM tag_groups g
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE group_id=g.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE g.id=$1 AND g.archived_at IS NULL`, id).Scan(&v.ID, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetGroupIncludingArchived(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `SELECT g.id,g.group_name,g.sort_order,COALESCE(m.state,''),m.readback_at
		FROM tag_groups g
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE group_id=g.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE g.id=$1`, id).Scan(&v.ID, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetTag(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order,COALESCE(m.state,''),m.readback_at,COALESCE(b.provider_tag_id,'')
		FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id
		LEFT JOIN tag_provider_tag_bindings b ON b.tag_id=t.id
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE tag_id=t.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt, &v.ProviderTagID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetTagIncludingArchived(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order,COALESCE(m.state,''),m.readback_at,COALESCE(b.provider_tag_id,'')
		FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id
		LEFT JOIN tag_provider_tag_bindings b ON b.tag_id=t.id
		LEFT JOIN LATERAL (
			SELECT state,readback_at FROM tag_catalog_mutation_receipts
			WHERE tag_id=t.id ORDER BY id DESC LIMIT 1
		) m ON true
		WHERE t.id=$1`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder, &v.ProviderMutationState, &v.ProviderReadbackAt, &v.ProviderTagID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) CreateGroup(ctx context.Context, name string) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag.catalog.groups.order'))`); err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `INSERT INTO tag_groups(group_name,sort_order) SELECT $1,COALESCE(max(sort_order)+1,0) FROM tag_groups WHERE archived_at IS NULL RETURNING id,group_name,sort_order`, name).Scan(&v.ID, &v.Name, &v.SortOrder)
	return v, err
}
func (r *Repository) CreateTag(ctx context.Context, groupID int64, name string) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "tag.catalog.group.order:"+strconv.FormatInt(groupID, 10)); err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `WITH parent AS (SELECT id,group_name FROM tag_groups WHERE id=$1 AND archived_at IS NULL FOR KEY SHARE), inserted AS (INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) SELECT id,$2,COALESCE((SELECT max(sort_order)+1 FROM tag_catalog_tags WHERE group_id=$1 AND archived_at IS NULL),0) FROM parent RETURNING id,group_id,tag_name,sort_order) SELECT i.id,i.group_id,p.group_name,i.tag_name,i.sort_order FROM inserted i JOIN parent p ON p.id=i.group_id`, groupID, name).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) UpdateGroup(ctx context.Context, id int64, name string) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `UPDATE tag_groups SET group_name=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND archived_at IS NULL RETURNING id,group_name,sort_order`, id, name).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ArchiveGroup(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `UPDATE tag_groups SET archived_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND archived_at IS NULL RETURNING id,group_name,sort_order`, id).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE group_id=$1 AND archived_at IS NULL`, id); err != nil {
		return domain.Group{}, err
	}
	return v, nil
}
func (r *Repository) UpdateTag(ctx context.Context, id int64, name string) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET tag_name=$2,version=t.version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id, name).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ArchiveTag(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET archived_at=clock_timestamp(),version=t.version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ReorderGroups(ctx context.Context, ids []int64) ([]domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM tag_groups WHERE archived_at IS NULL FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		current = append(current, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(current, ids) {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=sort_order+(SELECT COALESCE(max(sort_order),0)+count(*)+1 FROM tag_groups WHERE archived_at IS NULL),version=version+1,updated_at=clock_timestamp() WHERE archived_at IS NULL`); err != nil {
		return nil, err
	}
	for i, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, i); err != nil {
			return nil, err
		}
	}
	return r.ListGroups(ctx)
}
func (r *Repository) ReorderTags(ctx context.Context, ids []int64) ([]domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.group_id FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id WHERE t.archived_at IS NULL AND g.archived_at IS NULL ORDER BY g.sort_order,g.id,t.sort_order,t.id FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := []int64{}
	groupByID := map[int64]int64{}
	for rows.Next() {
		var id, groupID int64
		if err = rows.Scan(&id, &groupID); err != nil {
			return nil, err
		}
		current = append(current, id)
		groupByID[id] = groupID
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(current, ids) {
		return nil, ErrConflict
	}
	// The frozen catalog order is group-major. A reorder may permute tags only
	// within their current group; accepting an arbitrary global permutation
	// would silently claim an order ListTags can never project.
	for index, id := range ids {
		if groupByID[id] != groupByID[current[index]] {
			return nil, ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=sort_order+(SELECT COALESCE(max(sort_order),0)+count(*)+1 FROM tag_catalog_tags WHERE archived_at IS NULL),version=version+1,updated_at=clock_timestamp() WHERE archived_at IS NULL`); err != nil {
		return nil, err
	}
	for i, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, i); err != nil {
			return nil, err
		}
	}
	return r.ListTags(ctx)
}
func keyDigest(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }
func (r *Repository) ReserveMutation(ctx context.Context, in tagport.MutationReceiptReservation) (tagport.MutationReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.MutationReceipt{}, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_operation_receipts(operation,actor_admin_user_id,idempotency_key_digest,payload_digest,state) VALUES($1,$2,$3,$4,'in_progress') ON CONFLICT(operation,actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, in.Operation, in.Actor, keyDigest(in.IdempotencyKey), in.PayloadDigest).Scan(&id)
	if err == nil {
		return tagport.MutationReceipt{ID: id, Operation: in.Operation, Actor: in.Actor, IdempotencyKey: in.IdempotencyKey, PayloadDigest: append([]byte(nil), in.PayloadDigest...), State: tagport.MutationInProgress}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tagport.MutationReceipt{}, false, err
	}
	var payload []byte
	var state string
	var results []int64
	err = tx.QueryRow(ctx, `SELECT id,payload_digest,state,result_ids FROM tag_operation_receipts WHERE operation=$1 AND actor_admin_user_id=$2 AND idempotency_key_digest=$3`, in.Operation, in.Actor, keyDigest(in.IdempotencyKey)).Scan(&id, &payload, &state, &results)
	if err != nil {
		return tagport.MutationReceipt{}, false, err
	}
	return tagport.MutationReceipt{ID: id, Operation: in.Operation, Actor: in.Actor, IdempotencyKey: in.IdempotencyKey, PayloadDigest: payload, State: tagport.MutationReceiptState(state), ResultIDs: results}, false, nil
}
func (r *Repository) CompleteMutation(ctx context.Context, id int64, ids []int64, _ time.Time) (tagport.MutationReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.MutationReceipt{}, err
	}
	var state string
	err = tx.QueryRow(ctx, `UPDATE tag_operation_receipts SET state='completed',result_ids=$2,completed_at=clock_timestamp() WHERE id=$1 AND state='in_progress' RETURNING state`, id, ids).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.MutationReceipt{}, ErrConflict
	}
	return tagport.MutationReceipt{ID: id, State: tagport.MutationReceiptState(state), ResultIDs: append([]int64(nil), ids...)}, err
}
func (r *Repository) Append(ctx context.Context, event tagport.Event) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var actor int64
	var payload map[string]any
	_ = json.Unmarshal(event.Payload, &payload)
	if value, ok := payload["actor"].(float64); ok {
		actor = int64(value)
	}
	if actor < 1 {
		return 0, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_audit_events(event_type,actor_admin_user_id,payload,occurred_at) VALUES($1,$2,$3::jsonb,$4) RETURNING id`, event.Type, actor, event.Payload, event.OccurredAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tag_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES($1,'tag_catalog',$2,$3::jsonb)`, event.Type, id, event.Payload)
	return id, err
}
func (r *Repository) TagReferences(ctx context.Context, id int64) (int64, error) {
	return r.referenceCount(ctx, "tag", id)
}
func (r *Repository) GroupReferences(ctx context.Context, id int64) (int64, error) {
	return r.referenceCount(ctx, "group", id)
}
func (r *Repository) referenceCount(ctx context.Context, kind string, id int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_references WHERE resource_kind=$1 AND resource_id=$2`, kind, id).Scan(&n)
	return n, err
}
func (r *Repository) CompleteProviderSync(ctx context.Context, observation tagport.SyncCompletion) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if observation.State == "" {
		observation.State = tagport.SyncExecuted
	}
	if observation.EffectID < 1 || observation.Generation < 1 || !validSyncCompletionState(observation.State) {
		return ErrInvalid
	}
	if observation.State != tagport.SyncExecuted {
		var receiptID, actor int64
		updateErr := tx.QueryRow(ctx, `UPDATE tag_sync_receipts SET state=$2,completed_at=CASE WHEN $2 IN ('final_failed','cancelled','reconciled') THEN clock_timestamp() ELSE NULL END WHERE effect_id=$1 AND state IN ('reserved','queued','outcome_unknown','retryable_failed') RETURNING id,actor_admin_user_id`, observation.EffectID, observation.State).Scan(&receiptID, &actor)
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return ErrConflict
		}
		if updateErr != nil {
			return updateErr
		}
		payload, marshalErr := json.Marshal(map[string]any{"actor": actor, "receipt_id": receiptID, "effect_id": observation.EffectID, "generation": observation.Generation, "state": observation.State})
		if marshalErr != nil {
			return marshalErr
		}
		_, updateErr = r.Append(ctx, tagport.Event{Type: "tag.catalog_sync_state_changed", Payload: payload, OccurredAt: time.Now().UTC(), IdempotencyKey: "tag-catalog-sync-state:" + strconv.FormatInt(observation.EffectID, 10) + ":" + strconv.FormatInt(observation.Generation, 10) + ":" + string(observation.State)})
		return updateErr
	}
	if !validProviderObservation(observation) {
		return ErrInvalid
	}
	var receiptID, actor int64
	err = tx.QueryRow(ctx, `SELECT id,actor_admin_user_id FROM tag_sync_receipts WHERE effect_id=$1 FOR UPDATE`, observation.EffectID).Scan(&receiptID, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var existingDigest string
	var existingSnapshot []byte
	err = tx.QueryRow(ctx, `SELECT artifact_digest,snapshot::text FROM tag_provider_observations WHERE effect_id=$1 AND generation=$2 FOR UPDATE`, observation.EffectID, observation.Generation).Scan(&existingDigest, &existingSnapshot)
	if err == nil {
		if existingDigest == observation.ArtifactDigest && canonicalProviderBytes(existingSnapshot) == string(observation.Snapshot) {
			var state string
			if scanErr := tx.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE id=$1`, receiptID).Scan(&state); scanErr != nil {
				return scanErr
			}
			if state == string(tagport.SyncReceiptExecuted) {
				return nil
			}
			return ErrConflict
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_observations(effect_id,generation,artifact_digest,snapshot) VALUES($1,$2,$3,$4::jsonb)`, observation.EffectID, observation.Generation, observation.ArtifactDigest, observation.Snapshot); err != nil {
		return err
	}
	var snapshot providerSnapshot
	if err = json.Unmarshal(observation.Snapshot, &snapshot); err != nil || snapshot.Groups == nil {
		return ErrInvalid
	}
	groupCount, tagCount, err := r.projectProviderSnapshot(ctx, tx, *snapshot.Groups)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE tag_sync_receipts SET state='executed',group_count=$2,tag_count=$3,completed_at=clock_timestamp() WHERE id=$1 AND state IN ('reserved','queued','outcome_unknown','retryable_failed')`, receiptID, groupCount, tagCount)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	payload, err := json.Marshal(map[string]any{"actor": actor, "receipt_id": receiptID, "effect_id": observation.EffectID, "generation": observation.Generation, "artifact_digest": observation.ArtifactDigest, "group_count": groupCount, "tag_count": tagCount, "state": tagport.SyncExecuted})
	if err != nil {
		return err
	}
	_, err = r.Append(ctx, tagport.Event{Type: "tag.catalog_sync_completed", Payload: payload, OccurredAt: time.Now().UTC(), IdempotencyKey: "tag-catalog-sync-completed:" + strconv.FormatInt(observation.EffectID, 10) + ":" + strconv.FormatInt(observation.Generation, 10)})
	return err
}

func validSyncCompletionState(state tagport.SyncState) bool {
	switch state {
	case tagport.SyncQueued, tagport.SyncExecuted, tagport.SyncOutcomeUnknown, tagport.SyncRetryableFailed, tagport.SyncFinalFailed, tagport.SyncCancelled, tagport.SyncReconciled:
		return true
	default:
		return false
	}
}

func (r *Repository) projectProviderSnapshot(ctx context.Context, tx pgx.Tx, groups []providerGroup) (int, int, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag.catalog.provider.projection'))`); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE tag_groups,tag_catalog_tags,tag_references,tag_provider_group_bindings,tag_provider_tag_bindings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `WITH boundary AS (SELECT COALESCE(max(sort_order),-1)+1 AS base FROM tag_groups WHERE archived_at IS NULL), ranked AS (SELECT id,(SELECT base FROM boundary)+row_number() OVER (ORDER BY sort_order,id)-1 AS next_order FROM tag_groups WHERE archived_at IS NULL) UPDATE tag_groups item SET sort_order=ranked.next_order::integer FROM ranked WHERE item.id=ranked.id`); err != nil {
		return 0, 0, err
	}
	groupBindings := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT provider_group_id,group_id FROM tag_provider_group_bindings`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var providerID string
		var id int64
		if err = rows.Scan(&providerID, &id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		groupBindings[providerID] = id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	seenGroups := map[string]struct{}{}
	for order, group := range groups {
		seenGroups[group.ID] = struct{}{}
		id := groupBindings[group.ID]
		if id == 0 {
			if err = tx.QueryRow(ctx, `INSERT INTO tag_groups(group_name,sort_order) VALUES($1,$2) RETURNING id`, group.Name, order).Scan(&id); err != nil {
				return 0, 0, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_group_bindings(provider_group_id,group_id) VALUES($1,$2)`, group.ID, id); err != nil {
				return 0, 0, err
			}
			groupBindings[group.ID] = id
		} else if _, err = tx.Exec(ctx, `UPDATE tag_groups SET group_name=$2,sort_order=$3,archived_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, group.Name, order); err != nil {
			return 0, 0, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT item.id FROM tag_groups item LEFT JOIN tag_provider_group_bindings binding ON binding.group_id=item.id WHERE item.archived_at IS NULL AND binding.group_id IS NULL ORDER BY item.sort_order,item.id`)
	if err != nil {
		return 0, 0, err
	}
	localGroups := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		localGroups = append(localGroups, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	for index, id := range localGroups {
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=$2,updated_at=clock_timestamp() WHERE id=$1`, id, len(groups)+index); err != nil {
			return 0, 0, err
		}
	}
	if _, err = tx.Exec(ctx, `WITH boundary AS (SELECT group_id,COALESCE(max(sort_order),-1)+1 AS base FROM tag_catalog_tags WHERE archived_at IS NULL GROUP BY group_id), ranked AS (SELECT item.id,boundary.base+row_number() OVER (PARTITION BY item.group_id ORDER BY item.sort_order,item.id)-1 AS next_order FROM tag_catalog_tags item JOIN boundary USING(group_id) WHERE item.archived_at IS NULL) UPDATE tag_catalog_tags item SET sort_order=ranked.next_order::integer FROM ranked WHERE item.id=ranked.id`); err != nil {
		return 0, 0, err
	}
	tagBindings := map[string]int64{}
	rows, err = tx.Query(ctx, `SELECT provider_tag_id,tag_id FROM tag_provider_tag_bindings`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var providerID string
		var id int64
		if err = rows.Scan(&providerID, &id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		tagBindings[providerID] = id
	}
	rows.Close()
	seenTags := map[string]struct{}{}
	providerCountByGroup := map[int64]int{}
	tagCount := 0
	for _, group := range groups {
		groupID := groupBindings[group.ID]
		for order, tag := range group.Tags {
			tagCount++
			seenTags[tag.ID] = struct{}{}
			id := tagBindings[tag.ID]
			if id == 0 {
				if err = tx.QueryRow(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES($1,$2,$3) RETURNING id`, groupID, tag.Name, order).Scan(&id); err != nil {
					return 0, 0, err
				}
				if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES($1,$2)`, tag.ID, id); err != nil {
					return 0, 0, err
				}
				tagBindings[tag.ID] = id
			} else if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET group_id=$2,tag_name=$3,sort_order=$4,archived_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, groupID, tag.Name, order); err != nil {
				return 0, 0, err
			}
		}
		providerCountByGroup[groupID] = len(group.Tags)
	}
	for providerID, id := range tagBindings {
		if _, ok := seenTags[providerID]; ok {
			continue
		}
		var references int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_references WHERE resource_kind='tag' AND resource_id=$1`, id).Scan(&references); err != nil {
			return 0, 0, err
		}
		if references != 0 {
			return 0, 0, ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return 0, 0, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT item.id,item.group_id FROM tag_catalog_tags item LEFT JOIN tag_provider_tag_bindings binding ON binding.tag_id=item.id WHERE item.archived_at IS NULL AND binding.tag_id IS NULL ORDER BY item.group_id,item.sort_order,item.id`)
	if err != nil {
		return 0, 0, err
	}
	type localTag struct{ id, groupID int64 }
	localTags := []localTag{}
	for rows.Next() {
		var id, groupID int64
		if err = rows.Scan(&id, &groupID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		localTags = append(localTags, localTag{id: id, groupID: groupID})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	localTagOffset := map[int64]int{}
	for _, tag := range localTags {
		order := providerCountByGroup[tag.groupID] + localTagOffset[tag.groupID]
		localTagOffset[tag.groupID]++
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=$2,updated_at=clock_timestamp() WHERE id=$1`, tag.id, order); err != nil {
			return 0, 0, err
		}
	}
	for providerID, id := range groupBindings {
		if _, ok := seenGroups[providerID]; ok {
			continue
		}
		var references, localChildren int64
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM tag_references WHERE resource_kind='group' AND resource_id=$1)+(SELECT count(*) FROM tag_references reference JOIN tag_catalog_tags child ON child.id=reference.resource_id WHERE reference.resource_kind='tag' AND child.group_id=$1), (SELECT count(*) FROM tag_catalog_tags child LEFT JOIN tag_provider_tag_bindings binding ON binding.tag_id=child.id WHERE child.group_id=$1 AND child.archived_at IS NULL AND binding.tag_id IS NULL)`, id).Scan(&references, &localChildren); err != nil {
			return 0, 0, err
		}
		if references != 0 || localChildren != 0 {
			return 0, 0, ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE group_id=$1`, id); err != nil {
			return 0, 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return 0, 0, err
		}
	}
	var activeTags int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_catalog_tags item JOIN tag_groups parent ON parent.id=item.group_id WHERE item.archived_at IS NULL AND parent.archived_at IS NULL`).Scan(&activeTags); err != nil {
		return 0, 0, err
	}
	if activeTags > 1000 {
		return 0, 0, ErrConflict
	}
	return len(groups), tagCount, nil
}

type providerSnapshot struct {
	Groups *[]providerGroup `json:"groups"`
}
type providerGroup struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Order int32         `json:"order"`
	Tags  []providerTag `json:"tags"`
}
type providerTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int32  `json:"order"`
}

// validProviderObservation repeats the successful snapshot schema at the
// owned persistence boundary. The sink is intentionally not trusted: both
// digest and canonical snapshot bytes must be independently reproducible here.
func validProviderObservation(observation tagport.SyncCompletion) bool {
	if len(observation.Snapshot) == 0 || len(observation.Snapshot) > 256<<10 || len(observation.ArtifactDigest) != 71 || !strings.HasPrefix(observation.ArtifactDigest, "sha256:") {
		return false
	}
	var snapshot providerSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(observation.Snapshot)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(&struct{}{}) != io.EOF || snapshot.Groups == nil || len(*snapshot.Groups) > 1000 {
		return false
	}
	tags := map[string]struct{}{}
	groups := map[string]struct{}{}
	count := 0
	for _, group := range *snapshot.Groups {
		if !providerID(group.ID) || !providerName(group.Name) {
			return false
		}
		if _, exists := groups[group.ID]; exists {
			return false
		}
		groups[group.ID] = struct{}{}
		for _, tag := range group.Tags {
			count++
			if count > 10000 || !providerID(tag.ID) || !providerName(tag.Name) {
				return false
			}
			if _, exists := tags[tag.ID]; exists {
				return false
			}
			tags[tag.ID] = struct{}{}
		}
	}
	canonical, err := canonicalProviderSnapshot(snapshot)
	if err != nil || string(canonical) != string(observation.Snapshot) {
		return false
	}
	return observation.ArtifactDigest == providerArtifactDigest(canonical)
}

func canonicalProviderBytes(raw []byte) string {
	var snapshot providerSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	canonical, err := canonicalProviderSnapshot(snapshot)
	if err != nil {
		return ""
	}
	return string(canonical)
}

func canonicalProviderSnapshot(snapshot providerSnapshot) ([]byte, error) {
	if snapshot.Groups == nil {
		return nil, errors.New("provider snapshot groups must be explicit")
	}
	sort.SliceStable(*snapshot.Groups, func(i, j int) bool {
		if (*snapshot.Groups)[i].Order == (*snapshot.Groups)[j].Order {
			return (*snapshot.Groups)[i].ID < (*snapshot.Groups)[j].ID
		}
		return (*snapshot.Groups)[i].Order < (*snapshot.Groups)[j].Order
	})
	for i := range *snapshot.Groups {
		if (*snapshot.Groups)[i].Tags == nil {
			(*snapshot.Groups)[i].Tags = []providerTag{}
		}
		sort.SliceStable((*snapshot.Groups)[i].Tags, func(a, b int) bool {
			if (*snapshot.Groups)[i].Tags[a].Order == (*snapshot.Groups)[i].Tags[b].Order {
				return (*snapshot.Groups)[i].Tags[a].ID < (*snapshot.Groups)[i].Tags[b].ID
			}
			return (*snapshot.Groups)[i].Tags[a].Order < (*snapshot.Groups)[i].Tags[b].Order
		})
	}
	return json.Marshal(snapshot)
}

func providerID(value string) bool {
	return providerRequiredText(value, 128)
}
func providerName(value string) bool {
	return providerRequiredText(value, 200)
}
func providerRequiredText(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func providerArtifactDigest(payload []byte) string {
	sum := sha256.Sum256([]byte("external-effect.artifact.v1\x00wecom.tag_catalog.snapshot.v1\x00" + string(payload)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func (r *Repository) ReserveSync(ctx context.Context, c tagport.SyncCommand) (tagport.SyncReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_sync_receipts(actor_admin_user_id,idempotency_key_digest,trace_id,sync_kind,state) VALUES($1,$2,$3,$4,'reserved') ON CONFLICT(actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, c.Actor, keyDigest(c.IdempotencyKey), c.TraceID, c.Kind).Scan(&id)
	if err == nil {
		return tagport.SyncReceipt{ID: id, Command: c, State: tagport.SyncReserved}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "tag_sync_receipts_single_active" {
			return tagport.SyncReceipt{}, tagport.ErrSyncInProgress
		}
		return tagport.SyncReceipt{}, err
	}
	var trace, kind, state, effectRef, effectState, acceptReceipt, queueReceipt string
	var eventID, jobID, effectID int64
	err = tx.QueryRow(ctx, `SELECT id,trace_id,sync_kind,state,event_id,queue_job_id,effect_id,effect_ref,effect_state,accept_receipt_id,queue_receipt_id FROM tag_sync_receipts WHERE actor_admin_user_id=$1 AND idempotency_key_digest=$2`, c.Actor, keyDigest(c.IdempotencyKey)).Scan(&id, &trace, &kind, &state, &eventID, &jobID, &effectID, &effectRef, &effectState, &acceptReceipt, &queueReceipt)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	if trace != c.TraceID || kind != string(c.Kind) {
		return tagport.SyncReceipt{}, ErrConflict
	}
	return tagport.SyncReceipt{ID: id, Command: c, State: tagport.SyncReceiptState(state), EventID: eventID, Effect: tagport.SyncEffectReceipt{QueueJobID: jobID, EffectID: effectID, EffectRef: effectRef, EffectState: effectState, AcceptReceiptID: acceptReceipt, QueueReceiptID: queueReceipt}}, nil
}

func (r *Repository) LatestSync(ctx context.Context) (tagport.SyncStatus, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncStatus{}, err
	}
	var status tagport.SyncStatus
	var state string
	err = tx.QueryRow(ctx, `SELECT id,effect_ref,state,group_count,tag_count,accepted_at,completed_at FROM tag_sync_receipts ORDER BY id DESC LIMIT 1`).Scan(&status.ReceiptID, &status.EffectID, &state, &status.GroupCount, &status.TagCount, &status.AcceptedAt, &status.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.SyncStatus{State: tagport.SyncIdle}, nil
	}
	if err != nil {
		return tagport.SyncStatus{}, err
	}
	status.State = tagport.SyncState(state)
	switch status.State {
	case tagport.SyncQueued, tagport.SyncOutcomeUnknown, tagport.SyncRetryableFailed:
		status.Active = true
	}
	return status, nil
}
func (r *Repository) AcceptSync(ctx context.Context, id, eventID int64, effect tagport.SyncEffectReceipt) (tagport.SyncReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	var actor int64
	var trace, kind, state string
	err = tx.QueryRow(ctx, `UPDATE tag_sync_receipts SET event_id=$2,queue_job_id=$3,effect_id=$4,effect_ref=$5,effect_state=$6,accept_receipt_id=$7,queue_receipt_id=$8,state='queued',accepted_at=clock_timestamp() WHERE id=$1 AND state='reserved' RETURNING actor_admin_user_id,trace_id,sync_kind,state`, id, eventID, effect.QueueJobID, effect.EffectID, effect.EffectRef, effect.EffectState, effect.AcceptReceiptID, effect.QueueReceiptID).Scan(&actor, &trace, &kind, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.SyncReceipt{}, ErrConflict
	}
	return tagport.SyncReceipt{ID: id, Command: tagport.SyncCommand{Actor: actor, TraceID: trace, Kind: tagport.SyncKind(kind)}, State: tagport.SyncReceiptState(state), EventID: eventID, Effect: effect}, err
}
func (r *Repository) ReadExecutionStatus(ctx context.Context) (tagport.ExecutionStatus, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.ExecutionStatus{}, err
	}
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	if err != nil {
		return tagport.ExecutionStatus{}, err
	}
	return tagport.ExecutionStatus{Payload: json.RawMessage(`{"mode":"provider_execution_unavailable","accepted":true,"queued":true,"attempted":false,"executed":false,"outcome_unknown":false,"reconciled":false,"real_external_call_executed":false,"sync_executed":false}`), ObservedAt: now.UTC()}, nil
}
func Digest(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
