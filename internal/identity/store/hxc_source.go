package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func hxcReceiptKey(subject identityport.HXCSubject, result identityport.HXCSubjectResult) [32]byte {
	return sha256.Sum256([]byte("hxc\x00" + hex.EncodeToString(subject.SubjectDigest[:]) + "\x00" + subject.RuleVersion + "\x00" + hex.EncodeToString(subject.PayloadDigest[:]) + "\x00" + string(result.Disposition) + "\x00" + string(result.MatchedBy) + "\x00" + strconv.FormatInt(int64(result.CustomerID), 10) + "\x00" + strconv.FormatInt(result.MergeCandidateID, 10)))
}

func (store *PostgresStore) ReplayHXCResolution(ctx context.Context, subject identityport.HXCSubject) (identityport.HXCSubjectResult, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.HXCSubjectResult{}, false, err
	}
	var result identityport.HXCSubjectResult
	var customerID, conflictID, candidateID *int64
	var payloadDigest []byte
	err = tx.QueryRow(ctx, `SELECT r.disposition,r.matched_by,r.reason_code,r.customer_id,r.conflict_id,r.merge_candidate_id,r.payload_digest
		FROM identity_source_resolution_receipts r JOIN identity_source_subjects s ON s.id=r.subject_id
		WHERE s.source_system='hxc' AND s.subject_digest=$1 AND r.rule_version=$2 AND r.payload_digest=$3
		ORDER BY r.id DESC LIMIT 1`, subject.SubjectDigest[:], subject.RuleVersion, subject.PayloadDigest[:]).Scan(
		&result.Disposition, &result.MatchedBy, &result.Reason, &customerID, &conflictID, &candidateID, &payloadDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityport.HXCSubjectResult{}, false, nil
	}
	if err != nil {
		return identityport.HXCSubjectResult{}, false, persistenceFailure(err)
	}
	if string(payloadDigest) != string(subject.PayloadDigest[:]) {
		return identityport.HXCSubjectResult{}, false, identityapp.ErrDeclaredPayloadMismatch
	}
	if customerID != nil {
		result.CustomerID = customerdomain.CustomerID(*customerID)
	}
	if conflictID != nil {
		result.ConflictID = *conflictID
	}
	if candidateID != nil {
		result.MergeCandidateID = *candidateID
		var status string
		if statusErr := tx.QueryRow(ctx, `SELECT status FROM customer_merge_candidates WHERE id=$1`, *candidateID).Scan(&status); statusErr != nil {
			return identityport.HXCSubjectResult{}, false, persistenceFailure(statusErr)
		} else if status == "confirmed" {
			return identityport.HXCSubjectResult{}, false, nil
		}
	}
	return result, true, nil
}

func (store *PostgresStore) PersistHXCResolution(ctx context.Context, subject identityport.HXCSubject, result identityport.HXCSubjectResult) (identityport.HXCSubjectResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.HXCSubjectResult{}, err
	}
	if store.observationVault == nil || store.phoneVault == nil || subject.RuleVersion == "" {
		return identityport.HXCSubjectResult{}, errStore
	}
	if replay, found, replayErr := store.ReplayHXCResolution(ctx, subject); replayErr != nil {
		return identityport.HXCSubjectResult{}, replayErr
	} else if found {
		replay.Position, replay.Replayed = subject.Position, true
		return replay, nil
	}
	status := string(result.Disposition)
	if result.Disposition == identityport.HXCUnmatched {
		status = "pending"
	}
	var customerID any
	if result.CustomerID > 0 {
		customerID = int64(result.CustomerID)
	}
	var subjectID int64
	err = tx.QueryRow(ctx, `INSERT INTO identity_source_subjects(source_system,subject_digest,status,customer_id,matched_by,reason_code,latest_payload_digest,source_updated_at)
		VALUES('hxc',$1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(source_system,subject_digest) DO UPDATE SET status=EXCLUDED.status,customer_id=EXCLUDED.customer_id,
		matched_by=EXCLUDED.matched_by,reason_code=EXCLUDED.reason_code,latest_payload_digest=EXCLUDED.latest_payload_digest,
		source_updated_at=EXCLUDED.source_updated_at,last_seen_at=CURRENT_TIMESTAMP,missed_complete_snapshots=0,version=identity_source_subjects.version+1
		RETURNING id`, subject.SubjectDigest[:], status, customerID, result.MatchedBy, result.Reason, subject.PayloadDigest[:], subject.SourceUpdatedAt).Scan(&subjectID)
	if err != nil {
		return identityport.HXCSubjectResult{}, persistenceFailure(err)
	}
	unionAssurance := "declared"
	if subject.UnionIDVerified {
		unionAssurance = "verified"
	}
	unionValue := subject.UnionID
	if result.Reason == identityport.HXCReasonInvalidUnionID {
		unionValue = ""
	}
	if err = store.syncHXCObservation(ctx, tx, subjectID, "unionid", subject.UnionIDScope, unionValue, unionAssurance, subject.SourceUpdatedAt); err != nil {
		return identityport.HXCSubjectResult{}, err
	}
	if err = store.syncHXCObservation(ctx, tx, subjectID, "phone", "phone:cn11", subject.Phone, "declared", subject.SourceUpdatedAt); err != nil {
		return identityport.HXCSubjectResult{}, err
	}
	if result.Disposition == identityport.HXCConflict {
		result.ConflictID, err = store.upsertHXCConflict(ctx, tx, subjectID, subject, result)
		if err != nil {
			return identityport.HXCSubjectResult{}, err
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE identity_source_conflicts SET status='resolved',
			resolution_code=CASE WHEN EXISTS (SELECT 1 FROM customer_merge_candidates mc WHERE mc.id=identity_source_conflicts.merge_candidate_id AND mc.status='confirmed') THEN 'resolved_by_merge' ELSE 'source_reconciled' END,
			resolved_by='system',
			resolved_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP,version=version+1 WHERE subject_id=$1 AND status='open'`, subjectID)
		if err != nil {
			return identityport.HXCSubjectResult{}, persistenceFailure(err)
		}
	}
	key := hxcReceiptKey(subject, result)
	var conflictID, candidateID any
	if result.ConflictID > 0 {
		conflictID = result.ConflictID
	}
	if result.MergeCandidateID > 0 {
		candidateID = result.MergeCandidateID
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_source_resolution_receipts(key_digest,payload_digest,subject_id,rule_version,disposition,matched_by,reason_code,customer_id,conflict_id,merge_candidate_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(key_digest) DO NOTHING`, key[:], subject.PayloadDigest[:], subjectID, subject.RuleVersion, result.Disposition, result.MatchedBy, result.Reason, customerID, conflictID, candidateID)
	if err != nil {
		return identityport.HXCSubjectResult{}, persistenceFailure(err)
	}
	return result, nil
}

func (store *PostgresStore) syncHXCObservation(ctx context.Context, tx pgx.Tx, subjectID int64, kind, scope, value, assurance string, sourceUpdatedAt time.Time) error {
	if value == "" {
		_, err := tx.Exec(ctx, `UPDATE identity_source_observations SET status='retired',last_seen_at=CURRENT_TIMESTAMP,version=version+1
			WHERE subject_id=$1 AND kind=$2 AND status='active'`, subjectID, kind)
		if err != nil {
			return persistenceFailure(err)
		}
		return nil
	}
	var digest [32]byte
	var ciphertext []byte
	var err error
	display := "stored"
	if kind == "phone" {
		digest = store.phoneVault.LookupDigest(value)
		ciphertext, err = store.phoneVault.Encrypt(value)
		display = identitysecure.MaskPhone(value)
	} else {
		digest = store.observationVault.LookupDigest(kind, scope, value)
		ciphertext, err = store.observationVault.Encrypt(kind, scope, value)
	}
	if err != nil {
		return errStore
	}
	var currentID int64
	var currentDigest []byte
	err = tx.QueryRow(ctx, `SELECT id,lookup_digest FROM identity_source_observations WHERE subject_id=$1 AND kind=$2 AND status='active' FOR UPDATE`, subjectID, kind).Scan(&currentID, &currentDigest)
	if err == nil && string(currentDigest) == string(digest[:]) {
		_, err = tx.Exec(ctx, `UPDATE identity_source_observations SET last_seen_at=CURRENT_TIMESTAMP,source_updated_at=$2,version=version+1 WHERE id=$1`, currentID, sourceUpdatedAt)
		if err != nil {
			return persistenceFailure(err)
		}
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return persistenceFailure(err)
	}
	if currentID > 0 {
		if _, err = tx.Exec(ctx, `UPDATE identity_source_observations SET status='retired',last_seen_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, currentID); err != nil {
			return persistenceFailure(err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_source_observations(subject_id,kind,scope_key,lookup_digest,ciphertext,display_value,key_version,assurance,status,source_updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'active',$9)`, subjectID, kind, scope, digest[:], ciphertext, display, identitysecure.ObservationKeyVersion, assurance, sourceUpdatedAt)
	if err != nil {
		return persistenceFailure(err)
	}
	return nil
}

func (store *PostgresStore) upsertHXCConflict(ctx context.Context, tx pgx.Tx, subjectID int64, subject identityport.HXCSubject, result identityport.HXCSubjectResult) (int64, error) {
	left, right := result.UnionCustomerID, result.PhoneCustomerID
	if left > right && right > 0 {
		left, right = right, left
	}
	var leftValue, rightValue, candidate any
	if left > 0 {
		leftValue = int64(left)
	}
	if right > 0 {
		rightValue = int64(right)
	}
	if result.MergeCandidateID > 0 {
		candidate = result.MergeCandidateID
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO identity_source_conflicts(subject_id,reason_code,left_customer_id,right_customer_id,merge_candidate_id,evidence_digest)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(subject_id,reason_code) WHERE status='open' DO UPDATE SET left_customer_id=EXCLUDED.left_customer_id,
		right_customer_id=EXCLUDED.right_customer_id,merge_candidate_id=EXCLUDED.merge_candidate_id,evidence_digest=EXCLUDED.evidence_digest,
		updated_at=CURRENT_TIMESTAMP,version=identity_source_conflicts.version+1 RETURNING id`,
		subjectID, result.Reason, leftValue, rightValue, candidate, subject.PayloadDigest[:]).Scan(&id)
	if err != nil {
		return 0, persistenceFailure(err)
	}
	return id, nil
}

func (store *PostgresStore) IgnoreHXCSourceConflict(ctx context.Context, command identityport.IgnoreHXCSourceConflictCommand) (identityport.HXCSourceConflict, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.HXCSourceConflict{}, false, err
	}
	if command.ConflictID < 1 || command.ExpectedVersion < 1 || command.Operator == "" || command.IdempotencyKey == "" || !validHXCIgnoreReason(command.ReasonCode) {
		return identityport.HXCSourceConflict{}, false, identityapp.ErrInvalidLinkCommand
	}
	key := sha256.Sum256([]byte(command.IdempotencyKey))
	payload := sha256.Sum256([]byte(strconv.FormatInt(command.ConflictID, 10) + "\x00" + strconv.FormatInt(command.ExpectedVersion, 10) + "\x00" + command.ReasonCode))
	var prior []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest FROM identity_source_conflict_actions WHERE key_digest=$1`, key[:]).Scan(&prior)
	if err == nil {
		if string(prior) != string(payload[:]) {
			return identityport.HXCSourceConflict{}, false, identityapp.ErrDeclaredPayloadMismatch
		}
		item, loadErr := loadHXCConflict(ctx, tx, command.ConflictID)
		return item, true, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identityport.HXCSourceConflict{}, false, persistenceFailure(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE identity_source_conflicts SET status='ignored',resolution_code=$2,resolved_by=$3,resolved_at=CURRENT_TIMESTAMP,
		updated_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open' AND version=$4`, command.ConflictID, command.ReasonCode, command.Operator, command.ExpectedVersion)
	if err != nil {
		return identityport.HXCSourceConflict{}, false, persistenceFailure(err)
	}
	if tag.RowsAffected() != 1 {
		return identityport.HXCSourceConflict{}, false, identityapp.ErrConcurrentIdentityChange
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_source_conflict_actions(conflict_id,key_digest,payload_digest,action,reason_code,actor,outcome)
		VALUES($1,$2,$3,'ignore',$4,$5,'ignored')`, command.ConflictID, key[:], payload[:], command.ReasonCode, command.Operator)
	if err != nil {
		return identityport.HXCSourceConflict{}, false, persistenceFailure(err)
	}
	item, err := loadHXCConflict(ctx, tx, command.ConflictID)
	return item, false, err
}

func validHXCIgnoreReason(value string) bool {
	switch value {
	case "not_same_person", "shared_phone", "source_data_error", "accepted_risk":
		return true
	default:
		return false
	}
}

func loadHXCConflict(ctx context.Context, tx pgx.Tx, id int64) (identityport.HXCSourceConflict, error) {
	var item identityport.HXCSourceConflict
	var subjectDigest, evidence []byte
	var left, right, candidate *int64
	err := tx.QueryRow(ctx, `SELECT c.id,s.subject_digest,c.reason_code,c.left_customer_id,c.right_customer_id,c.merge_candidate_id,c.evidence_digest,
		c.status,c.version,c.created_at,c.resolved_at FROM identity_source_conflicts c JOIN identity_source_subjects s ON s.id=c.subject_id WHERE c.id=$1`, id).Scan(
		&item.ID, &subjectDigest, &item.Reason, &left, &right, &candidate, &evidence, &item.Status, &item.Version, &item.CreatedAt, &item.ResolvedAt)
	if err != nil {
		return identityport.HXCSourceConflict{}, persistenceFailure(err)
	}
	item.SubjectRef = "HXC-" + hex.EncodeToString(subjectDigest[:6])
	item.EvidenceDigest = hex.EncodeToString(evidence)
	if left != nil {
		item.LeftCustomerID = customerdomain.CustomerID(*left)
	}
	if right != nil {
		item.RightCustomerID = customerdomain.CustomerID(*right)
	}
	if candidate != nil {
		item.MergeCandidateID = *candidate
	}
	return item, nil
}

func (store *PostgresStore) CompleteHXCSnapshot(ctx context.Context, seen [][32]byte) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	digests := make([][]byte, 0, len(seen))
	for _, digest := range seen {
		digests = append(digests, append([]byte(nil), digest[:]...))
	}
	rows, err := tx.Query(ctx, `UPDATE identity_source_subjects
		SET missed_complete_snapshots=LEAST(2,missed_complete_snapshots+1),
			status=CASE WHEN missed_complete_snapshots>=1 THEN 'retired' ELSE status END,
			customer_id=CASE WHEN missed_complete_snapshots>=1 THEN NULL ELSE customer_id END,
			matched_by=CASE WHEN missed_complete_snapshots>=1 THEN 'none' ELSE matched_by END,
			reason_code=CASE WHEN missed_complete_snapshots>=1 THEN 'source_retired' ELSE reason_code END,
			version=version+1
		WHERE source_system='hxc' AND NOT(subject_digest=ANY($1::bytea[]))
		RETURNING id,status`, digests)
	if err != nil {
		return persistenceFailure(err)
	}
	retired := make([]int64, 0)
	for rows.Next() {
		var id int64
		var status string
		if scanErr := rows.Scan(&id, &status); scanErr != nil {
			rows.Close()
			return persistenceFailure(scanErr)
		}
		if status == "retired" {
			retired = append(retired, id)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return persistenceFailure(err)
	}
	rows.Close()
	if len(retired) == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE identity_source_observations SET status='retired',last_seen_at=CURRENT_TIMESTAMP,version=version+1
		WHERE subject_id=ANY($1::bigint[]) AND status='active'`, retired)
	if err != nil {
		return persistenceFailure(err)
	}
	_, err = tx.Exec(ctx, `UPDATE identity_source_conflicts SET status='resolved',resolution_code='source_retired',resolved_by='system',
		resolved_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP,version=version+1
		WHERE subject_id=ANY($1::bigint[]) AND status='open'`, retired)
	if err != nil {
		return persistenceFailure(err)
	}
	return nil
}
