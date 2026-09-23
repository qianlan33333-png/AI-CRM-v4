package store

import (
	"context"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"time"
)

func (r *Repository) AudienceSubmissions(ctx context.Context, at time.Time) ([]surveyport.AudienceSubmission, error) {
	if at.IsZero() {
		return nil, surveyport.ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,questionnaire_id,id,staff_id,submitted_at FROM survey_submissions WHERE identity_state='resolved' AND customer_id IS NOT NULL AND submitted_at <= $1 ORDER BY questionnaire_id,customer_id,submitted_at,id`, at.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []surveyport.AudienceSubmission{}
	for rows.Next() {
		var f surveyport.AudienceSubmission
		if err = rows.Scan(&f.CustomerID, &f.QuestionnaireID, &f.SubmissionID, &f.StaffID, &f.SubmittedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

var _ surveyport.AudienceSubmissionReader = (*Repository)(nil)
