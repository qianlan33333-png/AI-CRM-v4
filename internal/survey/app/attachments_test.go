package app

import (
	"context"
	"errors"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"io"
	"testing"
)

func TestAttachmentPreparationStopsWhenRequestCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	service := &SubmissionService{}
	_, err := service.prepareAttachments(ctx, []surveyport.AttachmentUpload{{QuestionID: 7, Open: func() (io.ReadCloser, error) {
		opened = true
		return nil, errors.New("should not open after cancellation")
	}}})
	var field *surveyport.FieldError
	if opened || !errors.As(err, &field) || field.Code != "attachment_upload_failed" || field.QuestionID != 7 {
		t.Fatalf("canceled upload opened=%t error=%v", opened, err)
	}
}
