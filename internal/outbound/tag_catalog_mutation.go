package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// TagCatalogMutationAccepter is Outbound's only accept path for an official
// WeCom tag-directory write. Tag persists the immutable dispatch snapshot;
// this adapter owns only EER envelope construction and queueing.
type TagCatalogMutationAccepter struct {
	effects effectport.TransactionalAccepter
}

func NewTagCatalogMutationAccepter(effects effectport.TransactionalAccepter) (*TagCatalogMutationAccepter, error) {
	if effects == nil {
		return nil, errors.New("external effects transaction accepter is required")
	}
	return &TagCatalogMutationAccepter{effects: effects}, nil
}

func (a *TagCatalogMutationAccepter) EnqueueCatalogMutation(ctx context.Context, intent tagport.CatalogMutationIntent, idempotencyKey string) (tagport.CatalogMutationEffectReceipt, error) {
	if a == nil || a.effects == nil || intent.ID < 1 || intent.Actor < 1 || idempotencyKey == "" || !validTagCatalogMutation(intent) {
		return tagport.CatalogMutationEffectReceipt{}, errors.New("invalid tag catalog mutation intent")
	}
	source := effectport.Hash("tag.catalog.mutation.source.v1", strconv.FormatInt(intent.ID, 10))
	envelope := effectport.Envelope{
		Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComTagCatalogMutation,
		SourceRefDigest:   source,
		TargetRefDigest:   effectport.Hash("tag.catalog.mutation.target.v1", strconv.FormatInt(intent.GroupID, 10), strconv.FormatInt(intent.TagID, 10), intent.ProviderGroupID, intent.ProviderTagID),
		PayloadDigest:     effectport.Hash("tag.catalog.mutation.payload.v1", string(intent.Operation), intent.GroupName, intent.TagName, intent.ProviderGroupID, intent.ProviderTagID),
		PolicyVersionHash: effectport.Hash("tag.catalog.mutation.policy.v1"),
	}
	projection, receipt, err := a.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("outbound.wecom.tag_catalog.mutation.accept.v1", strconv.FormatInt(intent.Actor, 10), idempotencyKey), Envelope: envelope})
	if err != nil {
		return tagport.CatalogMutationEffectReceipt{}, err
	}
	if projection.ID == "" || projection.State != effectport.StateQueued || projection.QueueJobID < 1 || receipt.ID == "" || receipt.QueueReceiptID == "" {
		return tagport.CatalogMutationEffectReceipt{}, errors.New("incomplete tag catalog mutation acceptance")
	}
	return tagport.CatalogMutationEffectReceipt{EffectID: parseEffectID(projection.ID), QueueJobID: projection.QueueJobID, EffectRef: projection.ID, EffectState: string(projection.State), AcceptReceiptID: receipt.ID, QueueReceiptID: receipt.QueueReceiptID}, nil
}

type TagCatalogMutationProvider struct {
	dispatch tagport.CatalogMutationDispatchReader
	writer   wecomport.TagCatalogMutationWriter
	reader   CatalogReader
	now      func() time.Time
}

func NewTagCatalogMutationProvider(dispatch tagport.CatalogMutationDispatchReader, writer wecomport.TagCatalogMutationWriter, reader CatalogReader) (*TagCatalogMutationProvider, error) {
	if dispatch == nil || writer == nil || reader == nil {
		return nil, errors.New("tag catalog mutation provider dependencies are required")
	}
	return &TagCatalogMutationProvider{dispatch: dispatch, writer: writer, reader: reader, now: time.Now}, nil
}

func (p *TagCatalogMutationProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || p.dispatch == nil || p.writer == nil || p.reader == nil || envelope.Kind != effectport.KindWeComTagCatalogMutation || envelope.PolicyVersionHash != effectport.Hash("tag.catalog.mutation.policy.v1") || attempt.EffectID == "" {
		return tagCatalogMutationFinal("invalid_command", effectport.Hash("wecom.tag.catalog.mutation.invalid")), nil
	}
	dispatch, err := p.dispatch.ReadCatalogMutationDispatch(ctx, string(envelope.SourceRefDigest))
	if err != nil || dispatch.EffectRef != attempt.EffectID || !validTagCatalogMutation(dispatch.CatalogMutationIntent) || !sameTagCatalogMutationEnvelope(envelope, dispatch.CatalogMutationIntent) {
		return tagCatalogMutationFinal("dispatch_changed", effectport.Hash("wecom.tag.catalog.mutation.dispatch_changed", string(envelope.Fingerprint()))), nil
	}
	result, err := p.writer.MutateTagCatalog(ctx, wecomport.TagCatalogMutation{Operation: string(dispatch.Operation), GroupName: dispatch.GroupName, TagName: dispatch.TagName, ProviderGroupID: dispatch.ProviderGroupID, ProviderTagID: dispatch.ProviderTagID})
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if attempted && (!wecomport.ProviderWriteClassified(err) || wecomport.ProviderOutcomeUnknown(err)) {
			state = effectport.StateUnknown
		} else if attempted && !wecomport.ProviderRetryable(err) {
			state = effectport.StateFinalFailed
		}
		if state == effectport.StateFinalFailed {
			rejected := tagCatalogMutationFinal("provider_rejected", effectport.Hash("wecom.tag.catalog.mutation.rejected", dispatch.EffectRef, strconv.Itoa(int(attempt.Number))))
			rejected.CallAttempted = attempted
			return rejected, nil
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("wecom.tag.catalog.mutation.error", dispatch.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if !validMutationResult(dispatch.CatalogMutationIntent, result) {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("wecom.tag.catalog.mutation.invalid_response", dispatch.EffectRef), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	artifact := tagCatalogMutationArtifact{ProviderGroupID: result.ProviderGroupID, ProviderTagID: result.ProviderTagID}
	if snapshot, readErr := p.reader.ListCatalog(ctx); readErr == nil && catalogMutationReadbackMatches(dispatch.CatalogMutationIntent, result, snapshot) {
		now := p.now().UTC()
		artifact.ReadbackAt = &now
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("wecom.tag.catalog.mutation.artifact_encode", dispatch.EffectRef), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	resultArtifact := effectport.ResultArtifact{Kind: "wecom.tag_catalog.mutation.v1", Payload: payload}
	resultArtifact.Digest = effectport.Hash("external-effect.artifact.v1", resultArtifact.Kind, string(payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("wecom.tag.catalog.mutation.executed", dispatch.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true, Artifact: resultArtifact}, nil
}

type tagCatalogMutationArtifact struct {
	ProviderGroupID string     `json:"provider_group_id,omitempty"`
	ProviderTagID   string     `json:"provider_tag_id,omitempty"`
	ReadbackAt      *time.Time `json:"readback_at,omitempty"`
}

func tagCatalogMutationFinal(reason string, digest effectport.Digest) effectport.AdapterResult {
	payload := []byte(reason)
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: digest, Artifact: effectport.ResultArtifact{Kind: "wecom.tag_catalog.mutation_reason.v1", Payload: payload, Digest: effectport.Hash("external-effect.artifact.v1", "wecom.tag_catalog.mutation_reason.v1", string(payload))}}
}

func validTagCatalogMutation(intent tagport.CatalogMutationIntent) bool {
	switch intent.Operation {
	case tagport.CatalogGroupCreate:
		return intent.GroupID > 0 && intent.TagID > 0 && intent.GroupName != "" && intent.TagName != "" && intent.ProviderGroupID == "" && intent.ProviderTagID == ""
	case tagport.CatalogTagCreate:
		return intent.GroupID > 0 && intent.TagID > 0 && intent.TagName != "" && intent.ProviderGroupID != "" && intent.ProviderTagID == ""
	case tagport.CatalogGroupUpdate:
		return intent.GroupID > 0 && intent.TagID == 0 && intent.GroupName != "" && intent.ProviderGroupID != ""
	case tagport.CatalogTagUpdate:
		return intent.GroupID == 0 && intent.TagID > 0 && intent.TagName != "" && intent.ProviderTagID != ""
	case tagport.CatalogGroupArchive:
		return intent.GroupID > 0 && intent.TagID == 0 && intent.ProviderGroupID != ""
	case tagport.CatalogTagArchive:
		return intent.GroupID == 0 && intent.TagID > 0 && intent.ProviderTagID != ""
	default:
		return false
	}
}

func sameTagCatalogMutationEnvelope(envelope effectport.Envelope, intent tagport.CatalogMutationIntent) bool {
	return envelope.TargetRefDigest == effectport.Hash("tag.catalog.mutation.target.v1", strconv.FormatInt(intent.GroupID, 10), strconv.FormatInt(intent.TagID, 10), intent.ProviderGroupID, intent.ProviderTagID) &&
		envelope.PayloadDigest == effectport.Hash("tag.catalog.mutation.payload.v1", string(intent.Operation), intent.GroupName, intent.TagName, intent.ProviderGroupID, intent.ProviderTagID)
}

func validMutationResult(intent tagport.CatalogMutationIntent, result wecomport.TagCatalogMutationResult) bool {
	switch intent.Operation {
	case tagport.CatalogGroupCreate:
		return result.ProviderGroupID != "" && result.ProviderTagID != ""
	case tagport.CatalogTagCreate:
		return result.ProviderGroupID == intent.ProviderGroupID && result.ProviderTagID != ""
	default:
		return result.ProviderGroupID == intent.ProviderGroupID && result.ProviderTagID == intent.ProviderTagID
	}
}

func catalogMutationReadbackMatches(intent tagport.CatalogMutationIntent, result wecomport.TagCatalogMutationResult, snapshot CatalogSnapshot) bool {
	groupFound, groupNameMatches, tagFound, tagNameMatches := false, false, false, false
	for _, group := range snapshot.Groups {
		if result.ProviderGroupID != "" && group.ID == result.ProviderGroupID {
			groupFound = true
			groupNameMatches = group.Name == intent.GroupName
		}
		for _, tag := range group.Tags {
			if tag.ID == result.ProviderTagID {
				tagFound = true
				tagNameMatches = tag.Name == intent.TagName
			}
		}
	}
	switch intent.Operation {
	case tagport.CatalogGroupCreate:
		return groupFound && groupNameMatches && tagFound && tagNameMatches
	case tagport.CatalogTagCreate:
		return groupFound && tagFound && tagNameMatches
	case tagport.CatalogGroupUpdate:
		return groupFound && groupNameMatches
	case tagport.CatalogTagUpdate:
		return tagFound && tagNameMatches
	case tagport.CatalogGroupArchive:
		return !groupFound
	case tagport.CatalogTagArchive:
		return !tagFound
	default:
		return false
	}
}

type TagCatalogMutationCompletionSink struct{ writer tagport.CatalogMutationStore }

func NewTagCatalogMutationCompletionSink(writer tagport.CatalogMutationStore) (*TagCatalogMutationCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("tag catalog mutation completion writer is required")
	}
	return &TagCatalogMutationCompletionSink{writer: writer}, nil
}

func (s *TagCatalogMutationCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.writer == nil || envelope.Kind != effectport.KindWeComTagCatalogMutation || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid tag catalog mutation completion")
	}
	completion := tagport.CatalogMutationCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, Generation: attempt.Generation, Fence: attempt.Fence, CompletedAt: time.Now().UTC()}
	if result.Completion == effectport.StateExecuted {
		if !result.Artifact.Valid() || result.Artifact.Kind != "wecom.tag_catalog.mutation.v1" {
			return errors.New("tag catalog mutation result artifact is required")
		}
		var artifact tagCatalogMutationArtifact
		if json.Unmarshal(result.Artifact.Payload, &artifact) != nil {
			return errors.New("invalid tag catalog mutation result artifact")
		}
		completion.ProviderGroupID, completion.ProviderTagID, completion.ReadbackAt = artifact.ProviderGroupID, artifact.ProviderTagID, artifact.ReadbackAt
	} else if result.Artifact.Kind != "" && (!result.Artifact.Valid() || result.Artifact.Kind != "wecom.tag_catalog.mutation_reason.v1") {
		return errors.New("invalid tag catalog mutation failure artifact")
	}
	return s.writer.CompleteCatalogMutation(ctx, completion)
}

var _ tagport.CatalogMutationEnqueuer = (*TagCatalogMutationAccepter)(nil)
var _ effectport.ProviderAdapter = (*TagCatalogMutationProvider)(nil)
var _ effectport.CompletionSink = (*TagCatalogMutationCompletionSink)(nil)
