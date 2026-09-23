// Package migration contains the read-only legacy audience snapshot
// contract. It is used only by cmd/migrate-automation-operations.
package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const DonorCommit = "6bfbe5816bb89913c70adaca87d6a486260e016e"

var LogicalTables = []string{
	"audience_groups", "audience_packages", "audience_configuration_versions", "automation_agents",
	"audience_bindings", "audience_senders", "audience_members", "automation_policies",
	"automation_policy_versions", "automation_enrollments", "automation_actions",
}

var requiredColumns = map[string][]string{
	"ai_audience_package_groups":                 {"id", "name", "sort_order", "version", "created_at", "updated_at"},
	"ai_audience_package_metadata":               {"segment_id", "group_id", "lifecycle", "version", "created_at", "updated_at"},
	"ai_audience_package_configuration_versions": {"package_id", "version", "definition", "refresh_mode", "refresh_cron", "created_at"},
	"ai_audience_package_automation_bindings":    {"package_id", "automation_agent_id", "version", "created_at", "updated_at"},
	"ai_audience_package_senders":                {"package_id", "sender_userid", "sort_order", "is_enabled", "created_at", "updated_at"},
	"segments":                                   {"id", "name", "definition", "refresh_mode", "refresh_cron", "member_count", "refreshed_at", "created_at", "updated_at"},
	"segment_members":                            {"segment_id", "customer_id", "computed_at"},
	"identities":                                 {"customer_id", "kind", "scope", "normalized_value", "assurance", "source"},
	"automation_agent_configurations":            {"id", "agent_name", "agent_code", "automation_type", "status", "draft_role_prompt", "draft_task_prompt", "published_role_prompt", "published_task_prompt", "draft_version", "published_version", "fixed_content_package_json", "created_at", "updated_at"},
	"automations":                                {"id", "automation_code", "automation_name", "status", "current_version", "trigger_type", "condition_json", "action_json", "created_at", "updated_at"},
	"automation_rule_versions":                   {"automation_id", "version", "trigger_type", "condition_json", "action_json", "published_at"},
	"automation_enrollments":                     {"id", "automation_id", "automation_version", "source_event_id", "customer_id", "state", "enrolled_at", "completed_at"},
	"automation_execution_actions":               {"id", "enrollment_id", "action_type", "state", "external_effect_id", "receipt_digest", "created_at", "completed_at"},
}

type Manifest struct {
	SourceSystem          string            `json:"source_system"`
	DonorCommit           string            `json:"donor_commit"`
	SnapshotAt            time.Time         `json:"snapshot_at"`
	SchemaDigest          string            `json:"schema_digest"`
	SourceWatermarkDigest string            `json:"source_watermark_digest"`
	Counts                map[string]int    `json:"counts"`
	Digests               map[string]string `json:"digests"`
}

type Snapshot struct {
	Manifest Manifest                   `json:"manifest"`
	Tables   map[string]json.RawMessage `json:"tables"`
}

type Report struct {
	SourceSystem              string         `json:"source_system"`
	SnapshotAt                time.Time      `json:"snapshot_at"`
	SchemaDigest              string         `json:"schema_digest"`
	Counts                    map[string]int `json:"counts"`
	ActiveEnrollmentCount     int64          `json:"active_enrollment_count"`
	ActiveActionCount         int64          `json:"active_action_count"`
	OutcomeUnknownCount       int64          `json:"outcome_unknown_count"`
	InvalidVerifiedScopeCount int64          `json:"invalid_verified_scope_count"`
	WritableCredential        bool           `json:"writable_credential"`
}

func Inspect(ctx context.Context, pool *pgxpool.Pool, sourceSystem string) (Report, error) {
	if pool == nil || sourceSystem == "" {
		return Report{}, errors.New("source database and source-system are required")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, errors.New("begin read-only discovery")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='30s'; SET LOCAL lock_timeout='3s'`); err != nil {
		return Report{}, err
	}
	report := Report{SourceSystem: sourceSystem, Counts: map[string]int{}}
	var superuser bool
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp(),rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&report.SnapshotAt, &superuser); err != nil {
		return Report{}, err
	}
	var writable bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE(bool_or(has_table_privilege(current_user,format('public.%I',name),'INSERT,UPDATE,DELETE,TRUNCATE')),false) FROM unnest($1::text[]) AS name`, sourceTableNames()).Scan(&writable); err != nil {
		return Report{}, err
	}
	report.WritableCredential = superuser || writable
	schema, err := schemaRows(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err = validateSchema(schema); err != nil {
		return Report{}, err
	}
	schemaRaw, _ := json.Marshal(schema)
	schemaDigest := sha256.Sum256(schemaRaw)
	report.SchemaDigest = hex.EncodeToString(schemaDigest[:])
	for _, table := range sourceTableNames() {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.`+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
			return Report{}, fmt.Errorf("count %s: %w", table, err)
		}
		report.Counts[table] = count
	}
	var enrollmentUnknown int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='enrolled'),count(*) FILTER (WHERE state='outcome_unknown') FROM public.automation_enrollments`).Scan(&report.ActiveEnrollmentCount, &enrollmentUnknown); err != nil {
		return Report{}, err
	}
	var actionUnknown int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='queued'),count(*) FILTER (WHERE state='outcome_unknown') FROM public.automation_execution_actions`).Scan(&report.ActiveActionCount, &actionUnknown); err != nil {
		return Report{}, err
	}
	report.OutcomeUnknownCount = enrollmentUnknown + actionUnknown
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.identities WHERE assurance='verified' AND (btrim(scope)='' OR btrim(normalized_value)='')`).Scan(&report.InvalidVerifiedScopeCount); err != nil {
		return Report{}, err
	}
	return report, tx.Commit(ctx)
}

func Extract(ctx context.Context, pool *pgxpool.Pool, sourceSystem string) (Snapshot, Report, error) {
	report, err := Inspect(ctx, pool, sourceSystem)
	if err != nil {
		return Snapshot{}, report, err
	}
	if report.WritableCredential {
		return Snapshot{}, report, errors.New("source credential has write capability")
	}
	if report.ActiveEnrollmentCount > 0 || report.ActiveActionCount > 0 || report.OutcomeUnknownCount > 0 {
		return Snapshot{}, report, errors.New("source has in-flight or outcome-unknown automation effects")
	}
	if report.InvalidVerifiedScopeCount > 0 {
		return Snapshot{}, report, errors.New("source has invalid verified identity scope")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, report, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5min'; SET LOCAL lock_timeout='3s'`); err != nil {
		return Snapshot{}, report, err
	}
	var snapshotAt time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshotAt); err != nil {
		return Snapshot{}, report, err
	}
	var activeEnrollments, activeActions, unknownEnrollments, unknownActions int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='enrolled'),count(*) FILTER (WHERE state='outcome_unknown') FROM public.automation_enrollments`).Scan(&activeEnrollments, &unknownEnrollments); err != nil {
		return Snapshot{}, report, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='queued'),count(*) FILTER (WHERE state='outcome_unknown') FROM public.automation_execution_actions`).Scan(&activeActions, &unknownActions); err != nil {
		return Snapshot{}, report, err
	}
	if activeEnrollments > 0 || activeActions > 0 || unknownEnrollments+unknownActions > 0 {
		return Snapshot{}, report, errors.New("source changed and now has in-flight or outcome-unknown automation effects")
	}
	snapshot := Snapshot{Manifest: Manifest{SourceSystem: sourceSystem, DonorCommit: DonorCommit, SnapshotAt: snapshotAt.UTC(), SchemaDigest: report.SchemaDigest, Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, name := range LogicalTables {
		var raw []byte
		if err = tx.QueryRow(ctx, extractionQueries[name]).Scan(&raw); err != nil {
			return Snapshot{}, report, fmt.Errorf("extract %s: %w", name, err)
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return Snapshot{}, report, fmt.Errorf("decode %s", name)
		}
		canonical, _ := json.Marshal(rows)
		snapshot.Tables[name] = canonical
		snapshot.Manifest.Counts[name] = len(rows)
		digest := sha256.Sum256(canonical)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	watermarkRaw, _ := json.Marshal([]any{sourceSystem, snapshotAt.UTC(), snapshot.Manifest.Digests})
	watermark := sha256.Sum256(watermarkRaw)
	snapshot.Manifest.SourceWatermarkDigest = hex.EncodeToString(watermark[:])
	if err = ValidateSnapshot(snapshot); err != nil {
		return Snapshot{}, report, err
	}
	return snapshot, report, tx.Commit(ctx)
}

func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.Manifest.SourceSystem == "" || snapshot.Manifest.DonorCommit != DonorCommit || snapshot.Manifest.SnapshotAt.IsZero() || len(snapshot.Manifest.SchemaDigest) != 64 || len(snapshot.Manifest.SourceWatermarkDigest) != 64 {
		return errors.New("invalid automation operations manifest")
	}
	if _, err := hex.DecodeString(snapshot.Manifest.SchemaDigest); err != nil {
		return errors.New("invalid schema digest")
	}
	if _, err := hex.DecodeString(snapshot.Manifest.SourceWatermarkDigest); err != nil {
		return errors.New("invalid source watermark digest")
	}
	if len(snapshot.Tables) != len(LogicalTables) || len(snapshot.Manifest.Counts) != len(LogicalTables) || len(snapshot.Manifest.Digests) != len(LogicalTables) {
		return errors.New("automation operations manifest table set drift")
	}
	for _, name := range LogicalTables {
		raw, ok := snapshot.Tables[name]
		if !ok || !json.Valid(raw) {
			return fmt.Errorf("missing table %s", name)
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil || len(rows) != snapshot.Manifest.Counts[name] {
			return fmt.Errorf("count mismatch %s", name)
		}
		canonical, _ := json.Marshal(rows)
		digest := sha256.Sum256(canonical)
		if hex.EncodeToString(digest[:]) != snapshot.Manifest.Digests[name] {
			return fmt.Errorf("digest mismatch %s", name)
		}
	}
	return validateRelationships(snapshot)
}

func validateRelationships(snapshot Snapshot) error {
	var packages []struct {
		ID int64 `json:"id"`
	}
	var configurations []struct {
		PackageID int64 `json:"package_id"`
		Version   int64 `json:"version"`
	}
	var members []struct {
		SegmentID  int64 `json:"segment_id"`
		CustomerID int64 `json:"customer_id"`
	}
	if json.Unmarshal(snapshot.Tables["audience_packages"], &packages) != nil || json.Unmarshal(snapshot.Tables["audience_configuration_versions"], &configurations) != nil || json.Unmarshal(snapshot.Tables["audience_members"], &members) != nil {
		return errors.New("invalid audience migration relationships")
	}
	packageIDs := map[int64]bool{}
	configurationCount := map[int64]int{}
	memberCount := map[int64]int{}
	for _, row := range packages {
		if row.ID < 1 || packageIDs[row.ID] {
			return errors.New("invalid or duplicate audience package source key")
		}
		packageIDs[row.ID] = true
	}
	for _, row := range configurations {
		if row.PackageID < 1 || row.Version < 1 || !packageIDs[row.PackageID] {
			return errors.New("orphan or invalid audience configuration")
		}
		configurationCount[row.PackageID]++
	}
	for _, row := range members {
		if row.SegmentID < 1 || row.CustomerID < 1 || !packageIDs[row.SegmentID] {
			return errors.New("orphan or invalid audience member")
		}
		memberCount[row.SegmentID]++
		if memberCount[row.SegmentID] > 100000 {
			return errors.New("audience member capacity exceeded")
		}
	}
	for id := range packageIDs {
		if configurationCount[id] == 0 {
			return errors.New("audience package has no immutable configuration")
		}
	}
	return nil
}

func sourceTableNames() []string {
	values := make([]string, 0, len(requiredColumns))
	for table := range requiredColumns {
		values = append(values, table)
	}
	sort.Strings(values)
	return values
}

type schemaRow struct {
	Table    string `json:"table"`
	Column   string `json:"column"`
	DataType string `json:"data_type"`
	UDT      string `json:"udt"`
}

func schemaRows(ctx context.Context, tx pgx.Tx) ([]schemaRow, error) {
	rows, err := tx.Query(ctx, `SELECT table_name,column_name,data_type,udt_name FROM information_schema.columns WHERE table_schema='public' AND table_name=ANY($1::text[]) ORDER BY table_name,ordinal_position`, sourceTableNames())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []schemaRow{}
	for rows.Next() {
		var row schemaRow
		if err = rows.Scan(&row.Table, &row.Column, &row.DataType, &row.UDT); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func validateSchema(rows []schemaRow) error {
	present := map[string]map[string]bool{}
	for _, row := range rows {
		if present[row.Table] == nil {
			present[row.Table] = map[string]bool{}
		}
		present[row.Table][row.Column] = true
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			if !present[table][column] {
				return fmt.Errorf("unsupported source schema: missing public.%s.%s", table, column)
			}
		}
	}
	return nil
}

var extractionQueries = map[string]string{
	"audience_groups":                 `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,name,sort_order,version,created_at,updated_at FROM public.ai_audience_package_groups)x`,
	"audience_packages":               `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT m.segment_id AS id,m.group_id,m.lifecycle,m.version,s.name,s.definition,s.refresh_mode,s.refresh_cron,s.member_count,s.refreshed_at,s.created_at,s.updated_at FROM public.ai_audience_package_metadata m JOIN public.segments s ON s.id=m.segment_id)x`,
	"audience_configuration_versions": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY package_id,version),'[]') FROM (SELECT package_id,version,definition,refresh_mode,refresh_cron,created_at FROM public.ai_audience_package_configuration_versions)x`,
	"automation_agents":               `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,agent_name,agent_code,automation_type,status,draft_role_prompt,draft_task_prompt,published_role_prompt,published_task_prompt,draft_version,published_version,fixed_content_package_json,created_at,updated_at FROM public.automation_agent_configurations)x`,
	"audience_bindings":               `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY package_id),'[]') FROM (SELECT package_id,automation_agent_id,version,created_at,updated_at FROM public.ai_audience_package_automation_bindings)x`,
	"audience_senders":                `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY package_id,sort_order),'[]') FROM (SELECT package_id,sender_userid,sort_order,is_enabled,created_at,updated_at FROM public.ai_audience_package_senders)x`,
	"audience_members":                `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY segment_id,customer_id),'[]') FROM (SELECT sm.segment_id,sm.customer_id,sm.computed_at,COALESCE(jsonb_agg(jsonb_build_object('kind',i.kind,'scope',i.scope,'value',i.normalized_value,'assurance',i.assurance,'source',i.source) ORDER BY i.id) FILTER (WHERE i.id IS NOT NULL),'[]') AS identities FROM public.segment_members sm LEFT JOIN public.identities i ON i.customer_id=sm.customer_id GROUP BY sm.segment_id,sm.customer_id,sm.computed_at)x`,
	"automation_policies":             `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,automation_code,automation_name,status,current_version,trigger_type,condition_json,action_json,created_at,updated_at FROM public.automations)x`,
	"automation_policy_versions":      `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY automation_id,version),'[]') FROM (SELECT automation_id,version,trigger_type,condition_json,action_json,published_at FROM public.automation_rule_versions)x`,
	"automation_enrollments":          `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,automation_id,automation_version,source_event_id,customer_id,state,enrolled_at,completed_at FROM public.automation_enrollments)x`,
	"automation_actions":              `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,enrollment_id,action_type,state,external_effect_id,receipt_digest,created_at,completed_at FROM public.automation_execution_actions)x`,
}
