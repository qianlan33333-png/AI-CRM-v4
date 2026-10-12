package store

import (
	"context"
	"fmt"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func (r *Repository) BindSubmissionAttachment(ctx context.Context, sid, qid surveyport.ID, value surveyport.SubmissionAttachment) error {
	tx, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO survey_submission_attachments(answer_id,private_ref,file_name,mime_type,byte_size) SELECT id,$3,$4,$5,$6 FROM survey_submission_answers WHERE submission_id=$1 AND definition_question_id=$2 AND question_type='attachment'`, sid, qid, value.PrivateRef, value.FileName, value.MediaType, value.SizeBytes)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() != 1 {
		return surveyport.ErrInvalid
	}
	return nil
}
func (r *Repository) ReadSubmissionAttachment(ctx context.Context, qid, sid, aid surveyport.ID) (surveyport.SubmissionAttachment, error) {
	var value surveyport.SubmissionAttachment
	tx, err := tx(ctx)
	if err != nil {
		return value, err
	}
	err = tx.QueryRow(ctx, `SELECT att.private_ref,att.file_name,att.mime_type,att.byte_size FROM survey_submission_attachments att JOIN survey_submission_answers a ON a.id=att.answer_id JOIN survey_submissions s ON s.id=a.submission_id WHERE s.questionnaire_id=$1 AND s.id=$2 AND a.id=$3`, qid, sid, aid).Scan(&value.PrivateRef, &value.FileName, &value.MediaType, &value.SizeBytes)
	value.ViewURL = fmt.Sprintf("/api/admin/questionnaires/%d/submissions/%d/answers/%d/attachment", qid, sid, aid)
	return value, mapError(err)
}
