package provider

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInvoiceOfficialPageSignatureCachesTicketAndDoesNotForceRefresh(t *testing.T) {
	calls := 0
	oa := &OfficialAccount{AppID: "wx-fixture", Secret: "synthetic-secret"}
	oa.Client = &http.Client{Transport: invoiceTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "api.weixin.qq.com" {
			t.Fatal("foreign OA destination")
		}
		body := ""
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var v map[string]any
			if r.Method != "POST" || r.URL.RawQuery != "" || json.NewDecoder(r.Body).Decode(&v) != nil || v["force_refresh"] != false || v["appid"] != "wx-fixture" || v["secret"] != "synthetic-secret" {
				t.Fatal("token request contract")
			}
			body = `{"access_token":"synthetic-token","expires_in":7200}`
		case "/cgi-bin/ticket/getticket":
			if r.Method != "GET" || r.URL.Query().Get("access_token") != "synthetic-token" || r.URL.Query().Get("type") != "jsapi" {
				t.Fatal("ticket request contract")
			}
			body = `{"errcode":0,"ticket":"synthetic-ticket","expires_in":7200}`
		default:
			t.Fatal("unexpected OA endpoint")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	for i := 0; i < 2; i++ {
		page := "https://crm.fixture/my/orders"
		v, e := oa.SignPage(context.Background(), page)
		sum := sha1.Sum([]byte(fmt.Sprintf("jsapi_ticket=synthetic-ticket&noncestr=%s&timestamp=%d&url=%s", v.Nonce, v.Timestamp, page)))
		if e != nil || v.AppID != "wx-fixture" || v.Signature != hex.EncodeToString(sum[:]) {
			t.Fatal("JS SDK signature contract", e)
		}
	}
	if calls != 2 {
		t.Fatal("ticket cache repeatedly refreshed provider token", calls)
	}
}
