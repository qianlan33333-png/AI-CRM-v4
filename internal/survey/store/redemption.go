package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func (r *Repository) redemptionDigest(id surveyport.ID, code string) ([32]byte, error) {
	token, e := r.cipher.Token("survey-redemption-code-v1", fmt.Sprint(id), strings.TrimSpace(code))
	if e != nil {
		return [32]byte{}, surveyport.ErrUnavailable
	}
	return sha256.Sum256([]byte(token)), nil
}
func (r *Repository) InsertRedemptionCode(ctx context.Context, id surveyport.ID, code string, actor int64, now time.Time) (bool, error) {
	t, e := tx(ctx)
	if e != nil {
		return false, e
	}
	digest, e := r.redemptionDigest(id, code)
	if e != nil {
		return false, e
	}
	encrypted, e := r.cipher.Encrypt(code)
	if e != nil {
		return false, surveyport.ErrUnavailable
	}
	tag, e := t.Exec(ctx, `INSERT INTO survey_redemption_codes(questionnaire_id,code_digest,code_ciphertext,created_by,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(questionnaire_id,code_digest) DO NOTHING`, id, digest[:], encrypted, actor, now)
	return tag.RowsAffected() == 1, mapError(e)
}
func (r *Repository) ListRedemptionCodes(ctx context.Context, id surveyport.ID, limit, offset int32) (surveyport.RedemptionPage, error) {
	result := surveyport.RedemptionPage{Items: []surveyport.RedemptionCode{}, Limit: limit, Offset: offset}
	t, e := tx(ctx)
	if e != nil {
		return result, e
	}
	e = t.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE used_submission_id IS NULL),count(*) FILTER(WHERE used_submission_id IS NOT NULL) FROM survey_redemption_codes WHERE questionnaire_id=$1`, id).Scan(&result.Total, &result.Available, &result.Used)
	if e != nil {
		return result, mapError(e)
	}
	rows, e := t.Query(ctx, `SELECT c.id,c.code_ciphertext,c.used_submission_id,s.submitted_at FROM survey_redemption_codes c LEFT JOIN survey_submissions s ON s.id=c.used_submission_id WHERE c.questionnaire_id=$1 ORDER BY c.id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if e != nil {
		return result, mapError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var item surveyport.RedemptionCode
		var encrypted []byte
		if e = rows.Scan(&item.ID, &encrypted, &item.UsedSubmissionID, &item.UsedAt); e != nil {
			return result, mapError(e)
		}
		item.Code, e = r.cipher.Decrypt(encrypted)
		if e != nil {
			return result, surveyport.ErrUnavailable
		}
		result.Items = append(result.Items, item)
	}
	return result, mapError(rows.Err())
}
func (r *Repository) ConsumeRedemptionCode(ctx context.Context, qid, questionID surveyport.ID, code string, submissionID surveyport.ID) error {
	if strings.TrimSpace(code) == "" {
		return surveyport.CodeRequired(questionID)
	}
	t, e := tx(ctx)
	if e != nil {
		return e
	}
	digest, e := r.redemptionDigest(qid, code)
	if e != nil {
		return e
	}
	var id surveyport.ID
	var used *surveyport.ID
	e = t.QueryRow(ctx, `SELECT id,used_submission_id FROM survey_redemption_codes WHERE questionnaire_id=$1 AND code_digest=$2 FOR UPDATE`, qid, digest[:]).Scan(&id, &used)
	if errors.Is(e, pgx.ErrNoRows) {
		return surveyport.CodeInvalid(questionID)
	}
	if e != nil {
		return mapError(e)
	}
	if used != nil {
		return surveyport.CodeUsed(questionID)
	}
	tag, e := t.Exec(ctx, `UPDATE survey_redemption_codes SET used_submission_id=$2 WHERE id=$1 AND used_submission_id IS NULL`, id, submissionID)
	if e != nil {
		return mapError(e)
	}
	if tag.RowsAffected() != 1 {
		return surveyport.CodeUsed(questionID)
	}
	return nil
}
