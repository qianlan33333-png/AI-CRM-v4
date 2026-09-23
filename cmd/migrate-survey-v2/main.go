// Command migrate-survey-v2 imports encrypted Survey history, with an explicit
// append-only mode for unchanged historical source facts.
package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

const magic = "AICRM-SURVEY-V2-SNAPSHOT-1\n"

const historicalQuestionnaireAuditSQL = `INSERT INTO survey_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,metadata,occurred_at) VALUES('survey_history_imported','questionnaire',$1,'migration',jsonb_build_object('source_enabled',$2::boolean,'target_status','disabled','snapshot_at',$3::timestamptz),clock_timestamp())`

var tables = []string{"questionnaires", "questionnaire_questions", "questionnaire_options", "questionnaire_score_rules", "questionnaire_submissions", "questionnaire_submission_answers", "questionnaire_external_push_logs", "questionnaire_scrm_apply_logs"}

type Manifest struct {
	SourceSystem string            `json:"source_system"`
	SnapshotAt   time.Time         `json:"snapshot_at"`
	Counts       map[string]int    `json:"counts"`
	Digests      map[string]string `json:"digests"`
}
type Snapshot struct {
	Manifest Manifest                   `json:"manifest"`
	Tables   map[string]json.RawMessage `json:"tables"`
}
type questionnaire struct {
	ID               int64           `json:"id"`
	Slug             string          `json:"slug"`
	Name             string          `json:"name"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Disabled         bool            `json:"is_disabled"`
	Assessment       bool            `json:"assessment_enabled"`
	AssessmentConfig json.RawMessage `json:"assessment_config"`
	Display          string          `json:"answer_display_mode"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}
type question struct {
	ID              int64  `json:"id"`
	QuestionnaireID int64  `json:"questionnaire_id"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	Placeholder     string `json:"placeholder"`
	Dimension       string `json:"dimension"`
	Sidebar         string `json:"sidebar"`
	Required        bool   `json:"required"`
	Sort            int    `json:"sort"`
}
type option struct {
	ID               int64           `json:"id"`
	QuestionID       int64           `json:"question_id"`
	Text             string          `json:"text"`
	Score            float64         `json:"score"`
	Tags             json.RawMessage `json:"tags"`
	Sort             int             `json:"sort"`
	TypeKey          string          `json:"type_key"`
	IsOther          bool            `json:"is_other"`
	OtherPlaceholder string          `json:"other_placeholder"`
	OtherMax         int             `json:"other_max"`
}
type rule struct {
	ID              int64           `json:"id"`
	QuestionnaireID int64           `json:"questionnaire_id"`
	Min             *float64        `json:"min"`
	Max             *float64        `json:"max"`
	Tags            json.RawMessage `json:"tags"`
	Sort            int             `json:"sort"`
}
type submission struct {
	ID              int64           `json:"id"`
	QuestionnaireID int64           `json:"questionnaire_id"`
	UnionID         string          `json:"union_id"`
	MatchedBy       string          `json:"matched_by"`
	SourceChannel   string          `json:"source_channel"`
	CampaignID      string          `json:"campaign_id"`
	StaffID         string          `json:"staff_id"`
	Total           float64         `json:"total"`
	FinalTags       json.RawMessage `json:"final_tags"`
	Result          json.RawMessage `json:"result"`
	Token           string          `json:"token"`
	SubmittedAt     time.Time       `json:"submitted_at"`
	CreatedAt       time.Time       `json:"created_at"`
}
type answer struct {
	ID           int64           `json:"id"`
	SubmissionID int64           `json:"submission_id"`
	QuestionID   int64           `json:"question_id"`
	Type         string          `json:"type"`
	Title        string          `json:"title"`
	Text         string          `json:"text"`
	OptionIDs    json.RawMessage `json:"option_ids"`
	OptionTexts  json.RawMessage `json:"option_texts"`
	OptionScores json.RawMessage `json:"option_scores"`
	OptionTags   json.RawMessage `json:"option_tags"`
	Score        float64         `json:"score"`
	CreatedAt    time.Time       `json:"created_at"`
}
type operation struct {
	ID              int64     `json:"id"`
	QuestionnaireID int64     `json:"questionnaire_id"`
	SubmissionID    int64     `json:"submission_id"`
	Status          string    `json:"status"`
	FailureCategory string    `json:"failure_category"`
	OccurredAt      time.Time `json:"occurred_at"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "survey migration failed:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: extract|validate|import|reconcile|rollback")
	}
	switch args[0] {
	case "extract":
		return extract(args[1:])
	case "validate":
		return validate(args[1:])
	case "import":
		return importSnapshot(args[1:])
	case "reconcile":
		return reconcile(args[1:])
	case "rollback":
		return rollbackImport(args[1:])
	default:
		return errors.New("unknown phase")
	}
}
func common(name string, args []string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	file := fs.String("snapshot", "", "encrypted snapshot path")
	key := fs.String("snapshot-key-file", "", "0600 base64url 32-byte snapshot key file")
	return fs, file, key
}
func extract(args []string) error {
	fs, file, keyFile := common("extract", args)
	source := fs.String("source-url", "", "read-only PostgreSQL URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *keyFile == "" || *source == "" {
		return errors.New("source-url, snapshot and snapshot-key-file are required")
	}
	key, err := readKey(*keyFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, *source)
	if err != nil {
		return errors.New("connect source")
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("begin read-only snapshot")
	}
	defer tx.Rollback(ctx)
	var snapshotAt time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshotAt); err != nil {
		return err
	}
	queries := map[string]string{
		"questionnaires":                   `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,slug,name,title,description,is_disabled,assessment_enabled,assessment_config,answer_display_mode,created_at,updated_at FROM questionnaires)x`,
		"questionnaire_questions":          `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,questionnaire_id,type,title,required,sort_order AS sort,placeholder_text AS placeholder,assessment_dimension_key AS dimension,sidebar_profile_field AS sidebar FROM questionnaire_questions)x`,
		"questionnaire_options":            `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,question_id,option_text AS text,score,tag_codes AS tags,sort_order AS sort,assessment_type_key AS type_key,is_other,other_placeholder,other_max_length AS other_max FROM questionnaire_options)x`,
		"questionnaire_score_rules":        `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,questionnaire_id,min_score AS min,max_score AS max,tag_codes AS tags,sort_order AS sort FROM questionnaire_score_rules)x`,
		"questionnaire_submissions":        `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,questionnaire_id,unionid AS union_id,matched_by,source_channel,campaign_id,staff_id,total_score AS total,final_tags,result_token AS token,assessment_result_snapshot AS result,submitted_at,created_at FROM questionnaire_submissions)x`,
		"questionnaire_submission_answers": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,submission_id,question_id,question_type AS type,question_title_snapshot AS title,selected_option_ids AS option_ids,selected_option_texts_snapshot AS option_texts,selected_option_scores_snapshot AS option_scores,selected_option_tags_snapshot AS option_tags,text_value AS text,score_contribution AS score,created_at FROM questionnaire_submission_answers)x`,
		"questionnaire_external_push_logs": `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,questionnaire_id,submission_record_id AS submission_id,status,CASE WHEN status='success' THEN '' WHEN response_status_code BETWEEN 400 AND 499 THEN 'provider_4xx' WHEN response_status_code>=500 THEN 'provider_5xx' ELSE 'provider_failure' END AS failure_category,created_at AS occurred_at FROM questionnaire_external_push_logs)x`,
		"questionnaire_scrm_apply_logs":    `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM (SELECT id,questionnaire_id,submission_id,status,CASE WHEN status='identity_unresolved' THEN 'identity_unresolved' WHEN status LIKE 'skipped%' THEN status ELSE 'provider_failure' END AS failure_category,created_at AS occurred_at FROM questionnaire_scrm_apply_logs)x`,
	}
	snap := Snapshot{Manifest: Manifest{SourceSystem: "ai-crm-v2:150.158.82.186/openclaw_wecom", SnapshotAt: snapshotAt.UTC(), Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, table := range tables {
		var raw []byte
		if err = tx.QueryRow(ctx, queries[table]).Scan(&raw); err != nil {
			return fmt.Errorf("extract %s", table)
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return fmt.Errorf("decode %s", table)
		}
		canonical, _ := json.Marshal(rows)
		snap.Tables[table] = canonical
		snap.Manifest.Counts[table] = len(rows)
		digest := sha256.Sum256(canonical)
		snap.Manifest.Digests[table] = hex.EncodeToString(digest[:])
	}
	plain, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	encrypted, err := encrypt(key, plain)
	if err != nil {
		return err
	}
	if err = os.WriteFile(*file, encrypted, 0600); err != nil {
		return err
	}
	fmt.Printf("snapshot_at=%s manifest_sha256=%x counts=%s\n", snap.Manifest.SnapshotAt.Format(time.RFC3339Nano), sha256.Sum256(plain), safeCounts(snap.Manifest.Counts))
	return tx.Commit(ctx)
}

func validate(args []string) error {
	fs, file, keyFile := common("validate", args)
	if err := fs.Parse(args); err != nil {
		return err
	}
	snap, _, err := load(*file, *keyFile)
	if err != nil {
		return err
	}
	if err = validateSnapshot(snap); err != nil {
		return err
	}
	fmt.Printf("valid snapshot_at=%s counts=%s unresolved_candidates=%d missing_definition_answers=%d\n", snap.Manifest.SnapshotAt.Format(time.RFC3339Nano), safeCounts(snap.Manifest.Counts), identityUnresolved(snap), missingDefinitions(snap))
	return nil
}
func validateSnapshot(s Snapshot) error {
	if s.Manifest.SourceSystem == "" || s.Manifest.SnapshotAt.IsZero() {
		return errors.New("invalid manifest")
	}
	for _, table := range tables {
		raw, ok := s.Tables[table]
		if !ok || !json.Valid(raw) {
			return fmt.Errorf("missing table %s", table)
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil || len(rows) != s.Manifest.Counts[table] {
			return fmt.Errorf("count mismatch %s", table)
		}
		canonical, _ := json.Marshal(rows)
		digest := sha256.Sum256(canonical)
		if hex.EncodeToString(digest[:]) != s.Manifest.Digests[table] {
			return fmt.Errorf("digest mismatch %s", table)
		}
	}
	var questionnaires []questionnaire
	var questions []question
	var options []option
	var rules []rule
	var submissions []submission
	var answers []answer
	var pushes, scrm []operation
	for _, entry := range []struct {
		name string
		out  any
	}{{"questionnaires", &questionnaires}, {"questionnaire_questions", &questions}, {"questionnaire_options", &options}, {"questionnaire_score_rules", &rules}, {"questionnaire_submissions", &submissions}, {"questionnaire_submission_answers", &answers}, {"questionnaire_external_push_logs", &pushes}, {"questionnaire_scrm_apply_logs", &scrm}} {
		if err := decodeTable(s, entry.name, entry.out); err != nil {
			return err
		}
	}
	for _, row := range questions {
		if !validAssessmentBusinessKey(row.Dimension) {
			return fmt.Errorf("invalid assessment business key questionnaire_questions/%d", row.ID)
		}
	}
	for _, row := range options {
		if !validAssessmentBusinessKey(row.TypeKey) {
			return fmt.Errorf("invalid assessment business key questionnaire_options/%d", row.ID)
		}
	}
	for _, row := range submissions {
		if _, err := legacyHistoricalUnionID(row.UnionID); err != nil {
			return fmt.Errorf("invalid historical unionid questionnaire_submissions/%d", row.ID)
		}
	}
	return nil
}

func importSnapshot(args []string) error {
	fs, file, keyFile := common("import", args)
	target := fs.String("target-url", "", "v3 PostgreSQL URL")
	dataKeyFile := fs.String("data-key-file", "", "v3 survey data key file")
	confirm := fs.Bool("confirm-import", false, "perform target write")
	appendOnly := fs.Bool("append-only", false, "append to unchanged complete source history; preserve existing mappings")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*confirm {
		return errors.New("confirm-import is required")
	}
	snap, manifestDigest, err := load(*file, *keyFile)
	if err != nil {
		return err
	}
	if err = validateSnapshot(snap); err != nil {
		return err
	}
	dataKey, err := readKey(*dataKeyFile)
	if err != nil {
		return err
	}
	surveyCipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(dataKey))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, *target)
	if err != nil {
		return errors.New("connect target")
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	batchKey := "survey-v2-" + snap.Manifest.SnapshotAt.Format("20060102T150405Z")
	manifestRaw, _ := json.Marshal(snap.Manifest)
	var batchID int64
	if batchID, err = beginOrReplayBatch(ctx, tx, batchKey, snap.Manifest, manifestRaw, manifestDigest); err != nil {
		return err
	}
	if *appendOnly {
		if err = verifyAppendOnlySource(ctx, tx, snap); err != nil {
			return err
		}
	}
	var actor int64
	if err = tx.QueryRow(ctx, `SELECT id FROM admin_users ORDER BY id LIMIT 1`).Scan(&actor); err != nil {
		return errors.New("target has no administrator for migrated definitions")
	}
	var questionnaires []questionnaire
	var questions []question
	var options []option
	var rules []rule
	var submissions []submission
	var answers []answer
	var pushes, scrm []operation
	for _, entry := range []struct {
		name string
		out  any
	}{
		{"questionnaires", &questionnaires}, {"questionnaire_questions", &questions}, {"questionnaire_options", &options}, {"questionnaire_score_rules", &rules},
		{"questionnaire_submissions", &submissions}, {"questionnaire_submission_answers", &answers}, {"questionnaire_external_push_logs", &pushes}, {"questionnaire_scrm_apply_logs", &scrm},
	} {
		if err = decodeTable(snap, entry.name, entry.out); err != nil {
			return err
		}
	}
	qTarget := map[int64]int64{}
	versionTarget := map[int64]int64{}
	questionTarget := map[int64]int64{}
	submissionTarget := map[int64]int64{}
	submissionSource := map[int64]submission{}
	questionsByQ := map[int64][]question{}
	optionsByQuestion := map[int64][]option{}
	rulesByQ := map[int64][]rule{}
	for _, v := range questions {
		questionsByQ[v.QuestionnaireID] = append(questionsByQ[v.QuestionnaireID], v)
	}
	for _, v := range options {
		optionsByQuestion[v.QuestionID] = append(optionsByQuestion[v.QuestionID], v)
	}
	for _, v := range rules {
		rulesByQ[v.QuestionnaireID] = append(rulesByQ[v.QuestionnaireID], v)
	}
	for _, v := range submissions {
		submissionSource[v.ID] = v
	}
	for _, q := range questionnaires {
		digest := recordDigest(q)
		var targetID int64
		if found, pk, e := mapped(ctx, tx, snap.Manifest.SourceSystem, "questionnaires", fmt.Sprint(q.ID), digest); e != nil {
			return e
		} else if found {
			targetID = pk
		} else {
			mode := "survey"
			if q.Assessment {
				mode = "assessment"
			}
			// Historical definitions become admin-visible only. Import must not
			// create a new public H5 entry point or act as a traffic cutover.
			status := "disabled"
			err = tx.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,1,$9,$10) RETURNING id`, trimNonEmpty(q.Name, 200), trimNonEmpty(q.Title, 500), trim(q.Description, 10000), mode, display(q.Display), safeSlug(q.Slug, q.ID), status, actor, q.CreatedAt, q.UpdatedAt).Scan(&targetID)
			if err != nil {
				return fmt.Errorf("import questionnaire %d: %w", q.ID, err)
			}
			if _, err = tx.Exec(ctx, historicalQuestionnaireAuditSQL, targetID, !q.Disabled, snap.Manifest.SnapshotAt); err != nil {
				return err
			}
			definitionDigest := recordDigest(struct {
				Q         questionnaire
				Questions []question
				Rules     []rule
			}{q, questionsByQ[q.ID], rulesByQ[q.ID]})
			assessment := q.AssessmentConfig
			if !q.Assessment || !json.Valid(assessment) {
				assessment = json.RawMessage(`{}`)
			}
			var versionID int64
			err = tx.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,$2,$3,$4,$5,$6,$7,TRUE,$8,$9,$10) RETURNING id`, targetID, mode, display(q.Display), trimNonEmpty(q.Title, 500), trim(q.Description, 10000), assessment, definitionDigest[:], q.UpdatedAt, actor, q.CreatedAt).Scan(&versionID)
			if err != nil {
				return err
			}
			orderedQuestions := questionsByQ[q.ID]
			sort.SliceStable(orderedQuestions, func(i, j int) bool {
				if orderedQuestions[i].Sort == orderedQuestions[j].Sort {
					return orderedQuestions[i].ID < orderedQuestions[j].ID
				}
				return orderedQuestions[i].Sort < orderedQuestions[j].Sort
			})
			for index, item := range orderedQuestions {
				validation := map[string]any{}
				orderedOptions := optionsByQuestion[item.ID]
				sort.SliceStable(orderedOptions, func(i, j int) bool {
					if orderedOptions[i].Sort == orderedOptions[j].Sort {
						return orderedOptions[i].ID < orderedOptions[j].ID
					}
					return orderedOptions[i].Sort < orderedOptions[j].Sort
				})
				if item.Type == "single_choice" {
					validation["min_selections"] = 1
					validation["max_selections"] = 1
				} else if item.Type == "multi_choice" {
					validation["min_selections"] = map[bool]int{true: 1, false: 0}[item.Required]
					validation["max_selections"] = len(orderedOptions)
				}
				validationRaw, _ := json.Marshal(validation)
				var targetQuestion int64
				err = tx.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, versionID, item.Type, trimNonEmpty(item.Title, 1000), item.Dimension, safeOpaque(item.Sidebar), item.Required, index, trim(item.Placeholder, 500), validationRaw).Scan(&targetQuestion)
				if err != nil {
					return err
				}
				questionTarget[item.ID] = targetQuestion
				questionDigest := recordDigest(item)
				if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_questions", fmt.Sprint(item.ID), "survey_definition_questions", targetQuestion, questionDigest, "imported"); err != nil {
					return err
				}
				for oi, opt := range orderedOptions {
					tags := validArray(opt.Tags)
					var targetOption int64
					err = tx.QueryRow(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, targetQuestion, versionID, trimNonEmpty(opt.Text, 1000), opt.Score, opt.TypeKey, tags, opt.IsOther, trim(opt.OtherPlaceholder, 500), otherMax(opt), oi).Scan(&targetOption)
					if err != nil {
						return err
					}
					optionDigest := recordDigest(opt)
					if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_options", fmt.Sprint(opt.ID), "survey_definition_options", targetOption, optionDigest, "imported"); err != nil {
						return err
					}
				}
			}
			orderedRules := rulesByQ[q.ID]
			sort.SliceStable(orderedRules, func(i, j int) bool { return orderedRules[i].Sort < orderedRules[j].Sort })
			for ri, item := range orderedRules {
				var targetRule int64
				err = tx.QueryRow(ctx, `INSERT INTO survey_score_rules(definition_version_id,minimum_score,maximum_score,tag_codes,sort_order) VALUES($1,$2,$3,$4,$5) RETURNING id`, versionID, item.Min, item.Max, validArray(item.Tags), ri).Scan(&targetRule)
				if err != nil {
					return err
				}
				ruleDigest := recordDigest(item)
				if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_score_rules", fmt.Sprint(item.ID), "survey_score_rules", targetRule, ruleDigest, "imported"); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, targetID, versionID); err != nil {
				return err
			}
			versionTarget[q.ID] = versionID
			if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaires", fmt.Sprint(q.ID), "survey_questionnaires", targetID, digest, "imported"); err != nil {
				return err
			}
		}
		qTarget[q.ID] = targetID
		if versionTarget[q.ID] == 0 {
			var versionID int64
			_ = tx.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, targetID).Scan(&versionID)
			versionTarget[q.ID] = versionID
		}
	}
	// Definitions are immutable facts. A replay may reuse their mappings only
	// when every child fact still has the exact frozen source digest. In
	// particular, an already-mapped questionnaire cannot hide an option or
	// score-rule change from a later snapshot.
	if err = verifyDefinitionGraph(ctx, tx, snap.Manifest.SourceSystem, questions, options, rules); err != nil {
		return err
	}
	// Reload definition source mappings so a repeated import has the same
	// source-to-target graph before answers are reconciled.
	for _, item := range questions {
		var target *int64
		var existing []byte
		err = tx.QueryRow(ctx, `SELECT target_pk,record_digest FROM survey_migration_source_map WHERE source_system=$1 AND source_table='questionnaire_questions' AND source_pk=$2`, snap.Manifest.SourceSystem, fmt.Sprint(item.ID)).Scan(&target, &existing)
		if err == nil && target != nil {
			digest := recordDigest(item)
			if string(existing) != string(digest[:]) {
				return errors.New("migration source drift")
			}
			questionTarget[item.ID] = *target
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	for _, s := range submissions {
		targetQ := qTarget[s.QuestionnaireID]
		if targetQ == 0 {
			return fmt.Errorf("submission %d missing questionnaire", s.ID)
		}
		digest := recordDigest(s)
		if found, pk, e := mapped(ctx, tx, snap.Manifest.SourceSystem, "questionnaire_submissions", fmt.Sprint(s.ID), digest); e != nil {
			return e
		} else if found {
			if err = ensureLegacyExternalProjection(ctx, tx, pk, s); err != nil {
				return err
			}
			submissionTarget[s.ID] = pk
			continue
		}
		identityState := "anonymous"
		var evidence any
		if strings.TrimSpace(s.UnionID) != "" {
			identityState = "unresolved"
			d := sha256.Sum256([]byte(strings.TrimSpace(s.UnionID)))
			evidence = d[:]
		}
		result := s.Result
		if !validObject(result) {
			result = json.RawMessage(`{}`)
		}
		var resultObject map[string]any
		_ = json.Unmarshal(result, &resultObject)
		if resultObject == nil {
			resultObject = map[string]any{}
		}
		var finalTags []any
		if json.Unmarshal(s.FinalTags, &finalTags) == nil {
			resultObject["_legacy_final_tags"] = finalTags
		}
		resultObject["_legacy_matched_by"] = trim(s.MatchedBy, 100)
		result, _ = json.Marshal(resultObject)
		keyDigest := sha256.Sum256([]byte(fmt.Sprintf("legacy:%d", s.ID)))
		payload := recordDigest(s)
		var targetID int64
		err = tx.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,identity_state,identity_reason,evidence_digest,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at,created_at) SELECT $1,$2,1,$3,$4,$5,$6,$7,q.slug,q.title,q.mode,$8,$9,$10,$11,$12,$13,$14 FROM survey_questionnaires q WHERE q.id=$1 RETURNING id`, targetQ, versionTarget[s.QuestionnaireID], identityState, map[bool]string{true: "legacy_unionid_scope_missing", false: ""}[identityState == "unresolved"], evidence, keyDigest[:], payload[:], s.Total, result, trim(s.SourceChannel, 100), trim(s.CampaignID, 200), trim(s.StaffID, 200), s.SubmittedAt, s.CreatedAt).Scan(&targetID)
		if err != nil {
			return err
		}
		submissionTarget[s.ID] = targetID
		if err = ensureLegacyExternalProjection(ctx, tx, targetID, s); err != nil {
			return err
		}
		if strings.TrimSpace(s.Token) != "" {
			tokenDigest := sha256.Sum256([]byte(s.Token))
			var tokenSubmissionID int64
			err = tx.QueryRow(ctx, `INSERT INTO survey_result_tokens(submission_id,token_digest,created_at) VALUES($1,$2,$3)
				ON CONFLICT(token_digest) DO UPDATE SET submission_id=survey_result_tokens.submission_id
				RETURNING submission_id`, targetID, tokenDigest[:], s.CreatedAt).Scan(&tokenSubmissionID)
			if err != nil {
				return err
			}
			if tokenSubmissionID != targetID {
				return errors.New("legacy result token conflicts with another submission")
			}
		} else {
			if err = quarantine(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_result_tokens", fmt.Sprint(s.ID), "missing_result_token", map[string]any{"submission_source_id": s.ID}, digest); err != nil {
				return err
			}
		}
		if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_submissions", fmt.Sprint(s.ID), "survey_submissions", targetID, digest, "imported"); err != nil {
			return err
		}
	}
	for _, a := range answers {
		targetS := submissionTarget[a.SubmissionID]
		if targetS == 0 {
			return fmt.Errorf("answer %d missing submission", a.ID)
		}
		digest := recordDigest(a)
		if found, _, e := mapped(ctx, tx, snap.Manifest.SourceSystem, "questionnaire_submission_answers", fmt.Sprint(a.ID), digest); e != nil {
			return e
		} else if found {
			continue
		}
		targetQuestion := questionTarget[a.QuestionID]
		missing := targetQuestion == 0
		var qid any
		if !missing {
			qid = targetQuestion
		}
		selected := selectedOptions(a)
		encrypted, err := surveyCipher.Encrypt(a.Text)
		if err != nil {
			return err
		}
		if a.Text == "" {
			encrypted = nil
		}
		masked := ""
		if a.Text != "" {
			masked = "[protected]"
			if a.Type == "mobile" {
				masked = mask(a.Text)
			}
		}
		var targetID int64
		err = tx.QueryRow(ctx, `INSERT INTO survey_submission_answers(submission_id,definition_question_id,legacy_source_question_id,question_type,question_title_snapshot,question_sort_order,required_snapshot,selected_options_snapshot,text_value_ciphertext,text_value_masked,answer_digest,score_snapshot,legacy_definition_missing,created_at) VALUES($1,$2,$3,$4,$5,0,FALSE,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, targetS, qid, a.QuestionID, validAnswerType(a.Type, missing), trimNonEmpty(a.Title, 1000), selected, encrypted, masked, digest[:], a.Score, missing, a.CreatedAt).Scan(&targetID)
		if err != nil {
			return err
		}
		if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, "questionnaire_submission_answers", fmt.Sprint(a.ID), "survey_submission_answers", targetID, digest, "imported"); err != nil {
			return err
		}
	}
	for _, set := range []struct {
		table, kind string
		rows        []operation
	}{{"questionnaire_external_push_logs", "external_push", pushes}, {"questionnaire_scrm_apply_logs", "scrm_apply", scrm}} {
		for _, o := range set.rows {
			digest := recordDigest(o)
			if found, _, e := mapped(ctx, tx, snap.Manifest.SourceSystem, set.table, fmt.Sprint(o.ID), digest); e != nil {
				return e
			} else if found {
				continue
			}
			targetQ := qTarget[o.QuestionnaireID]
			targetS := submissionTarget[o.SubmissionID]
			if targetQ == 0 {
				targetQ = qTarget[submissionSource[o.SubmissionID].QuestionnaireID]
			}
			if targetQ == 0 {
				if err = quarantine(ctx, tx, batchID, snap.Manifest.SourceSystem, set.table, fmt.Sprint(o.ID), "missing_questionnaire_association", map[string]any{"submission_source_id": o.SubmissionID, "status": legacyStatus(set.kind, o.Status)}, digest); err != nil {
					return err
				}
				if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, set.table, fmt.Sprint(o.ID), "survey_migration_quarantine", nil, digest, "quarantined"); err != nil {
					return err
				}
				continue
			}
			var submissionValue any
			if targetS > 0 {
				submissionValue = targetS
			}
			status := legacyStatus(set.kind, o.Status)
			var targetID int64
			err = tx.QueryRow(ctx, `INSERT INTO survey_external_operation_receipts(questionnaire_id,submission_id,operation_kind,status,failure_category,occurred_at,read_only_legacy,replayable,source_system,source_table,source_pk,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,TRUE,FALSE,$7,$8,$9,$6,$6) RETURNING id`, targetQ, submissionValue, set.kind, status, trim(o.FailureCategory, 100), o.OccurredAt, snap.Manifest.SourceSystem, set.table, fmt.Sprint(o.ID)).Scan(&targetID)
			if err != nil {
				return err
			}
			if err = writeMap(ctx, tx, batchID, snap.Manifest.SourceSystem, set.table, fmt.Sprint(o.ID), "survey_external_operation_receipts", targetID, digest, "imported"); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE survey_migration_batches SET status='imported',updated_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("imported batch=%s counts=%s unresolved=%d missing_definition_answers=%d provider_effects_created=0\n", batchKey, safeCounts(snap.Manifest.Counts), identityUnresolved(snap), missingDefinitions(snap))
	return nil
}

func rollbackImport(args []string) error {
	fs, file, keyFile := common("rollback", args)
	target := fs.String("target-url", "", "v3 PostgreSQL URL")
	confirm := fs.Bool("confirm-rollback", false, "delete only this snapshot batch from target")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*confirm || *target == "" {
		return errors.New("target-url and confirm-rollback are required")
	}
	snap, _, err := load(*file, *keyFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, *target)
	if err != nil {
		return errors.New("connect target")
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var batchID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM survey_migration_batches WHERE source_system=$1 AND snapshot_at=$2 FOR UPDATE`, snap.Manifest.SourceSystem, snap.Manifest.SnapshotAt).Scan(&batchID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('aicrm.survey_migration_rollback','authorized',true)`); err != nil {
		return err
	}
	var unsafe int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM survey_submissions s JOIN survey_migration_source_map qmap ON qmap.migration_batch_id=$1 AND qmap.target_table='survey_questionnaires' AND qmap.target_pk=s.questionnaire_id LEFT JOIN survey_migration_source_map smap ON smap.migration_batch_id=$1 AND smap.target_table='survey_submissions' AND smap.target_pk=s.id WHERE smap.id IS NULL`, batchID).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe != 0 {
		return errors.New("rollback blocked: imported questionnaires contain v3 submissions")
	}
	commands := []string{
		`DELETE FROM survey_external_operation_receipts WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_external_operation_receipts')`,
		`DELETE FROM survey_operation_configurations WHERE questionnaire_id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_questionnaires')`,
		`DELETE FROM survey_submission_answers WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_submission_answers')`,
		`DELETE FROM survey_result_tokens WHERE submission_id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_submissions')`,
		`DELETE FROM survey_submissions WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_submissions')`,
		`DELETE FROM survey_score_rules WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_score_rules')`,
		`DELETE FROM survey_definition_options WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_definition_options')`,
		`UPDATE survey_questionnaires SET active_definition_version_id=NULL WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_questionnaires')`,
		`DELETE FROM survey_definition_questions WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_definition_questions')`,
		`DELETE FROM survey_definition_versions WHERE questionnaire_id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_questionnaires')`,
		`DELETE FROM survey_questionnaires WHERE id IN (SELECT target_pk FROM survey_migration_source_map WHERE migration_batch_id=$1 AND target_table='survey_questionnaires')`,
		`DELETE FROM survey_migration_quarantine WHERE migration_batch_id=$1`,
		`DELETE FROM survey_migration_source_map WHERE migration_batch_id=$1`,
		`DELETE FROM survey_migration_batches WHERE id=$1`,
	}
	for _, command := range commands {
		if _, err = tx.Exec(ctx, command, batchID); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("rolled_back snapshot_at=%s target_only=true source_unchanged=true\n", snap.Manifest.SnapshotAt.Format(time.RFC3339Nano))
	return nil
}

func reconcile(args []string) error {
	fs, file, keyFile := common("reconcile", args)
	target := fs.String("target-url", "", "v3 PostgreSQL URL")
	appendOnly := fs.Bool("append-only", false, "reconcile complete source history across all import batches")
	allowEnabled := fs.Bool("allow-audited-enabled-definition", false, "allow only an audited enable of unchanged imported definition version one")
	confirmedScope := fs.String("confirmed-unionid-scope", "", "operator-verified Open Platform scope for validating already-resolved historical customers")
	dataKeyFile := fs.String("data-key-file", "", "v3 survey data key file required for protected-answer reconciliation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	snap, manifestDigest, err := load(*file, *keyFile)
	if err != nil {
		return err
	}
	if err = validateSnapshot(snap); err != nil {
		return err
	}
	dataKey, err := readKey(*dataKeyFile)
	if err != nil {
		return errors.New("data-key-file is required for protected-answer reconciliation")
	}
	surveyCipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(dataKey))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, *target)
	if err != nil {
		return errors.New("connect target")
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable, AccessMode: pgx.ReadWrite})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var batchID int64
	var storedManifest []byte
	if err = tx.QueryRow(ctx, `SELECT id,manifest_digest FROM survey_migration_batches WHERE source_system=$1 AND snapshot_at=$2 FOR UPDATE`, snap.Manifest.SourceSystem, snap.Manifest.SnapshotAt).Scan(&batchID, &storedManifest); err != nil {
		return err
	}
	if !bytes.Equal(storedManifest, manifestDigest[:]) {
		return errors.New("migration batch manifest mismatch")
	}
	sourceIndex, err := buildFrozenSourceIndex(snap)
	if err != nil {
		return err
	}
	expected := sourceIndex.digests()
	for _, table := range tables {
		rows, err := tx.Query(ctx, `SELECT migration_batch_id,source_pk,target_table,target_pk,record_digest,import_state FROM survey_migration_source_map WHERE (migration_batch_id=$1 OR $3) AND source_system=$4 AND source_table=$2 ORDER BY id`, batchID, table, *appendOnly, snap.Manifest.SourceSystem)
		if err != nil {
			return err
		}
		type mappedFact struct {
			batchID                int64
			pk, targetTable, state string
			targetPK               *int64
			digest                 []byte
		}
		facts := []mappedFact{}
		seen := map[string]bool{}
		for rows.Next() {
			var fact mappedFact
			if err = rows.Scan(&fact.batchID, &fact.pk, &fact.targetTable, &fact.targetPK, &fact.digest, &fact.state); err != nil {
				rows.Close()
				return err
			}
			expectedDigest, ok := expected[table][fact.pk]
			if !ok || seen[fact.pk] || !bytes.Equal(fact.digest, expectedDigest[:]) {
				rows.Close()
				return fmt.Errorf("migration reconciliation failed: %s/%s mapping drift", table, fact.pk)
			}
			seen[fact.pk] = true
			facts = append(facts, fact)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(seen) != len(expected[table]) {
			return fmt.Errorf("migration reconciliation failed: %s source=%d target=%d", table, len(expected[table]), len(seen))
		}
		for _, fact := range facts {
			var expectedDigest [32]byte
			copy(expectedDigest[:], fact.digest)
			sourceFact, sourceErr := sourceIndex.fact(table, fact.pk)
			if sourceErr != nil {
				return sourceErr
			}
			if err = verifyMappedFact(ctx, tx, fact.batchID, snap.Manifest.SourceSystem, table, fact.pk, fact.targetTable, fact.targetPK, fact.state, expectedDigest, sourceFact, sourceIndex, surveyCipher, *confirmedScope, *allowEnabled); err != nil {
				return err
			}
		}
	}
	if err = verifyDerivedQuarantines(ctx, tx, batchID, snap.Manifest.SourceSystem, sourceIndex); err != nil {
		return err
	}
	var duplicates int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT source_system,source_table,source_pk,count(*) FROM survey_migration_source_map WHERE (migration_batch_id=$1 OR $2) AND source_system=$3 GROUP BY 1,2,3 HAVING count(*)>1)x`, batchID, *appendOnly, snap.Manifest.SourceSystem).Scan(&duplicates); err != nil || duplicates != 0 {
		return errors.New("duplicate source mapping")
	}
	if _, err = tx.Exec(ctx, `UPDATE survey_migration_batches SET status='reconciled',updated_at=clock_timestamp() WHERE id=$1`, batchID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("reconciled counts=%s duplicates=0 silent_loss=0 wrong_oneid_bindings=0 provider_effects_created=0\n", safeCounts(snap.Manifest.Counts))
	return nil
}

type frozenSourceIndex struct {
	facts          map[string]map[string]any
	questionnaires map[int64]questionnaire
	questions      map[int64]question
	options        map[int64]option
	rules          map[int64]rule
	submissions    map[int64]submission
	questionOrder  map[int64]int
	optionOrder    map[int64]int
	ruleOrder      map[int64]int
	optionCount    map[int64]int
}

func buildFrozenSourceIndex(s Snapshot) (*frozenSourceIndex, error) {
	index := &frozenSourceIndex{facts: map[string]map[string]any{}, questionnaires: map[int64]questionnaire{}, questions: map[int64]question{}, options: map[int64]option{}, rules: map[int64]rule{}, submissions: map[int64]submission{}, questionOrder: map[int64]int{}, optionOrder: map[int64]int{}, ruleOrder: map[int64]int{}, optionCount: map[int64]int{}}
	for _, table := range tables {
		index.facts[table] = map[string]any{}
	}
	add := func(table string, id int64, value any) error {
		pk := fmt.Sprint(id)
		if _, ok := index.facts[table][pk]; ok {
			return fmt.Errorf("duplicate frozen source row %s/%s", table, pk)
		}
		index.facts[table][pk] = value
		return nil
	}
	var questionnaires []questionnaire
	var questions []question
	var options []option
	var rules []rule
	var submissions []submission
	var answers []answer
	var pushes, scrm []operation
	for _, entry := range []struct {
		name string
		out  any
	}{{"questionnaires", &questionnaires}, {"questionnaire_questions", &questions}, {"questionnaire_options", &options}, {"questionnaire_score_rules", &rules}, {"questionnaire_submissions", &submissions}, {"questionnaire_submission_answers", &answers}, {"questionnaire_external_push_logs", &pushes}, {"questionnaire_scrm_apply_logs", &scrm}} {
		if err := decodeTable(s, entry.name, entry.out); err != nil {
			return nil, err
		}
	}
	for _, row := range questionnaires {
		if err := add("questionnaires", row.ID, row); err != nil {
			return nil, err
		}
		index.questionnaires[row.ID] = row
	}
	for _, row := range questions {
		if err := add("questionnaire_questions", row.ID, row); err != nil {
			return nil, err
		}
		index.questions[row.ID] = row
	}
	for _, row := range options {
		if err := add("questionnaire_options", row.ID, row); err != nil {
			return nil, err
		}
		index.options[row.ID] = row
	}
	for _, row := range rules {
		if err := add("questionnaire_score_rules", row.ID, row); err != nil {
			return nil, err
		}
		index.rules[row.ID] = row
	}
	for _, row := range submissions {
		if err := add("questionnaire_submissions", row.ID, row); err != nil {
			return nil, err
		}
		index.submissions[row.ID] = row
	}
	for _, row := range answers {
		if err := add("questionnaire_submission_answers", row.ID, row); err != nil {
			return nil, err
		}
	}
	for _, row := range pushes {
		if err := add("questionnaire_external_push_logs", row.ID, row); err != nil {
			return nil, err
		}
	}
	for _, row := range scrm {
		if err := add("questionnaire_scrm_apply_logs", row.ID, row); err != nil {
			return nil, err
		}
	}
	questionGroups := map[int64][]question{}
	optionGroups := map[int64][]option{}
	ruleGroups := map[int64][]rule{}
	for _, value := range index.questions {
		questionGroups[value.QuestionnaireID] = append(questionGroups[value.QuestionnaireID], value)
	}
	for _, value := range index.options {
		optionGroups[value.QuestionID] = append(optionGroups[value.QuestionID], value)
		index.optionCount[value.QuestionID]++
	}
	for _, value := range index.rules {
		ruleGroups[value.QuestionnaireID] = append(ruleGroups[value.QuestionnaireID], value)
	}
	for _, rows := range questionGroups {
		sort.SliceStable(rows, func(a, b int) bool {
			if rows[a].Sort == rows[b].Sort {
				return rows[a].ID < rows[b].ID
			}
			return rows[a].Sort < rows[b].Sort
		})
		for position, value := range rows {
			index.questionOrder[value.ID] = position
		}
	}
	for _, rows := range optionGroups {
		sort.SliceStable(rows, func(a, b int) bool {
			if rows[a].Sort == rows[b].Sort {
				return rows[a].ID < rows[b].ID
			}
			return rows[a].Sort < rows[b].Sort
		})
		for position, value := range rows {
			index.optionOrder[value.ID] = position
		}
	}
	for _, rows := range ruleGroups {
		sort.SliceStable(rows, func(a, b int) bool {
			if rows[a].Sort == rows[b].Sort {
				return rows[a].ID < rows[b].ID
			}
			return rows[a].Sort < rows[b].Sort
		})
		for position, value := range rows {
			index.ruleOrder[value.ID] = position
		}
	}
	return index, nil
}
func (i *frozenSourceIndex) digests() map[string]map[string][32]byte {
	out := map[string]map[string][32]byte{}
	for table, rows := range i.facts {
		out[table] = map[string][32]byte{}
		for pk, value := range rows {
			out[table][pk] = recordDigest(value)
		}
	}
	return out
}
func (i *frozenSourceIndex) fact(table, pk string) (any, error) {
	value, ok := i.facts[table][pk]
	if !ok {
		return nil, fmt.Errorf("frozen source fact missing: %s/%s", table, pk)
	}
	return value, nil
}
func (i *frozenSourceIndex) questionSort(value question) int { return i.questionOrder[value.ID] }
func (i *frozenSourceIndex) optionSort(value option) int     { return i.optionOrder[value.ID] }
func (i *frozenSourceIndex) ruleSort(value rule) int         { return i.ruleOrder[value.ID] }
func (i *frozenSourceIndex) optionsForQuestion(questionID int64) int {
	return i.optionCount[questionID]
}

func (i *frozenSourceIndex) definitionDigest(value questionnaire) [32]byte {
	// The importer uses missing map entries (nil slices), serialized as null.
	// Preserve that frozen encoding for definitions without children.
	var questions []question
	var rules []rule
	for _, candidate := range i.questions {
		if candidate.QuestionnaireID == value.ID {
			questions = append(questions, candidate)
		}
	}
	for _, candidate := range i.rules {
		if candidate.QuestionnaireID == value.ID {
			rules = append(rules, candidate)
		}
	}
	sort.SliceStable(questions, func(a, b int) bool { return questions[a].ID < questions[b].ID })
	sort.SliceStable(rules, func(a, b int) bool { return rules[a].ID < rules[b].ID })
	return recordDigest(struct {
		Q         questionnaire
		Questions []question
		Rules     []rule
	}{value, questions, rules})
}

func verifyMappedFact(ctx context.Context, tx pgx.Tx, batchID int64, source, table, pk, targetTable string, targetPK *int64, state string, digest [32]byte, sourceFact any, sourceIndex *frozenSourceIndex, surveyCipher *secure.Cipher, confirmedScope string, allowEnabled bool) error {
	if state == "quarantined" {
		var reason string
		var safe []byte
		var storedDigest []byte
		err := tx.QueryRow(ctx, `SELECT reason_code,safe_snapshot,record_digest FROM survey_migration_quarantine WHERE migration_batch_id=$1 AND source_system=$2 AND source_table=$3 AND source_pk=$4`, batchID, source, table, pk).Scan(&reason, &safe, &storedDigest)
		if err != nil || !bytes.Equal(storedDigest, digest[:]) || targetTable != "survey_migration_quarantine" || targetPK != nil {
			return fmt.Errorf("migration reconciliation failed: %s/%s missing quarantine fact", table, pk)
		}
		operation, ok := sourceFact.(operation)
		if !ok {
			return fmt.Errorf("migration reconciliation failed: %s/%s unsafe quarantine fact", table, pk)
		}
		// An operation is quarantined only when neither its declared questionnaire nor
		// its source submission supplies a mapped questionnaire. This matches import's
		// deterministic owner fallback and prevents a valid operation being hidden.
		if _, ownerErr := sourceTargetPK(ctx, tx, source, "questionnaires", operation.QuestionnaireID); ownerErr == nil {
			return fmt.Errorf("migration reconciliation failed: %s/%s unsafe quarantine owner", table, pk)
		}
		if sourceSubmission, found := sourceIndex.submissions[operation.SubmissionID]; found {
			if _, ownerErr := sourceTargetPK(ctx, tx, source, "questionnaires", sourceSubmission.QuestionnaireID); ownerErr == nil {
				return fmt.Errorf("migration reconciliation failed: %s/%s unsafe quarantine owner", table, pk)
			}
		}
		kind := map[string]string{"questionnaire_external_push_logs": "external_push", "questionnaire_scrm_apply_logs": "scrm_apply"}[table]
		expectedSafe, _ := json.Marshal(map[string]any{"submission_source_id": operation.SubmissionID, "status": legacyStatus(kind, operation.Status)})
		if reason != "missing_questionnaire_association" || !jsonEquivalent(safe, expectedSafe) {
			return fmt.Errorf("migration reconciliation failed: %s/%s quarantine fact drift", table, pk)
		}
		return nil
	}
	if state != "imported" || targetPK == nil {
		return fmt.Errorf("migration reconciliation failed: %s/%s invalid mapped fact", table, pk)
	}
	var exists bool
	var err error
	switch targetTable {
	case "survey_questionnaires", "survey_definition_questions", "survey_definition_options", "survey_score_rules":
		if !mapTargetTable(table, targetTable) {
			return fmt.Errorf("migration reconciliation failed: %s/%s type mismatch", table, pk)
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+targetTable+` WHERE id=$1)`, *targetPK).Scan(&exists)
		if err != nil || !exists {
			return fmt.Errorf("migration reconciliation failed: %s/%s missing target fact", table, pk)
		}
		if err := verifyDefinitionFact(ctx, tx, source, table, *targetPK, sourceFact, sourceIndex, allowEnabled); err != nil {
			return fmt.Errorf("migration reconciliation failed: %s/%s: %w", table, pk, err)
		}
		return nil
	case "survey_submissions":
		if table != "questionnaire_submissions" {
			return fmt.Errorf("migration reconciliation failed: %s/%s type mismatch", table, pk)
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_submissions WHERE id=$1 AND payload_digest=$2)`, *targetPK, digest[:]).Scan(&exists)
	case "survey_submission_answers":
		if table != "questionnaire_submission_answers" {
			return fmt.Errorf("migration reconciliation failed: %s/%s type mismatch", table, pk)
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_submission_answers WHERE id=$1 AND answer_digest=$2)`, *targetPK, digest[:]).Scan(&exists)
	case "survey_external_operation_receipts":
		if table != "questionnaire_external_push_logs" && table != "questionnaire_scrm_apply_logs" {
			return fmt.Errorf("migration reconciliation failed: %s/%s type mismatch", table, pk)
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_external_operation_receipts WHERE id=$1 AND read_only_legacy=TRUE AND replayable=FALSE AND source_system=$2 AND source_table=$3 AND source_pk=$4)`, *targetPK, source, table, pk).Scan(&exists)
	default:
		return fmt.Errorf("migration reconciliation failed: %s/%s unknown target type", table, pk)
	}
	if err != nil || !exists {
		return fmt.Errorf("migration reconciliation failed: %s/%s missing target fact", table, pk)
	}
	switch value := sourceFact.(type) {
	case submission:
		return verifySubmissionFact(ctx, tx, source, *targetPK, value, sourceIndex, surveyCipher, confirmedScope)
	case answer:
		return verifyAnswerFact(ctx, tx, source, *targetPK, value, sourceIndex, surveyCipher)
	case operation:
		return verifyOperationFact(ctx, tx, source, table, *targetPK, value, sourceIndex)
	}
	return fmt.Errorf("migration reconciliation failed: %s/%s source type mismatch", table, pk)
}

func verifyDerivedQuarantines(ctx context.Context, tx pgx.Tx, batchID int64, source string, sourceIndex *frozenSourceIndex) error {
	for _, value := range sourceIndex.submissions {
		if strings.TrimSpace(value.Token) != "" {
			continue
		}
		var reason string
		var safe, storedDigest []byte
		err := tx.QueryRow(ctx, `SELECT reason_code,safe_snapshot,record_digest FROM survey_migration_quarantine WHERE migration_batch_id=(SELECT migration_batch_id FROM survey_migration_source_map WHERE source_system=$1 AND source_table='questionnaire_submissions' AND source_pk=$2) AND source_system=$1 AND source_table='questionnaire_result_tokens' AND source_pk=$2`, source, fmt.Sprint(value.ID)).Scan(&reason, &safe, &storedDigest)
		expectedSafe, _ := json.Marshal(map[string]any{"submission_source_id": value.ID})
		expectedDigest := recordDigest(value)
		if err != nil || reason != "missing_result_token" || !jsonEquivalent(safe, expectedSafe) || !bytes.Equal(storedDigest, expectedDigest[:]) {
			return errors.New("migration reconciliation failed: missing result-token quarantine")
		}
	}
	return nil
}

func sourceTargetPK(ctx context.Context, tx pgx.Tx, source, table string, sourceID int64) (int64, error) {
	var target *int64
	if err := tx.QueryRow(ctx, `SELECT target_pk FROM survey_migration_source_map WHERE source_system=$1 AND source_table=$2 AND source_pk=$3`, source, table, fmt.Sprint(sourceID)).Scan(&target); err != nil || target == nil {
		return 0, errors.New("migration reconciliation failed: mapped owner missing")
	}
	return *target, nil
}

func verifyDefinitionFact(ctx context.Context, tx pgx.Tx, source, table string, targetPK int64, sourceFact any, sourceIndex *frozenSourceIndex, allowEnabled bool) error {
	mismatch := func(ok bool) error {
		if !ok {
			return fmt.Errorf("migration reconciliation failed: %s target fact drift", table)
		}
		return nil
	}
	switch value := sourceFact.(type) {
	case questionnaire:
		var name, title, description, mode, displayMode, slug, status string
		var activeVersion *int64
		err := tx.QueryRow(ctx, `SELECT name,title,description,mode,answer_display_mode,slug,status,active_definition_version_id FROM survey_questionnaires WHERE id=$1`, targetPK).Scan(&name, &title, &description, &mode, &displayMode, &slug, &status, &activeVersion)

		statusMatches := status == "disabled"
		if err == nil && status == "published" && allowEnabled {
			// A single audited enable can change only the shell status/version, never
			// the immutable published definition, names or snapshots checked below.
			var audited bool
			auditErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_questionnaires q JOIN survey_audit_events a ON a.aggregate_id=q.id AND a.aggregate_type='questionnaire' WHERE q.id=$1 AND q.version=2 AND a.event_type='definition_enable' AND a.actor_scope='admin:'||q.updated_by::text AND a.occurred_at=q.updated_at AND a.metadata->>'expected_version'='1' AND a.metadata->>'status'='published' AND a.metadata->>'id'=q.id::text)`, targetPK).Scan(&audited)
			statusMatches = auditErr == nil && audited
		}

		if err != nil {
			return errors.New("questionnaire target unreadable")
		}
		fields := []string{}
		for _, check := range []struct {
			name string
			ok   bool
		}{
			{"active_definition_version_id", activeVersion != nil},
			{"name", name == trimNonEmpty(value.Name, 200)},
			{"title", title == trimNonEmpty(value.Title, 500)},
			{"description", description == trim(value.Description, 10000)},
			{"mode", mode == map[bool]string{true: "assessment", false: "survey"}[value.Assessment]},
			{"answer_display_mode", displayMode == display(value.Display)},
			{"slug", slug == safeSlug(value.Slug, value.ID)},
			{"status_or_enable_audit", statusMatches},
		} {
			if !check.ok {
				fields = append(fields, check.name)
			}
		}
		if len(fields) > 0 {
			return fmt.Errorf("questionnaire target fact drift fields=%s", strings.Join(fields, ","))
		}

		assessment := value.AssessmentConfig
		if !value.Assessment || !json.Valid(assessment) {
			assessment = json.RawMessage(`{}`)
		}
		expectedDigest := sourceIndex.definitionDigest(value)
		var owner, number int64
		var vmode, vdisplay, vtitle, vdescription string
		var vassessment, vdigest []byte
		var immutable bool
		var published time.Time
		err = tx.QueryRow(ctx, `SELECT questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at FROM survey_definition_versions WHERE id=$1`, *activeVersion).Scan(&owner, &number, &vmode, &vdisplay, &vtitle, &vdescription, &vassessment, &vdigest, &immutable, &published)

		if err != nil {
			return errors.New("definition version unreadable")
		}
		fields = nil
		for _, check := range []struct {
			name string
			ok   bool
		}{
			{"definition.questionnaire_id", owner == targetPK}, {"definition.version_number", number == 1},
			{"definition.mode", vmode == mode}, {"definition.answer_display_mode", vdisplay == displayMode},
			{"definition.title_snapshot", vtitle == trimNonEmpty(value.Title, 500)},
			{"definition.description_snapshot", vdescription == trim(value.Description, 10000)},
			{"definition.assessment_config", jsonEquivalent(vassessment, assessment)},
			{"definition.definition_digest", bytes.Equal(vdigest, expectedDigest[:])},
			{"definition.is_immutable", immutable}, {"definition.published_at", published.Equal(value.UpdatedAt)},
		} {
			if !check.ok {
				fields = append(fields, check.name)
			}
		}
		if len(fields) > 0 {
			return fmt.Errorf("questionnaire target fact drift fields=%s", strings.Join(fields, ","))
		}
		return nil

	case question:
		owner, err := sourceTargetPK(ctx, tx, source, "questionnaires", value.QuestionnaireID)
		if err != nil {
			return err
		}
		var typ, title, dimension, sidebar, placeholder string
		var required bool
		var questionnaireID int64
		var sortOrder int
		var validation []byte
		err = tx.QueryRow(ctx, `SELECT q.question_type,q.title,q.assessment_dimension_key,q.sidebar_profile_field,q.required,q.placeholder_text,q.sort_order,q.validation,v.questionnaire_id FROM survey_definition_questions q JOIN survey_definition_versions v ON v.id=q.definition_version_id WHERE q.id=$1`, targetPK).Scan(&typ, &title, &dimension, &sidebar, &required, &placeholder, &sortOrder, &validation, &questionnaireID)
		expectedValidation := map[string]any{}
		if value.Type == "single_choice" {
			expectedValidation["min_selections"] = 1
			expectedValidation["max_selections"] = 1
		} else if value.Type == "multi_choice" {
			expectedValidation["min_selections"] = map[bool]int{true: 1, false: 0}[value.Required]
			expectedValidation["max_selections"] = sourceIndex.optionsForQuestion(value.ID)
		}
		expectedValidationRaw, _ := json.Marshal(expectedValidation)
		return mismatch(err == nil && questionnaireID == owner && typ == value.Type && title == trimNonEmpty(value.Title, 1000) && dimension == value.Dimension && sidebar == safeOpaque(value.Sidebar) && required == value.Required && placeholder == trim(value.Placeholder, 500) && sortOrder == sourceIndex.questionSort(value) && jsonEquivalent(validation, expectedValidationRaw))
	case option:
		owner, err := sourceTargetPK(ctx, tx, source, "questionnaire_questions", value.QuestionID)
		if err != nil {
			return err
		}
		var questionID int64
		var text, typeKey, placeholder string
		var score float64
		var tags []byte
		var isOther bool
		var otherMaxLength, sortOrder int
		err = tx.QueryRow(ctx, `SELECT question_id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order FROM survey_definition_options WHERE id=$1`, targetPK).Scan(&questionID, &text, &score, &typeKey, &tags, &isOther, &placeholder, &otherMaxLength, &sortOrder)
		return mismatch(err == nil && questionID == owner && text == trimNonEmpty(value.Text, 1000) && score == value.Score && typeKey == value.TypeKey && jsonEquivalent(tags, validArray(value.Tags)) && isOther == value.IsOther && placeholder == trim(value.OtherPlaceholder, 500) && otherMaxLength == otherMax(value) && sortOrder == sourceIndex.optionSort(value))
	case rule:
		owner, err := sourceTargetPK(ctx, tx, source, "questionnaires", value.QuestionnaireID)
		if err != nil {
			return err
		}
		var questionnaireID int64
		var min, max *float64
		var tags []byte
		var sortOrder int
		err = tx.QueryRow(ctx, `SELECT v.questionnaire_id,r.minimum_score,r.maximum_score,r.tag_codes,r.sort_order FROM survey_score_rules r JOIN survey_definition_versions v ON v.id=r.definition_version_id WHERE r.id=$1`, targetPK).Scan(&questionnaireID, &min, &max, &tags, &sortOrder)
		return mismatch(err == nil && questionnaireID == owner && floatPointerEqual(min, value.Min) && floatPointerEqual(max, value.Max) && jsonEquivalent(tags, validArray(value.Tags)) && sortOrder == sourceIndex.ruleSort(value))
	}
	return errors.New("migration reconciliation failed: definition source type mismatch")
}

func legacyHistoricalUnionID(value string) (string, error) {
	if strings.TrimSpace(value) != value || len(value) > 1024 {
		return "", errors.New("invalid historical unionid")
	}
	return value, nil
}

func historicalSurveyProjectionDigest(unionID string) [32]byte {
	return sha256.Sum256([]byte("survey-legacy-unionid-v1\x00" + unionID))
}

// ensureLegacyExternalProjection backfills pre-0099 imports during an exact
// snapshot replay. The field remains a Survey-owned historical read fact; it
// never changes the submission's unresolved OneID status or customer_id.
func ensureLegacyExternalProjection(ctx context.Context, tx pgx.Tx, submissionID int64, source submission) error {
	unionID, err := legacyHistoricalUnionID(source.UnionID)
	if err != nil {
		return err
	}
	var actual string
	var storedDigest []byte
	err = tx.QueryRow(ctx, `SELECT historical_unionid,source_projection_digest FROM survey_legacy_external_projections WHERE submission_id=$1 FOR UPDATE`, submissionID).Scan(&actual, &storedDigest)
	if unionID == "" {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("historical survey projection drift")
	}
	digest := historicalSurveyProjectionDigest(unionID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO survey_legacy_external_projections(submission_id,historical_unionid,source_projection_digest) VALUES($1,$2,$3)`, submissionID, unionID, digest[:])
		return err
	}
	if err != nil || actual != unionID || !bytes.Equal(storedDigest, digest[:]) {
		return errors.New("historical survey projection drift")
	}
	return nil
}

func verifyLegacyExternalProjection(ctx context.Context, tx pgx.Tx, submissionID int64, source submission) error {
	unionID, err := legacyHistoricalUnionID(source.UnionID)
	if err != nil {
		return err
	}
	var actual string
	var storedDigest []byte
	err = tx.QueryRow(ctx, `SELECT historical_unionid,source_projection_digest FROM survey_legacy_external_projections WHERE submission_id=$1`, submissionID).Scan(&actual, &storedDigest)
	if unionID == "" {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return errors.New("historical survey projection drift")
	}
	expected := historicalSurveyProjectionDigest(unionID)
	if err != nil || actual != unionID || !bytes.Equal(storedDigest, expected[:]) {
		return errors.New("historical survey projection drift")
	}
	return nil
}

func verifySubmissionFact(ctx context.Context, tx pgx.Tx, source string, targetPK int64, value submission, sourceIndex *frozenSourceIndex, surveyCipher *secure.Cipher, confirmedScope string) error {
	questionnaireID, err := sourceTargetPK(ctx, tx, source, "questionnaires", value.QuestionnaireID)
	if err != nil {
		return err
	}
	var actualQuestionnaire, actualDefinitionVersion int64
	var customer *int64
	var identityState, identityReason, slug, title, mode, channel, campaign, staff string
	var evidence, payload []byte
	var total float64
	var result []byte
	var submitted, created time.Time
	err = tx.QueryRow(ctx, `SELECT questionnaire_id,definition_version_id,customer_id,identity_state,identity_reason,evidence_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at,created_at FROM survey_submissions WHERE id=$1`, targetPK).Scan(&actualQuestionnaire, &actualDefinitionVersion, &customer, &identityState, &identityReason, &evidence, &payload, &slug, &title, &mode, &total, &result, &channel, &campaign, &staff, &submitted, &created)
	if err != nil {
		return errors.New("migration reconciliation failed: submission fact unreadable")
	}
	identity := "anonymous"
	reason := ""
	var expectedEvidence []byte
	if strings.TrimSpace(value.UnionID) != "" {
		identity = "unresolved"
		reason = "legacy_unionid_scope_missing"
		d := sha256.Sum256([]byte(strings.TrimSpace(value.UnionID)))
		expectedEvidence = d[:]
	}
	identityMatches := customer == nil && identityState == identity && identityReason == reason
	if !identityMatches && customer != nil && identityState == "resolved" && identityReason == "legacy_unionid_scope_confirmed" && identity == "unresolved" && confirmedScope != "" {
		var resolver identityport.Resolver = identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
		resolved, resolveErr := resolver.Resolve(platformpostgres.BindTransaction(ctx, tx), identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: confirmedScope, Value: strings.TrimSpace(value.UnionID), Assurance: identitydomain.AssuranceDeclared, Source: "survey_migration_reconciliation"})
		identityMatches = resolveErr == nil && resolved.Status == identityport.ResolveFound && int64(resolved.CustomerID) == *customer
	}
	resultObject := map[string]any{}
	_ = json.Unmarshal(value.Result, &resultObject)
	if resultObject == nil {
		resultObject = map[string]any{}
	}
	var tags []any
	if json.Unmarshal(value.FinalTags, &tags) == nil {
		resultObject["_legacy_final_tags"] = tags
	}
	resultObject["_legacy_matched_by"] = trim(value.MatchedBy, 100)
	expectedResult, _ := json.Marshal(resultObject)
	payloadDigest := recordDigest(value)
	questionnaireSource, ok := sourceIndex.questionnaires[value.QuestionnaireID]
	if !ok {
		return errors.New("migration reconciliation failed: submission owner missing")
	}
	var activeDefinition int64
	if err = tx.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, questionnaireID).Scan(&activeDefinition); err != nil {
		return errors.New("migration reconciliation failed: submission definition missing")
	}
	expectedMode := map[bool]string{true: "assessment", false: "survey"}[questionnaireSource.Assessment]
	if actualQuestionnaire != questionnaireID || actualDefinitionVersion != activeDefinition || !identityMatches || !bytes.Equal(evidence, expectedEvidence) || !bytes.Equal(payload, payloadDigest[:]) || slug != safeSlug(questionnaireSource.Slug, questionnaireSource.ID) || title != trimNonEmpty(questionnaireSource.Title, 500) || mode != expectedMode || total != value.Total || !jsonEquivalent(result, expectedResult) || channel != trim(value.SourceChannel, 100) || campaign != trim(value.CampaignID, 200) || staff != trim(value.StaffID, 200) || !submitted.Equal(value.SubmittedAt) || !created.Equal(value.CreatedAt) {
		return errors.New("migration reconciliation failed: submission target fact drift")
	}
	if err = verifyLegacyExternalProjection(ctx, tx, targetPK, value); err != nil {
		return errors.New("migration reconciliation failed: historical survey projection drift")
	}
	if strings.TrimSpace(value.Token) != "" {
		d := sha256.Sum256([]byte(value.Token))
		var tokenSubmission int64
		if err = tx.QueryRow(ctx, `SELECT submission_id FROM survey_result_tokens WHERE token_digest=$1`, d[:]).Scan(&tokenSubmission); err != nil || tokenSubmission != targetPK {
			return errors.New("migration reconciliation failed: result token binding drift")
		}
	}
	_ = surveyCipher
	return nil
}

func verifyAnswerFact(ctx context.Context, tx pgx.Tx, source string, targetPK int64, value answer, sourceIndex *frozenSourceIndex, surveyCipher *secure.Cipher) error {
	submissionID, err := sourceTargetPK(ctx, tx, source, "questionnaire_submissions", value.SubmissionID)
	if err != nil {
		return err
	}
	var actualSubmission int64
	var definitionQuestion *int64
	var legacyQuestion *int64
	var typ, title, masked string
	var selected, ciphertext, digest []byte
	var score float64
	var missing bool
	var created time.Time
	err = tx.QueryRow(ctx, `SELECT submission_id,definition_question_id,legacy_source_question_id,question_type,question_title_snapshot,selected_options_snapshot,text_value_ciphertext,text_value_masked,answer_digest,score_snapshot,legacy_definition_missing,created_at FROM survey_submission_answers WHERE id=$1`, targetPK).Scan(&actualSubmission, &definitionQuestion, &legacyQuestion, &typ, &title, &selected, &ciphertext, &masked, &digest, &score, &missing, &created)
	if err != nil {
		return errors.New("migration reconciliation failed: answer fact unreadable")
	}
	var expectedQuestion *int64
	if q, e := sourceTargetPK(ctx, tx, source, "questionnaire_questions", value.QuestionID); e == nil {
		expectedQuestion = &q
	}
	plain := ""
	if value.Text == "" && ciphertext != nil {
		return errors.New("migration reconciliation failed: empty protected answer ciphertext drift")
	}
	if value.Text != "" {
		var decryptErr error
		plain, decryptErr = surveyCipher.Decrypt(ciphertext)
		if decryptErr != nil {
			return errors.New("migration reconciliation failed: protected answer unreadable")
		}
	}
	expectedMasked := ""
	if value.Text != "" {
		expectedMasked = "[protected]"
		if value.Type == "mobile" {
			expectedMasked = mask(value.Text)
		}
	}
	answerDigest := recordDigest(value)
	if actualSubmission != submissionID || !int64PointerEqual(definitionQuestion, expectedQuestion) || legacyQuestion == nil || *legacyQuestion != value.QuestionID || typ != validAnswerType(value.Type, expectedQuestion == nil) || title != trimNonEmpty(value.Title, 1000) || !jsonEquivalent(selected, selectedOptions(value)) || plain != value.Text || masked != expectedMasked || !bytes.Equal(digest, answerDigest[:]) || score != value.Score || missing != (expectedQuestion == nil) || !created.Equal(value.CreatedAt) {
		return errors.New("migration reconciliation failed: answer target fact drift")
	}
	return nil
}

func verifyOperationFact(ctx context.Context, tx pgx.Tx, source, table string, targetPK int64, value operation, sourceIndex *frozenSourceIndex) error {
	questionnaireID, err := sourceTargetPK(ctx, tx, source, "questionnaires", value.QuestionnaireID)
	if err != nil {
		sourceSubmission, ok := sourceIndex.submissions[value.SubmissionID]
		if !ok {
			return err
		}
		questionnaireID, err = sourceTargetPK(ctx, tx, source, "questionnaires", sourceSubmission.QuestionnaireID)
		if err != nil {
			return err
		}
	}
	var expectedSubmission *int64
	if value.SubmissionID != 0 {
		sub, subErr := sourceTargetPK(ctx, tx, source, "questionnaire_submissions", value.SubmissionID)
		if subErr == nil {
			expectedSubmission = &sub
		}
	}
	kind := map[string]string{"questionnaire_external_push_logs": "external_push", "questionnaire_scrm_apply_logs": "scrm_apply"}[table]
	var actualQuestionnaire int64
	var actualSubmission *int64
	var actualKind, status, failure string
	var occurred time.Time
	var readOnly, replayable bool
	err = tx.QueryRow(ctx, `SELECT questionnaire_id,submission_id,operation_kind,status,failure_category,occurred_at,read_only_legacy,replayable FROM survey_external_operation_receipts WHERE id=$1`, targetPK).Scan(&actualQuestionnaire, &actualSubmission, &actualKind, &status, &failure, &occurred, &readOnly, &replayable)
	if err != nil || actualQuestionnaire != questionnaireID || !int64PointerEqual(actualSubmission, expectedSubmission) || actualKind != kind || status != legacyStatus(kind, value.Status) || failure != trim(value.FailureCategory, 100) || !occurred.Equal(value.OccurredAt) || !readOnly || replayable {
		return errors.New("migration reconciliation failed: legacy operation fact drift")
	}
	return nil
}

func jsonEquivalent(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
func int64PointerEqual(left, right *int64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}
func floatPointerEqual(left, right *float64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func mapTargetTable(source, target string) bool {
	return (source == "questionnaires" && target == "survey_questionnaires") ||
		(source == "questionnaire_questions" && target == "survey_definition_questions") ||
		(source == "questionnaire_options" && target == "survey_definition_options") ||
		(source == "questionnaire_score_rules" && target == "survey_score_rules")
}

func load(file, keyFile string) (Snapshot, [32]byte, error) {
	var snap Snapshot
	key, err := readKey(keyFile)
	if err != nil {
		return snap, [32]byte{}, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return snap, [32]byte{}, errors.New("read snapshot")
	}
	plain, err := decrypt(key, data)
	if err != nil {
		return snap, [32]byte{}, err
	}
	digest := sha256.Sum256(mustJSON(snap))
	if json.Unmarshal(plain, &snap) != nil {
		return snap, digest, errors.New("decode snapshot")
	}
	digest = sha256.Sum256(plain)
	return snap, digest, nil
}
func readKey(file string) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, errors.New("read key file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("key file permissions must be 0600")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("key must be base64url encoded 32 bytes")
	}
	return key, nil
}
func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(append([]byte(magic), nonce...), aead.Seal(nil, nonce, plain, []byte(magic))...), nil
}
func decrypt(key, value []byte) ([]byte, error) {
	if !strings.HasPrefix(string(value), magic) {
		return nil, errors.New("invalid snapshot header")
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	body := value[len(magic):]
	if len(body) < aead.NonceSize() {
		return nil, errors.New("invalid snapshot")
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], []byte(magic))
	if err != nil {
		return nil, errors.New("snapshot authentication failed")
	}
	return plain, nil
}
func decodeTable(s Snapshot, name string, out any) error {
	if err := json.Unmarshal(s.Tables[name], out); err != nil {
		return fmt.Errorf("invalid frozen table %s", name)
	}
	return nil
}
func recordDigest(v any) [32]byte { raw, _ := json.Marshal(v); return sha256.Sum256(raw) }
func beginOrReplayBatch(ctx context.Context, tx pgx.Tx, batchKey string, manifest Manifest, manifestRaw []byte, digest [32]byte) (int64, error) {
	var (
		batchID        int64
		sourceSystem   string
		snapshotAt     time.Time
		existingDigest []byte
	)
	err := tx.QueryRow(ctx, `INSERT INTO survey_migration_batches(batch_key,source_system,snapshot_at,manifest,manifest_digest,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'importing',clock_timestamp(),clock_timestamp())
		ON CONFLICT(batch_key) DO UPDATE SET batch_key=survey_migration_batches.batch_key
		RETURNING id,source_system,snapshot_at,manifest_digest`, batchKey, manifest.SourceSystem, manifest.SnapshotAt, manifestRaw, digest[:]).Scan(&batchID, &sourceSystem, &snapshotAt, &existingDigest)
	if err != nil {
		return 0, err
	}
	if sourceSystem != manifest.SourceSystem || !snapshotAt.Equal(manifest.SnapshotAt) || !bytes.Equal(existingDigest, digest[:]) {
		return 0, errors.New("migration batch manifest mismatch")
	}
	return batchID, nil
}

func verifyDefinitionGraph(ctx context.Context, tx pgx.Tx, source string, questions []question, options []option, rules []rule) error {
	verify := func(table, pk string, digest [32]byte) error {
		found, target, err := mapped(ctx, tx, source, table, pk, digest)
		if err != nil {
			return err
		}
		if !found || target == 0 {
			return fmt.Errorf("migration definition graph incomplete: %s/%s", table, pk)
		}
		return nil
	}
	for _, item := range questions {
		if err := verify("questionnaire_questions", fmt.Sprint(item.ID), recordDigest(item)); err != nil {
			return err
		}
	}
	for _, item := range options {
		if err := verify("questionnaire_options", fmt.Sprint(item.ID), recordDigest(item)); err != nil {
			return err
		}
	}
	for _, item := range rules {
		if err := verify("questionnaire_score_rules", fmt.Sprint(item.ID), recordDigest(item)); err != nil {
			return err
		}
	}
	return nil
}

func writeMap(ctx context.Context, tx pgx.Tx, batch int64, source, table, pk, target string, targetPK any, digest [32]byte, state string) error {
	_, err := tx.Exec(ctx, `INSERT INTO survey_migration_source_map(migration_batch_id,source_system,source_table,source_pk,target_table,target_pk,record_digest,import_state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp())`, batch, source, table, pk, target, targetPK, digest[:], state)
	return err
}
func mapped(ctx context.Context, tx pgx.Tx, source, table, pk string, digest [32]byte) (bool, int64, error) {
	var target *int64
	var existing []byte
	err := tx.QueryRow(ctx, `SELECT target_pk,record_digest FROM survey_migration_source_map WHERE source_system=$1 AND source_table=$2 AND source_pk=$3`, source, table, pk).Scan(&target, &existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if string(existing) != string(digest[:]) {
		return false, 0, errors.New("migration source drift")
	}
	if target == nil {
		return true, 0, nil
	}
	return true, *target, nil
}
func quarantine(ctx context.Context, tx pgx.Tx, batch int64, source, table, pk, reason string, safe any, digest [32]byte) error {
	raw, _ := json.Marshal(safe)
	_, err := tx.Exec(ctx, `INSERT INTO survey_migration_quarantine(migration_batch_id,source_system,source_table,source_pk,reason_code,safe_snapshot,record_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()) ON CONFLICT DO NOTHING`, batch, source, table, pk, reason, raw, digest[:])
	return err
}
func identityUnresolved(s Snapshot) int {
	var rows []submission
	if decodeTable(s, "questionnaire_submissions", &rows) != nil {
		return 0
	}
	n := 0
	for _, v := range rows {
		if strings.TrimSpace(v.UnionID) != "" {
			n++
		}
	}
	return n
}
func missingDefinitions(s Snapshot) int {
	var questions []question
	var answers []answer
	if decodeTable(s, "questionnaire_questions", &questions) != nil || decodeTable(s, "questionnaire_submission_answers", &answers) != nil {
		return 0
	}
	known := map[int64]bool{}
	for _, v := range questions {
		known[v.ID] = true
	}
	n := 0
	for _, v := range answers {
		if !known[v.QuestionID] {
			n++
		}
	}
	return n
}
func selectedOptions(a answer) json.RawMessage {
	var ids []int64
	var texts []string
	var scores []float64
	var tags []json.RawMessage
	_ = json.Unmarshal(a.OptionIDs, &ids)
	_ = json.Unmarshal(a.OptionTexts, &texts)
	_ = json.Unmarshal(a.OptionScores, &scores)
	_ = json.Unmarshal(a.OptionTags, &tags)
	out := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		text := ""
		score := 0.0
		if i < len(texts) {
			text = texts[i]
		}
		if i < len(scores) {
			score = scores[i]
		}
		var tagCodes any = []any{}
		if i < len(tags) && json.Valid(tags[i]) {
			tagCodes = tags[i]
		}
		out = append(out, map[string]any{"option_id": id, "option_text": trim(text, 1000), "score": score, "tag_codes": tagCodes})
	}
	raw, _ := json.Marshal(out)
	return raw
}
func safeCounts(m map[string]int) string {
	keys := append([]string(nil), tables...)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}
func trim(v string, max int) string {
	v = strings.TrimSpace(v)
	r := []rune(v)
	if len(r) > max {
		v = string(r[:max])
	}
	return v
}
func trimNonEmpty(v string, max int) string {
	v = trim(v, max)
	if v == "" {
		return "legacy"
	}
	return v
}
func safeSlug(v string, id int64) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	v = strings.Trim(b.String(), "-")
	if v == "" {
		v = fmt.Sprintf("legacy-questionnaire-%d", id)
	}
	if len(v) > 120 {
		v = v[:120]
	}
	return v
}
func safeOpaque(v string) string {
	v = strings.TrimSpace(v)
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return ""
		}
	}
	if len(v) > 128 {
		return ""
	}
	return v
}

// validAssessmentBusinessKey is intentionally only for the legacy assessment
// association columns. A nonempty invalid source key is rejected before the
// import transaction, rather than silently changing an old association.
func validAssessmentBusinessKey(v string) bool {
	if v == "" {
		return true
	}
	if !utf8.ValidString(v) || strings.TrimSpace(v) != v || len([]rune(v)) > 128 {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func display(v string) string {
	if v == "one_by_one" {
		return v
	}
	return "all_in_one"
}
func validArray(v json.RawMessage) json.RawMessage {
	var a []any
	if json.Unmarshal(v, &a) != nil {
		return json.RawMessage(`[]`)
	}
	raw, _ := json.Marshal(a)
	return raw
}
func validObject(v json.RawMessage) bool { var o map[string]any; return json.Unmarshal(v, &o) == nil }
func otherMax(v option) int {
	if !v.IsOther {
		return 0
	}
	if v.OtherMax < 1 {
		return 200
	}
	if v.OtherMax > 200 {
		return 200
	}
	return v.OtherMax
}
func validAnswerType(v string, missing bool) string {
	if missing && v != "single_choice" && v != "multi_choice" && v != "textarea" && v != "mobile" {
		return "legacy_unknown"
	}
	switch v {
	case "single_choice", "multi_choice", "textarea", "mobile":
		return v
	default:
		return "legacy_unknown"
	}
}
func mask(v string) string {
	r := []rune(v)
	if len(r) < 7 {
		return "***"
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}
func legacyStatus(kind, status string) string {
	if kind == "external_push" {
		if status == "success" {
			return "legacy_success"
		}
		return "legacy_failed"
	}
	if strings.HasPrefix(status, "skipped") {
		return "skipped"
	}
	if status == "success" {
		return "legacy_success"
	}
	return "legacy_failed"
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

// verifyAppendOnlySource rejects deleted/changed historical rows before any import
// writes. Definition changes require a separate versioned migration, not append mode.
func verifyAppendOnlySource(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	index, err := buildFrozenSourceIndex(snap)
	if err != nil {
		return err
	}
	expected := index.digests()
	rows, err := tx.Query(ctx, `SELECT source_table,source_pk,record_digest FROM survey_migration_source_map WHERE source_system=$1`, snap.Manifest.SourceSystem)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]map[string]bool{}
	for rows.Next() {
		var table, pk string
		var digest []byte
		if err = rows.Scan(&table, &pk, &digest); err != nil {
			return err
		}
		want, ok := expected[table][pk]
		if !ok || !bytes.Equal(want[:], digest) {
			return fmt.Errorf("append-only source drift: %s mapping changed or omitted", table)
		}
		if seen[table] == nil {
			seen[table] = map[string]bool{}
		}
		if seen[table][pk] {
			return errors.New("append-only duplicate source mapping")
		}
		seen[table][pk] = true
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, table := range []string{"questionnaires", "questionnaire_questions", "questionnaire_options", "questionnaire_score_rules"} {
		if len(seen[table]) != len(expected[table]) {
			return fmt.Errorf("append-only requires unchanged existing definitions: %s", table)
		}
	}
	return nil
}
