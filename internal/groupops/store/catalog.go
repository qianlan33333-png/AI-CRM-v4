package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	"time"
)

func (r *Repository) ListCatalog(ctx context.Context, q string, limit, offset int) (out p.CatalogPage, err error) {
	out = p.CatalogPage{Items: []p.CatalogGroup{}, Limit: limit, Offset: offset}
	if limit < 1 || limit > 100 || offset < 0 {
		return out, ErrInvalid
	}
	err = r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		where, args := directoryGroupFilter(0, q)
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM group_ops_directory_groups"+where, args...).Scan(&out.Total); err != nil {
			return err
		}
		// The search condition has at most one argument; use a separate bounded query.
		rows, err := tx.Query(ctx, `SELECT chat_reference,display_name,owner_user_id,member_count,sync_state,observed_at,checked_at,COALESCE(owner_staff_id,0),external_member_count FROM group_ops_directory_groups WHERE ($1='' OR strpos(lower(display_name),lower($1))>0 OR strpos(lower(chat_reference),lower($1))>0) ORDER BY checked_at DESC,chat_reference LIMIT $2 OFFSET $3`, q, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g p.CatalogGroup
			if err = rows.Scan(&g.ChatID, &g.Name, &g.OwnerUserID, &g.MemberCount, &g.State, &g.ObservedAt, &g.CheckedAt, &g.OwnerStaffID, &g.ExternalMemberCount); err != nil {
				return err
			}
			out.Items = append(out.Items, g)
		}
		return rows.Err()
	})
	return
}
func (r *Repository) ReadCatalogGroup(ctx context.Context, id string) (g p.CatalogGroup, err error) {
	err = r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		return tx.QueryRow(ctx, `SELECT chat_reference,display_name,owner_user_id,member_count,sync_state,observed_at,checked_at,COALESCE(owner_staff_id,0),external_member_count FROM group_ops_directory_groups WHERE chat_reference=$1`, id).Scan(&g.ChatID, &g.Name, &g.OwnerUserID, &g.MemberCount, &g.State, &g.ObservedAt, &g.CheckedAt, &g.OwnerStaffID, &g.ExternalMemberCount)
	})
	return
}
func (r *Repository) SaveCatalogGroup(ctx context.Context, g p.CatalogGroup, runID int64) error {
	return r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		var existed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM group_ops_directory_groups WHERE chat_reference=$1)`, g.ChatID).Scan(&existed); err != nil {
			return err
		}
		digest := string(e.Hash("group.catalog.v1", g.ChatID, g.CheckedAt.Format(time.RFC3339Nano)))
		_, err := tx.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,display_name,owner_user_id,member_count,source_digest,refreshed_at,sync_state,observed_at,checked_at,owner_staff_id,external_member_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$6,NULLIF($9,0),$10)
 ON CONFLICT(chat_reference) DO UPDATE SET
 display_name=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.display_name ELSE group_ops_directory_groups.display_name END,
 owner_staff_id=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.owner_staff_id ELSE group_ops_directory_groups.owner_staff_id END,
 owner_user_id=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.owner_user_id ELSE group_ops_directory_groups.owner_user_id END,
 external_member_count=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.external_member_count ELSE group_ops_directory_groups.external_member_count END,
 member_count=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.member_count ELSE group_ops_directory_groups.member_count END,
 source_digest=EXCLUDED.source_digest,sync_state=EXCLUDED.sync_state,checked_at=EXCLUDED.checked_at,
 observed_at=COALESCE(EXCLUDED.observed_at,group_ops_directory_groups.observed_at),
 refreshed_at=CASE WHEN EXCLUDED.sync_state='ready' THEN EXCLUDED.refreshed_at ELSE group_ops_directory_groups.refreshed_at END
 WHERE group_ops_directory_groups.checked_at<=EXCLUDED.checked_at`, g.ChatID, g.Name, g.OwnerUserID, g.MemberCount, digest, g.CheckedAt, g.State, g.ObservedAt, g.OwnerStaffID, g.ExternalMemberCount)
		if err != nil {
			return err
		}
		if runID == 0 {
			return nil
		}
		outcome := "added"
		if existed {
			outcome = "updated"
		}
		if g.State != "ready" {
			outcome = "failed"
		}
		_, err = tx.Exec(ctx, `INSERT INTO group_ops_catalog_observations(run_id,chat_id,outcome) VALUES($1,$2,$3) ON CONFLICT(run_id,chat_id) DO UPDATE SET outcome=CASE WHEN group_ops_catalog_observations.outcome='added' AND EXCLUDED.outcome='updated' THEN 'added' ELSE EXCLUDED.outcome END`, runID, g.ChatID, outcome)
		return err
	})
}
func (r *Repository) CatalogStatus(ctx context.Context) (out p.CatalogStatus, err error) {
	err = r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		err := tx.QueryRow(ctx, `SELECT next_auto_at,rerun_requested FROM group_ops_catalog_control WHERE singleton`).Scan(&out.NextAutoAt, &out.Rerun)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var run p.CatalogRun
		err = tx.QueryRow(ctx, `SELECT id,state,cursor,started_at,finished_at FROM group_ops_catalog_runs ORDER BY id DESC LIMIT 1`).Scan(&run.ID, &run.State, &run.Cursor, &run.StartedAt, &run.FinishedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome='added'),count(*) FILTER(WHERE outcome='updated'),count(*) FILTER(WHERE outcome='failed') FROM group_ops_catalog_observations WHERE run_id=$1`, run.ID).Scan(&run.Discovered, &run.Added, &run.Updated, &run.Failed)
		out.Run = &run
		return err
	})
	return
}

// WithinCatalogRequest serializes acceptance with River insertion. No provider
// calls occur in this transaction. Daily due time is never moved by manual work.
func (r *Repository) WithinCatalogRequest(ctx context.Context, manual bool, now time.Time, enqueue func(context.Context, int64) error) error {
	return r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO group_ops_catalog_control(singleton,next_auto_at) VALUES(true,$1) ON CONFLICT DO NOTHING`, now); err != nil {
			return err
		}
		var due time.Time
		var rerun bool
		if err := tx.QueryRow(ctx, `SELECT next_auto_at,rerun_requested FROM group_ops_catalog_control WHERE singleton FOR UPDATE`).Scan(&due, &rerun); err != nil {
			return err
		}
		daily := !due.After(now)
		if !manual && !daily {
			return nil
		}
		if daily {
			for !due.After(now) {
				due = due.Add(24 * time.Hour)
			}
			if _, err := tx.Exec(ctx, `UPDATE group_ops_catalog_control SET next_auto_at=$1 WHERE singleton`, due); err != nil {
				return err
			}
		}
		var active int64
		err := tx.QueryRow(ctx, `SELECT id FROM group_ops_catalog_runs WHERE state IN ('queued','running')`).Scan(&active)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE group_ops_catalog_control SET rerun_requested=true WHERE singleton`)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return r.createCatalogRun(ctx, now, enqueue)
	})
}
func (r *Repository) createCatalogRun(ctx context.Context, now time.Time, enqueue func(context.Context, int64) error) error {
	tx, _ := transaction(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO group_ops_catalog_runs(state,started_at) VALUES('queued',$1) RETURNING id`, now).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE group_ops_catalog_control SET rerun_requested=false WHERE singleton`); err != nil {
		return err
	}
	return enqueue(ctx, id)
}
func (r *Repository) StartCatalogRun(ctx context.Context, id int64) (run p.CatalogRun, err error) {
	err = r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		return tx.QueryRow(ctx, `UPDATE group_ops_catalog_runs SET state=CASE WHEN state='queued' THEN 'running' ELSE state END WHERE id=$1 RETURNING id,state,cursor,started_at`, id).Scan(&run.ID, &run.State, &run.Cursor, &run.StartedAt)
	})
	return
}
func (r *Repository) CheckpointCatalogRun(ctx context.Context, id int64, cursor string) error {
	return r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		_, err := tx.Exec(ctx, `UPDATE group_ops_catalog_runs SET cursor=$2 WHERE id=$1 AND state='running'`, id, cursor)
		return err
	})
}
func (r *Repository) FinishCatalogRun(ctx context.Context, id int64, failed bool, now time.Time, enqueue func(context.Context, int64) error) error {
	return r.uow.Within(ctx, func(ctx context.Context) error {
		tx, _ := transaction(ctx)
		var rerun bool
		if err := tx.QueryRow(ctx, `SELECT rerun_requested FROM group_ops_catalog_control WHERE singleton FOR UPDATE`).Scan(&rerun); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE group_ops_catalog_runs SET state=CASE WHEN $2 THEN 'failed' WHEN EXISTS(SELECT 1 FROM group_ops_catalog_observations WHERE run_id=$1 AND outcome='failed') THEN 'partial' ELSE 'completed' END,finished_at=$3 WHERE id=$1 AND state IN ('queued','running')`, id, failed, now)
		if err != nil {
			return err
		}
		if rerun && result.RowsAffected() > 0 {
			return r.createCatalogRun(ctx, now, enqueue)
		}
		return nil
	})
}
