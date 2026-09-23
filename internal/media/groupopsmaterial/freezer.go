// Package groupopsmaterial freezes Media-owned, provider-ready package facts
// for Group Ops acceptance. It deliberately does not call WeCom: the source
// must have completed the Media-owned preparation/lease boundary before this
// method returns a snapshot that an outbound worker can submit unchanged.
package groupopsmaterial

import (
	"context"
	"errors"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

var ErrUnavailable = errors.New("group ops material freezer unavailable")

type PreparedPlan struct {
	Items []PreparedMaterial
}

// PreparedMaterial carries the exact source and receipt facts behind one
// ordered provider attachment. A matching msgtype alone is not enough: a
// same-type media record may have been replaced between acceptance and send.
type PreparedMaterial struct {
	Reference     mediaport.GroupOpsMaterialReference
	SourceDigest  string
	ReceiptDigest string
	ReadyUntil    time.Time
	Attachment    mediaport.GroupOpsProviderReadyAttachment
}

// PreparedPlanReader is implemented inside Media's composition root. Its read
// must lock every ordered local reference and only return media IDs backed by
// ready Media prep/lease receipts. It must never expose a URL/blob reference
// to the Group Ops worker.
type PreparedPlanReader interface {
	ReadPreparedGroupOpsPlan(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (PreparedPlan, error)
}

type Freezer struct{ reader PreparedPlanReader }

var _ mediaport.GroupOpsMaterialSnapshotFreezer = (*Freezer)(nil)

func NewFreezer(reader PreparedPlanReader) (*Freezer, error) {
	if reader == nil {
		return nil, ErrUnavailable
	}
	return &Freezer{reader: reader}, nil
}

func (freezer *Freezer) FreezeGroupOpsMaterial(ctx context.Context, sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) (mediaport.GroupOpsMaterialSnapshot, error) {
	snapshot, _, err := freezer.FreezeGroupOpsMaterialWithFacts(ctx, sources, requiredThrough)
	return snapshot, err
}

func (freezer *Freezer) FreezeGroupOpsMaterialWithFacts(ctx context.Context, sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) (mediaport.GroupOpsMaterialSnapshot, []PreparedMaterial, error) {
	if freezer == nil || freezer.reader == nil || ctx == nil || requiredThrough.IsZero() || mediaport.ValidateGroupOpsMaterialSourceSnapshot(sources) != nil {
		return mediaport.GroupOpsMaterialSnapshot{}, nil, ErrUnavailable
	}
	pkg, err := freezer.reader.ReadPreparedGroupOpsPlan(ctx, sources, requiredThrough)
	if err != nil || !matchesSources(sources, requiredThrough, pkg.Items) {
		return mediaport.GroupOpsMaterialSnapshot{}, nil, ErrUnavailable
	}
	attachments := make([]mediaport.GroupOpsProviderReadyAttachment, len(pkg.Items))
	for index, item := range pkg.Items {
		attachments[index] = item.Attachment
	}
	snapshot := mediaport.GroupOpsMaterialSnapshot{
		SchemaVersion: 2, NodeKind: "message",
		Attachments: attachments,
	}
	if err := mediaport.ValidateGroupOpsMaterialSnapshot(snapshot); err != nil {
		return mediaport.GroupOpsMaterialSnapshot{}, nil, ErrUnavailable
	}
	return snapshot, append([]PreparedMaterial(nil), pkg.Items...), nil
}

func matchesSources(sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time, items []PreparedMaterial) bool {
	if len(sources.References) != len(items) {
		return false
	}
	for index, source := range sources.References {
		item := items[index]
		if item.Reference != source.Reference || item.SourceDigest != source.SourceDigest || item.Attachment.MsgType == "" {
			return false
		}
		requiresLease := source.Reference.Kind != "group_invite"
		if requiresLease && (item.ReceiptDigest == "" || !item.ReadyUntil.After(requiredThrough)) {
			return false
		}
		if !requiresLease && (item.ReceiptDigest != "" || !item.ReadyUntil.IsZero()) {
			return false
		}
		if source.Reference.Kind == "miniprogram" && (item.Attachment.AppID != source.ProviderFields.AppID || item.Attachment.PagePath != source.ProviderFields.PagePath || item.Attachment.Title != source.ProviderFields.Title) {
			return false
		}
		if source.Reference.Kind == "group_invite" && item.Attachment != source.ProviderFields {
			return false
		}
		reference := source.Reference
		want := reference.Kind
		if want == "attachment" {
			want = "file"
		}
		if want == "group_invite" {
			want = "link"
		}
		if item.Attachment.MsgType != want {
			return false
		}
	}
	return true
}
