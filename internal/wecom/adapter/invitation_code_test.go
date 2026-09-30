package adapter

import (
	"context"
	"encoding/json"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvitationCodeBindsOneGroupAndPreservesUnknownReceipt(t *testing.T) {
	failRead := false
	staleRead := false
	adds := 0
	boundChats := []string{"test-group"}
	server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			out.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/groupchat/add_join_way":
			adds++
			var body struct {
				Scene int      `json:"scene"`
				Auto  int      `json:"auto_create_room"`
				IDs   []string `json:"chat_id_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Scene != 2 || body.Auto != 0 || len(body.IDs) != 1 || body.IDs[0] != "test-group" {
				t.Errorf("incorrect single-group request %+v %v", body, err)
			}
			out.Write([]byte(`{"errcode":0,"config_id":"config-1"}`))
		case "/cgi-bin/externalcontact/groupchat/update_join_way":
			var body struct {
				ConfigID string   `json:"config_id"`
				Scene    int      `json:"scene"`
				Auto     int      `json:"auto_create_room"`
				IDs      []string `json:"chat_id_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ConfigID != "config-1" || body.Scene != 2 || body.Auto != 0 || len(body.IDs) != 2 {
				t.Errorf("incorrect update request %+v %v", body, err)
			}
			boundChats = body.IDs
			out.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/groupchat/get_join_way":
			if failRead {
				out.WriteHeader(503)
				return
			}
			ids := boundChats
			if staleRead {
				ids = []string{"test-group"}
			}
			_ = json.NewEncoder(out).Encode(map[string]any{"errcode": 0, "join_way": map[string]any{"config_id": "config-1", "scene": 2, "auto_create_room": 0, "chat_id_list": ids, "qr_code": "https://wework.qpic.cn/code"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			out.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "fixture-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	code, err := client.CreateInvitationCode(context.Background(), "test-group")
	if err != nil || code.ConfigID != "config-1" || code.QRCode == "" || adds != 1 {
		t.Fatal(code, err, adds)
	}
	failRead = true
	code, err = client.CreateInvitationCode(context.Background(), "test-group")
	if err == nil || !w.ProviderCallAttempted(err) || code.ConfigID != "config-1" || adds != 2 {
		t.Fatal("must preserve accepted config for reconciliation", code, err, adds)
	}
	failRead = false
	code, err = client.UpdateInvitationCodeForGroups(context.Background(), "config-1", []string{"test-group", "next-group"})
	if err != nil || code.ConfigID != "config-1" || code.QRCode == "" {
		t.Fatal("stable join-way update", code, err)
	}
	staleRead = true
	code, err = client.UpdateInvitationCodeForGroups(context.Background(), "config-1", []string{"test-group", "next-group"})
	if err == nil || !w.ProviderCallAttempted(err) || code.ConfigID != "config-1" {
		t.Fatal("stale group readback must remain unresolved", code, err)
	}
}

func TestNativeInvitationWritesAndReadsAllOfficialOptions(t *testing.T) {
	calls := 0
	var saved map[string]any
	corrupt := false
	qrURL := "https://wework.qpic.cn/native-code"
	server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			out.Write([]byte(`{"errcode":0,"access_token":"fixture","expires_in":7200}`))
		case "/cgi-bin/externalcontact/groupchat/add_join_way", "/cgi-bin/externalcontact/groupchat/update_join_way":
			calls++
			if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
				t.Error(err)
			}
			out.Write([]byte(`{"errcode":0,"config_id":"native-config"}`))
		case "/cgi-bin/externalcontact/groupchat/get_join_way":
			copy := map[string]any{}
			for k, v := range saved {
				copy[k] = v
			}
			copy["config_id"] = "native-config"
			copy["qr_code"] = qrURL
			if corrupt {
				copy["auto_create_room"] = 0
			}
			json.NewEncoder(out).Encode(map[string]any{"errcode": 0, "join_way": copy})
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "fixture", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"a", "b", "c", "d", "e"}
	o := w.InvitationJoinWayOptions{AutoCreateRoom: true, RoomBaseName: "活动群", RoomBaseID: 10, Remark: "秋季活动", State: "native-source"}
	code, err := c.CreateNativeInvitationCode(context.Background(), ids, o)
	if err != nil || code.ConfigID != "native-config" || saved["auto_create_room"] != float64(1) || saved["room_base_name"] != o.RoomBaseName || saved["room_base_id"] != float64(10) || saved["remark"] != o.Remark || saved["state"] != o.State {
		t.Fatalf("native params not delivered: %+v %v %+v", code, err, saved)
	}
	qrURL = "http://p.qpic.cn/wwhead/native-code"
	o.AutoCreateRoom = false
	o.Remark = ""
	o.State = ""
	code, err = c.UpdateNativeInvitationCode(context.Background(), code.ConfigID, ids, o)
	if err != nil || saved["auto_create_room"] != float64(0) || saved["remark"] != "" || saved["state"] != "" || saved["config_id"] != "native-config" || code.QRCode != "https://p.qpic.cn/wwhead/native-code" {
		t.Fatal(code, err, saved)
	}
	if _, err = c.CreateNativeInvitationCode(context.Background(), append(ids, "f"), o); err == nil || calls != 2 {
		t.Fatal("over-limit input reached Provider", err, calls)
	}
	o.AutoCreateRoom = true
	corrupt = true
	code, err = c.UpdateNativeInvitationCode(context.Background(), "native-config", ids, o)
	if err == nil || !w.ProviderCallAttempted(err) || code.ConfigID != "native-config" || code.QRCode != "" {
		t.Fatal("mismatched readback must retain unresolved config", code, err)
	}
	corrupt = false
	for _, invalidQR := range []string{"http://attacker.test/code", "https://p.qpic.cn.attacker.test/code", "http://user:pass@p.qpic.cn/code", "http://p.qpic.cn:8080/code"} {
		qrURL = invalidQR
		code, err = c.UpdateNativeInvitationCode(context.Background(), "native-config", ids, o)
		if err == nil || !w.ProviderCallAttempted(err) || code.ConfigID != "native-config" || code.QRCode != "" {
			t.Fatal("untrusted QR must remain unresolved", code, err)
		}
	}

}
