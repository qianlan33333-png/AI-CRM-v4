// Package migration contains the one-off, receipt-only PostgreSQL adapter for
// authoritative historical OneID imports. Runtime modules do not import it.
package migration

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrReceiptConflict = errors.New("identity history import receipt conflict")

type PostgreSQLReceipts struct{}

func (PostgreSQLReceipts) RecordSubject(ctx context.Context, runKey, sourceKey string, digest [32]byte, customerID int64, identityCount int) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var storedDigest []byte
	var storedCustomer int64
	var storedCount int
	err = tx.QueryRow(ctx, `SELECT source_digest,customer_id,identity_count FROM identity_history_import_receipts WHERE run_key=$1 AND source_key=$2`, runKey, sourceKey).Scan(&storedDigest, &storedCustomer, &storedCount)
	if err == nil {
		if string(storedDigest) != string(digest[:]) || storedCustomer != customerID || storedCount != identityCount {
			return ErrReceiptConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_history_import_receipts(run_key,source_key,source_digest,outcome,customer_id,identity_count) VALUES($1,$2,$3,'canonical',$4,$5)`, runKey, sourceKey, digest[:], customerID, identityCount)
	return err
}

func (PostgreSQLReceipts) RecordQuarantine(ctx context.Context, runKey, sourceKey string, digest [32]byte, reason, evidenceDigest string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(map[string]string{"source_digest": evidenceDigest})
	if err != nil {
		return err
	}
	var storedDigest []byte
	var storedReason string
	var storedEvidence []byte
	err = tx.QueryRow(ctx, `SELECT source_digest,reason_code,safe_evidence FROM identity_history_import_receipts WHERE run_key=$1 AND source_key=$2`, runKey, sourceKey).Scan(&storedDigest, &storedReason, &storedEvidence)
	if err == nil {
		var left, right any
		_ = json.Unmarshal(storedEvidence, &left)
		_ = json.Unmarshal(evidence, &right)
		leftJSON, _ := json.Marshal(left)
		rightJSON, _ := json.Marshal(right)
		if string(storedDigest) != string(digest[:]) || storedReason != reason || string(leftJSON) != string(rightJSON) {
			return ErrReceiptConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_history_import_receipts(run_key,source_key,source_digest,outcome,reason_code,safe_evidence) VALUES($1,$2,$3,'quarantined',$4,$5)`, runKey, sourceKey, digest[:], reason, evidence)
	return err
}
