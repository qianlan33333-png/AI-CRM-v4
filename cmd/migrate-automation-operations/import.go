// Package migration imports the encrypted legacy Automation Operations
// snapshot. It is a one-time command dependency and is never imported by the
// runtime composition root.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type Buckets struct {
	Source     int `json:"source"`
	Imported   int `json:"imported"`
	Duplicate  int `json:"duplicate"`
	Mapped     int `json:"mapped"`
	Unresolved int `json:"unresolved"`
	Conflict   int `json:"conflict"`
	Invalid    int `json:"invalid"`
	Quarantine int `json:"quarantine"`
}

type Report struct {
	BatchKey               string             `json:"batch_key"`
	DryRun                 bool               `json:"dry_run"`
	Tables                 map[string]Buckets `json:"tables"`
	ProviderEffectsCreated int64              `json:"provider_effects_created"`
	RiverJobsCreated       int64              `json:"river_jobs_created"`
}

type Dependencies struct {
	ActorID  int64
	Identity identityport.Resolver
	Access   accessport.Repository
}

type groupRow struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type packageRow struct {
	ID          int64           `json:"id"`
	GroupID     *int64          `json:"group_id"`
	Lifecycle   string          `json:"lifecycle"`
	Version     int64           `json:"version"`
	Name        string          `json:"name"`
	Definition  json.RawMessage `json:"definition"`
	RefreshMode string          `json:"refresh_mode"`
	RefreshCron *string         `json:"refresh_cron"`
	MemberCount int64           `json:"member_count"`
	RefreshedAt *time.Time      `json:"refreshed_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
type configRow struct {
	PackageID, Version int64
	Definition         json.RawMessage `json:"definition"`
	RefreshMode        string          `json:"refresh_mode"`
	RefreshCron        *string         `json:"refresh_cron"`
	CreatedAt          time.Time       `json:"created_at"`
	refreshModePresent bool
}

func (r *configRow) UnmarshalJSON(raw []byte) error {
	var x struct {
		PackageID   int64           `json:"package_id"`
		Version     int64           `json:"version"`
		Definition  json.RawMessage `json:"definition"`
		RefreshMode string          `json:"refresh_mode"`
		RefreshCron *string         `json:"refresh_cron"`
		CreatedAt   time.Time       `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return err
	}
	r.PackageID, r.Version, r.Definition, r.RefreshMode, r.RefreshCron, r.CreatedAt = x.PackageID, x.Version, x.Definition, x.RefreshMode, x.RefreshCron, x.CreatedAt
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	_, r.refreshModePresent = fields["refresh_mode"]
	return nil
}

// importedRefresh is the closed v3 schedule fact carried by an immutable
// configuration source row. The donor's four durable modes are mapped exactly;
// unknown source values never become a guessed manual configuration.
type importedRefresh struct {
	Mode string
	Cron string
}

func frozenRefreshConfiguration(row configRow) (importedRefresh, error) {
	cron := ""
	if row.RefreshCron != nil {
		cron = *row.RefreshCron
	}
	mode := row.RefreshMode
	if !row.refreshModePresent {
		// Frozen snapshots predating the explicit donor mode have no authority
		// to call the configuration manual. Preserve their historical schedule
		// shape as legacy_custom instead; this is also how prior imports stored
		// an absent source mode.
		mode = "legacy_custom"
	}
	var result importedRefresh
	switch mode {
	case "manual":
		result = importedRefresh{Mode: "manual"}
	case "incremental_3m":
		result = importedRefresh{Mode: "every_3m"}
	case "daily_0200":
		result = importedRefresh{Mode: "daily_0200"}
	case "incremental_3m_plus_daily_0200":
		result = importedRefresh{Mode: "every_3m_plus_daily_0200"}
	case "scheduled", "legacy_custom":
		result = importedRefresh{Mode: "legacy_custom", Cron: cron}
	default:
		return importedRefresh{}, fmt.Errorf("unknown frozen refresh mode %q", row.RefreshMode)
	}
	if result.Mode != "legacy_custom" && cron != "" {
		return importedRefresh{}, fmt.Errorf("frozen refresh mode %q has an unexpected cron", row.RefreshMode)
	}
	if err := segmentapp.ValidateRefresh(result.Mode, result.Cron); err != nil {
		return importedRefresh{}, fmt.Errorf("frozen refresh mode %q: %w", row.RefreshMode, err)
	}
	return result, nil
}

type agentRow struct {
	ID                  int64           `json:"id"`
	AgentName           string          `json:"agent_name"`
	AgentCode           string          `json:"agent_code"`
	AutomationType      string          `json:"automation_type"`
	Status              string          `json:"status"`
	DraftRolePrompt     string          `json:"draft_role_prompt"`
	DraftTaskPrompt     string          `json:"draft_task_prompt"`
	PublishedRolePrompt string          `json:"published_role_prompt"`
	PublishedTaskPrompt string          `json:"published_task_prompt"`
	DraftVersion        int64           `json:"draft_version"`
	PublishedVersion    int64           `json:"published_version"`
	Fixed               json.RawMessage `json:"fixed_content_package_json"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}
type bindingRow struct {
	PackageID int64     `json:"package_id"`
	AgentID   int64     `json:"automation_agent_id"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type senderRow struct {
	PackageID int64     `json:"package_id"`
	UserID    string    `json:"sender_userid"`
	SortOrder int16     `json:"sort_order"`
	Enabled   bool      `json:"is_enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type identityRow struct{ Kind, Scope, Value, Assurance, Source string }

func (r *identityRow) UnmarshalJSON(raw []byte) error {
	var x struct {
		Kind      string `json:"kind"`
		Scope     string `json:"scope"`
		Value     string `json:"value"`
		Assurance string `json:"assurance"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return err
	}
	r.Kind, r.Scope, r.Value, r.Assurance, r.Source = x.Kind, x.Scope, x.Value, x.Assurance, x.Source
	return nil
}

type memberRow struct {
	SegmentID  int64         `json:"segment_id"`
	CustomerID int64         `json:"customer_id"`
	ComputedAt time.Time     `json:"computed_at"`
	Identities []identityRow `json:"identities"`
}

type importer struct {
	ctx                            context.Context
	tx                             pgx.Tx
	snapshot                       segmentmigration.Snapshot
	batchID, actor                 int64
	segmentActor                   segmentport.MutationActor
	report                         Report
	groupMap, packageMap, agentMap map[int64]int64
	identity                       identityport.Resolver
	access                         accessport.Repository
}

func Import(ctx context.Context, pool *pgxpool.Pool, snapshot segmentmigration.Snapshot, dependencies Dependencies, dryRun bool) (Report, error) {
	if pool == nil || dependencies.ActorID < 1 || dependencies.Identity == nil || dependencies.Access == nil {
		return Report{}, errors.New("target database and migration dependencies are required")
	}
	if err := segmentmigration.ValidateSnapshot(snapshot); err != nil {
		return Report{}, err
	}
	manifestRaw, _ := json.Marshal(snapshot.Manifest)
	manifestDigest := sha256.Sum256(manifestRaw)
	batchKey := "automationops-v2-" + snapshot.Manifest.SnapshotAt.UTC().Format("20060102T150405.000000000Z")
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	txContext := platformpostgres.BindTransaction(ctx, tx)
	beforeEffects, beforeJobs, err := safetyCounts(txContext, tx)
	if err != nil {
		return Report{}, err
	}
	var batchID int64
	var priorDigest []byte
	var priorStatus string
	err = tx.QueryRow(ctx, `SELECT id,manifest_digest,status FROM automation_operations_migration_batches WHERE batch_key=$1 FOR UPDATE`, batchKey).Scan(&batchID, &priorDigest, &priorStatus)
	if err == nil {
		if len(priorDigest) != 32 || string(priorDigest) != string(manifestDigest[:]) {
			return Report{}, errors.New("existing migration batch payload drift")
		}
		report, loadErr := loadReport(ctx, tx, batchID, batchKey, dryRun, snapshot.Manifest.Counts)
		return report, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Report{}, err
	}
	watermark, err := hex.DecodeString(snapshot.Manifest.SourceWatermarkDigest)
	if err != nil || len(watermark) != 32 {
		return Report{}, errors.New("invalid source watermark")
	}
	err = tx.QueryRow(ctx, `INSERT INTO automation_operations_migration_batches(batch_key,source_system,donor_commit,snapshot_at,source_watermark_digest,manifest,manifest_digest,provider_effect_count_before,river_job_count_before,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,'importing',clock_timestamp(),clock_timestamp()) RETURNING id`, batchKey, snapshot.Manifest.SourceSystem, snapshot.Manifest.DonorCommit, snapshot.Manifest.SnapshotAt, watermark, manifestRaw, manifestDigest[:], beforeEffects, beforeJobs).Scan(&batchID)
	if err != nil {
		return Report{}, err
	}
	actor, err := dependencies.Access.UserByID(txContext, dependencies.ActorID, false)
	if err != nil || !actor.Active {
		return Report{}, errors.New("migration actor is not an active administrator")
	}
	// Attribute imported V3 configuration to the verified target administrator
	// running this command, never to an invented legacy or machine principal.
	segmentActor, err := segmentport.AdminMutationActor(dependencies.ActorID)
	if err != nil {
		return Report{}, err
	}
	i := &importer{ctx: txContext, tx: tx, snapshot: snapshot, batchID: batchID, actor: dependencies.ActorID, segmentActor: segmentActor, report: Report{BatchKey: batchKey, DryRun: dryRun, Tables: map[string]Buckets{}}, groupMap: map[int64]int64{}, packageMap: map[int64]int64{}, agentMap: map[int64]int64{}, identity: dependencies.Identity, access: dependencies.Access}
	for _, step := range []func() error{i.agents, i.groups, i.packagesAndConfigurations, i.bindings, i.senders, i.snapshots, i.history} {
		if err = step(); err != nil {
			return Report{}, err
		}
	}
	for name, expected := range snapshot.Manifest.Counts {
		bucket := i.report.Tables[name]
		if bucket.Source != expected || bucket.Source != bucket.Imported+bucket.Duplicate+bucket.Mapped+bucket.Unresolved+bucket.Conflict+bucket.Invalid+bucket.Quarantine {
			return Report{}, fmt.Errorf("count equation failed for %s", name)
		}
	}
	afterEffects, afterJobs, err := safetyCounts(txContext, tx)
	if err != nil {
		return Report{}, err
	}
	i.report.ProviderEffectsCreated, i.report.RiverJobsCreated = afterEffects-beforeEffects, afterJobs-beforeJobs
	if i.report.ProviderEffectsCreated != 0 || i.report.RiverJobsCreated != 0 {
		return Report{}, errors.New("migration attempted to create effects or jobs")
	}
	if _, err = tx.Exec(ctx, `UPDATE automation_operations_migration_batches SET status='imported',provider_effect_count_after=$2,river_job_count_after=$3,updated_at=clock_timestamp() WHERE id=$1`, batchID, afterEffects, afterJobs); err != nil {
		return Report{}, err
	}
	if dryRun {
		return i.report, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return i.report, nil
}

func safetyCounts(ctx context.Context, tx pgx.Tx) (int64, int64, error) {
	var effects, jobs int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil {
		return 0, 0, err
	}
	return effects, jobs, nil
}

func decodeRows[T any](snapshot segmentmigration.Snapshot, name string) ([]T, []json.RawMessage, error) {
	var rawRows []json.RawMessage
	if err := json.Unmarshal(snapshot.Tables[name], &rawRows); err != nil {
		return nil, nil, err
	}
	values := make([]T, len(rawRows))
	for index := range rawRows {
		if err := json.Unmarshal(rawRows[index], &values[index]); err != nil {
			return nil, nil, fmt.Errorf("decode %s row %d", name, index)
		}
	}
	return values, rawRows, nil
}
func recordDigest(raw json.RawMessage) [32]byte { return sha256.Sum256(raw) }
func sourcePK(snapshot segmentmigration.Snapshot, key string) string {
	return snapshot.Manifest.SourceWatermarkDigest[:16] + ":" + key
}

func (i *importer) add(name, disposition string) {
	b := i.report.Tables[name]
	b.Source++
	switch disposition {
	case "imported":
		b.Imported++
	case "duplicate":
		b.Duplicate++
	case "mapped":
		b.Mapped++
	case "unresolved":
		b.Unresolved++
	case "conflict":
		b.Conflict++
	case "invalid":
		b.Invalid++
	default:
		b.Quarantine++
	}
	i.report.Tables[name] = b
}
func (i *importer) mapRow(name, pk, target string, targetID *int64, digest [32]byte, disposition string) error {
	_, err := i.tx.Exec(i.ctx, `INSERT INTO automation_operations_migration_source_map(batch_id,source_system,source_table,source_pk,target_table,target_pk,record_digest,disposition,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp())`, i.batchID, i.snapshot.Manifest.SourceSystem, name, sourcePK(i.snapshot, pk), target, targetID, digest[:], disposition)
	if err == nil {
		i.add(name, disposition)
	}
	return err
}
func (i *importer) quarantine(name, pk, reason string, digest [32]byte, summary map[string]any, disposition string) error {
	raw, _ := json.Marshal(summary)
	if _, err := i.tx.Exec(i.ctx, `INSERT INTO automation_operations_migration_quarantine(batch_id,source_system,source_table,source_pk,reason_code,safe_summary,record_digest,created_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,clock_timestamp())`, i.batchID, i.snapshot.Manifest.SourceSystem, name, sourcePK(i.snapshot, pk), reason, raw, digest[:]); err != nil {
		return err
	}
	return i.mapRow(name, pk, "", nil, digest, disposition)
}

func (i *importer) agents() error {
	rows, raws, err := decodeRows[agentRow](i.snapshot, "automation_agents")
	if err != nil {
		return err
	}
	for index, row := range rows {
		digest := recordDigest(raws[index])
		pk := strconv.FormatInt(row.ID, 10)
		if row.ID < 1 || row.AgentCode == "" || row.PublishedVersion < 1 {
			if err = i.quarantine("automation_agents", pk, "invalid_agent", digest, map[string]any{"source_id": row.ID}, "invalid"); err != nil {
				return err
			}
			continue
		}
		var fixed automationport.FixedContentPackage
		if json.Unmarshal(row.Fixed, &fixed) != nil {
			if err = i.quarantine("automation_agents", pk, "invalid_fixed_content", digest, map[string]any{"source_id": row.ID}, "invalid"); err != nil {
				return err
			}
			continue
		}
		sourceMaterials, _ := json.Marshal(map[string]any{"images": fixed.ImageLibraryIDs, "mini_programs": fixed.MiniprogramLibraryIDs, "attachments": fixed.AttachmentLibraryIDs, "group_invites": fixed.GroupInviteLibraryIDs, "dynamic_card": fixed.DynamicMiniprogramCard})
		sourceMaterialsDigest := sha256.Sum256(sourceMaterials)
		// Donor-local media IDs have no authority in v3 and could alias unrelated
		// target rows. Keep only their digest as historical evidence; an operator
		// must select and validate v3-owned materials before activation.
		fixed.ImageLibraryIDs = nil
		fixed.MiniprogramLibraryIDs = nil
		fixed.AttachmentLibraryIDs = nil
		fixed.GroupInviteLibraryIDs = nil
		fixed.DynamicMiniprogramCard = nil
		fixedRaw, _ := json.Marshal(fixed)
		legacy, _ := json.Marshal(map[string]any{"migration_source": "ai-crm-v2", "source_id": row.ID, "source_status": row.Status, "source_materials_digest": hex.EncodeToString(sourceMaterialsDigest[:])})
		var targetID int64
		var existingType string
		var existingVersion int64
		var existingRole, existingTask string
		var existingFixed []byte
		e := i.tx.QueryRow(i.ctx, `SELECT id,automation_type,published_version,published_role_prompt,published_task_prompt,fixed_content_package FROM automation_agents WHERE agent_code=$1`, row.AgentCode).Scan(&targetID, &existingType, &existingVersion, &existingRole, &existingTask, &existingFixed)
		disposition := "imported"
		if errors.Is(e, pgx.ErrNoRows) {
			e = i.tx.QueryRow(i.ctx, `INSERT INTO automation_agents(agent_name,agent_code,automation_type,status,execution_enabled,draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt,draft_version,published_version,fixed_content_package,legacy_configuration,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,'paused',false,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12,$12,$13,$14) RETURNING id`, row.AgentName, row.AgentCode, row.AutomationType, row.DraftRolePrompt, row.DraftTaskPrompt, row.PublishedRolePrompt, row.PublishedTaskPrompt, row.DraftVersion, row.PublishedVersion, fixedRaw, legacy, i.actor, row.CreatedAt, row.UpdatedAt).Scan(&targetID)
		} else if e == nil {
			var existing automationport.FixedContentPackage
			if json.Unmarshal(existingFixed, &existing) != nil || existingType != row.AutomationType || existingVersion != row.PublishedVersion || existingRole != row.PublishedRolePrompt || existingTask != row.PublishedTaskPrompt || !sameJSON(existingFixed, fixedRaw) {
				if err = i.quarantine("automation_agents", pk, "agent_code_digest_conflict", digest, map[string]any{"source_id": row.ID, "agent_code": row.AgentCode}, "conflict"); err != nil {
					return err
				}
				continue
			}
			disposition = "mapped"
		}
		if e != nil {
			return e
		}
		i.agentMap[row.ID] = targetID
		if err = i.mapRow("automation_agents", pk, "automation_agents", &targetID, digest, disposition); err != nil {
			return err
		}
	}
	return nil
}

func (i *importer) groups() error {
	rows, raws, err := decodeRows[groupRow](i.snapshot, "audience_groups")
	if err != nil {
		return err
	}
	for index, row := range rows {
		digest := recordDigest(raws[index])
		pk := strconv.FormatInt(row.ID, 10)
		var targetID int64
		e := i.tx.QueryRow(i.ctx, `SELECT id FROM segment_audience_groups WHERE lower(btrim(name))=lower(btrim($1))`, row.Name).Scan(&targetID)
		disposition := "imported"
		if errors.Is(e, pgx.ErrNoRows) {
			e = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_groups(name,sort_order,version,created_by,updated_by,created_at,updated_at,created_actor_kind,created_actor_ref,updated_actor_kind,updated_actor_ref) VALUES($1,$2,1,$3,$3,$4,$5,$6,$7,$6,$7) RETURNING id`, row.Name, row.SortOrder, i.actor, row.CreatedAt, row.UpdatedAt, i.segmentActor.Kind, i.segmentActor.Reference).Scan(&targetID)
		} else if e == nil {
			disposition = "mapped"
		}
		if e != nil {
			return e
		}
		i.groupMap[row.ID] = targetID
		if err = i.mapRow("audience_groups", pk, "segment_audience_groups", &targetID, digest, disposition); err != nil {
			return err
		}
	}
	return nil
}

func (i *importer) packagesAndConfigurations() error {
	packages, packageRaws, err := decodeRows[packageRow](i.snapshot, "audience_packages")
	if err != nil {
		return err
	}
	configs, configRaws, err := decodeRows[configRow](i.snapshot, "audience_configuration_versions")
	if err != nil {
		return err
	}
	configsByPackage := map[int64][]int{}
	for index, c := range configs {
		configsByPackage[c.PackageID] = append(configsByPackage[c.PackageID], index)
	}
	for index, row := range packages {
		digest := recordDigest(packageRaws[index])
		pk := strconv.FormatInt(row.ID, 10)
		code := "v2-audience-" + strconv.FormatInt(row.ID, 10)
		var targetID int64
		var groupID *int64
		if row.GroupID != nil {
			if mapped, ok := i.groupMap[*row.GroupID]; ok {
				groupID = &mapped
			}
		}
		e := i.tx.QueryRow(i.ctx, `SELECT id FROM segment_audience_packages WHERE code=$1`, code).Scan(&targetID)
		disposition := "imported"
		if errors.Is(e, pgx.ErrNoRows) {
			archived := row.Lifecycle == "archived"
			e = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_packages(group_id,code,name,lifecycle,version,created_by,updated_by,created_at,updated_at,archived_at,created_actor_kind,created_actor_ref,updated_actor_kind,updated_actor_ref) VALUES($1,$2,$3,$4,1,$5,$5,$6::timestamptz,$7::timestamptz,CASE WHEN $4='archived' THEN $7::timestamptz ELSE NULL::timestamptz END,$8,$9,$8,$9) RETURNING id`, groupID, code, row.Name, map[bool]string{true: "archived", false: "paused"}[archived], i.actor, row.CreatedAt, row.UpdatedAt, i.segmentActor.Kind, i.segmentActor.Reference).Scan(&targetID)
		} else if e == nil {
			disposition = "mapped"
		}
		if e != nil {
			return e
		}
		i.packageMap[row.ID] = targetID
		if err = i.mapRow("audience_packages", pk, "segment_audience_packages", &targetID, digest, disposition); err != nil {
			return err
		}
		indices := configsByPackage[row.ID]
		if len(indices) == 0 {
			return fmt.Errorf("source package %d has no immutable configuration version", row.ID)
		}
		sort.Slice(indices, func(a, b int) bool { return configs[indices[a]].Version < configs[indices[b]].Version })
		var currentID int64
		for _, configIndex := range indices {
			c := configs[configIndex]
			raw := configRaws[configIndex]
			cd := recordDigest(raw)
			cpk := fmt.Sprintf("%d:%d", c.PackageID, c.Version)
			var object map[string]json.RawMessage
			if json.Unmarshal(c.Definition, &object) != nil || object == nil {
				if err = i.quarantine("audience_configuration_versions", cpk, "invalid_definition", cd, map[string]any{"package_source_id": row.ID, "source_version": c.Version}, "invalid"); err != nil {
					return err
				}
				continue
			}
			definition := compactJSON(c.Definition)
			definitionDigest := sha256.Sum256(definition)
			refresh, refreshErr := frozenRefreshConfiguration(c)
			if refreshErr != nil {
				return fmt.Errorf("source configuration %s refresh: %w", cpk, refreshErr)
			}
			var existingID int64
			e = i.tx.QueryRow(i.ctx, `SELECT id FROM segment_audience_configuration_versions WHERE package_id=$1 AND digest=$2 AND refresh_mode=$3 AND COALESCE(refresh_cron_utc,'')=$4 ORDER BY version DESC LIMIT 1`, targetID, definitionDigest[:], refresh.Mode, refresh.Cron).Scan(&existingID)
			cdisp := "imported"
			if errors.Is(e, pgx.ErrNoRows) {
				var next int64
				if e = i.tx.QueryRow(i.ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_configuration_versions WHERE package_id=$1`, targetID).Scan(&next); e == nil {
					e = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,refresh_cron_utc,refresh_mode,digest,created_by,created_at,created_actor_kind,created_actor_ref) VALUES($1,$2,1,$3::jsonb,NULLIF($4,''),$5,$6,$7,$8::timestamptz,$9,$10) RETURNING id`, targetID, next, definition, refresh.Cron, refresh.Mode, definitionDigest[:], i.actor, c.CreatedAt, i.segmentActor.Kind, i.segmentActor.Reference).Scan(&existingID)
				}
			} else if e == nil {
				cdisp = "duplicate"
			}
			if e != nil {
				return e
			}
			currentID = existingID
			if err = i.mapRow("audience_configuration_versions", cpk, "segment_audience_configuration_versions", &existingID, cd, cdisp); err != nil {
				return err
			}
		}
		if currentID < 1 {
			return fmt.Errorf("package %d has no importable configuration", row.ID)
		}
		if _, err = i.tx.Exec(i.ctx, `UPDATE segment_audience_packages SET current_configuration_version_id=$2,updated_at=GREATEST(updated_at,$3) WHERE id=$1`, targetID, currentID, row.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (i *importer) bindings() error {
	rows, raws, err := decodeRows[bindingRow](i.snapshot, "audience_bindings")
	if err != nil {
		return err
	}
	for index, row := range rows {
		digest := recordDigest(raws[index])
		pk := strconv.FormatInt(row.PackageID, 10)
		packageID, pok := i.packageMap[row.PackageID]
		agentID, aok := i.agentMap[row.AgentID]
		if !pok || !aok {
			if err = i.quarantine("audience_bindings", pk, "binding_reference_unresolved", digest, map[string]any{"package_source_id": row.PackageID, "agent_source_id": row.AgentID}, "unresolved"); err != nil {
				return err
			}
			continue
		}
		agent, err := loadAgentDigests(i.ctx, i.tx, agentID)
		if err != nil {
			return err
		}
		var id int64
		err = i.tx.QueryRow(i.ctx, `SELECT id FROM segment_audience_automation_binding_versions WHERE package_id=$1 AND agent_id=$2 AND agent_published_version=$3 AND content_digest=$4 AND materials_digest=$5 ORDER BY version DESC LIMIT 1`, packageID, agentID, agent.version, agent.content[:], agent.materials[:]).Scan(&id)
		disp := "imported"
		if errors.Is(err, pgx.ErrNoRows) {
			var next int64
			if err = i.tx.QueryRow(i.ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_automation_binding_versions WHERE package_id=$1`, packageID).Scan(&next); err == nil {
				err = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_automation_binding_versions(package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_at,created_actor_kind,created_actor_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, packageID, next, agentID, agent.kind, agent.version, agent.content[:], agent.materials[:], i.actor, row.CreatedAt, i.segmentActor.Kind, i.segmentActor.Reference).Scan(&id)
			}
		} else if err == nil {
			disp = "duplicate"
		}
		if err != nil {
			return err
		}
		if _, err = i.tx.Exec(i.ctx, `UPDATE segment_audience_packages SET current_automation_binding_id=$2 WHERE id=$1`, packageID, id); err != nil {
			return err
		}
		if err = i.mapRow("audience_bindings", pk, "segment_audience_automation_binding_versions", &id, digest, disp); err != nil {
			return err
		}
	}
	return nil
}

func (i *importer) senders() error {
	rows, raws, err := decodeRows[senderRow](i.snapshot, "audience_senders")
	if err != nil {
		return err
	}
	byPackage := map[int64][]int{}
	for index, row := range rows {
		byPackage[row.PackageID] = append(byPackage[row.PackageID], index)
	}
	for sourcePackage, indices := range byPackage {
		packageID, ok := i.packageMap[sourcePackage]
		if !ok {
			for _, index := range indices {
				row := rows[index]
				if err = i.quarantine("audience_senders", fmt.Sprintf("%d:%d", row.PackageID, row.SortOrder), "sender_package_unresolved", recordDigest(raws[index]), map[string]any{"package_source_id": row.PackageID, "sort_order": row.SortOrder}, "unresolved"); err != nil {
					return err
				}
			}
			continue
		}
		type mappedSender struct {
			index          int
			staff, version int64
			refreshed      time.Time
		}
		mapped := []mappedSender{}
		complete := true
		for _, index := range indices {
			row := rows[index]
			pk := fmt.Sprintf("%d:%d", row.PackageID, row.SortOrder)
			if !row.Enabled {
				if err = i.mapRow("audience_senders", pk, "", nil, recordDigest(raws[index]), "duplicate"); err != nil {
					return err
				}
				continue
			}
			user, e := i.access.UserByWeComUserID(i.ctx, row.UserID, false)
			if errors.Is(e, accessdomain.ErrNotFound) || e == nil && !user.Active {
				complete = false
				if err = i.quarantine("audience_senders", pk, "sender_identity_unresolved", recordDigest(raws[index]), map[string]any{"package_source_id": row.PackageID, "sort_order": row.SortOrder}, "unresolved"); err != nil {
					return err
				}
				continue
			}
			if e != nil {
				return e
			}
			mapped = append(mapped, mappedSender{index: index, staff: user.ID, version: user.SessionVersion, refreshed: user.UpdatedAt})
		}
		if !complete || len(mapped) == 0 {
			for _, m := range mapped {
				row := rows[m.index]
				if err = i.quarantine("audience_senders", fmt.Sprintf("%d:%d", row.PackageID, row.SortOrder), "sender_set_incomplete", recordDigest(raws[m.index]), map[string]any{"package_source_id": row.PackageID, "sort_order": row.SortOrder}, "quarantine"); err != nil {
					return err
				}
			}
			continue
		}
		var setID int64
		var next int64
		if err = i.tx.QueryRow(i.ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_sender_sets WHERE package_id=$1`, packageID).Scan(&next); err != nil {
			return err
		}
		if err = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_sender_sets(package_id,version,created_by,created_at,created_actor_kind,created_actor_ref) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, packageID, next, i.actor, i.snapshot.Manifest.SnapshotAt, i.segmentActor.Kind, i.segmentActor.Reference).Scan(&setID); err != nil {
			return err
		}
		sort.Slice(mapped, func(a, b int) bool { return rows[mapped[a].index].SortOrder < rows[mapped[b].index].SortOrder })
		for position, m := range mapped {
			if _, err = i.tx.Exec(i.ctx, `INSERT INTO segment_audience_sender_set_members(sender_set_id,sort_order,staff_id,eligibility_version,eligibility_refreshed_at) VALUES($1,$2,$3,$4,$5)`, setID, position+1, m.staff, m.version, m.refreshed); err != nil {
				return err
			}
			row := rows[m.index]
			if err = i.mapRow("audience_senders", fmt.Sprintf("%d:%d", row.PackageID, row.SortOrder), "segment_audience_sender_set_members", &setID, recordDigest(raws[m.index]), "mapped"); err != nil {
				return err
			}
		}
		if _, err = i.tx.Exec(i.ctx, `UPDATE segment_audience_packages SET current_sender_set_id=$2 WHERE id=$1`, packageID, setID); err != nil {
			return err
		}
	}
	return nil
}

func (i *importer) snapshots() error {
	rows, raws, err := decodeRows[memberRow](i.snapshot, "audience_members")
	if err != nil {
		return err
	}
	byPackage := map[int64][]int{}
	for index, row := range rows {
		byPackage[row.SegmentID] = append(byPackage[row.SegmentID], index)
	}
	for sourcePackage, packageID := range i.packageMap {
		indices := byPackage[sourcePackage]
		ids := []int64{}
		mappedRows := map[int]int64{}
		for _, index := range indices {
			row := rows[index]
			target, disposition, e := resolveOneID(i.ctx, i.identity, row.Identities)
			if e != nil {
				return e
			}
			pk := fmt.Sprintf("%d:%d", row.SegmentID, row.CustomerID)
			if disposition != "mapped" {
				if err = i.quarantine("audience_members", pk, "oneid_"+disposition, recordDigest(raws[index]), map[string]any{"package_source_id": row.SegmentID, "identity_count": len(row.Identities)}, disposition); err != nil {
					return err
				}
				continue
			}
			mappedRows[index] = target
			ids = append(ids, target)
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		uniqueIDs := ids[:0]
		for _, id := range ids {
			if len(uniqueIDs) == 0 || uniqueIDs[len(uniqueIDs)-1] != id {
				uniqueIDs = append(uniqueIDs, id)
			}
		}
		var configID int64
		if err = i.tx.QueryRow(i.ctx, `SELECT current_configuration_version_id FROM segment_audience_packages WHERE id=$1`, packageID).Scan(&configID); err != nil {
			return err
		}
		sourceKey := sha256.Sum256([]byte("migration-snapshot:" + i.snapshot.Manifest.SourceWatermarkDigest + ":" + strconv.FormatInt(sourcePackage, 10)))
		var runID int64
		if err = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_refresh_runs(package_id,configuration_version_id,source_key_digest,reference_time,state,created_at,updated_at,completed_at) VALUES($1,$2,$3,$4,'published',$4,$4,$4) RETURNING id`, packageID, configID, sourceKey[:], i.snapshot.Manifest.SnapshotAt).Scan(&runID); err != nil {
			return err
		}
		customerIDs := make([]customerdomain.CustomerID, len(uniqueIDs))
		for index, id := range uniqueIDs {
			customerIDs[index] = customerdomain.CustomerID(id)
		}
		memberDigest := segmentdomain.DigestMembers(customerIDs)
		watermark := sha256.Sum256([]byte(i.snapshot.Manifest.SourceWatermarkDigest + ":" + strconv.FormatInt(sourcePackage, 10)))
		var snapshotID int64
		if err = i.tx.QueryRow(i.ctx, `INSERT INTO segment_audience_snapshots(package_id,configuration_version_id,refresh_run_id,state,reference_time,member_count,member_digest,source_watermark_digest,created_at,published_at) VALUES($1,$2,$3,'published',$4,$5,$6,$7,$4,$4) RETURNING id`, packageID, configID, runID, i.snapshot.Manifest.SnapshotAt, len(uniqueIDs), memberDigest[:], watermark[:]).Scan(&snapshotID); err != nil {
			return err
		}
		for _, id := range uniqueIDs {
			if _, err = i.tx.Exec(i.ctx, `INSERT INTO segment_audience_snapshot_members(snapshot_id,customer_id,entered_at,identity_disposition) VALUES($1,$2,$3,'resolved')`, snapshotID, id, i.snapshot.Manifest.SnapshotAt); err != nil {
				return err
			}
		}
		if _, err = i.tx.Exec(i.ctx, `UPDATE segment_audience_packages SET published_snapshot_id=$2 WHERE id=$1`, packageID, snapshotID); err != nil {
			return err
		}
		for index, target := range mappedRows {
			row := rows[index]
			// A snapshot member is identified by (snapshot_id, customer_id), so
			// storing the snapshot ID alone cannot prove which canonical OneID was
			// selected. Persist the canonical customer ID for shadow comparison;
			// the immutable source key already carries the source package/customer.
			if err = i.mapRow("audience_members", fmt.Sprintf("%d:%d", row.SegmentID, row.CustomerID), "segment_audience_snapshot_members.customer_id", &target, recordDigest(raws[index]), "mapped"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *importer) history() error {
	for _, name := range []string{"automation_policies", "automation_policy_versions", "automation_enrollments", "automation_actions"} {
		var rows []json.RawMessage
		if err := json.Unmarshal(i.snapshot.Tables[name], &rows); err != nil {
			return err
		}
		for index, raw := range rows {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil {
				return fmt.Errorf("invalid history row %s", name)
			}
			pk := historyPK(name, object, index)
			state := historyString(object, "state", historyString(object, "status", "unknown"))
			occurred := historyTime(object)
			var effectDigest []byte
			if value := historyString(object, "external_effect_id", ""); value != "" {
				sum := sha256.Sum256([]byte(value))
				effectDigest = sum[:]
			}
			digest := recordDigest(raw)
			summary, _ := json.Marshal(map[string]any{"source_index": index, "source_state": state})
			var id int64
			if err := i.tx.QueryRow(i.ctx, `INSERT INTO automation_operations_legacy_history(batch_id,source_system,source_table,source_pk,source_state,source_effect_digest,record_digest,safe_summary,occurred_at,imported_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,clock_timestamp()) RETURNING id`, i.batchID, i.snapshot.Manifest.SourceSystem, name, sourcePK(i.snapshot, pk), state, effectDigest, digest[:], summary, occurred).Scan(&id); err != nil {
				return err
			}
			if err := i.mapRow(name, pk, "automation_operations_legacy_history", &id, digest, "imported"); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveOneID(ctx context.Context, resolver identityport.Resolver, identities []identityRow) (int64, string, error) {
	roots := map[int64]struct{}{}
	valid := 0
	for _, identity := range identities {
		if identity.Assurance != "verified" {
			continue
		}
		if identity.Kind == "" || identity.Scope == "" || identity.Value == "" {
			return 0, "invalid", nil
		}
		result, err := resolver.Resolve(ctx, identitydomain.Reference{Kind: identitydomain.Kind(identity.Kind), Scope: identity.Scope, Value: identity.Value, Assurance: identitydomain.AssuranceVerified, Source: identity.Source})
		if errors.Is(err, identitydomain.ErrInvalidReference) {
			return 0, "invalid", nil
		}
		if err != nil {
			return 0, "", err
		}
		valid++
		switch result.Status {
		case identityport.ResolveFound:
			roots[int64(result.CustomerID)] = struct{}{}
		case identityport.ResolveConflict:
			return 0, "conflict", nil
		}
	}
	if len(roots) > 1 {
		return 0, "conflict", nil
	}
	if len(roots) == 0 {
		if valid == 0 {
			return 0, "unresolved", nil
		}
		return 0, "unresolved", nil
	}
	for id := range roots {
		return id, "mapped", nil
	}
	return 0, "unresolved", nil
}

type agentDigests struct {
	kind               string
	version            int64
	content, materials [32]byte
}

func loadAgentDigests(ctx context.Context, tx pgx.Tx, id int64) (agentDigests, error) {
	var kind, role, task string
	var version int64
	var fixedRaw []byte
	if err := tx.QueryRow(ctx, `SELECT automation_type,published_version,published_role_prompt,published_task_prompt,fixed_content_package FROM automation_agents WHERE id=$1`, id).Scan(&kind, &version, &role, &task, &fixedRaw); err != nil {
		return agentDigests{}, err
	}
	var fixed automationport.FixedContentPackage
	if json.Unmarshal(fixedRaw, &fixed) != nil {
		return agentDigests{}, errors.New("invalid target fixed content")
	}
	contentRaw, _ := json.Marshal(map[string]any{"automation_type": kind, "published_role_prompt": role, "published_task_prompt": task, "content_text": fixed.ContentText})
	materialsRaw, _ := json.Marshal(map[string]any{"images": fixed.ImageLibraryIDs, "mini_programs": fixed.MiniprogramLibraryIDs, "attachments": fixed.AttachmentLibraryIDs, "group_invites": fixed.GroupInviteLibraryIDs, "dynamic_card": fixed.DynamicMiniprogramCard})
	return agentDigests{kind: kind, version: version, content: sha256.Sum256(contentRaw), materials: sha256.Sum256(materialsRaw)}, nil
}
func compactJSON(raw []byte) []byte {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	out, _ := json.Marshal(value)
	return out
}
func sameJSON(a, b []byte) bool { return string(compactJSON(a)) == string(compactJSON(b)) }
func historyString(object map[string]json.RawMessage, key, fallback string) string {
	var value string
	if json.Unmarshal(object[key], &value) == nil && value != "" {
		return value
	}
	return fallback
}
func historyTime(object map[string]json.RawMessage) time.Time {
	for _, key := range []string{"completed_at", "published_at", "enrolled_at", "created_at", "updated_at"} {
		var value *time.Time
		if json.Unmarshal(object[key], &value) == nil && value != nil {
			return value.UTC()
		}
	}
	return time.Unix(0, 0).UTC()
}
func historyPK(name string, object map[string]json.RawMessage, index int) string {
	if name == "automation_policy_versions" {
		var a, v int64
		json.Unmarshal(object["automation_id"], &a)
		json.Unmarshal(object["version"], &v)
		return fmt.Sprintf("%d:%d", a, v)
	}
	var id int64
	for _, key := range []string{"id", "automation_id"} {
		if json.Unmarshal(object[key], &id) == nil && id > 0 {
			return strconv.FormatInt(id, 10)
		}
	}
	return strconv.Itoa(index + 1)
}

func loadReport(ctx context.Context, tx pgx.Tx, batchID int64, batchKey string, dry bool, counts map[string]int) (Report, error) {
	report := Report{BatchKey: batchKey, DryRun: dry, Tables: map[string]Buckets{}}
	var effectsBefore, jobsBefore int64
	var effectsAfter, jobsAfter *int64
	if err := tx.QueryRow(ctx, `SELECT provider_effect_count_before,provider_effect_count_after,river_job_count_before,river_job_count_after FROM automation_operations_migration_batches WHERE id=$1`, batchID).Scan(&effectsBefore, &effectsAfter, &jobsBefore, &jobsAfter); err != nil {
		return report, err
	}
	if effectsAfter != nil {
		report.ProviderEffectsCreated = *effectsAfter - effectsBefore
	}
	if jobsAfter != nil {
		report.RiverJobsCreated = *jobsAfter - jobsBefore
	}
	rows, err := tx.Query(ctx, `SELECT source_table,disposition,count(*) FROM automation_operations_migration_source_map WHERE batch_id=$1 GROUP BY source_table,disposition`, batchID)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, disposition string
		var count int
		if err = rows.Scan(&name, &disposition, &count); err != nil {
			return report, err
		}
		b := report.Tables[name]
		b.Source = counts[name]
		switch disposition {
		case "imported":
			b.Imported = count
		case "duplicate":
			b.Duplicate = count
		case "mapped":
			b.Mapped = count
		case "unresolved":
			b.Unresolved = count
		case "conflict":
			b.Conflict = count
		case "invalid":
			b.Invalid = count
		default:
			b.Quarantine = count
		}
		report.Tables[name] = b
	}
	return report, rows.Err()
}

func Reconcile(ctx context.Context, pool *pgxpool.Pool, batchKey string, snapshot segmentmigration.Snapshot) (Report, error) {
	if pool == nil || batchKey == "" {
		return Report{}, errors.New("target and batch-key are required")
	}
	if err := segmentmigration.ValidateSnapshot(snapshot); err != nil {
		return Report{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	var batchID, effectsBefore, effectsAfter, jobsBefore, jobsAfter int64
	var manifestRaw []byte
	if err = tx.QueryRow(ctx, `SELECT id,manifest,provider_effect_count_before,provider_effect_count_after,river_job_count_before,river_job_count_after FROM automation_operations_migration_batches WHERE batch_key=$1 AND status IN ('imported','reconciled') FOR UPDATE`, batchKey).Scan(&batchID, &manifestRaw, &effectsBefore, &effectsAfter, &jobsBefore, &jobsAfter); err != nil {
		return Report{}, err
	}
	shadow, err := shadowInTx(ctx, tx, batchKey, snapshot)
	if err != nil {
		return Report{}, err
	}
	if !shadow.ReadyForReconcile {
		return Report{}, errors.New("frozen snapshot comparison is not ready for reconciliation")
	}
	var manifest segmentmigration.Manifest
	if json.Unmarshal(manifestRaw, &manifest) != nil {
		return Report{}, errors.New("invalid stored manifest")
	}
	report, err := loadReport(ctx, tx, batchID, batchKey, false, manifest.Counts)
	if err != nil {
		return report, err
	}
	for name, count := range manifest.Counts {
		b := report.Tables[name]
		if b.Source != count || b.Source != b.Imported+b.Duplicate+b.Mapped+b.Unresolved+b.Conflict+b.Invalid+b.Quarantine {
			return report, fmt.Errorf("count equation failed for %s", name)
		}
	}
	report.ProviderEffectsCreated = effectsAfter - effectsBefore
	report.RiverJobsCreated = jobsAfter - jobsBefore
	if report.ProviderEffectsCreated != 0 || report.RiverJobsCreated != 0 {
		return report, errors.New("migration batch changed provider effects or durable jobs")
	}
	if _, err = tx.Exec(ctx, `UPDATE automation_operations_migration_batches SET status='reconciled',updated_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return report, err
	}
	return report, tx.Commit(ctx)
}

func Rollback(ctx context.Context, pool *pgxpool.Pool, batchKey string, confirm bool) error {
	if pool == nil || batchKey == "" || !confirm {
		return errors.New("target, batch-key and confirm-rollback are required")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var batchID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM automation_operations_migration_batches WHERE batch_key=$1 AND status IN ('imported','reconciled') FOR UPDATE`, batchKey).Scan(&batchID); err != nil {
		return err
	}
	var active int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM segment_audience_packages p JOIN automation_operations_migration_source_map m ON m.target_table='segment_audience_packages' AND m.target_pk=p.id WHERE m.batch_id=$1 AND p.lifecycle='active'`, batchID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("cannot rollback while imported packages are active")
	}
	if _, err = tx.Exec(ctx, `UPDATE segment_audience_packages SET lifecycle='paused',archived_at=NULL,updated_at=clock_timestamp() WHERE id IN (SELECT target_pk FROM automation_operations_migration_source_map WHERE batch_id=$1 AND target_table='segment_audience_packages') AND lifecycle<>'archived'`, batchID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE automation_operations_migration_batches SET status='rolled_back',updated_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
