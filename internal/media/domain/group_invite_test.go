package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestH03GroupInviteMetadataBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	item, err := NewGroupInvite(mediaport.GroupInviteCreateCommand{Title: " 入群卡 ", Description: " 说明 ",
		JoinURL: "https://work.weixin.qq.com/gm/safe-token", Actor: 7}, now)
	if err != nil || item.Name != "入群卡" || item.Title != "入群卡" || !item.Enabled || item.Version != 1 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	for _, test := range []struct {
		name    string
		command mediaport.GroupInviteCreateCommand
	}{
		{"empty title", mediaport.GroupInviteCreateCommand{JoinURL: "https://work.weixin.qq.com/gm/safe", Actor: 7}},
		{"title bytes", mediaport.GroupInviteCreateCommand{Title: strings.Repeat("界", 43), JoinURL: "https://work.weixin.qq.com/gm/safe", Actor: 7}},
		{"description bytes", mediaport.GroupInviteCreateCommand{Title: "入群", Description: strings.Repeat("界", 171), JoinURL: "https://work.weixin.qq.com/gm/safe", Actor: 7}},
		{"wrong host", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "https://example.com/gm/safe", Actor: 7}},
		{"wrong scheme", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "http://work.weixin.qq.com/gm/safe", Actor: 7}},
		{"userinfo", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "https://user@work.weixin.qq.com/gm/safe", Actor: 7}},
		{"query", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "https://work.weixin.qq.com/gm/safe?q=1", Actor: 7}},
		{"missing token", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "https://work.weixin.qq.com/gm/", Actor: 7}},
		{"negative image", mediaport.GroupInviteCreateCommand{Title: "入群", JoinURL: "https://work.weixin.qq.com/gm/safe", CoverImageID: -1, Actor: 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewGroupInvite(test.command, now); !errors.Is(err, ErrInvalidGroupInvite) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	exact, err := NewGroupInvite(mediaport.GroupInviteCreateCommand{Title: strings.Repeat("x", 128),
		Description: strings.Repeat("x", 512), JoinURL: "https://work.weixin.qq.com/gm/safe", Actor: 7}, now)
	if err != nil || !ValidGroupInvite(exact, false) {
		t.Fatalf("exact boundary=%#v err=%v", exact, err)
	}
}

func TestH03GroupInvitePatchAndArchive(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	item, err := NewGroupInvite(mediaport.GroupInviteCreateCommand{Name: "素材", Title: "入群", JoinURL: "https://work.weixin.qq.com/gm/safe", Actor: 7}, now)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = 9
	if _, err = ApplyGroupInvitePatch(item, mediaport.GroupInvitePatch{}, 7, now.Add(time.Minute)); !errors.Is(err, ErrInvalidGroupInvite) {
		t.Fatalf("empty patch err=%v", err)
	}
	title, enabled := "新标题", false
	updated, err := ApplyGroupInvitePatch(item, mediaport.GroupInvitePatch{Title: &title, Enabled: &enabled}, 8, now.Add(time.Minute))
	if err != nil || updated.Title != title || updated.Enabled || updated.UpdatedBy != 8 || updated.Version != 2 {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	archived, err := ArchiveGroupInvite(updated, 8, now.Add(2*time.Minute))
	if err != nil || archived.ArchivedAt == nil || archived.Enabled || archived.Version != 3 {
		t.Fatalf("archived=%#v err=%v", archived, err)
	}
}
