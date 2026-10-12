package http

import (
	"context"
	"encoding/json"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type publicSubmissionBody struct {
	Version       int64                         `json:"version"`
	SubmissionKey string                        `json:"submission_key"`
	Answers       []surveyport.SubmissionAnswer `json:"answers"`
	SourceChannel string                        `json:"source_channel,omitempty"`
	CampaignID    string                        `json:"campaign_id,omitempty"`
}

func decodeSubmission(w http.ResponseWriter, r *http.Request) (publicSubmissionBody, []surveyport.AttachmentUpload, func(), error) {
	var body publicSubmissionBody
	cleanup := func() {}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		if err != nil || mediaType != "application/json" {
			return body, nil, cleanup, surveyport.ErrInvalid
		}
		err = decode(r, &body)
		if err != nil {
			err = &surveyport.FieldError{Code: "invalid_public_input", Message: "invalid_public_input"}
		}
		return body, nil, cleanup, err
	}
	// A multipart POST is a simple cross-origin request. Require a same-origin
	// browser context rather than inheriting JSON's preflight protection.
	origin, err := url.Parse(r.Header.Get("Origin"))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || err != nil || origin.Host != r.Host || (origin.Scheme != scheme && !(r.Header.Get("X-Forwarded-Proto") == origin.Scheme && origin.Scheme == "https")) {
		return body, nil, cleanup, surveyport.ErrResultForbidden
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	*r = *r.WithContext(ctx)
	cleanup = cancel
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(120 * time.Second)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	// At most the definition's existing 200 fields; files spill to request-owned
	// disk at 32KB. Each individual file has its strict size checked below.
	r.Body = http.MaxBytesReader(w, r.Body, 200*(surveyport.MaximumAttachmentBytes-1)+maxBody+(1<<20))
	err = r.ParseMultipartForm(32 << 10)
	if r.MultipartForm != nil {
		cleanup = func() { cancel(); _ = r.MultipartForm.RemoveAll() }
	}
	if err != nil {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	if len(r.MultipartForm.Value) != 1 || len(r.MultipartForm.Value["submission"]) != 1 {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	raw := r.MultipartForm.Value["submission"][0]
	if len(raw) > maxBody {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&body); err != nil {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	var uploads []surveyport.AttachmentUpload
	if len(r.MultipartForm.File) > 200 {
		return body, nil, cleanup, surveyport.ErrInvalid
	}
	for name, files := range r.MultipartForm.File {
		if !strings.HasPrefix(name, "attachment:") || len(files) != 1 {
			return body, nil, cleanup, surveyport.ErrInvalid
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(name, "attachment:"), 10, 64)
		if err != nil || id < 1 {
			return body, nil, cleanup, surveyport.ErrInvalid
		}
		file := files[0]
		if file.Size >= surveyport.MaximumAttachmentBytes {
			return body, nil, cleanup, &surveyport.FieldError{Code: "attachment_too_large", Message: "图片必须小于 2MB", QuestionID: surveyport.ID(id)}
		}
		uploads = append(uploads, surveyport.AttachmentUpload{QuestionID: surveyport.ID(id), FileName: file.Filename, MediaType: file.Header.Get("Content-Type"), Open: func() (io.ReadCloser, error) { return file.Open() }})
	}
	return body, uploads, cleanup, nil
}
func (h *Handler) submissionAttachment(w http.ResponseWriter, r *http.Request, qid, sid, aid surveyport.ID) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	reader, ok := h.submissions.(surveyport.SubmissionAttachmentReader)
	if !ok {
		resultError(w, surveyport.ErrUnavailable)
		return
	}
	image, err := reader.ReadSubmissionAttachment(r.Context(), qid, sid, aid)
	if err != nil {
		resultError(w, err)
		return
	}
	w.Header().Set("Content-Type", image.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": image.FileName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(image.Content)
}
