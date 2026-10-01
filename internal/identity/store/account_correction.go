package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const correctionSource = "wecom.account_correction"
const correctionReason = "reviewed_distinct_wecom_accounts"

// CorrectDistinctWeComAccounts only corrects an hxc UnionID attached to the
// wrong one of two verified WeCom accounts. Historical ownership stays on the
// retired identity. It never merges roots or rewrites business history.
// The caller appends platform audit in this same UoW before committing.
func (store *PostgresStore) CorrectDistinctWeComAccounts(ctx context.Context, cmd identityport.AccountCorrectionCommand) (identityport.AccountCorrectionResult, error) {
	var result identityport.AccountCorrectionResult
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return result, err
	}
	p := cmd.Plan
	refs := []identitydomain.NormalizedReference{}
	for _, f := range []identitydomain.VerifiedFact{cmd.LeftExternal, cmd.RightExternal, cmd.LeftUnion, cmd.RightUnion} {
		if !f.Valid() {
			return result, identityapp.ErrInvalidLinkCommand
		}
		refs = append(refs, f.Reference())
	}
	if p.RunKey == "" || p.Operator == "" || p.LeftCustomerID < 1 || p.RightCustomerID < 1 || p.LeftCustomerID == p.RightCustomerID || p.LeftVersion < 1 || p.RightVersion < 1 || p.WrongIdentityID < 1 || p.WrongIdentityVersion < 1 || p.CandidateID < 1 || p.CandidateVersion < 1 || p.HXCSubjectID < 1 || p.HXCSubjectVersion < 1 || len(p.ConflictIDs) == 0 || store.observationVault == nil ||
		refs[0].Kind != identitydomain.KindWeComExternalUserID || refs[1].Kind != refs[0].Kind || refs[0].Scope != refs[1].Scope || refs[0].NormalizedValue == refs[1].NormalizedValue ||
		refs[2].Kind != identitydomain.KindUnionID || refs[3].Kind != refs[2].Kind || refs[2].Scope != refs[3].Scope || refs[2].NormalizedValue == refs[3].NormalizedValue {
		return result, identityapp.ErrInvalidLinkCommand
	}
	for _, id := range p.ConflictIDs {
		if id < 1 {
			return result, identityapp.ErrInvalidLinkCommand
		}
	}
	raw, _ := json.Marshal(struct {
		Plan identityport.AccountCorrectionPlan
		Refs []identitydomain.NormalizedReference
	}{p, refs})
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, correctionSource+":"+p.RunKey); err != nil {
		return result, persistenceFailure(err)
	}
	var priorFingerprint string
	var priorResult []byte
	err = tx.QueryRow(ctx, `SELECT metadata_json->>'fingerprint',metadata_json->'result' FROM identity_link_evidence WHERE source=$1 AND source_event_id=$2 AND evidence_type=$3`, correctionSource, p.RunKey, correctionReason).Scan(&priorFingerprint, &priorResult)
	if err == nil {
		if priorFingerprint != fingerprint {
			return result, identityapp.ErrDeclaredPayloadMismatch
		}
		if err = json.Unmarshal(priorResult, &result); err != nil {
			return result, errStore
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, persistenceFailure(err)
	}
	ordered := append([]identitydomain.NormalizedReference(nil), refs...)
	sort.Slice(ordered, func(i, j int) bool { return identityLockValue(ordered[i]) < identityLockValue(ordered[j]) })
	for _, ref := range ordered {
		if err = lockIdentityKey(ctx, tx, ref); err != nil {
			return result, persistenceFailure(err)
		}
	}
	if err = lockCustomers(ctx, tx, []int64{p.LeftCustomerID, p.RightCustomerID}); err != nil {
		return result, identityapp.ErrConcurrentIdentityChange
	}
	for _, root := range []struct{ id, version int64 }{{p.LeftCustomerID, p.LeftVersion}, {p.RightCustomerID, p.RightVersion}} {
		current, e := activeCustomerLocked(ctx, tx, root.id)
		if e != nil || current.Version != root.version {
			return result, identityapp.ErrConcurrentIdentityChange
		}
	}
	for i, customer := range []int64{p.LeftCustomerID, p.RightCustomerID} {
		var matches bool
		if err = tx.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(normalized_value=$4) FROM customer_identities WHERE customer_id=$1 AND kind=$2 AND scope_key=$3 AND status='active' AND assurance='verified'`, customer, string(refs[i].Kind), refs[i].Scope, refs[i].NormalizedValue).Scan(&matches); err != nil {
			return result, persistenceFailure(err)
		}
		if !matches {
			return result, identityapp.ErrConcurrentIdentityChange
		}
	}
	var wrongMatches bool
	err = tx.QueryRow(ctx, `SELECT customer_id=$2 AND kind='unionid' AND scope_key=$3 AND normalized_value=$4 AND source='hxc' AND status='active' AND assurance='verified' AND version=$5 FROM customer_identities WHERE id=$1 FOR UPDATE`, p.WrongIdentityID, p.RightCustomerID, refs[2].Scope, refs[2].NormalizedValue, p.WrongIdentityVersion).Scan(&wrongMatches)
	if err != nil || !wrongMatches {
		return result, identityapp.ErrConcurrentIdentityChange
	}
	var unexpected int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM customer_identities WHERE status='active' AND kind='unionid' AND scope_key=$1 AND id<>$2 AND (customer_id IN($3,$4) OR normalized_value IN($5,$6))`, refs[2].Scope, p.WrongIdentityID, p.LeftCustomerID, p.RightCustomerID, refs[2].NormalizedValue, refs[3].NormalizedValue).Scan(&unexpected)
	if err != nil {
		return result, persistenceFailure(err)
	}
	if unexpected != 0 {
		return result, identityapp.ErrConcurrentIdentityChange
	}
	var candidateMatches bool
	err = tx.QueryRow(ctx, `SELECT status='open' AND version=$2 AND left_customer_id=$3 AND right_customer_id=$4 AND left_customer_version=$5 AND right_customer_version=$6 FROM customer_merge_candidates WHERE id=$1 FOR UPDATE`, p.CandidateID, p.CandidateVersion, p.LeftCustomerID, p.RightCustomerID, p.LeftVersion, p.RightVersion).Scan(&candidateMatches)
	if err != nil || !candidateMatches {
		return result, identityapp.ErrConcurrentIdentityChange
	}
	var subjectDigest, payloadDigest []byte
	var rule string
	lookup := store.observationVault.LookupDigest("unionid", refs[2].Scope, refs[2].NormalizedValue)
	err = tx.QueryRow(ctx, `SELECT s.subject_digest,s.latest_payload_digest,r.rule_version FROM identity_source_subjects s
 JOIN identity_source_observations o ON o.subject_id=s.id AND o.kind='unionid' AND o.status='active' AND o.scope_key=$4 AND o.lookup_digest=$5 AND o.assurance='verified'
 JOIN LATERAL(SELECT rule_version FROM identity_source_resolution_receipts WHERE subject_id=s.id AND payload_digest=s.latest_payload_digest ORDER BY id DESC LIMIT 1)r ON true
 WHERE s.id=$1 AND s.status='matched' AND s.customer_id=$2 AND s.version=$3 FOR UPDATE OF s`, p.HXCSubjectID, p.RightCustomerID, p.HXCSubjectVersion, refs[2].Scope, lookup[:]).Scan(&subjectDigest, &payloadDigest, &rule)
	if err != nil || len(subjectDigest) != 32 || len(payloadDigest) != 32 {
		return result, identityapp.ErrConcurrentIdentityChange
	}
	// All preconditions passed; each following mutation still rolls back with
	// the caller's audit on any error.
	if _, err = tx.Exec(ctx, `UPDATE customer_identities SET status='retired',version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, p.WrongIdentityID); err != nil {
		return result, persistenceFailure(err)
	}
	ids := []*int64{&result.LeftUnionID, &result.RightUnionID}
	for i, customer := range []int64{p.LeftCustomerID, p.RightCustomerID} {
		ref := refs[i+2]
		err = tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,source_event_id,normalizer_version,verified_at) VALUES($1,'unionid',$2,$3,'verified',$4,$5,$6,CURRENT_TIMESTAMP) RETURNING id`, customer, ref.Scope, ref.NormalizedValue, correctionSource, p.RunKey, ref.NormalizerVersion).Scan(ids[i])
		if err != nil {
			return result, persistenceFailure(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, customer); err != nil {
			return result, persistenceFailure(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by=$2,resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, p.CandidateID, p.Operator); err != nil {
		return result, persistenceFailure(err)
	}
	for _, id := range p.ConflictIDs {
		tag, e := tx.Exec(ctx, `UPDATE customer_identity_conflicts c SET status='resolved',resolved_by=$4,resolved_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP,version=version+1 FROM identity_link_evidence e WHERE c.id=$1 AND c.status='open' AND c.left_customer_id=$2 AND c.right_customer_id=$2 AND c.reason='single_value_strong_namespace' AND c.evidence_id=e.id AND e.evidence_type='verified_wecom_unionid_contact_pair' AND e.source='wecom.directory_sync' AND e.left_customer_id=$3`, id, p.RightCustomerID, p.RightCustomerID, p.Operator)
		if e != nil {
			return result, persistenceFailure(e)
		}
		if tag.RowsAffected() != 1 {
			return result, identityapp.ErrConcurrentIdentityChange
		}
		result.ResolvedConflicts = append(result.ResolvedConflicts, id)
	}
	if _, err = tx.Exec(ctx, `UPDATE identity_source_subjects SET customer_id=$2,matched_by='unionid',reason_code=$3,version=version+1,last_seen_at=CURRENT_TIMESTAMP WHERE id=$1`, p.HXCSubjectID, p.LeftCustomerID, correctionReason); err != nil {
		return result, persistenceFailure(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE identity_source_observations SET customer_identity_id=$2,version=version+1 WHERE subject_id=$1 AND kind='unionid' AND status='active'`, p.HXCSubjectID, result.LeftUnionID); err != nil {
		return result, persistenceFailure(err)
	}
	subject := identityport.HXCSubject{RuleVersion: rule}
	copy(subject.SubjectDigest[:], subjectDigest)
	copy(subject.PayloadDigest[:], payloadDigest)
	hxcResult := identityport.HXCSubjectResult{Disposition: identityport.HXCMatched, MatchedBy: identityport.HXCMatchUnionID, Reason: identityport.HXCReason(correctionReason), CustomerID: customerdomain.CustomerID(p.LeftCustomerID)}
	key := hxcReceiptKey(subject, hxcResult)
	if _, err = tx.Exec(ctx, `INSERT INTO identity_source_resolution_receipts(key_digest,payload_digest,subject_id,rule_version,disposition,matched_by,reason_code,customer_id) VALUES($1,$2,$3,$4,'matched','unionid',$5,$6)`, key[:], payloadDigest, p.HXCSubjectID, rule, correctionReason, p.LeftCustomerID); err != nil {
		return result, persistenceFailure(err)
	}
	result.RetiredIdentityID = p.WrongIdentityID
	result.RejectedCandidate = p.CandidateID
	result.ReviewedSubject = p.HXCSubjectID
	// Persist the receipt with its allocated evidence ID in one transaction.
	err = tx.QueryRow(ctx, `INSERT INTO identity_link_evidence(left_customer_id,right_customer_id,left_identity_id,right_identity_id,evidence_type,strength,source,source_event_id,evidence_digest,policy_version,operator) VALUES($1,$2,$3,$4,$5,'strong',$6,$7,$8,'wecom-account-correction-v1',$9) RETURNING id`, p.LeftCustomerID, p.RightCustomerID, result.LeftUnionID, result.RightUnionID, correctionReason, correctionSource, p.RunKey, fingerprint, p.Operator).Scan(&result.EvidenceID)
	if err != nil {
		return result, persistenceFailure(err)
	}
	metadata, _ := json.Marshal(map[string]any{"fingerprint": fingerprint, "result": result, "reviewed_subject_digest": hex.EncodeToString(subjectDigest)})
	_, err = tx.Exec(ctx, `UPDATE identity_link_evidence SET metadata_json=$2 WHERE id=$1`, result.EvidenceID, metadata)
	if err != nil {
		return result, persistenceFailure(err)
	}
	return result, nil
}

// ReviewedHXCAccount applies only to the exact reviewed source subject and
// active provider-verified UnionID. Sharing a phone cannot reunify these roots.
func (store *PostgresStore) ReviewedHXCAccount(ctx context.Context, subject identityport.HXCSubject) (identityport.HXCSubjectResult, bool, error) {
	var result identityport.HXCSubjectResult
	if !subject.UnionIDVerified || subject.UnionID == "" || subject.ConflictReason == identityport.HXCReasonDuplicateUnionID {
		return result, false, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return result, false, err
	}
	err = tx.QueryRow(ctx, `SELECT i.customer_id FROM customer_identities i
 JOIN identity_link_evidence e ON e.left_customer_id=i.customer_id AND e.left_identity_id=i.id
 JOIN customer_identities other ON other.id=e.right_identity_id AND other.customer_id=e.right_customer_id AND other.status='active' AND other.assurance='verified' AND other.kind='unionid' AND other.scope_key=i.scope_key AND other.normalized_value<>i.normalized_value
 JOIN customers l ON l.id=e.left_customer_id AND l.status='active' JOIN customers r ON r.id=e.right_customer_id AND r.status='active'
 WHERE i.kind='unionid' AND i.scope_key=$1 AND i.normalized_value=$2 AND i.status='active' AND i.assurance='verified'
 AND e.source=$3 AND e.evidence_type=$4 AND e.metadata_json->>'reviewed_subject_digest'=$5
 ORDER BY e.id DESC LIMIT 1`, subject.UnionIDScope, subject.UnionID, correctionSource, correctionReason, hex.EncodeToString(subject.SubjectDigest[:])).Scan(&result.CustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, persistenceFailure(err)
	}
	result.Position = subject.Position
	result.Disposition = identityport.HXCMatched
	result.MatchedBy = identityport.HXCMatchUnionID
	result.Reason = identityport.HXCReason(correctionReason)
	result.UnionCustomerID = result.CustomerID
	return result, true, nil
}
