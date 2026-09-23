// Package outbound is the sole owner of WeCom business-write intent.  This
// adapter accepts only opaque tag-catalog sync intent; it has no customer or
// provider credential data and delegates durable execution to EER.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type TagCatalogSyncAccepter struct {
	effects effectport.TransactionalAccepter
}
type TagCatalogCompletionSink struct{ writer tagport.SyncCompletionWriter }

func NewTagCatalogCompletionSink(writer tagport.SyncCompletionWriter) (*TagCatalogCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("tag snapshot writer is required")
	}
	return &TagCatalogCompletionSink{writer}, nil
}
func (s *TagCatalogCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.writer == nil || envelope.Kind != effectport.KindWeComTagCatalog {
		return errors.New("invalid tag catalog completion")
	}
	state, ok := tagCompletionState(result.Completion)
	if !ok || (state == tagport.SyncExecuted && (!result.Artifact.Valid() || result.Artifact.Kind != "wecom.tag_catalog.snapshot.v1")) {
		return errors.New("invalid tag catalog completion")
	}
	// effect id is intentionally opaque to the adapter; source receipt is not a customer identifier.
	effectID := parseEffectID(effectRef)
	if effectID < 1 {
		return errors.New("invalid tag effect reference")
	}
	return s.writer.CompleteProviderSync(ctx, tagport.SyncCompletion{EffectID: effectID, Generation: attempt.Generation, State: state, ArtifactDigest: string(result.Artifact.Digest), Snapshot: result.Artifact.Payload})
}

func tagCompletionState(state effectport.State) (tagport.SyncState, bool) {
	switch state {
	case effectport.StateQueued:
		return tagport.SyncQueued, true
	case effectport.StateExecuted:
		return tagport.SyncExecuted, true
	case effectport.StateUnknown:
		return tagport.SyncOutcomeUnknown, true
	case effectport.StateRetryable:
		return tagport.SyncRetryableFailed, true
	case effectport.StateFinalFailed:
		return tagport.SyncFinalFailed, true
	case effectport.StateCancelled:
		return tagport.SyncCancelled, true
	case effectport.StateReconciled:
		return tagport.SyncReconciled, true
	default:
		return "", false
	}
}

func NewTagCatalogSyncAccepter(effects effectport.TransactionalAccepter) (*TagCatalogSyncAccepter, error) {
	if effects == nil {
		return nil, errors.New("external effects transaction accepter is required")
	}
	return &TagCatalogSyncAccepter{effects}, nil
}
func (a *TagCatalogSyncAccepter) EnqueueSync(ctx context.Context, job tagport.SyncJob) (tagport.SyncEffectReceipt, error) {
	if a == nil || a.effects == nil || job.ReceiptID < 1 || job.Actor < 1 || job.IdempotencyKey == "" {
		return tagport.SyncEffectReceipt{}, errors.New("invalid tag catalog sync intent")
	}
	key := effectport.Hash("outbound.wecom.tag_catalog.sync", strconv.FormatInt(job.Actor, 10), job.IdempotencyKey)
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComTagCatalog,
		SourceRefDigest:   effectport.Hash("tag.sync.receipt", strconv.FormatInt(job.ReceiptID, 10)),
		TargetRefDigest:   effectport.Hash("wecom.tag.catalog", "single-enterprise"),
		PayloadDigest:     effectport.Hash("tag.sync.payload", string(job.Kind), job.TraceID),
		PolicyVersionHash: effectport.Hash("outbound.wecom.tag_catalog.policy", "v1")}
	p, receipt, err := a.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: key, Envelope: envelope})
	if err != nil {
		return tagport.SyncEffectReceipt{}, err
	}
	if p.ID == "" || p.QueueJobID < 1 || receipt.ID == "" {
		return tagport.SyncEffectReceipt{}, errors.New("incomplete external effect acceptance")
	}
	if receipt.QueueReceiptID == "" {
		return tagport.SyncEffectReceipt{}, errors.New("external effect queue receipt is missing")
	}
	return tagport.SyncEffectReceipt{QueueJobID: p.QueueJobID, EffectID: parseEffectID(p.ID), EffectRef: p.ID, EffectState: string(p.State), AcceptReceiptID: receipt.ID, QueueReceiptID: receipt.QueueReceiptID}, nil
}
func parseEffectID(value string) int64 {
	var id int64
	_, _ = fmt.Sscanf(value, "eer_%d", &id)
	return id
}
