package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type Repository struct {
	pool   *pgxpool.Pool
	uow    platformport.UnitOfWork
	cipher *secure.Cipher
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork, cipher *secure.Cipher) (*Repository, error) {
	if pool == nil || uow == nil || cipher == nil {
		return nil, surveyport.ErrUnavailable
	}
	return &Repository{pool: pool, uow: uow, cipher: cipher}, nil
}

func tx(ctx context.Context) (pgx.Tx, error) { return platformpostgres.RequireTransaction(ctx) }

type scanner interface{ Scan(...any) error }

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return surveyport.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505", "23514", "40001", "40P01":
			return surveyport.ErrConflict
		case "23503":
			return surveyport.ErrReferenced
		}
	}
	return err
}

const baseColumns = `q.id,q.name,q.title,q.description,q.mode,q.answer_display_mode,q.slug,q.status,q.created_by,q.version,q.created_at,q.updated_at,q.active_definition_version_id,(SELECT count(*) FROM survey_submissions s WHERE s.questionnaire_id=q.id)`

func scanBase(row scanner) (surveyport.Questionnaire, *int64, error) {
	var q surveyport.Questionnaire
	var active *int64
	err := row.Scan(&q.ID, &q.Name, &q.Title, &q.Description, &q.Mode, &q.AnswerDisplayMode, &q.Slug, &q.Status, &q.CreatedBy, &q.Version, &q.CreatedAt, &q.UpdatedAt, &active, &q.SubmissionCount)
	return q, active, mapError(err)
}

func (r *Repository) List(ctx context.Context, limit, offset int32, search string, status surveyport.QuestionnaireStatus) ([]surveyport.Questionnaire, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	// Archived definitions remain addressable by their retained historical
	// records, but never appear in the default owner list. An explicit status
	// query is reserved for historical/read-only administration.
	where := `q.mode='survey' AND ($1='' OR q.name ILIKE '%'||$1||'%' OR q.title ILIKE '%'||$1||'%' OR q.slug ILIKE '%'||$1||'%') AND (($2='' AND q.status<>'archived') OR q.status=$2)`
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_questionnaires q WHERE `+where, search, string(status)).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `SELECT `+baseColumns+` FROM survey_questionnaires q WHERE `+where+` ORDER BY q.updated_at DESC,q.id DESC LIMIT $3 OFFSET $4`, search, string(status), limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	type listItem struct {
		questionnaire surveyport.Questionnaire
		active        *int64
	}
	baseItems := make([]listItem, 0)
	for rows.Next() {
		q, active, e := scanBase(rows)
		if e != nil {
			return nil, 0, e
		}
		baseItems = append(baseItems, listItem{questionnaire: q, active: active})
	}
	if err = rows.Err(); err != nil {
		return nil, 0, mapError(err)
	}
	// A pgx transaction has one connection. Fully consume and close the base
	// result set before loadDefinition issues child queries on that connection.
	rows.Close()
	items := make([]surveyport.Questionnaire, 0, len(baseItems))
	for _, baseItem := range baseItems {
		if baseItem.active != nil {
			if err = r.loadDefinition(ctx, t, &baseItem.questionnaire, *baseItem.active); err != nil {
				return nil, 0, err
			}
		}
		items = append(items, baseItem.questionnaire)
	}
	return items, total, nil
}

func (r *Repository) LockQuestionnaireStatus(ctx context.Context, id surveyport.ID) (surveyport.QuestionnaireStatus, error) {
	t, err := tx(ctx)
	if err != nil {
		return "", err
	}
	var status surveyport.QuestionnaireStatus
	err = t.QueryRow(ctx, `SELECT status FROM survey_questionnaires WHERE id=$1 FOR UPDATE`, id).Scan(&status)
	return status, mapError(err)
}

func (r *Repository) Get(ctx context.Context, id surveyport.ID, lock bool) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	query := `SELECT ` + baseColumns + ` FROM survey_questionnaires q WHERE q.id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	q, active, err := scanBase(t.QueryRow(ctx, query, id))
	if err != nil {
		return q, err
	}
	if active == nil {
		return q, surveyport.ErrUnavailable
	}
	if err = r.loadDefinition(ctx, t, &q, *active); err != nil {
		return surveyport.Questionnaire{}, err
	}
	return q, nil
}

func (r *Repository) loadDefinition(ctx context.Context, t pgx.Tx, q *surveyport.Questionnaire, versionID int64) error {
	var assessment []byte
	var mode surveyport.QuestionnaireMode
	var display surveyport.AnswerDisplayMode
	if err := t.QueryRow(ctx, `SELECT mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,version_number FROM survey_definition_versions WHERE id=$1 AND questionnaire_id=$2`, versionID, q.ID).Scan(&mode, &display, &q.Title, &q.Description, &assessment, &q.DefinitionVersion); err != nil {
		return mapError(err)
	}
	q.Mode, q.AnswerDisplayMode, q.AssessmentConfig = mode, display, append(json.RawMessage(nil), assessment...)
	rows, err := t.Query(ctx, `SELECT id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation FROM survey_definition_questions WHERE definition_version_id=$1 ORDER BY sort_order,id`, versionID)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	q.Questions = []surveyport.Question{}
	for rows.Next() {
		var item surveyport.Question
		var validation []byte
		if err = rows.Scan(&item.ID, &item.Type, &item.Title, &item.AssessmentDimensionKey, &item.SidebarProfileField, &item.Required, &item.SortOrder, &item.Placeholder, &validation); err != nil {
			return mapError(err)
		}
		if json.Unmarshal(validation, &item.Validation) != nil {
			return surveyport.ErrUnavailable
		}
		item.Options = []surveyport.Option{}
		q.Questions = append(q.Questions, item)
	}
	if err = rows.Err(); err != nil {
		return mapError(err)
	}
	for index := range q.Questions {
		optionRows, e := t.Query(ctx, `SELECT id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order FROM survey_definition_options WHERE question_id=$1 ORDER BY sort_order,id`, q.Questions[index].ID)
		if e != nil {
			return mapError(e)
		}
		for optionRows.Next() {
			var option surveyport.Option
			var tags []byte
			if e = optionRows.Scan(&option.ID, &option.Text, &option.Score, &option.AssessmentTypeKey, &tags, &option.IsOther, &option.OtherPlaceholder, &option.OtherMaximumLength, &option.SortOrder); e != nil {
				optionRows.Close()
				return mapError(e)
			}
			if json.Unmarshal(tags, &option.TagCodes) != nil {
				optionRows.Close()
				return surveyport.ErrUnavailable
			}
			q.Questions[index].Options = append(q.Questions[index].Options, option)
		}
		e = optionRows.Err()
		optionRows.Close()
		if e != nil {
			return mapError(e)
		}
	}
	ruleRows, err := t.Query(ctx, `SELECT minimum_score,maximum_score,tag_codes,sort_order FROM survey_score_rules WHERE definition_version_id=$1 ORDER BY sort_order,id`, versionID)
	if err != nil {
		return mapError(err)
	}
	defer ruleRows.Close()
	q.ScoreRules = []surveyport.ScoreRule{}
	for ruleRows.Next() {
		var rule surveyport.ScoreRule
		var tags []byte
		if err = ruleRows.Scan(&rule.MinimumScore, &rule.MaximumScore, &tags, &rule.SortOrder); err != nil {
			return mapError(err)
		}
		if json.Unmarshal(tags, &rule.TagCodes) != nil {
			return surveyport.ErrUnavailable
		}
		q.ScoreRules = append(q.ScoreRules, rule)
	}
	return mapError(ruleRows.Err())
}

func (r *Repository) Create(ctx context.Context, q surveyport.Questionnaire, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return q, err
	}
	err = t.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,1,$9,$9) RETURNING id,created_at,updated_at`, q.Name, q.Title, q.Description, q.Mode, q.AnswerDisplayMode, q.Slug, q.Status, actor, now).Scan(&q.ID, &q.CreatedAt, &q.UpdatedAt)
	if err != nil {
		return q, mapError(err)
	}
	q.CreatedBy, q.Version = actor, 1
	versionID, err := r.insertVersion(ctx, t, q, 1, actor, now)
	if err != nil {
		return q, err
	}
	if _, err = t.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, q.ID, versionID); err != nil {
		return q, mapError(err)
	}
	return r.Get(ctx, q.ID, false)
}

func (r *Repository) insertVersion(ctx context.Context, t pgx.Tx, q surveyport.Questionnaire, number, actor int64, now time.Time) (int64, error) {
	assessment := q.AssessmentConfig
	if len(assessment) == 0 {
		assessment = json.RawMessage(`{}`)
	}
	definitionRaw, _ := json.Marshal(struct {
		Mode        surveyport.QuestionnaireMode `json:"mode"`
		Display     surveyport.AnswerDisplayMode `json:"answer_display_mode"`
		Title       string                       `json:"title"`
		Description string                       `json:"description"`
		Assessment  json.RawMessage              `json:"assessment_config"`
		Questions   []surveyport.Question        `json:"questions"`
		Rules       []surveyport.ScoreRule       `json:"score_rules"`
	}{q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, q.Questions, q.ScoreRules})
	digest := sha256.Sum256(definitionRaw)
	var versionID int64
	err := t.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, q.ID, number, q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, digest[:], actor, now).Scan(&versionID)
	if err != nil {
		return 0, mapError(err)
	}
	if err = r.insertChildren(ctx, t, versionID, q); err != nil {
		return 0, err
	}
	return versionID, nil
}

func (r *Repository) insertChildren(ctx context.Context, t pgx.Tx, versionID int64, q surveyport.Questionnaire) error {
	for _, question := range q.Questions {
		validation, _ := json.Marshal(question.Validation)
		var questionID int64
		err := t.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, versionID, question.Type, question.Title, question.AssessmentDimensionKey, question.SidebarProfileField, question.Required, question.SortOrder, question.Placeholder, validation).Scan(&questionID)
		if err != nil {
			return mapError(err)
		}
		for _, option := range question.Options {
			tags, _ := json.Marshal(option.TagCodes)
			if _, err = t.Exec(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, questionID, versionID, option.Text, option.Score, option.AssessmentTypeKey, tags, option.IsOther, option.OtherPlaceholder, option.OtherMaximumLength, option.SortOrder); err != nil {
				return mapError(err)
			}
		}
	}
	for _, rule := range q.ScoreRules {
		tags, _ := json.Marshal(rule.TagCodes)
		if _, err := t.Exec(ctx, `INSERT INTO survey_score_rules(definition_version_id,minimum_score,maximum_score,tag_codes,sort_order) VALUES($1,$2,$3,$4,$5)`, versionID, rule.MinimumScore, rule.MaximumScore, tags, rule.SortOrder); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (r *Repository) Replace(ctx context.Context, q surveyport.Questionnaire, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return q, err
	}
	current, err := r.Get(ctx, q.ID, true)
	if err != nil {
		return q, err
	}
	if current.Version != expected {
		return q, surveyport.ErrConflict
	}
	var activeID int64
	var immutable bool
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, q.ID).Scan(&activeID); err != nil {
		return q, mapError(err)
	}
	if err = t.QueryRow(ctx, `SELECT is_immutable FROM survey_definition_versions WHERE id=$1`, activeID).Scan(&immutable); err != nil {
		return q, mapError(err)
	}
	if immutable {
		activeID, err = r.insertVersion(ctx, t, q, expected+1, actor, now)
		if err != nil {
			return q, err
		}
		q.Status = surveyport.StatusDraft
	} else {
		if _, err = t.Exec(ctx, `DELETE FROM survey_definition_questions WHERE definition_version_id=$1`, activeID); err != nil {
			return q, mapError(err)
		}
		if _, err = t.Exec(ctx, `DELETE FROM survey_score_rules WHERE definition_version_id=$1`, activeID); err != nil {
			return q, mapError(err)
		}
		assessment := q.AssessmentConfig
		if len(assessment) == 0 {
			assessment = json.RawMessage(`{}`)
		}
		if _, err = t.Exec(ctx, `UPDATE survey_definition_versions SET mode=$2,answer_display_mode=$3,title_snapshot=$4,description_snapshot=$5,assessment_config=$6,version_number=$7 WHERE id=$1`, activeID, q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, expected+1); err != nil {
			return q, mapError(err)
		}
		if err = r.insertChildren(ctx, t, activeID, q); err != nil {
			return q, err
		}
	}
	command, err := t.Exec(ctx, `UPDATE survey_questionnaires SET name=$2,title=$3,description=$4,mode=$5,answer_display_mode=$6,slug=$7,status=$8,active_definition_version_id=$9,updated_by=$10,updated_at=$11,version=version+1 WHERE id=$1 AND version=$12`, q.ID, q.Name, q.Title, q.Description, q.Mode, q.AnswerDisplayMode, q.Slug, q.Status, activeID, actor, now, expected)
	if err != nil {
		return q, mapError(err)
	}
	if command.RowsAffected() != 1 {
		return q, surveyport.ErrConflict
	}
	return r.Get(ctx, q.ID, false)
}

func (r *Repository) Publish(ctx context.Context, id surveyport.ID, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	current, err := r.Get(ctx, id, true)
	if err != nil {
		return current, err
	}
	if current.Version != expected {
		return current, surveyport.ErrConflict
	}
	var active int64
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, id).Scan(&active); err != nil {
		return current, mapError(err)
	}
	if _, err = t.Exec(ctx, `UPDATE survey_definition_versions SET is_immutable=TRUE,published_at=$2 WHERE id=$1 AND is_immutable=FALSE`, active, now); err != nil {
		return current, mapError(err)
	}
	return r.updateStatus(ctx, t, id, surveyport.StatusPublished, expected, actor, now)
}

func (r *Repository) SetStatus(ctx context.Context, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	if status == surveyport.StatusPublished {
		var immutable bool
		if err = t.QueryRow(ctx, `SELECT v.is_immutable FROM survey_questionnaires q JOIN survey_definition_versions v ON v.id=q.active_definition_version_id WHERE q.id=$1 FOR UPDATE OF q`, id).Scan(&immutable); err != nil {
			return surveyport.Questionnaire{}, mapError(err)
		}
		if !immutable {
			return surveyport.Questionnaire{}, surveyport.ErrConflict
		}
	}
	return r.updateStatus(ctx, t, id, status, expected, actor, now)
}
func (r *Repository) updateStatus(ctx context.Context, t pgx.Tx, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	cmd, err := t.Exec(ctx, `UPDATE survey_questionnaires SET status=$2,updated_by=$3,updated_at=$4,version=version+1 WHERE id=$1 AND version=$5`, id, status, actor, now, expected)
	if err != nil {
		return surveyport.Questionnaire{}, mapError(err)
	}
	if cmd.RowsAffected() != 1 {
		return surveyport.Questionnaire{}, surveyport.ErrConflict
	}
	return r.Get(ctx, id, false)
}

func (r *Repository) DeleteDraft(ctx context.Context, id surveyport.ID, expected int64) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	var status surveyport.QuestionnaireStatus
	var version int64
	if err = t.QueryRow(ctx, `SELECT status,version FROM survey_questionnaires WHERE id=$1 FOR UPDATE`, id).Scan(&status, &version); err != nil {
		return mapError(err)
	}
	if status != surveyport.StatusDraft || version != expected {
		return surveyport.ErrConflict
	}
	var referenced bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_submissions WHERE questionnaire_id=$1)`, id).Scan(&referenced); err != nil {
		return mapError(err)
	}
	if referenced {
		return surveyport.ErrReferenced
	}
	if _, err = t.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=NULL WHERE id=$1`, id); err != nil {
		return mapError(err)
	}
	if _, err = t.Exec(ctx, `DELETE FROM survey_definition_versions WHERE questionnaire_id=$1`, id); err != nil {
		return mapError(err)
	}
	cmd, err := t.Exec(ctx, `DELETE FROM survey_questionnaires WHERE id=$1`, id)
	if err != nil {
		return mapError(err)
	}
	if cmd.RowsAffected() != 1 {
		return surveyport.ErrNotFound
	}
	return nil
}

func (r *Repository) Reserve(ctx context.Context, res surveyapp.Reservation) (surveyapp.Receipt, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.Receipt{}, false, err
	}
	out, err := scanOperationReceipt(t.QueryRow(ctx, `INSERT INTO survey_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb)`, res.Operation, res.ActorScope, res.KeyDigest[:], res.PayloadDigest[:], res.CreatedAt))
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, false, mapError(err)
	}
	out, err = scanOperationReceipt(t.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb) FROM survey_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3 FOR UPDATE`, res.Operation, res.ActorScope, res.KeyDigest[:]))
	return out, false, mapError(err)
}
func (r *Repository) Complete(ctx context.Context, id int64, result json.RawMessage, now time.Time) (surveyapp.Receipt, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.Receipt{}, err
	}
	out, err := scanOperationReceipt(t.QueryRow(ctx, `UPDATE survey_operation_receipts SET state='completed',result_snapshot=$2,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, id, result, now))
	return out, mapError(err)
}

func scanOperationReceipt(row scanner) (surveyapp.Receipt, error) {
	var out surveyapp.Receipt
	var keyDigest, payloadDigest []byte
	if err := row.Scan(&out.ID, &out.Operation, &out.ActorScope, &keyDigest, &payloadDigest, &out.State, &out.Result); err != nil {
		return out, err
	}
	if len(keyDigest) != sha256.Size || len(payloadDigest) != sha256.Size {
		return surveyapp.Receipt{}, surveyport.ErrUnavailable
	}
	copy(out.KeyDigest[:], keyDigest)
	copy(out.PayloadDigest[:], payloadDigest)
	return out, nil
}
func (r *Repository) AppendAuditAndOutbox(ctx context.Context, event string, id surveyport.ID, actor string, payload json.RawMessage, key string, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,metadata,occurred_at) VALUES($1,'questionnaire',$2,$3,$4,$5)`, event, id, actor, payload, now); err != nil {
		return mapError(err)
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_outbox(event_type,aggregate_type,aggregate_id,payload,idempotency_key,occurred_at) VALUES($1,'questionnaire',$2,$3,$4,$5)`, event, id, payload, key, now); err != nil {
		return mapError(err)
	}
	return nil
}

func (r *Repository) GetPublishedBySlug(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	q, active, err := scanBase(t.QueryRow(ctx, `SELECT `+baseColumns+` FROM survey_questionnaires q WHERE q.slug=$1 AND q.status='published'`, slug))
	if err != nil {
		return q, err
	}
	if active == nil {
		return q, surveyport.ErrUnavailable
	}
	if err = r.loadDefinition(ctx, t, &q, *active); err != nil {
		return surveyport.Questionnaire{}, err
	}
	return q, nil
}

// GetBySlug is the minimal Survey-owned lookup used to recover a retained
// idempotency receipt or completion claim. It deliberately does not make the
// questionnaire publicly readable: callers must still use GetPublishedBySlug
// before serving a new answer form.
func (r *Repository) GetBySlug(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	q, _, err := scanBase(t.QueryRow(ctx, `SELECT `+baseColumns+` FROM survey_questionnaires q WHERE q.slug=$1`, slug))
	return q, err
}

// FindSubmissionByKey keeps payload conflict comparison within Survey's
// persistence boundary. The returned Submission is application-internal;
// public handlers only receive its safe receipt projection.
func (r *Repository) FindSubmissionByKey(ctx context.Context, questionnaireID surveyport.ID, keyDigest, payloadDigest [32]byte) (surveyport.Submission, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, false, err
	}
	var id int64
	var existingPayload []byte
	err = t.QueryRow(ctx, `SELECT id,payload_digest FROM survey_submissions WHERE questionnaire_id=$1 AND submission_key_digest=$2 FOR UPDATE`, questionnaireID, keyDigest[:]).Scan(&id, &existingPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return surveyport.Submission{}, false, nil
	}
	if err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	if string(existingPayload) != string(payloadDigest[:]) {
		return surveyport.Submission{}, false, surveyport.ErrConflict
	}
	stored, err := r.GetSubmission(ctx, surveyport.ID(id))
	return stored, true, err
}

func (r *Repository) CreateSubmission(ctx context.Context, input surveyapp.PersistSubmission) (surveyport.Submission, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, false, err
	}
	// Preserve the established same-key replay before the new customer claim is
	// considered. This also keeps historical, pre-cutover submission-key
	// receipts readable without backfilling them into the new claim table.
	existing, found, err := r.FindSubmissionByKey(ctx, input.Questionnaire.ID, input.SubmissionKeyDigest, input.PayloadDigest)
	if err != nil {
		return surveyport.Submission{}, false, err
	}
	if found {
		return existing, false, nil
	}

	// The PK serializes competing submission keys for the same canonical
	// customer and questionnaire. Because this runs in the caller's UoW, a
	// later failure rolls the placeholder back with the submission, answers,
	// audit, Outbox, and any accepted external effect.
	claim, err := t.Exec(ctx, `INSERT INTO survey_submission_claims(questionnaire_id,customer_id,claimed_at) VALUES($1,$2,$3) ON CONFLICT(questionnaire_id,customer_id) DO NOTHING`, input.Questionnaire.ID, input.Command.Identity.CustomerID, input.Now)
	if err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	if claim.RowsAffected() != 1 {
		// A concurrent same-key request may have observed no submission before
		// blocking on the claim. Re-read it after the conflict to preserve the
		// original idempotent receipt; all other keys are already-submitted.
		existing, found, err = r.FindSubmissionByKey(ctx, input.Questionnaire.ID, input.SubmissionKeyDigest, input.PayloadDigest)
		if err != nil {
			return surveyport.Submission{}, false, err
		}
		if found {
			return existing, false, nil
		}
		return surveyport.Submission{}, false, surveyport.ErrAlreadySubmitted
	}
	var definitionID int64
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1 AND status='published' FOR SHARE`, input.Questionnaire.ID).Scan(&definitionID); err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	resultRaw, _ := json.Marshal(input.Result)
	var submissionID int64
	err = t.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,identity_reason,evidence_digest,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at,created_at) VALUES($1,$2,$3,$4,$5,'',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17) RETURNING id`, input.Questionnaire.ID, definitionID, input.Questionnaire.DefinitionVersion, input.Command.Identity.CustomerID, input.Command.Identity.State, decodeEvidence(input.Command.Identity.EvidenceDigest), input.SubmissionKeyDigest[:], input.PayloadDigest[:], input.Questionnaire.Slug, input.Questionnaire.Title, input.Questionnaire.Mode, input.TotalScore, resultRaw, input.Command.SourceChannel, input.Command.CampaignID, input.Command.StaffID, input.Now).Scan(&submissionID)
	if err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	if claim, err = t.Exec(ctx, `UPDATE survey_submission_claims SET submission_id=$3 WHERE questionnaire_id=$1 AND customer_id=$2 AND submission_id IS NULL`, input.Questionnaire.ID, input.Command.Identity.CustomerID, submissionID); err != nil || claim.RowsAffected() != 1 {
		if err != nil {
			return surveyport.Submission{}, false, mapError(err)
		}
		return surveyport.Submission{}, false, surveyport.ErrUnavailable
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_result_tokens(submission_id,token_digest,created_at) VALUES($1,$2,$3)`, submissionID, input.TokenDigest[:], input.Now); err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	for _, answer := range input.Answers {
		var encrypted []byte
		if answer.TextValue != "" {
			encrypted, err = r.cipher.Encrypt(answer.TextValue)
			if err != nil {
				return surveyport.Submission{}, false, surveyport.ErrUnavailable
			}
		}
		options, _ := json.Marshal(answer.SelectedOptions)
		answerRaw, _ := json.Marshal(answer)
		answerDigest := sha256.Sum256(answerRaw)
		var questionID any
		if answer.QuestionID != nil {
			questionID = *answer.QuestionID
		}
		if _, err = t.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,definition_question_id,legacy_source_question_id,question_type,question_title_snapshot,question_sort_order,required_snapshot,selected_options_snapshot,text_value_ciphertext,text_value_masked,answer_digest,score_snapshot,legacy_definition_missing,created_at) VALUES($1,$2,$3,$4,$5,$6,FALSE,$7,$8,$9,$10,$11,$12,$13)`, submissionID, questionID, answer.LegacySourceQuestionID, answer.QuestionType, answer.QuestionTitle, answer.SortOrder, options, encrypted, answer.TextValueMasked, answerDigest[:], answer.Score, answer.LegacyDefinitionMissing, input.Now); err != nil {
			return surveyport.Submission{}, false, mapError(err)
		}
	}
	stored, err := r.GetSubmission(ctx, surveyport.ID(submissionID))
	return stored, true, err
}

func (r *Repository) HasSubmissionClaim(ctx context.Context, questionnaireID surveyport.ID, customerID customerdomain.CustomerID) (bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var claimed bool
	err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_submission_claims WHERE questionnaire_id=$1 AND customer_id=$2 AND submission_id IS NOT NULL)`, questionnaireID, customerID).Scan(&claimed)
	return claimed, mapError(err)
}

func (r *Repository) RecordPhoneBinding(ctx context.Context, submissionID, answerID surveyport.ID, customerID, identityID int64, status identityport.DeclaredAttachStatus, evidence [32]byte, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	var identity any
	if identityID > 0 {
		identity = identityID
	}
	_, err = t.Exec(ctx, `INSERT INTO survey_phone_binding_receipts(submission_id,answer_id,customer_id,identity_id,status,evidence_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(answer_id) DO NOTHING`, submissionID, answerID, customerID, identity, status, evidence[:], now)
	return mapError(err)
}

func decodeEvidence(value string) []byte {
	if value == "" {
		return nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil
	}
	return decoded
}

func (r *Repository) GetSubmissionByTokenDigest(ctx context.Context, digest [32]byte) (surveyport.Submission, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, err
	}
	var id surveyport.ID
	err = t.QueryRow(ctx, `SELECT s.id FROM survey_result_tokens token JOIN survey_submissions s ON s.id=token.submission_id WHERE token.token_digest=$1 AND token.revoked_at IS NULL AND (token.expires_at IS NULL OR token.expires_at>CURRENT_TIMESTAMP)`, digest[:]).Scan(&id)
	if err != nil {
		return surveyport.Submission{}, mapError(err)
	}
	return r.GetSubmission(ctx, id)
}
func (r *Repository) GetSubmission(ctx context.Context, id surveyport.ID) (surveyport.Submission, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, err
	}
	return r.loadSubmission(ctx, t, id)
}
func (r *Repository) loadSubmission(ctx context.Context, t pgx.Tx, id surveyport.ID) (surveyport.Submission, error) {
	var s surveyport.Submission
	var customer *int64
	var evidence []byte
	var resultRaw []byte
	err := t.QueryRow(ctx, `SELECT id,questionnaire_id,definition_version_number,customer_id,identity_state,evidence_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at FROM survey_submissions WHERE id=$1`, id).Scan(&s.ID, &s.QuestionnaireID, &s.DefinitionVersion, &customer, &s.Identity.State, &evidence, &s.QuestionnaireSlug, &s.QuestionnaireTitle, &s.Mode, &s.TotalScore, &resultRaw, &s.SourceChannel, &s.CampaignID, &s.StaffID, &s.SubmittedAt)
	if err != nil {
		return s, mapError(err)
	}
	if customer != nil {
		typed := customerdomain.CustomerID(*customer)
		s.Identity.CustomerID = &typed
	}
	if len(evidence) == 32 {
		s.Identity.EvidenceDigest = hex.EncodeToString(evidence)
	}
	if json.Unmarshal(resultRaw, &s.Result) != nil {
		return s, surveyport.ErrUnavailable
	}
	s.Answers = []surveyport.AnswerSnapshot{}
	rows, err := t.Query(ctx, `SELECT a.id,a.definition_question_id,a.legacy_source_question_id,a.question_type,a.question_title_snapshot,a.question_sort_order,a.selected_options_snapshot,a.text_value_ciphertext,a.text_value_masked,a.score_snapshot,a.legacy_definition_missing,COALESCE(b.status,'') FROM survey_submission_answers a LEFT JOIN survey_phone_binding_receipts b ON b.answer_id=a.id WHERE a.submission_id=$1 ORDER BY a.question_sort_order,a.id`, id)
	if err != nil {
		return s, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a surveyport.AnswerSnapshot
		var qid *int64
		var options, encrypted []byte
		if err = rows.Scan(&a.ID, &qid, &a.LegacySourceQuestionID, &a.QuestionType, &a.QuestionTitle, &a.SortOrder, &options, &encrypted, &a.TextValueMasked, &a.Score, &a.LegacyDefinitionMissing, &a.PhoneBindingStatus); err != nil {
			return s, mapError(err)
		}
		if qid != nil {
			typed := surveyport.ID(*qid)
			a.QuestionID = &typed
		}
		if json.Unmarshal(options, &a.SelectedOptions) != nil {
			return s, surveyport.ErrUnavailable
		}
		// Text and mobile values remain encrypted at rest. This default reader is
		// used by list, detail, result and export projections, none of which is a
		// separately authorized PII reveal operation.
		_ = encrypted
		s.Answers = append(s.Answers, a)
	}
	return s, mapError(rows.Err())
}

func (r *Repository) ListSubmissions(ctx context.Context, id surveyport.ID, limit, offset int32, state surveyport.IdentityState) ([]surveyport.Submission, int64, error) {
	return r.listSubmissionQuery(ctx, `questionnaire_id=$1 AND ($2='' OR identity_state=$2)`, []any{id, string(state)}, limit, offset)
}
func (r *Repository) CustomerHistory(ctx context.Context, customer int64, limit, offset int32) ([]surveyport.Submission, int64, error) {
	return r.listSubmissionQuery(ctx, `customer_id=$1`, []any{customer}, limit, offset)
}
func (r *Repository) CustomerHistoryWindow(ctx context.Context, query surveyport.CustomerHistoryQuery) ([]surveyport.Submission, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	var afterAt any
	if !query.AfterAt.IsZero() {
		afterAt = query.AfterAt.UTC()
	}
	rows, err := t.Query(ctx, `SELECT id FROM survey_submissions
		WHERE customer_id=$1 AND submitted_at <= $2
		AND ($3::timestamptz IS NULL OR (submitted_at,id) < ($3,$4))
		ORDER BY submitted_at DESC,id DESC LIMIT $5`, query.CustomerID, query.Watermark.UTC(), afterAt, query.AfterID, query.Limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := []surveyport.ID{}
	for rows.Next() {
		var id surveyport.ID
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	items := make([]surveyport.Submission, 0, len(ids))
	for _, id := range ids {
		item, loadErr := r.loadSubmission(ctx, t, id)
		if loadErr != nil {
			return nil, loadErr
		}
		items = append(items, item)
	}
	return items, nil
}

// ExternalSubmissions reads only the Survey-owned historic union projection.
// The Host has already authenticated and authorized the caller and obtained
// these source values through OneID; this store never reads Identity tables or
// treats the historic union as an identity assertion.
func (r *Repository) ExternalSubmissions(ctx context.Context, query surveyport.ExternalSubmissionQuery) (surveyport.ExternalSubmissionPage, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.ExternalSubmissionPage{}, err
	}
	questionnaireIDs, err := externalQuestionnaireTargets(ctx, t, query.QuestionnaireSourceID)
	if err != nil {
		return surveyport.ExternalSubmissionPage{}, err
	}
	// $1 is always the OneID-resolved V3 customer. $2 is a set of historical
	// source facts. The union branches are deliberately disjoint: a legacy
	// source row can only match its preserved historic union, while a native
	// V3 row can only match its canonical customer_id.
	args := []any{query.CustomerID, query.HistoricalUnionIDs}
	legacyWhere := []string{"projection.historical_unionid = ANY($2::text[])"}
	nativeWhere := []string{"submission.customer_id=$1", `NOT EXISTS (SELECT 1 FROM survey_legacy_external_projections legacy_projection WHERE legacy_projection.submission_id=submission.id)`}
	if len(questionnaireIDs) > 0 {
		args = append(args, questionnaireIDs)
		condition := "submission.questionnaire_id=ANY($" + strconv.Itoa(len(args)) + "::bigint[])"
		legacyWhere, nativeWhere = append(legacyWhere, condition), append(nativeWhere, condition)
	}
	if !query.SubmittedFrom.IsZero() {
		args = append(args, query.SubmittedFrom.UTC())
		condition := "submission.submitted_at >= $" + strconv.Itoa(len(args))
		legacyWhere, nativeWhere = append(legacyWhere, condition), append(nativeWhere, condition)
	}
	if !query.SubmittedTo.IsZero() {
		args = append(args, query.SubmittedTo.UTC())
		operator := "<="
		if query.SubmittedEndExclusive {
			operator = "<"
		}
		condition := "submission.submitted_at " + operator + " $" + strconv.Itoa(len(args))
		legacyWhere, nativeWhere = append(legacyWhere, condition), append(nativeWhere, condition)
	}
	eligible := `WITH eligible AS (
		SELECT submission.id AS submission_id,submission_map.source_system,submission_map.source_pk AS source_record_id,projection.historical_unionid,questionnaire_map.source_pk AS questionnaire_source_id,
			submission.definition_version_number AS definition_version,submission.title_snapshot AS questionnaire_title,submission.submitted_at,submission.result_snapshot,TRUE AS legacy
		FROM survey_submissions submission
		JOIN survey_legacy_external_projections projection ON projection.submission_id=submission.id
		JOIN survey_migration_source_map submission_map ON submission_map.target_table='survey_submissions'
			AND submission_map.target_pk=submission.id
			AND submission_map.source_table='questionnaire_submissions'
			AND submission_map.import_state='imported'
		JOIN survey_migration_source_map questionnaire_map ON questionnaire_map.source_system=submission_map.source_system
			AND questionnaire_map.source_table='questionnaires'
			AND questionnaire_map.target_table='survey_questionnaires'
			AND questionnaire_map.target_pk=submission.questionnaire_id
			AND questionnaire_map.import_state='imported'
		WHERE ` + strings.Join(legacyWhere, " AND ") + `
		UNION ALL
		SELECT submission.id AS submission_id,'aicrm_v3'::text AS source_system,submission.id::text AS source_record_id,''::text AS historical_unionid,
			COALESCE(native_questionnaire_map.source_pk,submission.questionnaire_id::text) AS questionnaire_source_id,
			submission.definition_version_number AS definition_version,submission.title_snapshot AS questionnaire_title,submission.submitted_at,submission.result_snapshot,FALSE AS legacy
		FROM survey_submissions submission
		LEFT JOIN LATERAL (
			SELECT source_pk FROM survey_migration_source_map
			WHERE target_table='survey_questionnaires' AND target_pk=submission.questionnaire_id
				AND source_table='questionnaires' AND import_state='imported'
			ORDER BY id LIMIT 1
		) native_questionnaire_map ON TRUE
		WHERE ` + strings.Join(nativeWhere, " AND ") + `
	)`
	sourceWhere := []string{"TRUE"}
	if query.SourceSystem != "" {
		args = append(args, query.SourceSystem)
		sourceWhere = append(sourceWhere, "source_system=$"+strconv.Itoa(len(args)))
		args = append(args, query.SourceRecordID)
		sourceWhere = append(sourceWhere, "source_record_id=$"+strconv.Itoa(len(args)))
	}
	if !query.BeforeSubmittedAt.IsZero() {
		args = append(args, query.BeforeSubmittedAt.UTC(), int64(query.BeforeSubmissionID))
		sourceWhere = append(sourceWhere, "(submitted_at,submission_id) < ($"+strconv.Itoa(len(args)-1)+",$"+strconv.Itoa(len(args))+")")
	}
	var total int64
	if err = t.QueryRow(ctx, eligible+` SELECT count(*) FROM eligible WHERE `+strings.Join(sourceWhere, " AND "), args...).Scan(&total); err != nil {
		return surveyport.ExternalSubmissionPage{}, mapError(err)
	}
	limitPosition := len(args) + 1
	offsetPosition := len(args) + 2
	args = append(args, query.Limit, query.Offset)
	rows, err := t.Query(ctx, eligible+` SELECT submission_id,source_system,source_record_id,historical_unionid,questionnaire_source_id,definition_version,questionnaire_title,submitted_at,result_snapshot,legacy FROM eligible WHERE `+strings.Join(sourceWhere, " AND ")+` ORDER BY submitted_at DESC,submission_id DESC LIMIT $`+strconv.Itoa(limitPosition)+` OFFSET $`+strconv.Itoa(offsetPosition), args...)
	if err != nil {
		return surveyport.ExternalSubmissionPage{}, mapError(err)
	}
	type externalSubmissionRow struct {
		id   surveyport.ID
		item surveyport.ExternalSubmission
	}
	base := make([]externalSubmissionRow, 0)
	for rows.Next() {
		var row externalSubmissionRow
		var questionnaireSource string
		var result []byte
		if err = rows.Scan(&row.id, &row.item.SourceSystem, &row.item.SourceRecordID, &row.item.HistoricalUnionID, &questionnaireSource, &row.item.DefinitionVersion, &row.item.QuestionnaireTitle, &row.item.SubmittedAt, &result, &row.item.Legacy); err != nil {
			rows.Close()
			return surveyport.ExternalSubmissionPage{}, mapError(err)
		}
		parsedSource, parseErr := strconv.ParseInt(questionnaireSource, 10, 64)
		if parseErr != nil || parsedSource < 1 {
			rows.Close()
			return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
		}
		row.item.SubmissionID = row.id
		if row.item.SourceSystem == "" || row.item.SourceRecordID == "" {
			rows.Close()
			return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
		}
		row.item.QuestionnaireSourceID = parsedSource
		row.item.FinalTags, row.item.AssessmentResult, err = externalAssessmentProjection(result)
		if err != nil {
			rows.Close()
			return surveyport.ExternalSubmissionPage{}, err
		}
		row.item.Answers = []surveyport.ExternalSubmissionAnswer{}
		base = append(base, row)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return surveyport.ExternalSubmissionPage{}, mapError(err)
	}
	// pgx transactions share one connection. Close the page cursor before
	// fetching its answers so the child query cannot leave the connection busy.
	rows.Close()

	if len(base) > 0 {
		ids := make([]int64, 0, len(base))
		byID := make(map[surveyport.ID]*surveyport.ExternalSubmission, len(base))
		for index := range base {
			ids = append(ids, int64(base[index].id))
			byID[base[index].id] = &base[index].item
		}
		answerRows, queryErr := t.Query(ctx, `SELECT submission_id,question_title_snapshot,selected_options_snapshot,text_value_ciphertext,score_snapshot FROM survey_submission_answers WHERE submission_id=ANY($1::bigint[]) ORDER BY submission_id,id`, ids)
		if queryErr != nil {
			return surveyport.ExternalSubmissionPage{}, mapError(queryErr)
		}
		for answerRows.Next() {
			var submissionID surveyport.ID
			var answer surveyport.ExternalSubmissionAnswer
			var selected, encrypted []byte
			if queryErr = answerRows.Scan(&submissionID, &answer.QuestionTitle, &selected, &encrypted, &answer.ScoreContribution); queryErr != nil {
				answerRows.Close()
				return surveyport.ExternalSubmissionPage{}, mapError(queryErr)
			}
			var options []surveyport.SelectedOptionSnapshot
			if json.Unmarshal(selected, &options) != nil {
				answerRows.Close()
				return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
			}
			answer.SelectedOptionTexts = make([]string, 0, len(options))
			for _, option := range options {
				answer.SelectedOptionTexts = append(answer.SelectedOptionTexts, option.OptionText)
			}
			if len(encrypted) > 0 {
				answer.TextValue, queryErr = r.cipher.Decrypt(encrypted)
				if queryErr != nil {
					answerRows.Close()
					return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
				}
			}
			item, found := byID[submissionID]
			if !found {
				answerRows.Close()
				return surveyport.ExternalSubmissionPage{}, surveyport.ErrUnavailable
			}
			item.Answers = append(item.Answers, answer)
		}
		if queryErr = answerRows.Err(); queryErr != nil {
			answerRows.Close()
			return surveyport.ExternalSubmissionPage{}, mapError(queryErr)
		}
		answerRows.Close()
	}
	page := surveyport.ExternalSubmissionPage{Items: make([]surveyport.ExternalSubmission, 0, len(base)), Total: total, Limit: query.Limit, Offset: query.Offset}
	for _, row := range base {
		page.Items = append(page.Items, row.item)
	}
	return page, nil
}

// externalQuestionnaireTargets gives a frozen donor questionnaire ID an
// explicit meaning. A mapped source ID selects its mapped V3 questionnaire.
// If an unrelated native questionnaire has the same numeric ID, the request
// is ambiguous and fails closed rather than guessing which collection to read.
func externalQuestionnaireTargets(ctx context.Context, t pgx.Tx, sourceID int64) ([]int64, error) {
	if sourceID == 0 {
		return nil, nil
	}
	rows, err := t.Query(ctx, `SELECT DISTINCT target_pk FROM survey_migration_source_map WHERE source_table='questionnaires' AND target_table='survey_questionnaires' AND source_pk=$1 AND import_state='imported' AND target_pk IS NOT NULL ORDER BY target_pk`, strconv.FormatInt(sourceID, 10))
	if err != nil {
		return nil, mapError(err)
	}
	mapped := []int64{}
	for rows.Next() {
		var target int64
		if err = rows.Scan(&target); err != nil {
			rows.Close()
			return nil, mapError(err)
		}
		mapped = append(mapped, target)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, mapError(err)
	}
	rows.Close()
	if len(mapped) == 0 {
		return []int64{sourceID}, nil
	}
	var collides bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_questionnaires WHERE id=$1 AND NOT (id=ANY($2::bigint[])))`, sourceID, mapped).Scan(&collides); err != nil {
		return nil, mapError(err)
	}
	if collides {
		return nil, surveyport.ErrConflict
	}
	return mapped, nil
}

func externalAssessmentProjection(raw []byte) (json.RawMessage, json.RawMessage, error) {
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(raw, &snapshot) != nil || snapshot == nil {
		return nil, nil, surveyport.ErrUnavailable
	}
	finalTags := json.RawMessage(`[]`)
	if sourceTags, found := snapshot["_legacy_final_tags"]; found {
		var array []json.RawMessage
		if json.Unmarshal(sourceTags, &array) != nil {
			return nil, nil, surveyport.ErrUnavailable
		}
		finalTags = append(json.RawMessage(nil), sourceTags...)
	} else if nativeTags, found := snapshot["tag_codes"]; found {
		// Native V3 assessments retain their computed tag_codes in the
		// AssessmentResult snapshot. This is the native equivalent of the
		// donor's final_tags field; no historical identity fact is inferred.
		var array []json.RawMessage
		if json.Unmarshal(nativeTags, &array) != nil {
			return nil, nil, surveyport.ErrUnavailable
		}
		finalTags = append(json.RawMessage(nil), nativeTags...)
	}
	delete(snapshot, "_legacy_final_tags")
	delete(snapshot, "_legacy_matched_by")
	assessment, err := json.Marshal(snapshot)
	if err != nil {
		return nil, nil, surveyport.ErrUnavailable
	}
	return finalTags, assessment, nil
}

func (r *Repository) listSubmissionQuery(ctx context.Context, where string, args []any, limit, offset int32) ([]surveyport.Submission, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	limitPosition := len(args) + 1
	offsetPosition := len(args) + 2
	args = append(args, limit, offset)
	rows, err := t.Query(ctx, `SELECT id FROM survey_submissions WHERE `+where+` ORDER BY submitted_at DESC,id DESC LIMIT $`+fmt.Sprint(limitPosition)+` OFFSET $`+fmt.Sprint(offsetPosition), args...)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	ids := []surveyport.ID{}
	for rows.Next() {
		var id surveyport.ID
		if err = rows.Scan(&id); err != nil {
			return nil, 0, mapError(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, mapError(err)
	}
	items := make([]surveyport.Submission, 0, len(ids))
	for _, id := range ids {
		item, e := r.loadSubmission(ctx, t, id)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, item)
	}
	return items, total, nil
}
func (r *Repository) SubmissionAnalytics(ctx context.Context, id surveyport.ID) (surveyport.Analytics, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Analytics{}, err
	}
	var a surveyport.Analytics
	err = t.QueryRow(ctx, `SELECT q.id,v.version_number,q.slug,q.status,count(s.id),COALESCE(avg(s.total_score),0) FROM survey_questionnaires q JOIN survey_definition_versions v ON v.id=q.active_definition_version_id LEFT JOIN survey_submissions s ON s.questionnaire_id=q.id WHERE q.id=$1 GROUP BY q.id,v.version_number`, id).Scan(&a.QuestionnaireID, &a.DefinitionVersion, &a.Slug, &a.State, &a.SubmissionCount, &a.AverageScore)
	return a, mapError(err)
}

func (r *Repository) ListOperationReceipts(ctx context.Context, id surveyport.ID, limit, offset int32) ([]surveyport.OperationReceipt, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := `($1=0 OR questionnaire_id=$1)`
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_external_operation_receipts WHERE `+where, id).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `SELECT id,questionnaire_id,submission_id,operation_kind,status,failure_category,occurrence_count,occurred_at,read_only_legacy,replayable,COALESCE(source_pk,''),provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE `+where+` ORDER BY occurred_at DESC,id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	items := make([]surveyport.OperationReceipt, 0)
	for rows.Next() {
		var item surveyport.OperationReceipt
		var submission *int64
		if err = rows.Scan(&item.ID, &item.QuestionnaireID, &submission, &item.OperationKind, &item.Status, &item.FailureCategory, &item.OccurrenceCount, &item.OccurredAt, &item.ReadOnlyLegacy, &item.Replayable, &item.SourcePK, &item.ProviderCallAttempted, &item.ProviderRealCallExecuted, &item.ProviderResultReceived, &item.ProviderAttemptNumber); err != nil {
			return nil, 0, mapError(err)
		}
		if submission != nil {
			value := surveyport.ID(*submission)
			item.SubmissionID = &value
		}
		item.RealEffectExecuted = item.ProviderRealCallExecuted != nil && *item.ProviderRealCallExecuted || item.ReadOnlyLegacy && item.Status == "legacy_success"
		items = append(items, item)
	}
	return items, total, mapError(rows.Err())
}

const legacySubmissionSelect = `SELECT s.id,sm.source_pk::bigint,qm.source_pk::bigint,s.questionnaire_id,s.customer_id,COALESCE(s.result_snapshot->>'_legacy_matched_by',s.identity_reason),s.source_channel,s.total_score,COALESCE(s.result_snapshot->'_legacy_final_tags','[]'::jsonb),s.submitted_at,s.created_at FROM survey_submissions s JOIN survey_migration_source_map sm ON sm.target_table='survey_submissions' AND sm.target_pk=s.id AND sm.source_table='questionnaire_submissions' JOIN survey_migration_source_map qm ON qm.target_table='survey_questionnaires' AND qm.target_pk=s.questionnaire_id AND qm.source_table='questionnaires'`

func scanLegacySubmission(row scanner) (surveyport.LegacySubmission, error) {
	var item surveyport.LegacySubmission
	err := row.Scan(&item.ID, &item.SourceID, &item.QuestionnaireSourceID, &item.QuestionnaireID, &item.CustomerID, &item.MatchedBy, &item.SourceChannel, &item.TotalScore, &item.FinalTags, &item.SubmittedAt, &item.CreatedAt)
	return item, mapError(err)
}

func (r *Repository) ListLegacyUnresolved(ctx context.Context, questionnaire surveyport.ID, limit, offset int32) ([]surveyport.LegacySubmission, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := `s.identity_state IN ('unresolved','conflict') AND ($1=0 OR s.questionnaire_id=$1)`
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM (`+legacySubmissionSelect+` WHERE `+where+`) x`, questionnaire).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, legacySubmissionSelect+` WHERE `+where+` ORDER BY s.submitted_at DESC,s.id DESC LIMIT $2 OFFSET $3`, questionnaire, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	items := make([]surveyport.LegacySubmission, 0)
	for rows.Next() {
		item, e := scanLegacySubmission(rows)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, item)
	}
	return items, total, mapError(rows.Err())
}

func (r *Repository) GetLegacyUnresolved(ctx context.Context, id surveyport.ID) (surveyport.LegacySubmission, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.LegacySubmission{}, err
	}
	return scanLegacySubmission(t.QueryRow(ctx, legacySubmissionSelect+` WHERE s.id=$1 AND s.identity_state IN ('unresolved','conflict')`, id))
}

func (r *Repository) ListLegacyAnswers(ctx context.Context, submission surveyport.ID, limit, offset int32) ([]surveyport.LegacyAnswer, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_submission_answers WHERE submission_id=$1`, submission).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `SELECT a.id,am.source_pk::bigint,a.submission_id,sm.source_pk::bigint,COALESCE(a.legacy_source_question_id,0),a.question_type,a.question_title_snapshot,a.text_value_masked,a.selected_options_snapshot,a.score_snapshot,a.created_at FROM survey_submission_answers a JOIN survey_migration_source_map am ON am.target_table='survey_submission_answers' AND am.target_pk=a.id AND am.source_table='questionnaire_submission_answers' JOIN survey_migration_source_map sm ON sm.target_table='survey_submissions' AND sm.target_pk=a.submission_id AND sm.source_table='questionnaire_submissions' WHERE a.submission_id=$1 ORDER BY a.id LIMIT $2 OFFSET $3`, submission, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	items := make([]surveyport.LegacyAnswer, 0)
	for rows.Next() {
		var item surveyport.LegacyAnswer
		var raw []byte
		if err = rows.Scan(&item.ID, &item.SourceID, &item.SubmissionID, &item.SubmissionSourceID, &item.QuestionSourceID, &item.QuestionType, &item.QuestionTitle, &item.TextValue, &raw, &item.ScoreContribution, &item.CreatedAt); err != nil {
			return nil, 0, mapError(err)
		}
		var options []surveyport.SelectedOptionSnapshot
		if json.Unmarshal(raw, &options) != nil {
			return nil, 0, surveyport.ErrUnavailable
		}
		ids := make([]surveyport.ID, 0, len(options))
		texts := make([]string, 0, len(options))
		scores := make([]float64, 0, len(options))
		tags := make([][]string, 0, len(options))
		for _, option := range options {
			ids = append(ids, option.OptionID)
			texts = append(texts, option.OptionText)
			scores = append(scores, option.Score)
			tags = append(tags, option.TagCodes)
		}
		item.SelectedOptionIDs, _ = json.Marshal(ids)
		item.SelectedOptionTexts, _ = json.Marshal(texts)
		item.SelectedOptionScores, _ = json.Marshal(scores)
		item.SelectedOptionTags, _ = json.Marshal(tags)
		items = append(items, item)
	}
	return items, total, mapError(rows.Err())
}

func (r *Repository) GetOperationConfiguration(ctx context.Context, id surveyport.ID) (surveyport.OperationConfiguration, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.OperationConfiguration{}, err
	}
	var value surveyport.OperationConfiguration
	err = t.QueryRow(ctx, `SELECT q.id,COALESCE(c.completion_navigation_ref,''),COALESCE(c.completion_target,'{}'::jsonb),c.completion_channel_id,COALESCE(c.lead_qr_title,''),COALESCE(c.lead_qr_subtitle,''),COALESCE(c.external_push_enabled,FALSE),COALESCE(c.external_push_configuration_ref,''),COALESCE(c.external_push_metadata,'{}'::jsonb),COALESCE(c.version,0),COALESCE(c.updated_at,q.updated_at) FROM survey_questionnaires q LEFT JOIN survey_operation_configurations c ON c.questionnaire_id=q.id WHERE q.id=$1`, id).Scan(&value.QuestionnaireID, &value.CompletionNavigationRef, &value.CompletionTarget, &value.CompletionChannelID, &value.LeadQRTitle, &value.LeadQRSubtitle, &value.ExternalPushEnabled, &value.ExternalPushConfigurationRef, &value.ExternalPushMetadata, &value.Version, &value.UpdatedAt)
	return value, mapError(err)
}

func (r *Repository) SaveOperationConfiguration(ctx context.Context, value surveyport.OperationConfiguration, actor int64, now time.Time) (surveyport.OperationConfiguration, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.OperationConfiguration{}, err
	}
	var stored surveyport.OperationConfiguration
	err = t.QueryRow(ctx, `INSERT INTO survey_operation_configurations(questionnaire_id,completion_navigation_ref,completion_target,completion_channel_id,lead_qr_title,lead_qr_subtitle,external_push_enabled,external_push_configuration_ref,external_push_metadata,updated_by,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(questionnaire_id) DO UPDATE SET completion_navigation_ref=EXCLUDED.completion_navigation_ref,completion_target=EXCLUDED.completion_target,completion_channel_id=EXCLUDED.completion_channel_id,lead_qr_title=EXCLUDED.lead_qr_title,lead_qr_subtitle=EXCLUDED.lead_qr_subtitle,external_push_enabled=EXCLUDED.external_push_enabled,external_push_configuration_ref=EXCLUDED.external_push_configuration_ref,external_push_metadata=EXCLUDED.external_push_metadata,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,version=survey_operation_configurations.version+1 WHERE survey_operation_configurations.version=$12 RETURNING questionnaire_id,completion_navigation_ref,completion_target,completion_channel_id,lead_qr_title,lead_qr_subtitle,external_push_enabled,external_push_configuration_ref,external_push_metadata,version,updated_at`, value.QuestionnaireID, value.CompletionNavigationRef, value.CompletionTarget, value.CompletionChannelID, value.LeadQRTitle, value.LeadQRSubtitle, value.ExternalPushEnabled, value.ExternalPushConfigurationRef, value.ExternalPushMetadata, actor, now, value.Version).Scan(&stored.QuestionnaireID, &stored.CompletionNavigationRef, &stored.CompletionTarget, &stored.CompletionChannelID, &stored.LeadQRTitle, &stored.LeadQRSubtitle, &stored.ExternalPushEnabled, &stored.ExternalPushConfigurationRef, &stored.ExternalPushMetadata, &stored.Version, &stored.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return surveyport.OperationConfiguration{}, surveyport.ErrConflict
	}
	return stored, mapError(err)
}

func (r *Repository) RecordDisabledOperation(ctx context.Context, qid surveyport.ID, sid *surveyport.ID, kind string, digest [32]byte, now time.Time) (surveyport.OperationReceipt, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.OperationReceipt{}, err
	}
	var item surveyport.OperationReceipt
	var submission *int64
	err = t.QueryRow(ctx, `INSERT INTO survey_external_operation_receipts(questionnaire_id,submission_id,operation_kind,status,failure_category,occurred_at,read_only_legacy,replayable,idempotency_key_digest,created_at,updated_at) VALUES($1,$2,$3,'disabled','provider_disabled',$4,FALSE,TRUE,$5,$4,$4) ON CONFLICT(idempotency_key_digest) DO UPDATE SET updated_at=survey_external_operation_receipts.updated_at RETURNING id,questionnaire_id,submission_id,operation_kind,status,failure_category,occurrence_count,occurred_at,read_only_legacy,replayable`, qid, sid, kind, now, digest[:]).Scan(&item.ID, &item.QuestionnaireID, &submission, &item.OperationKind, &item.Status, &item.FailureCategory, &item.OccurrenceCount, &item.OccurredAt, &item.ReadOnlyLegacy, &item.Replayable)
	if submission != nil {
		value := surveyport.ID(*submission)
		item.SubmissionID = &value
	}
	return item, mapError(err)
}

// RecordCompletionEffect binds the accepted opaque effect to Survey's own
// operation receipt before the caller's Unit of Work commits. A replay may
// observe the existing binding only when it is byte-for-byte the same effect.
func (r *Repository) RecordCompletionEffect(ctx context.Context, qid, sid surveyport.ID, configurationRef, effectID, state string, digest [32]byte, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if effectID == "" || configurationRef == "" || (state != "accepted" && state != "queued") {
		return surveyport.ErrInvalid
	}
	var priorEffectID, priorConfiguration string
	err = t.QueryRow(ctx, `INSERT INTO survey_external_operation_receipts(questionnaire_id,submission_id,operation_kind,configuration_ref,effect_id,status,occurred_at,read_only_legacy,replayable,idempotency_key_digest,created_at,updated_at) VALUES($1,$2,'external_push',$3,$4,$5,$6,FALSE,TRUE,$7,$6,$6) ON CONFLICT(idempotency_key_digest) DO UPDATE SET updated_at=survey_external_operation_receipts.updated_at RETURNING effect_id,configuration_ref`, qid, sid, configurationRef, effectID, state, now, digest[:]).Scan(&priorEffectID, &priorConfiguration)
	if err != nil {
		return mapError(err)
	}
	if priorEffectID != effectID || priorConfiguration != configurationRef {
		return surveyport.ErrConflict
	}
	return nil
}

func (r *Repository) RecordCompletionSnapshot(ctx context.Context, qid, sid surveyport.ID, policy surveyport.CompletionPolicy, evidenceDigest string, identityCiphertext []byte, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if policy.ConfigurationReference == "" || policy.ConfigurationReference != strings.TrimSpace(policy.ConfigurationReference) || len(policy.ConfigurationReference) > 128 || len(evidenceDigest) != 64 {
		return surveyport.ErrInvalid
	}
	configurationDigest, ok := rawCompletionDigest(policy.ConfigurationDigest)
	if !ok {
		sum := sha256.Sum256([]byte("survey.completion.unavailable.v1\x00" + policy.ConfigurationReference))
		configurationDigest = sum[:]
	}
	evidence, err := hex.DecodeString(evidenceDigest)
	if err != nil || len(evidence) != 32 {
		return surveyport.ErrInvalid
	}
	metadata, err := json.Marshal(struct {
		Day          *int64            `json:"day,omitempty"`
		Frequency    *int64            `json:"frequency,omitempty"`
		ExpiresAtTS  *int64            `json:"expires_at_ts,omitempty"`
		PushType     string            `json:"type,omitempty"`
		Remark       string            `json:"remark,omitempty"`
		CustomParams map[string]string `json:"custom_params,omitempty"`
	}{policy.Day, policy.Frequency, policy.ExpiresAtTS, policy.PushType, policy.Remark, policy.CustomParams})
	if err != nil {
		return surveyport.ErrInvalid
	}
	_, err = t.Exec(ctx, `INSERT INTO survey_completion_push_snapshots(submission_id,questionnaire_id,configuration_ref,configuration_version,configuration_digest,identity_kind,identity_scope,identity_evidence_digest,external_identity_ciphertext,metadata,definition_version_id,result_snapshot,created_at) SELECT s.id,s.questionnaire_id,$3,$4,$5,$6,$7,$8,$9,$10,s.definition_version_id,s.result_snapshot,$11 FROM survey_submissions s WHERE s.id=$1 AND s.questionnaire_id=$2 ON CONFLICT(submission_id) DO NOTHING`, sid, qid, policy.ConfigurationReference, policy.ConfigurationVersion, configurationDigest, policy.IdentityKind, policy.IdentityScope, evidence, identityCiphertext, metadata, now)
	return mapError(err)
}

func (r *Repository) GetCompletionTestSnapshot(ctx context.Context, qid surveyport.ID, key string) (surveyapp.CompletionTestSnapshot, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, err
	}
	if qid < 1 || len(key) < 16 || len(key) > 200 {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrInvalid
	}
	keyDigest := sha256.Sum256([]byte(key))
	var stored surveyapp.CompletionTestSnapshot
	var storedConfigurationDigest, storedMetadata, storedSource, storedTarget, storedPayload, storedPolicy []byte
	err = t.QueryRow(ctx, `SELECT questionnaire_id,test_run_id,questionnaire_title,submitted_at,configuration_ref,configuration_version,configuration_digest,metadata,source_digest,target_digest,payload_digest,policy_digest FROM survey_completion_test_push_snapshots WHERE idempotency_key_digest=$1 FOR UPDATE`, keyDigest[:]).Scan(&stored.QuestionnaireID, &stored.TestRunID, &stored.QuestionnaireTitle, &stored.SubmittedAt, &stored.Policy.ConfigurationReference, &stored.Policy.ConfigurationVersion, &storedConfigurationDigest, &storedMetadata, &storedSource, &storedTarget, &storedPayload, &storedPolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return surveyapp.CompletionTestSnapshot{}, false, nil
	}
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, mapError(err)
	}
	if stored.QuestionnaireID != qid {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrConflict
	}
	stored.Policy.ConfigurationDigest = "sha256:" + hex.EncodeToString(storedConfigurationDigest)
	if err = json.Unmarshal(storedMetadata, &stored.Policy); err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrUnavailable
	}
	stored.SourceDigest, stored.TargetDigest = "sha256:"+hex.EncodeToString(storedSource), "sha256:"+hex.EncodeToString(storedTarget)
	stored.PayloadDigest, stored.PolicyDigest = "sha256:"+hex.EncodeToString(storedPayload), "sha256:"+hex.EncodeToString(storedPolicy)
	stored.IdempotencyKey = key
	return stored, true, nil
}

func (r *Repository) RecordCompletionTestSnapshot(ctx context.Context, value surveyapp.CompletionTestSnapshot) (surveyapp.CompletionTestSnapshot, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, err
	}
	if value.QuestionnaireID < 1 || !strings.HasPrefix(value.TestRunID, "questionnaire-test-") || len(value.TestRunID) != len("questionnaire-test-")+32 || value.QuestionnaireTitle == "" || len(value.QuestionnaireTitle) > 512 || !validCompletionReference(value.Policy.ConfigurationReference) || !validCompletionDigest(value.SourceDigest) || !validCompletionDigest(value.TargetDigest) || !validCompletionDigest(value.PayloadDigest) || !validCompletionDigest(value.PolicyDigest) || len(value.IdempotencyKey) < 16 || len(value.IdempotencyKey) > 200 {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrInvalid
	}
	configurationDigest, ok := rawCompletionDigest(value.Policy.ConfigurationDigest)
	if !ok {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrInvalid
	}
	metadata, err := completionPolicyMetadata(value.Policy)
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, err
	}
	source, _ := rawCompletionDigest(value.SourceDigest)
	target, _ := rawCompletionDigest(value.TargetDigest)
	payload, _ := rawCompletionDigest(value.PayloadDigest)
	policy, _ := rawCompletionDigest(value.PolicyDigest)
	keyDigest := sha256.Sum256([]byte(value.IdempotencyKey))
	result, err := t.Exec(ctx, `INSERT INTO survey_completion_test_push_snapshots(questionnaire_id,test_run_id,questionnaire_title,submitted_at,configuration_ref,configuration_version,configuration_digest,metadata,source_digest,target_digest,payload_digest,policy_digest,idempotency_key_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$4) ON CONFLICT(idempotency_key_digest) DO NOTHING`, value.QuestionnaireID, value.TestRunID, value.QuestionnaireTitle, value.SubmittedAt, value.Policy.ConfigurationReference, value.Policy.ConfigurationVersion, configurationDigest, metadata, source, target, payload, policy, keyDigest[:])
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, mapError(err)
	}
	if result.RowsAffected() == 1 {
		return value, true, nil
	}
	var stored surveyapp.CompletionTestSnapshot
	var storedConfigurationDigest, storedMetadata, storedSource, storedTarget, storedPayload, storedPolicy []byte
	err = t.QueryRow(ctx, `SELECT questionnaire_id,test_run_id,questionnaire_title,submitted_at,configuration_ref,configuration_version,configuration_digest,metadata,source_digest,target_digest,payload_digest,policy_digest FROM survey_completion_test_push_snapshots WHERE idempotency_key_digest=$1 FOR UPDATE`, keyDigest[:]).Scan(&stored.QuestionnaireID, &stored.TestRunID, &stored.QuestionnaireTitle, &stored.SubmittedAt, &stored.Policy.ConfigurationReference, &stored.Policy.ConfigurationVersion, &storedConfigurationDigest, &storedMetadata, &storedSource, &storedTarget, &storedPayload, &storedPolicy)
	if err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, mapError(err)
	}
	stored.Policy.ConfigurationDigest = "sha256:" + hex.EncodeToString(storedConfigurationDigest)
	if err = json.Unmarshal(storedMetadata, &stored.Policy); err != nil {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrUnavailable
	}
	stored.SourceDigest, stored.TargetDigest = "sha256:"+hex.EncodeToString(storedSource), "sha256:"+hex.EncodeToString(storedTarget)
	stored.PayloadDigest, stored.PolicyDigest = "sha256:"+hex.EncodeToString(storedPayload), "sha256:"+hex.EncodeToString(storedPolicy)
	stored.IdempotencyKey = value.IdempotencyKey
	if stored.QuestionnaireID != value.QuestionnaireID || stored.TestRunID != value.TestRunID || stored.QuestionnaireTitle != value.QuestionnaireTitle || stored.Policy.ConfigurationReference != value.Policy.ConfigurationReference || stored.Policy.ConfigurationVersion != value.Policy.ConfigurationVersion || stored.Policy.ConfigurationDigest != value.Policy.ConfigurationDigest || stored.SourceDigest != value.SourceDigest || stored.TargetDigest != value.TargetDigest || stored.PayloadDigest != value.PayloadDigest || stored.PolicyDigest != value.PolicyDigest {
		return surveyapp.CompletionTestSnapshot{}, false, surveyport.ErrConflict
	}
	return stored, false, nil
}

func completionPolicyMetadata(policy surveyport.CompletionPolicy) ([]byte, error) {
	metadata, err := json.Marshal(struct {
		Day          *int64            `json:"day,omitempty"`
		Frequency    *int64            `json:"frequency,omitempty"`
		ExpiresAtTS  *int64            `json:"expires_at_ts,omitempty"`
		PushType     string            `json:"type,omitempty"`
		Remark       string            `json:"remark,omitempty"`
		CustomParams map[string]string `json:"custom_params,omitempty"`
	}{policy.Day, policy.Frequency, policy.ExpiresAtTS, policy.PushType, policy.Remark, policy.CustomParams})
	if err != nil {
		return nil, surveyport.ErrInvalid
	}
	return metadata, nil
}

func (r *Repository) RecordCompletionTestEffect(ctx context.Context, qid surveyport.ID, testRunID, configurationRef, effectID, state string, digest [32]byte, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	receiptState, ok := completionTestReceiptStatus(state)
	if qid < 1 || !strings.HasPrefix(testRunID, "questionnaire-test-") || !validCompletionReference(configurationRef) || effectID == "" || !ok {
		return surveyport.ErrInvalid
	}
	var priorEffect, priorRef, priorRun string
	err = t.QueryRow(ctx, `INSERT INTO survey_external_operation_receipts(questionnaire_id,operation_kind,configuration_ref,effect_id,status,occurred_at,read_only_legacy,replayable,idempotency_key_digest,source_system,source_table,source_pk,created_at,updated_at) VALUES($1,'external_push',$2,$3,$4,$5,FALSE,TRUE,$6,'survey','completion_test_push_snapshots',$7,$5,$5) ON CONFLICT(idempotency_key_digest) DO UPDATE SET updated_at=survey_external_operation_receipts.updated_at RETURNING effect_id,configuration_ref,source_pk`, qid, configurationRef, effectID, receiptState, now, digest[:], testRunID).Scan(&priorEffect, &priorRef, &priorRun)
	if err != nil {
		return mapError(err)
	}
	if priorEffect != effectID || priorRef != configurationRef || priorRun != testRunID {
		return surveyport.ErrConflict
	}
	return nil
}

// completionTestReceiptStatus translates External Effects states into the
// older Survey receipt vocabulary. The mapping is only used for a replay's
// prospective INSERT: an existing receipt retains the terminal execution
// facts recorded by CompleteCompletionEffect in the EER completion UoW.
func completionTestReceiptStatus(value string) (string, bool) {
	switch value {
	case "accepted", "queued", "attempted", "executed", "outcome_unknown", "reconciled":
		return value, true
	case "retryable_failed":
		return "attempted", true
	case "final_failed":
		return "failed", true
	case "cancelled":
		return "skipped", true
	}
	return "", false
}

// completionPayloadQuerier permits the outbound provider to reconstruct its
// protected payload after External Effects has committed the attempt. Writes
// still require a Unit of Work; this is the deliberately read-only exception
// that uses the repository pool when no transaction is bound to the context.
type completionPayloadQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (r *Repository) completionPayloadQuerier(ctx context.Context) completionPayloadQuerier {
	if bound, err := tx(ctx); err == nil {
		return bound
	}
	return r.pool
}

func (r *Repository) ReadCompletionPayload(ctx context.Context, sourceDigest string) (surveyport.CompletionPayload, error) {
	t := r.completionPayloadQuerier(ctx)
	digest, ok := completionDigestBytes(sourceDigest)
	if !ok {
		return surveyport.CompletionPayload{}, surveyport.ErrInvalid
	}
	var out surveyport.CompletionPayload
	var customer *int64
	var storedDigest []byte
	var configurationVersion, identityKind, identityScope string
	var configurationDigest, metadata, resultSnapshot, identityCiphertext []byte
	err := t.QueryRow(ctx, `SELECT r.questionnaire_id,r.submission_id,r.configuration_ref,s.customer_id,s.title_snapshot,s.submitted_at,s.payload_digest,p.configuration_version,p.configuration_digest,p.identity_kind,p.identity_scope,p.external_identity_ciphertext,p.metadata,p.result_snapshot FROM survey_external_operation_receipts r JOIN survey_submissions s ON s.id=r.submission_id JOIN survey_completion_push_snapshots p ON p.submission_id=s.id WHERE r.operation_kind='external_push' AND r.idempotency_key_digest=$1`, digest).Scan(&out.QuestionnaireID, &out.SubmissionID, &out.ConfigurationReference, &customer, &out.QuestionnaireTitle, &out.SubmittedAt, &storedDigest, &configurationVersion, &configurationDigest, &identityKind, &identityScope, &identityCiphertext, &metadata, &resultSnapshot)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r.readCompletionTestPayload(ctx, t, sourceDigest)
		}
		return surveyport.CompletionPayload{}, mapError(err)
	}
	if customer == nil || *customer < 1 || len(storedDigest) != 32 {
		return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
	}
	out.CustomerID = *customer
	out.SourceDigest = sourceDigest
	out.TargetDigest = completionDigest("survey.completion.target.v1", out.ConfigurationReference)
	out.PayloadDigest = completionDigest("survey.completion.payload.v1", hex.EncodeToString(storedDigest))
	out.PolicyDigest = completionDigest("survey.completion.policy.v1", "v1")
	out.IdempotencyKey = "survey.completion:" + strconv.FormatInt(int64(out.SubmissionID), 10)
	if len(configurationDigest) != 32 || !json.Valid(metadata) || !json.Valid(resultSnapshot) {
		return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
	}
	out.AssessmentResult = append(json.RawMessage(nil), resultSnapshot...)
	out.Policy.ConfigurationReference, out.Policy.ConfigurationVersion = out.ConfigurationReference, configurationVersion
	out.Policy.ConfigurationDigest = "sha256:" + hex.EncodeToString(configurationDigest)
	out.Policy.IdentityKind, out.Policy.IdentityScope = identitydomain.Kind(identityKind), identityScope
	if err = json.Unmarshal(metadata, &out.Policy); err != nil {
		return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
	}
	if len(identityCiphertext) > 0 {
		out.ExternalUserID, err = r.cipher.Decrypt(identityCiphertext)
		if err != nil || out.ExternalUserID == "" {
			return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
		}
	}
	rows, err := t.Query(ctx, `SELECT question_type,question_title_snapshot,selected_options_snapshot,text_value_ciphertext FROM survey_submission_answers WHERE submission_id=$1 ORDER BY question_sort_order,id`, out.SubmissionID)
	if err != nil {
		return surveyport.CompletionPayload{}, mapError(err)
	}
	defer rows.Close()
	out.Answers = []surveyport.CompletionAnswer{}
	for rows.Next() {
		var answer surveyport.CompletionAnswer
		var selected, encrypted []byte
		if err = rows.Scan(&answer.QuestionType, &answer.QuestionTitle, &selected, &encrypted); err != nil {
			return surveyport.CompletionPayload{}, mapError(err)
		}
		var options []surveyport.SelectedOptionSnapshot
		if json.Unmarshal(selected, &options) != nil {
			return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
		}
		answer.OptionTexts = make([]string, 0, len(options))
		for _, option := range options {
			answer.OptionTexts = append(answer.OptionTexts, option.OptionText)
		}
		if len(encrypted) > 0 {
			answer.TextValue, err = r.cipher.Decrypt(encrypted)
			if err != nil {
				return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
			}
		}
		out.Answers = append(out.Answers, answer)
	}
	if err = rows.Err(); err != nil {
		return surveyport.CompletionPayload{}, mapError(err)
	}
	return out, nil
}

func (r *Repository) readCompletionTestPayload(ctx context.Context, t completionPayloadQuerier, sourceDigest string) (surveyport.CompletionPayload, error) {
	source, ok := rawCompletionDigest(sourceDigest)
	if !ok {
		return surveyport.CompletionPayload{}, surveyport.ErrInvalid
	}
	var out surveyport.CompletionPayload
	var configurationDigest, metadata, target, payload, policy []byte
	err := t.QueryRow(ctx, `SELECT questionnaire_id,test_run_id,questionnaire_title,submitted_at,configuration_ref,configuration_version,configuration_digest,metadata,target_digest,payload_digest,policy_digest FROM survey_completion_test_push_snapshots WHERE source_digest=$1`, source).Scan(&out.QuestionnaireID, &out.TestRunID, &out.QuestionnaireTitle, &out.SubmittedAt, &out.ConfigurationReference, &out.Policy.ConfigurationVersion, &configurationDigest, &metadata, &target, &payload, &policy)
	if err != nil {
		return surveyport.CompletionPayload{}, mapError(err)
	}
	if len(configurationDigest) != 32 || len(target) != 32 || len(payload) != 32 || len(policy) != 32 || !json.Valid(metadata) {
		return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
	}
	out.SyntheticTest, out.ExternalUserID, out.SourceDigest = true, "questionnaire_test", sourceDigest
	out.TargetDigest, out.PayloadDigest, out.PolicyDigest = "sha256:"+hex.EncodeToString(target), "sha256:"+hex.EncodeToString(payload), "sha256:"+hex.EncodeToString(policy)
	out.IdempotencyKey = "survey.completion.test:" + out.TestRunID
	out.Answers, out.AssessmentResult = []surveyport.CompletionAnswer{}, json.RawMessage(`{}`)
	out.Policy.ConfigurationReference, out.Policy.ConfigurationDigest = out.ConfigurationReference, "sha256:"+hex.EncodeToString(configurationDigest)
	if err = json.Unmarshal(metadata, &out.Policy); err != nil {
		return surveyport.CompletionPayload{}, surveyport.ErrUnavailable
	}
	return out, nil
}

func (r *Repository) CompleteCompletionEffect(ctx context.Context, effectID, state string, callAttempted, realCall bool, resultReceived *bool, receiptDigest string, attempt int32, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if effectID == "" || attempt < 1 || !validCompletionState(state) || !validCompletionDigest(receiptDigest) {
		return surveyport.ErrInvalid
	}
	status, category := completionOperationStatus(state, realCall)
	result, err := t.Exec(ctx, `UPDATE survey_external_operation_receipts SET status=$2,failure_category=$3,occurred_at=$4,updated_at=$4,provider_call_attempted=$5,provider_real_call_executed=$6,provider_result_received=$7,provider_attempt_number=$8 WHERE effect_id=$1 AND operation_kind='external_push' AND read_only_legacy=FALSE`, effectID, status, category, now, callAttempted, realCall, resultReceived, attempt)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return surveyport.ErrNotFound
	}
	return nil
}

func completionDigestBytes(value string) ([]byte, bool) {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return nil, false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	if err != nil || len(decoded) != 32 {
		return nil, false
	}
	// The effect idempotency key stores a digest of the opaque source digest,
	// just as all other survey operation receipts do. Keep the raw digest out
	// of the external-effects record while retaining a deterministic lookup.
	sum := sha256.Sum256([]byte(value))
	return sum[:], true
}

func rawCompletionDigest(value string) ([]byte, bool) {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return nil, false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return decoded, err == nil && len(decoded) == 32
}

func completionDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validCompletionDigest(value string) bool {
	_, ok := completionDigestBytes(value)
	return ok
}

func validCompletionReference(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}

func validCompletionState(state string) bool {
	switch state {
	case "executed", "outcome_unknown", "retryable_failed", "final_failed", "reconciled":
		return true
	default:
		return false
	}
}

func completionOperationStatus(state string, realCall bool) (string, string) {
	switch state {
	case "executed":
		if realCall {
			return "executed", ""
		}
		return "failed", "provider_execution_unproven"
	case "outcome_unknown":
		return "outcome_unknown", "provider_outcome_unknown"
	case "retryable_failed":
		return "attempted", "retryable_provider_failure"
	case "reconciled":
		return "reconciled", ""
	default:
		return "failed", "provider_final_failure"
	}
}

func (r *Repository) CreateOAuthState(ctx context.Context, digest [32]byte, state surveyapp.OAuthState, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, `INSERT INTO survey_oauth_states(state_digest,questionnaire_slug,redirect_path,expires_at,created_at) VALUES($1,$2,$3,$4,$5)`, digest[:], state.Slug, state.Redirect, state.ExpiresAt, now)
	return mapError(err)
}
func (r *Repository) ConsumeOAuthState(ctx context.Context, digest [32]byte, now time.Time) (surveyapp.OAuthState, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.OAuthState{}, err
	}
	var state surveyapp.OAuthState
	err = t.QueryRow(ctx, `UPDATE survey_oauth_states SET consumed_at=$2 WHERE state_digest=$1 AND consumed_at IS NULL AND expires_at>$2 RETURNING questionnaire_slug,redirect_path,expires_at`, digest[:], now).Scan(&state.Slug, &state.Redirect, &state.ExpiresAt)
	return state, mapError(err)
}
func (r *Repository) CreateIdentitySession(ctx context.Context, digest [32]byte, identity surveyport.SubmissionIdentity, expires, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, `INSERT INTO survey_identity_sessions(session_digest,customer_id,identity_state,evidence_digest,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)`, digest[:], identity.CustomerID, identity.State, decodeEvidence(identity.EvidenceDigest), expires, now)
	return mapError(err)
}
func (r *Repository) ReadIdentitySession(ctx context.Context, digest [32]byte, now time.Time) (surveyport.SubmissionIdentity, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.SubmissionIdentity{}, err
	}
	var result surveyport.SubmissionIdentity
	var customer *int64
	var evidence []byte
	err = t.QueryRow(ctx, `SELECT customer_id,identity_state,evidence_digest FROM survey_identity_sessions WHERE session_digest=$1 AND revoked_at IS NULL AND expires_at>$2`, digest[:], now).Scan(&customer, &result.State, &evidence)
	if err != nil {
		return result, mapError(err)
	}
	if customer != nil {
		id := customerdomain.CustomerID(*customer)
		result.CustomerID = &id
	}
	result.EvidenceDigest = hex.EncodeToString(evidence)
	return result, nil
}

func (r *Repository) String() string { return fmt.Sprintf("survey.Repository(%p)", r) }

var _ surveyapp.Store = (*Repository)(nil)
var _ surveyapp.SubmissionStore = (*Repository)(nil)
var _ surveyapp.OAuthStore = (*Repository)(nil)
var _ surveyport.CompletionPayloadReader = (*Repository)(nil)
var _ surveyport.CompletionEffectProjector = (*Repository)(nil)
