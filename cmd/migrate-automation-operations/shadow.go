package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
)

// ShadowPackage compares the frozen source snapshot with the exact published
// target snapshot at the same reference time. It intentionally emits counts
// and equality flags only; canonical customer IDs never leave the command.
type ShadowPackage struct {
	SourcePackageID      int64  `json:"source_package_id"`
	TargetPackageID      int64  `json:"target_package_id"`
	Lifecycle            string `json:"lifecycle"`
	StructureMatches     bool   `json:"structure_matches"`
	ConfigurationMatches bool   `json:"configuration_matches"`
	ReferenceTimeMatches bool   `json:"reference_time_matches"`
	SourceMemberRows     int    `json:"source_member_rows"`
	MappedMemberRows     int    `json:"mapped_member_rows"`
	IsolatedMemberRows   int    `json:"isolated_member_rows"`
	CanonicalMemberCount int    `json:"canonical_member_count"`
	TargetMemberCount    int64  `json:"target_member_count"`
	MemberDigestMatches  bool   `json:"member_digest_matches"`
	PausedOrArchived     bool   `json:"paused_or_archived"`
	Isolated             bool   `json:"isolated"`
	Ready                bool   `json:"ready"`
}

// ShadowQuarantine proves that a source row intentionally isolated during
// import still has its immutable receipt and original digest. It carries no
// source payload.
type ShadowQuarantine struct {
	SourceTable         string `json:"source_table"`
	SourcePK            string `json:"source_pk"`
	Disposition         string `json:"disposition"`
	ReasonCodeMatches   bool   `json:"reason_code_matches"`
	RecordDigestMatches bool   `json:"record_digest_matches"`
	Ready               bool   `json:"ready"`
}

type ShadowReport struct {
	BatchKey                string             `json:"batch_key"`
	BatchStatus             string             `json:"batch_status"`
	ReferenceTime           time.Time          `json:"reference_time"`
	Packages                []ShadowPackage    `json:"packages"`
	ProviderEffectsCreated  int64              `json:"provider_effects_created"`
	RiverJobsCreated        int64              `json:"river_jobs_created"`
	OutcomeUnknownCount     int64              `json:"outcome_unknown_count"`
	History                 []ShadowHistory    `json:"history"`
	Quarantines             []ShadowQuarantine `json:"quarantines"`
	Targets                 []ShadowTarget     `json:"targets"`
	SnapshotManifestMatches bool               `json:"snapshot_manifest_matches"`
	ReadyForReconcile       bool               `json:"ready_for_reconcile"`
	ReadyForProbe           bool               `json:"ready_for_probe"`
}

// ShadowTarget proves that a current configuration row recorded as mapped by
// the migration receipt still exists and retains the imported association and
// facts. It intentionally contains IDs only for locally-owned configuration,
// never a customer or external identity.
type ShadowTarget struct {
	SourceTable string `json:"source_table"`
	SourcePK    string `json:"source_pk"`
	TargetTable string `json:"target_table"`
	TargetPK    int64  `json:"target_pk"`
	Exists      bool   `json:"exists"`
	FactsMatch  bool   `json:"facts_match"`
	Ready       bool   `json:"ready"`
}

// ShadowHistory is a safe, read-only comparison of one frozen legacy runtime
// record. It deliberately carries no source payload or Provider identifier.
type ShadowHistory struct {
	SourceTable               string    `json:"source_table"`
	SourcePK                  string    `json:"source_pk"`
	SourceState               string    `json:"source_state"`
	OccurredAt                time.Time `json:"occurred_at"`
	StateMatches              bool      `json:"state_matches"`
	OccurredAtMatches         bool      `json:"occurred_at_matches"`
	RecordDigestMatches       bool      `json:"record_digest_matches"`
	SourceEffectDigestMatches bool      `json:"source_effect_digest_matches"`
	SourceReceiptMatches      bool      `json:"source_receipt_matches"`
	ReadOnly                  bool      `json:"read_only"`
	Replayable                bool      `json:"replayable"`
	Ready                     bool      `json:"ready"`
}

type frozenSourceRow struct {
	table  string
	pk     string
	digest [32]byte
	raw    json.RawMessage
}

type sourceReceipt struct {
	targetTable string
	targetPK    *int64
	disposition string
}

func receiptKey(table, pk string) string { return table + "\x00" + pk }

func isolatedDisposition(disposition string) bool {
	return disposition == "unresolved" || disposition == "conflict" || disposition == "invalid" || disposition == "quarantine"
}

// expectedQuarantineReason replays only the explicit reason selection already
// made by the importer. It does not invent a new identity or target lookup.
// This makes a changed non-member quarantine reason as observable as a changed
// OneID member outcome.
func expectedQuarantineReason(source frozenSourceRow, receipt sourceReceipt, receipts map[string]sourceReceipt) (string, error) {
	switch source.table {
	case "automation_agents":
		var row agentRow
		if err := json.Unmarshal(source.raw, &row); err != nil {
			return "", err
		}
		if row.ID < 1 || row.AgentCode == "" || row.PublishedVersion < 1 {
			return "invalid_agent", nil
		}
		var fixed automationport.FixedContentPackage
		if err := json.Unmarshal(row.Fixed, &fixed); err != nil {
			return "invalid_fixed_content", nil
		}
		if receipt.disposition == "conflict" {
			return "agent_code_digest_conflict", nil
		}
	case "audience_configuration_versions":
		var row configRow
		if err := json.Unmarshal(source.raw, &row); err != nil {
			return "", err
		}
		var definition map[string]json.RawMessage
		if err := json.Unmarshal(row.Definition, &definition); err != nil || definition == nil {
			return "invalid_definition", nil
		}
	case "audience_bindings":
		if receipt.disposition == "unresolved" {
			return "binding_reference_unresolved", nil
		}
	case "audience_senders":
		var row senderRow
		if err := json.Unmarshal(source.raw, &row); err != nil {
			return "", err
		}
		if receipt.disposition == "quarantine" {
			return "sender_set_incomplete", nil
		}
		if receipt.disposition == "unresolved" {
			packageReceipt, ok := receipts[receiptKey("audience_packages", strconv.FormatInt(row.PackageID, 10))]
			if !ok || packageReceipt.targetPK == nil || isolatedDisposition(packageReceipt.disposition) {
				return "sender_package_unresolved", nil
			}
			return "sender_identity_unresolved", nil
		}
	case "audience_members":
		return "oneid_" + receipt.disposition, nil
	}
	return "", fmt.Errorf("unexpected quarantined source outcome %s/%s (%s)", source.table, source.pk, receipt.disposition)
}

// frozenSourceRows derives every source receipt key from the protected,
// canonical snapshot. This is deliberately shared by target and quarantine
// checks so a count-only reconciliation cannot hide a missing or changed row.
func frozenSourceRows(snapshot segmentmigration.Snapshot) ([]frozenSourceRow, error) {
	rows := make([]frozenSourceRow, 0)
	for _, table := range segmentmigration.LogicalTables {
		var rawRows []json.RawMessage
		if err := json.Unmarshal(snapshot.Tables[table], &rawRows); err != nil {
			return nil, fmt.Errorf("decode source table %s", table)
		}
		for index, raw := range rawRows {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil {
				return nil, fmt.Errorf("decode source row %s", table)
			}
			var id, packageID, version, segmentID, customerID, sortOrder int64
			_ = json.Unmarshal(object["id"], &id)
			_ = json.Unmarshal(object["package_id"], &packageID)
			_ = json.Unmarshal(object["automation_id"], &packageID)
			_ = json.Unmarshal(object["version"], &version)
			_ = json.Unmarshal(object["segment_id"], &segmentID)
			_ = json.Unmarshal(object["customer_id"], &customerID)
			_ = json.Unmarshal(object["sort_order"], &sortOrder)
			var pk string
			switch table {
			case "audience_groups", "audience_packages", "automation_agents":
				pk = strconv.FormatInt(id, 10)
			case "audience_configuration_versions":
				pk = fmt.Sprintf("%d:%d", packageID, version)
			case "audience_bindings":
				pk = strconv.FormatInt(packageID, 10)
			case "audience_senders":
				pk = fmt.Sprintf("%d:%d", packageID, sortOrder)
			case "audience_members":
				pk = fmt.Sprintf("%d:%d", segmentID, customerID)
			default:
				pk = historyPK(table, object, index)
			}
			if pk == "" || pk == "0" || pk == "0:0" {
				return nil, fmt.Errorf("invalid source key %s", table)
			}
			rows = append(rows, frozenSourceRow{table: table, pk: pk, digest: recordDigest(raw), raw: raw})
		}
	}
	return rows, nil
}

func sourceReceipts(ctx context.Context, tx pgx.Tx, batchID int64, snapshot segmentmigration.Snapshot) (map[string]sourceReceipt, []ShadowQuarantine, error) {
	rows, err := frozenSourceRows(snapshot)
	if err != nil {
		return nil, nil, err
	}
	receipts := make(map[string]sourceReceipt, len(rows))
	quarantines := make([]ShadowQuarantine, 0)
	for _, source := range rows {
		receiptSourcePK := sourcePK(snapshot, source.pk)
		var receipt sourceReceipt
		var digest []byte
		if err = tx.QueryRow(ctx, `SELECT target_table,target_pk,disposition,record_digest FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, source.table, receiptSourcePK).Scan(&receipt.targetTable, &receipt.targetPK, &receipt.disposition, &digest); err != nil {
			return nil, nil, fmt.Errorf("source receipt %s/%s: %w", source.table, source.pk, err)
		}
		if !bytes.Equal(digest, source.digest[:]) {
			return nil, nil, fmt.Errorf("source receipt digest %s/%s", source.table, source.pk)
		}
		if isolatedDisposition(receipt.disposition) {
			var reason string
			var quarantinedDigest []byte
			if err = tx.QueryRow(ctx, `SELECT reason_code,record_digest FROM automation_operations_migration_quarantine WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, source.table, receiptSourcePK).Scan(&reason, &quarantinedDigest); err != nil {
				return nil, nil, fmt.Errorf("quarantine receipt %s/%s: %w", source.table, source.pk, err)
			}
			expectedReason, reasonErr := expectedQuarantineReason(source, receipt, receipts)
			if reasonErr != nil {
				return nil, nil, reasonErr
			}
			item := ShadowQuarantine{SourceTable: source.table, SourcePK: receiptSourcePK, Disposition: receipt.disposition, ReasonCodeMatches: reason == expectedReason, RecordDigestMatches: bytes.Equal(quarantinedDigest, source.digest[:])}
			item.Ready = item.ReasonCodeMatches && item.RecordDigestMatches
			if !item.Ready {
				return nil, nil, fmt.Errorf("quarantine receipt drift %s/%s", source.table, source.pk)
			}
			quarantines = append(quarantines, item)
		}
		receipts[receiptKey(source.table, source.pk)] = receipt
	}
	var persisted int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_quarantine WHERE batch_id=$1`, batchID).Scan(&persisted); err != nil {
		return nil, nil, err
	}
	if persisted != len(quarantines) {
		return nil, nil, errors.New("migration quarantine receipt count drift")
	}
	return receipts, quarantines, nil
}

func mappedTarget(receipt sourceReceipt, table string) (int64, bool) {
	if receipt.targetTable != table || receipt.targetPK == nil || *receipt.targetPK < 1 {
		return 0, false
	}
	return *receipt.targetPK, receipt.disposition == "imported" || receipt.disposition == "mapped" || receipt.disposition == "duplicate"
}

func sourceAgentFixed(row agentRow) ([]byte, error) {
	var fixed automationport.FixedContentPackage
	if err := json.Unmarshal(row.Fixed, &fixed); err != nil {
		return nil, err
	}
	fixed.ImageLibraryIDs = nil
	fixed.MiniprogramLibraryIDs = nil
	fixed.AttachmentLibraryIDs = nil
	fixed.GroupInviteLibraryIDs = nil
	fixed.DynamicMiniprogramCard = nil
	return json.Marshal(fixed)
}

type sourceAgentBindingFact struct {
	version            int64
	content, materials [32]byte
}

func sourceAgentBindingFacts(snapshot segmentmigration.Snapshot) (map[int64]sourceAgentBindingFact, error) {
	rows, _, err := decodeRows[agentRow](snapshot, "automation_agents")
	if err != nil {
		return nil, err
	}
	facts := make(map[int64]sourceAgentBindingFact, len(rows))
	for _, row := range rows {
		fixedRaw, fixedErr := sourceAgentFixed(row)
		if fixedErr != nil || row.ID < 1 || row.AgentCode == "" || row.PublishedVersion < 1 {
			continue
		}
		var fixed automationport.FixedContentPackage
		if json.Unmarshal(fixedRaw, &fixed) != nil {
			continue
		}
		contentRaw, _ := json.Marshal(map[string]any{"automation_type": row.AutomationType, "published_role_prompt": row.PublishedRolePrompt, "published_task_prompt": row.PublishedTaskPrompt, "content_text": fixed.ContentText})
		materialsRaw, _ := json.Marshal(map[string]any{"images": fixed.ImageLibraryIDs, "mini_programs": fixed.MiniprogramLibraryIDs, "attachments": fixed.AttachmentLibraryIDs, "group_invites": fixed.GroupInviteLibraryIDs, "dynamic_card": fixed.DynamicMiniprogramCard})
		facts[row.ID] = sourceAgentBindingFact{version: row.PublishedVersion, content: sha256.Sum256(contentRaw), materials: sha256.Sum256(materialsRaw)}
	}
	return facts, nil
}

func senderPositions(snapshot segmentmigration.Snapshot, receipts map[string]sourceReceipt) (map[string]int16, error) {
	rows, _, err := decodeRows[senderRow](snapshot, "audience_senders")
	if err != nil {
		return nil, err
	}
	byPackage := map[int64][]senderRow{}
	for _, row := range rows {
		key := receiptKey("audience_senders", fmt.Sprintf("%d:%d", row.PackageID, row.SortOrder))
		if receipt, ok := receipts[key]; ok && receipt.disposition == "mapped" {
			byPackage[row.PackageID] = append(byPackage[row.PackageID], row)
		}
	}
	positions := make(map[string]int16, len(rows))
	for packageID, set := range byPackage {
		sort.Slice(set, func(a, b int) bool { return set[a].SortOrder < set[b].SortOrder })
		for index, row := range set {
			positions[fmt.Sprintf("%d:%d", packageID, row.SortOrder)] = int16(index + 1)
		}
	}
	return positions, nil
}

// shadowMappedTargets verifies the target association for every mapped current
// configuration row not already covered by the published audience snapshot.
// It reads the target tables in the same transaction as the source receipts;
// staff resolution goes through the existing Access owner rather than a new
// directory lookup.
func shadowMappedTargets(ctx context.Context, tx pgx.Tx, snapshot segmentmigration.Snapshot, receipts map[string]sourceReceipt) ([]ShadowTarget, error) {
	sources, err := frozenSourceRows(snapshot)
	if err != nil {
		return nil, err
	}
	agents, err := sourceAgentBindingFacts(snapshot)
	if err != nil {
		return nil, err
	}
	positions, err := senderPositions(snapshot, receipts)
	if err != nil {
		return nil, err
	}
	access := accessstore.NewPostgreSQL()
	boundContext := platformpostgres.BindTransaction(ctx, tx)
	items := make([]ShadowTarget, 0)
	for _, source := range sources {
		receipt, ok := receipts[receiptKey(source.table, source.pk)]
		if !ok {
			return nil, fmt.Errorf("target source receipt %s/%s", source.table, source.pk)
		}
		if isolatedDisposition(receipt.disposition) {
			continue
		}
		item := ShadowTarget{SourceTable: source.table, SourcePK: source.pk, TargetTable: receipt.targetTable}
		switch source.table {
		case "automation_agents":
			var row agentRow
			if err = json.Unmarshal(source.raw, &row); err != nil {
				return nil, err
			}
			targetID, mapped := mappedTarget(receipt, "automation_agents")
			if !mapped {
				return nil, fmt.Errorf("agent target receipt %s", source.pk)
			}
			item.TargetPK = targetID
			fixedRaw, fixedErr := sourceAgentFixed(row)
			if fixedErr != nil {
				return nil, fixedErr
			}
			var code, kind, role, task string
			var version int64
			var fixed []byte
			err = tx.QueryRow(ctx, `SELECT agent_code,automation_type,published_role_prompt,published_task_prompt,published_version,fixed_content_package FROM automation_agents WHERE id=$1`, targetID).Scan(&code, &kind, &role, &task, &version, &fixed)
			if errors.Is(err, pgx.ErrNoRows) {
				items = append(items, item)
				continue
			}
			if err != nil {
				return nil, err
			}
			item.Exists = true
			item.FactsMatch = code == row.AgentCode && kind == row.AutomationType && role == row.PublishedRolePrompt && task == row.PublishedTaskPrompt && version == row.PublishedVersion && sameJSON(fixed, fixedRaw)
		case "audience_groups":
			var row groupRow
			if err = json.Unmarshal(source.raw, &row); err != nil {
				return nil, err
			}
			targetID, mapped := mappedTarget(receipt, "segment_audience_groups")
			if !mapped {
				return nil, fmt.Errorf("group target receipt %s", source.pk)
			}
			item.TargetPK = targetID
			var name string
			var sortOrder int
			err = tx.QueryRow(ctx, `SELECT name,sort_order FROM segment_audience_groups WHERE id=$1`, targetID).Scan(&name, &sortOrder)
			if errors.Is(err, pgx.ErrNoRows) {
				items = append(items, item)
				continue
			}
			if err != nil {
				return nil, err
			}
			item.Exists = true
			item.FactsMatch = name == row.Name && (receipt.disposition != "imported" || sortOrder == row.SortOrder)
		case "audience_configuration_versions":
			var row configRow
			if err = json.Unmarshal(source.raw, &row); err != nil {
				return nil, err
			}
			targetID, mapped := mappedTarget(receipt, "segment_audience_configuration_versions")
			packageReceipt, packageOK := receipts[receiptKey("audience_packages", strconv.FormatInt(row.PackageID, 10))]
			packageID, packageMapped := mappedTarget(packageReceipt, "segment_audience_packages")
			if !mapped || !packageOK || !packageMapped {
				return nil, fmt.Errorf("configuration target receipt %s", source.pk)
			}
			item.TargetPK = targetID
			var actualPackage int64
			var definition []byte
			var cron *string
			var mode string
			err = tx.QueryRow(ctx, `SELECT package_id,definition::text,refresh_cron_utc,refresh_mode FROM segment_audience_configuration_versions WHERE id=$1`, targetID).Scan(&actualPackage, &definition, &cron, &mode)
			if errors.Is(err, pgx.ErrNoRows) {
				items = append(items, item)
				continue
			}
			if err != nil {
				return nil, err
			}
			expectedRefresh, refreshErr := frozenRefreshConfiguration(row)
			if refreshErr != nil {
				return nil, fmt.Errorf("configuration %s refresh: %w", source.pk, refreshErr)
			}
			actualCron := ""
			if cron != nil {
				actualCron = *cron
			}
			item.Exists = true
			item.FactsMatch = actualPackage == packageID && sameJSON(definition, row.Definition) && mode == expectedRefresh.Mode && actualCron == expectedRefresh.Cron
		case "audience_bindings":
			var row bindingRow
			if err = json.Unmarshal(source.raw, &row); err != nil {
				return nil, err
			}
			targetID, mapped := mappedTarget(receipt, "segment_audience_automation_binding_versions")
			packageReceipt, packageOK := receipts[receiptKey("audience_packages", strconv.FormatInt(row.PackageID, 10))]
			packageID, packageMapped := mappedTarget(packageReceipt, "segment_audience_packages")
			agentReceipt, agentOK := receipts[receiptKey("automation_agents", strconv.FormatInt(row.AgentID, 10))]
			agentID, agentMapped := mappedTarget(agentReceipt, "automation_agents")
			expected, expectedOK := agents[row.AgentID]
			if !mapped || !packageOK || !packageMapped || !agentOK || !agentMapped || !expectedOK {
				return nil, fmt.Errorf("binding target receipt %s", source.pk)
			}
			item.TargetPK = targetID
			var actualPackage, actualAgent, version int64
			var content, materials []byte
			var current bool
			err = tx.QueryRow(ctx, `SELECT b.package_id,b.agent_id,b.agent_published_version,b.content_digest,b.materials_digest,COALESCE(p.current_automation_binding_id=b.id,false) FROM segment_audience_automation_binding_versions b JOIN segment_audience_packages p ON p.id=b.package_id WHERE b.id=$1`, targetID).Scan(&actualPackage, &actualAgent, &version, &content, &materials, &current)
			if errors.Is(err, pgx.ErrNoRows) {
				items = append(items, item)
				continue
			}
			if err != nil {
				return nil, err
			}
			item.Exists = true
			item.FactsMatch = actualPackage == packageID && actualAgent == agentID && version == expected.version && bytes.Equal(content, expected.content[:]) && bytes.Equal(materials, expected.materials[:]) && current
		case "audience_senders":
			var row senderRow
			if err = json.Unmarshal(source.raw, &row); err != nil {
				return nil, err
			}
			if receipt.disposition == "duplicate" {
				item.FactsMatch = !row.Enabled && receipt.targetTable == "" && receipt.targetPK == nil
				item.Exists = item.FactsMatch
				break
			}
			senderSetID, mapped := mappedTarget(receipt, "segment_audience_sender_set_members")
			packageReceipt, packageOK := receipts[receiptKey("audience_packages", strconv.FormatInt(row.PackageID, 10))]
			packageID, packageMapped := mappedTarget(packageReceipt, "segment_audience_packages")
			position, positioned := positions[source.pk]
			if !mapped || !packageOK || !packageMapped || !positioned {
				return nil, fmt.Errorf("sender target receipt %s", source.pk)
			}
			item.TargetPK = senderSetID
			user, userErr := access.UserByWeComUserID(boundContext, row.UserID, false)
			if userErr != nil {
				return nil, fmt.Errorf("sender access resolution %s: %w", source.pk, userErr)
			}
			var actualPackage, staffID int64
			var actualPosition int16
			var current bool
			err = tx.QueryRow(ctx, `SELECT s.package_id,m.staff_id,m.sort_order,COALESCE(p.current_sender_set_id=s.id,false) FROM segment_audience_sender_set_members m JOIN segment_audience_sender_sets s ON s.id=m.sender_set_id JOIN segment_audience_packages p ON p.id=s.package_id WHERE m.sender_set_id=$1 AND m.sort_order=$2`, senderSetID, position).Scan(&actualPackage, &staffID, &actualPosition, &current)
			if errors.Is(err, pgx.ErrNoRows) {
				items = append(items, item)
				continue
			}
			if err != nil {
				return nil, err
			}
			item.Exists = true
			item.FactsMatch = row.Enabled && actualPackage == packageID && actualPosition == position && staffID == user.ID && current
		default:
			continue
		}
		item.Ready = item.Exists && item.FactsMatch
		items = append(items, item)
	}
	return items, nil
}

func Shadow(ctx context.Context, pool *pgxpool.Pool, batchKey string, snapshot segmentmigration.Snapshot) (ShadowReport, error) {
	if pool == nil || batchKey == "" {
		return ShadowReport{}, errors.New("target and batch-key are required")
	}
	if err := segmentmigration.ValidateSnapshot(snapshot); err != nil {
		return ShadowReport{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ShadowReport{}, err
	}
	defer tx.Rollback(ctx)

	report, err := shadowInTx(ctx, tx, batchKey, snapshot)
	if err != nil {
		return report, err
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// shadowInTx is shared by the read-only operator report and Reconcile. The
// latter uses it inside its SERIALIZABLE transaction so a batch is never
// marked reconciled after a separately observed, stale comparison.
func shadowInTx(ctx context.Context, tx pgx.Tx, batchKey string, snapshot segmentmigration.Snapshot) (ShadowReport, error) {
	report := ShadowReport{BatchKey: batchKey, ReferenceTime: snapshot.Manifest.SnapshotAt.UTC()}
	var batchID, effectsBefore, effectsAfter, jobsBefore, jobsAfter int64
	var donorCommit, sourceWatermark string
	var manifestDigest []byte
	if err := tx.QueryRow(ctx, `SELECT id,status,donor_commit,encode(source_watermark_digest,'hex'),manifest_digest,provider_effect_count_before,provider_effect_count_after,river_job_count_before,river_job_count_after FROM automation_operations_migration_batches WHERE batch_key=$1`, batchKey).Scan(&batchID, &report.BatchStatus, &donorCommit, &sourceWatermark, &manifestDigest, &effectsBefore, &effectsAfter, &jobsBefore, &jobsAfter); err != nil {
		return report, err
	}
	if donorCommit != snapshot.Manifest.DonorCommit || sourceWatermark != snapshot.Manifest.SourceWatermarkDigest {
		return report, errors.New("stored batch does not belong to supplied frozen snapshot")
	}
	manifestRaw, err := json.Marshal(snapshot.Manifest)
	if err != nil {
		return report, errors.New("encode supplied frozen manifest")
	}
	expectedManifestDigest := sha256.Sum256(manifestRaw)
	report.SnapshotManifestMatches = bytes.Equal(manifestDigest, expectedManifestDigest[:])
	if !report.SnapshotManifestMatches {
		return report, errors.New("stored batch manifest does not match supplied frozen snapshot")
	}
	report.ProviderEffectsCreated = effectsAfter - effectsBefore
	report.RiverJobsCreated = jobsAfter - jobsBefore
	receipts, quarantines, err := sourceReceipts(ctx, tx, batchID, snapshot)
	if err != nil {
		return report, err
	}
	report.Quarantines = quarantines
	allReady := report.ProviderEffectsCreated == 0 && report.RiverJobsCreated == 0
	targets, err := shadowMappedTargets(ctx, tx, snapshot, receipts)
	if err != nil {
		return report, err
	}
	report.Targets = targets
	for _, item := range targets {
		allReady = allReady && item.Ready
	}

	packages, _, err := decodeRows[packageRow](snapshot, "audience_packages")
	if err != nil {
		return report, err
	}
	configs, _, err := decodeRows[configRow](snapshot, "audience_configuration_versions")
	if err != nil {
		return report, err
	}
	members, _, err := decodeRows[memberRow](snapshot, "audience_members")
	if err != nil {
		return report, err
	}
	latestConfig := map[int64]configRow{}
	for _, item := range configs {
		if current, ok := latestConfig[item.PackageID]; !ok || item.Version > current.Version {
			latestConfig[item.PackageID] = item
		}
	}
	membersByPackage := map[int64][]memberRow{}
	for _, item := range members {
		membersByPackage[item.SegmentID] = append(membersByPackage[item.SegmentID], item)
	}
	sort.Slice(packages, func(a, b int) bool { return packages[a].ID < packages[b].ID })

	probeEligible := false
	for _, source := range packages {
		item := ShadowPackage{SourcePackageID: source.ID, SourceMemberRows: len(membersByPackage[source.ID])}
		packageReceipt, ok := receipts[receiptKey("audience_packages", strconv.FormatInt(source.ID, 10))]
		if !ok {
			return report, fmt.Errorf("package %d source receipt", source.ID)
		}
		if isolatedDisposition(packageReceipt.disposition) {
			item.Isolated = true
			item.Ready = true
			report.Packages = append(report.Packages, item)
			continue
		}
		targetPackageID, mapped := mappedTarget(packageReceipt, "segment_audience_packages")
		if !mapped {
			return report, fmt.Errorf("package %d target receipt", source.ID)
		}
		item.TargetPackageID = targetPackageID
		var targetDefinition []byte
		var targetCron *string
		var targetMode string
		var targetName string
		var targetGroupID *int64
		var targetSnapshotID int64
		var targetSnapshotState string
		var targetReference time.Time
		var targetDigest []byte
		if err = tx.QueryRow(ctx, `SELECT p.lifecycle,p.name,p.group_id,c.definition::text,c.refresh_cron_utc,c.refresh_mode,s.id,s.state,s.reference_time,s.member_count,s.member_digest FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id AND s.package_id=p.id WHERE p.id=$1`, item.TargetPackageID).Scan(&item.Lifecycle, &targetName, &targetGroupID, &targetDefinition, &targetCron, &targetMode, &targetSnapshotID, &targetSnapshotState, &targetReference, &item.TargetMemberCount, &targetDigest); err != nil {
			return report, fmt.Errorf("package %d target projection: %w", source.ID, err)
		}
		sourceConfiguration, ok := latestConfig[source.ID]
		if !ok {
			return report, fmt.Errorf("package %d has no source configuration", source.ID)
		}
		expectedRefresh, refreshErr := frozenRefreshConfiguration(sourceConfiguration)
		if refreshErr != nil {
			return report, fmt.Errorf("package %d refresh: %w", source.ID, refreshErr)
		}
		actualCron := ""
		if targetCron != nil {
			actualCron = *targetCron
		}
		var expectedGroupID *int64
		if source.GroupID != nil {
			if groupReceipt, groupOK := receipts[receiptKey("audience_groups", strconv.FormatInt(*source.GroupID, 10))]; groupOK {
				if groupID, groupMapped := mappedTarget(groupReceipt, "segment_audience_groups"); groupMapped {
					expectedGroupID = &groupID
				}
			}
		}
		item.StructureMatches = targetName == source.Name && ((targetGroupID == nil && expectedGroupID == nil) || (targetGroupID != nil && expectedGroupID != nil && *targetGroupID == *expectedGroupID))
		item.ConfigurationMatches = sameJSON(sourceConfiguration.Definition, targetDefinition) && targetMode == expectedRefresh.Mode && actualCron == expectedRefresh.Cron
		item.ReferenceTimeMatches = targetReference.UTC().Equal(snapshot.Manifest.SnapshotAt.UTC())
		item.PausedOrArchived = item.Lifecycle == "paused" || (source.Lifecycle == "archived" && item.Lifecycle == "archived")

		canonical := make([]int64, 0, item.SourceMemberRows)
		for _, sourceMember := range membersByPackage[source.ID] {
			memberKey := fmt.Sprintf("%d:%d", sourceMember.SegmentID, sourceMember.CustomerID)
			memberReceipt, ok := receipts[receiptKey("audience_members", memberKey)]
			if !ok {
				return report, fmt.Errorf("member source receipt %s", memberKey)
			}
			if memberReceipt.disposition == "mapped" && memberReceipt.targetPK != nil && *memberReceipt.targetPK > 0 && memberReceipt.targetTable == "segment_audience_snapshot_members.customer_id" {
				item.MappedMemberRows++
				canonical = append(canonical, *memberReceipt.targetPK)
				continue
			}
			if memberReceipt.disposition != "unresolved" && memberReceipt.disposition != "conflict" && memberReceipt.disposition != "invalid" && memberReceipt.disposition != "quarantine" {
				return report, fmt.Errorf("member source receipt disposition %q", memberReceipt.disposition)
			}
			item.IsolatedMemberRows++
		}
		sort.Slice(canonical, func(a, b int) bool { return canonical[a] < canonical[b] })
		unique := canonical[:0]
		for _, id := range canonical {
			if len(unique) == 0 || unique[len(unique)-1] != id {
				unique = append(unique, id)
			}
		}
		item.CanonicalMemberCount = len(unique)
		// A controlled activation probe requires at least one verified canonical
		// recipient. A fully isolated audience is still completely reconcilable,
		// but it cannot prove a live execution path.
		probeEligible = probeEligible || item.CanonicalMemberCount > 0
		customerIDs := make([]customerdomain.CustomerID, len(unique))
		for index, id := range unique {
			customerIDs[index] = customerdomain.CustomerID(id)
		}
		expectedDigest := segmentdomain.DigestMembers(customerIDs)
		actualCustomerIDs := make([]customerdomain.CustomerID, 0, item.TargetMemberCount)
		rows, queryErr := tx.Query(ctx, `SELECT customer_id,entered_at,identity_disposition FROM segment_audience_snapshot_members WHERE snapshot_id=$1 ORDER BY customer_id`, targetSnapshotID)
		if queryErr != nil {
			return report, queryErr
		}
		enteredAtMatches := true
		for rows.Next() {
			var customerID int64
			var enteredAt time.Time
			var disposition string
			if queryErr = rows.Scan(&customerID, &enteredAt, &disposition); queryErr != nil {
				rows.Close()
				return report, queryErr
			}
			actualCustomerIDs = append(actualCustomerIDs, customerdomain.CustomerID(customerID))
			enteredAtMatches = enteredAtMatches && enteredAt.UTC().Equal(snapshot.Manifest.SnapshotAt.UTC()) && disposition == "resolved"
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return report, queryErr
		}
		rows.Close()
		actualDigest := segmentdomain.DigestMembers(actualCustomerIDs)
		item.MemberDigestMatches = targetSnapshotState == "published" && item.MappedMemberRows+item.IsolatedMemberRows == item.SourceMemberRows && int64(len(unique)) == item.TargetMemberCount && int64(len(actualCustomerIDs)) == item.TargetMemberCount && bytes.Equal(expectedDigest[:], targetDigest) && bytes.Equal(expectedDigest[:], actualDigest[:]) && enteredAtMatches
		item.Ready = item.StructureMatches && item.ConfigurationMatches && item.ReferenceTimeMatches && item.PausedOrArchived && item.MemberDigestMatches
		allReady = allReady && item.Ready
		report.Packages = append(report.Packages, item)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM automation_run_recipients r JOIN automation_runs x ON x.id=r.run_id WHERE r.state='outcome_unknown' AND x.package_id IN (SELECT target_pk FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table='audience_packages' AND target_pk IS NOT NULL)`, batchID).Scan(&report.OutcomeUnknownCount); err != nil {
		return report, err
	}
	for _, name := range []string{"automation_policies", "automation_policy_versions", "automation_enrollments", "automation_actions"} {
		var rows []json.RawMessage
		if err = json.Unmarshal(snapshot.Tables[name], &rows); err != nil {
			return report, err
		}
		for index, raw := range rows {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil {
				return report, fmt.Errorf("invalid history row %s", name)
			}
			pk := historyPK(name, object, index)
			item := ShadowHistory{SourceTable: name, SourcePK: pk, SourceState: historyString(object, "state", historyString(object, "status", "unknown")), OccurredAt: historyTime(object)}
			var historyID int64
			var storedDigest, storedEffect, sourceReceiptDigest []byte
			var storedState string
			var storedOccurred time.Time
			if err = tx.QueryRow(ctx, `SELECT id,source_state,occurred_at,record_digest,source_effect_digest,read_only,replayable FROM automation_operations_legacy_history WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, name, sourcePK(snapshot, pk)).Scan(&historyID, &storedState, &storedOccurred, &storedDigest, &storedEffect, &item.ReadOnly, &item.Replayable); err != nil {
				return report, fmt.Errorf("history %s/%s: %w", name, pk, err)
			}
			item.StateMatches = storedState == item.SourceState
			item.OccurredAtMatches = storedOccurred.UTC().Equal(item.OccurredAt.UTC())
			expected := recordDigest(raw)
			item.RecordDigestMatches = bytes.Equal(storedDigest, expected[:])
			var mappedID *int64
			var mappedTarget, mappedDisposition string
			if err = tx.QueryRow(ctx, `SELECT target_pk,target_table,disposition,record_digest FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, name, sourcePK(snapshot, pk)).Scan(&mappedID, &mappedTarget, &mappedDisposition, &sourceReceiptDigest); err != nil {
				return report, fmt.Errorf("history source receipt %s/%s: %w", name, pk, err)
			}
			item.SourceReceiptMatches = mappedID != nil && *mappedID == historyID && mappedTarget == "automation_operations_legacy_history" && mappedDisposition == "imported" && bytes.Equal(sourceReceiptDigest, expected[:])
			expectedEffect := []byte(nil)
			if effectID := historyString(object, "external_effect_id", ""); effectID != "" {
				sum := sha256.Sum256([]byte(effectID))
				expectedEffect = sum[:]
			}
			item.SourceEffectDigestMatches = bytes.Equal(storedEffect, expectedEffect)
			item.Ready = item.StateMatches && item.OccurredAtMatches && item.RecordDigestMatches && item.SourceEffectDigestMatches && item.SourceReceiptMatches && item.ReadOnly && !item.Replayable
			allReady = allReady && item.Ready
			report.History = append(report.History, item)
		}
	}
	report.ReadyForReconcile = (report.BatchStatus == "imported" || report.BatchStatus == "reconciled") && allReady && report.OutcomeUnknownCount == 0
	report.ReadyForProbe = report.BatchStatus == "reconciled" && report.ReadyForReconcile && probeEligible
	return report, nil
}
