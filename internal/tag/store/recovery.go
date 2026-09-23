package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"strings"
)

func (r *Repository) ListCatalogMutationRecoveries(ctx context.Context) ([]tagport.CatalogMutationRecovery, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.operation,COALESCE(NULLIF(m.tag_name,''),NULLIF(m.group_name,''),t.tag_name,g.group_name,''),m.state,m.completion_generation
 FROM tag_catalog_mutation_receipts m LEFT JOIN tag_catalog_tags t ON t.id=m.tag_id LEFT JOIN tag_groups g ON g.id=m.group_id
 WHERE m.state IN ('final_failed','retryable_failed') ORDER BY m.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tagport.CatalogMutationRecovery{}
	for rows.Next() {
		var v tagport.CatalogMutationRecovery
		if err = rows.Scan(&v.ID, &v.Operation, &v.Name, &v.State, &v.Generation); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) LockCatalogMutationRecovery(ctx context.Context, id int64) (tagport.CatalogMutationDispatch, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.CatalogMutationDispatch{}, err
	}
	if id < 1 {
		return tagport.CatalogMutationDispatch{}, ErrInvalid
	}
	var v tagport.CatalogMutationDispatch
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_admin_user_id,COALESCE(group_id,0),COALESCE(tag_id,0),group_name,tag_name,provider_group_id,provider_tag_id,effect_ref,source_ref_digest FROM tag_catalog_mutation_receipts WHERE id=$1`, id).Scan(&v.ID, &v.Operation, &v.Actor, &v.GroupID, &v.TagID, &v.GroupName, &v.TagName, &v.ProviderGroupID, &v.ProviderTagID, &v.EffectRef, &v.SourceRefDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	groupID, err := catalogMutationScopeGroupID(ctx, tx, tagport.CatalogMutationScope{Operation: v.Operation, GroupID: v.GroupID, TagID: v.TagID})
	if err != nil {
		return v, err
	}
	// Match ordinary catalog commands' group-first lock order. A later mutation
	// makes this historical payload stale, even if that later write also failed.
	var locked int64
	if err = tx.QueryRow(ctx, `SELECT id FROM tag_groups WHERE id=$1 FOR UPDATE`, groupID).Scan(&locked); err != nil {
		return v, err
	}
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM tag_catalog_mutation_receipts WHERE id=$1 FOR UPDATE`, id).Scan(&state); err != nil {
		return v, err
	}
	if state != "final_failed" && state != "retryable_failed" {
		// EER may only replay an already accepted retry key in these states.
		// A new key is rejected there before any queue or Provider activity.
		return v, nil
	}
	var stale bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tag_catalog_mutation_receipts m LEFT JOIN tag_catalog_tags t ON t.id=m.tag_id WHERE m.id<>$1 AND ((m.id>$1 AND (m.tag_id=$3 OR m.tag_id IS NULL OR $4)) OR m.state IN ('reserved','queued','outcome_unknown','retryable_failed')) AND (m.group_id=$2 OR t.group_id=$2))`, id, groupID, v.TagID, strings.HasPrefix(string(v.Operation), "group_")).Scan(&stale)
	if err != nil {
		return v, err
	}
	if stale {
		return v, ErrConflict
	}
	// The immutable payload must still match its local resources. Sync or
	// later local edits cannot turn a historical retry into a different write.
	var current bool
	switch v.Operation {
	case tagport.CatalogGroupCreate:
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tag_groups g JOIN tag_catalog_tags t ON t.group_id=g.id WHERE g.id=$1 AND t.id=$2 AND g.group_name=$3 AND t.tag_name=$4 AND g.archived_at IS NULL AND t.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM tag_provider_group_bindings b WHERE b.group_id=g.id) AND NOT EXISTS(SELECT 1 FROM tag_provider_tag_bindings b WHERE b.tag_id=t.id))`, v.GroupID, v.TagID, v.GroupName, v.TagName).Scan(&current)
	case tagport.CatalogTagCreate:
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id JOIN tag_provider_group_bindings b ON b.group_id=g.id WHERE t.id=$1 AND g.id=$2 AND t.tag_name=$3 AND b.provider_group_id=$4 AND t.archived_at IS NULL AND g.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM tag_provider_tag_bindings b WHERE b.tag_id=t.id))`, v.TagID, v.GroupID, v.TagName, v.ProviderGroupID).Scan(&current)
	case tagport.CatalogGroupUpdate, tagport.CatalogGroupArchive:
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tag_groups g JOIN tag_provider_group_bindings b ON b.group_id=g.id WHERE g.id=$1 AND b.provider_group_id=$2 AND (($3='group_update' AND g.archived_at IS NULL AND g.group_name=$4) OR ($3='group_archive' AND g.archived_at IS NOT NULL)))`, v.GroupID, v.ProviderGroupID, string(v.Operation), v.GroupName).Scan(&current)
	case tagport.CatalogTagUpdate, tagport.CatalogTagArchive:
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tag_catalog_tags t JOIN tag_provider_tag_bindings b ON b.tag_id=t.id WHERE t.id=$1 AND b.provider_tag_id=$2 AND (($3='tag_update' AND t.archived_at IS NULL AND t.tag_name=$4) OR ($3='tag_archive' AND t.archived_at IS NOT NULL)))`, v.TagID, v.ProviderTagID, string(v.Operation), v.TagName).Scan(&current)
	}
	if err != nil {
		return v, err
	}
	if !current {
		return v, ErrConflict
	}
	return v, nil
}
