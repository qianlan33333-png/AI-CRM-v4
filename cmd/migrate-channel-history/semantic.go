package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type semanticValidation struct {
	Channels, ReferencedMaterials, ReferencedTags int64
	MissingMaterialRows, MissingTagRows           int64
}

type semanticResult struct {
	RepairRunID, RepairedChannels, ReconciledContacts, Conflicts, LegacyAssets, MaterialMappings, TagMappings int64
	ProviderCalls, ProviderEffects                                                                            int64
}

type legacyAssetReader interface {
	GetContactWay(context.Context, string) (wecomport.AcquisitionAssetResult, error)
}

type semanticAssignment struct {
	id                   int64
	priority, ratio, cap int
}

func validateSemantics(manifest snapshotManifest) (semanticValidation, error) {
	for _, name := range expectedSourceTables {
		if _, ok := manifest.table(name); !ok {
			return semanticValidation{}, fmt.Errorf("semantic snapshot missing required table %s", name)
		}
	}
	available := map[string]map[int64]bool{}
	for _, name := range []string{"image_library", "miniprogram_library", "attachment_library", "group_invite_library"} {
		available[name] = map[int64]bool{}
		table, _ := manifest.table(name)
		for _, row := range table.Rows {
			if id, ok := jsonInt(row.Payload, "id"); ok {
				available[name][id] = true
			}
		}
	}
	tags := map[string]bool{}
	if table, ok := manifest.table("wecom_corp_tags"); ok {
		for _, row := range table.Rows {
			if id := firstString(row.Payload, "tag_id"); id != "" {
				tags[id] = true
			}
		}
	}
	result := semanticValidation{}
	channels, _ := manifest.table("automation_channel")
	result.Channels = int64(len(channels.Rows))
	for _, row := range channels.Rows {
		for _, spec := range []struct{ table, field string }{{"image_library", "welcome_image_library_ids"}, {"miniprogram_library", "welcome_miniprogram_library_ids"}, {"attachment_library", "welcome_attachment_library_ids"}, {"group_invite_library", "welcome_group_invite_library_ids"}} {
			for _, id := range jsonIDs(row.Payload, spec.field) {
				result.ReferencedMaterials++
				if !available[spec.table][id] {
					result.MissingMaterialRows++
				}
			}
		}
		if id := firstString(row.Payload, "entry_tag_id"); id != "" {
			result.ReferencedTags++
			if !tags[id] {
				result.MissingTagRows++
			}
		}
	}
	if result.MissingMaterialRows != 0 || result.MissingTagRows != 0 {
		return result, errors.New("semantic snapshot does not contain every referenced material/tag")
	}
	return result, nil
}

func (runner importRunner) SemanticRepair(ctx context.Context, manifest snapshotManifest) (semanticResult, error) {
	if _, err := validateSemantics(manifest); err != nil {
		return semanticResult{}, err
	}
	actor, err := runner.migrationActor(ctx)
	if err != nil {
		return semanticResult{}, err
	}
	runner.ActorID = actor
	var runID int64
	if err = runner.Pool.Native().QueryRow(ctx, `SELECT id FROM channel_history_import_runs WHERE snapshot_id=$1 AND manifest_digest=decode($2,'hex') AND state IN ('completed','reconciled')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&runID); err != nil {
		return semanticResult{}, err
	}
	var repairID int64
	err = runner.Pool.Native().QueryRow(ctx, `INSERT INTO channel_semantic_repair_runs(import_run_id,state,source_config_count) VALUES($1,'validating',$2) ON CONFLICT(import_run_id) DO UPDATE SET state=CASE WHEN channel_semantic_repair_runs.state='activated' THEN 'activated' ELSE 'validating' END RETURNING id`, runID, len(mustTable(manifest, "automation_channel").Rows)).Scan(&repairID)
	if err != nil {
		return semanticResult{}, err
	}
	result := semanticResult{RepairRunID: repairID}
	if result.MaterialMappings, err = runner.importReferencedMaterials(ctx, runID, manifest); err != nil {
		return result, runner.failSemantic(ctx, repairID, err)
	}
	if result.TagMappings, err = runner.importReferencedTags(ctx, runID, manifest); err != nil {
		return result, runner.failSemantic(ctx, repairID, err)
	}
	channelMap, err := runner.sourceChannelMap(ctx, runID)
	if err != nil {
		return result, runner.failSemantic(ctx, repairID, err)
	}
	if result.ReconciledContacts, err = runner.reconcileHistoricalContacts(ctx, runID, manifest, channelMap); err != nil {
		return result, runner.failSemantic(ctx, repairID, err)
	}
	assignees := semanticAssignees(manifest)
	channels := mustTable(manifest, "automation_channel")
	for _, row := range channels.Rows {
		sourceID, ok := jsonInt(row.Payload, "id")
		if !ok {
			continue
		}
		channelID := channelMap[sourceID]
		if channelID < 1 {
			continue
		}
		repaired, conflict, repairErr := runner.repairChannelConfig(ctx, repairID, runID, channelID, row, assignees[sourceID])
		if repairErr != nil {
			return result, runner.failSemantic(ctx, repairID, repairErr)
		}
		if repaired {
			result.RepairedChannels++
		}
		if conflict {
			result.Conflicts++
		}
	}
	if result.LegacyAssets, err = runner.importLegacyAssets(ctx, runID, repairID, manifest, channelMap); err != nil {
		return result, runner.failSemantic(ctx, repairID, err)
	}
	state := "repaired"
	if result.Conflicts > 0 {
		state = "blocked"
	}
	_, err = runner.Pool.Native().Exec(ctx, `UPDATE channel_semantic_repair_runs SET state=$2,repaired_config_count=$3,conflict_count=$4,completed_at=clock_timestamp() WHERE id=$1`, repairID, state, result.RepairedChannels, result.Conflicts)
	return result, err
}

func (runner importRunner) reconcileHistoricalContacts(ctx context.Context, runID int64, manifest snapshotManifest, channelMap map[int64]int64) (int64, error) {
	var reconciled int64
	for _, row := range mustTable(manifest, "automation_channel_contact").Rows {
		sourceContactID, contactOK := jsonInt(row.Payload, "id")
		sourceChannelID, channelOK := jsonInt(row.Payload, "channel_id")
		channelID := channelMap[sourceChannelID]
		if !contactOK || !channelOK || channelID < 1 {
			continue
		}
		err := runner.UOW.Within(ctx, func(txctx context.Context) error {
			tx, txErr := platformpostgres.RequireTransaction(txctx)
			if txErr != nil {
				return txErr
			}
			var historyID int64
			var original *int64
			if txErr = tx.QueryRow(txctx, `SELECT id,customer_id FROM channel_history_contacts WHERE import_run_id=$1 AND channel_id=$2 AND source_contact_id=$3`, runID, channelID, sourceContactID).Scan(&historyID, &original); txErr != nil {
				if errors.Is(txErr, pgx.ErrNoRows) {
					return nil
				}
				return txErr
			}
			resolved, _, resolveErr := runner.resolveHistoricalContact(txctx, row.Payload, "channel_history_semantic_reconcile")
			if resolveErr != nil || resolved == nil {
				return resolveErr
			}
			var current *int64
			if txErr = tx.QueryRow(txctx, `SELECT COALESCE((SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=$1 ORDER BY reconciled_at DESC,id DESC LIMIT 1),(SELECT customer_id FROM channel_history_contacts WHERE id=$1))`, historyID).Scan(&current); txErr != nil {
				return txErr
			}
			if current != nil {
				if *current != *resolved {
					return errors.New("historical contact OneID conflict")
				}
				return nil
			}
			digest, decodeErr := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
			if decodeErr != nil || len(digest) != sha256.Size {
				return errors.New("invalid historical contact digest")
			}
			tag, txErr := tx.Exec(txctx, `INSERT INTO channel_history_contact_reconciliations(history_contact_id,prior_customer_id,customer_id,evidence_digest) VALUES($1,$2,$3,$4) ON CONFLICT(history_contact_id,customer_id) DO NOTHING`, historyID, original, *resolved, digest)
			if txErr == nil && tag.RowsAffected() == 1 {
				reconciled++
			}
			return txErr
		})
		if err != nil {
			return reconciled, err
		}
	}
	return reconciled, nil
}

func (runner importRunner) failSemantic(ctx context.Context, id int64, cause error) error {
	_, _ = runner.Pool.Native().Exec(ctx, `UPDATE channel_semantic_repair_runs SET state='failed',completed_at=clock_timestamp() WHERE id=$1`, id)
	return cause
}

func (runner importRunner) sourceChannelMap(ctx context.Context, runID int64) (map[int64]int64, error) {
	rows, err := runner.Pool.Native().Query(ctx, `SELECT source_pk,target_id FROM channel_history_source_maps WHERE import_run_id=$1 AND source_table='automation_channel' AND target_id IS NOT NULL`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var key string
		var target int64
		if err = rows.Scan(&key, &target); err != nil {
			return nil, err
		}
		if id, ok := sourcePKID(key); ok {
			out[id] = target
		}
	}
	return out, rows.Err()
}

func sourcePKID(value string) (int64, bool) {
	value = strings.TrimPrefix(value, "id=")
	if i := strings.IndexByte(value, '#'); i >= 0 {
		value = value[:i]
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
func mustTable(manifest snapshotManifest, name string) snapshotTable {
	table, _ := manifest.table(name)
	return table
}

func semanticAssignees(manifest snapshotManifest) map[int64][]snapshotRow {
	out := map[int64][]snapshotRow{}
	table, _ := manifest.table("automation_channel_assignee")
	for _, row := range table.Rows {
		if id, ok := jsonInt(row.Payload, "channel_id"); ok && firstString(row.Payload, "status") != "inactive" {
			out[id] = append(out[id], row)
		}
	}
	for id := range out {
		sort.SliceStable(out[id], func(i, j int) bool {
			a, _ := jsonInt(out[id][i].Payload, "priority")
			b, _ := jsonInt(out[id][j].Payload, "priority")
			return a < b
		})
	}
	return out
}

func semanticExpectedAssignees(manifest snapshotManifest) map[int64][]snapshotRow {
	out := semanticAssignees(manifest)
	for _, row := range mustTable(manifest, "automation_channel").Rows {
		channelID, ok := jsonInt(row.Payload, "id")
		owner := firstString(row.Payload, "owner_staff_id")
		if !ok || owner == "" || len(out[channelID]) != 0 {
			continue
		}
		out[channelID] = []snapshotRow{{Payload: json.RawMessage(`{"staff_id":` + strconv.Quote(owner) + `,"priority":1,"ratio_percent":100,"status":"active"}`)}}
	}
	return out
}

func (runner importRunner) repairChannelConfig(ctx context.Context, repairID, runID, channelID int64, row snapshotRow, sourceAssignees []snapshotRow) (bool, bool, error) {
	var currentVersion, entityVersion int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT current_config_version,version FROM channels WHERE id=$1`, channelID).Scan(&currentVersion, &entityVersion); err != nil {
		return false, false, err
	}
	var existing int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT config_version FROM channel_semantic_repaired_configs WHERE repair_run_id=$1 AND channel_id=$2`, repairID, channelID).Scan(&existing); err == nil {
		return true, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, false, err
	}
	if entityVersion > 1 {
		migrationOwned, ownershipErr := runner.migrationOwnedCurrentConfig(ctx, repairID, channelID, currentVersion)
		if ownershipErr != nil {
			return false, false, ownershipErr
		}
		if !migrationOwned {
			source := sha256.Sum256(row.Payload)
			current := sha256.Sum256([]byte(strconv.FormatInt(currentVersion, 10)))
			_, err := runner.Pool.Native().Exec(ctx, `INSERT INTO channel_semantic_repair_conflicts(repair_run_id,channel_id,field_name,source_digest,current_digest) VALUES($1,$2,'current_config_version',$3,$4) ON CONFLICT DO NOTHING`, repairID, channelID, source[:], current[:])
			return false, true, err
		}
	}
	members := sourceAssignees
	if len(members) == 0 && firstString(row.Payload, "owner_staff_id") != "" {
		members = []snapshotRow{{Payload: json.RawMessage(`{"staff_id":` + strconv.Quote(firstString(row.Payload, "owner_staff_id")) + `,"priority":1,"ratio_percent":100,"status":"active"}`)}}
	}
	assigned := []semanticAssignment{}
	unavailableAssignees := 0
	for _, item := range members {
		providerID := firstString(item.Payload, "staff_id")
		var staffID int64
		if err := runner.Pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE wecom_userid=$1 AND is_active`, providerID).Scan(&staffID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// A departed or otherwise Provider-ineligible historical assignee
				// remains available in channel_history_assignees. Do not invent a
				// replacement or an active Access account; repair the rest of the
				// immutable config and keep activation blocked instead.
				unavailableAssignees++
				continue
			}
			return false, false, err
		}
		priority, _ := jsonInt(item.Payload, "priority")
		ratio, _ := jsonInt(item.Payload, "ratio_percent")
		cap, _ := jsonInt(item.Payload, "max_scans_24h")
		assigned = append(assigned, semanticAssignment{staffID, int(priority), int(ratio), int(cap)})
	}
	priorityInvalid := normalizeSemanticPriorities(assigned)
	mode := firstString(row.Payload, "assignment_mode")
	if mode != "multi_staff" {
		mode = "single_owner"
	}
	strategy := firstString(row.Payload, "assignment_strategy")
	if strategy != "cap_switch" {
		strategy = "ratio"
	}
	if strategy == "ratio" && len(assigned) > 0 {
		for i := range assigned {
			if assigned[i].ratio == 0 && len(assigned) == 1 {
				assigned[i].ratio = 100
			}
		}
	}
	assignmentBlockers := semanticAssignmentBlockers(strategy, assigned)
	if unavailableAssignees > 0 {
		assignmentBlockers = append(assignmentBlockers, "assignee_provider_ineligible")
	}
	if priorityInvalid {
		assignmentBlockers = append(assignmentBlockers, "assignment_priority_invalid")
	}
	if strategy == "cap_switch" {
		for _, a := range assigned {
			if a.cap < 1 {
				return false, true, runner.recordSemanticConflict(ctx, repairID, channelID, "assignment_cap", row.Payload, []byte("missing"))
			}
		}
	}
	images, missing, err := runner.mappedMaterialIDs(ctx, runID, "image_library", jsonIDs(row.Payload, "welcome_image_library_ids"))
	if err != nil {
		return false, false, err
	}
	minis, m2, err := runner.mappedMaterialIDs(ctx, runID, "miniprogram_library", jsonIDs(row.Payload, "welcome_miniprogram_library_ids"))
	if err != nil {
		return false, false, err
	}
	missing = append(missing, m2...)
	attachments, m3, err := runner.mappedMaterialIDs(ctx, runID, "attachment_library", jsonIDs(row.Payload, "welcome_attachment_library_ids"))
	if err != nil {
		return false, false, err
	}
	missing = append(missing, m3...)
	groups, m4, err := runner.mappedMaterialIDs(ctx, runID, "group_invite_library", jsonIDs(row.Payload, "welcome_group_invite_library_ids"))
	if err != nil {
		return false, false, err
	}
	missing = append(missing, m4...)
	blockers := append(append([]string{}, assignmentBlockers...), missing...)
	var tagID any
	var tagName, tagGroupName string
	providerTag := firstString(row.Payload, "entry_tag_id")
	if providerTag != "" {
		digest := sha256.Sum256([]byte(providerTag))
		var local *int64
		var state, nameSnapshot, groupNameSnapshot string
		err = runner.Pool.Native().QueryRow(ctx, `SELECT m.tag_id,m.state,COALESCE(t.tag_name,m.name_snapshot),COALESCE(g.group_name,m.group_name_snapshot)
			FROM channel_legacy_tag_maps m
			LEFT JOIN tag_catalog_tags t ON t.id=m.tag_id AND t.archived_at IS NULL
			LEFT JOIN tag_groups g ON g.id=t.group_id AND g.archived_at IS NULL
			WHERE m.import_run_id=$1 AND m.provider_tag_id_digest=$2`, runID, digest[:]).Scan(&local, &state, &nameSnapshot, &groupNameSnapshot)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, false, err
		}
		if projectedID, projectedName, projectedGroup, ok := projectMappedEntryTag(local, state, nameSnapshot, groupNameSnapshot); err != nil || !ok {
			blockers = append(blockers, "tag_unmapped")
		} else {
			tagID, tagName, tagGroupName = projectedID, projectedName, projectedGroup
		}
	}
	configVersion := currentVersion + 1
	digest := sha256.Sum256(append([]byte("channel-semantic-v1\x00"), row.Payload...))
	created := firstTime(row.Payload, "updated_at")
	if created.IsZero() {
		created = time.Now().UTC()
	}
	desired := firstString(row.Payload, "status")
	if desired != "active" && desired != "archived" {
		desired = "inactive"
	}
	err = runner.UOW.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		_, e = tx.Exec(txctx, `INSERT INTO channel_config_versions(channel_id,config_version,channel_type,carrier_type,name,scene_value,qrcode_url,customer_channel,link_url,final_url,welcome_message,welcome_image_ids,welcome_miniprogram_ids,welcome_attachment_ids,welcome_group_invite_ids,auto_accept_friend,entry_tag_id,entry_tag_name,entry_tag_group_name,assignment_mode,assignment_strategy,overflow_policy,config_digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`, channelID, configVersion, normalizeChannelType(firstString(row.Payload, "channel_type")), normalizeCarrier(firstString(row.Payload, "carrier_type")), firstString(row.Payload, "channel_name", "name"), firstString(row.Payload, "scene_value"), firstString(row.Payload, "qr_url"), firstString(row.Payload, "customer_channel"), firstString(row.Payload, "link_url"), firstString(row.Payload, "final_url"), firstString(row.Payload, "welcome_message"), images, minis, attachments, groups, jsonBool(row.Payload, "auto_accept_friend"), tagID, tagName, tagGroupName, mode, strategy, firstString(row.Payload, "overflow_policy"), digest[:], runner.ActorID, created)
		if e != nil {
			return e
		}
		for _, a := range assigned {
			var ratio, cap any
			if a.ratio > 0 {
				ratio = a.ratio
			}
			if a.cap > 0 {
				cap = a.cap
			}
			if _, e = tx.Exec(txctx, `INSERT INTO channel_assignees(channel_id,config_version,staff_id,priority,ratio_percent,max_scans_24h,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, channelID, configVersion, a.id, a.priority, ratio, cap, created); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(txctx, `UPDATE channels SET current_config_version=$2,version=version+1,status='inactive',updated_at=GREATEST(updated_at,$3) WHERE id=$1 AND current_config_version=$4`, channelID, configVersion, created, currentVersion); e != nil {
			return e
		}
		blockerJSON, _ := json.Marshal(blockers)
		_, e = tx.Exec(txctx, `INSERT INTO channel_semantic_repaired_configs(repair_run_id,channel_id,config_version,desired_status,blockers) VALUES($1,$2,$3,$4,$5::jsonb)`, repairID, channelID, configVersion, desired, blockerJSON)
		return e
	})
	return err == nil, false, err
}

func (runner importRunner) migrationOwnedCurrentConfig(ctx context.Context, repairID, channelID, configVersion int64) (bool, error) {
	var owned bool
	err := runner.Pool.Native().QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM channel_semantic_repaired_configs c WHERE c.channel_id=$1 AND c.config_version=$2 AND c.repair_run_id<>$3)
		AND NOT EXISTS(SELECT 1 FROM channel_operation_receipts r WHERE r.channel_id=$1 AND r.state='completed' AND r.operation<>'create')`, channelID, configVersion, repairID).Scan(&owned)
	return owned, err
}

func normalizeChannelType(v string) string {
	if v == "wecom_customer_acquisition" || v == "link" {
		return "wecom_customer_acquisition"
	}
	return "qrcode"
}
func normalizeCarrier(v string) string {
	if v == "link" {
		return "link"
	}
	return "qrcode"
}

func projectMappedEntryTag(local *int64, state, name, group string) (any, string, string, bool) {
	name = strings.TrimSpace(name)
	group = strings.TrimSpace(group)
	if local == nil || *local < 1 || state != "mapped" || name == "" || group == "" {
		return nil, "", "", false
	}
	return *local, name, group, true
}
func (runner importRunner) recordSemanticConflict(ctx context.Context, repairID, channelID int64, field string, source, current []byte) error {
	s := sha256.Sum256(source)
	c := sha256.Sum256(current)
	_, err := runner.Pool.Native().Exec(ctx, `INSERT INTO channel_semantic_repair_conflicts(repair_run_id,channel_id,field_name,source_digest,current_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, repairID, channelID, field, s[:], c[:])
	return err
}

func (runner importRunner) mappedMaterialIDs(ctx context.Context, runID int64, table string, source []int64) ([]int64, []string, error) {
	out := []int64{}
	missing := []string{}
	for _, id := range source {
		var target *int64
		var state string
		err := runner.Pool.Native().QueryRow(ctx, `SELECT media_id,state FROM channel_legacy_material_maps WHERE import_run_id=$1 AND source_table=$2 AND source_id=$3`, runID, table, id).Scan(&target, &state)
		if errors.Is(err, pgx.ErrNoRows) || target == nil || state != "mapped" {
			missing = append(missing, table+":"+strconv.FormatInt(id, 10))
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		out = append(out, *target)
	}
	return out, missing, nil
}

func (runner importRunner) importReferencedMaterials(ctx context.Context, runID int64, manifest snapshotManifest) (int64, error) {
	var count int64
	for _, name := range []string{"image_library", "attachment_library", "miniprogram_library", "group_invite_library"} {
		table := mustTable(manifest, name)
		for _, row := range table.Rows {
			id, ok := jsonInt(row.Payload, "id")
			if !ok {
				continue
			}
			var exists bool
			if err := runner.Pool.Native().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel_legacy_material_maps WHERE import_run_id=$1 AND source_table=$2 AND source_id=$3)`, runID, name, id).Scan(&exists); err != nil {
				return count, err
			}
			if exists {
				count++
				continue
			}
			target, state, reason, err := runner.createMedia(ctx, runID, name, id, row)
			if err != nil {
				return count, err
			}
			digest, _ := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
			_, err = runner.Pool.Native().Exec(ctx, `INSERT INTO channel_legacy_material_maps(import_run_id,source_table,source_id,media_kind,media_id,source_digest,state,reason_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, runID, name, id, mediaKind(name), target, digest, state, reason)
			if err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

func (runner importRunner) createMedia(ctx context.Context, runID int64, table string, id int64, row snapshotRow) (any, string, string, error) {
	key := "channel-semantic-" + strconv.FormatInt(runID, 10) + "-" + table + "-" + strconv.FormatInt(id, 10)
	switch table {
	case "image_library":
		content, err := decodeBase64Field(row.Payload, "data_base64")
		if err != nil || len(content) == 0 {
			return nil, "unresolved", "image_bytes_unavailable", nil
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(content))
		if err != nil {
			return nil, "invalid", "invalid_image", nil
		}
		mime := firstString(row.Payload, "mime_type")
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/gif" {
			return nil, "invalid", "invalid_image_mime", nil
		}
		result, err := runner.Media.CreateImage(ctx, runner.ActorID, key, mediastore.ImageInput{FileName: nonempty(firstString(row.Payload, "file_name"), "legacy-image-"+strconv.FormatInt(id, 10)), MIME: mime, Name: nonempty(firstString(row.Payload, "name"), "Historical image"), Content: content, Width: int32(cfg.Width), Height: int32(cfg.Height), Enabled: jsonBoolDefault(row.Payload, "enabled", true)})
		if err != nil {
			return nil, "", "", err
		}
		target, err := mediaResultID(result)
		return target, "mapped", "", err
	case "attachment_library":
		content, err := decodeBase64Field(row.Payload, "data_base64")
		if err != nil || len(content) == 0 || !bytes.HasPrefix(content, []byte("%PDF-")) {
			return nil, "unresolved", "pdf_bytes_unavailable", nil
		}
		result, err := runner.Media.CreateAttachment(ctx, runner.ActorID, key, mediastore.AttachmentInput{FileName: nonempty(firstString(row.Payload, "file_name"), "legacy.pdf"), Name: nonempty(firstString(row.Payload, "name"), "Historical attachment"), Description: firstString(row.Payload, "description"), Content: content, Enabled: jsonBoolDefault(row.Payload, "enabled", true)})
		if err != nil {
			return nil, "", "", err
		}
		target, err := mediaResultID(result)
		return target, "mapped", "", err
	case "miniprogram_library":
		input := map[string]any{"name": nonempty(firstString(row.Payload, "name"), firstString(row.Payload, "title")), "appid": firstString(row.Payload, "appid"), "pagepath": nonempty(firstString(row.Payload, "pagepath"), "pages/index/index"), "title": nonempty(firstString(row.Payload, "title"), firstString(row.Payload, "name")), "enabled": jsonBoolDefault(row.Payload, "enabled", true)}
		if thumb, ok := jsonInt(row.Payload, "thumb_image_id"); ok {
			mapped, _, _ := runner.mappedMaterialIDs(ctx, runID, "image_library", []int64{thumb})
			if len(mapped) > 0 {
				input["thumb_image_id"] = float64(mapped[0])
			}
		}
		result, err := runner.Media.CreateMiniProgram(ctx, runner.ActorID, key, input)
		if err != nil {
			return nil, "", "", err
		}
		target, err := mediaResultID(result)
		return target, "mapped", "", err
	case "group_invite_library":
		input := map[string]any{"name": nonempty(firstString(row.Payload, "name"), firstString(row.Payload, "title")), "title": nonempty(firstString(row.Payload, "title"), firstString(row.Payload, "name")), "description": firstString(row.Payload, "description"), "join_url": firstString(row.Payload, "join_url"), "enabled": jsonBoolDefault(row.Payload, "enabled", true)}
		result, err := runner.Media.CreateGroupInvite(ctx, runner.ActorID, key, input)
		if err != nil {
			return nil, "", "", err
		}
		target, err := mediaResultID(result)
		return target, "mapped", "", err
	}
	return nil, "invalid", "unsupported_material", nil
}

func mediaKind(table string) string {
	return map[string]string{"image_library": "image", "miniprogram_library": "miniprogram", "attachment_library": "attachment", "group_invite_library": "group_invite"}[table]
}

const legacyAssetUpsertSQL = `INSERT INTO channel_legacy_acquisition_assets(
	import_run_id,source_asset_id,channel_id,config_version,asset_version,kind,
	provider_asset_ref,result_url,source_status,source_digest
) VALUES($1,$2,$3,$4,$5,'contact_way_qrcode',$6,$7,$8,$9)
ON CONFLICT(channel_id,kind,asset_version) DO UPDATE SET
	import_run_id=EXCLUDED.import_run_id,
	source_asset_id=EXCLUDED.source_asset_id,
	config_version=EXCLUDED.config_version,
	provider_asset_ref=EXCLUDED.provider_asset_ref,
	result_url=EXCLUDED.result_url,
	source_status=EXCLUDED.source_status,
	verification_status='legacy_unverified',
	source_digest=EXCLUDED.source_digest,
	provider_readback_digest='',
	verified_at=NULL,
	updated_at=clock_timestamp()
WHERE channel_legacy_acquisition_assets.retired_at IS NULL`

func (runner importRunner) importReferencedTags(ctx context.Context, runID int64, manifest snapshotManifest) (int64, error) {
	var count int64
	groupNames := map[string]string{}
	for _, row := range mustTable(manifest, "wecom_corp_tag_groups").Rows {
		if providerGroup := firstString(row.Payload, "group_id"); providerGroup != "" {
			groupNames[providerGroup] = firstString(row.Payload, "group_name")
		}
	}
	for _, row := range mustTable(manifest, "wecom_corp_tags").Rows {
		provider := firstString(row.Payload, "tag_id")
		if provider == "" {
			continue
		}
		digest := sha256.Sum256([]byte(provider))
		var local *int64
		state := "unresolved"
		var id int64
		err := runner.Pool.Native().QueryRow(ctx, `SELECT tag_id FROM tag_provider_tag_bindings WHERE provider_tag_id=$1`, provider).Scan(&id)
		if err == nil {
			local = &id
			state = "mapped"
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return count, err
		}
		if firstString(row.Payload, "deleted_at") != "" {
			local = nil
			state = "deleted"
		}
		groupName := firstString(row.Payload, "group_name")
		if groupName == "" {
			groupName = groupNames[firstString(row.Payload, "group_id")]
		}
		_, err = runner.Pool.Native().Exec(ctx, `INSERT INTO channel_legacy_tag_maps(import_run_id,provider_tag_id_digest,tag_id,name_snapshot,group_name_snapshot,state) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, runID, digest[:], local, firstString(row.Payload, "tag_name"), groupName, state)
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (runner importRunner) importLegacyAssets(ctx context.Context, runID, repairID int64, manifest snapshotManifest, channelMap map[int64]int64) (int64, error) {
	var count int64
	table := mustTable(manifest, "automation_channel_qrcode_asset")
	for _, row := range table.Rows {
		id, ok := jsonInt(row.Payload, "id")
		sourceChannel, _ := jsonInt(row.Payload, "channel_id")
		channelID := channelMap[sourceChannel]
		if !ok || channelID < 1 {
			continue
		}
		var configVersion int64
		if err := runner.Pool.Native().QueryRow(ctx, `SELECT config_version FROM channel_semantic_repaired_configs WHERE repair_run_id=$1 AND channel_id=$2`, repairID, channelID).Scan(&configVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return count, err
		}
		digest, _ := hex.DecodeString(strings.TrimPrefix(row.Digest, "sha256:"))
		assetVersion := int64(1_000_000_000) + id
		_, err := runner.Pool.Native().Exec(ctx, legacyAssetUpsertSQL, runID, id, channelID, configVersion, assetVersion, firstString(row.Payload, "config_id"), firstString(row.Payload, "qr_url"), nonempty(firstString(row.Payload, "status"), "unknown"), digest)
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

type legacyVerificationResult struct {
	Checked, Verified, Stale, Bindings, ProviderCalls int64
	ProviderEffects                                   int64
}

type semanticGateResult struct {
	ChannelConfigFieldMismatches          int64 `json:"channel_config_field_mismatches"`
	SourceContactsNotCounted              int64 `json:"source_contacts_not_counted"`
	SourceAssigneesNotProjected           int64 `json:"source_assignees_not_projected"`
	ReferencedMaterialsUnmapped           int64 `json:"referenced_materials_unmapped"`
	ReferencedTagsUnmapped                int64 `json:"referenced_tags_unmapped"`
	ActiveLegacyQRWithoutBinding          int64 `json:"active_legacy_qr_without_binding"`
	ActiveLegacyQRWithoutProviderReadback int64 `json:"active_legacy_qr_without_provider_readback"`
	DuplicateSourceMaps                   int64 `json:"duplicate_source_maps"`
	WrongOneIDBindings                    int64 `json:"wrong_oneid_bindings"`
	ProviderEffectsCreatedByImport        int64 `json:"provider_effects_created_by_import"`
	ProviderCallsDuringImport             int64 `json:"provider_calls_during_import"`
	SilentLoss                            int64 `json:"silent_loss"`
	UnresolvedOneIDContacts               int64 `json:"unresolved_oneid_contacts"`
}

func (g semanticGateResult) safeForActivation() bool {
	return g.ChannelConfigFieldMismatches == 0 && g.SourceContactsNotCounted == 0 && g.SourceAssigneesNotProjected == 0 &&
		g.ReferencedMaterialsUnmapped == 0 && g.ReferencedTagsUnmapped == 0 && g.ActiveLegacyQRWithoutBinding == 0 &&
		g.ActiveLegacyQRWithoutProviderReadback == 0 && g.DuplicateSourceMaps == 0 && g.WrongOneIDBindings == 0 &&
		g.ProviderEffectsCreatedByImport == 0 && g.ProviderCallsDuringImport == 0 && g.SilentLoss == 0
}

func (runner importRunner) SemanticReconcile(ctx context.Context, manifest snapshotManifest) (semanticGateResult, error) {
	var gates semanticGateResult
	var runID, repairID int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT r.id,s.id FROM channel_history_import_runs r JOIN channel_semantic_repair_runs s ON s.import_run_id=r.id WHERE r.snapshot_id=$1 AND r.manifest_digest=decode($2,'hex')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&runID, &repairID); err != nil {
		return gates, err
	}
	var repaired int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_semantic_repaired_configs WHERE repair_run_id=$1`, repairID).Scan(&repaired); err != nil {
		return gates, err
	}
	gates.ChannelConfigFieldMismatches = semanticConfigMismatchCount(int64(len(mustTable(manifest, "automation_channel").Rows)), repaired)
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM channel_history_source_maps WHERE import_run_id=$1)-$2,
		(SELECT count(*)-count(DISTINCT (source_table,source_pk)) FROM channel_history_source_maps WHERE import_run_id=$1),
		(SELECT count(*) FROM channel_history_contacts WHERE import_run_id=$1 AND customer_id IS NULL AND NOT EXISTS(SELECT 1 FROM channel_history_contact_reconciliations r WHERE r.history_contact_id=channel_history_contacts.id))`, runID, manifest.rowCount()).Scan(&gates.SilentLoss, &gates.DuplicateSourceMaps, &gates.UnresolvedOneIDContacts); err != nil {
		return gates, err
	}
	expectedContacts := int64(len(mustTable(manifest, "automation_channel_contact").Rows))
	var storedContacts int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_history_contacts WHERE import_run_id=$1`, runID).Scan(&storedContacts); err != nil {
		return gates, err
	}
	gates.SourceContactsNotCounted = expectedContacts - storedContacts
	if gates.SourceContactsNotCounted < 0 {
		gates.SourceContactsNotCounted = 0
	}
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_legacy_material_maps WHERE import_run_id=$1 AND state<>'mapped'`, runID).Scan(&gates.ReferencedMaterialsUnmapped); err != nil {
		return gates, err
	}
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_legacy_tag_maps WHERE import_run_id=$1 AND state<>'mapped'`, runID).Scan(&gates.ReferencedTagsUnmapped); err != nil {
		return gates, err
	}
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE verification_status<>'legacy_verified_active'),
		count(*) FILTER(WHERE verification_status='legacy_verified_active' AND NOT EXISTS(SELECT 1 FROM channel_acquisition_state_bindings b WHERE b.channel_id=a.channel_id AND b.asset_kind='contact_way_qrcode' AND b.asset_version>=2000000000 AND b.active_until IS NULL))
		FROM channel_legacy_acquisition_assets a WHERE import_run_id=$1 AND lower(source_status) IN ('active','enabled','executed') AND retired_at IS NULL`, runID).Scan(&gates.ActiveLegacyQRWithoutProviderReadback, &gates.ActiveLegacyQRWithoutBinding); err != nil {
		return gates, err
	}
	channelMap, err := runner.sourceChannelMap(ctx, runID)
	if err != nil {
		return gates, err
	}
	for sourceChannelID, members := range semanticExpectedAssignees(manifest) {
		channelID := channelMap[sourceChannelID]
		var projected int64
		if channelID > 0 {
			err = runner.Pool.Native().QueryRow(ctx, `SELECT count(*) FROM channel_assignees a JOIN channel_semantic_repaired_configs c ON c.channel_id=a.channel_id AND c.config_version=a.config_version WHERE c.repair_run_id=$1 AND a.channel_id=$2`, repairID, channelID).Scan(&projected)
		}
		if err != nil {
			return gates, err
		}
		var unavailableRepresented bool
		if channelID > 0 {
			err = runner.Pool.Native().QueryRow(ctx, `SELECT blockers ? 'assignee_provider_ineligible' FROM channel_semantic_repaired_configs WHERE repair_run_id=$1 AND channel_id=$2`, repairID, channelID).Scan(&unavailableRepresented)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return gates, err
			}
			err = nil
		}
		gates.SourceAssigneesNotProjected += semanticUnprojectedAssigneeCount(int64(len(members)), projected, unavailableRepresented)
	}
	wrong, err := runner.verifyOneIDBindings(ctx, runID, manifest)
	if err != nil {
		return gates, err
	}
	gates.WrongOneIDBindings = wrong
	return gates, nil
}

func semanticConfigMismatchCount(sourceConfigs, repairedConfigs int64) int64 {
	if sourceConfigs <= repairedConfigs {
		return 0
	}
	return sourceConfigs - repairedConfigs
}

func semanticUnprojectedAssigneeCount(expected, projected int64, unavailableRepresented bool) int64 {
	if projected >= expected || unavailableRepresented {
		return 0
	}
	return expected - projected
}

func semanticAssignmentBlockers(strategy string, assigned []semanticAssignment) []string {
	if len(assigned) == 0 {
		// An empty source assignment is a representable historical fact, not a
		// three-way conflict. Preserve the remaining configuration and keep the
		// channel disabled until an administrator assigns eligible staff.
		return []string{"assignees_missing"}
	}
	if strategy != "ratio" {
		return nil
	}
	sum := 0
	for _, item := range assigned {
		sum += item.ratio
	}
	if sum == 100 {
		return nil
	}
	// Positive source ratios can be stored losslessly even when their sum is
	// invalid for runtime routing. Keep them verbatim, but do not allow the
	// repaired channel to become active.
	return []string{"assignment_ratio_invalid"}
}

func normalizeSemanticPriorities(assigned []semanticAssignment) bool {
	seen := map[int]bool{}
	invalid := len(assigned) > 5
	for _, item := range assigned {
		if item.priority < 1 || item.priority > 5 || seen[item.priority] {
			invalid = true
		}
		seen[item.priority] = true
	}
	if invalid && len(assigned) <= 5 {
		// The immutable history projection keeps the original source priority.
		// The runnable config only needs a deterministic unique order.
		for index := range assigned {
			assigned[index].priority = index + 1
		}
	}
	return invalid
}

func (runner importRunner) VerifyLegacyAssets(ctx context.Context, manifest snapshotManifest, reader legacyAssetReader, digester wecom.StateDigester, corpID string) (legacyVerificationResult, error) {
	if reader == nil || digester == nil || corpID == "" {
		return legacyVerificationResult{}, errors.New("legacy provider readback dependencies unavailable")
	}
	var runID int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT id FROM channel_history_import_runs WHERE snapshot_id=$1 AND manifest_digest=decode($2,'hex')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&runID); err != nil {
		return legacyVerificationResult{}, err
	}
	channelMap, err := runner.sourceChannelMap(ctx, runID)
	if err != nil {
		return legacyVerificationResult{}, err
	}
	result := legacyVerificationResult{}
	for _, row := range mustTable(manifest, "automation_channel_qrcode_asset").Rows {
		id, ok := jsonInt(row.Payload, "id")
		if !ok {
			continue
		}
		configID := firstString(row.Payload, "config_id")
		sourceCorp := firstString(row.Payload, "corp_id")
		if sourceCorp != "" && sourceCorp != corpID {
			return result, errors.New("legacy asset corp scope mismatch")
		}
		result.Checked++
		readback, readErr := reader.GetContactWay(ctx, configID)
		result.ProviderCalls++
		status := "legacy_stale"
		readbackDigest := ""
		if readErr == nil && readback.ProviderAssetRef == configID && readback.URL != "" {
			status = "legacy_verified_active"
			d := sha256.Sum256([]byte(readback.ProviderAssetRef + "\x00" + readback.URL))
			readbackDigest = "sha256:" + hex.EncodeToString(d[:])
			result.Verified++
		} else {
			result.Stale++
		}
		_, err = runner.Pool.Native().Exec(ctx, `UPDATE channel_legacy_acquisition_assets SET verification_status=$3,result_url=CASE WHEN $3='legacy_verified_active' THEN $4 ELSE result_url END,provider_readback_digest=$5,verified_at=clock_timestamp(),updated_at=clock_timestamp() WHERE import_run_id=$1 AND source_asset_id=$2`, runID, id, status, readback.URL, readbackDigest)
		if err != nil {
			return result, err
		}
		if status != "legacy_verified_active" {
			continue
		}
		sourceChannel, _ := jsonInt(row.Payload, "channel_id")
		channelID := channelMap[sourceChannel]
		scenes := legacyScenes(manifest, sourceChannel, firstString(row.Payload, "scene_value"))
		for _, scene := range scenes {
			digest, err := digester.DigestState(corpID, scene.value)
			if err != nil {
				return result, err
			}
			bindingInput := append([]byte("legacy-state\x00"+strconv.FormatInt(channelID, 10)+"\x00"), digest[:]...)
			bindingDigest := sha256.Sum256(bindingInput)
			err = runner.UOW.Within(ctx, func(txctx context.Context) error {
				tx, e := platformpostgres.RequireTransaction(txctx)
				if e != nil {
					return e
				}
				_, e = tx.Exec(txctx, `SELECT pg_advisory_xact_lock(hashtextextended(
					'channel.state-digest:' || $1 || ':1:' || encode($2::bytea, 'hex'), 0
				))`, corpID, digest[:])
				if e != nil {
					return e
				}
				var represented bool
				e = tx.QueryRow(txctx, `SELECT EXISTS(
					SELECT 1 FROM channel_acquisition_state_bindings
					WHERE corp_id=$1 AND digest_key_version=1 AND state_digest=$2
					  AND channel_id=$3 AND asset_kind=$4 AND active_until IS NULL
				)`, corpID, digest[:], channelID, channelstore.AcquisitionAssetQRCode).Scan(&represented)
				if e != nil || represented {
					return e
				}
				assetVersion := int64(2_000_000_000) + scene.id
				var occupied bool
				e = tx.QueryRow(txctx, `SELECT EXISTS(
					SELECT 1 FROM channel_acquisition_state_bindings
					WHERE channel_id=$1 AND asset_kind=$2 AND asset_version=$3
				)`, channelID, channelstore.AcquisitionAssetQRCode, assetVersion).Scan(&occupied)
				if e != nil {
					return e
				}
				if occupied {
					assetVersion = legacyStateBindingAssetVersion(bindingDigest)
				}
				_, _, e = runner.States.PutBinding(txctx, channelstore.StateBinding{CorpID: corpID, DigestKeyVersion: 1, StateDigest: digest, ChannelID: channelID, AssetKind: channelstore.AcquisitionAssetQRCode, AssetVersion: assetVersion, BindingDigest: bindingDigest, ActiveFrom: scene.active})
				return e
			})
			if err != nil {
				return result, err
			}
			result.Bindings++
		}
	}
	return result, nil
}

func legacyStateBindingAssetVersion(bindingDigest [sha256.Size]byte) int64 {
	// Keep migration-owned versions in a separate positive range. The digest
	// makes a changed State for a reused source alias ID deterministic without
	// rewriting the old immutable binding.
	return 4_000_000_000 + int64(binary.BigEndian.Uint64(bindingDigest[:8])&((uint64(1)<<62)-1))
}

const channelActivationSQL = `UPDATE channels
	SET status=$2,
		archived_at=CASE WHEN $2='archived' THEN COALESCE(archived_at,clock_timestamp()) ELSE NULL END,
		updated_at=clock_timestamp()
	WHERE id=$1 AND current_config_version=$3`

type legacyScene struct {
	id     int64
	value  string
	active time.Time
}

func legacyScenes(manifest snapshotManifest, channelID int64, assetScene string) []legacyScene {
	out := []legacyScene{}
	seen := map[string]bool{}
	for _, row := range mustTable(manifest, "automation_channel_scene_alias").Rows {
		parent, _ := jsonInt(row.Payload, "channel_id")
		if parent != channelID || firstString(row.Payload, "status") == "revoked" {
			continue
		}
		value := firstString(row.Payload, "scene_value")
		if value == "" || seen[value] {
			continue
		}
		id, _ := jsonInt(row.Payload, "id")
		active := firstTime(row.Payload, "first_seen_at", "created_at")
		if active.IsZero() {
			active = time.Now().UTC()
		}
		out = append(out, legacyScene{id: id, value: value, active: active})
		seen[value] = true
	}
	if assetScene != "" && !seen[assetScene] {
		out = append(out, legacyScene{id: 9_000_000 + int64(len(out)), value: assetScene, active: time.Now().UTC()})
	}
	return out
}

func (runner importRunner) ActivateRepaired(ctx context.Context, manifest snapshotManifest) (map[string]int64, error) {
	gates, err := runner.SemanticReconcile(ctx, manifest)
	if err != nil {
		return nil, err
	}
	if !gates.safeForActivation() {
		return nil, errors.New("semantic repair final gates are not satisfied")
	}
	var runID, repairID int64
	if err := runner.Pool.Native().QueryRow(ctx, `SELECT r.id,s.id FROM channel_history_import_runs r JOIN channel_semantic_repair_runs s ON s.import_run_id=r.id WHERE r.snapshot_id=$1 AND r.manifest_digest=decode($2,'hex') AND s.state IN ('repaired','activated')`, manifest.SnapshotID, manifest.DigestHex()).Scan(&runID, &repairID); err != nil {
		return nil, err
	}
	var activated, blocked int64
	rows, err := runner.Pool.Native().Query(ctx, `SELECT channel_id,config_version,desired_status,jsonb_array_length(blockers) FROM channel_semantic_repaired_configs WHERE repair_run_id=$1 AND activated_at IS NULL`, repairID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type item struct {
		id, version int64
		status      string
		blockers    int
	}
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.version, &v.status, &v.blockers); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	for _, v := range items {
		if v.blockers > 0 {
			blocked++
			continue
		}
		if v.status == "active" {
			var ready bool
			if err = runner.Pool.Native().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel_acquisition_assets WHERE channel_id=$1 AND state IN ('executed','reconciled') AND retired_at IS NULL) OR EXISTS(SELECT 1 FROM channel_legacy_acquisition_assets WHERE channel_id=$1 AND verification_status='legacy_verified_active' AND retired_at IS NULL)`, v.id).Scan(&ready); err != nil {
				return nil, err
			}
			if !ready {
				blocked++
				continue
			}
		}
		err = runner.UOW.Within(ctx, func(txctx context.Context) error {
			tx, txErr := platformpostgres.RequireTransaction(txctx)
			if txErr != nil {
				return txErr
			}
			updated, txErr := tx.Exec(txctx, channelActivationSQL, v.id, v.status, v.version)
			if txErr != nil {
				return txErr
			}
			if updated.RowsAffected() != 1 {
				return errors.New("channel changed concurrently during semantic activation")
			}
			marked, txErr := tx.Exec(txctx, `UPDATE channel_semantic_repaired_configs SET activated_at=clock_timestamp() WHERE repair_run_id=$1 AND channel_id=$2 AND activated_at IS NULL`, repairID, v.id)
			if txErr != nil {
				return txErr
			}
			if marked.RowsAffected() != 1 {
				return errors.New("semantic activation receipt changed concurrently")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		activated++
	}
	state := "activated"
	if blocked > 0 {
		state = "blocked"
	}
	_, err = runner.Pool.Native().Exec(ctx, `UPDATE channel_semantic_repair_runs SET state=$2,completed_at=clock_timestamp() WHERE id=$1`, repairID, state)
	return map[string]int64{"activated": activated, "blocked": blocked, "provider_calls": 0, "provider_effects": 0}, err
}

func jsonIDs(raw json.RawMessage, key string) []int64 {
	data, ok := decodeObject(raw)[key]
	if !ok {
		return []int64{}
	}
	var ids []int64
	if json.Unmarshal(data, &ids) == nil {
		return ids
	}
	var stringsIDs []string
	if json.Unmarshal(data, &stringsIDs) == nil {
		for _, v := range stringsIDs {
			if id, e := strconv.ParseInt(v, 10, 64); e == nil && id > 0 {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
func jsonBool(raw json.RawMessage, key string) bool { return jsonBoolDefault(raw, key, false) }
func jsonBoolDefault(raw json.RawMessage, key string, fallback bool) bool {
	data, ok := decodeObject(raw)[key]
	if !ok {
		return fallback
	}
	var v bool
	if json.Unmarshal(data, &v) != nil {
		return fallback
	}
	return v
}
func decodeBase64Field(raw json.RawMessage, key string) ([]byte, error) {
	value := firstString(raw, key)
	if value == "" {
		return nil, nil
	}
	if i := strings.Index(value, ","); strings.HasPrefix(value, "data:") && i >= 0 {
		value = value[i+1:]
	}
	return base64.StdEncoding.DecodeString(value)
}
func nonempty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func mediaResultID(result map[string]any) (int64, error) {
	switch value := result["id"].(type) {
	case int64:
		if value > 0 {
			return value, nil
		}
	case int:
		if value > 0 {
			return int64(value), nil
		}
	case float64:
		id := int64(value)
		if id > 0 && float64(id) == value {
			return id, nil
		}
	}
	return 0, errors.New("media owner returned invalid id")
}
