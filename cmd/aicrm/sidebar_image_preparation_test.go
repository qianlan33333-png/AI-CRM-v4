package main

import (
	"context"
	"errors"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type sidebarSourceStub struct{ err error }

func (s sidebarSourceStub) GetSourceSnapshot(_ context.Context, ref string) (outboundport.MaterialSourceSnapshot, error) {
	return outboundport.MaterialSourceSnapshot{SourceRef: ref, SourceType: "image", FileName: "image.png", MediaType: "image/png", SizeBytes: 13, SnapshotVersion: 1}, s.err
}
func (sidebarSourceStub) ListEnabledSourceSnapshots(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return outboundport.MaterialSnapshotPage{}, nil
}
func (sidebarSourceStub) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, nil
}

type sidebarPreparerStub struct {
	result  outboundport.MaterialResult
	err     error
	calls   int
	request outboundport.MaterialRequest
}

func (s *sidebarPreparerStub) Prepare(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return outboundport.MaterialResult{}, errors.New("unexpected Prepare")
}
func (s *sidebarPreparerStub) ReadyForSend(_ context.Context, request outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	s.calls++
	s.request = request
	return s.result, s.err
}

func TestSidebarPreparationUsesUnifiedProviderCredential(t *testing.T) {
	through := time.Now().Add(6 * time.Minute)
	for _, item := range []struct {
		name   string
		result outboundport.MaterialResult
		err    error
		want   error
	}{
		{"queued", outboundport.MaterialResult{State: "queued"}, nil, mediaport.ErrSidebarMaterialPreparing},
		{"unknown", outboundport.MaterialResult{State: "outcome_unknown"}, outboundport.MediaPreparationTerminalError{State: "outcome_unknown", Code: "upload_outcome_unknown"}, mediaport.ErrSidebarMaterialOutcomeUnknown},
		{"failed", outboundport.MaterialResult{State: "final_failed"}, outboundport.MediaPreparationTerminalError{State: "final_failed", Code: "provider_rejected"}, mediaport.ErrSidebarMaterialPreparationFailed},
		{"ready", outboundport.MaterialResult{State: "ready", MediaID: "provider-id", ExpiresAt: through.Add(time.Hour)}, nil, nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			preparer := &sidebarPreparerStub{result: item.result, err: item.err}
			adapter := sidebarImagePreparation{enabled: true, sources: sidebarSourceStub{}, preparer: preparer, scopeDigest: "scope-digest"}
			result, err := adapter.ReadSidebarImageForSend(context.Background(), 5, through)
			if !errors.Is(err, item.want) {
				t.Fatalf("error=%v want=%v", err, item.want)
			}
			if preparer.request.SourceRef != "image:5" || preparer.request.ValidThrough != through || preparer.request.CorpScopeDigest != "scope-digest" {
				t.Fatal("request not bound to frozen media source")
			}
			if item.want == nil && result.MediaID != "provider-id" {
				t.Fatal("missing actual provider credential")
			}
		})
	}
	preparer := &sidebarPreparerStub{}
	adapter := sidebarImagePreparation{enabled: true, sources: sidebarSourceStub{err: errors.New("disabled image")}, preparer: preparer}
	if _, err := adapter.ReadSidebarImageForSend(context.Background(), 5, through); err == nil || preparer.calls != 0 {
		t.Fatal("disabled image queued preparation")
	}
}
