package store

import (
	"context"
	"strings"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// BindMaterialPreparationAccepter connects Media's local write transaction to
// Outbound's durable acceptance Port. The port accepts work only; it cannot
// upload to a Provider while the Media transaction is open.
func (r *Repository) BindMaterialPreparationAccepter(accepter outboundport.MaterialPreparationAccepter, scopeDigest string) error {
	if r == nil || accepter == nil || strings.TrimSpace(scopeDigest) == "" || r.materialPreparationAccepter != nil {
		return ErrInvalid
	}
	r.materialPreparationAccepter = accepter
	r.materialPreparationScopeDigest = scopeDigest
	return nil
}

func (r *Repository) acceptMaterialPreparationWithin(ctx context.Context, sourceRef string) error {
	if r.materialPreparationAccepter == nil {
		return nil
	}
	snapshot, err := r.sourceSnapshotWithin(ctx, sourceRef)
	if err != nil {
		return err
	}
	_, err = r.materialPreparationAccepter.AcceptMaterialPreparationWithin(ctx, outboundport.MaterialRequest{
		MaterialSourceSnapshot: outboundport.MaterialSourceSnapshot{
			SourceRef:       snapshot.SourceRef,
			SourceType:      snapshot.SourceType,
			ContentDigest:   snapshot.ContentDigest,
			FileName:        snapshot.FileName,
			MediaType:       snapshot.MediaType,
			SizeBytes:       snapshot.SizeBytes,
			SnapshotVersion: snapshot.SnapshotVersion,
		},
		CorpScopeDigest: r.materialPreparationScopeDigest,
	})
	return err
}
