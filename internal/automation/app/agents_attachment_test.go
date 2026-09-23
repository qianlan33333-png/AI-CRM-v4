package app

import (
	"context"
	"errors"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type agentAttachmentReader struct {
	exists map[int64]bool
	err    error
	calls  []int64
}

func (reader *agentAttachmentReader) AttachmentExists(_ context.Context, id int64) (bool, error) {
	reader.calls = append(reader.calls, id)
	return reader.exists[id], reader.err
}

func TestAgentAttachmentReferencesFailClosed(t *testing.T) {
	service := &Service{}
	if err := service.validateAttachmentReferences(context.Background(), automationport.FixedContentPackage{}); err != nil {
		t.Fatalf("empty attachment set err=%v", err)
	}
	if err := service.validateAttachmentReferences(context.Background(), automationport.FixedContentPackage{AttachmentLibraryIDs: []int64{7}}); !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("missing reader err=%v", err)
	}
	reader := &agentAttachmentReader{exists: map[int64]bool{7: true}}
	service.attachments = reader
	if err := service.validateAttachmentReferences(context.Background(), automationport.FixedContentPackage{AttachmentLibraryIDs: []int64{7}}); err != nil || len(reader.calls) != 1 || reader.calls[0] != 7 {
		t.Fatalf("valid reader err=%v calls=%v", err, reader.calls)
	}
	reader.calls, reader.exists = nil, map[int64]bool{}
	if err := service.validateAttachmentReferences(context.Background(), automationport.FixedContentPackage{AttachmentLibraryIDs: []int64{7}}); !errors.Is(err, ErrInvalidAgent) || len(reader.calls) != 1 {
		t.Fatalf("missing attachment err=%v calls=%v", err, reader.calls)
	}
	reader.calls, reader.err = nil, errors.New("store unavailable")
	if err := service.validateAttachmentReferences(context.Background(), automationport.FixedContentPackage{AttachmentLibraryIDs: []int64{7}}); !errors.Is(err, ErrAgentUnavailable) || len(reader.calls) != 1 {
		t.Fatalf("reader failure err=%v calls=%v", err, reader.calls)
	}
}
