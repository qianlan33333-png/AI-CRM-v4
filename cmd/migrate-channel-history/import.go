package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type importRunner struct {
	Pool         *platformpostgres.Pool
	UOW          platformport.UnitOfWork
	Resolver     identityport.Resolver
	UnionIDScope string
	WeComCorpID  string
	ActorID      int64
	Media        *mediastore.Repository
	States       *channelstore.PostgreSQLStore
}
type importResult struct {
	RunID, SourceRows, Imported, AlreadyImported, Unresolved, Quarantined, Invalid int64
	SnapshotID                                                                     string
	ProviderCalls, ProviderEffects                                                 int64
}
type reconcileResult struct {
	RunID, SourceRows, SourceMaps, Imported, AlreadyImported, Unresolved, Quarantined, Invalid int64
	DuplicateSourceMaps, ProviderEffectsCreatedByImport, ProviderCallsDuringImport, SilentLoss int64
	WrongOneIDBindings                                                                         int64
}

func (runner importRunner) Import(ctx context.Context, manifest snapshotManifest) (importResult, error) {
	if runner.Pool == nil || runner.UOW == nil || runner.Resolver == nil {
		return importResult{}, errors.New("import dependencies unavailable")
	}
	actor, err := runner.migrationActor(ctx)
	if err != nil {
		return importResult{}, err
	}
	runner.ActorID = actor
	runID, err := runner.reserveRun(ctx, manifest)
	if err != nil {
		return importResult{}, err
	}
	channelMap := map[int64]int64{}
	duplicates := duplicateSourceIDs(manifest)
	duplicateChannels := duplicates["automation_channel"]
	if table, found := manifest.table("automation_channel"); found {
		for _, row := range table.Rows {
			if duplicateRowID(row, duplicates[table.Name]) {
				if err = runner.quarantineRow(ctx, runID, table.Name, row, "duplicate_source_primary_id"); err != nil {
					return importResult{}, runner.failRun(ctx, runID, err)
				}
				continue
			}
			if err = runner.importChannelRow(ctx, runID, row, channelMap); err != nil {
				return importResult{}, runner.failRun(ctx, runID, err)
			}
		}
	}
	for _, table := range manifest.Tables {
		if table.Name == "automation_channel" {
			continue
		}
		for _, row := range table.Rows {
			if duplicateRowID(row, duplicates[table.Name]) {
				err = runner.quarantineRow(ctx, runID, table.Name, row, "duplicate_source_primary_id")
				if err != nil {
					return importResult{}, runner.failRun(ctx, runID, err)
				}
				continue
			}
			switch table.Name {
			case "automation_channel_contact":
				err = runner.importContactRow(ctx, runID, row, channelMap, duplicateChannels)
			case "automation_channel_assignee":
				err = runner.importAssigneeRow(ctx, runID, row, channelMap, duplicateChannels)
			default:
				err = runner.importEffectRow(ctx, runID, table.Name, row, channelMap, duplicateChannels)
			}
			if err != nil {
				return importResult{}, runner.failRun(ctx, runID, err)
			}
		}
	}
	result, err := runner.completeRun(ctx, runID, manifest)
	return result, err
}

func duplicateSourceIDs(manifest snapshotManifest) map[string]map[int64]bool {
	result := make(map[string]map[int64]bool, len(manifest.Tables))
	for _, table := range manifest.Tables {
		counts := map[int64]int{}
		for _, row := range table.Rows {
			if id, ok := jsonInt(row.Payload, "id"); ok {
				counts[id]++
			}
		}
		result[table.Name] = map[int64]bool{}
		for id, count := range counts {
			if count > 1 {
				result[table.Name][id] = true
			}
		}
	}
	return result
}

func duplicateRowID(row snapshotRow, duplicates map[int64]bool) bool {
	id, ok := jsonInt(row.Payload, "id")
	return ok && duplicates[id]
}

func (runner importRunner) quarantineRow(ctx context.Context, runID int64, table string, row snapshotRow, reason string) error {
	return runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		mapped, _, _, err := existingSourceMap(txctx, tx, runID, table, row)
		if err != nil || mapped {
			return err
		}
		return insertSourceMap(txctx, tx, runID, table, row, "quarantined", "", 0, reason)
	})
}

func (manifest snapshotManifest) table(name string) (snapshotTable, bool) {
	for _, table := range manifest.Tables {
		if table.Name == name {
			return table, true
		}
	}
	return snapshotTable{}, false
}

func (runner importRunner) migrationActor(ctx context.Context) (int64, error) {
	if runner.ActorID > 0 {
		var valid bool
		err := runner.Pool.Native().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users u JOIN admin_user_roles r ON r.admin_user_id=u.id WHERE u.id=$1 AND u.is_active AND r.role_code='super_admin')`, runner.ActorID).Scan(&valid)
		if err != nil || !valid {
			return 0, errors.New("migration actor must be an active superadmin")
		}
		return runner.ActorID, nil
	}
	var id int64
	err := runner.Pool.Native().QueryRow(ctx, `SELECT u.id FROM admin_users u JOIN admin_user_roles r ON r.admin_user_id=u.id WHERE u.is_active AND r.role_code='super_admin' ORDER BY u.id LIMIT 1`).Scan(&id)
	if err != nil {
		return 0, errors.New("active migration superadmin not found")
	}
	return id, nil
}

func (runner importRunner) reserveRun(ctx context.Context, manifest snapshotManifest) (int64, error) {
	host, _ := hex.DecodeString(strings.TrimPrefix(manifest.SourceHostDigest, "sha256:"))
	digest, _ := hex.DecodeString(manifest.DigestHex())
	var id int64
	err := runner.Pool.Native().QueryRow(ctx, `INSERT INTO channel_history_import_runs(snapshot_id,source_host_digest,snapshot_timestamp,manifest_digest,state) VALUES($1,$2,$3,$4,'importing') ON CONFLICT(snapshot_id) DO UPDATE SET state='importing',completed_at=NULL WHERE channel_history_import_runs.manifest_digest=EXCLUDED.manifest_digest AND channel_history_import_runs.state IN ('importing','failed','rolled_back') RETURNING id`, manifest.SnapshotID, host, manifest.SnapshotTimestamp.UTC(), digest).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = runner.Pool.Native().QueryRow(ctx, `SELECT id FROM channel_history_import_runs WHERE snapshot_id=$1 AND manifest_digest=$2 AND state IN ('completed','reconciled')`, manifest.SnapshotID, digest).Scan(&id)
	}
	return id, err
}

func (runner importRunner) importChannelRow(ctx context.Context, runID int64, row snapshotRow, channelMap map[int64]int64) error {
	return runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		mapped, outcome, target, err := existingSourceMap(txctx, tx, runID, "automation_channel", row)
		if err != nil {
			return err
		}
		if mapped {
			if target > 0 {
				if sourceID, ok := jsonInt(row.Payload, "id"); ok {
					channelMap[sourceID] = target
				}
			}
			_ = outcome
			return nil
		}
		sourceID, idOK := jsonInt(row.Payload, "id")
		if !idOK || sourceID < 1 {
			return insertSourceMap(txctx, tx, runID, "automation_channel", row, "invalid", "", 0, "invalid_channel_id")
		}
		code := firstString(row.Payload, "code", "channel_code")
		if !channelCode.MatchString(code) {
			code = "legacy-channel-" + strconv.FormatInt(sourceID, 10)
		}
		name := firstString(row.Payload, "name", "channel_name")
		if strings.TrimSpace(name) == "" || len([]rune(name)) > 200 {
			name = "历史渠道 " + strconv.FormatInt(sourceID, 10)
		}
		var existing int64
		err = tx.QueryRow(txctx, `SELECT id FROM channels WHERE lower(code)=lower($1)`, code).Scan(&existing)
		if err == nil {
			channelMap[sourceID] = existing
			return insertSourceMap(txctx, tx, runID, "automation_channel", row, "already_imported", "channel", existing, "existing_channel_code")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		created := firstTime(row.Payload, "created_at")
		if created.IsZero() {
			created = time.Now().UTC()
		}
		updated := firstTime(row.Payload, "updated_at")
		if updated.Before(created) {
			updated = created
		}
		channelType, carrier := "qrcode", "qrcode"
		if firstString(row.Payload, "channel_type", "carrier_type") == "link" {
			channelType, carrier = "wecom_customer_acquisition", "link"
		}
		var channelID int64
		if err = tx.QueryRow(txctx, `INSERT INTO channels(code,status,current_config_version,version,created_at,updated_at) VALUES($1,'inactive',1,1,$2,$3) RETURNING id`, code, created, updated).Scan(&channelID); err != nil {
			return err
		}
		configDigest := sha256.Sum256(append([]byte("channel-history-config-v1\x00"), row.Payload...))
		if _, err = tx.Exec(txctx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,assignment_mode,assignment_strategy,overflow_policy,config_digest,created_by,created_at) VALUES($1,1,$2,$3,$4,'single_owner','ratio','',$5,$6,$7)`, channelID, channelType, carrier, name, configDigest[:], runner.ActorID, updated); err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `INSERT INTO channel_assignees(channel_id,config_version,staff_id,priority,ratio_percent,created_at) VALUES($1,1,$2,1,100,$3)`, channelID, runner.ActorID, updated); err != nil {
			return err
		}
		channelMap[sourceID] = channelID
		return insertSourceMap(txctx, tx, runID, "automation_channel", row, "imported", "channel", channelID, "")
	})
}

func (runner importRunner) importContactRow(ctx context.Context, runID int64, row snapshotRow, channelMap map[int64]int64, duplicateChannels map[int64]bool) error {
	return runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		mapped, _, _, err := existingSourceMap(txctx, tx, runID, "automation_channel_contact", row)
		if err != nil || mapped {
			return err
		}
		id, idOK := jsonInt(row.Payload, "id")
		sourceChannel, channelOK := jsonInt(row.Payload, "channel_id")
		channelID, found := channelMap[sourceChannel]
		if channelOK && !found && duplicateChannels[sourceChannel] {
			return insertSourceMap(txctx, tx, runID, "automation_channel_contact", row, "quarantined", "", 0, "ambiguous_parent_channel")
		}
		first, last := firstTime(row.Payload, "first_channel_entered_at"), firstTime(row.Payload, "last_channel_entered_at")
		count, countOK := jsonInt(row.Payload, "enter_count")
		if !idOK || id < 1 || !channelOK || !found || first.IsZero() || last.Before(first) || !countOK || count < 1 {
			return insertSourceMap(txctx, tx, runID, "automation_channel_contact", row, "invalid", "", 0, "invalid_channel_contact")
		}
		customerID, reason, err := runner.resolveHistoricalContact(txctx, row.Payload, "channel_history_import")
		if err != nil {
			return err
		}
		outcome := "unresolved"
		if customerID != nil {
			outcome = "imported"
			reason = ""
		}
		owner := firstString(row.Payload, "owner_staff_id")
		created := firstTime(row.Payload, "created_at")
		if created.IsZero() {
			created = first
		}
		updated := firstTime(row.Payload, "updated_at")
		if updated.Before(created) {
			updated = created
		}
		var target int64
		inserted := true
		err = tx.QueryRow(txctx, `INSERT INTO channel_history_contacts(import_run_id,channel_id,source_contact_id,customer_id,owner_reference,first_entered_at,last_entered_at,enter_count,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(import_run_id,channel_id,source_contact_id) DO NOTHING RETURNING id`, runID, channelID, id, customerID, owner, first, last, count, created, updated).Scan(&target)
		if errors.Is(err, pgx.ErrNoRows) {
			inserted = false
			err = tx.QueryRow(txctx, `SELECT id FROM channel_history_contacts WHERE import_run_id=$1 AND channel_id=$2 AND source_contact_id=$3`, runID, channelID, id).Scan(&target)
		}
		if err != nil {
			return err
		}
		if outcome == "imported" {
			if !inserted {
				outcome, reason = "already_imported", "duplicate_source_contact_id"
			}
			return insertSourceMap(txctx, tx, runID, "automation_channel_contact", row, outcome, "contact", target, reason)
		}
		return insertSourceMap(txctx, tx, runID, "automation_channel_contact", row, outcome, "", 0, reason)
	})
}

// resolveHistoricalContact only asks OneID to resolve an existing identity.
// A source export is not provider verification, so the reference remains
// declared and can never provision, attach or merge a customer root.
func (runner importRunner) resolveHistoricalContact(ctx context.Context, raw json.RawMessage, source string) (*int64, string, error) {
	externalID := firstString(raw, "external_userid", "external_user_id")
	if externalID != "" && strings.TrimSpace(runner.WeComCorpID) != "" {
		resolved, err := runner.Resolver.Resolve(ctx, identitydomain.Reference{
			Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + strings.TrimSpace(runner.WeComCorpID),
			Value: externalID, Assurance: identitydomain.AssuranceDeclared, Source: source,
		})
		if err != nil {
			return nil, "", err
		}
		switch resolved.Status {
		case identityport.ResolveFound:
			if resolved.CustomerID > 0 {
				value := int64(resolved.CustomerID)
				return &value, "", nil
			}
		case identityport.ResolveConflict:
			return nil, "identity_conflict", nil
		}
	}
	unionID := firstString(raw, "unionid", "union_id")
	if unionID != "" && strings.HasPrefix(runner.UnionIDScope, "wechat-open-platform:") && len(runner.UnionIDScope) > len("wechat-open-platform:") {
		resolved, err := runner.Resolver.Resolve(ctx, identitydomain.Reference{
			Kind: identitydomain.KindUnionID, Scope: runner.UnionIDScope, Value: unionID,
			Assurance: identitydomain.AssuranceDeclared, Source: source,
		})
		if err != nil {
			return nil, "", err
		}
		if resolved.Status == identityport.ResolveFound && resolved.CustomerID > 0 {
			value := int64(resolved.CustomerID)
			return &value, "", nil
		}
		if resolved.Status == identityport.ResolveConflict {
			return nil, "identity_conflict", nil
		}
		return nil, "identity_not_found", nil
	}
	if externalID != "" && strings.TrimSpace(runner.WeComCorpID) == "" {
		return nil, "wecom_corp_scope_missing", nil
	}
	if unionID != "" {
		return nil, "unionid_scope_missing", nil
	}
	if externalID != "" {
		return nil, "identity_not_found", nil
	}
	return nil, "identity_missing", nil
}

func (runner importRunner) importAssigneeRow(ctx context.Context, runID int64, row snapshotRow, channelMap map[int64]int64, duplicateChannels map[int64]bool) error {
	return runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		mapped, _, _, err := existingSourceMap(txctx, tx, runID, "automation_channel_assignee", row)
		if err != nil || mapped {
			return err
		}
		id, idOK := jsonInt(row.Payload, "id")
		sourceChannel, channelOK := jsonInt(row.Payload, "channel_id")
		channelID, found := channelMap[sourceChannel]
		if channelOK && !found && duplicateChannels[sourceChannel] {
			return insertSourceMap(txctx, tx, runID, "automation_channel_assignee", row, "quarantined", "", 0, "ambiguous_parent_channel")
		}
		priority, pOK := jsonInt(row.Payload, "priority")
		created := firstCivilTime(row.Payload, "created_at")
		updated := firstCivilTime(row.Payload, "updated_at")
		status := firstString(row.Payload, "status")
		if !idOK || id < 1 || !channelOK || !found || !pOK || priority < 0 || created == "" || updated == "" || status == "" {
			return insertSourceMap(txctx, tx, runID, "automation_channel_assignee", row, "invalid", "", 0, "invalid_channel_assignee")
		}
		var ratio, cap any
		if value, ok := jsonInt(row.Payload, "ratio_percent"); ok {
			ratio = value
		}
		if value, ok := jsonInt(row.Payload, "max_scans_24h"); ok {
			cap = value
		}
		var target int64
		inserted := true
		err = tx.QueryRow(txctx, `INSERT INTO channel_history_assignees(import_run_id,channel_id,source_assignee_id,staff_reference,display_name_snapshot,priority,ratio_percent,max_scans_24h,status,source_created_at,source_updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::timestamp,$11::timestamp) ON CONFLICT(import_run_id,channel_id,source_assignee_id) DO NOTHING RETURNING id`, runID, channelID, id, firstString(row.Payload, "staff_id"), firstString(row.Payload, "display_name_snapshot"), priority, ratio, cap, status, created, updated).Scan(&target)
		if errors.Is(err, pgx.ErrNoRows) {
			inserted = false
			err = tx.QueryRow(txctx, `SELECT id FROM channel_history_assignees WHERE import_run_id=$1 AND channel_id=$2 AND source_assignee_id=$3`, runID, channelID, id).Scan(&target)
		}
		if err != nil {
			return err
		}
		if !inserted {
			return insertSourceMap(txctx, tx, runID, "automation_channel_assignee", row, "already_imported", "assignee", target, "duplicate_source_assignee_id")
		}
		return insertSourceMap(txctx, tx, runID, "automation_channel_assignee", row, "imported", "assignee", target, "")
	})
}

func (runner importRunner) importEffectRow(ctx context.Context, runID int64, table string, row snapshotRow, channelMap map[int64]int64, duplicateChannels map[int64]bool) error {
	return runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, err := platformpostgres.RequireTransaction(txctx)
		if err != nil {
			return err
		}
		mapped, _, _, err := existingSourceMap(txctx, tx, runID, table, row)
		if err != nil || mapped {
			return err
		}
		var channelID any
		if source, ok := jsonInt(row.Payload, "channel_id"); ok {
			if duplicateChannels[source] {
				return insertSourceMap(txctx, tx, runID, table, row, "quarantined", "", 0, "ambiguous_parent_channel")
			}
			if mappedID, found := channelMap[source]; found {
				channelID = mappedID
			}
		}
		occurred := firstTime(row.Payload, "occurred_at", "created_at", "updated_at")
		if occurred.IsZero() {
			occurred = time.Now().UTC()
		}
		digest, _ := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
		var target int64
		err = tx.QueryRow(txctx, `INSERT INTO channel_history_effects(import_run_id,channel_id,source_effect_id,effect_kind,provider_state,occurred_at,fact_digest) VALUES($1,$2,$3,$4,'legacy_unverified',$5,$6) ON CONFLICT(import_run_id,source_effect_id) DO UPDATE SET source_effect_id=EXCLUDED.source_effect_id RETURNING id`, runID, channelID, table+":"+row.SourcePK, table, occurred, digest).Scan(&target)
		if err != nil {
			return err
		}
		return insertSourceMap(txctx, tx, runID, table, row, "imported", "effect", target, "archive_only_no_provider_effect")
	})
}

func existingSourceMap(ctx context.Context, tx pgx.Tx, runID int64, table string, row snapshotRow) (bool, string, int64, error) {
	var outcome string
	var target *int64
	var digest []byte
	err := tx.QueryRow(ctx, `SELECT outcome,target_id,source_digest FROM channel_history_source_maps WHERE import_run_id=$1 AND source_table=$2 AND source_pk=$3`, runID, table, row.SourcePK).Scan(&outcome, &target, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", 0, nil
	}
	if err != nil {
		return false, "", 0, err
	}
	expected, _ := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
	if string(digest) != string(expected) {
		return false, "", 0, errors.New("source row digest drift")
	}
	if target != nil {
		return true, outcome, *target, nil
	}
	return true, outcome, 0, nil
}
func insertSourceMap(ctx context.Context, tx pgx.Tx, runID int64, table string, row snapshotRow, outcome, kind string, target int64, reason string) error {
	digest, _ := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
	var targetValue any
	if target > 0 {
		targetValue = target
	}
	_, err := tx.Exec(ctx, `INSERT INTO channel_history_source_maps(import_run_id,source_table,source_pk,source_digest,outcome,target_kind,target_id,reason_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, runID, table, row.SourcePK, digest, outcome, kind, targetValue, reason)
	return err
}

func (runner importRunner) completeRun(ctx context.Context, runID int64, manifest snapshotManifest) (importResult, error) {
	result := importResult{RunID: runID, SnapshotID: manifest.SnapshotID, SourceRows: int64(manifest.rowCount())}
	err := runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FILTER(WHERE outcome='imported'),count(*) FILTER(WHERE outcome='already_imported'),count(*) FILTER(WHERE outcome='unresolved'),count(*) FILTER(WHERE outcome='quarantined'),count(*) FILTER(WHERE outcome='invalid') FROM channel_history_source_maps WHERE import_run_id=$1`, runID).Scan(&result.Imported, &result.AlreadyImported, &result.Unresolved, &result.Quarantined, &result.Invalid)
	if err != nil {
		return importResult{}, err
	}
	if result.Imported+result.AlreadyImported+result.Unresolved+result.Quarantined+result.Invalid != result.SourceRows {
		return importResult{}, errors.New("silent source row loss detected")
	}
	_, err = runner.Pool.Native().Exec(ctx, `UPDATE channel_history_import_runs SET state='completed',imported_count=$2,unresolved_count=$3,quarantined_count=$4,invalid_count=$5,completed_at=clock_timestamp() WHERE id=$1 AND state IN ('importing','completed')`, runID, result.Imported+result.AlreadyImported, result.Unresolved, result.Quarantined, result.Invalid)
	return result, err
}
func (manifest snapshotManifest) rowCount() int {
	n := 0
	for _, table := range manifest.Tables {
		n += len(table.Rows)
	}
	return n
}
func (runner importRunner) failRun(ctx context.Context, runID int64, cause error) error {
	_, _ = runner.Pool.Native().Exec(ctx, `UPDATE channel_history_import_runs SET state='failed' WHERE id=$1 AND state='importing'`, runID)
	return cause
}

func (runner importRunner) Reconcile(ctx context.Context, manifest snapshotManifest) (reconcileResult, error) {
	var result reconcileResult
	err := runner.Pool.Native().QueryRow(ctx, `SELECT id FROM channel_history_import_runs WHERE snapshot_id=$1 AND manifest_digest=decode($2,'hex') AND state IN ('completed','reconciled')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&result.RunID)
	if err != nil {
		return result, err
	}
	result.SourceRows = int64(manifest.rowCount())
	err = runner.Pool.Native().QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome='imported'),count(*) FILTER(WHERE outcome='already_imported'),count(*) FILTER(WHERE outcome='unresolved'),count(*) FILTER(WHERE outcome='quarantined'),count(*) FILTER(WHERE outcome='invalid') FROM channel_history_source_maps WHERE import_run_id=$1`, result.RunID).Scan(&result.SourceMaps, &result.Imported, &result.AlreadyImported, &result.Unresolved, &result.Quarantined, &result.Invalid)
	if err != nil {
		return result, err
	}
	err = runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM (SELECT source_table,source_pk,count(*) FROM channel_history_source_maps WHERE import_run_id=$1 GROUP BY source_table,source_pk HAVING count(*)>1) d`, result.RunID).Scan(&result.DuplicateSourceMaps)
	if err != nil {
		return result, err
	}
	result.SilentLoss = result.SourceRows - result.SourceMaps
	result.WrongOneIDBindings, err = runner.verifyOneIDBindings(ctx, result.RunID, manifest)
	if err != nil {
		return result, err
	}
	if result.SilentLoss != 0 || result.DuplicateSourceMaps != 0 || result.WrongOneIDBindings != 0 {
		return result, errors.New("channel import reconciliation mismatch")
	}
	_, err = runner.Pool.Native().Exec(ctx, `UPDATE channel_history_import_runs SET state='reconciled',completed_at=COALESCE(completed_at,clock_timestamp()) WHERE id=$1`, result.RunID)
	return result, err
}

func (runner importRunner) verifyOneIDBindings(ctx context.Context, runID int64, manifest snapshotManifest) (int64, error) {
	table, ok := manifest.table("automation_channel_contact")
	if !ok {
		return 0, nil
	}
	var wrong int64
	for _, row := range table.Rows {
		sourceContactID, contactOK := jsonInt(row.Payload, "id")
		sourceChannelID, channelOK := jsonInt(row.Payload, "channel_id")
		if !contactOK || !channelOK {
			continue
		}
		var actualCustomerID *int64
		var targetChannelID int64
		err := runner.Pool.Native().QueryRow(ctx, `SELECT target_id FROM channel_history_source_maps WHERE import_run_id=$1 AND source_table='automation_channel' AND (source_pk LIKE $2 OR source_pk=$3) AND target_kind='channel' ORDER BY id LIMIT 1`, runID, "id="+strconv.FormatInt(sourceChannelID, 10)+"#%", strconv.FormatInt(sourceChannelID, 10)).Scan(&targetChannelID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return wrong, err
		}
		err = runner.Pool.Native().QueryRow(ctx, `SELECT COALESCE((SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=h.id ORDER BY reconciled_at DESC,id DESC LIMIT 1),h.customer_id) FROM channel_history_contacts h WHERE h.import_run_id=$1 AND h.channel_id=$2 AND h.source_contact_id=$3`, runID, targetChannelID, sourceContactID).Scan(&actualCustomerID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return wrong, err
		}
		var expected *int64
		err = runner.UOW.Within(ctx, func(txctx context.Context) error {
			var resolveErr error
			expected, _, resolveErr = runner.resolveHistoricalContact(txctx, row.Payload, "channel_history_reconcile")
			return resolveErr
		})
		if err != nil {
			return wrong, err
		}
		if expected == nil && actualCustomerID != nil || expected != nil && (actualCustomerID == nil || *actualCustomerID != *expected) {
			wrong++
		}
	}
	return wrong, nil
}

func (runner importRunner) ReplayCheck(ctx context.Context, manifest snapshotManifest) (reconcileResult, error) {
	before, err := runner.Reconcile(ctx, manifest)
	if err != nil {
		return before, err
	}
	var beforeFacts int64
	if err = runner.Pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_history_source_maps WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_contacts WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_assignees WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_effects WHERE import_run_id=$1)`, before.RunID).Scan(&beforeFacts); err != nil {
		return before, err
	}
	if _, err = runner.Import(ctx, manifest); err != nil {
		return before, err
	}
	after, err := runner.Reconcile(ctx, manifest)
	if err != nil {
		return after, err
	}
	var afterFacts int64
	if err = runner.Pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_history_source_maps WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_contacts WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_assignees WHERE import_run_id=$1)+(SELECT count(*) FROM channel_history_effects WHERE import_run_id=$1)`, after.RunID).Scan(&afterFacts); err != nil {
		return after, err
	}
	if before != after || beforeFacts != afterFacts {
		return after, errors.New("replay changed imported facts")
	}
	return after, nil
}
func (runner importRunner) Rollback(ctx context.Context, manifest snapshotManifest) (map[string]any, error) {
	var runID int64
	err := runner.Pool.Native().QueryRow(ctx, `SELECT id FROM channel_history_import_runs WHERE snapshot_id=$1 AND manifest_digest=decode($2,'hex') AND state IN ('completed','reconciled','failed')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&runID)
	if err != nil {
		return nil, err
	}
	tx, err := runner.Pool.Native().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var runtimeRefs int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM channel_acquisition_entrant_receipts r JOIN channel_acquisition_state_bindings b ON b.id=r.binding_id WHERE b.channel_id IN (SELECT target_id FROM channel_history_source_maps WHERE import_run_id=$1 AND target_kind='channel' AND target_id IS NOT NULL)`, runID).Scan(&runtimeRefs)
	if err != nil {
		return nil, err
	}
	if runtimeRefs > 0 {
		return nil, errors.New("rollback blocked by new runtime channel references")
	}
	if _, err = tx.Exec(ctx, `SET LOCAL aicrm.channel_history_rollback='on'`); err != nil {
		return nil, err
	}
	for _, table := range []string{"channel_history_effects", "channel_history_assignees", "channel_history_contacts", "channel_history_source_maps"} {
		if _, err = tx.Exec(ctx, "DELETE FROM "+table+" WHERE import_run_id=$1", runID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE channel_history_import_runs SET state='rolled_back',imported_count=0,unresolved_count=0,quarantined_count=0,invalid_count=0,completed_at=clock_timestamp() WHERE id=$1`, runID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"run_id": runID, "rolled_back": true, "provider_calls": 0, "note": "inactive imported channel definitions are retained for audit and safely reused by a subsequent import"}, nil
}

var channelCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

func decodeObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func firstString(raw json.RawMessage, keys ...string) string {
	object := decodeObject(raw)
	for _, key := range keys {
		var value string
		if data, ok := object[key]; ok && json.Unmarshal(data, &value) == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func jsonInt(raw json.RawMessage, key string) (int64, bool) {
	data, ok := decodeObject(raw)[key]
	if !ok || string(data) == "null" {
		return 0, false
	}
	var value int64
	if json.Unmarshal(data, &value) == nil {
		return value, true
	}
	var number json.Number
	if json.Unmarshal(data, &number) == nil {
		value, err := number.Int64()
		return value, err == nil
	}
	return 0, false
}
func firstTime(raw json.RawMessage, keys ...string) time.Time {
	for _, key := range keys {
		value := firstString(raw, key)
		if value == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse("2006-01-02 15:04:05.999999", value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
func firstCivilTime(raw json.RawMessage, key string) string {
	value := firstString(raw, key)
	for _, layout := range []string{"2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02 15:04:05.000000")
		}
	}
	return ""
}
