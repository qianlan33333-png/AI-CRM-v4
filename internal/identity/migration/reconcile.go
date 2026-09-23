package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrReceiptReconciliationMismatch means the persisted receipt set no longer
// proves the approved identity-history facts for a commerce import run.
var ErrReceiptReconciliationMismatch = errors.New("identity history receipt reconciliation mismatch")

// SubjectReceiptExpectation is deliberately limited to the immutable receipt
// facts that Identity owns. The composition root obtains CustomerID through
// the trusted Resolve port; this verifier never reads Customer or Order data.
type SubjectReceiptExpectation struct {
	SourceKey     string
	SourceDigest  [32]byte
	CustomerID    int64
	IdentityCount int
}

// QuarantineReceiptExpectation contains only the safe digest evidence that is
// persisted by Identity; it never carries a raw historical identifier.
type QuarantineReceiptExpectation struct {
	SourceKey      string
	SourceDigest   [32]byte
	ReasonCode     string
	EvidenceDigest string
}

type ReceiptReconciliation struct {
	Canonical, Quarantined int64
}

// PostgreSQLReceiptVerifier is a read-only Identity-owned verifier. It must
// not be implemented with RecordSubject or RecordQuarantine: a missing row
// during reconciliation is evidence of drift, never a reason to insert one.
type PostgreSQLReceiptVerifier struct{ Pool *pgxpool.Pool }

func (v PostgreSQLReceiptVerifier) Verify(ctx context.Context, runKey string, subjects []SubjectReceiptExpectation, quarantines []QuarantineReceiptExpectation) (ReceiptReconciliation, error) {
	if v.Pool == nil || runKey == "" || !validReceiptExpectations(subjects, quarantines) {
		return ReceiptReconciliation{}, ErrReceiptReconciliationMismatch
	}
	for _, expected := range subjects {
		if err := v.verifySubject(ctx, runKey, expected); err != nil {
			return ReceiptReconciliation{}, err
		}
	}
	for _, expected := range quarantines {
		if err := v.verifyQuarantine(ctx, runKey, expected); err != nil {
			return ReceiptReconciliation{}, err
		}
	}
	var total int64
	if err := v.Pool.QueryRow(ctx, `SELECT count(*) FROM identity_history_import_receipts WHERE run_key=$1`, runKey).Scan(&total); err != nil {
		return ReceiptReconciliation{}, receiptReconciliationError(err)
	}
	if total != int64(len(subjects)+len(quarantines)) {
		return ReceiptReconciliation{}, ErrReceiptReconciliationMismatch
	}
	return ReceiptReconciliation{Canonical: int64(len(subjects)), Quarantined: int64(len(quarantines))}, nil
}

func validReceiptExpectations(subjects []SubjectReceiptExpectation, quarantines []QuarantineReceiptExpectation) bool {
	keys := make(map[string]struct{}, len(subjects)+len(quarantines))
	for _, expected := range subjects {
		if expected.SourceKey == "" || expected.CustomerID < 1 || expected.IdentityCount < 1 || expected.SourceDigest == ([32]byte{}) {
			return false
		}
		if _, duplicate := keys[expected.SourceKey]; duplicate {
			return false
		}
		keys[expected.SourceKey] = struct{}{}
	}
	for _, expected := range quarantines {
		if expected.SourceKey == "" || expected.ReasonCode == "" || expected.EvidenceDigest == "" || expected.SourceDigest == ([32]byte{}) {
			return false
		}
		if _, duplicate := keys[expected.SourceKey]; duplicate {
			return false
		}
		keys[expected.SourceKey] = struct{}{}
	}
	return true
}

func (v PostgreSQLReceiptVerifier) verifySubject(ctx context.Context, runKey string, expected SubjectReceiptExpectation) error {
	var digest []byte
	var outcome string
	var customerID *int64
	var identityCount int
	var reason *string
	var evidence []byte
	if err := v.Pool.QueryRow(ctx, `SELECT source_digest,outcome,customer_id,identity_count,reason_code,safe_evidence FROM identity_history_import_receipts WHERE run_key=$1 AND source_key=$2`, runKey, expected.SourceKey).Scan(&digest, &outcome, &customerID, &identityCount, &reason, &evidence); err != nil {
		return receiptReconciliationError(err)
	}
	if !bytes.Equal(digest, expected.SourceDigest[:]) || outcome != "canonical" || customerID == nil || *customerID != expected.CustomerID || identityCount != expected.IdentityCount || reason != nil || evidence != nil {
		return ErrReceiptReconciliationMismatch
	}
	return nil
}

func (v PostgreSQLReceiptVerifier) verifyQuarantine(ctx context.Context, runKey string, expected QuarantineReceiptExpectation) error {
	var digest []byte
	var outcome string
	var customerID *int64
	var identityCount int
	var reason *string
	var evidence []byte
	if err := v.Pool.QueryRow(ctx, `SELECT source_digest,outcome,customer_id,identity_count,reason_code,safe_evidence FROM identity_history_import_receipts WHERE run_key=$1 AND source_key=$2`, runKey, expected.SourceKey).Scan(&digest, &outcome, &customerID, &identityCount, &reason, &evidence); err != nil {
		return receiptReconciliationError(err)
	}
	wantEvidence, err := json.Marshal(map[string]string{"source_digest": expected.EvidenceDigest})
	if err != nil {
		return err
	}
	var actual any
	if json.Unmarshal(evidence, &actual) != nil {
		return ErrReceiptReconciliationMismatch
	}
	actualEvidence, err := json.Marshal(actual)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, expected.SourceDigest[:]) || outcome != "quarantined" || customerID != nil || identityCount != 0 || reason == nil || *reason != expected.ReasonCode || !bytes.Equal(actualEvidence, wantEvidence) {
		return ErrReceiptReconciliationMismatch
	}
	return nil
}

func receiptReconciliationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReceiptReconciliationMismatch
	}
	return err
}
