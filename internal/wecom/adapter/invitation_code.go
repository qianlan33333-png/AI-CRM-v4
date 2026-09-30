package adapter

import (
	"context"
	"encoding/json"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/url"
	"slices"
)

// Legacy plans bind existing groups and explicitly disable automatic creation.
func (c *Client) CreateInvitationCode(ctx context.Context, chat string) (w.InvitationCode, error) {
	return c.CreateInvitationCodeForGroups(ctx, []string{chat})
}
func (c *Client) CreateInvitationCodeForGroups(ctx context.Context, chats []string) (w.InvitationCode, error) {
	return c.writeInvitationCode(ctx, "", chats, nil)
}
func (c *Client) UpdateInvitationCodeForGroups(ctx context.Context, id string, chats []string) (w.InvitationCode, error) {
	if invalid(id) {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, false)
	}
	return c.writeInvitationCode(ctx, id, chats, nil)
}
func (c *Client) CreateNativeInvitationCode(ctx context.Context, chats []string, o w.InvitationJoinWayOptions) (w.InvitationCode, error) {
	return c.writeInvitationCode(ctx, "", chats, &o)
}
func (c *Client) UpdateNativeInvitationCode(ctx context.Context, id string, chats []string, o w.InvitationJoinWayOptions) (w.InvitationCode, error) {
	if invalid(id) {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, false)
	}
	return c.writeInvitationCode(ctx, id, chats, &o)
}
func (c *Client) writeInvitationCode(ctx context.Context, id string, chats []string, o *w.InvitationJoinWayOptions) (w.InvitationCode, error) {
	code := w.InvitationCode{ConfigID: id}
	if !c.DirectoryReady() || len(chats) == 0 || len(chats) > 5 || (o != nil && !o.Valid()) {
		return code, w.WrapProviderWriteError(ErrResponse, false)
	}
	seen := map[string]bool{}
	for _, chat := range chats {
		if invalid(chat) || seen[chat] {
			return code, w.WrapProviderWriteError(ErrResponse, false)
		}
		seen[chat] = true
	}
	token, err := c.contactAccessToken(ctx)
	if err != nil {
		return code, w.WrapProviderWriteError(err, false)
	}
	body := map[string]any{"scene": 2, "auto_create_room": 0, "chat_id_list": chats}
	if o != nil {
		if o.AutoCreateRoom {
			body["auto_create_room"] = 1
			body["room_base_name"] = o.RoomBaseName
			body["room_base_id"] = o.RoomBaseID
		}
		// Empty values are intentional: update replaces previously configured data.
		body["remark"] = o.Remark
		body["state"] = o.State
	}
	path := "/cgi-bin/externalcontact/groupchat/add_join_way"
	if id != "" {
		path = "/cgi-bin/externalcontact/groupchat/update_join_way"
		body["config_id"] = id
	}
	raw, _ := json.Marshal(body)
	result, err := c.requestJSON(ctx, http.MethodPost, path, url.Values{"access_token": {token}}, raw)
	if err != nil {
		return code, w.WrapProviderWriteError(err, true)
	}
	if id == "" {
		code.ConfigID = result.ConfigID
	}
	if invalid(code.ConfigID) {
		return code, w.WrapProviderWriteError(ErrResponse, true)
	}
	raw, _ = json.Marshal(map[string]string{"config_id": code.ConfigID})
	detail, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get_join_way", url.Values{"access_token": {token}}, raw)
	if err != nil {
		return code, w.WrapProviderWriteError(err, true)
	}
	j := detail.JoinWay
	auto := 0
	if o != nil && o.AutoCreateRoom {
		auto = 1
	}
	if j.ConfigID != code.ConfigID || j.Scene != 2 || j.AutoCreateRoom != auto || !validProviderHTTPS(j.QRCode) || !slices.Equal(slices.Sorted(slices.Values(j.ChatIDs)), slices.Sorted(slices.Values(chats))) {
		return code, w.WrapProviderWriteError(ErrResponse, true)
	}
	if o != nil && (j.Remark != o.Remark || j.ChannelState != o.State || (o.AutoCreateRoom && (j.RoomBaseName != o.RoomBaseName || j.RoomBaseID != o.RoomBaseID))) {
		return code, w.WrapProviderWriteError(ErrResponse, true)
	}
	code.QRCode = j.QRCode
	return code, nil
}
