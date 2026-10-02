package store

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func (r *Repository) ListCollectedPhoneAnswersWithin(ctx context.Context, after, through int64) ([]int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT id FROM survey_submission_answers WHERE question_type='mobile' AND text_value_ciphertext IS NOT NULL AND octet_length(text_value_ciphertext)>0 AND id>$1 AND ($2::bigint=0 OR id<=$2) ORDER BY id LIMIT 100`, after, through)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	return ids, mapError(rows.Err())
}

// Only the internal replay application receives plaintext, inside the source
// owner's UoW. HTTP list/detail/export projections keep their masked contract.
func (r *Repository) ReadCollectedPhoneAnswerWithin(ctx context.Context, id int64) (surveyapp.CollectedPhoneAnswer, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.CollectedPhoneAnswer{}, err
	}
	var a surveyapp.CollectedPhoneAnswer
	var customer *int64
	var encrypted []byte
	err = t.QueryRow(ctx, `SELECT a.id,a.submission_id,s.customer_id,a.text_value_ciphertext,s.submitted_at FROM survey_submission_answers a JOIN survey_submissions s ON s.id=a.submission_id WHERE a.id=$1 AND a.question_type='mobile'`, id).Scan(&a.AnswerID, &a.SubmissionID, &customer, &encrypted, &a.CollectedAt)
	if err != nil {
		return a, mapError(err)
	}
	if customer != nil {
		v := customerdomain.CustomerID(*customer)
		a.CustomerID = &v
	}
	a.Phone, err = r.cipher.Decrypt(encrypted)
	if err != nil {
		return a, surveyport.ErrUnavailable
	}
	return a, nil
}
