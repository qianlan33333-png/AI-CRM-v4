package app

import (
	"context"
	"errors"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestGroupOpsPreparationWriterCommitsValidatedProviderReceipt(t *testing.T) {
	through := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	sources := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
		Reference:    mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7},
		SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}}
	command := mediaport.GroupOpsMaterialPreparationCommand{
		SourceSnapshot:  sources,
		RequiredThrough: through,
		Actor:           7,
		IdempotencyKey:  "prep-writer-command-0001",
		Items: []mediaport.GroupOpsMaterialPreparation{{
			Reference:     sources.References[0].Reference,
			SourceDigest:  sources.References[0].SourceDigest,
			ReceiptDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ReadyUntil:    through.Add(time.Hour),
			Attachment:    mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "provider-image-7"},
		}},
	}
	store := &preparationStoreTestDouble{}
	writer := NewGroupOpsMaterialPreparationWriter(preparationUOWTestDouble{}, store)
	writer.now = func() time.Time { return through }
	receipt, err := writer.RecordPreparedGroupOpsMaterials(context.Background(), command)
	if err != nil || receipt.ID != 42 || store.calls != 1 || store.command.Items[0].Attachment.MediaID != "provider-image-7" {
		t.Fatalf("receipt=%+v calls=%d command=%+v err=%v", receipt, store.calls, store.command, err)
	}
}

func TestGroupOpsPreparationWriterRejectsUnprovedMedia(t *testing.T) {
	command := mediaport.GroupOpsMaterialPreparationCommand{
		SourceSnapshot:  mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{Reference: mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7}, SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},
		RequiredThrough: time.Now().UTC().Add(time.Hour),
		Actor:           7,
		IdempotencyKey:  "prep-writer-invalid-0001",
		Items: []mediaport.GroupOpsMaterialPreparation{{
			Reference:    mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7},
			SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Attachment:   mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "not-enough-without-receipt"},
		}},
	}
	store := &preparationStoreTestDouble{}
	writer := NewGroupOpsMaterialPreparationWriter(preparationUOWTestDouble{}, store)
	if _, err := writer.RecordPreparedGroupOpsMaterials(context.Background(), command); !errors.Is(err, mediaport.ErrInvalidGroupOpsMaterialPreparation) || store.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, store.calls)
	}
}

type preparationUOWTestDouble struct{}

func (preparationUOWTestDouble) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type preparationStoreTestDouble struct {
	command mediaport.GroupOpsMaterialPreparationCommand
	calls   int
}

func (store *preparationStoreTestDouble) RecordPreparedGroupOpsMaterialsWithin(_ context.Context, command mediaport.GroupOpsMaterialPreparationCommand, _ time.Time) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	store.calls++
	store.command = command
	return mediaport.GroupOpsMaterialPreparationReceipt{ID: 42, Actor: command.Actor, ItemCount: len(command.Items)}, nil
}
