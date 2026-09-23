package main

import (
	"context"
	"testing"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type audienceChannelCatalogStub struct{ page channelport.CatalogPage }

func (s audienceChannelCatalogStub) List(context.Context, channelport.CatalogFilter) (channelport.CatalogPage, error) {
	return s.page, nil
}

func TestAudienceChannelReferenceUsesExactCodeOrUniqueName(t *testing.T) {
	resolver := audienceChannelReferenceAdapter{channels: audienceChannelCatalogStub{page: channelport.CatalogPage{Items: []channeldomain.Channel{
		{ID: 1, Code: "channel-v3", Config: channeldomain.Config{Name: "渠道标题"}},
		{ID: 2, Code: "channel-v4", Config: channeldomain.Config{Name: "另一渠道"}},
	}, Total: 2}}}
	for _, fixture := range []struct {
		value, want string
		found       bool
	}{
		{"渠道标题", "channel-v3", true},
		{"channel-v4", "channel-v4", true},
		{"不存在渠道", "", false},
	} {
		code, found, err := resolver.ResolveAudienceChannel(context.Background(), fixture.value)
		if err != nil || code != fixture.want || found != fixture.found {
			t.Fatalf("%q = (%q, %t, %v), want (%q, %t, nil)", fixture.value, code, found, err, fixture.want, fixture.found)
		}
	}
}

func TestAudienceChannelReferenceRejectsAmbiguousName(t *testing.T) {
	resolver := audienceChannelReferenceAdapter{channels: audienceChannelCatalogStub{page: channelport.CatalogPage{Items: []channeldomain.Channel{
		{ID: 1, Code: "channel-a", Config: channeldomain.Config{Name: "同名渠道"}},
		{ID: 2, Code: "channel-b", Config: channeldomain.Config{Name: "同名渠道"}},
	}, Total: 2}}}
	code, found, err := resolver.ResolveAudienceChannel(context.Background(), "同名渠道")
	if err != nil || found || code != "" {
		t.Fatalf("ambiguous name = (%q, %t, %v), want empty false nil", code, found, err)
	}
}

func TestAudienceChannelReferenceRejectsTruncatedDirectory(t *testing.T) {
	resolver := audienceChannelReferenceAdapter{channels: audienceChannelCatalogStub{page: channelport.CatalogPage{Items: []channeldomain.Channel{{ID: 1, Code: "channel-v3", Config: channeldomain.Config{Name: "渠道标题"}}}, Total: 101}}}
	code, found, err := resolver.ResolveAudienceChannel(context.Background(), "渠道标题")
	if err != nil || found || code != "" {
		t.Fatalf("truncated directory = (%q, %t, %v), want empty false nil", code, found, err)
	}
}

type audienceRadarDirectoryStub struct {
	page   radarport.LinkPage
	detail radarport.LinkDetail
}

func (s audienceRadarDirectoryStub) List(context.Context, radarport.ListQuery) (radarport.LinkPage, error) {
	return s.page, nil
}
func (s audienceRadarDirectoryStub) Get(context.Context, radar.RadarID) (radarport.LinkDetail, error) {
	return s.detail, nil
}

func TestAudienceRadarReferenceUsesStableIDOrUniqueTitle(t *testing.T) {
	resolver := audienceRadarReferenceAdapter{radars: audienceRadarDirectoryStub{
		page:   radarport.LinkPage{Items: []radarport.LinkSummary{{Link: radar.Link{ID: 88, Title: "雷达标题"}}, {Link: radar.Link{ID: 89, Title: "另一雷达"}}}, Total: 2},
		detail: radarport.LinkDetail{Link: radar.Link{ID: 88}},
	}}
	for _, fixture := range []struct {
		value, want string
		found       bool
	}{
		{"雷达标题", "88", true},
		{"88", "88", true},
		{"不存在雷达", "", false},
	} {
		id, found, err := resolver.ResolveAudienceRadar(context.Background(), fixture.value)
		if err != nil || id != fixture.want || found != fixture.found {
			t.Fatalf("%q = (%q, %t, %v), want (%q, %t, nil)", fixture.value, id, found, err, fixture.want, fixture.found)
		}
	}
}

func TestAudienceRadarReferenceRejectsAmbiguousTitle(t *testing.T) {
	resolver := audienceRadarReferenceAdapter{radars: audienceRadarDirectoryStub{page: radarport.LinkPage{Items: []radarport.LinkSummary{
		{Link: radar.Link{ID: 88, Title: "同名雷达"}},
		{Link: radar.Link{ID: 89, Title: "同名雷达"}},
	}, Total: 2}}}
	id, found, err := resolver.ResolveAudienceRadar(context.Background(), "同名雷达")
	if err != nil || found || id != "" {
		t.Fatalf("ambiguous title = (%q, %t, %v), want empty false nil", id, found, err)
	}
}

func TestAudienceRadarReferenceRejectsTruncatedDirectory(t *testing.T) {
	resolver := audienceRadarReferenceAdapter{radars: audienceRadarDirectoryStub{page: radarport.LinkPage{Items: []radarport.LinkSummary{{Link: radar.Link{ID: 88, Title: "雷达标题"}}}, Total: 101}}}
	id, found, err := resolver.ResolveAudienceRadar(context.Background(), "雷达标题")
	if err != nil || found || id != "" {
		t.Fatalf("truncated directory = (%q, %t, %v), want empty false nil", id, found, err)
	}
}
