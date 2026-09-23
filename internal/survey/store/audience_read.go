package store

import (
	"context"
	"encoding/json"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

// FirstCompleteAudienceChoices selects one resolved customer's first complete
// submission per questionnaire. Free text is never exposed to Segment.
func (r *Repository) FirstCompleteAudienceChoices(ctx context.Context, reference time.Time) ([]surveyport.AudienceChoiceAnswer, error) {
	if reference.IsZero() {
		return nil, surveyport.ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH ranked AS (SELECT id,questionnaire_id,customer_id,staff_id,submitted_at,row_number() OVER (PARTITION BY questionnaire_id,customer_id ORDER BY submitted_at,id) AS n FROM survey_submissions WHERE identity_state='resolved' AND customer_id IS NOT NULL AND submitted_at <= $1)
		SELECT s.customer_id,s.questionnaire_id,s.id,s.staff_id,s.submitted_at,a.definition_question_id,a.selected_options_snapshot
		FROM ranked s JOIN survey_submission_answers a ON a.submission_id=s.id
		WHERE s.n=1 AND a.definition_question_id IS NOT NULL AND a.question_type IN ('single_choice','multi_choice') ORDER BY s.questionnaire_id,s.customer_id,a.id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []surveyport.AudienceChoiceAnswer{}
	for rows.Next() {
		var item surveyport.AudienceChoiceAnswer
		var options []struct {
			OptionID surveyport.ID `json:"option_id"`
		}
		if err = rows.Scan(&item.CustomerID, &item.QuestionnaireID, &item.SubmissionID, &item.StaffID, &item.SubmittedAt, &item.QuestionID, &options); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(options)
		if json.Unmarshal(raw, &options) != nil {
			return nil, surveyport.ErrUnavailable
		}
		for _, option := range options {
			item.OptionIDs = append(item.OptionIDs, option.OptionID)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ surveyport.AudienceChoiceAnswerReader = (*Repository)(nil)
