// Command migrate-message-archive extracts one explicit, read-only legacy archive
// snapshot or imports a previously extracted snapshot. It never starts sync or
// invokes the WeCom SDK.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	archivemigration "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/migration"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	errInvalidArguments = errors.New("invalid migration arguments")
	errReconcileDrift   = errors.New("migration reconciliation drift")
)

type options struct {
	mode, snapshot, digest, sourceDatabaseURLFile, sourceRevision, corpID string
	confirm                                                               bool
	limit                                                                 int
	afterParticipantID                                                    int64
}

type rowResult struct {
	SourceRowKey string `json:"source_row_key"`
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason,omitempty"`
}

type result struct {
	Inserted, Duplicates, Unresolved, Quarantined int
	Rows                                          []rowResult `json:"rows,omitempty"`
	NextParticipantID                             int64       `json:"next_participant_id,omitempty"`
}

type historicalIdentityReader interface {
	VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error)
}

type historicalStaffReader interface {
	UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
}

type historicalResolver struct {
	identity historicalIdentityReader
	staff    historicalStaffReader
	corpID   string
	now      func() time.Time
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "message archive migration failed:", publicError(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-message-archive", flag.ContinueOnError)
	flags.SetOutput(ioDiscard{})
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "extract|inspect|dry-run|apply|reconcile|re-resolve")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "0600 path to an offline normalized archive snapshot")
	flags.StringVar(&cfg.sourceDatabaseURLFile, "source-database-url-file", "", "0600 file containing the read-only legacy PostgreSQL URL for extract")
	flags.StringVar(&cfg.sourceRevision, "source-revision", "", "40-character frozen legacy source revision for extract")
	flags.StringVar(&cfg.corpID, "wecom-corp-id", "", "WeCom corp ID for extract")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "exact snapshot SHA-256 required for apply and reconcile")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm the exact snapshot for apply")
	flags.IntVar(&cfg.limit, "limit", 500, "bounded re-resolve participant count")
	flags.Int64Var(&cfg.afterParticipantID, "after-participant-id", 0, "exclusive participant ID cursor for re-resolve")
	if err := flags.Parse(args); err != nil || cfg.limit < 1 || cfg.limit > 5000 || cfg.afterParticipantID < 0 {
		return errInvalidArguments
	}
	if cfg.mode == "extract" {
		manifest, err := extractLegacyArchiveSnapshot(ctx, cfg)
		if err != nil {
			return err
		}
		if err = saveArchiveSnapshot(cfg.snapshot, manifest); err != nil {
			return err
		}
		persisted, err := archivemigration.Load(cfg.snapshot)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"mode": "extract", "manifest_sha256": hex.EncodeToString(persisted.Digest[:]), "source_name": persisted.SourceName, "summary": persisted.Summary(), "source_behavior": "read_only_legacy_snapshot"})
	}
	if cfg.snapshot == "" {
		return errInvalidArguments
	}
	manifest, err := archivemigration.Load(cfg.snapshot)
	if err != nil {
		return err
	}
	summary := manifest.Summary()
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": "inspect", "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "source_name": manifest.SourceName, "summary": summary})
	}
	if cfg.mode != "dry-run" && cfg.mode != "apply" && cfg.mode != "reconcile" && cfg.mode != "re-resolve" {
		return errInvalidArguments
	}
	if (cfg.mode == "apply" || cfg.mode == "reconcile") && confirmDigest(cfg.digest, manifest.Digest) != nil {
		return errInvalidArguments
	}
	if cfg.mode == "apply" && !cfg.confirm {
		return errInvalidArguments
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	native := pool.Native()
	resolver := newHistoricalResolver(manifest)

	switch cfg.mode {
	case "dry-run":
		values, dryErr := dryRun(ctx, native, manifest, resolver)
		if dryErr != nil {
			return dryErr
		}
		return printJSON(map[string]any{"mode": "dry-run", "eligible": true, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "source_name": manifest.SourceName, "summary": summary, "result": values, "identity_behavior": "read_existing_verified_only"})
	case "apply":
		values, applyErr := apply(ctx, native, manifest, resolver)
		if applyErr != nil {
			return applyErr
		}
		return printJSON(map[string]any{"mode": "apply", "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "result": values, "summary": summary, "identity_behavior": "read_existing_verified_only"})
	case "reconcile":
		matched, reconcileErr := reconcile(ctx, native, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": "reconcile", "matched": matched, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	case "re-resolve":
		values, resolveErr := reResolve(ctx, native, manifest, resolver, cfg.limit, cfg.afterParticipantID)
		if resolveErr != nil {
			return resolveErr
		}
		return printJSON(map[string]any{"mode": "re-resolve", "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "result": values, "identity_behavior": "read_existing_verified_only"})
	}
	return errInvalidArguments
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

func publicError(err error) string {
	switch {
	case errors.Is(err, errInvalidArguments):
		return "invalid_arguments"
	case errors.Is(err, archivemigration.ErrInvalidManifest):
		return "invalid_snapshot"
	case errors.Is(err, errReconcileDrift):
		return "reconciliation_failed"
	case errors.Is(err, pgx.ErrNoRows):
		return "required_migration_run_not_found"
	default:
		// Database and Provider errors can embed row values. The command reports a
		// stable code instead of placing offline message content in terminal logs.
		return "migration_unavailable"
	}
}

func confirmDigest(value string, digest [sha256.Size]byte) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || subtle.ConstantTimeCompare(decoded, digest[:]) != 1 {
		return errInvalidArguments
	}
	return nil
}

func newHistoricalResolver(manifest archivemigration.Manifest) historicalResolver {
	return historicalResolver{
		identity: identityquery.NewPostgreSQL(),
		staff:    accessstore.NewPostgreSQL(),
		corpID:   strings.TrimPrefix(manifest.CorpScope, "wecom-corp:"),
		now:      time.Now,
	}
}

// Resolve only reads facts already verified by their owning domains. It never
// calls a Provider, constructs a VerifiedFact, provisions a Customer, attaches
// an identity, or changes a customer merge relationship.
func (resolver historicalResolver) Resolve(ctx context.Context, message *domain.Message) error {
	if message == nil || resolver.identity == nil || resolver.staff == nil || resolver.corpID == "" {
		return errInvalidArguments
	}
	now := resolver.now().UTC()
	for index := range message.Participants {
		participant := &message.Participants[index]
		switch participant.ActorType {
		case domain.ActorExternal:
			customerID, found, err := resolver.identity.VerifiedWeComCustomer(ctx, resolver.corpID, participant.ProviderValue)
			if err != nil {
				return err
			}
			participant.StaffUserID, participant.IdentityID = 0, 0
			if !found {
				participant.CustomerID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = 0, domain.ResolutionNotFound, "historical_no_verified_identity", &now
				continue
			}
			participant.CustomerID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = customerID, domain.ResolutionFound, "historical_verified_identity_read", &now
		case domain.ActorStaff:
			staff, err := resolver.staff.UserByWeComUserID(ctx, participant.ProviderValue, false)
			if errors.Is(err, accessdomain.ErrNotFound) {
				participant.StaffUserID, participant.CustomerID, participant.IdentityID = 0, 0, 0
				participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = domain.ResolutionNotFound, "historical_staff_not_found", &now
				continue
			}
			if err != nil {
				return err
			}
			participant.StaffUserID, participant.CustomerID, participant.IdentityID = staff.ID, 0, 0
			participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = domain.ResolutionNotApplicable, "historical_staff_read", &now
		case domain.ActorRobot:
			participant.StaffUserID, participant.CustomerID, participant.IdentityID = 0, 0, 0
			participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = domain.ResolutionNotApplicable, "robot", &now
		default:
			participant.StaffUserID, participant.CustomerID, participant.IdentityID = 0, 0, 0
			participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = domain.ResolutionNotApplicable, "historical_actor_unknown", &now
		}
	}
	if !message.Valid() {
		return errInvalidArguments
	}
	return nil
}

func dryRun(ctx context.Context, pool *pgxpool.Pool, manifest archivemigration.Manifest, resolver historicalResolver) (result, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	txContext := platformpostgres.BindTransaction(ctx, tx)
	runID, err := beginRunTx(txContext, tx, manifest, "dry_run")
	if err != nil {
		return result{}, err
	}
	values, err := applyRowsTx(txContext, tx, runID, manifest, resolver)
	if err != nil {
		return result{}, err
	}
	if err = finishRunTx(txContext, tx, runID, "succeeded", ""); err != nil {
		return result{}, err
	}
	// Deliberately rollback after executing the same row-level inserts,
	// duplicate checks, OneID reads and receipt construction as apply.
	return values, nil
}

func apply(ctx context.Context, pool *pgxpool.Pool, manifest archivemigration.Manifest, resolver historicalResolver) (result, error) {
	runID, err := beginRun(ctx, pool, manifest, "apply")
	if err != nil {
		return result{}, err
	}
	values := result{}
	for _, row := range manifest.SortedRecords() {
		outcome, reason, err := applyRow(ctx, pool, runID, manifest, resolver, row)
		if err != nil {
			_ = finishRun(context.Background(), pool, runID, "failed", "apply_failed")
			return values, err
		}
		values.add(row.SourceRowKey, outcome, reason)
	}
	if err = finishRun(ctx, pool, runID, "succeeded", ""); err != nil {
		return values, err
	}
	return values, nil
}

func applyRowsTx(ctx context.Context, tx pgx.Tx, runID int64, manifest archivemigration.Manifest, resolver historicalResolver) (result, error) {
	values := result{}
	for _, row := range manifest.SortedRecords() {
		outcome, reason, err := applyRowTx(ctx, tx, runID, manifest, resolver, row)
		if err != nil {
			return values, err
		}
		values.add(row.SourceRowKey, outcome, reason)
	}
	return values, nil
}

func (values *result) add(key, outcome, reason string) {
	values.Rows = append(values.Rows, rowResult{SourceRowKey: key, Outcome: outcome, Reason: reason})
	switch outcome {
	case "inserted":
		values.Inserted++
	case "duplicate":
		values.Duplicates++
	case "unresolved":
		values.Unresolved++
	case "quarantined":
		values.Quarantined++
	}
}

func beginRun(ctx context.Context, pool *pgxpool.Pool, manifest archivemigration.Manifest, mode string) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO message_archive_migration_runs(source_name,mode,status,source_digest) VALUES($1,$2,'running',$3) RETURNING id`, manifest.SourceName, mode, manifest.Digest[:]).Scan(&id)
	return id, err
}

func beginRunTx(ctx context.Context, tx pgx.Tx, manifest archivemigration.Manifest, mode string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO message_archive_migration_runs(source_name,mode,status,source_digest) VALUES($1,$2,'running',$3) RETURNING id`, manifest.SourceName, mode, manifest.Digest[:]).Scan(&id)
	return id, err
}

func finishRun(ctx context.Context, pool *pgxpool.Pool, id int64, status, code string) error {
	command, err := pool.Exec(ctx, `UPDATE message_archive_migration_runs SET status=$2,error_code=$3,finished_at=clock_timestamp() WHERE id=$1 AND status='running'`, id, status, code)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errReconcileDrift
	}
	return nil
}

func finishRunTx(ctx context.Context, tx pgx.Tx, id int64, status, code string) error {
	command, err := tx.Exec(ctx, `UPDATE message_archive_migration_runs SET status=$2,error_code=$3,finished_at=clock_timestamp() WHERE id=$1 AND status='running'`, id, status, code)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errReconcileDrift
	}
	return nil
}

func applyRow(ctx context.Context, pool *pgxpool.Pool, runID int64, manifest archivemigration.Manifest, resolver historicalResolver, row archivemigration.SourceRow) (string, string, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	outcome, reason, err := applyRowTx(platformpostgres.BindTransaction(ctx, tx), tx, runID, manifest, resolver, row)
	if err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return outcome, reason, nil
}

func applyRowTx(ctx context.Context, tx pgx.Tx, runID int64, manifest archivemigration.Manifest, resolver historicalResolver, row archivemigration.SourceRow) (string, string, error) {
	message, err := manifest.Normalized(row)
	if err != nil {
		return "", "", err
	}
	projection, err := manifest.HistoricalProjection(row)
	if err != nil {
		return "", "", err
	}
	if err = resolver.Resolve(ctx, &message); err != nil {
		return "", "", err
	}
	rowDigest := digestRow(row)
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES($1) ON CONFLICT(corp_scope) DO NOTHING`, manifest.CorpScope); err != nil {
		return "", "", err
	}
	var existingID int64
	existingErr := tx.QueryRow(ctx, `SELECT id FROM message_archive_messages WHERE corp_scope=$1 AND msgid=$2 FOR UPDATE`, manifest.CorpScope, row.MsgID).Scan(&existingID)
	outcome, reason := "", ""
	if errors.Is(existingErr, pgx.ErrNoRows) {
		if existingID, err = insertMessage(ctx, tx, message); err != nil {
			return "", "", err
		}
		for _, participant := range message.Participants {
			if err = insertParticipant(ctx, tx, existingID, participant); err != nil {
				return "", "", err
			}
		}
		for _, media := range message.Media {
			if err = insertMedia(ctx, tx, existingID, media); err != nil {
				return "", "", err
			}
		}
		if err = ensureHistoricalProjection(ctx, tx, existingID, projection); err != nil {
			return "", "", err
		}
		outcome = "inserted"
		if unresolved(message) {
			outcome, reason = "unresolved", "historical_reference_unresolved"
		}
	} else if existingErr != nil {
		return "", "", existingErr
	} else {
		// A repeated row is safe only when every immutable message, participant
		// and media fact that reconcile checks agrees. Comparing just normalized
		// JSON would accept a changed sequence or timestamp, then leave a receipt
		// that reconciliation can never validate.
		equivalenceErr := reconcileMessageTarget(ctx, tx, existingID, message)
		if equivalenceErr == nil {
			equivalenceErr = ensureHistoricalProjection(ctx, tx, existingID, projection)
		}
		switch {
		case equivalenceErr == nil:
			outcome = "duplicate"
		case errors.Is(equivalenceErr, errReconcileDrift):
			outcome, reason, existingID = "quarantined", "source_conflicts_existing_message", 0
		default:
			return "", "", equivalenceErr
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_migration_receipts(migration_run_id,source_row_key,source_digest,source_msgid,source_seq,target_message_id,outcome,reason_code) VALUES($1,$2,$3,$4,$5,NULLIF($6,0),$7,$8)`, runID, row.SourceRowKey, rowDigest[:], row.MsgID, int64(row.Seq), existingID, outcome, reason); err != nil {
		return "", "", err
	}
	return outcome, reason, nil
}

func unresolved(message domain.Message) bool {
	for _, participant := range message.Participants {
		if participant.ResolutionStatus == domain.ResolutionNotFound || participant.ResolutionStatus == domain.ResolutionConflict {
			return true
		}
	}
	return false
}

func insertMessage(ctx context.Context, tx pgx.Tx, message domain.Message) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,action,msgtype,conversation_type,roomid,msgtime_ms,occurred_at,content_text,normalized_payload,provider_payload,recalled_msgid) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12::jsonb,'null'::jsonb),$13) RETURNING id`, message.CorpScope, int64(message.Seq), message.MsgID, message.Action, message.MessageType, message.Conversation, message.RoomID, message.OccurredAt.UnixMilli(), message.OccurredAt, message.ContentText, []byte(message.Normalized), optionalJSON(message.ProviderPayload), message.RecalledMsgID).Scan(&id)
	return id, err
}

func insertParticipant(ctx context.Context, tx pgx.Tx, messageID int64, participant domain.Participant) error {
	var staffID, customerID, identityID any
	if participant.StaffUserID > 0 {
		staffID = participant.StaffUserID
	}
	if participant.CustomerID > 0 {
		customerID = int64(participant.CustomerID)
	}
	if participant.IdentityID > 0 {
		identityID = participant.IdentityID
	}
	_, err := tx.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,staff_user_id,customer_id_at_ingest,identity_id_at_ingest,resolution_status,resolution_reason,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, messageID, participant.Role, participant.ActorType, participant.ProviderValue, participant.ProviderDigest[:], staffID, customerID, identityID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt)
	return err
}

func insertMedia(ctx context.Context, tx pgx.Tx, messageID int64, media domain.MediaReference) error {
	var size any
	if media.HasSize {
		size = media.Size
	}
	_, err := tx.Exec(ctx, `INSERT INTO message_archive_media(message_id,media_kind,provider_file_ref,provider_file_digest,expected_md5,expected_size,status) VALUES($1,$2,$3,$4,$5,$6,'pending')`, messageID, media.Kind, media.FileID, media.Digest[:], media.MD5, size)
	return err
}

// reResolve is an operator-invoked, bounded pass over existing archive-owned
// unresolved participant rows. It reads only pre-existing verified OneID facts
// and Access staff rows. It is intentionally neither a worker nor a schedule.
func reResolve(ctx context.Context, pool *pgxpool.Pool, manifest archivemigration.Manifest, resolver historicalResolver, limit int, afterParticipantID int64) (result, error) {
	if limit < 1 || limit > 5000 || afterParticipantID < 0 {
		return result{}, errInvalidArguments
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	txContext := platformpostgres.BindTransaction(ctx, tx)
	rows, err := tx.Query(txContext, `SELECT participant.id,participant.actor_type,participant.provider_value,participant.resolution_status
		FROM message_archive_participants participant
		JOIN message_archive_messages message ON message.id=participant.message_id
		WHERE message.corp_scope=$1 AND participant.resolution_status='not_found' AND participant.id>$2
		ORDER BY participant.id LIMIT $3 FOR UPDATE SKIP LOCKED`, manifest.CorpScope, afterParticipantID, limit)
	if err != nil {
		return result{}, err
	}
	type candidate struct {
		id       int64
		actor    domain.ActorType
		value    string
		previous domain.ResolutionStatus
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.id, &item.actor, &item.value, &item.previous); err != nil {
			rows.Close()
			return result{}, err
		}
		candidates = append(candidates, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result{}, err
	}
	rows.Close()
	values := result{}
	if len(candidates) > 0 {
		values.NextParticipantID = candidates[len(candidates)-1].id
	}
	for _, candidate := range candidates {
		message := domain.Message{CorpScope: manifest.CorpScope, MsgID: "re-resolve", MessageType: "system", Conversation: "private", OccurredAt: time.Unix(1, 0), Normalized: json.RawMessage(`{}`), Participants: []domain.Participant{{Role: "subject", ActorType: candidate.actor, ProviderValue: candidate.value, ProviderDigest: domain.DigestProviderValue(candidate.value), ResolutionStatus: domain.ResolutionNotFound}}}
		if err = resolver.Resolve(txContext, &message); err != nil {
			return values, err
		}
		participant := message.Participants[0]
		var staffID, customerID any
		if participant.StaffUserID > 0 {
			staffID = participant.StaffUserID
		}
		if participant.CustomerID > 0 {
			customerID = int64(participant.CustomerID)
		}
		if _, err = tx.Exec(txContext, `UPDATE message_archive_participants SET staff_user_id=$2,customer_id_at_ingest=$3,identity_id_at_ingest=NULL,resolution_status=$4,resolution_reason=$5,resolved_at=$6 WHERE id=$1`, candidate.id, staffID, customerID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt); err != nil {
			return values, err
		}
		if _, err = tx.Exec(txContext, `INSERT INTO message_archive_resolution_attempts(participant_id,attempt_source,previous_status,new_status,customer_id,reason_code,occurred_at) VALUES($1,'migration',$2,$3,$4,$5,$6)`, candidate.id, candidate.previous, participant.ResolutionStatus, customerID, participant.ResolutionReason, participant.ResolvedAt); err != nil {
			return values, err
		}
		outcome := "unresolved"
		if participant.ResolutionStatus == domain.ResolutionFound || (participant.ActorType == domain.ActorStaff && participant.StaffUserID > 0) {
			outcome = "resolved"
		}
		values.Rows = append(values.Rows, rowResult{SourceRowKey: fmt.Sprintf("participant:%d", candidate.id), Outcome: outcome, Reason: participant.ResolutionReason})
		if outcome == "unresolved" {
			values.Unresolved++
		} else {
			values.Inserted++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result{}, err
	}
	return values, nil
}

func reconcile(ctx context.Context, pool *pgxpool.Pool, manifest archivemigration.Manifest) (bool, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var sourceRun int64
	err = tx.QueryRow(ctx, `SELECT id FROM message_archive_migration_runs WHERE source_name=$1 AND mode='apply' AND status='succeeded' AND source_digest=$2 ORDER BY id DESC LIMIT 1`, manifest.SourceName, manifest.Digest[:]).Scan(&sourceRun)
	if err != nil {
		return false, err
	}
	var receiptCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM message_archive_migration_receipts WHERE migration_run_id=$1`, sourceRun).Scan(&receiptCount); err != nil || receiptCount != len(manifest.Records) {
		return false, errReconcileDrift
	}
	for _, row := range manifest.SortedRecords() {
		if err = reconcileRow(ctx, tx, sourceRun, manifest, row); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	runID, err := beginRun(ctx, pool, manifest, "reconcile")
	if err != nil {
		return false, err
	}
	if err = finishRun(ctx, pool, runID, "succeeded", ""); err != nil {
		return false, err
	}
	return true, nil
}

type receipt struct {
	digest   []byte
	msgID    string
	seq      int64
	targetID *int64
	outcome  string
	reason   string
}

func reconcileRow(ctx context.Context, tx pgx.Tx, runID int64, manifest archivemigration.Manifest, row archivemigration.SourceRow) error {
	var value receipt
	err := tx.QueryRow(ctx, `SELECT source_digest,source_msgid,source_seq,target_message_id,outcome,reason_code FROM message_archive_migration_receipts WHERE migration_run_id=$1 AND source_row_key=$2`, runID, row.SourceRowKey).Scan(&value.digest, &value.msgID, &value.seq, &value.targetID, &value.outcome, &value.reason)
	if err != nil || len(value.digest) != sha256.Size {
		return errReconcileDrift
	}
	expectedDigest := digestRow(row)
	if subtle.ConstantTimeCompare(value.digest, expectedDigest[:]) != 1 || value.msgID != row.MsgID || value.seq != int64(row.Seq) {
		return errReconcileDrift
	}
	message, err := manifest.Normalized(row)
	if err != nil {
		return errReconcileDrift
	}
	projection, err := manifest.HistoricalProjection(row)
	if err != nil {
		return errReconcileDrift
	}
	switch value.outcome {
	case "inserted", "duplicate", "unresolved":
		if value.targetID == nil || *value.targetID < 1 || !validReceiptReason(value.outcome, value.reason) {
			return errReconcileDrift
		}
		return reconcileTarget(ctx, tx, *value.targetID, message, projection)
	case "quarantined":
		if value.targetID != nil || value.reason != "source_conflicts_existing_message" {
			return errReconcileDrift
		}
		var conflictingMessageID int64
		if err = tx.QueryRow(ctx, `SELECT id FROM message_archive_messages WHERE corp_scope=$1 AND msgid=$2`, manifest.CorpScope, row.MsgID).Scan(&conflictingMessageID); err != nil {
			return errReconcileDrift
		}
		equivalenceErr := reconcileTarget(ctx, tx, conflictingMessageID, message, projection)
		if equivalenceErr == nil {
			return errReconcileDrift
		}
		if errors.Is(equivalenceErr, errReconcileDrift) {
			return nil
		}
		return equivalenceErr
	default:
		return errReconcileDrift
	}
}

func validReceiptReason(outcome, reason string) bool {
	switch outcome {
	case "inserted", "duplicate":
		return reason == ""
	case "unresolved":
		return reason == "historical_reference_unresolved"
	default:
		return false
	}
}

func reconcileTarget(ctx context.Context, tx pgx.Tx, targetID int64, expected domain.Message, projection archivemigration.HistoricalProjection) error {
	if err := reconcileMessageTarget(ctx, tx, targetID, expected); err != nil {
		return err
	}
	return reconcileHistoricalProjection(ctx, tx, targetID, projection)
}

func reconcileMessageTarget(ctx context.Context, tx pgx.Tx, targetID int64, expected domain.Message) error {
	var scope, msgID, action, messageType, conversation, roomID, contentText, recalledMsgID string
	var seq, messageTimeMS int64
	var occurredAt time.Time
	var normalized []byte
	var provider *[]byte
	err := tx.QueryRow(ctx, `SELECT corp_scope,seq,msgid,action,msgtype,conversation_type,roomid,msgtime_ms,occurred_at,content_text,normalized_payload,provider_payload,recalled_msgid FROM message_archive_messages WHERE id=$1`, targetID).Scan(&scope, &seq, &msgID, &action, &messageType, &conversation, &roomID, &messageTimeMS, &occurredAt, &contentText, &normalized, &provider, &recalledMsgID)
	if err != nil || scope != expected.CorpScope || seq != int64(expected.Seq) || msgID != expected.MsgID || action != expected.Action || messageType != expected.MessageType || conversation != expected.Conversation || roomID != expected.RoomID || messageTimeMS != expected.OccurredAt.UnixMilli() || !occurredAt.Equal(expected.OccurredAt) || contentText != expected.ContentText || recalledMsgID != expected.RecalledMsgID || !sameJSON(normalized, expected.Normalized) || !sameJSON(optionalJSONPtr(provider), optionalJSON(expected.ProviderPayload)) {
		return errReconcileDrift
	}
	rows, err := tx.Query(ctx, `SELECT participant_role,actor_type,provider_value,provider_value_digest FROM message_archive_participants WHERE message_id=$1`, targetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := map[string]int{}
	for rows.Next() {
		var role, actor, value string
		var digest []byte
		if err = rows.Scan(&role, &actor, &value, &digest); err != nil || len(digest) != sha256.Size {
			return errReconcileDrift
		}
		actual[role+"\x00"+actor+"\x00"+value+"\x00"+hex.EncodeToString(digest)]++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	expectedParticipants := map[string]int{}
	for _, participant := range expected.Participants {
		expectedParticipants[participant.Role+"\x00"+string(participant.ActorType)+"\x00"+participant.ProviderValue+"\x00"+hex.EncodeToString(participant.ProviderDigest[:])]++
	}
	if !sameCounts(actual, expectedParticipants) {
		return errReconcileDrift
	}
	mediaRows, err := tx.Query(ctx, `SELECT media_kind,provider_file_ref,provider_file_digest,expected_md5,expected_size FROM message_archive_media WHERE message_id=$1`, targetID)
	if err != nil {
		return err
	}
	defer mediaRows.Close()
	actualMedia := map[string]int{}
	for mediaRows.Next() {
		var kind, ref, md5 string
		var digest []byte
		var size *int64
		if err = mediaRows.Scan(&kind, &ref, &digest, &md5, &size); err != nil || len(digest) != sha256.Size {
			return errReconcileDrift
		}
		sizePart := ""
		if size != nil {
			sizePart = fmt.Sprintf("%d", *size)
		}
		actualMedia[kind+"\x00"+ref+"\x00"+hex.EncodeToString(digest)+"\x00"+md5+"\x00"+sizePart]++
	}
	if err = mediaRows.Err(); err != nil {
		return err
	}
	expectedMedia := map[string]int{}
	for _, media := range expected.Media {
		sizePart := ""
		if media.HasSize {
			sizePart = fmt.Sprintf("%d", media.Size)
		}
		expectedMedia[media.Kind+"\x00"+media.FileID+"\x00"+hex.EncodeToString(media.Digest[:])+"\x00"+media.MD5+"\x00"+sizePart]++
	}
	if !sameCounts(actualMedia, expectedMedia) {
		return errReconcileDrift
	}
	return nil
}

func ensureHistoricalProjection(ctx context.Context, tx pgx.Tx, messageID int64, projection archivemigration.HistoricalProjection) error {
	if projection.UnionID == "" && projection.GroupName == "" {
		return nil
	}
	digest := historicalProjectionDigest(projection)
	var unionID, groupName string
	var storedDigest []byte
	err := tx.QueryRow(ctx, `SELECT historical_unionid,historical_group_name,source_projection_digest FROM message_archive_legacy_projections WHERE message_id=$1 FOR UPDATE`, messageID).Scan(&unionID, &groupName, &storedDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO message_archive_legacy_projections(message_id,historical_unionid,historical_group_name,source_projection_digest) VALUES($1,$2,$3,$4)`, messageID, projection.UnionID, projection.GroupName, digest[:])
		return err
	}
	if err != nil || len(storedDigest) != sha256.Size || unionID != projection.UnionID || groupName != projection.GroupName || subtle.ConstantTimeCompare(storedDigest, digest[:]) != 1 {
		return errReconcileDrift
	}
	return nil
}

func reconcileHistoricalProjection(ctx context.Context, tx pgx.Tx, messageID int64, projection archivemigration.HistoricalProjection) error {
	var unionID, groupName string
	var storedDigest []byte
	err := tx.QueryRow(ctx, `SELECT historical_unionid,historical_group_name,source_projection_digest FROM message_archive_legacy_projections WHERE message_id=$1`, messageID).Scan(&unionID, &groupName, &storedDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		if projection.UnionID == "" && projection.GroupName == "" {
			return nil
		}
		return errReconcileDrift
	}
	digest := historicalProjectionDigest(projection)
	if err != nil || len(storedDigest) != sha256.Size || unionID != projection.UnionID || groupName != projection.GroupName || subtle.ConstantTimeCompare(storedDigest, digest[:]) != 1 {
		return errReconcileDrift
	}
	return nil
}

func historicalProjectionDigest(projection archivemigration.HistoricalProjection) [sha256.Size]byte {
	return sha256.Sum256([]byte(projection.UnionID + "\x00" + projection.GroupName))
}

func sameCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, count := range left {
		if right[key] != count {
			return false
		}
	}
	return true
}

func digestRow(row archivemigration.SourceRow) [sha256.Size]byte {
	raw, _ := json.Marshal(row)
	return sha256.Sum256(raw)
}

func optionalJSON(value []byte) []byte {
	if len(value) == 0 {
		return []byte("null")
	}
	return value
}

func optionalJSONPtr(value *[]byte) []byte {
	if value == nil || len(*value) == 0 {
		return []byte("null")
	}
	return *value
}

func sameJSON(left, right []byte) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	return string(rawA) == string(rawB)
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }

var _ identityport.DirectoryIdentityReader = identityquery.NewPostgreSQL()
var _ accessport.Repository = accessstore.NewPostgreSQL()
