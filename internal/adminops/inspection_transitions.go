package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type inspectionTransition struct {
	ID            int64
	CheckID, Code string
}

// critical_active survives temporary unknown/warning observations. Only a
// fresh successful observation closes the incident and permits recovery.
func recordInspectionIssue(ctx context.Context, tx pgx.Tx, r opsport.CheckResult, at time.Time) (*inspectionTransition, *inspectionTransition, error) {
	if r.Status == "ok" {
		var id int64
		e := tx.QueryRow(ctx, `SELECT COALESCE(min(id),0) FROM adminops_inspection_issues WHERE check_id=$1 AND status<>'resolved' AND last_seen<=$2 AND critical_active`, r.ID, at).Scan(&id)
		if e != nil {
			return nil, nil, e
		}
		_, e = tx.Exec(ctx, `UPDATE adminops_inspection_issues SET status='resolved',resolved_at=$2,critical_active=false,version=version+1 WHERE check_id=$1 AND status<>'resolved' AND last_seen<=$2`, r.ID, at)
		if e != nil {
			return nil, nil, e
		}
		if e = recoverIncidentEpisodes(ctx, tx, r.ID, at); e != nil {
			return nil, nil, e
		}
		if id > 0 {
			return nil, &inspectionTransition{ID: id, CheckID: r.ID, Code: "fresh_observation_recovered"}, nil
		}
		return nil, nil, nil
	}
	fingerprint := string(effectport.Hash("ops-issue-v1", r.ID, r.Code))
	var active bool
	e := tx.QueryRow(ctx, `SELECT critical_active FROM adminops_inspection_issues WHERE fingerprint=$1 FOR UPDATE`, fingerprint).Scan(&active)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, nil, e
	}
	newlyCritical := r.Status == "critical" && !active
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO adminops_inspection_issues(fingerprint,check_id,code,status,severity,first_seen,last_seen,critical_active) VALUES($1,$2,$3,'open',$4,$5,$5,$6)
 ON CONFLICT(fingerprint) DO UPDATE SET status=CASE WHEN adminops_inspection_issues.status='resolved' THEN 'open' ELSE adminops_inspection_issues.status END,
 severity=EXCLUDED.severity,last_seen=GREATEST(adminops_inspection_issues.last_seen,EXCLUDED.last_seen),resolved_at=NULL,critical_active=adminops_inspection_issues.critical_active OR EXCLUDED.critical_active,occurrences=adminops_inspection_issues.occurrences+1,version=adminops_inspection_issues.version+1 RETURNING id`, fingerprint, r.ID, r.Code, r.Status, at, r.Status == "critical").Scan(&id)
	if e != nil {
		return nil, nil, e
	}
	if e = observeIncidentEpisode(ctx, tx, id, r, at); e != nil {
		return nil, nil, e
	}
	if newlyCritical {
		return &inspectionTransition{ID: id, CheckID: r.ID, Code: r.Code}, nil, nil
	}
	return nil, nil, nil
}

func transitionMessage(kind string, runID int64, at time.Time, changes []inspectionTransition, detailURL string) ([]byte, error) {
	title := "CRM 严重异常合并通知"
	if kind == "recovery" {
		title = "CRM 严重异常恢复通知"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n巡查 #%d · %s\n变化检查项：%d\n", title, runID, at.Format(time.RFC3339), len(changes))
	for i, c := range changes {
		if i >= 30 {
			b.WriteString("其余变化见治理看板。\n")
			break
		}
		fmt.Fprintf(&b, "问题 #%d · %s · %s\n", c.ID, c.CheckID, c.Code)
	}
	if kind == "recovery" {
		b.WriteString("上述检查已由新的成功采集确认恢复；其他检查状态请查详情。\n")
	}
	if detailURL != "" {
		b.WriteString("详情：" + detailURL + "\n")
	}
	content, e := json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": b.String()}})
	if e != nil {
		return nil, e
	}
	if len(content) > 24000 {
		return nil, ErrInspectionInvalid
	}
	return content, nil
}
