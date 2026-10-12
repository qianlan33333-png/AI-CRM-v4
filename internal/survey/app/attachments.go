package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sort"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type attachmentStore interface {
	BindSubmissionAttachment(context.Context, surveyport.ID, surveyport.ID, surveyport.SubmissionAttachment) error
	ReadSubmissionAttachment(context.Context, surveyport.ID, surveyport.ID, surveyport.ID) (surveyport.SubmissionAttachment, error)
}
type preparedAttachment struct {
	QuestionID surveyport.ID              `json:"question_id"`
	Info       mediaport.PrivateImageInfo `json:"image"`
	issue      error
	upload     surveyport.AttachmentUpload
}

func (s *SubmissionService) BindPrivateImages(images mediaport.PrivateImageStorage) error {
	if s == nil || images == nil {
		return surveyport.ErrInvalid
	}
	s.images = images
	return nil
}
func attachmentError(id surveyport.ID, code, message string) error {
	return &surveyport.FieldError{Code: code, Message: message, QuestionID: id}
}
func readAttachment(upload surveyport.AttachmentUpload) ([]byte, error) {
	var file io.ReadCloser
	var err error
	if upload.Open != nil {
		file, err = upload.Open()
	} else {
		file = io.NopCloser(bytes.NewReader(upload.Content))
	}
	if err != nil {
		return nil, attachmentError(upload.QuestionID, "attachment_upload_failed", "图片读取失败，请重新选择后重试")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, surveyport.MaximumAttachmentBytes))
	if err != nil {
		return nil, attachmentError(upload.QuestionID, "attachment_upload_failed", "图片读取失败，请重新选择后重试")
	}
	if len(data) >= surveyport.MaximumAttachmentBytes {
		return nil, attachmentError(upload.QuestionID, "attachment_too_large", "图片必须小于 2MB")
	}
	return data, nil
}
func (s *SubmissionService) prepareAttachments(ctx context.Context, uploads []surveyport.AttachmentUpload) ([]preparedAttachment, error) {
	if len(uploads) > surveydomain.MaximumQuestions {
		return nil, surveyport.ErrInvalid
	}
	prepared := make([]preparedAttachment, 0, len(uploads))
	seen := map[surveyport.ID]bool{}
	for _, upload := range uploads {
		if ctx.Err() != nil {
			return nil, attachmentError(upload.QuestionID, "attachment_upload_failed", "图片检查已中断，请重试")
		}
		if upload.QuestionID < 1 || seen[upload.QuestionID] {
			return nil, surveyport.ErrInvalid
		}
		seen[upload.QuestionID] = true
		if s.images == nil {
			return nil, surveyport.ErrUnavailable
		}
		content, err := readAttachment(upload)
		if err != nil {
			return nil, err
		}
		info, err := s.images.InspectPrivateImage(mediaport.PrivateImageUpload{FileName: upload.FileName, MediaType: upload.MediaType, Content: content})
		var issue error
		if err != nil {
			// Inspection still finishes before UoW. Its safe rejection is
			// applied after receipt comparison so changed same-key content
			// cannot hide behind a format error or a later definition update.
			info = mediaport.PrivateImageInfo{FileName: upload.FileName, MediaType: upload.MediaType, SizeBytes: int64(len(content)), Digest: sha256.Sum256(content)}
			issue = attachmentError(upload.QuestionID, "attachment_format_invalid", "请上传有效的 JPG、PNG 或 GIF 图片")
		}
		if ctx.Err() != nil {
			return nil, attachmentError(upload.QuestionID, "attachment_upload_failed", "图片检查已中断，请重试")
		}
		prepared = append(prepared, preparedAttachment{QuestionID: upload.QuestionID, Info: info, upload: upload, issue: issue})
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].QuestionID < prepared[j].QuestionID })
	return prepared, nil
}
func validateAttachments(q surveyport.Questionnaire, answers []surveyport.SubmissionAnswer, prepared []preparedAttachment) error {
	questions := map[surveyport.ID]surveyport.Question{}
	files := map[surveyport.ID]bool{}
	values := map[surveyport.ID]surveyport.SubmissionAnswer{}
	for _, question := range q.Questions {
		questions[question.ID] = question
	}
	for _, answer := range answers {
		values[answer.QuestionID] = answer
	}
	for _, file := range prepared {
		question, ok := questions[file.QuestionID]
		if !ok || question.Type != surveyport.QuestionAttachment || !values[file.QuestionID].Attachment {
			return surveyport.ErrInvalid
		}
		if file.issue != nil {
			return file.issue
		}
		files[file.QuestionID] = true
	}
	for _, question := range q.Questions {
		if question.Type != surveyport.QuestionAttachment {
			continue
		}
		if question.Required && !files[question.ID] {
			return attachmentError(question.ID, "attachment_required", "请上传图片")
		}
		if values[question.ID].Attachment != files[question.ID] {
			return attachmentError(question.ID, "attachment_required", "请重新选择图片")
		}
	}
	return nil
}
func (s *SubmissionService) persistAttachments(ctx context.Context, submission surveyport.Submission, prepared []preparedAttachment) error {
	if len(prepared) == 0 {
		return nil
	}
	store, ok := s.store.(attachmentStore)
	if !ok || s.images == nil {
		return surveyport.ErrUnavailable
	}
	for _, file := range prepared {
		content, err := readAttachment(file.upload)
		if err != nil {
			return err
		}
		info, err := s.images.StorePrivateImageWithin(ctx, mediaport.PrivateImageUpload{FileName: file.Info.FileName, MediaType: file.Info.MediaType, Content: content}, file.Info)
		if err != nil {
			return err
		}
		if err = store.BindSubmissionAttachment(ctx, submission.ID, file.QuestionID, surveyport.SubmissionAttachment{PrivateRef: info.Ref, FileName: info.FileName, MediaType: info.MediaType, SizeBytes: info.SizeBytes}); err != nil {
			return err
		}
	}
	return nil
}
func (s *SubmissionService) ReadSubmissionAttachment(ctx context.Context, qid, sid, aid surveyport.ID) (surveyport.AttachmentContent, error) {
	var result surveyport.AttachmentContent
	store, ok := s.store.(attachmentStore)
	if !ok || s.images == nil {
		return result, surveyport.ErrUnavailable
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		association, err := store.ReadSubmissionAttachment(tx, qid, sid, aid)
		if err != nil {
			return err
		}
		image, err := s.images.ReadPrivateImageWithin(tx, association.PrivateRef)
		if errors.Is(err, mediaport.ErrPrivateImageNotFound) {
			return surveyport.ErrNotFound
		}
		if err != nil {
			return err
		}
		if association.FileName != image.Info.FileName || association.MediaType != image.Info.MediaType || association.SizeBytes != image.Info.SizeBytes {
			return surveyport.ErrUnavailable
		}
		result = surveyport.AttachmentContent{FileName: image.Info.FileName, MediaType: image.Info.MediaType, Content: image.Content}
		return nil
	})
	return result, classify(err)
}
