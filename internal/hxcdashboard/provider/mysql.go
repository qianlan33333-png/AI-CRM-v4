package provider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
)

const BatchSize = 1000
const sourceTimeout = 20 * time.Minute

// HXC stores business timestamps in MySQL DATETIME columns using Beijing wall
// time. DATETIME carries no timezone, so the driver must attach the source
// location when scanning values and when formatting query parameters.
var sourceLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

var ErrSourceNotReady = errors.New("hxc source is not ready")

type MySQL struct{ db *sql.DB }

func Open(dsn string) (*MySQL, error) {
	if strings.TrimSpace(dsn) != dsn || dsn == "" {
		return nil, ErrSourceNotReady
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, ErrSourceNotReady
	}
	cfg.ParseTime = true
	cfg.Loc = sourceLocation
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 2 * time.Minute
	cfg.WriteTimeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, ErrSourceNotReady
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	return &MySQL{db: db}, nil
}

func (source *MySQL) Close() error {
	if source == nil || source.db == nil {
		return nil
	}
	return source.db.Close()
}
func (source *MySQL) Ready() bool { return source != nil && source.db != nil }

var requiredColumns = map[string][]string{
	"new_version_users":               {"id", "unionid", "phone", "member_level", "member_expires_at", "first_login_at", "updated_at", "is_deleted"},
	"new_version_user_subscriptions":  {"user_id", "tier", "expires_at", "monthly_chat_quota", "current_period_used", "updated_at"},
	"new_version_memberships":         {"id", "user_id", "phone", "status", "consultation_limit", "consultation_used", "start_date", "end_date", "created_at", "updated_at"},
	"new_version_conversations":       {"user_id", "lesson_id", "content_type", "chat_mode", "created_at", "updated_at", "is_deleted"},
	"new_version_messages":            {"user_id", "role", "total_tokens", "created_at", "is_deleted"},
	"new_version_user_path_progress":  {"id", "user_id", "path_id", "current_seq", "status", "updated_at"},
	"new_version_lesson_path_items":   {"path_id"},
	"new_version_card_open_log":       {"user_id", "opened_at"},
	"new_version_consultation_states": {"user_id", "session_id", "is_deep_consult", "session_type", "started_at", "ended_at", "created_at", "updated_at"},
	"new_version_assessments":         {"user_id", "status", "completed_at", "created_at", "updated_at"},
	"new_version_growth_reviews":      {"user_id", "surfaced_at", "created_at"},
	"new_version_user_backgrounds":    {"user_id", "business_stage", "main_line_type", "focus_topics", "pain_tag", "updated_at"},
	"new_version_user_diagnoses":      {"user_id", "stage", "main_line_type", "user_segment", "updated_at"},
	"new_version_user_interests":      {"user_id", "interest_keys", "updated_at"},
}

func (source *MySQL) Preflight(ctx context.Context) error {
	if !source.Ready() {
		return ErrSourceNotReady
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			var exists int
			if err := source.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&exists); err != nil || exists != 1 {
				return fmt.Errorf("hxc_schema_missing:%s.%s", table, column)
			}
		}
	}
	rows, err := source.db.QueryContext(ctx, "EXPLAIN "+currentBatchSQL, batchArgs("", time.Now().UTC())...)
	if err != nil {
		return fmt.Errorf("hxc_explain_failed: %w", err)
	}
	return rows.Close()
}

func (source *MySQL) ReadSnapshot(ctx context.Context, asOf time.Time) (port.Snapshot, error) {
	if !source.Ready() {
		return port.Snapshot{}, ErrSourceNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	tx, err := source.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return port.Snapshot{}, fmt.Errorf("begin HXC snapshot: %w", err)
	}
	defer tx.Rollback()
	result := port.Snapshot{AsOf: asOf.UTC(), Rows: make([]domain.SourceRow, 0, BatchSize)}
	after := ""
	hasher := sha256.New()
	for {
		batch, err := readBatch(ctx, tx, after, asOf.UTC())
		if err != nil {
			return port.Snapshot{}, err
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			row := batch[i]
			encoded, _ := json.Marshal(row)
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
			hasher.Write(size[:])
			hasher.Write(encoded)
			result.Rows = append(result.Rows, row)
			if result.Watermark == nil || row.SourceUpdatedAt.After(*result.Watermark) {
				value := row.SourceUpdatedAt
				result.Watermark = &value
			}
		}
		after = batch[len(batch)-1].HXCUserID
		if len(batch) < BatchSize {
			break
		}
	}
	copy(result.Digest[:], hasher.Sum(nil))
	if err = tx.Commit(); err != nil {
		return port.Snapshot{}, fmt.Errorf("commit HXC read-only snapshot: %w", err)
	}
	result.Complete = true
	return result, nil
}

func readBatch(ctx context.Context, tx *sql.Tx, after string, asOf time.Time) ([]domain.SourceRow, error) {
	rows, err := tx.QueryContext(ctx, currentBatchSQL, batchArgs(after, asOf)...)
	if err != nil {
		return nil, fmt.Errorf("query HXC batch: %w", err)
	}
	defer rows.Close()
	result := make([]domain.SourceRow, 0, BatchSize)
	for rows.Next() {
		row, scanErr := scanSourceRow(rows, asOf)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HXC batch: %w", err)
	}
	return result, nil
}

type sourceRowScanner interface{ Scan(...any) error }

// scanSourceRow keeps the projection contract in one place so the database
// path and the controlled scanner fixture exercise the identical column order.
func scanSourceRow(scanner sourceRowScanner, asOf time.Time) (domain.SourceRow, error) {
	var row domain.SourceRow
	var union, phone, lastCapability, business, mainline, segment, pain, planStatus sql.NullString
	var expiry, formalLogin sql.NullTime
	var tokenUsed, learningFound, membershipFound int64
	var membershipAttribution, membershipStatus, subscriptionTier, profileTier sql.NullString
	var membershipExpiry, subscriptionExpiry, profileExpiry sql.NullTime
	var learningCurrent, learningTotal sql.NullInt64
	var openCount int64
	// MySQL reports the GREATEST/NULLIF expression as []byte even with
	// parseTime enabled, so use a scanner that applies the same source
	// location as native DATETIME columns.
	var lastUsed, lastOpened sourceNullTime
	var capJSON, topicsJSON []byte
	if err := scanner.Scan(&row.HXCUserID, &union, &phone, &row.SubscriptionTier, &expiry, &row.MonthlyChatQuota, &row.CurrentPeriodUsed, &row.ConsultationLimit, &row.ConsultationUsed, &row.MembershipAttribution, &row.Sessions7D, &row.Sessions30D, &row.SessionsTotal, &row.UserMessages7D, &row.UserMessages30D, &row.UserMessagesTotal, &capJSON, &lastUsed, &lastCapability, &business, &mainline, &segment, &topicsJSON, &pain, &formalLogin, &tokenUsed, &learningFound, &planStatus, &learningCurrent, &learningTotal, &openCount, &lastOpened, &membershipFound, &membershipAttribution, &membershipStatus, &membershipExpiry, &subscriptionTier, &subscriptionExpiry, &profileTier, &profileExpiry, &row.SourceUpdatedAt); err != nil {
		return domain.SourceRow{}, fmt.Errorf("scan HXC batch: %w", err)
	}
	row.UnionID = union.String
	row.Phone = phone.String
	if expiry.Valid {
		row.SubscriptionExpiresAt = &expiry.Time
	}
	if lastUsed.Valid {
		row.LastUsedAt = &lastUsed.Time
	}
	if formalLogin.Valid {
		row.FormalLoginAt = &formalLogin.Time
		row.FormallyLoggedIn = true
	}
	row.HasTokenUsage = tokenUsed != 0
	row.LearningPlanFound = learningFound != 0
	row.LearningPlanStatus = planStatus.String
	if learningCurrent.Valid {
		value := learningCurrent.Int64
		row.LearningPlanCurrent = &value
	}
	if learningTotal.Valid {
		value := learningTotal.Int64
		row.LearningPlanTotal = &value
	}
	row.CardOpenCount7D = openCount
	if lastOpened.Valid {
		row.CardLastOpenedAt = &lastOpened.Time
	}
	selected := selectMembershipSource(asOf, membershipCandidate{found: membershipFound != 0, source: membershipAttribution.String, status: membershipStatus.String, expiresAt: nullableTime(membershipExpiry)}, membershipCandidate{source: "subscription", status: subscriptionTier.String, expiresAt: nullableTime(subscriptionExpiry)}, membershipCandidate{source: "user_profile", status: profileTier.String, expiresAt: nullableTime(profileExpiry)})
	row.MembershipRecordFound, row.IsMember = selected.found, selected.isMember
	row.MembershipSource, row.MembershipStatus, row.MembershipExpiresAt = selected.source, selected.status, selected.expiresAt
	row.CapabilityUsage = append([]byte(nil), capJSON...)
	row.FocusTopics = append([]byte(nil), topicsJSON...)
	row.LastCapability = lastCapability.String
	row.BusinessStage = business.String
	row.MainLineType = mainline.String
	row.UserSegment = segment.String
	row.PainTag = pain.String
	return row, nil
}

type membershipCandidate struct {
	found     bool
	source    string
	status    string
	expiresAt *time.Time
	isMember  bool
	effective bool
}

// selectMembershipSource preserves the legacy OR predicate without pairing a
// status from one source with an expiry from another. A concrete expired
// consultation membership yields to independently valid subscription/profile
// evidence; otherwise it remains observable as the selected source.
func selectMembershipSource(reference time.Time, membership, subscription, profile membershipCandidate) membershipCandidate {
	membership.isMember = membership.found && membershipEvidenceActive(reference, membership.status, membership.expiresAt)
	membership.effective = membership.isMember && membershipExpiryEffective(reference, membership.expiresAt)
	subscription.found = subscriptionEvidencePresent(reference, subscription.status, subscription.expiresAt)
	subscription.isMember = subscription.found && membershipEvidenceActive(reference, subscription.status, subscription.expiresAt)
	subscription.effective = subscription.isMember && membershipExpiryEffective(reference, subscription.expiresAt)
	profile.found = subscriptionEvidencePresent(reference, profile.status, profile.expiresAt)
	profile.isMember = profile.found && membershipEvidenceActive(reference, profile.status, profile.expiresAt)
	profile.effective = profile.isMember && membershipExpiryEffective(reference, profile.expiresAt)

	// A source only wins the current fact if its own expiry is still effective.
	// This keeps an expired active consultation record from hiding a valid
	// subscription, while retaining source-local facts when all are expired.
	for _, candidate := range []membershipCandidate{membership, subscription, profile} {
		if candidate.effective {
			return candidate
		}
	}
	if membership.found {
		return membership
	}
	if subscription.found {
		return subscription
	}
	if profile.found {
		return profile
	}
	return membershipCandidate{}
}

func subscriptionEvidencePresent(reference time.Time, status string, expiresAt *time.Time) bool {
	normalized := normalizedMembershipStatus(status)
	if normalized == "free" {
		return false
	}
	return normalized != "" || expiresAt != nil && expiresAt.After(reference)
}

// membershipEvidenceActive is the frozen legacy OR predicate: a recognized
// membership status or a future source-local expiry is evidence. An explicit
// expired/free source cannot become a member merely because it has a date.
func membershipEvidenceActive(reference time.Time, status string, expiresAt *time.Time) bool {
	normalized := normalizedMembershipStatus(status)
	if normalized == "expired" || normalized == "free" {
		return false
	}
	return normalized == "active" || normalized == "valid" || normalized == "premium" || normalized == "standard" || normalized == "trial" || expiresAt != nil && expiresAt.After(reference)
}

func membershipExpiryEffective(reference time.Time, expiresAt *time.Time) bool {
	return expiresAt == nil || expiresAt.After(reference)
}

func normalizedMembershipStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

type sourceNullTime struct {
	Time  time.Time
	Valid bool
}

func (value *sourceNullTime) Scan(input any) error {
	switch typed := input.(type) {
	case nil:
		value.Time = time.Time{}
		value.Valid = false
		return nil
	case time.Time:
		value.Time = typed.In(sourceLocation)
		value.Valid = true
		return nil
	case []byte:
		return value.scanText(string(typed))
	case string:
		return value.scanText(typed)
	default:
		return fmt.Errorf("unsupported HXC datetime type %T", input)
	}
}

func (value *sourceNullTime) scanText(input string) error {
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05.999999999", input, sourceLocation)
	if err != nil {
		value.Time = time.Time{}
		value.Valid = false
		return fmt.Errorf("parse HXC datetime: %w", err)
	}
	value.Time = parsed
	value.Valid = true
	return nil
}

func batchArgs(after string, asOf time.Time) []any {
	args := []any{after, BatchSize}
	for range 16 {
		args = append(args, asOf)
	}
	return args
}

const currentBatchSQL = `WITH active_users AS (
 SELECT id,unionid,phone,member_level,member_expires_at,first_login_at,updated_at FROM new_version_users WHERE is_deleted=0 AND id>? ORDER BY id LIMIT ?
), phone_counts AS (SELECT phone,COUNT(*) n FROM new_version_users WHERE is_deleted=0 AND phone IS NOT NULL AND TRIM(phone)<>'' GROUP BY phone),
membership_ranked AS (
 SELECT u.id user_id,m.consultation_limit,m.consultation_used,m.status,m.end_date,IF(m.user_id=u.id,'user_id','unique_phone') attribution,COALESCE(m.updated_at,m.created_at,m.end_date,m.start_date) source_updated_at,
 ROW_NUMBER() OVER(PARTITION BY u.id ORDER BY (m.user_id=u.id) DESC,(m.status='active' AND m.end_date>=?) DESC,(m.status='active') DESC,m.end_date DESC,COALESCE(m.updated_at,m.created_at,m.end_date,m.start_date) DESC,m.id DESC) row_num
 FROM active_users u LEFT JOIN phone_counts pc ON pc.phone=u.phone AND pc.n=1 JOIN new_version_memberships m ON m.user_id=u.id OR ((m.user_id IS NULL OR m.user_id='') AND pc.phone IS NOT NULL AND m.phone=pc.phone)
), membership_current AS (SELECT user_id,consultation_limit,consultation_used,status,end_date,attribution,source_updated_at FROM membership_ranked WHERE row_num=1),
token_usage AS (SELECT m.user_id,MAX(CASE WHEN COALESCE(m.total_tokens,0)>0 THEN 1 ELSE 0 END) has_token_usage,MAX(m.created_at) source_updated_at FROM new_version_messages m JOIN active_users u ON m.user_id COLLATE utf8mb4_general_ci=u.id WHERE COALESCE(m.is_deleted,0)=0 GROUP BY m.user_id),
lesson_totals AS (SELECT path_id,COUNT(*) total_lessons FROM new_version_lesson_path_items GROUP BY path_id),
learning_ranked AS (SELECT p.user_id,p.status,LEAST(GREATEST(COALESCE(p.current_seq,0),0),COALESCE(t.total_lessons,0)) current_lessons,COALESCE(t.total_lessons,0) total_lessons,p.updated_at source_updated_at,ROW_NUMBER() OVER(PARTITION BY p.user_id ORDER BY CASE WHEN p.status='active' THEN 0 ELSE 1 END,p.updated_at DESC,p.id DESC) row_num FROM new_version_user_path_progress p JOIN active_users u ON p.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN lesson_totals t ON t.path_id COLLATE utf8mb4_general_ci=p.path_id WHERE p.status IN ('active','done','paused')),
learning_current AS (SELECT user_id,status,current_lessons,total_lessons,source_updated_at FROM learning_ranked WHERE row_num=1),
card_open_usage AS (SELECT o.user_id,SUM(CASE WHEN o.opened_at>=? - INTERVAL 7 DAY THEN 1 ELSE 0 END) open_count_7d,MAX(o.opened_at) last_opened_at,MAX(o.opened_at) source_updated_at FROM new_version_card_open_log o JOIN active_users u ON o.user_id COLLATE utf8mb4_general_ci=u.id GROUP BY o.user_id),
conversation_usage AS (SELECT c.user_id,COUNT(*) sessions_total,SUM(c.created_at>=? - INTERVAL 30 DAY) sessions_30d,SUM(c.created_at>=? - INTERVAL 7 DAY) sessions_7d,
 SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer') peer_total,SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' AND c.created_at>=? - INTERVAL 30 DAY) peer_30d,SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' AND c.created_at>=? - INTERVAL 7 DAY) peer_7d,MAX(CASE WHEN NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' THEN c.updated_at END) peer_last,
 SUM((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') lesson_total,SUM(((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.created_at>=? - INTERVAL 30 DAY) lesson_30d,SUM(((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.created_at>=? - INTERVAL 7 DAY) lesson_7d,MAX(CASE WHEN (c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson' THEN c.updated_at END) lesson_last,MAX(c.updated_at) source_updated_at FROM new_version_conversations c JOIN active_users u ON u.id=c.user_id WHERE c.is_deleted=0 GROUP BY c.user_id),
message_usage AS (SELECT m.user_id,COUNT(*) messages_total,SUM(m.created_at>=? - INTERVAL 30 DAY) messages_30d,SUM(m.created_at>=? - INTERVAL 7 DAY) messages_7d,MAX(m.created_at) last_used,MAX(m.created_at) source_updated_at FROM new_version_messages m JOIN active_users u ON u.id=m.user_id WHERE m.is_deleted=0 AND m.role='user' GROUP BY m.user_id),
coach_usage AS (SELECT c.user_id,COUNT(DISTINCT c.session_id) total,SUM(COALESCE(c.started_at,c.created_at,c.updated_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(c.started_at,c.created_at,c.updated_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(c.ended_at,c.updated_at,c.started_at,c.created_at)) last_used,MAX(COALESCE(c.updated_at,c.ended_at,c.started_at,c.created_at)) source_updated_at FROM new_version_consultation_states c JOIN active_users u ON u.id=c.user_id WHERE c.is_deep_consult=1 OR c.session_type='topic_consult' GROUP BY c.user_id),
assessment_usage AS (SELECT a.user_id,COUNT(*) total,SUM(COALESCE(a.completed_at,a.updated_at,a.created_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(a.completed_at,a.updated_at,a.created_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(a.completed_at,a.updated_at,a.created_at)) last_used,MAX(COALESCE(a.updated_at,a.completed_at,a.created_at)) source_updated_at FROM new_version_assessments a JOIN active_users u ON a.user_id COLLATE utf8mb4_general_ci=u.id WHERE a.status='completed' GROUP BY a.user_id),
review_usage AS (SELECT r.user_id,COUNT(*) total,SUM(COALESCE(r.surfaced_at,r.created_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(r.surfaced_at,r.created_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(r.surfaced_at,r.created_at)) last_used,MAX(COALESCE(r.surfaced_at,r.created_at)) source_updated_at FROM new_version_growth_reviews r JOIN active_users u ON r.user_id COLLATE utf8mb4_general_ci=u.id GROUP BY r.user_id)
SELECT u.id,NULLIF(TRIM(u.unionid),''),NULLIF(TRIM(u.phone),''),COALESCE(NULLIF(TRIM(s.tier),''),NULLIF(TRIM(u.member_level),''),'free'),COALESCE(s.expires_at,u.member_expires_at),GREATEST(COALESCE(s.monthly_chat_quota,0),0),GREATEST(COALESCE(s.current_period_used,0),0),GREATEST(COALESCE(mc.consultation_limit,0),0),GREATEST(COALESCE(mc.consultation_used,0),0),COALESCE(mc.attribution,'none'),COALESCE(c.sessions_7d,0),COALESCE(c.sessions_30d,0),COALESCE(c.sessions_total,0),COALESCE(msg.messages_7d,0),COALESCE(msg.messages_30d,0),COALESCE(msg.messages_total,0),
JSON_OBJECT('peer_chat',JSON_OBJECT('count_7d',COALESCE(c.peer_7d,0),'count_30d',COALESCE(c.peer_30d,0),'count_total',COALESCE(c.peer_total,0),'last_used_at',c.peer_last),'coach_consult',JSON_OBJECT('count_7d',COALESCE(coach.count_7d,0),'count_30d',COALESCE(coach.count_30d,0),'count_total',COALESCE(coach.total,0),'last_used_at',coach.last_used),'lesson',JSON_OBJECT('count_7d',COALESCE(c.lesson_7d,0),'count_30d',COALESCE(c.lesson_30d,0),'count_total',COALESCE(c.lesson_total,0),'last_used_at',c.lesson_last),'assessment',JSON_OBJECT('count_7d',COALESCE(a.count_7d,0),'count_30d',COALESCE(a.count_30d,0),'count_total',COALESCE(a.total,0),'last_used_at',a.last_used),'weekly_review',JSON_OBJECT('count_7d',COALESCE(r.count_7d,0),'count_30d',COALESCE(r.count_30d,0),'count_total',COALESCE(r.total,0),'last_used_at',r.last_used)),
NULLIF(GREATEST(COALESCE(c.peer_last,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(coach.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(c.lesson_last,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(a.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(r.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(msg.last_used,TIMESTAMP('1000-01-01 00:00:00'))),TIMESTAMP('1000-01-01 00:00:00')),
CASE WHEN GREATEST(COALESCE(r.last_used,'1000-01-01'),COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01'),COALESCE(msg.last_used,'1000-01-01'))='1000-01-01' THEN NULL WHEN COALESCE(msg.last_used,'1000-01-01')>=GREATEST(COALESCE(r.last_used,'1000-01-01'),COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'user_message' WHEN COALESCE(r.last_used,'1000-01-01')>=GREATEST(COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'weekly_review' WHEN COALESCE(a.last_used,'1000-01-01')>=GREATEST(COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'assessment' WHEN COALESCE(c.lesson_last,'1000-01-01')>=GREATEST(COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'lesson' WHEN COALESCE(coach.last_used,'1000-01-01')>=COALESCE(c.peer_last,'1000-01-01') THEN 'coach_consult' ELSE 'peer_chat' END,
COALESCE(NULLIF(TRIM(bg.business_stage),''),NULLIF(TRIM(d.stage),'')),COALESCE(NULLIF(TRIM(bg.main_line_type),''),NULLIF(TRIM(d.main_line_type),'')),NULLIF(TRIM(d.user_segment),''),CASE WHEN JSON_TYPE(bg.focus_topics)='ARRAY' AND JSON_LENGTH(bg.focus_topics)>0 THEN bg.focus_topics WHEN JSON_TYPE(i.interest_keys)='ARRAY' THEN i.interest_keys ELSE JSON_ARRAY() END,NULLIF(TRIM(bg.pain_tag),''),u.first_login_at,COALESCE(tok.has_token_usage,0),CASE WHEN lp.user_id IS NULL THEN 0 ELSE 1 END,lp.status,lp.current_lessons,lp.total_lessons,COALESCE(openlog.open_count_7d,0),openlog.last_opened_at,
CASE WHEN mc.user_id IS NULL THEN 0 ELSE 1 END,COALESCE(mc.attribution,''),COALESCE(mc.status,''),mc.end_date,
NULLIF(TRIM(s.tier),''),s.expires_at,NULLIF(TRIM(u.member_level),''),u.member_expires_at,
GREATEST(u.updated_at,COALESCE(s.updated_at,u.updated_at),COALESCE(mc.source_updated_at,u.updated_at),COALESCE(c.source_updated_at,u.updated_at),COALESCE(msg.source_updated_at,u.updated_at),COALESCE(tok.source_updated_at,u.updated_at),COALESCE(lp.source_updated_at,u.updated_at),COALESCE(openlog.source_updated_at,u.updated_at),COALESCE(coach.source_updated_at,u.updated_at),COALESCE(a.source_updated_at,u.updated_at),COALESCE(r.source_updated_at,u.updated_at),COALESCE(bg.updated_at,u.updated_at),COALESCE(d.updated_at,u.updated_at),COALESCE(i.updated_at,u.updated_at))
FROM active_users u LEFT JOIN new_version_user_subscriptions s ON s.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN membership_current mc ON mc.user_id=u.id LEFT JOIN conversation_usage c ON c.user_id=u.id LEFT JOIN message_usage msg ON msg.user_id=u.id LEFT JOIN token_usage tok ON tok.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN learning_current lp ON lp.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN card_open_usage openlog ON openlog.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN coach_usage coach ON coach.user_id=u.id LEFT JOIN assessment_usage a ON a.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN review_usage r ON r.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_backgrounds bg ON bg.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_diagnoses d ON d.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_interests i ON i.user_id COLLATE utf8mb4_general_ci=u.id ORDER BY u.id`
