package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (s *PostgresStore) PhoneRecovery(ctx context.Context, c identityport.DeclaredPhoneCommand, ref identitydomain.NormalizedReference, apply bool) (identityport.PhoneRecoveryResult, error) {
	r := identityport.PhoneRecoveryResult{}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return r, err
	}
	if s.canonicalRoots == nil || s.phoneVault == nil {
		return r, errStore
	}
	key := sha256.Sum256([]byte(c.IdempotencyKey))
	payload := sha256.Sum256([]byte(strconv.FormatInt(int64(c.CustomerID), 10) + "\x00" + ref.NormalizedValue + "\x00" + c.Source + "\x00" + c.SourceEventID))
	if apply {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "canonical-phone-recovery:"+hex.EncodeToString(key[:])); err != nil {
			return r, persistenceFailure(err)
		}
	}
	var outcome, source, event string
	var prior []byte
	var owner, identity *int64
	err = tx.QueryRow(ctx, `SELECT payload_digest,outcome,source,source_event_id,customer_id,identity_id FROM identity_declared_phone_receipts WHERE key_digest=$1`, key[:]).Scan(&prior, &outcome, &source, &event, &owner, &identity)
	if errors.Is(err, pgx.ErrNoRows) {
		r.Disposition = "no_original_receipt"
		return r, nil
	}
	if err != nil {
		return r, persistenceFailure(err)
	}
	if !bytes.Equal(prior, payload[:]) || source != c.Source || event != c.SourceEventID {
		return r, identityapp.ErrDeclaredPayloadMismatch
	}
	r.Binding = identityport.DeclaredAttachResult{Status: identityport.DeclaredReplayed, ReplayOf: identityport.DeclaredAttachStatus(outcome)}
	if owner != nil {
		r.Binding.CustomerID = customerdomain.CustomerID(*owner)
	}
	if identity != nil {
		r.Binding.IdentityID = *identity
	}
	if outcome != "invalid" {
		r.Disposition = "original:" + outcome
		// Report the actual owner for prior successes, never attach again.
		if outcome == "attached" || outcome == "already_linked" {
			if identity == nil {
				r.Disposition = "original_identity_unavailable"
				return r, nil
			}
			var liveOwner customerdomain.CustomerID
			err = tx.QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE id=$1 AND kind='phone' AND status='active' AND ((scope_key='phone:cn11' AND normalized_value_digest=$2) OR (scope_key='phone:e164' AND normalized_value=$3))`, *identity, s.phoneDigest(ref), "+86"+ref.NormalizedValue).Scan(&liveOwner)
			if errors.Is(err, pgx.ErrNoRows) {
				r.Disposition = "original_identity_unavailable"
				return r, nil
			}
			if err != nil {
				return r, persistenceFailure(err)
			}
			roots, e := s.canonicalRoots.CanonicalCustomerRoots(ctx, []customerdomain.CustomerID{c.CustomerID, liveOwner})
			if e != nil {
				return s.unsafeRecovery(r, e)
			}
			if roots[c.CustomerID] != roots[liveOwner] {
				r.Disposition = "original_identity_drift"
			} else {
				r.Binding.CustomerID = liveOwner
			}
		}
		return r, nil
	}
	roots, err := s.canonicalRoots.CanonicalCustomerRoots(ctx, []customerdomain.CustomerID{c.CustomerID})
	if err != nil {
		return s.unsafeRecovery(r, err)
	}
	path, err := customerPath(ctx, tx, int64(c.CustomerID))
	if err != nil {
		return s.unsafeRecovery(r, err)
	}
	if len(path) < 2 || path[len(path)-1].Status != "active" || path[len(path)-1].MergedTo != 0 {
		r.Disposition = "invalid_without_active_alias"
		return r, nil
	}
	if customerdomain.CustomerID(path[len(path)-1].ID) != roots[c.CustomerID] {
		r.Disposition = "canonical_changed"
		return r, nil
	}
	if apply {
		locked, e := lockCustomerPaths(ctx, tx, []int64{int64(c.CustomerID)})
		if e != nil {
			return s.unsafeRecovery(r, e)
		}
		root, e := locked.activeRootFor(ctx, tx, int64(c.CustomerID))
		if e != nil {
			return s.unsafeRecovery(r, e)
		}
		if customerdomain.CustomerID(root.ID) != roots[c.CustomerID] {
			r.Disposition = "canonical_changed"
			return r, nil
		}
		path, e = customerPath(ctx, tx, int64(c.CustomerID))
		if e != nil {
			return s.unsafeRecovery(r, e)
		}
	}
	mergeIDs, digest, valid, err := auditedPhoneRecoveryPath(ctx, tx, path)
	if err != nil {
		return r, persistenceFailure(err)
	}
	if !valid {
		r.Disposition = "unaudited_alias"
		return r, nil
	}
	root := path[len(path)-1]
	var auditPayload, recoveryKey []byte
	var originalCustomer, target int64
	var auditedMerges []int64
	err = tx.QueryRow(ctx, `SELECT original_payload_digest,original_customer_id,target_customer_id,merge_ids,recovery_key_digest FROM identity_phone_canonical_recoveries WHERE original_key_digest=$1`, key[:]).Scan(&auditPayload, &originalCustomer, &target, &auditedMerges, &recoveryKey)
	if err == nil {
		if !bytes.Equal(auditPayload, payload[:]) || originalCustomer != int64(c.CustomerID) {
			return r, identityapp.ErrDeclaredPayloadMismatch
		}
		if len(mergeIDs) < len(auditedMerges) {
			r.Disposition = "recovery_lineage_changed"
			return r, nil
		}
		for i, id := range auditedMerges {
			if mergeIDs[i] != id {
				r.Disposition = "recovery_lineage_changed"
				return r, nil
			}
		}
		var restoredOutcome string
		var restoredIdentity *int64
		var restoredOwner int64
		err = tx.QueryRow(ctx, `SELECT outcome,identity_id,customer_id FROM identity_declared_phone_receipts WHERE key_digest=$1`, recoveryKey).Scan(&restoredOutcome, &restoredIdentity, &restoredOwner)
		if err != nil {
			return r, persistenceFailure(err)
		}
		if restoredOwner != target {
			return r, identityapp.ErrDeclaredPayloadMismatch
		}
		r.Binding = identityport.DeclaredAttachResult{Status: identityport.DeclaredReplayed, ReplayOf: identityport.DeclaredAttachStatus(restoredOutcome), CustomerID: customerdomain.CustomerID(root.ID)}
		if restoredIdentity != nil {
			r.Binding.IdentityID = *restoredIdentity
		}
		if restoredOutcome == "attached" || restoredOutcome == "already_linked" {
			var belongs bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities WHERE id=$1 AND customer_id=$2 AND kind='phone' AND status='active' AND ((scope_key='phone:cn11' AND normalized_value_digest=$3) OR (scope_key='phone:e164' AND normalized_value=$4)))`, r.Binding.IdentityID, root.ID, s.phoneDigest(ref), "+86"+ref.NormalizedValue).Scan(&belongs)
			if err != nil {
				return r, persistenceFailure(err)
			}
			if !belongs {
				r.Disposition = "recovery_identity_drift"
				return r, nil
			}
		}
		r.Disposition = "replayed:recovery_" + restoredOutcome
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return r, persistenceFailure(err)
	}
	r.Disposition = "recoverable"
	r.Binding.CustomerID = customerdomain.CustomerID(root.ID)
	if !apply {
		var existingOwner int64
		err = tx.QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE kind='phone' AND status='active' AND ((scope_key='phone:cn11' AND normalized_value_digest=$1) OR (scope_key='phone:e164' AND normalized_value=$2))`, s.phoneDigest(ref), "+86"+ref.NormalizedValue).Scan(&existingOwner)
		if err == nil && existingOwner != root.ID {
			r.Disposition = "recoverable_conflict"
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return r, persistenceFailure(err)
		}
		return r, nil
	}
	// One deterministic namespace per original source, not per mutable root.
	recoveryCommand := c
	recoveryCommand.CustomerID = customerdomain.CustomerID(root.ID)
	recoveryCommand.IdempotencyKey = "canonical-phone-recovery:v1:" + hex.EncodeToString(key[:])
	binding, err := s.AttachDeclaredPhone(ctx, recoveryCommand, ref)
	if err != nil {
		return r, err
	}
	if binding.Status != identityport.DeclaredAttached && binding.Status != identityport.DeclaredAlreadyLinked && binding.Status != identityport.DeclaredConflict {
		return r, errStore
	}
	recoveryDigest := sha256.Sum256([]byte(recoveryCommand.IdempotencyKey))
	_, err = tx.Exec(ctx, `INSERT INTO identity_phone_canonical_recoveries(original_key_digest,original_payload_digest,original_customer_id,target_customer_id,target_lineage_version,lineage_digest,merge_ids,recovery_key_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, key[:], payload[:], c.CustomerID, root.ID, root.Lineage, digest[:], mergeIDs, recoveryDigest[:])
	if err != nil {
		return r, persistenceFailure(err)
	}
	r.Binding = binding
	r.Disposition = "recovered"
	if binding.Status == identityport.DeclaredConflict {
		r.Disposition = "recovery_conflict"
	}
	return r, nil
}

func (s *PostgresStore) phoneDigest(ref identitydomain.NormalizedReference) []byte {
	d := s.phoneVault.LookupDigest(ref.NormalizedValue)
	return d[:]
}

func (s *PostgresStore) unsafeRecovery(r identityport.PhoneRecoveryResult, err error) (identityport.PhoneRecoveryResult, error) {
	// Canonical query errors retain safe categories. Storage/network failures
	// still abort, rather than silently classifying a broken database as a skip.
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errCustomerRootChanged) || errors.Is(err, identityquery.ErrInvalidQuery) || errors.Is(err, identityquery.ErrNotFound) {
		r.Disposition = "unsafe_lineage"
		return r, nil
	}
	return r, err
}

func auditedPhoneRecoveryPath(ctx context.Context, tx pgx.Tx, path []customer) ([]int64, [32]byte, bool, error) {
	ids := []int64{}
	text := "canonical-phone-recovery:v1"
	for i, item := range path {
		text += fmt.Sprintf("\x00%d:%d:%d", item.ID, item.Lineage, item.MergedTo)
		if i == len(path)-1 {
			break
		}
		var id int64
		err := tx.QueryRow(ctx, `SELECT m.id FROM customer_merges m JOIN customer_merge_candidates c ON c.id=m.candidate_id JOIN identity_link_evidence e ON e.id=m.evidence_id WHERE m.from_customer_id=$1 AND m.to_customer_id=$2 AND m.from_lineage_version_after=$3 AND m.reversible_status<>'reversed' AND m.reversed_at IS NULL AND c.status='confirmed' AND c.selected_survivor_customer_id=m.to_customer_id AND e.strength='strong'`, item.ID, item.MergedTo, item.Lineage).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, [32]byte{}, false, nil
		}
		if err != nil {
			return nil, [32]byte{}, false, err
		}
		ids = append(ids, id)
		text += fmt.Sprintf(":%d", id)
	}
	return ids, sha256.Sum256([]byte(text)), true, nil
}
