package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const memberGridViewColumns = `id,product_id,name,position,config_json,version,created_by,updated_by,created_at,updated_at`
const memberGridCollaboratorColumns = `id,product_id,admin_user_id,permission,version,created_by,updated_by,created_at,updated_at`
const memberGridShareColumns = `product_id,enabled,public_id,generation,version,created_by,updated_by,created_at,updated_at`

func scanMemberGridView(row rowScanner) (v productport.MemberGridView, err error) {
	var config []byte
	err = row.Scan(&v.ID, &v.ProductID, &v.Name, &v.Position, &config, &v.Version, &v.CreatedBy, &v.UpdatedBy, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, productport.ErrProductReadNotFound
	}
	if err != nil {
		return v, mapDatabaseError(err)
	}
	if !json.Valid(config) {
		return v, productport.ErrProductReadUnavailable
	}
	v.Config = append(json.RawMessage(nil), config...)
	return v, nil
}
func scanMemberGridCollaborator(row rowScanner) (v productport.MemberGridCollaborator, err error) {
	err = row.Scan(&v.ID, &v.ProductID, &v.AdminUserID, &v.Permission, &v.Version, &v.CreatedBy, &v.UpdatedBy, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, productport.ErrProductReadNotFound
	}
	if err != nil {
		return v, mapDatabaseError(err)
	}
	return v, nil
}
func scanMemberGridShare(row rowScanner) (v productport.MemberGridShare, err error) {
	err = row.Scan(&v.ProductID, &v.Enabled, &v.PublicID, &v.Generation, &v.Version, &v.CreatedBy, &v.UpdatedBy, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, productport.ErrProductReadNotFound
	}
	if err != nil {
		return v, mapDatabaseError(err)
	}
	return v, nil
}

func (r *Repository) ListMemberGridViews(ctx context.Context, id productport.ID) ([]productport.MemberGridView, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT `+memberGridViewColumns+` FROM product_service_period_member_views WHERE product_id=$1 ORDER BY position,id`, id)
	if e != nil {
		return nil, mapDatabaseError(e)
	}
	defer rows.Close()
	out := []productport.MemberGridView{}
	for rows.Next() {
		v, e := scanMemberGridView(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) GetMemberGridView(ctx context.Context, id, viewID productport.ID) (productport.MemberGridView, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridView{}, e
	}
	return scanMemberGridView(tx.QueryRow(ctx, `SELECT `+memberGridViewColumns+` FROM product_service_period_member_views WHERE product_id=$1 AND id=$2`, id, viewID))
}
func (r *Repository) CreateMemberGridView(ctx context.Context, v productport.MemberGridView) (productport.MemberGridView, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return v, e
	}
	return scanMemberGridView(tx.QueryRow(ctx, `INSERT INTO product_service_period_member_views(product_id,name,position,config_json,created_by,updated_by,created_at,updated_at) VALUES($1,$2,(SELECT COALESCE(MAX(position)+1,0) FROM product_service_period_member_views WHERE product_id=$1),$3,$4,$4,$5,$5) RETURNING `+memberGridViewColumns, v.ProductID, v.Name, v.Config, v.CreatedBy, v.CreatedAt))
}
func (r *Repository) UpdateMemberGridView(ctx context.Context, v productport.MemberGridView) (productport.MemberGridView, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return v, e
	}
	return scanMemberGridView(tx.QueryRow(ctx, `UPDATE product_service_period_member_views SET name=$3,config_json=$4,version=version+1,updated_by=$5,updated_at=$6 WHERE product_id=$1 AND id=$2 AND version=$7 RETURNING `+memberGridViewColumns, v.ProductID, v.ID, v.Name, v.Config, v.UpdatedBy, v.UpdatedAt, v.Version))
}
func (r *Repository) DeleteMemberGridView(ctx context.Context, id, viewID productport.ID, version int64) (productport.MemberGridView, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridView{}, e
	}
	return scanMemberGridView(tx.QueryRow(ctx, `DELETE FROM product_service_period_member_views WHERE product_id=$1 AND id=$2 AND version=$3 RETURNING `+memberGridViewColumns, id, viewID, version))
}
func (r *Repository) ListMemberGridCollaborators(ctx context.Context, id productport.ID) ([]productport.MemberGridCollaborator, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT `+memberGridCollaboratorColumns+` FROM product_service_period_member_collaborators WHERE product_id=$1 ORDER BY id`, id)
	if e != nil {
		return nil, mapDatabaseError(e)
	}
	defer rows.Close()
	out := []productport.MemberGridCollaborator{}
	for rows.Next() {
		v, e := scanMemberGridCollaborator(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) FindMemberGridCollaborator(ctx context.Context, id productport.ID, adminID int64) (productport.MemberGridCollaborator, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridCollaborator{}, e
	}
	return scanMemberGridCollaborator(tx.QueryRow(ctx, `SELECT `+memberGridCollaboratorColumns+` FROM product_service_period_member_collaborators WHERE product_id=$1 AND admin_user_id=$2`, id, adminID))
}
func (r *Repository) CreateMemberGridCollaborator(ctx context.Context, v productport.MemberGridCollaborator) (productport.MemberGridCollaborator, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return v, e
	}
	return scanMemberGridCollaborator(tx.QueryRow(ctx, `INSERT INTO product_service_period_member_collaborators(product_id,admin_user_id,permission,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$4,$5,$5) RETURNING `+memberGridCollaboratorColumns, v.ProductID, v.AdminUserID, v.Permission, v.CreatedBy, v.CreatedAt))
}
func (r *Repository) UpdateMemberGridCollaborator(ctx context.Context, v productport.MemberGridCollaborator) (productport.MemberGridCollaborator, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return v, e
	}
	return scanMemberGridCollaborator(tx.QueryRow(ctx, `UPDATE product_service_period_member_collaborators SET permission=$3,version=version+1,updated_by=$4,updated_at=$5 WHERE product_id=$1 AND id=$2 AND version=$6 RETURNING `+memberGridCollaboratorColumns, v.ProductID, v.ID, v.Permission, v.UpdatedBy, v.UpdatedAt, v.Version))
}
func (r *Repository) DeleteMemberGridCollaborator(ctx context.Context, id, cid productport.ID, version int64) (productport.MemberGridCollaborator, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridCollaborator{}, e
	}
	return scanMemberGridCollaborator(tx.QueryRow(ctx, `DELETE FROM product_service_period_member_collaborators WHERE product_id=$1 AND id=$2 AND version=$3 RETURNING `+memberGridCollaboratorColumns, id, cid, version))
}
func (r *Repository) GetMemberGridShare(ctx context.Context, id productport.ID) (productport.MemberGridShare, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridShare{}, e
	}
	return scanMemberGridShare(tx.QueryRow(ctx, `SELECT `+memberGridShareColumns+` FROM product_service_period_member_shares WHERE product_id=$1`, id))
}
func (r *Repository) GetMemberGridShareByToken(ctx context.Context, token string) (productport.MemberGridShare, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return productport.MemberGridShare{}, e
	}
	return scanMemberGridShare(tx.QueryRow(ctx, `SELECT `+memberGridShareColumns+` FROM product_service_period_member_shares WHERE enabled AND public_id=$1`, token))
}
func (r *Repository) SetMemberGridShare(ctx context.Context, v productport.MemberGridShare, expected int64) (productport.MemberGridShare, error) {
	tx, e := transaction(ctx)
	if e != nil {
		return v, e
	}
	if expected == 0 {
		return scanMemberGridShare(tx.QueryRow(ctx, `INSERT INTO product_service_period_member_shares(product_id,enabled,public_id,generation,version,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5,$6,$6) ON CONFLICT(product_id) DO NOTHING RETURNING `+memberGridShareColumns, v.ProductID, v.Enabled, v.PublicID, v.Generation, v.CreatedBy, v.CreatedAt))
	}
	return scanMemberGridShare(tx.QueryRow(ctx, `UPDATE product_service_period_member_shares SET enabled=$3,public_id=$4,generation=$5,version=version+1,updated_by=$6,updated_at=$7 WHERE product_id=$1 AND version=$2 RETURNING `+memberGridShareColumns, v.ProductID, expected, v.Enabled, v.PublicID, v.Generation, v.UpdatedBy, v.UpdatedAt))
}

var _ = time.Time{}
