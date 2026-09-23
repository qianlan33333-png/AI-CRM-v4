package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

const bindingColumns = `id,package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_actor_kind,created_actor_ref,created_at`
const currentBindingColumns = `b.id,b.package_id,b.version,b.agent_id,b.automation_type,b.agent_published_version,b.content_digest,b.materials_digest,b.created_by,b.created_actor_kind,b.created_actor_ref,b.created_at`

func scanBinding(row pgx.Row) (segmentdomain.AutomationBinding, error) {
	var out segmentdomain.AutomationBinding
	var kind string
	var content, materials []byte
	var createdBy *int64
	err := row.Scan(&out.ID, &out.PackageID, &out.Version, &out.AgentID, &kind, &out.AgentPublishedVersion, &content, &materials, &createdBy, &out.CreatedActorKind, &out.CreatedActorRef, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if len(content) != sha256.Size || len(materials) != sha256.Size {
		return out, ErrConflict
	}
	if createdBy != nil {
		out.CreatedBy = *createdBy
	}
	copy(out.ContentDigest[:], content)
	copy(out.MaterialsDigest[:], materials)
	out.AutomationType = automationport.AutomationType(kind)
	return out, nil
}

func (r *Repository) CurrentBinding(ctx context.Context, packageID int64) (segmentdomain.AutomationBinding, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.AutomationBinding{}, err
	}
	return scanBinding(t.QueryRow(ctx, `SELECT `+currentBindingColumns+` FROM segment_audience_packages p JOIN segment_audience_automation_binding_versions b ON b.id=p.current_automation_binding_id AND b.package_id=p.id WHERE p.id=$1`, packageID))
}

func (r *Repository) CreateBinding(ctx context.Context, item segmentdomain.AutomationBinding) (segmentdomain.AutomationBinding, error) {
	t, err := tx(ctx)
	if err != nil {
		return item, err
	}
	actor := Actor{Kind: item.CreatedActorKind, Reference: item.CreatedActorRef, StaffID: item.CreatedBy}
	if !actor.Valid() {
		return item, ErrInvalid
	}
	var version int64
	if err = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_automation_binding_versions WHERE package_id=$1`, item.PackageID).Scan(&version); err != nil {
		return item, err
	}
	item.Version = version
	return scanBinding(t.QueryRow(ctx, `INSERT INTO segment_audience_automation_binding_versions(package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_actor_kind,created_actor_ref,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,$10,$11) RETURNING `+bindingColumns, item.PackageID, item.Version, item.AgentID, item.AutomationType, item.AgentPublishedVersion, item.ContentDigest[:], item.MaterialsDigest[:], item.CreatedBy, item.CreatedActorKind, item.CreatedActorRef, item.CreatedAt))
}

func (r *Repository) SetCurrentBinding(ctx context.Context, packageID, bindingID, expectedVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	return r.SetCurrentBindingWithActor(ctx, packageID, bindingID, expectedVersion, adminActor(actor), now)
}

func (r *Repository) SetCurrentBindingWithActor(ctx context.Context, packageID, bindingID, expectedVersion int64, actor Actor, now time.Time) (segmentdomain.Package, error) {
	if !actor.Valid() {
		return segmentdomain.Package{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	item, err := scanPackage(t.QueryRow(ctx, `UPDATE segment_audience_packages SET current_automation_binding_id=$2,version=version+1,updated_by=NULLIF($4,0),updated_actor_kind=$5,updated_actor_ref=$6,updated_at=$7 WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING `+packageColumns, packageID, bindingID, expectedVersion, actor.StaffID, actor.Kind, actor.Reference, now))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	return item, err
}

func (r *Repository) ClearCurrentBinding(ctx context.Context, packageID, expectedVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	return r.ClearCurrentBindingWithActor(ctx, packageID, expectedVersion, adminActor(actor), now)
}

func (r *Repository) ClearCurrentBindingWithActor(ctx context.Context, packageID, expectedVersion int64, actor Actor, now time.Time) (segmentdomain.Package, error) {
	if !actor.Valid() {
		return segmentdomain.Package{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	item, err := scanPackage(t.QueryRow(ctx, `UPDATE segment_audience_packages SET current_automation_binding_id=NULL,version=version+1,updated_by=NULLIF($3,0),updated_actor_kind=$4,updated_actor_ref=$5,updated_at=$6 WHERE id=$1 AND version=$2 AND lifecycle='paused' AND current_automation_binding_id IS NOT NULL RETURNING `+packageColumns, packageID, expectedVersion, actor.StaffID, actor.Kind, actor.Reference, now))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	return item, err
}

func (r *Repository) CurrentSenderSet(ctx context.Context, packageID int64) (segmentdomain.SenderSet, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.SenderSet{}, err
	}
	var out segmentdomain.SenderSet
	var createdBy *int64
	err = t.QueryRow(ctx, `SELECT s.id,s.package_id,s.version,s.created_by,s.created_actor_kind,s.created_actor_ref,s.created_at FROM segment_audience_packages p JOIN segment_audience_sender_sets s ON s.id=p.current_sender_set_id AND s.package_id=p.id WHERE p.id=$1`, packageID).Scan(&out.ID, &out.PackageID, &out.Version, &createdBy, &out.CreatedActorKind, &out.CreatedActorRef, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if createdBy != nil {
		out.CreatedBy = *createdBy
	}
	rows, err := t.Query(ctx, `SELECT sort_order,staff_id,eligibility_version,eligibility_refreshed_at FROM segment_audience_sender_set_members WHERE sender_set_id=$1 ORDER BY sort_order`, out.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item segmentdomain.Sender
		if err = rows.Scan(&item.SortOrder, &item.StaffID, &item.EligibilityVersion, &item.EligibilityRefreshedAt); err != nil {
			return out, err
		}
		out.Members = append(out.Members, item)
	}
	return out, rows.Err()
}

func (r *Repository) CreateSenderSet(ctx context.Context, item segmentdomain.SenderSet) (segmentdomain.SenderSet, error) {
	t, err := tx(ctx)
	if err != nil {
		return item, err
	}
	actor := Actor{Kind: item.CreatedActorKind, Reference: item.CreatedActorRef, StaffID: item.CreatedBy}
	if len(item.Members) < 1 || len(item.Members) > 5 || !actor.Valid() {
		return item, ErrInvalid
	}
	if err = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_sender_sets WHERE package_id=$1`, item.PackageID).Scan(&item.Version); err != nil {
		return item, err
	}
	if err = t.QueryRow(ctx, `INSERT INTO segment_audience_sender_sets(package_id,version,created_by,created_actor_kind,created_actor_ref,created_at) VALUES($1,$2,NULLIF($3,0),$4,$5,$6) RETURNING id`, item.PackageID, item.Version, item.CreatedBy, item.CreatedActorKind, item.CreatedActorRef, item.CreatedAt).Scan(&item.ID); err != nil {
		return item, err
	}
	for index, member := range item.Members {
		if member.StaffID < 1 || member.EligibilityVersion < 1 || member.EligibilityRefreshedAt.IsZero() {
			return item, ErrInvalid
		}
		member.SortOrder = index + 1
		item.Members[index] = member
		if _, err = t.Exec(ctx, `INSERT INTO segment_audience_sender_set_members(sender_set_id,sort_order,staff_id,eligibility_version,eligibility_refreshed_at) VALUES($1,$2,$3,$4,$5)`, item.ID, member.SortOrder, member.StaffID, member.EligibilityVersion, member.EligibilityRefreshedAt); err != nil {
			if unique(err) {
				return item, ErrConflict
			}
			return item, err
		}
	}
	return item, nil
}

func (r *Repository) SetCurrentSenderSet(ctx context.Context, packageID, senderSetID, expectedVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	return r.SetCurrentSenderSetWithActor(ctx, packageID, senderSetID, expectedVersion, adminActor(actor), now)
}

func (r *Repository) SetCurrentSenderSetWithActor(ctx context.Context, packageID, senderSetID, expectedVersion int64, actor Actor, now time.Time) (segmentdomain.Package, error) {
	if !actor.Valid() {
		return segmentdomain.Package{}, ErrInvalid
	}
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	item, err := scanPackage(t.QueryRow(ctx, `UPDATE segment_audience_packages SET current_sender_set_id=$2,version=version+1,updated_by=NULLIF($4,0),updated_actor_kind=$5,updated_actor_ref=$6,updated_at=$7 WHERE id=$1 AND version=$3 AND lifecycle IN ('paused','active') RETURNING `+packageColumns, packageID, senderSetID, expectedVersion, actor.StaffID, actor.Kind, actor.Reference, now))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	return item, err
}
