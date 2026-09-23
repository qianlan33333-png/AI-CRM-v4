package cutoverproof

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type Beginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func Capture(ctx context.Context, db Beginner, scopes Scopes) (Snapshot, error) {
	s := Snapshot{Version: 1, Scopes: scopes, Rows: []Row{}}
	if db == nil || scopes.Validate() != nil {
		return s, ErrInvalid
	}
	return capture(ctx, db, s)
}

func CaptureExistingWecom(ctx context.Context, db Beginner, corp string) (Snapshot, error) {
	s := Snapshot{Version: 2, ResolutionMode: ExistingWecomOnly, Scopes: Scopes{CorpID: corp}, Rows: []Row{}}
	if db == nil || validateCorp(corp) != nil {
		return s, ErrInvalid
	}
	return capture(ctx, db, s)
}

func capture(ctx context.Context, db Beginner, s Snapshot) (Snapshot, error) {
	tx, e := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return s, errors.New("begin proof capture")
	}
	defer tx.Rollback(ctx)
	if e = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&s.CapturedAt); e != nil {
		return s, errors.New("proof capture timestamp")
	}
	rows, e := tx.Query(ctx, `WITH required AS (
 SELECT unionid FROM service_period_entitlements WHERE tenant_id='aicrm' AND status IN ('active','expired','refunded')
 UNION SELECT unionid FROM commerce_coupon_claims WHERE tenant_id='aicrm'
 ) SELECT jsonb_build_object(
 'unionid',r.unionid,'crm_status',COALESCE(c.identity_status,''),'primary_external_id',COALESCE(c.primary_external_userid,''),
 'evidence',COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'map_id',m.id,'corp_id',m.corp_id,'external_id',m.external_userid,'unionid',m.unionid,'status',m.status,
 'provider_ok',COALESCE(jsonb_typeof(m.raw_profile->'errcode')='number' AND m.raw_profile->'errcode'='0'::jsonb AND jsonb_typeof(m.raw_profile->'external_contact'->'unionid')='string' AND jsonb_typeof(m.raw_profile->'external_contact'->'external_userid')='string',false),
 'raw_unionid',COALESCE(m.raw_profile->'external_contact'->>'unionid',''),
 'raw_external_id',COALESCE(m.raw_profile->'external_contact'->>'external_userid','')
 ) ORDER BY m.id) FROM wecom_external_contact_identity_map m WHERE m.corp_id=$1 AND m.unionid=r.unionid AND m.external_userid=c.primary_external_userid),'[]'::jsonb)
 ) FROM required r LEFT JOIN crm_user_identity c ON c.unionid=r.unionid ORDER BY r.unionid`, s.Scopes.CorpID)
	if e != nil {
		return s, errors.New("proof source query failed")
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return s, errors.New("proof row scan failed")
		}
		var r Row
		if e = json.Unmarshal(raw, &r); e != nil {
			return s, ErrInvalid
		}
		s.Rows = append(s.Rows, r)
	}
	if rows.Err() != nil {
		return s, errors.New("proof source rows failed")
	}
	if s.Validate() != nil {
		return s, ErrInvalid
	}
	if e = tx.Commit(ctx); e != nil {
		return s, errors.New("proof capture commit")
	}
	return s, nil
}
