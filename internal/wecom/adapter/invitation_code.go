package adapter

import (
	"context"
	"encoding/json"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/url"
	"slices"
)

// Only outbound receives this writer. Always bind one existing group and
// explicitly disable automatic group creation.
func (c *Client) CreateInvitationCode(ctx context.Context, chat string) (w.InvitationCode, error) {
	return c.CreateInvitationCodeForGroups(ctx, []string{chat})
}

func (c *Client) CreateInvitationCodeForGroups(ctx context.Context, chats []string) (w.InvitationCode, error) {
	if !c.DirectoryReady() || len(chats) == 0 || len(chats) > 5 {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, false)
	}
	for _, chat := range chats {
		if invalid(chat) {
			return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, false)
		}
	}
	token, err := c.contactAccessToken(ctx)
	if err != nil {
		return w.InvitationCode{}, w.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]any{"scene": 2, "auto_create_room": 0, "chat_id_list": chats})
	result, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/add_join_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return w.InvitationCode{}, w.WrapProviderWriteError(err, true)
	}
	if invalid(result.ConfigID) {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, true)
	}
	body, _ = json.Marshal(map[string]string{"config_id": result.ConfigID})
	detail, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get_join_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return w.InvitationCode{ConfigID: result.ConfigID}, w.WrapProviderWriteError(err, true)
	}
	if !validJoinWayReadback(detail.JoinWay.ConfigID, detail.JoinWay.Scene, detail.JoinWay.AutoCreateRoom, detail.JoinWay.ChatIDs, detail.JoinWay.QRCode, result.ConfigID, chats) {
		return w.InvitationCode{ConfigID: result.ConfigID}, w.WrapProviderWriteError(ErrResponse, true)
	}
	return w.InvitationCode{ConfigID: result.ConfigID, QRCode: detail.JoinWay.QRCode}, nil
}

func (c *Client) UpdateInvitationCodeForGroups(ctx context.Context, configID string, chats []string) (w.InvitationCode, error) {
	if !c.DirectoryReady() || invalid(configID) || len(chats) == 0 || len(chats) > 5 {
		return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(ErrResponse, false)
	}
	for _, chat := range chats {
		if invalid(chat) {
			return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(ErrResponse, false)
		}
	}
	token, err := c.contactAccessToken(ctx)
	if err != nil {
		return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(err, false)
	}
	// update_join_way replaces the configuration. Keep the original QR scene
	// and manual group policy when switching its bound customer group.
	body, _ := json.Marshal(map[string]any{"config_id": configID, "scene": 2, "auto_create_room": 0, "chat_id_list": chats})
	if _, err = c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/update_join_way", url.Values{"access_token": {token}}, body); err != nil {
		return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(err, true)
	}
	body, _ = json.Marshal(map[string]string{"config_id": configID})
	detail, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get_join_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(err, true)
	}
	if !validJoinWayReadback(detail.JoinWay.ConfigID, detail.JoinWay.Scene, detail.JoinWay.AutoCreateRoom, detail.JoinWay.ChatIDs, detail.JoinWay.QRCode, configID, chats) {
		return w.InvitationCode{ConfigID: configID}, w.WrapProviderWriteError(ErrResponse, true)
	}
	return w.InvitationCode{ConfigID: configID, QRCode: detail.JoinWay.QRCode}, nil
}

func validJoinWayReadback(configID string, scene, autoCreateRoom int, actualChats []string, qrCode, expectedConfigID string, expectedChats []string) bool {
	if configID != expectedConfigID || scene != 2 || autoCreateRoom != 0 || !validProviderHTTPS(qrCode) || len(actualChats) != len(expectedChats) {
		return false
	}
	return slices.Equal(slices.Sorted(slices.Values(actualChats)), slices.Sorted(slices.Values(expectedChats)))
}
