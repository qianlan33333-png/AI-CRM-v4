package port

import (
	"context"
	"io"
	"time"
)

const MaximumAttachmentBytes = 2 << 20

type AttachmentUpload struct {
	QuestionID          ID
	FileName, MediaType string
	Content             []byte
	// Open is a request-scoped multipart file, never a persisted upload ticket.
	Open func() (io.ReadCloser, error)
}
type SubmissionAttachment struct {
	FileName   string `json:"file_name"`
	MediaType  string `json:"media_type"`
	SizeBytes  int64  `json:"size_bytes"`
	ViewURL    string `json:"view_url,omitempty"`
	PrivateRef string `json:"-"`
}
type AttachmentContent struct {
	FileName, MediaType string
	Content             []byte
}
type SubmissionAttachmentReader interface {
	ReadSubmissionAttachment(context.Context, ID, ID, ID) (AttachmentContent, error)
}

// FieldError carries safe feedback, never rejected code or file contents.
type FieldError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	QuestionID ID     `json:"question_id,omitempty"`
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error {
	if e.Code == "submission_payload_conflict" {
		return ErrConflict
	}
	return ErrInvalid
}
func CodeRequired(id ID) error {
	return &FieldError{"redemption_code_required", "请输入兑换码", id}
}
func CodeInvalid(id ID) error {
	return &FieldError{"redemption_code_invalid", "兑换码无效，请检查后重试", id}
}
func CodeUsed(id ID) error {
	return &FieldError{"redemption_code_used", "该兑换码已使用，请更换兑换码", id}
}

type RedemptionCode struct {
	ID               ID         `json:"id"`
	Code             string     `json:"code"`
	UsedSubmissionID *ID        `json:"used_submission_id,omitempty"`
	UsedAt           *time.Time `json:"used_at,omitempty"`
}
type RedemptionPage struct {
	Items     []RedemptionCode `json:"items"`
	Total     int64            `json:"total"`
	Available int64            `json:"available"`
	Used      int64            `json:"used"`
	Limit     int32            `json:"limit"`
	Offset    int32            `json:"offset"`
}
type RedemptionImportResult struct {
	Imported   int `json:"imported"`
	Duplicates int `json:"duplicates"`
	EmptyRows  int `json:"empty_rows"`
}
type RedemptionAdministration interface {
	ListRedemptionCodes(context.Context, ID, int32, int32) (RedemptionPage, error)
	ImportRedemptionCodes(context.Context, ID, int64, string, []byte) (RedemptionImportResult, error)
}
