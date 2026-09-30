package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBatchTagIDProjectionHydratesCompleteDetailsAndRejectsFailedDetail(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "detail_failed"}[failed], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":120}`))
				case "/cgi-bin/externalcontact/batch/get_by_user":
					_, _ = w.Write([]byte(`{"errcode":0,"external_contact_list":[{"external_contact":{"external_userid":"fixture-external"},"follow_info":{"userid":"one","tag_id":["company-tag","rule-tag"]}}]}`))
				case "/cgi-bin/externalcontact/get":
					if failed {
						_, _ = w.Write([]byte(`{"errcode":84061}`))
						return
					}
					_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"fixture-external","name":"完整资料"},"follow_user":[{"userid":"one","tags":[{"tag_id":"company-tag","tag_name":"企业标签","type":1},{"tag_id":"rule-tag","tag_name":"规则组标签","type":3},{"tag_name":"个人备注标签","type":2}]},{"userid":"two","tags":[]}]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "fixture-contact-secret"
			page, err := client.BatchExternalContacts(context.Background(), "one", "", 100)
			if failed {
				if err == nil || len(page.Contacts) != 0 {
					t.Fatal("failed detail published a partial page")
				}
				return
			}
			if err != nil || len(page.Contacts) != 1 || page.Contacts[0].Name != "完整资料" || len(page.Contacts[0].FollowInfo) != 2 {
				t.Fatalf("complete hydration failed: page=%+v err=%v", page, err)
			}
			follow := page.Contacts[0].FollowInfo[0]
			if !follow.TagsProjected || len(follow.Tags) != 2 || follow.Tags[0].Name != "企业标签" || follow.Tags[1].Type != 3 || len(follow.UnidentifiedTags) != 1 || follow.UnidentifiedTags[0].ProviderTagID != "" || follow.UnidentifiedTags[0].Name != "个人备注标签" || !page.Contacts[0].FollowInfo[1].TagsProjected {
				t.Fatalf("tag identity/presence lost: %+v", page.Contacts[0].FollowInfo)
			}
		})
	}
}
