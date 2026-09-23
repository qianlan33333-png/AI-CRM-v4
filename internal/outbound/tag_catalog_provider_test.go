package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type catalogReaderFunc func(context.Context) (CatalogSnapshot, error)

func (f catalogReaderFunc) ListCatalog(ctx context.Context) (CatalogSnapshot, error) { return f(ctx) }

func tagEnvelope() effect.Envelope {
	return effect.Envelope{Owner: effect.OwnerOutbound, Kind: effect.KindWeComTagCatalog,
		SourceRefDigest: effect.Hash("source"), TargetRefDigest: effect.Hash("target"), PayloadDigest: effect.Hash("payload"), PolicyVersionHash: effect.Hash("policy")}
}

func TestTagCatalogProviderCanonicalizesAndFiltersDeleted(t *testing.T) {
	provider, err := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		return CatalogSnapshot{Groups: []CatalogGroup{{ID: "g2", Name: "two", Order: 2, Tags: []CatalogTag{{ID: "t2", Name: "two", Order: 2}, {ID: "gone", Name: "gone", Order: 1, Deleted: true}}}, {ID: "g1", Name: "one", Order: 1, Tags: []CatalogTag{{ID: "t1", Name: "one", Order: 1}}}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateExecuted || !result.Artifact.Valid() {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	want := `{"groups":[{"id":"g1","name":"one","order":1,"tags":[{"id":"t1","name":"one","order":1}]},{"id":"g2","name":"two","order":2,"tags":[{"id":"t2","name":"two","order":2}]}]}`
	if string(result.Artifact.Payload) != want {
		t.Fatalf("payload=%s", result.Artifact.Payload)
	}
}

func TestTagCatalogProviderFailureBoundaries(t *testing.T) {
	for name, readErr := range map[string]error{
		"pre-call":  &ReadError{Err: errors.New("unavailable")},
		"post-call": &ReadError{Err: errors.New("timeout"), CallAttempted: true},
	} {
		t.Run(name, func(t *testing.T) {
			provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) { return CatalogSnapshot{}, readErr }))
			result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
			if err == nil {
				t.Fatal("expected error")
			}
			if name == "pre-call" && (result.Completion != effect.StateRetryable || result.CallAttempted) {
				t.Fatalf("result=%+v", result)
			}
			if name == "post-call" && (result.Completion != effect.StateUnknown || !result.CallAttempted) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
	provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		return CatalogSnapshot{Groups: []CatalogGroup{{ID: "g", Name: "g", Tags: []CatalogTag{{ID: "x", Name: "x"}, {ID: "x", Name: "again"}}}}}, nil
	}))
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
	if err != nil || result.Completion != effect.StateUnknown || !result.CallAttempted || result.Artifact.Valid() {
		t.Fatalf("invalid=%+v err=%v", result, err)
	}
}

func TestTagCatalogProviderEmptySnapshotIsExecuted(t *testing.T) {
	provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) { return CatalogSnapshot{Groups: []CatalogGroup{}}, nil }))
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
	if err != nil || result.Completion != effect.StateExecuted || string(result.Artifact.Payload) != `{"groups":[]}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCanonicalCatalogSnapshotRejectsMissingGroupsAndControlText(t *testing.T) {
	if _, ok := CanonicalCatalogSnapshot(CatalogSnapshot{}); ok {
		t.Fatal("missing groups accepted")
	}
	if _, ok := CanonicalCatalogSnapshot(CatalogSnapshot{Groups: []CatalogGroup{{ID: "g\x00", Name: "name", Tags: []CatalogTag{}}}}); ok {
		t.Fatal("control identifier accepted")
	}
}

type catalogMutationDispatchStore struct {
	dispatch tagport.CatalogMutationDispatch
}

func (catalogMutationDispatchStore) GuardCatalogMutation(context.Context, tagport.CatalogMutationScope) error {
	return errors.New("not used")
}

func (s catalogMutationDispatchStore) ReserveCatalogMutation(context.Context, tagport.CatalogMutationPlan) (tagport.CatalogMutationIntent, error) {
	return tagport.CatalogMutationIntent{}, errors.New("not used")
}
func (s catalogMutationDispatchStore) AcceptCatalogMutation(context.Context, int64, tagport.CatalogMutationEffectReceipt) error {
	return errors.New("not used")
}
func (s catalogMutationDispatchStore) ReadCatalogMutationDispatch(_ context.Context, source string) (tagport.CatalogMutationDispatch, error) {
	if source != s.dispatch.SourceRefDigest {
		return tagport.CatalogMutationDispatch{}, errors.New("wrong source")
	}
	return s.dispatch, nil
}
func (s catalogMutationDispatchStore) CompleteCatalogMutation(context.Context, tagport.CatalogMutationCompletion) error {
	return errors.New("not used")
}

type catalogMutationWriterFunc func(context.Context, wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error)

func (f catalogMutationWriterFunc) MutateTagCatalog(ctx context.Context, mutation wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
	return f(ctx, mutation)
}

func mutationEnvelope(intent tagport.CatalogMutationIntent) effect.Envelope {
	return effect.Envelope{Owner: effect.OwnerOutbound, Kind: effect.KindWeComTagCatalogMutation,
		SourceRefDigest: effect.Hash("tag.catalog.mutation.source.v1", strconv.FormatInt(intent.ID, 10)),
		TargetRefDigest: effect.Hash("tag.catalog.mutation.target.v1", strconv.FormatInt(intent.GroupID, 10), strconv.FormatInt(intent.TagID, 10), intent.ProviderGroupID, intent.ProviderTagID),
		PayloadDigest:   effect.Hash("tag.catalog.mutation.payload.v1", string(intent.Operation), intent.GroupName, intent.TagName, intent.ProviderGroupID, intent.ProviderTagID), PolicyVersionHash: effect.Hash("tag.catalog.mutation.policy.v1")}
}

func TestTagCatalogMutationProviderBindsOnlyConfirmedCreateAndReadback(t *testing.T) {
	intent := tagport.CatalogMutationIntent{ID: 8, Operation: tagport.CatalogGroupCreate, Actor: 7, GroupID: 21, TagID: 34, GroupName: "阶段", TagName: "新客"}
	envelope := mutationEnvelope(intent)
	dispatch := tagport.CatalogMutationDispatch{CatalogMutationIntent: intent, EffectRef: "eer_9", SourceRefDigest: string(envelope.SourceRefDigest)}
	called := 0
	provider, err := NewTagCatalogMutationProvider(catalogMutationDispatchStore{dispatch: dispatch}, catalogMutationWriterFunc(func(_ context.Context, input wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
		called++
		if input.Operation != "group_create" || input.GroupName != "阶段" || input.TagName != "新客" {
			t.Fatalf("writer input=%+v", input)
		}
		return wecomport.TagCatalogMutationResult{ProviderGroupID: "group-9", ProviderTagID: "tag-9"}, nil
	}), catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		return CatalogSnapshot{Groups: []CatalogGroup{{ID: "group-9", Name: "阶段", Tags: []CatalogTag{{ID: "tag-9", Name: "新客"}}}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_9", Number: 1, Generation: 1, Fence: 1})
	if err != nil || called != 1 || result.Completion != effect.StateExecuted || !result.Artifact.Valid() {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, called)
	}
	var artifact tagCatalogMutationArtifact
	if json.Unmarshal(result.Artifact.Payload, &artifact) != nil || artifact.ProviderGroupID != "group-9" || artifact.ProviderTagID != "tag-9" || artifact.ReadbackAt == nil {
		t.Fatalf("artifact=%s", result.Artifact.Payload)
	}
}

func TestTagCatalogMutationProviderUnknownCreateDoesNotGuessOrRetry(t *testing.T) {
	intent := tagport.CatalogMutationIntent{ID: 8, Operation: tagport.CatalogGroupCreate, Actor: 7, GroupID: 21, TagID: 34, GroupName: "阶段", TagName: "新客"}
	envelope := mutationEnvelope(intent)
	dispatch := tagport.CatalogMutationDispatch{CatalogMutationIntent: intent, EffectRef: "eer_9", SourceRefDigest: string(envelope.SourceRefDigest)}
	called := 0
	provider, _ := NewTagCatalogMutationProvider(catalogMutationDispatchStore{dispatch: dispatch}, catalogMutationWriterFunc(func(context.Context, wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
		called++
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteError(errors.New("timeout"), true)
	}), catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) { return CatalogSnapshot{}, nil }))
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_9", Number: 1, Generation: 1, Fence: 1})
	if err == nil || result.Completion != effect.StateUnknown || !result.CallAttempted || called != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, called)
	}
}

func TestTagCatalogMutationRejectedPreservesAttemptEvidence(t *testing.T) {
	intent := tagport.CatalogMutationIntent{ID: 8, Operation: tagport.CatalogTagArchive, Actor: 7, TagID: 34, ProviderTagID: "provider-tag"}
	envelope := mutationEnvelope(intent)
	dispatch := tagport.CatalogMutationDispatch{CatalogMutationIntent: intent, EffectRef: "eer_9", SourceRefDigest: string(envelope.SourceRefDigest)}
	provider, err := NewTagCatalogMutationProvider(catalogMutationDispatchStore{dispatch: dispatch}, catalogMutationWriterFunc(func(context.Context, wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteDisposition(errors.New("rejected"), true, false, false)
	}), catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		t.Fatal("rejected mutation should not read back")
		return CatalogSnapshot{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_9", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || !result.CallAttempted || result.RealExternalCallExecuted || result.ReceiptDigest != effect.Hash("wecom.tag.catalog.mutation.rejected", "eer_9", "1") {
		t.Fatalf("rejection evidence=%+v err=%v", result, err)
	}
}
