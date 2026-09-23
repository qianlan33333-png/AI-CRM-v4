// Command migrate-phone-identities performs the bounded, one-time phone
// identity migration. It never connects to the source production host: the
// operator must supply an already exported, checksummed minimal snapshot.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const schemaVersion = "phone-identity-import/v1"

var mainlandPhone = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

type snapshotRow struct {
	SchemaVersion   string `json:"schema_version"`
	SourceRowID     string `json:"source_row_id"`
	CorpID          string `json:"corp_id"`
	ExternalUserID  string `json:"external_userid"`
	Phone           string `json:"phone"`
	SourceUpdatedAt string `json:"source_updated_at"`
}

type classifiedRow struct {
	row          snapshotRow
	receiptRowID string
	digest       [32]byte
	phone        string
	customerID   customerdomain.CustomerID
	outcome      string
	errorCode    string
}

type counts struct {
	Input          int64 `json:"input"`
	Ready          int64 `json:"ready,omitempty"`
	Attached       int64 `json:"attached"`
	AlreadyLinked  int64 `json:"already_linked"`
	Conflict       int64 `json:"conflict"`
	Unresolved     int64 `json:"unresolved"`
	Invalid        int64 `json:"invalid"`
	DuplicateInput int64 `json:"duplicate_input"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "phone identity migration failed:", publicError(err))
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "inspect", "inspect|dry-run|apply|reconcile|rollback")
	input := flag.String("input", "", "versioned .jsonl or .csv snapshot")
	manifest := flag.String("manifest-sha256", "", "required lowercase SHA-256 of the snapshot")
	corpID := flag.String("corp-id", "", "expected WeCom corp id")
	runKey := flag.String("run-key", "", "stable import run key; defaults to manifest digest")
	confirmApply := flag.Bool("confirm-apply", false, "required for apply")
	confirmRollback := flag.Bool("confirm-rollback", false, "required for rollback")
	flag.Parse()
	if *mode != "inspect" && *mode != "dry-run" && *mode != "apply" && *mode != "reconcile" && *mode != "rollback" {
		return errors.New("invalid mode")
	}
	if *runKey == "" && *manifest != "" {
		*runKey = "phone-import:" + (*manifest)[:min(32, len(*manifest))]
	}
	if *mode == "reconcile" || *mode == "rollback" {
		if *runKey == "" {
			return errors.New("run-key is required")
		}
		return withDatabase(func(ctx context.Context, pool *platformpostgres.Pool, uow platformport.UnitOfWork) error {
			if *mode == "reconcile" {
				return reconcile(ctx, uow, *runKey)
			}
			if !*confirmRollback {
				return errors.New("confirm-rollback is required")
			}
			return rollback(ctx, uow, *runKey)
		})
	}
	if *input == "" || *manifest == "" || *corpID == "" {
		return errors.New("input, manifest-sha256 and corp-id are required")
	}
	rows, digest, err := loadSnapshot(*input, *manifest)
	if err != nil {
		return err
	}
	classified := syntaxClassify(rows, *corpID)
	if *mode == "inspect" {
		return printResult(*mode, 0, summarize(classified))
	}
	return withDatabase(func(ctx context.Context, pool *platformpostgres.Pool, uow platformport.UnitOfWork) error {
		phoneDataKey, keyErr := platformconfig.IdentityPhoneDataKey()
		if keyErr != nil {
			return keyErr
		}
		vault, vaultErr := identitysecure.NewPhoneVault(phoneDataKey)
		if vaultErr != nil {
			return errors.New("identity phone data key is required")
		}
		queries := identityquery.NewPostgreSQL(vault)
		if err = classifyAgainstTarget(ctx, uow, queries, classified, *corpID); err != nil {
			return err
		}
		if *mode == "dry-run" {
			return printResult(*mode, 0, summarize(classified))
		}
		if !*confirmApply {
			return errors.New("confirm-apply is required")
		}
		runID, applied, createErr := createRun(ctx, uow, *runKey, digest, int64(len(rows)))
		if createErr != nil {
			return createErr
		}
		if applied {
			return reconcile(ctx, uow, *runKey)
		}
		oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}
		projection := customerstore.NewPostgreSQL()
		if err = applyRows(ctx, uow, oneID, projection, runID, classified); err != nil {
			return err
		}
		if err = finishRun(ctx, uow, runID, summarize(classified)); err != nil {
			return err
		}
		return reconcile(ctx, uow, *runKey)
	})
}

func loadSnapshot(path, expected string) ([]snapshotRow, [32]byte, error) {
	var zero [32]byte
	if filepath.Ext(path) != ".jsonl" && filepath.Ext(path) != ".csv" {
		return nil, zero, errors.New("snapshot must be jsonl or csv")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, zero, errors.New("snapshot unavailable")
	}
	digest := sha256.Sum256(data)
	if len(expected) != 64 || hex.EncodeToString(digest[:]) != expected {
		return nil, zero, errors.New("manifest digest mismatch")
	}
	var rows []snapshotRow
	if filepath.Ext(path) == ".jsonl" {
		rows, err = parseJSONL(data)
	} else {
		rows, err = parseCSV(data)
	}
	if err != nil || len(rows) == 0 {
		return nil, zero, errors.New("invalid snapshot")
	}
	return rows, digest, nil
}

func parseJSONL(data []byte) ([]snapshotRow, error) {
	rows := []snapshotRow{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var row snapshotRow
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&row) != nil {
			return nil, errors.New("invalid jsonl row")
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

func parseCSV(data []byte) ([]snapshotRow, error) {
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.ReuseRecord = false
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return nil, errors.New("invalid csv")
	}
	expected := []string{"schema_version", "source_row_id", "corp_id", "external_userid", "phone", "source_updated_at"}
	if len(records[0]) != len(expected) {
		return nil, errors.New("invalid csv header")
	}
	for i := range expected {
		if records[0][i] != expected[i] {
			return nil, errors.New("invalid csv header")
		}
	}
	rows := make([]snapshotRow, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != 6 {
			return nil, errors.New("invalid csv row")
		}
		rows = append(rows, snapshotRow{record[0], record[1], record[2], record[3], record[4], record[5]})
	}
	return rows, nil
}

func syntaxClassify(rows []snapshotRow, corpID string) []*classifiedRow {
	result := make([]*classifiedRow, len(rows))
	seenRows := map[string]struct{}{}
	seenPair := map[string]struct{}{}
	for index, row := range rows {
		raw, _ := json.Marshal(row)
		item := &classifiedRow{row: row, receiptRowID: row.SourceRowID, digest: sha256.Sum256(raw), outcome: "ready"}
		result[index] = item
		_, updatedAtErr := time.Parse(time.RFC3339, row.SourceUpdatedAt)
		if row.SchemaVersion != schemaVersion || row.SourceRowID == "" || row.CorpID != corpID || strings.TrimSpace(row.ExternalUserID) != row.ExternalUserID || row.ExternalUserID == "" || updatedAtErr != nil || strings.ContainsAny(row.SourceRowID+row.CorpID+row.ExternalUserID, "\x00\r\n") {
			if item.receiptRowID == "" {
				item.receiptRowID = fmt.Sprintf("invalid:%d:%x", index, item.digest[:6])
			}
			item.outcome, item.errorCode = "invalid", "invalid_source_row"
			continue
		}
		phone, err := normalizePhone(row.Phone)
		if err != nil {
			item.outcome, item.errorCode = "invalid", "invalid_phone"
			continue
		}
		item.phone = phone
		pair := row.CorpID + "\x00" + row.ExternalUserID + "\x00" + phone
		if _, exists := seenRows[row.SourceRowID]; exists {
			item.receiptRowID = fmt.Sprintf("duplicate:%d:%x", index, item.digest[:6])
			item.outcome, item.errorCode = "duplicate_input", "duplicate_source_row"
			continue
		}
		seenRows[row.SourceRowID] = struct{}{}
		if _, exists := seenPair[pair]; exists {
			item.outcome, item.errorCode = "duplicate_input", "duplicate_identity_phone"
			continue
		}
		seenPair[pair] = struct{}{}
	}
	return result
}

func classifyAgainstTarget(ctx context.Context, uow platformport.UnitOfWork, identities identityport.DirectoryIdentityReader, rows []*classifiedRow, corpID string) error {
	phoneOwners := map[string]map[customerdomain.CustomerID]struct{}{}
	for _, item := range rows {
		if item.outcome != "ready" {
			continue
		}
		err := uow.Within(ctx, func(txContext context.Context) error {
			var found bool
			var queryErr error
			item.customerID, found, queryErr = identities.VerifiedWeComCustomer(txContext, corpID, item.row.ExternalUserID)
			if queryErr != nil {
				return queryErr
			}
			if !found {
				item.outcome, item.errorCode = "unresolved_external_identity", "external_identity_missing"
			}
			return nil
		})
		if err != nil {
			return err
		}
		if item.outcome == "ready" {
			if phoneOwners[item.phone] == nil {
				phoneOwners[item.phone] = map[customerdomain.CustomerID]struct{}{}
			}
			phoneOwners[item.phone][item.customerID] = struct{}{}
		}
	}
	for _, item := range rows {
		if item.outcome != "ready" {
			continue
		}
		if len(phoneOwners[item.phone]) > 1 {
			item.outcome, item.errorCode = "conflict", "input_phone_multiple_customers"
			continue
		}
		err := uow.Within(ctx, func(txContext context.Context) error {
			owner, found, queryErr := identities.CustomerForPhone(txContext, item.phone)
			if queryErr != nil {
				return queryErr
			}
			if !found {
				item.outcome = "attached"
			} else if owner == item.customerID {
				item.outcome = "already_linked"
			} else {
				item.outcome, item.errorCode = "conflict", "phone_owned_by_other_customer"
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func applyRows(ctx context.Context, uow platformport.UnitOfWork, oneID identityapp.OneIDService, projection customerstore.PostgreSQL, runID int64, rows []*classifiedRow) error {
	for _, item := range rows {
		err := uow.Within(ctx, func(txContext context.Context) error {
			if item.outcome != "attached" && item.outcome != "already_linked" {
				return insertReceipt(txContext, runID, item)
			}
			result, attachErr := oneID.AttachDeclaredPhoneToCustomer(txContext, identityport.DeclaredPhoneCommand{CustomerID: item.customerID, Phone: item.phone, Source: "phone_import", SourceEventID: fmt.Sprintf("phone-import:%d:%x", runID, item.digest[:8]), IdempotencyKey: fmt.Sprintf("phone-import:%d:%x", runID, item.digest[:8])})
			if attachErr != nil {
				return attachErr
			}
			switch result.Status {
			case identityport.DeclaredAttached:
				item.outcome = "attached"
			case identityport.DeclaredAlreadyLinked:
				item.outcome = "already_linked"
			case identityport.DeclaredConflict:
				item.outcome, item.errorCode = "conflict", "concurrent_phone_conflict"
			case identityport.DeclaredInvalid:
				item.outcome, item.errorCode = "invalid", "inactive_customer"
			case identityport.DeclaredReplayed:
				item.outcome = string(result.ReplayOf)
				item.customerID = result.CustomerID
				return insertReceipt(txContext, runID, item)
			default:
				return errors.New("unexpected attach result")
			}
			if item.outcome == "attached" || item.outcome == "already_linked" {
				now := time.Now().UTC()
				if updateErr := projection.UpdateDirectoryPhone(txContext, item.customerID, maskPhone(item.phone), identitydomain.AssuranceDeclared, runID, now); updateErr != nil {
					return updateErr
				}
				if item.outcome == "attached" {
					if timelineErr := projection.AppendTimeline(txContext, customerport.TimelineEvent{CustomerID: item.customerID, SourceDomain: "identity",
						SourceEventID: fmt.Sprintf("phone-import:%d:%x", runID, item.digest[:8]), EventType: "customer.phone_attached",
						Title: "手机号已绑定", OccurredAt: now}); timelineErr != nil {
						return timelineErr
					}
				}
			}
			return insertReceipt(txContext, runID, item)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func createRun(ctx context.Context, uow platformport.UnitOfWork, runKey string, digest [32]byte, input int64) (int64, bool, error) {
	var id int64
	var status string
	var stored []byte
	err := uow.Within(ctx, func(txContext context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txContext)
		if err != nil {
			return err
		}
		err = tx.QueryRow(txContext, `SELECT id,status,source_manifest_digest FROM identity_phone_import_runs WHERE run_key=$1`, runKey).Scan(&id, &status, &stored)
		if err == nil {
			if string(stored) != string(digest[:]) {
				return identityapp.ErrDeclaredPayloadMismatch
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(txContext, `INSERT INTO identity_phone_import_runs(run_key,schema_version,source_manifest_digest,status,input_count) VALUES($1,$2,$3,'applying',$4) RETURNING id`, runKey, schemaVersion, digest[:], input).Scan(&id)
	})
	return id, status == "applied" || status == "reconciled", err
}

func finishRun(ctx context.Context, uow platformport.UnitOfWork, runID int64, value counts) error {
	return uow.Within(ctx, func(txContext context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txContext)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(txContext, `UPDATE identity_phone_import_runs SET status='applied',attached_count=$2,already_linked_count=$3,conflict_count=$4,unresolved_count=$5,invalid_count=$6,duplicate_input_count=$7,completed_at=clock_timestamp() WHERE id=$1 AND status='applying'`, runID, value.Attached, value.AlreadyLinked, value.Conflict, value.Unresolved, value.Invalid, value.DuplicateInput)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("import run changed")
		}
		return nil
	})
}

func reconcile(ctx context.Context, uow platformport.UnitOfWork, runKey string) error {
	var runID int64
	var status string
	var value counts
	var receipts int64
	err := uow.Within(ctx, func(txContext context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txContext)
		if err != nil {
			return err
		}
		err = tx.QueryRow(txContext, `SELECT id,status,input_count,attached_count,already_linked_count,conflict_count,unresolved_count,invalid_count,duplicate_input_count FROM identity_phone_import_runs WHERE run_key=$1 FOR UPDATE`, runKey).Scan(&runID, &status, &value.Input, &value.Attached, &value.AlreadyLinked, &value.Conflict, &value.Unresolved, &value.Invalid, &value.DuplicateInput)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("import run not found")
		}
		if err != nil {
			return err
		}
		if err = tx.QueryRow(txContext, `SELECT count(*) FROM identity_phone_import_receipts WHERE run_id=$1`, runID).Scan(&receipts); err != nil {
			return err
		}
		sum := value.Attached + value.AlreadyLinked + value.Conflict + value.Unresolved + value.Invalid + value.DuplicateInput
		if sum != value.Input || receipts != value.Input {
			return errors.New("reconciliation mismatch")
		}
		if status == "applied" {
			_, err = tx.Exec(txContext, `UPDATE identity_phone_import_runs SET status='reconciled' WHERE id=$1 AND status='applied'`, runID)
		}
		return err
	})
	if err != nil {
		return err
	}
	return printResult("reconcile", runID, value)
}

func rollback(ctx context.Context, uow platformport.UnitOfWork, runKey string) error {
	return uow.Within(ctx, func(txContext context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txContext)
		if err != nil {
			return err
		}
		var runID int64
		var status string
		if err = tx.QueryRow(txContext, `SELECT id,status FROM identity_phone_import_runs WHERE run_key=$1 FOR UPDATE`, runKey).Scan(&runID, &status); err != nil {
			return err
		}
		if status == "rolled_back" {
			return nil
		}
		if status != "applied" && status != "reconciled" {
			return errors.New("run is not rollback eligible")
		}
		rows, err := tx.Query(txContext, `UPDATE customer_identities identity SET status='retired',version=version+1,updated_at=clock_timestamp() FROM identity_phone_import_receipts receipt WHERE receipt.run_id=$1 AND receipt.outcome='attached' AND receipt.identity_id=identity.id AND identity.customer_id=receipt.customer_id AND identity.kind='phone' AND identity.assurance='declared' AND identity.source='phone_import' AND identity.status='active' AND identity.source_event_id LIKE 'phone-import:%' RETURNING identity.customer_id`, runID)
		if err != nil {
			return err
		}
		customerSet := map[customerdomain.CustomerID]struct{}{}
		for rows.Next() {
			var customerID customerdomain.CustomerID
			if err = rows.Scan(&customerID); err != nil {
				rows.Close()
				return err
			}
			customerSet[customerID] = struct{}{}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		projection := customerstore.NewPostgreSQL()
		now := time.Now().UTC()
		for customerID := range customerSet {
			var phone, protectedMask string
			var assurance identitydomain.Assurance
			queryErr := tx.QueryRow(txContext, `SELECT i.normalized_value,COALESCE(s.masked_value,''),i.assurance FROM customer_identities i LEFT JOIN identity_phone_secrets s ON s.identity_id=i.id WHERE i.customer_id=$1 AND i.kind='phone' AND i.status='active' ORDER BY (i.assurance='verified') DESC,i.id DESC LIMIT 1`, customerID).Scan(&phone, &protectedMask, &assurance)
			if errors.Is(queryErr, pgx.ErrNoRows) {
				if err = projection.ClearDirectoryPhone(txContext, customerID, now); err != nil {
					return err
				}
				continue
			}
			if queryErr != nil {
				return queryErr
			}
			if protectedMask == "" {
				protectedMask = maskPhone(phone)
			}
			if err = projection.UpdateDirectoryPhone(txContext, customerID, protectedMask, assurance, runID, now); err != nil {
				return err
			}
		}
		_, err = tx.Exec(txContext, `UPDATE identity_phone_import_runs SET status='rolled_back',rolled_back_at=clock_timestamp() WHERE id=$1`, runID)
		if err == nil {
			fmt.Printf("{\"mode\":\"rollback\",\"run_id\":%d,\"retired\":%d}\n", runID, len(customerSet))
		}
		return err
	})
}

func insertReceipt(ctx context.Context, runID int64, item *classifiedRow) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var cid any
	if item.customerID > 0 {
		cid = item.customerID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO identity_phone_import_receipts(run_id,source_row_id,source_row_digest,outcome,customer_id,error_code)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')) ON CONFLICT(run_id,source_row_id) DO NOTHING`, runID, item.receiptRowID, item.digest[:], item.outcome, cid, item.errorCode)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var storedDigest []byte
	var storedOutcome, storedError string
	var storedCustomerID *int64
	if err = tx.QueryRow(ctx, `SELECT source_row_digest,outcome,customer_id,COALESCE(error_code,'') FROM identity_phone_import_receipts WHERE run_id=$1 AND source_row_id=$2`, runID, item.receiptRowID).Scan(&storedDigest, &storedOutcome, &storedCustomerID, &storedError); err != nil {
		return err
	}
	customerMatches := (storedCustomerID == nil && item.customerID == 0) || (storedCustomerID != nil && *storedCustomerID == int64(item.customerID))
	if string(storedDigest) != string(item.digest[:]) || storedOutcome != item.outcome || storedError != item.errorCode || !customerMatches {
		return identityapp.ErrDeclaredPayloadMismatch
	}
	return nil
}

func normalizePhone(raw string) (string, error) {
	value := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(raw))
	if strings.HasPrefix(value, "+86") {
		value = strings.TrimPrefix(value, "+86")
	}
	ref, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "phone_import"})
	return ref.NormalizedValue, err
}
func maskPhone(value string) string {
	if len(value) != 11 {
		return "***"
	}
	return value[:3] + "****" + value[7:]
}
func summarize(rows []*classifiedRow) counts {
	result := counts{Input: int64(len(rows))}
	for _, item := range rows {
		switch item.outcome {
		case "attached":
			result.Attached++
		case "already_linked":
			result.AlreadyLinked++
		case "conflict":
			result.Conflict++
		case "unresolved_external_identity":
			result.Unresolved++
		case "invalid":
			result.Invalid++
		case "duplicate_input":
			result.DuplicateInput++
		case "ready":
			result.Ready++
		}
	}
	return result
}
func printResult(mode string, runID int64, value counts) error {
	payload := map[string]any{"mode": mode, "run_id": runID, "counts": value}
	return json.NewEncoder(os.Stdout).Encode(payload)
}
func withDatabase(run func(context.Context, *platformpostgres.Pool, platformport.UnitOfWork) error) error {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: url, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	return run(ctx, pool, uow)
}
func publicError(error) string { return "operation did not complete; inspect non-PII run receipts" }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
