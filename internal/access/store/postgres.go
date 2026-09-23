package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

var _ accessport.Repository = (*PostgreSQL)(nil)
var _ accessport.MessageArchiveStaffDirectory = (*PostgreSQL)(nil)

func NewPostgreSQL() *PostgreSQL { return &PostgreSQL{} }

func tx(ctx context.Context) (pgx.Tx, error) { return platformpostgres.RequireTransaction(ctx) }

func (*PostgreSQL) CountUsers(ctx context.Context) (int64, error) {
	database, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = database.QueryRow(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count)
	return count, err
}

func (store *PostgreSQL) UserByID(ctx context.Context, id int64, lock bool) (domain.User, error) {
	return store.user(ctx, `WHERE u.id = $1`, id, lock)
}

func (store *PostgreSQL) UserByUsername(ctx context.Context, username string, lock bool) (domain.User, error) {
	return store.user(ctx, `WHERE u.username = $1`, username, lock)
}

func (store *PostgreSQL) UserByWeComUserID(ctx context.Context, wecomUserID string, lock bool) (domain.User, error) {
	return store.user(ctx, `WHERE u.wecom_userid = $1`, wecomUserID, lock)
}

func (*PostgreSQL) UsersByWeComUserIDs(ctx context.Context, values []string) ([]domain.User, error) {
	if len(values) == 0 {
		return []domain.User{}, nil
	}
	if len(values) > 50 {
		return nil, domain.ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, domain.ErrInvalidInput
		}
		if _, exists := seen[value]; exists {
			return nil, domain.ErrInvalidInput
		}
		seen[value] = struct{}{}
	}
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `
		SELECT u.id, u.username, u.password_hash, u.display_name,
			COALESCE(u.wecom_userid, ''), u.is_active, u.login_enabled, u.legacy_login_reactivation_pending, u.access_granted_at, u.session_version,
			u.last_login_at, u.created_at, u.updated_at, r.role_code
		FROM admin_users u
		JOIN admin_user_roles r ON r.admin_user_id=u.id
		WHERE u.wecom_userid = ANY($1::text[])
		ORDER BY u.id, r.role_code`, values)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]domain.User, 0, len(values))
	byID := make(map[int64]int, len(values))
	for rows.Next() {
		var user domain.User
		var role domain.Role
		if err = rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
			&user.WeComUserID, &user.Active, &user.LoginEnabled, &user.LegacyLoginReactivationPending, &user.AccessGrantedAt, &user.SessionVersion, &user.LastLoginAt,
			&user.CreatedAt, &user.UpdatedAt, &role); err != nil {
			return nil, err
		}
		if index, exists := byID[user.ID]; exists {
			users[index].Roles = append(users[index].Roles, role)
			continue
		}
		user.Roles = []domain.Role{role}
		byID[user.ID] = len(users)
		users = append(users, user)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}

func (*PostgreSQL) ListUsers(ctx context.Context) ([]domain.User, error) {
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `SELECT id, username, password_hash, display_name,
		COALESCE(wecom_userid, ''), is_active, login_enabled, legacy_login_reactivation_pending, access_granted_at, session_version, last_login_at,
		created_at, updated_at FROM admin_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, 0)
	for rows.Next() {
		var user domain.User
		if err = rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
			&user.WeComUserID, &user.Active, &user.LoginEnabled, &user.LegacyLoginReactivationPending, &user.AccessGrantedAt, &user.SessionVersion, &user.LastLoginAt,
			&user.CreatedAt, &user.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		users = append(users, user)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range users {
		users[index].Roles, err = rolesForUser(ctx, database, users[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (*PostgreSQL) MessageArchiveStaff(ctx context.Context, ids []int64) ([]accessport.MessageArchiveStaff, error) {
	if len(ids) == 0 {
		return []accessport.MessageArchiveStaff{}, nil
	}
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, domain.ErrInvalidInput
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) > 1000 {
		return nil, domain.ErrInvalidInput
	}
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `SELECT id,display_name FROM admin_users WHERE id=ANY($1::bigint[]) ORDER BY display_name,id`, unique)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accessport.MessageArchiveStaff, 0, len(unique))
	for rows.Next() {
		var item accessport.MessageArchiveStaff
		if err = rows.Scan(&item.ID, &item.DisplayName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (*PostgreSQL) user(ctx context.Context, predicate string, argument any, lock bool) (domain.User, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.User{}, err
	}
	query := `SELECT u.id, u.username, u.password_hash, u.display_name,
		COALESCE(u.wecom_userid, ''), u.is_active, u.login_enabled, u.legacy_login_reactivation_pending, u.access_granted_at, u.session_version,
		u.last_login_at, u.created_at, u.updated_at FROM admin_users u ` + predicate
	if lock {
		query += ` FOR UPDATE OF u`
	}
	var user domain.User
	err = database.QueryRow(ctx, query, argument).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&user.WeComUserID, &user.Active, &user.LoginEnabled, &user.LegacyLoginReactivationPending, &user.AccessGrantedAt, &user.SessionVersion,
		&user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	user.Roles, err = rolesForUser(ctx, database, user.ID)
	return user, err
}

func (*PostgreSQL) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.User{}, err
	}
	err = database.QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, display_name, wecom_userid, is_active, login_enabled, legacy_login_reactivation_pending, access_granted_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $5, FALSE, CURRENT_TIMESTAMP)
		RETURNING id, login_enabled, access_granted_at, session_version, created_at, updated_at`,
		user.Username, user.PasswordHash, user.DisplayName, user.WeComUserID, user.Active,
	).Scan(&user.ID, &user.LoginEnabled, &user.AccessGrantedAt, &user.SessionVersion, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.User{}, mapDatabaseError(err)
	}
	for _, role := range user.Roles {
		if _, err = database.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) VALUES ($1, $2)`, user.ID, role); err != nil {
			return domain.User{}, mapDatabaseError(err)
		}
	}
	return user, nil
}

func (*PostgreSQL) CreateStaffProjection(ctx context.Context, user domain.User) (domain.User, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.User{}, err
	}
	if !user.Active || user.WeComUserID == "" || len(user.Roles) != 1 || user.Roles[0] != domain.RoleViewer {
		return domain.User{}, domain.ErrInvalidInput
	}
	err = database.QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, display_name, wecom_userid, is_active, login_enabled, legacy_login_reactivation_pending, access_granted_at)
		VALUES ($1, $2, $3, $4, TRUE, FALSE, FALSE, NULL)
		RETURNING id, login_enabled, access_granted_at, session_version, created_at, updated_at`,
		user.Username, user.PasswordHash, user.DisplayName, user.WeComUserID,
	).Scan(&user.ID, &user.LoginEnabled, &user.AccessGrantedAt, &user.SessionVersion, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.User{}, mapDatabaseError(err)
	}
	if _, err = database.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) VALUES ($1, $2)`, user.ID, domain.RoleViewer); err != nil {
		return domain.User{}, mapDatabaseError(err)
	}
	return user, nil
}

func (store *PostgreSQL) BootstrapUser(ctx context.Context, user domain.User) (domain.User, bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.User{}, false, err
	}
	if _, err = database.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('aicrm-access-bootstrap'))`); err != nil {
		return domain.User{}, false, err
	}
	var count int64
	if err = database.QueryRow(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return domain.User{}, false, err
	}
	if count > 0 {
		existing, lookupErr := store.UserByUsername(ctx, user.Username, true)
		if lookupErr == nil {
			return existing, false, nil
		}
		if errors.Is(lookupErr, domain.ErrNotFound) {
			return domain.User{}, false, domain.ErrConflict
		}
		return domain.User{}, false, lookupErr
	}
	created, err := store.CreateUser(ctx, user)
	return created, err == nil, err
}

func (*PostgreSQL) SetLoginEnabled(ctx context.Context, id int64, enabled bool, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `UPDATE admin_users SET login_enabled=$2,
		is_active=CASE WHEN $2 AND legacy_login_reactivation_pending THEN TRUE ELSE is_active END,
		legacy_login_reactivation_pending=FALSE, session_version=session_version+1, updated_at=$3
		WHERE id=$1 AND access_granted_at IS NOT NULL AND login_enabled IS DISTINCT FROM $2`, id, enabled, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err = database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return domain.ErrNotFound
		}
	}
	return nil
}

func (*PostgreSQL) GrantAccess(ctx context.Context, id int64, role domain.Role, displayName string, now time.Time) error {
	if (role != domain.RoleAdmin && role != domain.RoleViewer) || strings.TrimSpace(displayName) == "" {
		return domain.ErrInvalidInput
	}
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	var active bool
	var grantedAt *time.Time
	if err = database.QueryRow(ctx, `SELECT is_active, access_granted_at FROM admin_users WHERE id=$1 FOR UPDATE`, id).Scan(&active, &grantedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if !active || grantedAt != nil {
		return domain.ErrConflict
	}
	if _, err = database.Exec(ctx, `DELETE FROM admin_user_roles WHERE admin_user_id=$1`, id); err != nil {
		return err
	}
	if _, err = database.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) VALUES ($1,$2)`, id, role); err != nil {
		return mapDatabaseError(err)
	}
	_, err = database.Exec(ctx, `UPDATE admin_users SET display_name=$2, login_enabled=TRUE, access_granted_at=$3,
		session_version=session_version+1, updated_at=$3 WHERE id=$1`, id, strings.TrimSpace(displayName), now)
	return err
}

func (*PostgreSQL) ReserveLoginAccessRequest(ctx context.Context, actorID int64, key string, payloadDigest [32]byte, now time.Time) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var storedDigest []byte
	err = database.QueryRow(ctx, `
		INSERT INTO admin_access_login_compat_receipts
			(actor_admin_user_id, idempotency_key, payload_digest, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (actor_admin_user_id, idempotency_key) DO NOTHING
		RETURNING payload_digest`, actorID, key, payloadDigest[:], now).Scan(&storedDigest)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err = database.QueryRow(ctx, `
		SELECT payload_digest FROM admin_access_login_compat_receipts
		WHERE actor_admin_user_id=$1 AND idempotency_key=$2
		FOR UPDATE`, actorID, key).Scan(&storedDigest); err != nil {
		return false, err
	}
	if len(storedDigest) != len(payloadDigest) || string(storedDigest) != string(payloadDigest[:]) {
		return false, domain.ErrConflict
	}
	return false, nil
}

func (*PostgreSQL) SetWeComUserID(ctx context.Context, id int64, wecomUserID string, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `UPDATE admin_users SET wecom_userid=NULLIF($2,''),
		session_version=session_version+1, updated_at=$3
		WHERE id=$1 AND wecom_userid IS DISTINCT FROM NULLIF($2,'')`, id, wecomUserID, now)
	if err != nil {
		return mapDatabaseError(err)
	}
	if tag.RowsAffected() == 0 {
		return ensureUserExists(ctx, database, id)
	}
	return nil
}

func (*PostgreSQL) ReplaceRoles(ctx context.Context, id int64, roles []domain.Role, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	if err = ensureUserExists(ctx, database, id); err != nil {
		return err
	}
	if _, err = database.Exec(ctx, `DELETE FROM admin_user_roles WHERE admin_user_id=$1`, id); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err = database.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) VALUES ($1,$2)`, id, role); err != nil {
			return mapDatabaseError(err)
		}
	}
	_, err = database.Exec(ctx, `UPDATE admin_users SET session_version=session_version+1, updated_at=$2 WHERE id=$1`, id, now)
	return err
}

func (*PostgreSQL) SetPasswordHash(ctx context.Context, id int64, passwordHash string, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `UPDATE admin_users SET password_hash=$2,
		session_version=session_version+1, updated_at=$3 WHERE id=$1`, id, passwordHash, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (*PostgreSQL) SetLastLogin(ctx context.Context, id int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `UPDATE admin_users SET last_login_at=$2, updated_at=$2 WHERE id=$1`, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (*PostgreSQL) CreateSession(ctx context.Context, session domain.Session) (domain.Session, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	err = database.QueryRow(ctx, `INSERT INTO admin_sessions
		(token_digest, csrf_token_digest, admin_user_id, session_version, expires_at)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at, last_seen_at`,
		session.TokenDigest[:], session.CSRFTokenDigest[:], session.AdminUserID, session.SessionVersion, session.ExpiresAt,
	).Scan(&session.ID, &session.CreatedAt, &session.LastSeenAt)
	return session, err
}

func (*PostgreSQL) SessionByTokenDigest(ctx context.Context, digest [32]byte, lock bool) (domain.Session, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	query := `SELECT s.id, s.token_digest, s.csrf_token_digest, s.admin_user_id,
		s.session_version, s.expires_at, s.revoked_at, s.revoked_reason,
		s.created_at, s.last_seen_at, u.id, u.username, u.password_hash,
		u.display_name, COALESCE(u.wecom_userid,''), u.is_active, u.login_enabled, u.legacy_login_reactivation_pending, u.access_granted_at,
		u.session_version, u.last_login_at, u.created_at, u.updated_at
		FROM admin_sessions s JOIN admin_users u ON u.id=s.admin_user_id
		WHERE s.token_digest=$1`
	if lock {
		query += ` FOR UPDATE OF s`
	}
	var session domain.Session
	var tokenDigest, csrfDigest []byte
	err = database.QueryRow(ctx, query, digest[:]).Scan(
		&session.ID, &tokenDigest, &csrfDigest, &session.AdminUserID,
		&session.SessionVersion, &session.ExpiresAt, &session.RevokedAt, &session.RevokedReason,
		&session.CreatedAt, &session.LastSeenAt, &session.User.ID, &session.User.Username,
		&session.User.PasswordHash, &session.User.DisplayName, &session.User.WeComUserID,
		&session.User.Active, &session.User.LoginEnabled, &session.User.LegacyLoginReactivationPending, &session.User.AccessGrantedAt, &session.User.SessionVersion, &session.User.LastLoginAt,
		&session.User.CreatedAt, &session.User.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	if len(tokenDigest) != 32 || len(csrfDigest) != 32 {
		return domain.Session{}, errors.New("invalid session digest in database")
	}
	copy(session.TokenDigest[:], tokenDigest)
	copy(session.CSRFTokenDigest[:], csrfDigest)
	session.User.Roles, err = rolesForUser(ctx, database, session.User.ID)
	return session, err
}

func (*PostgreSQL) TouchSession(ctx context.Context, id int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `UPDATE admin_sessions SET last_seen_at=$2 WHERE id=$1`, id, now)
	return err
}

func (*PostgreSQL) RevokeSession(ctx context.Context, digest [32]byte, reason string, now time.Time) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	tag, err := database.Exec(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,$2),
		revoked_reason=CASE WHEN revoked_reason='' THEN $3 ELSE revoked_reason END WHERE token_digest=$1`, digest[:], now, reason)
	return tag.RowsAffected() > 0, err
}

func (*PostgreSQL) LoginRateLimit(ctx context.Context, digest [32]byte, lock bool) (domain.LoginRateLimit, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.LoginRateLimit{}, err
	}
	if lock {
		if _, err = database.Exec(ctx, `INSERT INTO admin_login_rate_limits
			(key_digest, window_started_at, updated_at) VALUES ($1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT (key_digest) DO NOTHING`, digest[:]); err != nil {
			return domain.LoginRateLimit{}, err
		}
	}
	query := `SELECT key_digest, window_started_at, failure_count, blocked_until, updated_at
		FROM admin_login_rate_limits WHERE key_digest=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var result domain.LoginRateLimit
	var key []byte
	err = database.QueryRow(ctx, query, digest[:]).Scan(&key, &result.WindowStartedAt, &result.FailureCount, &result.BlockedUntil, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LoginRateLimit{}, domain.ErrNotFound
	}
	if err == nil && len(key) == 32 {
		copy(result.KeyDigest[:], key)
	}
	return result, err
}

func (*PostgreSQL) SaveLoginRateLimit(ctx context.Context, limit domain.LoginRateLimit) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `INSERT INTO admin_login_rate_limits
		(key_digest, window_started_at, failure_count, blocked_until, updated_at)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (key_digest) DO UPDATE SET
		window_started_at=EXCLUDED.window_started_at, failure_count=EXCLUDED.failure_count,
		blocked_until=EXCLUDED.blocked_until, updated_at=EXCLUDED.updated_at`,
		limit.KeyDigest[:], limit.WindowStartedAt, limit.FailureCount, limit.BlockedUntil, limit.UpdatedAt)
	return err
}

func (*PostgreSQL) AppendLoginAudit(ctx context.Context, audit domain.LoginAudit) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `INSERT INTO admin_login_audit
		(admin_user_id, identifier_digest, remote_digest, outcome, reason, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, audit.AdminUserID, audit.IdentifierDigest[:], audit.RemoteDigest[:], audit.Outcome, audit.Reason, audit.CreatedAt)
	return err
}

func (*PostgreSQL) AppendAccessAudit(ctx context.Context, audit domain.AccessAudit) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `INSERT INTO admin_access_audit
		(actor_admin_user_id, target_admin_user_id, action, details, created_at)
		VALUES ($1,$2,$3,$4,$5)`, audit.ActorAdminUserID, audit.TargetAdminUserID,
		audit.Action, []byte(audit.Details), audit.CreatedAt)
	return err
}

func rolesForUser(ctx context.Context, database pgx.Tx, id int64) ([]domain.Role, error) {
	rows, err := database.Query(ctx, `SELECT role_code FROM admin_user_roles WHERE admin_user_id=$1 ORDER BY role_code`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]domain.Role, 0, 3)
	for rows.Next() {
		var role domain.Role
		if err = rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func ensureUserExists(ctx context.Context, database pgx.Tx, id int64) error {
	var exists bool
	if err := database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

func mapDatabaseError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return fmt.Errorf("%w: %s", domain.ErrConflict, databaseError.ConstraintName)
	}
	return err
}

func (*PostgreSQL) SuperAdminControl(ctx context.Context, lock bool) (domain.SuperAdminControl, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.SuperAdminControl{}, err
	}
	query := `SELECT admin_user_id, version, updated_at FROM access_super_admin_control WHERE singleton = TRUE`
	if lock {
		query += ` FOR UPDATE`
	}
	var control domain.SuperAdminControl
	if err = database.QueryRow(ctx, query).Scan(&control.AdminUserID, &control.Version, &control.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SuperAdminControl{}, domain.ErrNotFound
		}
		return domain.SuperAdminControl{}, err
	}
	return control, nil
}

func (*PostgreSQL) InitializeSuperAdminControl(ctx context.Context, adminUserID int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	command, err := database.Exec(ctx, `INSERT INTO access_super_admin_control (singleton, admin_user_id, version, updated_at)
		VALUES (TRUE, $1, 1, $2) ON CONFLICT (singleton) DO NOTHING`, adminUserID, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (*PostgreSQL) SetSuperAdminControl(ctx context.Context, adminUserID int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	command, err := database.Exec(ctx, `UPDATE access_super_admin_control
		SET admin_user_id = $1, version = version + 1, updated_at = $2 WHERE singleton = TRUE`, adminUserID, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (*PostgreSQL) ReserveGovernanceMutation(ctx context.Context, actorID int64, key, action string, targetID int64, digest [32]byte, now time.Time) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	command, err := database.Exec(ctx, `INSERT INTO admin_access_governance_receipts
		(actor_admin_user_id, idempotency_key, action, target_admin_user_id, payload_digest, created_at)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (actor_admin_user_id, idempotency_key) DO NOTHING`, actorID, key, action, targetID, digest[:], now)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 1 {
		return true, nil
	}
	var storedAction string
	var storedTarget int64
	var storedDigest []byte
	if err = database.QueryRow(ctx, `SELECT action, target_admin_user_id, payload_digest
		FROM admin_access_governance_receipts WHERE actor_admin_user_id=$1 AND idempotency_key=$2`, actorID, key).Scan(&storedAction, &storedTarget, &storedDigest); err != nil {
		return false, err
	}
	if storedAction != action || storedTarget != targetID || len(storedDigest) != len(digest) || string(storedDigest) != string(digest[:]) {
		return false, domain.ErrConflict
	}
	return false, nil
}

// SetStaffDisplayName compares the binding so a concurrent rebind cannot apply
// one employee's nickname to another. No login/session/role fields change.
func (*PostgreSQL) SetStaffDisplayName(ctx context.Context, id int64, providerID, name string, now time.Time) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 160 || strings.ContainsAny(name, "\x00\r\n") {
		return domain.ErrInvalidInput
	}
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	result, err := database.Exec(ctx, `UPDATE admin_users SET display_name=$3,updated_at=$4 WHERE id=$1 AND wecom_userid=$2`, id, providerID, name, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
