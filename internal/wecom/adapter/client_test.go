package adapter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestClientOAuthURLsAndExchange(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.URL.Path+"?"+request.URL.RawQuery)
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpid") != "wx corp" || request.URL.Query().Get("corpsecret") != "secret value" {
				t.Fatalf("token query=%q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"access-token","expires_in":120}`))
		case "/cgi-bin/user/getuserinfo":
			if request.URL.Query().Get("access_token") != "access-token" || request.URL.Query().Get("code") != "provider code" {
				t.Fatalf("userinfo query=%q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"UserId":"employee"}`))
		default:
			t.Fatal("unexpected endpoint")
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	qr, err := client.AuthorizationURL(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "state+/", "")
	if err != nil {
		t.Fatal(err)
	}
	assertAuthorization(t, qr, "open.work.weixin.qq.com", "/wwopen/sso/qrConnect", map[string]string{"appid": "wx corp", "agentid": "10001", "redirect_uri": "https://crm.example/auth/wecom/callback", "state": "state+/"})
	web, err := client.AuthorizationURL(context.Background(), wecom.OAuthSidebar, wecom.OAuthModeWeb, "state+/", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(web, "#wechat_redirect") {
		t.Fatalf("web URL missing fragment: %s", web)
	}
	assertAuthorization(t, strings.TrimSuffix(web, "#wechat_redirect"), "open.weixin.qq.com", "/connect/oauth2/authorize", map[string]string{"appid": "wx corp", "redirect_uri": "https://crm.example/api/sidebar/oauth/callback", "state": "state+/", "response_type": "code", "scope": "snsapi_base"})
	identity, err := client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "provider code")
	if err != nil || identity != (wecom.OAuthIdentity{CorpID: "wx corp", EmployeeID: "employee"}) {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if len(seen) != 2 {
		t.Fatalf("calls=%v", seen)
	}
}

func TestClientSignsBothTicketsCachesAndRefreshes(t *testing.T) {
	now := testNow
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls[request.URL.Path]++
		query := request.URL.Query()
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if query.Get("corpid") != "wx corp" || query.Get("corpsecret") != "secret value" {
				t.Fatalf("token query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":120}`))
		case "/cgi-bin/get_jsapi_ticket":
			if query.Get("access_token") != "token" || query.Get("type") != "" {
				t.Fatalf("corp ticket query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"ticket":"corp-ticket","expires_in":120}`))
		case "/cgi-bin/ticket/get":
			if query.Get("access_token") != "token" || query.Get("type") != "agent_config" {
				t.Fatalf("agent ticket query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"ticket":"agent-ticket","expires_in":120}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return now })
	config, err := client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if config.CorpID != "wx corp" || config.AgentID != "10001" || config.Config.Timestamp != testNow.Unix() || config.Config.NonceStr != strings.Repeat("01", 16) || !reflect.DeepEqual(config.Config.JSAPIList, []string{"getCurExternalContact", "sendChatMessage"}) || !reflect.DeepEqual(config.AgentConfig.JSAPIList, []string{"getCurExternalContact", "sendChatMessage"}) {
		t.Fatalf("config=%+v", config)
	}
	assertSignature(t, config.Config.Signature, "corp-ticket", config.Config.NonceStr, config.Config.Timestamp, "https://crm.example/sidebar?x=1")
	assertSignature(t, config.AgentConfig.Signature, "agent-ticket", config.AgentConfig.NonceStr, config.AgentConfig.Timestamp, "https://crm.example/sidebar?x=1")
	if _, err = client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1"); err != nil {
		t.Fatal(err)
	}
	if calls["/cgi-bin/gettoken"] != 1 || calls["/cgi-bin/get_jsapi_ticket"] != 1 || calls["/cgi-bin/ticket/get"] != 1 {
		t.Fatalf("cache calls=%v", calls)
	}
	now = now.Add(61 * time.Second)
	if _, err = client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1"); err != nil {
		t.Fatal(err)
	}
	if calls["/cgi-bin/gettoken"] != 2 || calls["/cgi-bin/get_jsapi_ticket"] != 2 || calls["/cgi-bin/ticket/get"] != 2 {
		t.Fatalf("refresh calls=%v", calls)
	}
}

func TestClientJSSDKExplicitAPIListOverridesDefault(t *testing.T) {
	client := &Client{config: Config{JSAPIList: []string{"getCurExternalContact"}, Now: func() time.Time { return testNow }, Random: func(value []byte) error {
		for index := range value {
			value[index] = 1
		}
		return nil
	}}}
	signature, err := client.sign("https://crm.example/sidebar", "ticket")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(signature.JSAPIList, []string{"getCurExternalContact"}) {
		t.Fatalf("explicit jsApiList=%v", signature.JSAPIList)
	}
}

func TestClientGroupMessageUsesExactChatIDListAndRejectsPartialReceipt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("wrong token secret")
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			calls++
			var body map[string]any
			if json.NewDecoder(request.Body).Decode(&body) != nil {
				t.Fatal("invalid JSON request")
			}
			if body["chat_type"] != "group" || body["sender"] != "owner-1" || body["allow_select"] != false {
				t.Fatalf("body=%v", body)
			}
			ids, ok := body["chat_id_list"].([]any)
			if !ok || len(ids) != 1 || ids[0] != "chat-1" || body["chat_ids"] != nil {
				t.Fatalf("exact target body=%v", body)
			}
			if calls == 1 {
				_, _ = writer.Write([]byte(`{"errcode":0,"msgid":"task-1"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"msgid":"task-2","fail_list":["chat-1"]}`))
		default:
			t.Fatalf("unexpected endpoint=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	request := wecomport.GroupMessageRequest{SenderUserID: "owner-1", ChatIDs: []string{"chat-1"}, Text: "hello"}
	receipt, attempted, err := client.SendGroupMessage(context.Background(), request)
	if err != nil || !attempted || receipt.MessageID != "task-1" {
		t.Fatalf("receipt=%+v attempted=%t err=%v", receipt, attempted, err)
	}
	_, attempted, err = client.SendGroupMessage(context.Background(), request)
	if !attempted || err == nil {
		t.Fatalf("partial receipt attempted=%t err=%v", attempted, err)
	}
	if rejected, ok := err.(wecomport.GroupMessageSendError); !ok || rejected.OutcomeUnknown() {
		t.Fatalf("partial receipt classification=%T %v", err, err)
	}
}

func TestClientGroupDirectoryReadsUseScopedListThenDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("wrong token secret")
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/groupchat/list":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			filter, ok := body["owner_filter"].(map[string]any)
			ids, idsOK := filter["userid_list"].([]any)
			if !ok || !idsOK || len(ids) != 1 || ids[0] != "owner-1" || body["status_filter"] != float64(0) || body["cursor"] != "" || body["limit"] != float64(100) {
				t.Fatalf("list request=%v", body)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"group_chat_list":[{"chat_id":"chat-1","status":0}],"next_cursor":"cursor-2"}`))
		case "/cgi-bin/externalcontact/groupchat/get":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["chat_id"] != "chat-1" || body["need_name"] != float64(1) {
				t.Fatalf("detail request=%v", body)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"group_chat":{"chat_id":"chat-1","owner":"owner-1","name":"Group one","member_list":[{"userid":"u-1"}]}}`))
		default:
			t.Fatalf("unexpected endpoint=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListGroupChats(context.Background(), "owner-1", "", 100)
	if err != nil || len(page.Items) != 1 || page.Items[0].ChatID != "chat-1" || page.NextCursor != "cursor-2" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	detail, err := client.GetGroupChat(context.Background(), "chat-1")
	if err != nil || detail.ChatID != "chat-1" || detail.OwnerUserID != "owner-1" || detail.Name != "Group one" || detail.MemberCount != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestClientRejectsProviderErrorsOversizeAndDoesNotExposeSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cgi-bin/gettoken" {
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token-secret","expires_in":120}`))
			return
		}
		_, _ = writer.Write([]byte(strings.Repeat("x", maxResponseBody+1)))
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	_, err := client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "code-secret")
	if !errors.Is(err, ErrResponse) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "token-secret") || strings.Contains(err.Error(), "code-secret") || strings.Contains(err.Error(), "secret value") {
		t.Fatalf("secret leaked in error %q", err)
	}
	if _, err = New(Config{Enabled: true, CorpID: "c", AgentID: "a", Secret: "s", AdminCallbackURI: "http://bad", SidebarCallbackURI: "https://crm.example/callback"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bad config err=%v", err)
	}
	providerError := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cgi-bin/gettoken" {
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token-secret","expires_in":120}`))
			return
		}
		_, _ = writer.Write([]byte(`{"errcode":40029,"errmsg":"invalid code-secret"}`))
	}))
	defer providerError.Close()
	client = newTestClient(t, providerError, func() time.Time { return testNow })
	_, err = client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "code-secret")
	if !errors.Is(err, ErrResponse) || strings.Contains(err.Error(), "code-secret") {
		t.Fatalf("provider error=%v", err)
	}
}

func TestCustomerDirectoryProviderDisabledMakesZeroCalls(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client, err := New(Config{Enabled: false, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if client.DirectoryReady() {
		t.Fatal("disabled directory must not be ready")
	}
	if _, err = client.ListContactStaff(context.Background()); !errors.Is(err, wecomport.ErrDirectoryDisabled) {
		t.Fatalf("err=%v", err)
	}
	if _, err = client.BatchExternalContacts(context.Background(), "staff", "", 100); !errors.Is(err, wecomport.ErrDirectoryDisabled) {
		t.Fatalf("err=%v", err)
	}
	if calls != 0 {
		t.Fatalf("network calls=%d", calls)
	}
}

func TestDirectoryClientDoesNotRequireOAuthConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpid") != "corp" || request.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("token query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get_contact_way":
			_, _ = writer.Write([]byte(`{"errcode":0,"contact_way":{"config_id":"config-1","qr_code":"https://wework.qpic.cn/wwpic/example"}}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil || !client.DirectoryReady() {
		t.Fatalf("directory client ready=%v err=%v", client != nil && client.DirectoryReady(), err)
	}
	asset, err := client.GetContactWay(context.Background(), "config-1")
	if err != nil || asset.ProviderAssetRef != "config-1" || asset.URL == "" {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
	if _, err = client.AuthorizationURL(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "state", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("directory-only OAuth err=%v", err)
	}
}

func TestCustomerDirectoryProviderListsStaffAndBatchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact secret" {
				t.Fatalf("secret route=%q", request.URL.Query().Get("corpsecret"))
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get_follow_user_list":
			_, _ = writer.Write([]byte(`{"errcode":0,"follow_user":["staff-1","staff-1","staff-2"]}`))
		case "/cgi-bin/externalcontact/batch/get_by_user":
			if request.Method != http.MethodPost || request.URL.Query().Get("access_token") != "contact-token" {
				t.Fatalf("request=%s %s", request.Method, request.URL.String())
			}
			// Production pages with 100 contacts can legitimately exceed the old
			// 64 KiB OAuth-oriented limit. Unknown Provider fields must remain
			// safely ignored without making the response unbounded.
			payload := `{"errcode":0,"next_cursor":"next-1","external_contact_list":[{"external_contact":{"external_userid":"ext-1","name":"Alice","avatar":"https://example/avatar","type":1,"gender":2,"corp_name":"Example","unionid":"union-ignored-here"},"follow_info":{"userid":"staff-1","description":"manual note","tags":[{"tag_id":"tag-1","tag_name":"重点客户","type":1}]}}],"provider_padding":"` + strings.Repeat("x", 70<<10) + `"}`
			_, _ = writer.Write([]byte(payload))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	staff, err := client.ListContactStaff(context.Background())
	if err != nil || len(staff) != 2 {
		t.Fatalf("staff=%v err=%v", staff, err)
	}
	page, err := client.BatchExternalContacts(context.Background(), "staff-1", "cursor-1", 100)
	if err != nil || page.NextCursor != "next-1" || len(page.Contacts) != 1 || page.Contacts[0].ExternalUserID != "ext-1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if len(page.Contacts[0].FollowInfo) != 1 || page.Contacts[0].FollowInfo[0].EmployeeID != "staff-1" || page.Contacts[0].FollowInfo[0].Description == nil || *page.Contacts[0].FollowInfo[0].Description != "manual note" || len(page.Contacts[0].FollowInfo[0].Tags) != 1 || page.Contacts[0].FollowInfo[0].Tags[0].ProviderTagID != "tag-1" {
		t.Fatalf("follow info=%+v", page.Contacts[0].FollowInfo)
	}
}

func TestBatchExternalContactsKeepsNullDescriptionUnprojected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/batch/get_by_user":
			_, _ = writer.Write([]byte(`{"errcode":0,"external_contact_list":[{"external_contact":{"external_userid":"external-1"},"follow_info":{"userid":"staff-1","description":null,"tags":[]}}]}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.BatchExternalContacts(context.Background(), "staff-1", "", 1)
	if err != nil || len(page.Contacts) != 1 || len(page.Contacts[0].FollowInfo) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	follow := page.Contacts[0].FollowInfo[0]
	if follow.DescriptionProjected || follow.Description != nil {
		t.Fatalf("null description was projected: %+v", follow)
	}
}

func TestCustomerDirectoryProviderReadsBoundedStaffProfilesAndKeepsPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact secret" {
				t.Fatal("profile read did not use the contact credential")
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/user/get":
			if request.URL.Query().Get("access_token") != "contact-token" {
				t.Fatal("profile query did not use directory token")
			}
			switch request.URL.Query().Get("userid") {
			case "staff-1":
				_, _ = writer.Write([]byte(`{"errcode":0,"userid":"staff-1","name":"运营一"}`))
			case "staff-2":
				_, _ = writer.Write([]byte(`{"errcode":48002}`))
			default:
				t.Fatal("unexpected profile userid")
			}
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	snapshot, err := client.ReadContactStaffProfiles(context.Background(), []string{"staff-1", "staff-2"})
	if err != nil || len(snapshot.Items) != 1 || snapshot.Items[0] != (wecomport.ContactStaffProfile{UserID: "staff-1", DisplayName: "运营一"}) || snapshot.ProfileReadState != "unavailable" || snapshot.ProfileErrorCode != "provider_permission_denied" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if _, err = client.ReadContactStaffProfiles(context.Background(), make([]string, 101)); err == nil {
		t.Fatal("unbounded profile request accepted")
	}
}

func TestCustomerDirectoryProviderRejectsArrayFollowInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/batch/get_by_user":
			_, _ = writer.Write([]byte(`{"errcode":0,"external_contact_list":[{"external_contact":{"external_userid":"ext-1","type":1,"gender":2},"follow_info":[{"userid":"staff-1","tags":[]}]}]}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	_, err := client.BatchExternalContacts(context.Background(), "staff-1", "", 100)
	var failure wecomport.DirectoryFailure
	if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != "provider_response_invalid" || failure.DirectoryFailureRetryable() {
		t.Fatalf("err=%v failure=%v", err, failure)
	}
}

func TestCustomerDirectoryProviderClassifiesProviderReadFailures(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantCode      string
		wantRetryable bool
		wantMax       int
	}{
		{name: "permission", status: http.StatusOK, body: `{"errcode":48001}`, wantCode: "provider_permission_denied"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"errcode":45009}`, wantCode: "provider_rate_limited", wantRetryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"errcode":-1}`, wantCode: "provider_unavailable", wantRetryable: true},
		{name: "http 200 system busy", status: http.StatusOK, body: `{"errcode":-1}`, wantCode: "provider_unavailable", wantRetryable: true, wantMax: 3},
		{name: "invalid response", status: http.StatusOK, body: `{"errcode":0,"external_contact_list":[{"external_contact":{"external_userid":""}}]}`, wantCode: "provider_response_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/batch/get_by_user":
					writer.WriteHeader(test.status)
					_, _ = writer.Write([]byte(test.body))
				default:
					t.Fatalf("unexpected path=%s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			_, err := client.BatchExternalContacts(context.Background(), "staff-1", "", 100)
			var failure wecomport.DirectoryFailure
			if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != test.wantCode || failure.DirectoryFailureRetryable() != test.wantRetryable {
				t.Fatalf("err=%v failure=%v", err, failure)
			}
			var limited wecomport.DirectoryFailureAttemptLimit
			if !errors.As(err, &limited) || limited.DirectoryFailureMaxAttempts() != test.wantMax {
				t.Fatalf("attempt limit=%v; want %d", limited, test.wantMax)
			}
			if strings.Contains(err.Error(), "contact-token") {
				t.Fatalf("provider token leaked in error=%q", err)
			}
		})
	}
}

func TestCustomerDirectoryProviderRefreshesExpiredReadTokenOnce(t *testing.T) {
	for name, persistent := range map[string]bool{"recovers": false, "persistent": true} {
		t.Run(name, func(t *testing.T) {
			tokenCalls, readCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					tokenCalls++
					_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/get_follow_user_list":
					readCalls++
					if readCalls == 1 || persistent {
						_, _ = writer.Write([]byte(`{"errcode":40014}`))
						return
					}
					_, _ = writer.Write([]byte(`{"errcode":0,"follow_user":["staff-1"]}`))
				default:
					t.Fatalf("unexpected path=%s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			staff, err := client.ListContactStaff(context.Background())
			if !persistent {
				if err != nil || len(staff) != 1 || tokenCalls != 2 || readCalls != 2 {
					t.Fatalf("staff=%v err=%v tokens=%d reads=%d", staff, err, tokenCalls, readCalls)
				}
				return
			}
			var failure wecomport.DirectoryFailure
			if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != "provider_credentials_invalid" || failure.DirectoryFailureRetryable() || tokenCalls != 2 || readCalls != 2 {
				t.Fatalf("err=%v tokens=%d reads=%d", err, tokenCalls, readCalls)
			}
		})
	}
}

func TestListTagCatalogUsesNarrowPostAndPreservesProviderFacts(t *testing.T) {
	for name, response := range map[string]string{
		"missing":   `{"errcode":0}`,
		"null":      `{"errcode":0,"tag_group":null}`,
		"empty":     `{"errcode":0,"tag_group":[]}`,
		"tag-null":  `{"errcode":0,"tag_group":[{"group_id":"g","group_name":"group","order":-1,"tag":null}]}`,
		"raw-facts": `{"errcode":0,"tag_group":[{"group_id":"g","group_name":"group","order":1,"tag":[{"id":"dup","name":"one","order":2,"deleted":false},{"id":"dup","name":"gone","order":3,"deleted":true}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.Method != http.MethodGet || r.URL.Query().Get("corpsecret") != "secret" {
						t.Fatal("unexpected token request")
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/get_corp_tag_list":
					if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "token" {
						t.Fatal("unexpected catalog request")
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{}` {
						t.Fatalf("catalog body=%q", body)
					}
					_, _ = w.Write([]byte(response))
				default:
					t.Fatal("unexpected path")
				}
			}))
			defer server.Close()
			client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "1", Secret: "secret", AdminCallbackURI: "https://id-dev.youcangogogo.com/auth/wecom/callback", SidebarCallbackURI: "https://id-dev.youcangogogo.com/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			groups, err := client.ListTagCatalog(context.Background())
			if name == "missing" || name == "null" {
				var failure *CatalogReadError
				if err == nil || !errors.As(err, &failure) || !failure.CallAttempted {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "empty" && (groups == nil || len(groups) != 0) {
				t.Fatalf("groups=%#v", groups)
			}
			if name == "tag-null" && (len(groups) != 1 || groups[0].Tags == nil || len(groups[0].Tags) != 0 || groups[0].Order != -1) {
				t.Fatalf("groups=%#v", groups)
			}
			if name == "raw-facts" && (len(groups) != 1 || len(groups[0].Tags) != 2 || groups[0].Tags[1].Deleted != true) {
				t.Fatalf("groups=%#v", groups)
			}
		})
	}
}

func TestGetGroupMessageSendResultSendsFrozenUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("token query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/get_groupmsg_send_result":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["msgid"] != "msg-1" || body["userid"] != "frozen-sender" || body["cursor"] != "cursor-1" {
				t.Fatalf("result body=%+v", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"next_cursor":"cursor-2","send_list":[{"userid":"frozen-sender","chat_id":"chat-1","status":1}]}`))
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.GetGroupMessageSendResult(context.Background(), "msg-1", "frozen-sender", "cursor-1", 100)
	if err != nil || page.NextCursor != "cursor-2" || len(page.Items) != 1 || page.Items[0].SenderUserID != "frozen-sender" || page.Items[0].ChatID != "chat-1" || page.Items[0].Status != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestListTagCatalogTokenFailureIsPreCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/gettoken" {
			t.Fatal("catalog endpoint should not be called")
		}
		_, _ = w.Write([]byte(`{"errcode":40001}`))
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	_, err := client.ListTagCatalog(context.Background())
	var failure *CatalogReadError
	if err == nil || !errors.As(err, &failure) || failure.CallAttempted {
		t.Fatalf("err=%v failure=%+v", err, failure)
	}
}

func TestMutateTagCatalogUsesOfficialDirectoryWritesAndPreservesUnknownCreate(t *testing.T) {
	tests := []struct {
		name       string
		mutation   wecomport.TagCatalogMutation
		path       string
		response   string
		assertBody func(*testing.T, map[string]any)
		want       wecomport.TagCatalogMutationResult
		unknown    bool
	}{
		{
			name:     "group create",
			mutation: wecomport.TagCatalogMutation{Operation: "group_create", GroupName: "Lifecycle", TagName: "Warm"},
			path:     "/cgi-bin/externalcontact/add_corp_tag",
			response: `{"errcode":0,"tag_group":{"group_id":"group-created","group_name":"Lifecycle","tag":[{"id":"tag-created","name":"Warm"}]}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["group_name"] != "Lifecycle" || len(body["tag"].([]any)) != 1 {
					t.Fatalf("create body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderGroupID: "group-created", ProviderTagID: "tag-created"},
		},
		{
			name:     "tag create",
			mutation: wecomport.TagCatalogMutation{Operation: "tag_create", ProviderGroupID: "group-existing", TagName: "Hot"},
			path:     "/cgi-bin/externalcontact/add_corp_tag",
			response: `{"errcode":0,"tag_group":{"group_id":"group-existing","group_name":"Lifecycle","tag":[{"id":"tag-created","name":"Hot"}]}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["group_id"] != "group-existing" || len(body["tag"].([]any)) != 1 {
					t.Fatalf("create body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderGroupID: "group-existing", ProviderTagID: "tag-created"},
		},
		{
			name:     "group rename",
			mutation: wecomport.TagCatalogMutation{Operation: "group_update", ProviderGroupID: "group-existing", GroupName: "Lifecycle 2"},
			path:     "/cgi-bin/externalcontact/edit_corp_tag",
			response: `{"errcode":0}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["id"] != "group-existing" || body["name"] != "Lifecycle 2" {
					t.Fatalf("rename body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderGroupID: "group-existing"},
		},
		{
			name:     "tag rename",
			mutation: wecomport.TagCatalogMutation{Operation: "tag_update", ProviderTagID: "tag-existing", TagName: "Hot 2"},
			path:     "/cgi-bin/externalcontact/edit_corp_tag",
			response: `{"errcode":0}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["id"] != "tag-existing" || body["name"] != "Hot 2" {
					t.Fatalf("rename body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderTagID: "tag-existing"},
		},
		{
			name:     "group archive",
			mutation: wecomport.TagCatalogMutation{Operation: "group_archive", ProviderGroupID: "group-existing"},
			path:     "/cgi-bin/externalcontact/del_corp_tag",
			response: `{"errcode":0}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if got := body["group_id"].([]any); len(got) != 1 || got[0] != "group-existing" {
					t.Fatalf("archive body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderGroupID: "group-existing"},
		},
		{
			name:     "tag archive",
			mutation: wecomport.TagCatalogMutation{Operation: "tag_archive", ProviderTagID: "tag-existing"},
			path:     "/cgi-bin/externalcontact/del_corp_tag",
			response: `{"errcode":0}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if got := body["tag_id"].([]any); len(got) != 1 || got[0] != "tag-existing" {
					t.Fatalf("archive body=%#v", body)
				}
			},
			want: wecomport.TagCatalogMutationResult{ProviderTagID: "tag-existing"},
		},
		{
			name:     "ambiguous create response",
			mutation: wecomport.TagCatalogMutation{Operation: "group_create", GroupName: "Lifecycle", TagName: "Warm"},
			path:     "/cgi-bin/externalcontact/add_corp_tag",
			response: `{"errcode":0,"tag_group":{}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["group_name"] != "Lifecycle" {
					t.Fatalf("create body=%#v", body)
				}
			},
			unknown: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.URL.Query().Get("corpsecret") != "contact-secret" {
						t.Fatalf("token query=%s", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case test.path:
					if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "contact-token" {
						t.Fatalf("write request=%s?%s", r.Method, r.URL.RawQuery)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					test.assertBody(t, body)
					_, _ = w.Write([]byte(test.response))
				default:
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.MutateTagCatalog(context.Background(), test.mutation)
			if test.unknown {
				if err == nil || !wecomport.ProviderCallAttempted(err) || !wecomport.ProviderOutcomeUnknown(err) {
					t.Fatalf("ambiguous create err=%v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("result=%+v err=%v want=%+v", got, err, test.want)
			}
		})
	}
}

func TestChannelContactWayLifecycleAndWelcomeAttachments(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/cgi-bin/gettoken" {
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/cgi-bin/externalcontact/get_contact_way":
			if !strings.Contains(string(body), `"config_id":"cw-1"`) {
				t.Fatalf("get body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"contact_way":{"config_id":"cw-1","qr_code":"https://wework.qpic.cn/wwpic/1"}}`))
		case "/cgi-bin/externalcontact/update_contact_way":
			if !strings.Contains(string(body), `"config_id":"cw-1"`) || !strings.Contains(string(body), `"state":"campaign"`) {
				t.Fatalf("update body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/del_contact_way":
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/send_welcome_msg":
			if !strings.Contains(string(body), `"msgtype":"image"`) || !strings.Contains(string(body), `"media_id":"media-1"`) || !strings.Contains(string(body), `"msgtype":"link"`) || !strings.Contains(string(body), `"url":"https://work.weixin.qq.com/gm/`) {
				t.Fatalf("welcome body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	request := wecomport.AcquisitionAssetRequest{Name: "Campaign", State: "campaign", StaffUserIDs: []string{"staff-1"}}
	updated, err := client.UpdateContactWay(context.Background(), "cw-1", request)
	if err != nil || updated.ProviderAssetRef != "cw-1" || calls["/cgi-bin/externalcontact/update_contact_way"] != 1 || calls["/cgi-bin/externalcontact/get_contact_way"] != 1 {
		t.Fatalf("updated=%+v calls=%v err=%v", updated, calls, err)
	}
	if err = client.SendWelcomeMessage(context.Background(), "welcome-code", "欢迎", []wecomport.WelcomeAttachment{{MsgType: "image", MediaID: "media-1"}, {MsgType: "link", Title: "入群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"}}); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteContactWay(context.Background(), "cw-1"); err != nil || calls["/cgi-bin/externalcontact/del_contact_way"] != 1 {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}

func TestCreateContactWayUsesQRCodeContractAndSecureProviderURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/add_contact_way":
			var body struct {
				Type   int      `json:"type"`
				Scene  int      `json:"scene"`
				Remark string   `json:"remark"`
				State  string   `json:"state"`
				User   []string `json:"user"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Type != 1 || body.Scene != 2 || len([]rune(body.Remark)) != 30 || len(body.State) != 30 || len(body.User) != 1 {
				t.Fatalf("contact-way request=%+v", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"config_id":"cw-1","qr_code":"http://p.qpic.cn/wwhead/example/0"}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	result, err := client.CreateContactWay(context.Background(), wecomport.AcquisitionAssetRequest{
		Name: strings.Repeat("中", 31), State: strings.Repeat("a", 30), StaffUserIDs: []string{"staff-1"},
	})
	if err != nil || result.ProviderAssetRef != "cw-1" || result.URL != "https://p.qpic.cn/wwhead/example/0" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if contactWayType([]string{"one", "two"}) != 2 {
		t.Fatal("multiple staff must use multi-user contact-way type")
	}
	for _, raw := range []string{"http://example.com/qr", "http://p.qpic.cn:8080/qr", "https://p.qpic.cn.evil.test/qr"} {
		if _, valid := normalizeContactWayQR(raw); valid {
			t.Fatalf("unsafe QR URL accepted: %q", raw)
		}
	}
}

func TestSendWelcomeMessagePreflightFailureIsNotAttempted(t *testing.T) {
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.SendWelcomeMessage(context.Background(), "welcome-code", "hello", []wecomport.WelcomeAttachment{{MsgType: "image"}})
	if err == nil || wecomport.ProviderCallAttempted(err) || wecomport.ProviderOutcomeUnknown(err) {
		t.Fatalf("preflight err=%v attempted=%t unknown=%t", err, wecomport.ProviderCallAttempted(err), wecomport.ProviderOutcomeUnknown(err))
	}
}

func TestSendWelcomeMessageClassifiesOnlyStrictNonzeroErrcodeAsRejected(t *testing.T) {
	cases := []struct {
		name     string
		response string
		accepted bool
		rejected bool
		code     int64
	}{
		{name: "accepted strict zero", response: `{"errcode":0}`, accepted: true},
		{name: "rejected strict nonzero", response: `{"errcode":40003,"errmsg":"ignored"}`, rejected: true, code: 40003},
		{name: "rejected negative numeric", response: `{"errcode":-1}`, rejected: true, code: -1},
		{name: "missing errcode is unknown", response: `{}`},
		{name: "null errcode is unknown", response: `{"errcode":null}`},
		{name: "string errcode is unknown", response: `{"errcode":"0"}`},
		{name: "fractional errcode is unknown", response: `{"errcode":0.0}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.URL.Query().Get("corpsecret") != "contact-secret" {
						t.Fatalf("welcome token did not use contact secret")
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/send_welcome_msg":
					_, _ = w.Write([]byte(test.response))
				default:
					t.Fatalf("unexpected endpoint=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			err = client.SendWelcomeMessage(context.Background(), "welcome-code", "hello", nil)
			if test.accepted {
				if err != nil {
					t.Fatalf("accepted welcome err=%v", err)
				}
				return
			}
			if err == nil || !wecomport.ProviderCallAttempted(err) {
				t.Fatalf("welcome err=%v attempted=%t", err, wecomport.ProviderCallAttempted(err))
			}
			if test.rejected {
				code, ok := wecomport.ProviderErrorCode(err)
				if wecomport.ProviderOutcomeUnknown(err) || !ok || code != test.code {
					t.Fatalf("rejected err=%v unknown=%t code=%d/%t", err, wecomport.ProviderOutcomeUnknown(err), code, ok)
				}
				return
			}
			if !wecomport.ProviderOutcomeUnknown(err) {
				t.Fatalf("ambiguous response became final err=%v", err)
			}
			if _, ok := wecomport.ProviderErrorCode(err); ok {
				t.Fatal("ambiguous response exposed a Provider error code")
			}
		})
	}
}

func TestPrivateMessageUsesSingleCustomerContractAndRejectsFailList(t *testing.T) {
	for name, providerResponse := range map[string]string{
		"accepted":        `{"errcode":0,"msgid":"msg-1","fail_list":[]}`,
		"target rejected": `{"errcode":0,"msgid":"msg-1","fail_list":["external-secret-id"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.URL.Query().Get("corpsecret") != "contact secret" {
						t.Fatal("private message did not use contact secret")
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/media/upload":
					if r.URL.Query().Get("type") != "image" || !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
						t.Fatalf("upload request=%s", r.URL.String())
					}
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), "Content-Type: image/png") || !strings.Contains(string(raw), "Content-Disposition: form-data;") || !strings.Contains(string(raw), "filename=image.png") || !strings.Contains(string(raw), "name=media") {
						t.Fatalf("multipart headers=%q", raw)
					}
					_, _ = w.Write([]byte(`{"errcode":0,"media_id":"media-1"}`))
				case "/cgi-bin/externalcontact/add_msg_template":
					raw, _ := io.ReadAll(r.Body)
					var body map[string]any
					if json.Unmarshal(raw, &body) != nil || body["chat_type"] != "single" || body["sender"] != "staff-secret-id" {
						t.Fatalf("message body=%s", raw)
					}
					targets, _ := body["external_userid"].([]any)
					if len(targets) != 1 || targets[0] != "external-secret-id" {
						t.Fatalf("targets=%v", targets)
					}
					_, _ = w.Write([]byte(providerResponse))
				default:
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			receipt, attempted, err := client.SendPrivateMessage(context.Background(), outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}, outboundport.PrivateMessagePayload{Text: "hello", Attachments: []outboundport.PrivateMessageAttachment{{Kind: "image", Content: []byte("png-image-bytes"), FileName: "image.png", MediaType: "image/png"}}})
			if !attempted || len(calls) != 3 {
				t.Fatalf("attempted=%v calls=%v err=%v", attempted, calls, err)
			}
			if name == "accepted" {
				if err != nil || receipt.MessageID != "msg-1" {
					t.Fatalf("receipt=%+v err=%v", receipt, err)
				}
				return
			}
			var failure outboundport.PrivateMessageSendError
			if err == nil || !errors.As(err, &failure) || failure.OutcomeUnknown() {
				t.Fatalf("known target rejection err=%v", err)
			}
		})
	}
}

func TestPrivateMessageUploadsPDFAsFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/media/upload":
			if r.URL.Query().Get("type") != "file" || !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("file upload request=%s", r.URL.String())
			}
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "Content-Type: application/pdf") || !strings.Contains(string(raw), "filename=guide.pdf") {
				t.Fatalf("file multipart=%q", raw)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"media_id":"file-media-1"}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			attachments, _ := body["attachments"].([]any)
			if len(attachments) != 1 {
				t.Fatalf("attachments=%v", attachments)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"msgid":"msg-file-1","fail_list":[]}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	receipt, attempted, err := client.SendPrivateMessage(context.Background(), outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}, outboundport.PrivateMessagePayload{Attachments: []outboundport.PrivateMessageAttachment{{Kind: "file", Content: []byte("%PDF-1.7 test"), FileName: "guide.pdf", MediaType: "application/pdf"}}})
	if err != nil || !attempted || receipt.MessageID != "msg-file-1" {
		t.Fatalf("receipt=%+v attempted=%t err=%v", receipt, attempted, err)
	}
}

func TestPrivateMessageRateLimitIsRetryableAndCanLaterSucceed(t *testing.T) {
	messageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			messageCalls++
			if messageCalls == 1 {
				_, _ = w.Write([]byte(`{"errcode":45009,"errmsg":"rate limited"}`))
				return
			}
			_, _ = w.Write([]byte(`{"errcode":0,"msgid":"retry-message-1","fail_list":[]}`))
		default:
			t.Fatalf("unexpected provider call %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	target := outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}
	payload := outboundport.PrivateMessagePayload{Attachments: []outboundport.PrivateMessageAttachment{{Kind: "image", MediaID: "prepared-media-1"}}}
	_, attempted, err := client.SendPrivateMessage(context.Background(), target, payload)
	var classified outboundport.PrivateMessageRetryableRejection
	if !attempted || err == nil || !errors.As(err, &classified) || classified.OutcomeUnknown() || !classified.Retryable() || classified.FailureCode() != "wecom_errcode_45009" {
		t.Fatalf("rate-limit attempted=%t err=%v classified=%T", attempted, err, classified)
	}
	receipt, attempted, err := client.SendPrivateMessage(context.Background(), target, payload)
	if err != nil || !attempted || receipt.MessageID != "retry-message-1" || messageCalls != 2 {
		t.Fatalf("retry receipt=%+v attempted=%t calls=%d err=%v", receipt, attempted, messageCalls, err)
	}
}

var testNow = time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)

func newTestClient(t *testing.T, server *httptest.Server, now func() time.Time) *Client {
	t.Helper()
	client, err := New(Config{Enabled: true, CorpID: "wx corp", AgentID: "10001", Secret: "secret value", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client(), Now: now, Random: func(value []byte) error {
		for i := range value {
			value[i] = 1
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertAuthorization(t *testing.T, raw, host, path string, want map[string]string) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != host || parsed.Path != path {
		t.Fatalf("url=%s", raw)
	}
	for key, value := range want {
		if parsed.Query().Get(key) != value {
			t.Fatalf("%s=%q want %q URL=%s", key, parsed.Query().Get(key), value, raw)
		}
	}
}

func assertSignature(t *testing.T, signature, ticket, nonce string, timestamp int64, signedURL string) {
	t.Helper()
	plain := "jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + strconvFormat(timestamp) + "&url=" + signedURL
	sum := sha1.Sum([]byte(plain))
	if signature != hex.EncodeToString(sum[:]) {
		t.Fatalf("signature=%s want=%x", signature, sum)
	}
}

func strconvFormat(value int64) string { return strconv.FormatInt(value, 10) }

func TestClientMarkContactTagsClassifiesDefiniteRejectionAndDisconnect(t *testing.T) {
	for _, fixture := range []struct {
		name               string
		mark               func(http.ResponseWriter, *http.Request)
		unknown, retryable bool
	}{
		{name: "provider business rejection", mark: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"errcode":40003,"errmsg":"invalid user"}`))
		}, unknown: false, retryable: false},
		{name: "disconnect after request", mark: func(w http.ResponseWriter, r *http.Request) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
		}, unknown: true, retryable: false},
		{name: "missing errcode is unknown", mark: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }, unknown: true, retryable: false},
		{name: "null errcode is unknown", mark: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"errcode":null}`)) }, unknown: true, retryable: false},
		{name: "string errcode is unknown", mark: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"errcode":"0"}`)) }, unknown: true, retryable: false},
		{name: "malformed success body is unknown", mark: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) }, unknown: true, retryable: false},
		{name: "gateway html is unknown after request", mark: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<html>gateway</html>`))
		}, unknown: true, retryable: false},
		{name: "gateway empty json is unknown after request", mark: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{}`))
		}, unknown: true, retryable: false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
				case "/cgi-bin/externalcontact/mark_tag":
					fixture.mark(w, r)
				default:
					t.Fatalf("unexpected endpoint=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			err = client.MarkContactTags(context.Background(), "staff-1", "external-1", []string{"tag-1"}, nil)
			if err == nil || !wecomport.ProviderCallAttempted(err) || wecomport.ProviderOutcomeUnknown(err) != fixture.unknown || wecomport.ProviderRetryable(err) != fixture.retryable {
				t.Fatalf("err=%T %v attempted=%t unknown=%t retryable=%t", err, err, wecomport.ProviderCallAttempted(err), wecomport.ProviderOutcomeUnknown(err), wecomport.ProviderRetryable(err))
			}
		})
	}
}

func TestClientReadExternalContactUsesDirectoryReadCredentialAndReturnsFollowTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("contact secret query=%q", r.URL.Query().Get("corpsecret"))
			}
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get":
			if r.URL.Query().Get("access_token") != "contact-token" || r.URL.Query().Get("external_userid") != "external-1" {
				t.Fatalf("read query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"external-1","name":"Contact","avatar":"https://avatar.example/1","type":1,"gender":2,"corp_name":"Example"},"follow_user":[{"userid":"staff-1","description":"operator note","tags":[{"tag_id":"tag-1","name":"Tag one","type":1}]}]}`))
		default:
			t.Fatalf("unexpected endpoint=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	contact, err := client.ReadExternalContact(context.Background(), "external-1")
	if err != nil || contact.ExternalUserID != "external-1" || len(contact.FollowInfo) != 1 || contact.FollowInfo[0].EmployeeID != "staff-1" || contact.FollowInfo[0].Description == nil || *contact.FollowInfo[0].Description != "operator note" || len(contact.FollowInfo[0].Tags) != 1 || contact.FollowInfo[0].Tags[0].ProviderTagID != "tag-1" {
		t.Fatalf("contact=%+v err=%v", contact, err)
	}
}

func TestClientReadExternalContactDescriptionTargetProjectsOnlyRequestedRelationship(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		want        wecomport.ExternalContactDescriptionTarget
		wantFailure bool
	}{
		{
			name:     "ignores unrelated and target malformed tags",
			response: `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"other","tags":[{"tag_id":7,"type":0}],"description":{}},{"userid":"staff-1","description":"manual\nnote","tags":[{"tag_id":"","type":0}]}]}`,
			want:     wecomport.ExternalContactDescriptionTarget{Description: "manual\nnote", Projected: true},
		},
		{
			name:     "explicit empty description is projected",
			response: `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":""}]}`,
			want:     wecomport.ExternalContactDescriptionTarget{Description: "", Projected: true},
		},
		{
			name:     "missing description is not projected",
			response: `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1"}]}`,
		},
		{
			name:     "null description is not projected",
			response: `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":null}]}`,
		},
		{
			name:        "wrong external contact",
			response:    `{"errcode":0,"external_contact":{"external_userid":"external-other"},"follow_user":[{"userid":"staff-1","description":"manual"}]}`,
			wantFailure: true,
		},
		{
			name:        "target relationship absent",
			response:    `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"other","description":"manual"}]}`,
			wantFailure: true,
		},
		{
			name:        "target relationship duplicated",
			response:    `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":"one"},{"userid":"staff-1","description":"two"}]}`,
			wantFailure: true,
		},
		{
			name:        "target description non string",
			response:    `{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":{}}]}`,
			wantFailure: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
				case "/cgi-bin/externalcontact/get":
					if r.URL.Query().Get("access_token") != "contact-token" || r.URL.Query().Get("external_userid") != "external-1" {
						t.Fatalf("read query=%s", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(testCase.response))
				default:
					t.Fatalf("unexpected endpoint=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.ReadExternalContactDescriptionTarget(context.Background(), "external-1", "staff-1")
			if testCase.wantFailure {
				var failure wecomport.DirectoryFailure
				if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != "provider_response_invalid" || failure.DirectoryFailureRetryable() {
					t.Fatalf("target=%+v err=%v", got, err)
				}
				return
			}
			if err != nil || got != testCase.want {
				t.Fatalf("target=%+v want=%+v err=%v", got, testCase.want, err)
			}
		})
	}
}

func TestClientReadExternalContactDescriptionTargetRefreshesOnceAndKeepsErrorClassification(t *testing.T) {
	t.Run("refreshes expired token once", func(t *testing.T) {
		tokenCalls, readCalls := 0, 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/cgi-bin/gettoken":
				tokenCalls++
				_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
			case "/cgi-bin/externalcontact/get":
				readCalls++
				if readCalls == 1 {
					_, _ = w.Write([]byte(`{"errcode":40014}`))
					return
				}
				_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":"manual"}]}`))
			default:
				t.Fatalf("unexpected endpoint=%s", r.URL.Path)
			}
		}))
		defer server.Close()
		client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		got, err := client.ReadExternalContactDescriptionTarget(context.Background(), "external-1", "staff-1")
		if err != nil || got.Description != "manual" || !got.Projected || tokenCalls != 2 || readCalls != 2 {
			t.Fatalf("target=%+v err=%v tokens=%d reads=%d", got, err, tokenCalls, readCalls)
		}
	})

	for _, testCase := range []struct {
		name          string
		response      string
		wantCode      string
		wantRetryable bool
	}{
		{name: "permission error", response: `{"errcode":48002}`, wantCode: "provider_permission_denied"},
		{name: "rate limited", response: `{"errcode":45009}`, wantCode: "provider_rate_limited", wantRetryable: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
				case "/cgi-bin/externalcontact/get":
					_, _ = w.Write([]byte(testCase.response))
				default:
					t.Fatalf("unexpected endpoint=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ReadExternalContactDescriptionTarget(context.Background(), "external-1", "staff-1")
			var failure wecomport.DirectoryFailure
			if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != testCase.wantCode || failure.DirectoryFailureRetryable() != testCase.wantRetryable {
				t.Fatalf("err=%v failure=%v", err, failure)
			}
		})
	}
}

func TestClientReadExternalContactKeepsStrictTagValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get":
			_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"external-1"},"follow_user":[{"userid":"staff-1","description":"manual","tags":[{"tag_id":7,"type":0}]}]}`))
		default:
			t.Fatalf("unexpected endpoint=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ReadExternalContact(context.Background(), "external-1")
	var failure wecomport.DirectoryFailure
	if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != "provider_response_invalid" {
		t.Fatalf("err=%v failure=%v", err, failure)
	}
}

func TestClientUpdatesExternalContactDescriptionThroughRemarkEndpoint(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("contact secret query=%q", r.URL.Query().Get("corpsecret"))
			}
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/remark":
			calls++
			if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "contact-token" {
				t.Fatalf("request=%s %s", r.Method, r.URL.String())
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["userid"] != "staff-1" || body["external_userid"] != "external-1" || body["description"] != "manual note\nexternal-1" {
				t.Fatalf("body=%v", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0}`))
		default:
			t.Fatalf("unexpected endpoint=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.UpdateExternalContactDescription(context.Background(), wecomport.ExternalContactDescriptionUpdate{EmployeeID: "staff-1", ExternalUserID: "external-1", Description: "manual note\nexternal-1"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestExternalContactDescriptionRetainsHistoricalValueOverWriteLimit(t *testing.T) {
	value := strings.Repeat("企", 151)
	raw, _ := json.Marshal(value)
	if got, projected, err := externalContactDescription(raw); err != nil || !projected || got == nil || *got != value {
		t.Fatalf("description=%v projected=%t err=%v", got, projected, err)
	}
	if validExternalContactDescription(value) {
		t.Fatal("151-rune description accepted")
	}
	if !validExternalContactDescription(strings.Repeat("企", 150)) {
		t.Fatal("150-rune description rejected")
	}
}

func TestExternalContactDescriptionDistinguishesExplicitEmptyFromOmitted(t *testing.T) {
	empty := json.RawMessage(`""`)
	value, projected, err := externalContactDescription(empty)
	if err != nil || !projected || value == nil || *value != "" {
		t.Fatalf("value=%v projected=%t err=%v", value, projected, err)
	}
	value, projected, err = externalContactDescription(nil)
	if err != nil || projected || value != nil {
		t.Fatalf("value=%v projected=%t err=%v", value, projected, err)
	}
	value, projected, err = externalContactDescription(json.RawMessage(`null`))
	if err != nil || projected || value != nil {
		t.Fatalf("null value=%v projected=%t err=%v", value, projected, err)
	}
}
func TestClientCustomerTransferUsesExactFrozenIDsAndRejectsOmittedRows(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatal("wrong contact secret endpoint")
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/transfer_customer":
			calls++
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["handover_userid"] != "source-user" || body["takeover_userid"] != "target-user" || body["transfer_success_msg"] != "您好" {
				t.Fatalf("transfer body=%v", body)
			}
			ids, ok := body["external_userid"].([]any)
			if !ok || len(ids) != 1 || ids[0] != "external-1" {
				t.Fatalf("transfer ids=%v", body)
			}
			if calls == 1 {
				_, _ = writer.Write([]byte(`{"errcode":0,"customer":[{"external_userid":"external-1","errcode":0}]}`))
				return
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"customer":[{"external_userid":"external-1"}]}`))
		case "/cgi-bin/externalcontact/transfer_result":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["handover_userid"] != "source-user" || body["takeover_userid"] != "target-user" || body["cursor"] != "cursor-1" {
				t.Fatalf("result body=%v", body)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"customer":[{"external_userid":"external-2","status":1,"takeover_time":1588262400},{"external_userid":"external-3","status":2,"takeover_time":1588482400},{"external_userid":"external-4","status":3,"takeover_time":0}],"next_cursor":"cursor-2"}`))
		default:
			t.Fatalf("unexpected endpoint=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := client.TransferCustomer(context.Background(), "source-user", "target-user", []string{"external-1"}, "您好")
	if err != nil || len(accepted.AcceptedExternalUserIDs) != 1 || accepted.AcceptedExternalUserIDs[0] != "external-1" || accepted.FailedCount != 0 {
		t.Fatalf("transfer=%+v err=%v", accepted, err)
	}
	if _, err = client.TransferCustomer(context.Background(), "source-user", "target-user", []string{"external-1"}, "您好"); err == nil {
		t.Fatal("omitted provider result was accepted")
	}
	observed, err := client.TransferResult(context.Background(), "source-user", "target-user", "cursor-1")
	if err != nil || observed.Cursor != "cursor-2" || len(observed.AcceptedExternalUserIDs) != 0 || len(observed.Observations) != 3 || observed.Observations[0].Status != 1 || observed.Observations[1].Status != 2 || observed.Observations[2].Status != 3 {
		t.Fatalf("result=%+v err=%v", observed, err)
	}
}

func TestTransferCustomerTreatsAmbiguousTopLevelResponsesAsOutcomeUnknown(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "gateway html", status: http.StatusBadGateway, body: "<html>bad gateway</html>"},
		{name: "server empty object", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "success malformed json", status: http.StatusOK, body: `{`},
		{name: "success missing errcode", status: http.StatusOK, body: `{"customer":[{"external_userid":"external-1","errcode":0}]}`},
		{name: "success null errcode", status: http.StatusOK, body: `{"errcode":null,"customer":[{"external_userid":"external-1","errcode":0}]}`},
		{name: "success string zero errcode", status: http.StatusOK, body: `{"errcode":"0","customer":[{"external_userid":"external-1","errcode":0}]}`},
		{name: "success fractional zero errcode", status: http.StatusOK, body: `{"errcode":0.0,"customer":[{"external_userid":"external-1","errcode":0}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/transfer_customer":
					writer.WriteHeader(test.status)
					_, _ = writer.Write([]byte(test.body))
				default:
					t.Fatalf("unexpected endpoint %s", request.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "secret", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.TransferCustomer(context.Background(), "source", "target", []string{"external-1"}, "")
			if err == nil || !wecomport.ProviderCallAttempted(err) || !wecomport.ProviderOutcomeUnknown(err) {
				t.Fatalf("err=%v attempted=%t unknown=%t", err, wecomport.ProviderCallAttempted(err), wecomport.ProviderOutcomeUnknown(err))
			}
		})
	}
}

func TestTransferCustomerTreatsStrictNonZeroTopLevelErrcodeAsFinalRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/transfer_customer":
			_, _ = writer.Write([]byte(`{"errcode":40003,"errmsg":"invalid"}`))
		default:
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.TransferCustomer(context.Background(), "source", "target", []string{"external-1"}, "")
	if err == nil || !wecomport.ProviderCallAttempted(err) || wecomport.ProviderOutcomeUnknown(err) {
		t.Fatalf("err=%v attempted=%t unknown=%t", err, wecomport.ProviderCallAttempted(err), wecomport.ProviderOutcomeUnknown(err))
	}
}

func TestPrivateMessageUsesPreparedMediaIDWithoutUpload(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_msg_template":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			attachments, _ := body["attachments"].([]any)
			image, _ := attachments[0].(map[string]any)["image"].(map[string]any)
			if image["media_id"] != "prepared-media-1" {
				t.Fatalf("prepared material was not used: %#v", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"msgid":"message-1","fail_list":[]}`))
		default:
			t.Fatalf("unexpected provider call %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	receipt, attempted, err := client.SendPrivateMessage(context.Background(), outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}, outboundport.PrivateMessagePayload{Attachments: []outboundport.PrivateMessageAttachment{{Kind: "image", MediaID: "prepared-media-1"}}})
	if err != nil || !attempted || receipt.MessageID != "message-1" || calls != 2 {
		t.Fatalf("receipt=%+v attempted=%v calls=%d err=%v", receipt, attempted, calls, err)
	}
}

func TestPrivateMessageRejectsPreparedMediaWithBytes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/cgi-bin/gettoken" {
			t.Fatalf("bytes must not reach an upload or message endpoint: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	_, attempted, err := client.SendPrivateMessage(context.Background(), outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}, outboundport.PrivateMessagePayload{Attachments: []outboundport.PrivateMessageAttachment{{Kind: "image", MediaID: "prepared-media-1", Content: []byte("must not upload")}}})
	if err == nil || attempted || calls != 1 {
		t.Fatalf("prepared material with bytes attempted=%v calls=%d err=%v", attempted, calls, err)
	}
}
