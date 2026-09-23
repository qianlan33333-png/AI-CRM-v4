package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// SurveyCompletionEndpoints lets an administrator edit only the destination.
// Signing material and identity disclosure policy remain runtime-owned.
type SurveyCompletionEndpoints struct {
	pool      *pgxpool.Pool
	runtime   SurveyCompletionTargetResolver
	templates []string
}

func NewSurveyCompletionEndpoints(pool *pgxpool.Pool, runtime SurveyCompletionTargetResolver, templates []string) *SurveyCompletionEndpoints {
	return &SurveyCompletionEndpoints{pool: pool, runtime: runtime, templates: append([]string(nil), templates...)}
}
func editableSurveyEndpoint(raw string) bool {
	return validSurveyCompletionEndpoint(raw)
}

func (s *SurveyCompletionEndpoints) ReadSurveyCompletionEndpointWithin(ctx context.Context, id surveyport.ID, ref string) (string, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	if id < 1 {
		return "", surveyport.ErrInvalid
	}
	var endpoint, storedRef string
	err = tx.QueryRow(ctx, `SELECT endpoint,configuration_reference FROM outbound_survey_completion_endpoints WHERE questionnaire_id=$1`, id).Scan(&endpoint, &storedRef)
	if err == nil {
		if ref != "" && ref != storedRef {
			return "", surveyport.ErrInvalid
		}
		return endpoint, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if ref == "" {
		return "", nil
	}
	target, found, err := s.runtime.SurveyCompletionTarget(ctx, ref)
	if err != nil {
		return "", err
	}
	if !found {
		return "", surveyport.ErrInvalid
	}
	return target.Endpoint, nil
}

func (s *SurveyCompletionEndpoints) SaveSurveyCompletionEndpointWithin(ctx context.Context, id surveyport.ID, ref, endpoint string, metadata json.RawMessage) (string, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	if id < 1 || endpoint != "" && !editableSurveyEndpoint(endpoint) || !validSurveyEndpointMetadata(metadata) {
		return "", surveyport.ErrInvalid
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("survey-completion-endpoint:%d", id)); err != nil {
		return "", err
	}
	var template, storedRef string
	err = tx.QueryRow(ctx, `SELECT template_reference,configuration_reference FROM outbound_survey_completion_endpoints WHERE questionnaire_id=$1 FOR UPDATE`, id).Scan(&template, &storedRef)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if storedRef != "" {
		if ref != "" && ref != storedRef {
			return "", surveyport.ErrInvalid
		}
	} else {
		template = ref
		if template == "" {
			template, err = s.equivalentDefaultTemplate(ctx)
			if err != nil {
				return "", err
			}
		}
	}
	if endpoint == "" {
		_, err = tx.Exec(ctx, `DELETE FROM outbound_survey_completion_endpoints WHERE questionnaire_id=$1`, id)
		return "", err
	}
	if _, found, resolveErr := s.runtime.SurveyCompletionTarget(ctx, template); resolveErr != nil || !found {
		if resolveErr != nil {
			return "", resolveErr
		}
		return "", surveyport.ErrInvalid
	}
	derived := fmt.Sprintf("survey-endpoint:%d", id)
	_, err = tx.Exec(ctx, `INSERT INTO outbound_survey_completion_endpoints(questionnaire_id,configuration_reference,template_reference,endpoint,configuration_metadata) VALUES($1,$2,$3,$4,$5) ON CONFLICT(questionnaire_id) DO UPDATE SET endpoint=excluded.endpoint,configuration_metadata=excluded.configuration_metadata,revision=outbound_survey_completion_endpoints.revision+1,updated_at=now()`, id, derived, template, endpoint, metadata)
	return derived, err
}

func (s *SurveyCompletionEndpoints) SurveyCompletionTarget(ctx context.Context, ref string) (SurveyCompletionTarget, bool, error) {
	if !strings.HasPrefix(ref, "survey-endpoint:") {
		return s.runtime.SurveyCompletionTarget(ctx, ref)
	}
	var endpoint, template string
	var metadata json.RawMessage
	var row pgx.Row
	if tx, err := platformpostgres.RequireTransaction(ctx); err == nil {
		row = tx.QueryRow(ctx, `SELECT endpoint,template_reference,configuration_metadata FROM outbound_survey_completion_endpoints WHERE configuration_reference=$1`, ref)
	} else {
		row = s.pool.QueryRow(ctx, `SELECT endpoint,template_reference,configuration_metadata FROM outbound_survey_completion_endpoints WHERE configuration_reference=$1`, ref)
	}
	err := row.Scan(&endpoint, &template, &metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return SurveyCompletionTarget{}, false, nil
	}
	if err != nil {
		return SurveyCompletionTarget{}, false, err
	}
	target, found, err := s.runtime.SurveyCompletionTarget(ctx, template)
	if err != nil || !found {
		return SurveyCompletionTarget{}, found, err
	}
	if !editableSurveyEndpoint(endpoint) {
		return SurveyCompletionTarget{}, false, surveyport.ErrInvalid
	}
	target.Reference, target.Endpoint = ref, endpoint
	target.SigningKey = append([]byte(nil), target.SigningKey...)
	applySurveyEndpointMetadata(&target, metadata)
	return target, true, nil
}

type surveyEndpointMetadata struct {
	PushType    string            `json:"type"`
	ExpiresAtTS *int64            `json:"expires_at_ts"`
	Day         *int64            `json:"day"`
	Frequency   *int64            `json:"frequency"`
	Remark      string            `json:"remark"`
	Custom      map[string]string `json:"custom_params"`
}

func validSurveyEndpointMetadata(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value surveyEndpointMetadata
	if json.Unmarshal(raw, &value) != nil || len(value.PushType) > 100 || len(value.Remark) > 1000 || len(value.Custom) > 100 {
		return false
	}
	for key, item := range value.Custom {
		if strings.TrimSpace(key) == "" || len(key) > 128 || len(item) > 4096 {
			return false
		}
	}
	return nonNegative(value.ExpiresAtTS) && nonNegative(value.Day) && nonNegative(value.Frequency)
}
func nonNegative(value *int64) bool { return value == nil || *value >= 0 }
func applySurveyEndpointMetadata(target *SurveyCompletionTarget, raw json.RawMessage) {
	var value surveyEndpointMetadata
	if target == nil || json.Unmarshal(raw, &value) != nil {
		return
	}
	target.PushType, target.ExpiresAtTS, target.Day, target.Frequency, target.Remark = value.PushType, value.ExpiresAtTS, value.Day, value.Frequency, value.Remark
	target.CustomParams = make(map[string]string, len(value.Custom))
	for key, item := range value.Custom {
		target.CustomParams[key] = item
	}
}
func (s *SurveyCompletionEndpoints) SurveyCompletionTargetReferences(ctx context.Context) ([]string, error) {
	return s.runtime.SurveyCompletionTargetReferences(ctx)
}
func (s *SurveyCompletionEndpoints) equivalentDefaultTemplate(ctx context.Context) (string, error) {
	refs := append([]string(nil), s.templates...)
	sort.Strings(refs)
	if len(refs) == 0 {
		return "", surveyport.ErrInvalid
	}
	var baseline SurveyCompletionTarget
	for index, ref := range refs {
		target, found, err := s.runtime.SurveyCompletionTarget(ctx, ref)
		if err != nil || !found {
			if err != nil {
				return "", err
			}
			return "", surveyport.ErrInvalid
		}
		target.Reference, target.Endpoint = "", ""
		if index == 0 {
			baseline = target
			continue
		}
		if !reflect.DeepEqual(baseline, target) {
			return "", surveyport.ErrInvalid
		}
	}
	return refs[0], nil
}

var _ SurveyCompletionTargetResolver = (*SurveyCompletionEndpoints)(nil)
var _ surveyport.CompletionEndpointManager = (*SurveyCompletionEndpoints)(nil)
