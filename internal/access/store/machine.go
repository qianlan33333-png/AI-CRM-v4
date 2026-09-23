package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

var _ accessport.MachineRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalVerificationRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalAuditRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalBatchRepository = (*PostgreSQL)(nil)

func (*PostgreSQL) MachineClientByID(ctx context.Context, clientID string, lock bool) (domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, err
	}
	query := machineClientSelect + ` WHERE c.client_id=$1`
	if lock {
		query += ` FOR UPDATE OF c`
	}
	client, err := scanMachineClient(database.QueryRow(ctx, query, clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.MachineClient{}, err
	}
	client.Capabilities, err = machineCapabilities(ctx, database, client.ID)
	return client, err
}

func (*PostgreSQL) ListMachineClients(ctx context.Context) ([]domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, machineClientSelect+` ORDER BY c.created_at DESC, c.id DESC`)
	if err != nil {
		return nil, err
	}
	clients := make([]domain.MachineClient, 0)
	for rows.Next() {
		client, scanErr := scanMachineClient(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		clients = append(clients, client)
	}
	// pgx transactions use one connection. Close the outer result before
	// loading grants so a multi-row management list never re-enters a busy
	// connection with an active cursor.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range clients {
		capabilities, capabilityErr := machineCapabilities(ctx, database, clients[index].ID)
		if capabilityErr != nil {
			return nil, capabilityErr
		}
		clients[index].Capabilities = capabilities
	}
	return clients, nil
}

func (*PostgreSQL) BeginHistoricalMachineImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var digest []byte
	var sourceSystem, sourceRevision string
	var snapshotAt time.Time
	var clientCount, auditCount int
	err = database.QueryRow(ctx, `SELECT manifest_digest,source_system,source_revision,snapshot_at,client_count,audit_count FROM access_machine_import_batches WHERE import_run_id=$1 FOR UPDATE`, batch.ImportRunID).Scan(&digest, &sourceSystem, &sourceRevision, &snapshotAt, &clientCount, &auditCount)
	if err == nil {
		if !bytes.Equal(digest, batch.ManifestDigest[:]) || sourceSystem != batch.SourceSystem || sourceRevision != batch.SourceRevision || !snapshotAt.Equal(batch.SnapshotAt.UTC()) || clientCount != batch.ClientCount || auditCount != batch.AuditCount {
			return false, domain.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err = database.Exec(ctx, `INSERT INTO access_machine_import_batches(import_run_id,source_system,source_revision,manifest_digest,snapshot_at,client_count,audit_count) VALUES($1,$2,$3,$4,$5,$6,$7)`, batch.ImportRunID, batch.SourceSystem, batch.SourceRevision, batch.ManifestDigest[:], batch.SnapshotAt.UTC(), batch.ClientCount, batch.AuditCount); err != nil {
		return false, mapDatabaseError(err)
	}
	return false, nil
}

func (*PostgreSQL) VerifyHistoricalMachineImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	var digest []byte
	var sourceSystem, sourceRevision string
	var snapshotAt time.Time
	var clientCount, auditCount int
	err = database.QueryRow(ctx, `SELECT manifest_digest,source_system,source_revision,snapshot_at,client_count,audit_count FROM access_machine_import_batches WHERE import_run_id=$1`, batch.ImportRunID).Scan(&digest, &sourceSystem, &sourceRevision, &snapshotAt, &clientCount, &auditCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, batch.ManifestDigest[:]) || sourceSystem != batch.SourceSystem || sourceRevision != batch.SourceRevision || !snapshotAt.Equal(batch.SnapshotAt.UTC()) || clientCount != batch.ClientCount || auditCount != batch.AuditCount {
		return domain.ErrConflict
	}
	return nil
}

// ImportHistoricalMachineClient writes a global source-record receipt plus a
// receipt for this sealed snapshot. A second factual snapshot may reference
// the same source row, but may never recreate or silently alter its client.
func (*PostgreSQL) ImportHistoricalMachineClient(ctx context.Context, input accessport.HistoricalMachineImportInput, client domain.MachineClient) (domain.MachineClient, bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, false, err
	}
	if err = lockHistoricalMachineSource(ctx, database, input); err != nil {
		return domain.MachineClient{}, false, err
	}
	if batch, batchErr := historicalMachineBatchReceipt(ctx, database, input, true); batchErr == nil {
		if !bytes.Equal(batch.digest, input.SourceRowDigest[:]) {
			return domain.MachineClient{}, false, domain.ErrConflict
		}
		stored, readErr := historicalMachineGlobalReceipt(ctx, database, input, false)
		if readErr != nil || !historicalMachineReceiptMatches(stored, input) || stored.outcome != "reissue_required" || stored.reason != "" || stored.clientID == nil {
			if readErr != nil {
				return domain.MachineClient{}, false, readErr
			}
			return domain.MachineClient{}, false, domain.ErrConflict
		}
		result, readErr := historicalMachineClient(ctx, database, *stored.clientID)
		return result, true, readErr
	} else if !errors.Is(batchErr, pgx.ErrNoRows) {
		return domain.MachineClient{}, false, batchErr
	}

	stored, readErr := historicalMachineGlobalReceipt(ctx, database, input, true)
	if readErr == nil {
		if !historicalMachineReceiptMatches(stored, input) || stored.outcome != "reissue_required" || stored.reason != "" || stored.clientID == nil {
			return domain.MachineClient{}, false, domain.ErrConflict
		}
		if err = recordHistoricalMachineBatchReceipt(ctx, database, input, true); err != nil {
			return domain.MachineClient{}, false, err
		}
		result, readErr := historicalMachineClient(ctx, database, *stored.clientID)
		return result, true, readErr
	}
	if !errors.Is(readErr, pgx.ErrNoRows) {
		return domain.MachineClient{}, false, readErr
	}

	created, err := createMachineClient(ctx, database, client)
	if err != nil {
		return domain.MachineClient{}, false, err
	}
	if err = recordHistoricalMachineGlobalReceipt(ctx, database, input, created.ID, "reissue_required", ""); err != nil {
		return domain.MachineClient{}, false, err
	}
	if err = recordHistoricalMachineBatchReceipt(ctx, database, input, false); err != nil {
		return domain.MachineClient{}, false, err
	}
	return created, false, nil
}

// RecordHistoricalMachineExclusion records a source fact V3 cannot host
// without widening authority. It follows the same cross-batch drift contract
// as a replacement credential, so an excluded fact can never be activated by
// importing it through another snapshot.
func (*PostgreSQL) RecordHistoricalMachineExclusion(ctx context.Context, input accessport.HistoricalMachineImportInput, reason string) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	if err = lockHistoricalMachineSource(ctx, database, input); err != nil {
		return false, err
	}
	if batch, batchErr := historicalMachineBatchReceipt(ctx, database, input, true); batchErr == nil {
		if !bytes.Equal(batch.digest, input.SourceRowDigest[:]) {
			return false, domain.ErrConflict
		}
		stored, readErr := historicalMachineGlobalReceipt(ctx, database, input, false)
		if readErr != nil {
			return false, readErr
		}
		if !historicalMachineReceiptMatches(stored, input) || stored.outcome != "excluded" || stored.reason != reason || stored.clientID != nil {
			return false, domain.ErrConflict
		}
		return true, nil
	} else if !errors.Is(batchErr, pgx.ErrNoRows) {
		return false, batchErr
	}

	stored, readErr := historicalMachineGlobalReceipt(ctx, database, input, true)
	if readErr == nil {
		if !historicalMachineReceiptMatches(stored, input) || stored.outcome != "excluded" || stored.reason != reason || stored.clientID != nil {
			return false, domain.ErrConflict
		}
		return true, recordHistoricalMachineBatchReceipt(ctx, database, input, true)
	}
	if !errors.Is(readErr, pgx.ErrNoRows) {
		return false, readErr
	}
	if err = recordHistoricalMachineGlobalReceipt(ctx, database, input, 0, "excluded", reason); err != nil {
		return false, err
	}
	return false, recordHistoricalMachineBatchReceipt(ctx, database, input, false)
}

// VerifyHistoricalMachineClient verifies the current snapshot receipt and its
// global source fact. Both are needed: a batch alone would not catch a changed
// later source record, and a global fact alone would not prove this snapshot.
func (*PostgreSQL) VerifyHistoricalMachineClient(ctx context.Context, input accessport.HistoricalMachineImportInput) (domain.MachineClient, string, string, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	batch, err := historicalMachineBatchReceipt(ctx, database, input, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, "", "", domain.ErrNotFound
	}
	if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	if !bytes.Equal(batch.digest, input.SourceRowDigest[:]) {
		return domain.MachineClient{}, "", "", domain.ErrConflict
	}
	stored, err := historicalMachineGlobalReceipt(ctx, database, input, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, "", "", domain.ErrNotFound
	}
	if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	if !historicalMachineReceiptMatches(stored, input) {
		return domain.MachineClient{}, "", "", domain.ErrConflict
	}
	if stored.outcome == "excluded" && stored.clientID == nil {
		return domain.MachineClient{}, stored.outcome, stored.reason, nil
	}
	if stored.outcome != "reissue_required" || stored.reason != "" || stored.clientID == nil {
		return domain.MachineClient{}, "", "", domain.ErrConflict
	}
	result, err := historicalMachineClient(ctx, database, *stored.clientID)
	return result, stored.outcome, stored.reason, err
}

type historicalMachineReceipt struct {
	digest             []byte
	ownerScopeDigest   []byte
	ownerMappingStatus string
	clientID           *int64
	outcome            string
	reason             string
}

type historicalMachineBatchRecord struct{ digest []byte }

func lockHistoricalMachineSource(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineImportInput) error {
	_, err := database.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.SourceSystem+"|"+input.SourceScope+"|"+input.SourceRowID)
	return err
}

func historicalMachineBatchReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineImportInput, lock bool) (historicalMachineBatchRecord, error) {
	query := `SELECT source_row_digest FROM access_machine_import_batch_receipts WHERE import_run_id=$1 AND source_system=$2 AND source_scope=$3 AND source_row_id=$4`
	if lock {
		query += ` FOR UPDATE`
	}
	var result historicalMachineBatchRecord
	err := database.QueryRow(ctx, query, input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceRowID).Scan(&result.digest)
	return result, err
}

func historicalMachineGlobalReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineImportInput, lock bool) (historicalMachineReceipt, error) {
	query := `SELECT source_row_digest,source_owner_scope_digest,owner_scope_mapping_status,machine_client_id,outcome,reason_code
		FROM access_machine_import_receipts WHERE source_system=$1 AND source_scope=$2 AND source_row_id=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	var result historicalMachineReceipt
	err := database.QueryRow(ctx, query, input.SourceSystem, input.SourceScope, input.SourceRowID).Scan(&result.digest, &result.ownerScopeDigest, &result.ownerMappingStatus, &result.clientID, &result.outcome, &result.reason)
	return result, err
}

func historicalMachineReceiptMatches(receipt historicalMachineReceipt, input accessport.HistoricalMachineImportInput) bool {
	return bytes.Equal(receipt.digest, input.SourceRowDigest[:]) && bytes.Equal(receipt.ownerScopeDigest, input.SourceOwnerScopeDigest[:]) && receipt.ownerMappingStatus == input.OwnerScopeMappingStatus
}

func recordHistoricalMachineGlobalReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineImportInput, clientID int64, outcome, reason string) error {
	var machineClientID any
	if clientID > 0 {
		machineClientID = clientID
	}
	_, err := database.Exec(ctx, `INSERT INTO access_machine_import_receipts
		(import_run_id,source_system,source_scope,source_row_id,source_row_digest,source_owner_scope_digest,owner_scope_mapping_status,
		 source_client_id,source_principal_id,source_principal_type,source_enabled,source_auth_version,machine_client_id,outcome,reason_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceRowID, input.SourceRowDigest[:], input.SourceOwnerScopeDigest[:], input.OwnerScopeMappingStatus,
		input.SourceClientID, input.PrincipalID, input.PrincipalType, input.SourceEnabled, input.SourceAuthVersion, machineClientID, outcome, reason)
	return mapDatabaseError(err)
}

func recordHistoricalMachineBatchReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineImportInput, replayed bool) error {
	_, err := database.Exec(ctx, `INSERT INTO access_machine_import_batch_receipts
		(import_run_id,source_system,source_scope,source_row_id,source_row_digest,replayed)
		VALUES($1,$2,$3,$4,$5,$6)`, input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceRowID, input.SourceRowDigest[:], replayed)
	return mapDatabaseError(err)
}

func historicalMachineClient(ctx context.Context, database pgx.Tx, clientID int64) (domain.MachineClient, error) {
	result, err := scanMachineClient(database.QueryRow(ctx, machineClientSelect+` WHERE c.id=$1`, clientID))
	if err != nil {
		return domain.MachineClient{}, err
	}
	result.Capabilities, err = machineCapabilities(ctx, database, result.ID)
	return result, err
}

func (*PostgreSQL) ImportHistoricalMachineAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	if err = lockHistoricalAuditSource(ctx, database, input); err != nil {
		return false, err
	}
	if batch, batchErr := historicalAuditBatchReceipt(ctx, database, input, true); batchErr == nil {
		if !bytes.Equal(batch.digest, input.SourceRowDigest[:]) {
			return false, domain.ErrConflict
		}
		stored, readErr := historicalAuditFact(ctx, database, input, false)
		if readErr != nil || !historicalAuditMatches(stored, input) {
			if readErr != nil {
				return false, readErr
			}
			return false, domain.ErrConflict
		}
		return true, nil
	} else if !errors.Is(batchErr, pgx.ErrNoRows) {
		return false, batchErr
	}
	stored, readErr := historicalAuditFact(ctx, database, input, true)
	if readErr == nil {
		if !historicalAuditMatches(stored, input) {
			return false, domain.ErrConflict
		}
		return true, recordHistoricalAuditBatchReceipt(ctx, database, input, true)
	}
	if !errors.Is(readErr, pgx.ErrNoRows) {
		return false, readErr
	}
	if err = recordHistoricalAuditFact(ctx, database, input); err != nil {
		return false, err
	}
	return false, recordHistoricalAuditBatchReceipt(ctx, database, input, false)
}

func (repository *PostgreSQL) VerifyHistoricalMachineAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	batch, err := historicalAuditBatchReceipt(ctx, database, input, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(batch.digest, input.SourceRowDigest[:]) {
		return domain.ErrConflict
	}
	stored, err := historicalAuditFact(ctx, database, input, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !historicalAuditMatches(stored, input) {
		return domain.ErrConflict
	}
	return nil
}

type historicalAuditReceipt struct {
	digest, before, after                  []byte
	operator, action, targetType, targetID string
	occurred                               time.Time
}

type historicalAuditBatchRecord struct{ digest []byte }

func lockHistoricalAuditSource(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineAuditInput) error {
	_, err := database.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.SourceSystem+"|"+input.SourceScope+"|"+fmt.Sprintf("%d", input.SourceAuditID))
	return err
}

func historicalAuditBatchReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineAuditInput, lock bool) (historicalAuditBatchRecord, error) {
	query := `SELECT source_row_digest FROM access_machine_historical_audit_batch_receipts WHERE import_run_id=$1 AND source_system=$2 AND source_scope=$3 AND source_audit_id=$4`
	if lock {
		query += ` FOR UPDATE`
	}
	var result historicalAuditBatchRecord
	err := database.QueryRow(ctx, query, input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceAuditID).Scan(&result.digest)
	return result, err
}

func historicalAuditFact(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineAuditInput, lock bool) (historicalAuditReceipt, error) {
	query := `SELECT source_row_digest,before_payload_digest,after_payload_digest,source_operator,source_action,source_target_type,source_target_id,occurred_at
		FROM access_machine_historical_audit_facts WHERE source_system=$1 AND source_scope=$2 AND source_audit_id=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	var result historicalAuditReceipt
	err := database.QueryRow(ctx, query, input.SourceSystem, input.SourceScope, input.SourceAuditID).Scan(&result.digest, &result.before, &result.after, &result.operator, &result.action, &result.targetType, &result.targetID, &result.occurred)
	return result, err
}

func historicalAuditMatches(receipt historicalAuditReceipt, input accessport.HistoricalMachineAuditInput) bool {
	return bytes.Equal(receipt.digest, input.SourceRowDigest[:]) && bytes.Equal(receipt.before, input.BeforeDigest[:]) && bytes.Equal(receipt.after, input.AfterDigest[:]) && receipt.operator == input.Operator && receipt.action == input.Action && receipt.targetType == input.TargetType && receipt.targetID == input.TargetID && receipt.occurred.Equal(input.OccurredAt.UTC())
}

func recordHistoricalAuditFact(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineAuditInput) error {
	_, err := database.Exec(ctx, `INSERT INTO access_machine_historical_audit_facts
		(import_run_id,source_system,source_scope,source_audit_id,source_row_digest,source_operator,source_action,source_target_type,source_target_id,before_payload_digest,after_payload_digest,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceAuditID, input.SourceRowDigest[:], input.Operator, input.Action, input.TargetType, input.TargetID, input.BeforeDigest[:], input.AfterDigest[:], input.OccurredAt.UTC())
	return mapDatabaseError(err)
}

func recordHistoricalAuditBatchReceipt(ctx context.Context, database pgx.Tx, input accessport.HistoricalMachineAuditInput, replayed bool) error {
	_, err := database.Exec(ctx, `INSERT INTO access_machine_historical_audit_batch_receipts
		(import_run_id,source_system,source_scope,source_audit_id,source_row_digest,replayed)
		VALUES($1,$2,$3,$4,$5,$6)`, input.ImportRunID, input.SourceSystem, input.SourceScope, input.SourceAuditID, input.SourceRowDigest[:], replayed)
	return mapDatabaseError(err)
}

func (*PostgreSQL) CreateMachineClient(ctx context.Context, client domain.MachineClient) (domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, err
	}
	return createMachineClient(ctx, database, client)
}

func createMachineClient(ctx context.Context, database pgx.Tx, client domain.MachineClient) (domain.MachineClient, error) {
	err := database.QueryRow(ctx, `
		INSERT INTO access_machine_clients
			(client_id, display_name, purpose, secret_hash, credential_hint, audiences, scopes,
			 allowed_cidrs, corp_id, owner_scope, token_ttl_seconds, expires_at, enabled, reissue_required, auth_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::cidr[],$9,$10::jsonb,$11,$12,$13,$14,$15)
		RETURNING id, created_at, updated_at`,
		client.ClientID, client.DisplayName, client.Purpose, client.SecretHash, client.CredentialHint,
		client.Audiences, client.Scopes, client.AllowedCIDRs, client.CorpID, client.OwnerScope.JSON(), client.TokenTTLSeconds, client.ExpiresAt,
		client.Enabled, client.ReissueRequired, client.AuthVersion,
	).Scan(&client.ID, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		return domain.MachineClient{}, mapDatabaseError(err)
	}
	if err = replaceMachineGrants(ctx, database, client.ID, client.Capabilities); err != nil {
		return domain.MachineClient{}, err
	}
	return client, nil
}

func (*PostgreSQL) ReplaceMachineClient(ctx context.Context, client domain.MachineClient) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `
		UPDATE access_machine_clients SET display_name=$2, purpose=$3, secret_hash=$4,
			credential_hint=$5, audiences=$6, scopes=$7, allowed_cidrs=$8::cidr[], corp_id=$9, owner_scope=$10::jsonb,
			token_ttl_seconds=$11, expires_at=$12, enabled=$13, reissue_required=$14,
			auth_version=$15, updated_at=clock_timestamp()
		WHERE id=$1`,
		client.ID, client.DisplayName, client.Purpose, client.SecretHash, client.CredentialHint,
		client.Audiences, client.Scopes, client.AllowedCIDRs, client.CorpID, client.OwnerScope.JSON(), client.TokenTTLSeconds, client.ExpiresAt,
		client.Enabled, client.ReissueRequired, client.AuthVersion,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return replaceMachineGrants(ctx, database, client.ID, client.Capabilities)
}

func (*PostgreSQL) SetMachineClientLastUsed(ctx context.Context, id int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `UPDATE access_machine_clients SET last_used_at=$2 WHERE id=$1`, id, now)
	return err
}

func (*PostgreSQL) AppendMachineAudit(ctx context.Context, audit domain.MachineAudit) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `INSERT INTO access_machine_audit
		(machine_client_id, actor_admin_user_id, action, outcome, details, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, audit.MachineClientID, audit.ActorAdminID,
		audit.Action, audit.Outcome, audit.Details, audit.CreatedAt)
	return err
}

// ListMachineAudit returns only safe, Access-owned audit facts for one caller.
// The calling app verifies the client ID before this read so an empty page never
// hides an invalid identifier.
func (*PostgreSQL) ListMachineAudit(ctx context.Context, clientID int64, limit int) ([]accessport.MachineAuditEntry, error) {
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `SELECT actor_admin_user_id, action, outcome, details, created_at
		FROM access_machine_audit
		WHERE machine_client_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]accessport.MachineAuditEntry, 0)
	for rows.Next() {
		var entry accessport.MachineAuditEntry
		var details []byte
		if err := rows.Scan(&entry.ActorAdminUserID, &entry.Action, &entry.Outcome, &details, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.Details = append(entry.Details[:0], details...)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

const machineClientSelect = `SELECT c.id, c.client_id, c.display_name, c.purpose, c.secret_hash,
	c.credential_hint, c.audiences, c.scopes, COALESCE(c.allowed_cidrs::text[], '{}'), c.corp_id, c.owner_scope,
	c.token_ttl_seconds, c.expires_at, c.enabled, c.reissue_required, c.auth_version,
	c.last_used_at, c.created_at, c.updated_at
	FROM access_machine_clients c`

type machineRow interface {
	Scan(...any) error
}

func scanMachineClient(row machineRow) (domain.MachineClient, error) {
	var client domain.MachineClient
	var ownerScope []byte
	err := row.Scan(&client.ID, &client.ClientID, &client.DisplayName, &client.Purpose, &client.SecretHash,
		&client.CredentialHint, &client.Audiences, &client.Scopes, &client.AllowedCIDRs, &client.CorpID, &ownerScope,
		&client.TokenTTLSeconds, &client.ExpiresAt, &client.Enabled, &client.ReissueRequired,
		&client.AuthVersion, &client.LastUsedAt, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		return client, err
	}
	client.OwnerScope, err = domain.NormalizeOwnerScope(ownerScope)
	return client, err
}

func machineCapabilities(ctx context.Context, database pgx.Tx, clientID int64) ([]string, error) {
	rows, err := database.Query(ctx, `SELECT capability FROM access_machine_client_grants WHERE machine_client_id=$1 ORDER BY capability`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	capabilities := make([]string, 0)
	for rows.Next() {
		var capability string
		if err := rows.Scan(&capability); err != nil {
			return nil, err
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities, rows.Err()
}

func replaceMachineGrants(ctx context.Context, database pgx.Tx, clientID int64, capabilities []string) error {
	if _, err := database.Exec(ctx, `DELETE FROM access_machine_client_grants WHERE machine_client_id=$1`, clientID); err != nil {
		return err
	}
	for _, capability := range capabilities {
		if _, err := database.Exec(ctx, `INSERT INTO access_machine_client_grants (machine_client_id, capability) VALUES ($1,$2)`, clientID, capability); err != nil {
			return err
		}
	}
	return nil
}
