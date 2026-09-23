package domain

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

const MaxAttachmentBytes = 10 << 20

var ErrUnsafeAttachment = errors.New("unsafe attachment upload")

type AttachmentInspection struct {
	MediaType string
}

// ReadAttachmentBounded reads exactly one attachment while refusing an
// over-limit body before it can reach persistence.
func ReadAttachmentBounded(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, ErrUnsafeAttachment
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxAttachmentBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxAttachmentBytes {
		return nil, ErrUnsafeAttachment
	}
	return data, nil
}

// InspectAttachment intentionally implements the minimal local attachment
// policy: a declared PDF whose bytes both sniff as PDF and begin with the PDF
// file signature. Office documents, archives, executables, remote fetches,
// and provider scanning are out of scope and therefore rejected rather than
// guessed about.
func InspectAttachment(filename, declaredMediaType string, data []byte) (AttachmentInspection, error) {
	if !safeAttachmentFilename(filename) || len(data) == 0 || len(data) > MaxAttachmentBytes {
		return AttachmentInspection{}, ErrUnsafeAttachment
	}
	mediaType, _, err := mime.ParseMediaType(declaredMediaType)
	if err != nil || strings.ToLower(mediaType) != "application/pdf" || http.DetectContentType(data) != "application/pdf" || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return AttachmentInspection{}, ErrUnsafeAttachment
	}
	return AttachmentInspection{MediaType: "application/pdf"}, nil
}

func safeAttachmentFilename(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 ||
		value != strings.TrimSpace(value) || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
