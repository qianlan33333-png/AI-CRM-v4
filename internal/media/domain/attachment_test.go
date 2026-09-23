package domain

import (
	"bytes"
	"errors"
	"testing"
)

func TestInspectAttachmentRequiresDeclaredAndSniffedPDF(t *testing.T) {
	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	inspection, err := InspectAttachment("guide.pdf", "application/pdf; charset=binary", pdf)
	if err != nil || inspection.MediaType != "application/pdf" {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	for _, input := range []struct {
		name, filename, declared string
		content                  []byte
	}{
		{"wrong declaration", "guide.pdf", "application/zip", pdf},
		{"non PDF bytes", "guide.pdf", "application/pdf", []byte("PK\x03\x04")},
		{"unsafe name", "../guide.pdf", "application/pdf", pdf},
	} {
		t.Run(input.name, func(t *testing.T) {
			if _, err := InspectAttachment(input.filename, input.declared, input.content); !errors.Is(err, ErrUnsafeAttachment) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReadAttachmentBoundedEnforcesTenMiB(t *testing.T) {
	if _, err := ReadAttachmentBounded(bytes.NewReader(make([]byte, MaxAttachmentBytes))); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	if _, err := ReadAttachmentBounded(bytes.NewReader(make([]byte, MaxAttachmentBytes+1))); !errors.Is(err, ErrUnsafeAttachment) {
		t.Fatalf("over boundary err=%v", err)
	}
}
