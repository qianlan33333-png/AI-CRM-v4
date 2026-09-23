package groupopsmaterial

import (
	"context"
	"errors"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestFreezerReturnsValidatedImmutableProviderManifest(t *testing.T) {
	requiredThrough := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	sources := sourceSnapshot()
	reader := &readerStub{value: PreparedPlan{Items: []PreparedMaterial{
		prepared("image", 7, digest7, digest8, requiredThrough.Add(time.Hour), mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "media-image-7"}),
		prepared("miniprogram", 8, digest8, digest9, requiredThrough.Add(time.Hour), mediaport.GroupOpsProviderReadyAttachment{MsgType: "miniprogram", AppID: "wx-course", PagePath: "pages/today", Title: "今日课程", MediaID: "media-cover-7"}),
		prepared("attachment", 9, digest9, digesta, requiredThrough.Add(time.Hour), mediaport.GroupOpsProviderReadyAttachment{MsgType: "file", MediaID: "media-file-7"}),
		prepared("group_invite", 10, digesta, "", time.Time{}, mediaport.GroupOpsProviderReadyAttachment{MsgType: "link", Title: "加入体验群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef", Description: "领取资料"}),
	}}}
	freezer, err := NewFreezer(reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := freezer.FreezeGroupOpsMaterial(context.Background(), sources, requiredThrough)
	if err != nil || reader.calls != 1 || len(snapshot.Attachments) != 4 {
		t.Fatalf("snapshot=%+v calls=%d err=%v", snapshot, reader.calls, err)
	}
	reader.value.Items[0].Attachment.MediaID = "changed-after-freeze"
	if snapshot.Attachments[0].MediaID != "media-image-7" {
		t.Fatalf("snapshot aliases mutable reader data: %+v", snapshot)
	}
}

func TestFreezerFailsClosedForSourceSwapAndInsufficientLease(t *testing.T) {
	requiredThrough := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	sources := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{Reference: mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7}, SourceDigest: digest7}}}
	freezer, err := NewFreezer(&readerStub{err: errors.New("lease not ready")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = freezer.FreezeGroupOpsMaterial(context.Background(), sources, requiredThrough); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	for _, item := range []PreparedMaterial{
		prepared("image", 8, digest7, digest8, requiredThrough.Add(time.Hour), mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "media-7"}),
		prepared("image", 7, digest8, digest8, requiredThrough.Add(time.Hour), mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "media-7"}),
		prepared("image", 7, digest7, digest8, requiredThrough, mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "media-7"}),
	} {
		candidate, err := NewFreezer(&readerStub{value: PreparedPlan{Items: []PreparedMaterial{item}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = candidate.FreezeGroupOpsMaterial(context.Background(), sources, requiredThrough); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("item=%+v err=%v", item, err)
		}
	}
}

func TestFreezerRejectsTitleOverWeComLimit(t *testing.T) {
	attachment := mediaport.GroupOpsProviderReadyAttachment{MsgType: "miniprogram", AppID: "wx-course", PagePath: "pages/today", Title: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MediaID: "media-cover-7"}
	if err := mediaport.ValidateGroupOpsProviderReadyAttachments([]mediaport.GroupOpsProviderReadyAttachment{attachment}); !errors.Is(err, mediaport.ErrInvalidGroupOpsMaterialSnapshot) {
		t.Fatalf("err=%v", err)
	}
}

const digest7 = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
const digest8 = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
const digest9 = "sha256:9999999999999999999999999999999999999999999999999999999999999999"
const digesta = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func sourceSnapshot() mediaport.GroupOpsMaterialSourceSnapshot {
	return mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{
		{Reference: mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7}, SourceDigest: digest7},
		{Reference: mediaport.GroupOpsMaterialReference{Kind: "miniprogram", ID: 8}, SourceDigest: digest8, ThumbnailImageID: 7, ThumbnailSourceDigest: digest7, ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "miniprogram", AppID: "wx-course", PagePath: "pages/today", Title: "今日课程"}},
		{Reference: mediaport.GroupOpsMaterialReference{Kind: "attachment", ID: 9}, SourceDigest: digest9},
		{Reference: mediaport.GroupOpsMaterialReference{Kind: "group_invite", ID: 10}, SourceDigest: digesta, ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "link", Title: "加入体验群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef", Description: "领取资料"}},
	}}
}

func prepared(kind string, id int64, source, receipt string, until time.Time, attachment mediaport.GroupOpsProviderReadyAttachment) PreparedMaterial {
	return PreparedMaterial{Reference: mediaport.GroupOpsMaterialReference{Kind: kind, ID: id}, SourceDigest: source, ReceiptDigest: receipt, ReadyUntil: until, Attachment: attachment}
}

type readerStub struct {
	value PreparedPlan
	err   error
	calls int
}

func (stub *readerStub) ReadPreparedGroupOpsPlan(_ context.Context, _ mediaport.GroupOpsMaterialSourceSnapshot, _ time.Time) (PreparedPlan, error) {
	stub.calls++
	return stub.value, stub.err
}
