// Command migrate-access-role-convergence performs the one-time, pre-0151
// reconciliation for the approved Access owner transition. It is deliberately
// a release command: runtime packages neither import it nor can invoke it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const convergenceID = "access-role-convergence-v1"

type account struct {
	ID             int64
	Username       string
	WeComUserID    string
	Active         bool
	SessionVersion int64
	Roles          []string
	TargetRole     string
}

type report struct {
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	Accounts        int    `json:"accounts"`
	ChangedAccounts int    `json:"changed_accounts"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("access role convergence failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "dry-run", "dry-run, apply, or replay-check")
	timeout := flag.Duration("timeout", 90*time.Second, "overall command timeout")
	flag.Parse()
	if *timeout <= 0 || (*mode != "dry-run" && *mode != "apply" && *mode != "replay-check") {
		return errors.New("invalid convergence mode or timeout")
	}
	if err := requireApplyApproval(*mode); err != nil {
		return err
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 2})
	if err != nil {
		return err
	}
	defer pool.Close()

	var result report
	switch *mode {
	case "dry-run":
		readTx, beginErr := pool.Native().BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if beginErr != nil {
			return beginErr
		}
		defer readTx.Rollback(ctx)
		accounts, err := loadAndValidate(ctx, readTx)
		if err != nil {
			return err
		}
		result = report{Mode: *mode, Status: "ready", Accounts: len(accounts), ChangedAccounts: changedCount(accounts)}
	case "apply":
		result, err = apply(ctx, pool.Native())
		if err != nil {
			return err
		}
	case "replay-check":
		readTx, beginErr := pool.Native().BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if beginErr != nil {
			return beginErr
		}
		defer readTx.Rollback(ctx)
		result, err = replayCheck(ctx, readTx)
		if err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
	return nil
}

func requireApplyApproval(mode string) error {
	if mode == "apply" && !platformconfig.AccessRoleConvergenceApproved() {
		return errors.New("apply requires AICRM_ACCESS_CONVERGENCE_APPROVED=1")
	}
	return nil
}

func loadAndValidate(ctx context.Context, db pgx.Tx) ([]account, error) {
	if err := assertPre0151(ctx, db); err != nil {
		return nil, err
	}
	accounts, err := loadAccounts(ctx, db)
	if err != nil {
		return nil, err
	}
	if err = validateAccounts(accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

func apply(ctx context.Context, pool *pgxpool.Pool) (report, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return report{}, err
	}
	defer tx.Rollback(ctx) // no-op after a successful commit
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('aicrm_access_role_convergence_v1'))"); err != nil {
		return report{}, err
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE admin_users, admin_user_roles, admin_sessions, admin_access_audit IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return report{}, err
	}
	accounts, err := loadAndValidate(ctx, tx)
	if err != nil {
		return report{}, err
	}
	var qianlan account
	for _, item := range accounts {
		if item.Username == "qianlan" {
			qianlan = item
			break
		}
	}
	for _, item := range accounts {
		if item.TargetRole == singleRole(item.Roles) {
			continue
		}
		before, _ := json.Marshal(item.Roles)
		if _, err = tx.Exec(ctx, "DELETE FROM admin_user_roles WHERE admin_user_id=$1", item.ID); err != nil {
			return report{}, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES($1,$2)", item.ID, item.TargetRole); err != nil {
			return report{}, err
		}
		if _, err = tx.Exec(ctx, "UPDATE admin_users SET session_version=session_version+1, updated_at=clock_timestamp() WHERE id=$1", item.ID); err != nil {
			return report{}, err
		}
		if _, err = tx.Exec(ctx, "UPDATE admin_sessions SET revoked_at=clock_timestamp(), revoked_reason='access_role_convergence' WHERE admin_user_id=$1 AND revoked_at IS NULL", item.ID); err != nil {
			return report{}, err
		}
		details, _ := json.Marshal(map[string]any{"convergence_id": convergenceID, "record_kind": "role_change", "executor": "controlled_release", "authorization": "user_approved_plan", "actor_semantics": "approved_owner", "before_roles": json.RawMessage(before), "after_role": item.TargetRole, "session_fenced": true})
		if _, err = tx.Exec(ctx, "INSERT INTO admin_access_audit(actor_admin_user_id,target_admin_user_id,action,details) VALUES($1,$2,'change_roles',$3::jsonb)", qianlan.ID, item.ID, details); err != nil {
			return report{}, err
		}
	}
	sentinel, _ := json.Marshal(map[string]any{"convergence_id": convergenceID, "record_kind": "completion", "executor": "controlled_release", "authorization": "user_approved_plan", "actor_semantics": "approved_owner", "changed_accounts": changedCount(accounts), "approved_super_admin": "qianlan"})
	if _, err = tx.Exec(ctx, "INSERT INTO admin_access_audit(actor_admin_user_id,target_admin_user_id,action,details) VALUES($1,$1,'change_roles',$2::jsonb)", qianlan.ID, sentinel); err != nil {
		return report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return report{}, err
	}
	return report{Mode: "apply", Status: "converged", Accounts: len(accounts), ChangedAccounts: changedCount(accounts)}, nil
}

func replayCheck(ctx context.Context, db pgx.Tx) (report, error) {
	var applied int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM platform_schema_migrations WHERE version IN ('0151','0152')").Scan(&applied); err != nil || applied != 2 {
		return report{}, errors.New("0151 and 0152 must both be applied before replay-check")
	}
	var sentinels int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM admin_access_audit WHERE details @> $1::jsonb", `{"convergence_id":"access-role-convergence-v1","record_kind":"completion"}`).Scan(&sentinels); err != nil || sentinels != 1 {
		return report{}, errors.New("convergence receipt is missing or ambiguous")
	}
	accounts, err := loadAccounts(ctx, db)
	if err != nil {
		return report{}, err
	}
	for _, item := range accounts {
		if len(item.Roles) != 1 {
			return report{}, errors.New("post-convergence role cardinality is invalid")
		}
	}
	var qianlan, admin account
	for _, item := range accounts {
		switch item.Username {
		case "qianlan":
			qianlan = item
		case "admin":
			admin = item
		}
	}
	if qianlan.ID == 0 || qianlan.WeComUserID != "QianLan" || !qianlan.Active || singleRole(qianlan.Roles) != "super_admin" || admin.ID == 0 || !admin.Active || singleRole(admin.Roles) != "admin" {
		return report{}, errors.New("post-convergence authority state is invalid")
	}
	var controlID int64
	var loginEnabled bool
	var grantedAt *time.Time
	if err := db.QueryRow(ctx, `SELECT c.admin_user_id,u.login_enabled,u.access_granted_at FROM access_super_admin_control c JOIN admin_users u ON u.id=c.admin_user_id`).Scan(&controlID, &loginEnabled, &grantedAt); err != nil || controlID != qianlan.ID || !loginEnabled || grantedAt == nil {
		return report{}, errors.New("post-convergence super-admin control or login grant is invalid")
	}
	return report{Mode: "replay-check", Status: "verified", Accounts: len(accounts)}, nil
}

func assertPre0151(ctx context.Context, db pgx.Tx) error {
	var applied int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM platform_schema_migrations WHERE version IN ('0151','0152')").Scan(&applied); err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	if applied != 0 {
		return errors.New("0151 or 0152 already applied; use replay-check")
	}
	var controls, receipts bool
	if err := db.QueryRow(ctx, "SELECT to_regclass('access_super_admin_control') IS NOT NULL, to_regclass('admin_access_governance_receipts') IS NOT NULL").Scan(&controls, &receipts); err != nil {
		return err
	}
	if controls || receipts {
		return errors.New("post-0151 access schema already exists")
	}
	var existing int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM admin_access_audit WHERE details @> $1::jsonb", `{"convergence_id":"access-role-convergence-v1","record_kind":"completion"}`).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return errors.New("convergence receipt already exists; use replay-check")
	}
	return nil
}

func loadAccounts(ctx context.Context, db pgx.Tx) ([]account, error) {
	rows, err := db.Query(ctx, `SELECT u.id,u.username,COALESCE(u.wecom_userid,''),u.is_active,u.session_version,COALESCE(array_agg(r.role_code ORDER BY r.role_code) FILTER (WHERE r.role_code IS NOT NULL),ARRAY[]::text[])
		FROM admin_users u LEFT JOIN admin_user_roles r ON r.admin_user_id=u.id GROUP BY u.id ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []account
	for rows.Next() {
		var item account
		if err = rows.Scan(&item.ID, &item.Username, &item.WeComUserID, &item.Active, &item.SessionVersion, &item.Roles); err != nil {
			return nil, err
		}
		item.TargetRole = highestRole(item.Roles)
		if item.Username == "qianlan" {
			item.TargetRole = "super_admin"
		}
		if item.Username == "admin" {
			item.TargetRole = "admin"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validateAccounts(items []account) error {
	if len(items) == 0 {
		return errors.New("initialized Access database has no accounts")
	}
	var supers []string
	var qianlan, admin *account
	for i := range items {
		item := &items[i]
		if len(item.Roles) == 0 || item.TargetRole == "" {
			return errors.New("historical account has no valid role")
		}
		for _, role := range item.Roles {
			if role == "super_admin" {
				supers = append(supers, item.Username)
			}
		}
		switch item.Username {
		case "qianlan":
			qianlan = item
		case "admin":
			admin = item
		}
	}
	sort.Strings(supers)
	if strings.Join(supers, ",") != "admin,qianlan" {
		return errors.New("expected legacy super-admin set is exactly admin and qianlan")
	}
	if qianlan == nil || !qianlan.Active || qianlan.WeComUserID != "QianLan" || admin == nil || !admin.Active {
		return errors.New("approved qianlan/admin identity precondition failed")
	}
	return nil
}

func highestRole(roles []string) string {
	for _, role := range []string{"super_admin", "admin", "viewer"} {
		for _, current := range roles {
			if current == role {
				return role
			}
		}
	}
	return ""
}
func singleRole(roles []string) string {
	if len(roles) == 1 {
		return roles[0]
	}
	return ""
}
func changedCount(items []account) int {
	n := 0
	for _, item := range items {
		if singleRole(item.Roles) != item.TargetRole {
			n++
		}
	}
	return n
}
