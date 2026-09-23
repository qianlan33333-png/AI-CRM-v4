package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrNotFound = errors.New("hxc dashboard not found")
var ErrActiveRefresh = errors.New("hxc dashboard refresh already active")

type PostgreSQL struct{ pool *pgxpool.Pool }

func NewPostgreSQL(pool *pgxpool.Pool) *PostgreSQL { return &PostgreSQL{pool: pool} }

func (store *PostgreSQL) CreateRun(ctx context.Context, runKey string, requestDigest [32]byte, trigger, identityMode string, requestedBy int64) (domain.RefreshRun, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return domain.RefreshRun{}, false, err
	}
	var run domain.RefreshRun
	err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,identity_mode,status,requested_by) VALUES($1,$2,$3,$4,'queued',NULLIF($5,0)) ON CONFLICT(run_key) DO NOTHING RETURNING id,trigger,identity_mode,status,projection_id,source_count,processed_count,identity_replay_verified_count,COALESCE(error_code,''),version,COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at`, runKey, requestDigest[:], trigger, identityMode, requestedBy).Scan(&run.ID, &run.Trigger, &run.IdentityMode, &run.Status, &run.ProjectionID, &run.SourceCount, &run.ProcessedCount, &run.ReplayVerified, &run.ErrorCode, &run.Version, &run.RequestedBy, &run.StartedAt, &run.CompletedAt, &run.CreatedAt, &run.UpdatedAt)
	if err == nil {
		return run, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		if isUniqueViolation(err) {
			return domain.RefreshRun{}, false, ErrActiveRefresh
		}
		return domain.RefreshRun{}, false, fmt.Errorf("create HXC refresh: %w", err)
	}
	run, err = store.GetRun(ctxByTx(ctx), 0, runKey)
	return run, true, err
}

func ctxByTx(ctx context.Context) context.Context { return ctx }

func (store *PostgreSQL) GetRun(ctx context.Context, id int64, runKey string) (domain.RefreshRun, error) {
	queryer := queryer(store.pool)
	if tx, err := platformpostgres.RequireTransaction(ctx); err == nil {
		queryer = tx
	}
	query := `SELECT id,trigger,identity_mode,status,projection_id,source_count,processed_count,identity_replay_verified_count,COALESCE(error_code,''),version,COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at FROM hxc_dashboard_refresh_runs WHERE `
	var arg any
	if id > 0 {
		query += "id=$1"
		arg = id
	} else {
		query += "run_key=$1"
		arg = runKey
	}
	var run domain.RefreshRun
	err := queryer.QueryRow(ctx, query, arg).Scan(&run.ID, &run.Trigger, &run.IdentityMode, &run.Status, &run.ProjectionID, &run.SourceCount, &run.ProcessedCount, &run.ReplayVerified, &run.ErrorCode, &run.Version, &run.RequestedBy, &run.StartedAt, &run.CompletedAt, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshRun{}, ErrNotFound
	}
	if err != nil {
		return domain.RefreshRun{}, fmt.Errorf("get HXC refresh: %w", err)
	}
	return run, nil
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryer(pool *pgxpool.Pool) rowQueryer { return pool }

func (store *PostgreSQL) MarkRunning(ctx context.Context, id int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='running',started_at=COALESCE(started_at,now()),updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','running','publishing')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (store *PostgreSQL) MarkPublishing(ctx context.Context, id int64, count, replayVerified int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='publishing',source_count=$2,processed_count=$2,identity_replay_verified_count=$3,updated_at=now(),version=version+1 WHERE id=$1 AND status='running'`, id, count, replayVerified)
	return err
}
func (store *PostgreSQL) Fail(ctx context.Context, id int64, code string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='failed',error_code=$2,completed_at=now(),updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','running','publishing')`, id, code)
	return err
}

func (store *PostgreSQL) Publish(ctx context.Context, runID int64, projection domain.Projection) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE hxc_dashboard_versions SET status='superseded' WHERE status='published'`); err != nil {
		return 0, err
	}
	var id int64
	c := projection.Counts
	err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_versions(rule_version,status,projection_as_of,source_watermark,source_digest,projection_digest,total_count,active_used_count,active_unused_count,registered_no_active_membership_count,matched_count,unmatched_count,conflict_count,matched_by_unionid_count,matched_by_phone_count,matched_by_both_count,pending_observation_count,invalid_identity_count,shared_facts_available) VALUES($1,'published',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id`, domain.RuleVersion, projection.AsOf, projection.Watermark, projection.SourceDigest[:], projection.ProjectionDigest[:], c.Total, c.ActiveUsed, c.ActiveUnused, c.RegisteredNoActiveMembership, c.Matched, c.Unmatched, c.Conflict, c.MatchedByUnionID, c.MatchedByPhone, c.MatchedByBoth, c.PendingObservation, c.InvalidIdentity, projection.SharedFactsAvailable).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert projection: %w", err)
	}
	for customer, state := range projection.RegistrationCoverage {
		if _, err = tx.Exec(ctx, `INSERT INTO hxc_registration_coverage(projection_id,customer_id,state) VALUES($1,$2,$3)`, id, customer, state); err != nil {
			return 0, err
		}
	}
	rows := make([][]any, 0, len(projection.Rows))
	for _, r := range projection.Rows {
		var customer any
		if r.CustomerID > 0 {
			customer = int64(r.CustomerID)
		}
		rows = append(rows, []any{id, r.SubjectDigest[:], r.UserRef, r.Stage, r.SubscriptionTier, r.SubscriptionExpiresAt, r.MonthlyChatQuota, r.CurrentPeriodUsed, r.ConsultationLimit, r.ConsultationUsed, r.MembershipAttribution, r.Sessions7D, r.Sessions30D, r.SessionsTotal, r.UserMessages7D, r.UserMessages30D, r.UserMessagesTotal, r.CapabilityUsage, r.LastUsedAt, nullString(r.LastCapability), nullString(r.BusinessStage), nullString(r.MainLineType), nullString(r.UserSegment), r.FocusTopics, nullString(r.PainTag), customer, r.IdentityState, r.MatchedBy, r.IdentityReasonCode, nullableInt64(r.IdentityCaseID), nullableInt64(r.MergeCandidateID), r.FormallyLoggedIn, r.FormalLoginAt, r.HasTokenUsage, r.LearningPlanFound, nullString(r.LearningPlanStatus), r.LearningPlanCurrent, r.LearningPlanTotal, r.CardOpenCount7D, r.CardLastOpenedAt, r.MembershipRecordFound, r.IsMember, nullString(r.MembershipSource), nullString(r.MembershipStatus), r.MembershipExpiresAt, r.SourceUpdatedAt})
	}
	columns := []string{"projection_id", "subject_digest", "user_ref", "stage", "subscription_tier", "subscription_expires_at", "monthly_chat_quota", "current_period_used", "consultation_limit", "consultation_used", "membership_attribution", "sessions_7d", "sessions_30d", "sessions_total", "user_messages_7d", "user_messages_30d", "user_messages_total", "capability_usage", "last_used_at", "last_capability", "business_stage", "main_line_type", "user_segment", "focus_topics", "pain_tag", "customer_id", "identity_state", "matched_by", "identity_reason_code", "identity_case_id", "merge_candidate_id", "formally_logged_in", "formal_login_at", "has_token_usage", "learning_plan_found", "learning_plan_status", "learning_plan_current", "learning_plan_total", "card_open_count_7d", "card_last_opened_at", "membership_record_found", "is_member", "membership_source", "membership_status", "membership_expires_at", "source_updated_at"}
	if len(rows) > 0 {
		if n, copyErr := tx.CopyFrom(ctx, pgx.Identifier{"hxc_dashboard_rows"}, columns, pgx.CopyFromRows(rows)); copyErr != nil || n != int64(len(rows)) {
			return 0, fmt.Errorf("copy projection rows: %w", copyErr)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='succeeded',projection_id=$2,source_count=$3,processed_count=$3,error_code=NULL,completed_at=now(),updated_at=now(),version=version+1 WHERE id=$1 AND status='publishing'`, runID, id, len(rows)); err != nil {
		return 0, err
	}
	// Refresh receipts keep an immutable reference to every published version.
	// Retention therefore prunes only the heavy row payload of old superseded
	// versions; deleting the version header would violate that receipt lineage.
	if _, err = tx.Exec(ctx, `DELETE FROM hxc_dashboard_rows WHERE projection_id IN (SELECT id FROM hxc_dashboard_versions WHERE status='superseded' ORDER BY published_at DESC,id DESC OFFSET 7)`); err != nil {
		return 0, err
	}
	return id, nil
}

// CurrentSharedFactsVersion returns the immutable projection currently exposed
// to consumers. Callers that need more than one bounded read keep this ID.
func (store *PostgreSQL) CurrentSharedFactsVersion(ctx context.Context) (int64, error) {
	var id int64
	if err := store.pool.QueryRow(ctx, `SELECT id FROM hxc_dashboard_versions WHERE status='published'`).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("read current HXC shared facts version: %w", err)
	}
	return id, nil
}

// SharedFacts implements a bounded read from the currently published immutable
// generation. Consumers with several batches should pin a version first.
func (store *PostgreSQL) SharedFacts(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]hxcport.SharedFacts, error) {
	ids, err := sharedFactsIDs(customerIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return map[customerdomain.CustomerID]hxcport.SharedFacts{}, nil
	}
	version, err := store.CurrentSharedFactsVersion(ctx)
	if err != nil {
		return nil, err
	}
	return store.SharedFactsAtVersion(ctx, version, customerIDs)
}

// SharedFactsAtVersion reads a retained immutable generation. It deliberately
// accepts a superseded version: a consumer that pinned it can finish its
// bounded batches without changing generations beneath the same evaluation.
func (store *PostgreSQL) SharedFactsAtVersion(ctx context.Context, version int64, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]hxcport.SharedFacts, error) {
	out := make(map[customerdomain.CustomerID]hxcport.SharedFacts)
	if version <= 0 {
		return nil, ErrNotFound
	}
	ids, err := sharedFactsIDs(customerIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin HXC shared facts snapshot: %w", err)
	}
	defer tx.Rollback(ctx)
	var available bool
	var expectedRows, retainedRows int64
	if err := tx.QueryRow(ctx, `SELECT v.shared_facts_available,v.total_count,(SELECT count(*) FROM hxc_dashboard_rows r WHERE r.projection_id=v.id) FROM hxc_dashboard_versions v WHERE v.id=$1`, version).Scan(&available, &expectedRows, &retainedRows); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, hxcport.ErrSharedFactsVersionUnavailable
		}
		return nil, fmt.Errorf("read HXC shared facts version: %w", err)
	}
	if expectedRows != retainedRows {
		return nil, hxcport.ErrSharedFactsVersionUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT r.customer_id,v.projection_as_of,r.source_updated_at,
		r.formally_logged_in,r.formal_login_at,r.has_token_usage,r.learning_plan_found,COALESCE(r.learning_plan_status,''),r.learning_plan_current,r.learning_plan_total,COALESCE(r.card_open_count_7d,0),r.card_last_opened_at,
		r.membership_record_found,r.is_member,COALESCE(r.membership_source,''),COALESCE(r.membership_status,''),r.subscription_tier,r.membership_expires_at,r.last_used_at
		FROM hxc_dashboard_versions v JOIN hxc_dashboard_rows r ON r.projection_id=v.id
		WHERE v.id=$1 AND r.identity_state='matched' AND r.customer_id=ANY($2)`, version, ids)
	if err != nil {
		return nil, fmt.Errorf("read HXC shared facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item hxcport.SharedFacts
		var customerID int64
		var formalLogin, cardOpened, expires, lastUsed *time.Time
		var formallyLogged, tokenUsed, learningFound, membershipFound, isMember *bool
		if err = rows.Scan(&customerID, &item.SourceAsOf, &item.SourceUpdatedAt, &formallyLogged, &formalLogin, &tokenUsed, &learningFound, &item.LearningPlanStatus, &item.LearningPlanCurrent, &item.LearningPlanTotal, &item.CardOpenCount7D, &cardOpened, &membershipFound, &isMember, &item.MembershipSource, &item.MembershipStatus, &item.Tier, &expires, &lastUsed); err != nil {
			return nil, fmt.Errorf("scan HXC shared facts: %w", err)
		}
		item.CustomerID = customerdomain.CustomerID(customerID)
		if _, duplicate := out[item.CustomerID]; duplicate {
			out[item.CustomerID] = hxcport.SharedFacts{CustomerID: item.CustomerID, Availability: hxcport.SharedFactsAmbiguous}
			continue
		}
		if !available {
			item.Availability = hxcport.SharedFactsUnavailable
			out[item.CustomerID] = item
			continue
		}
		item.Availability = hxcport.SharedFactsAvailable
		item.FormalLoginAt, item.CardLastOpenedAt, item.ExpiresAt, item.LastUsedAt = formalLogin, cardOpened, expires, lastUsed
		item.FormallyLoggedIn = formallyLogged != nil && *formallyLogged
		item.HasTokenUsage = tokenUsed != nil && *tokenUsed
		item.LearningPlanFound = learningFound != nil && *learningFound
		item.MembershipRecordFound = membershipFound != nil && *membershipFound
		item.IsMember = isMember != nil && *isMember
		item.Registered = true // a row exists only for an undeleted legacy HXC user.
		item.HasRealUsage = lastUsed != nil
		out[item.CustomerID] = item
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HXC shared facts: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit HXC shared facts snapshot: %w", err)
	}
	return out, nil
}

func sharedFactsIDs(customerIDs []customerdomain.CustomerID) ([]int64, error) {
	ids := make([]int64, 0, len(customerIDs))
	seen := map[customerdomain.CustomerID]bool{}
	for _, id := range customerIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, int64(id))
		}
	}
	if len(ids) > hxcport.MaxSharedFactsCustomerIDs {
		return nil, hxcport.ErrSharedFactsBatchTooLarge
	}
	return ids, nil
}

var _ hxcport.SharedFactsReader = (*PostgreSQL)(nil)
var _ hxcport.VersionedSharedFactsReader = (*PostgreSQL)(nil)

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type Summary struct {
	ID               int64
	RuleVersion      string
	AsOf             time.Time
	Watermark        *time.Time
	PublishedAt      time.Time
	SourceDigest     [32]byte
	ProjectionDigest [32]byte
	Counts           domain.Counts
}

func (store *PostgreSQL) Summary(ctx context.Context) (Summary, error) {
	var s Summary
	var sourceDigest, projectionDigest []byte
	err := store.pool.QueryRow(ctx, `SELECT id,rule_version,projection_as_of,source_watermark,published_at,source_digest,projection_digest,total_count,active_used_count,active_unused_count,registered_no_active_membership_count,matched_count,unmatched_count,conflict_count,matched_by_unionid_count,matched_by_phone_count,matched_by_both_count,pending_observation_count,invalid_identity_count FROM hxc_dashboard_versions WHERE status='published'`).Scan(&s.ID, &s.RuleVersion, &s.AsOf, &s.Watermark, &s.PublishedAt, &sourceDigest, &projectionDigest, &s.Counts.Total, &s.Counts.ActiveUsed, &s.Counts.ActiveUnused, &s.Counts.RegisteredNoActiveMembership, &s.Counts.Matched, &s.Counts.Unmatched, &s.Counts.Conflict, &s.Counts.MatchedByUnionID, &s.Counts.MatchedByPhone, &s.Counts.MatchedByBoth, &s.Counts.PendingObservation, &s.Counts.InvalidIdentity)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	copy(s.SourceDigest[:], sourceDigest)
	copy(s.ProjectionDigest[:], projectionDigest)
	return s, err
}

type Query struct {
	ProjectionID                                                                              int64
	Stages, SubscriptionTiers, LastCapabilities, BusinessStages, UserSegments, IdentityStates []string
	MatchedBy, IdentityReasonCodes                                                            []string
	SubjectDigest                                                                             []byte
	Sort                                                                                      string
	GroupBy                                                                                   string
	Limit, Offset                                                                             int
}
type Row struct {
	UserRef               string          `json:"user_ref"`
	Stage                 string          `json:"stage"`
	SubscriptionTier      string          `json:"subscription_tier"`
	SubscriptionExpiresAt *time.Time      `json:"subscription_expires_at,omitempty"`
	MonthlyChatQuota      int64           `json:"monthly_chat_quota"`
	CurrentPeriodUsed     int64           `json:"current_period_used"`
	ConsultationLimit     int64           `json:"consultation_limit"`
	ConsultationUsed      int64           `json:"consultation_used"`
	MembershipAttribution string          `json:"membership_attribution"`
	Sessions7D            int64           `json:"sessions_7d"`
	Sessions30D           int64           `json:"sessions_30d"`
	SessionsTotal         int64           `json:"sessions_total"`
	UserMessages7D        int64           `json:"user_messages_7d"`
	UserMessages30D       int64           `json:"user_messages_30d"`
	UserMessagesTotal     int64           `json:"user_messages_total"`
	CapabilityUsage       json.RawMessage `json:"capability_usage"`
	LastUsedAt            *time.Time      `json:"last_used_at,omitempty"`
	LastCapability        string          `json:"last_capability,omitempty"`
	BusinessStage         string          `json:"business_stage,omitempty"`
	MainLineType          string          `json:"main_line_type,omitempty"`
	UserSegment           string          `json:"user_segment,omitempty"`
	FocusTopics           json.RawMessage `json:"focus_topics"`
	PainTag               string          `json:"pain_tag,omitempty"`
	IdentityState         string          `json:"identity_state"`
	MatchedBy             string          `json:"matched_by"`
	IdentityReasonCode    string          `json:"identity_reason_code"`
	IdentityCaseID        int64           `json:"identity_case_id,omitempty"`
	MergeCandidateID      int64           `json:"merge_candidate_id,omitempty"`
	SourceUpdatedAt       time.Time       `json:"source_updated_at"`
}
type Group struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type workspaceReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (store *PostgreSQL) QueryRows(ctx context.Context, q Query) ([]Row, []Group, bool, error) {
	return queryWorkspaceRows(ctx, store.pool, q)
}
func queryWorkspaceRows(ctx context.Context, reader workspaceReader, q Query) ([]Row, []Group, bool, error) {
	where, args := queryPredicate(q)
	order := map[string]string{"last_used_at_desc": "last_used_at DESC NULLS LAST,subject_digest", "source_updated_at_desc": "source_updated_at DESC,subject_digest", "subscription_expires_at_asc": "subscription_expires_at ASC NULLS LAST,subject_digest", "subscription_expires_at_desc": "subscription_expires_at DESC NULLS LAST,subject_digest", "messages_7d_desc": "user_messages_7d DESC,subject_digest"}[q.Sort]
	if order == "" {
		order = "last_used_at DESC NULLS LAST,subject_digest"
	}
	if column := map[string]string{"stage": "stage", "subscription_tier": "subscription_tier", "last_capability": "last_capability", "business_stage": "business_stage", "user_segment": "user_segment", "identity_state": "identity_state", "matched_by": "matched_by", "identity_reason_code": "identity_reason_code"}[q.GroupBy]; column != "" {
		order = "COALESCE(NULLIF(" + column + ",''),'(empty)') ASC," + order
	}
	args = append(args, q.Limit+1, q.Offset)
	sqlText := `SELECT user_ref,stage,subscription_tier,subscription_expires_at,monthly_chat_quota,current_period_used,consultation_limit,consultation_used,membership_attribution,sessions_7d,sessions_30d,sessions_total,user_messages_7d,user_messages_30d,user_messages_total,capability_usage,last_used_at,COALESCE(last_capability,''),COALESCE(business_stage,''),COALESCE(main_line_type,''),COALESCE(user_segment,''),focus_topics,COALESCE(pain_tag,''),identity_state,matched_by,identity_reason_code,COALESCE(identity_case_id,0),COALESCE(merge_candidate_id,0),source_updated_at FROM hxc_dashboard_rows WHERE ` + strings.Join(where, " AND ") + " ORDER BY " + order + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := reader.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	items := make([]Row, 0, q.Limit+1)
	for rows.Next() {
		var row Row
		if err = rows.Scan(&row.UserRef, &row.Stage, &row.SubscriptionTier, &row.SubscriptionExpiresAt, &row.MonthlyChatQuota, &row.CurrentPeriodUsed, &row.ConsultationLimit, &row.ConsultationUsed, &row.MembershipAttribution, &row.Sessions7D, &row.Sessions30D, &row.SessionsTotal, &row.UserMessages7D, &row.UserMessages30D, &row.UserMessagesTotal, &row.CapabilityUsage, &row.LastUsedAt, &row.LastCapability, &row.BusinessStage, &row.MainLineType, &row.UserSegment, &row.FocusTopics, &row.PainTag, &row.IdentityState, &row.MatchedBy, &row.IdentityReasonCode, &row.IdentityCaseID, &row.MergeCandidateID, &row.SourceUpdatedAt); err != nil {
			return nil, nil, false, err
		}
		items = append(items, row)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, false, err
	}
	more := len(items) > q.Limit
	if more {
		items = items[:q.Limit]
	}
	groups := []Group{}
	groupColumn := map[string]string{"stage": "stage", "subscription_tier": "subscription_tier", "last_capability": "last_capability", "business_stage": "business_stage", "user_segment": "user_segment", "identity_state": "identity_state", "matched_by": "matched_by", "identity_reason_code": "identity_reason_code"}[q.GroupBy]
	if groupColumn != "" {
		groupArgs := args[:len(args)-2]
		groupRows, groupErr := reader.Query(ctx, `SELECT COALESCE(NULLIF(`+groupColumn+`,''),'(empty)'),COUNT(*) FROM hxc_dashboard_rows WHERE `+strings.Join(where, " AND ")+` GROUP BY COALESCE(NULLIF(`+groupColumn+`,''),'(empty)') ORDER BY COUNT(*) DESC,1`, groupArgs...)
		if groupErr != nil {
			return nil, nil, false, groupErr
		}
		defer groupRows.Close()
		for groupRows.Next() {
			var group Group
			if err = groupRows.Scan(&group.Key, &group.Count); err != nil {
				return nil, nil, false, err
			}
			groups = append(groups, group)
		}
		if err = groupRows.Err(); err != nil {
			return nil, nil, false, err
		}
	}
	return items, groups, more, nil
}

func queryPredicate(q Query) ([]string, []any) {
	where := []string{"projection_id=$1"}
	args := []any{q.ProjectionID}
	addArray := func(column string, values []string) {
		if len(values) > 0 {
			args = append(args, values)
			where = append(where, fmt.Sprintf("%s=ANY($%d)", column, len(args)))
		}
	}
	addArray("stage", q.Stages)
	addArray("subscription_tier", q.SubscriptionTiers)
	addArray("last_capability", q.LastCapabilities)
	addArray("business_stage", q.BusinessStages)
	addArray("user_segment", q.UserSegments)
	addArray("identity_state", q.IdentityStates)
	addArray("matched_by", q.MatchedBy)
	addArray("identity_reason_code", q.IdentityReasonCodes)
	if len(q.SubjectDigest) > 0 {
		args = append(args, q.SubjectDigest)
		where = append(where, fmt.Sprintf("subject_digest=$%d", len(args)))
	}
	return where, args
}

// QueryMetrics uses the identical relation predicate as the paginated rows.
func (store *PostgreSQL) QueryMetrics(ctx context.Context, q Query) (map[string]int64, []Group, error) {
	return queryWorkspaceMetrics(ctx, store.pool, q)
}
func queryWorkspaceMetrics(ctx context.Context, reader workspaceReader, q Query) (map[string]int64, []Group, error) {
	where, args := queryPredicate(q)
	counts := map[string]int64{"total": 0, "active_used": 0, "active_unused": 0, "registered_no_active_membership": 0}
	rows, err := reader.Query(ctx, `SELECT stage, count(*) FROM hxc_dashboard_rows WHERE `+strings.Join(where, " AND ")+` GROUP BY stage`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var stage string
		var count int64
		if err = rows.Scan(&stage, &count); err != nil {
			rows.Close()
			return nil, nil, err
		}
		counts[stage] = count
		counts["total"] += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	tiers := []Group{}
	rows, err = reader.Query(ctx, `SELECT subscription_tier,count(*) FROM hxc_dashboard_rows WHERE `+strings.Join(where, " AND ")+` GROUP BY subscription_tier ORDER BY count(*) DESC,subscription_tier`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g Group
		if err = rows.Scan(&g.Key, &g.Count); err != nil {
			return nil, nil, err
		}
		tiers = append(tiers, g)
	}
	return counts, tiers, rows.Err()
}

// QueryWorkspace pins rows, full group counts and metrics to one PostgreSQL
// snapshot, including while retention deletes an older published generation.
type WorkspaceResult struct {
	Items         []Row
	Groups, Tiers []Group
	Metrics       map[string]int64
	More          bool
}

func (store *PostgreSQL) QueryWorkspace(ctx context.Context, q Query) (out WorkspaceResult, err error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hxc_dashboard_versions WHERE id=$1)`, q.ProjectionID).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, ErrNotFound
	}
	out.Items, out.Groups, out.More, err = queryWorkspaceRows(ctx, tx, q)
	if err != nil {
		return out, err
	}
	out.Metrics, out.Tiers, err = queryWorkspaceMetrics(ctx, tx, q)
	if err != nil {
		return out, err
	}
	err = tx.Commit(ctx)
	return out, err
}
