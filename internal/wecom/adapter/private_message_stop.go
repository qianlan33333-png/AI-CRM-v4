package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

func (client *Client) StopPrivateMessage(ctx context.Context, messageID string) (bool, error) {
	if !client.DirectoryReady() || invalid(messageID) {
		return false, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return false, err
	}
	body, _ := json.Marshal(map[string]string{"msgid": messageID})
	raw, err := client.requestRawJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/cancel_groupmsg_send", url.Values{"access_token": {token}}, body)
	if err != nil {
		var failure *providerResponseError
		if errors.As(err, &failure) && failure.errCode != 0 {
			return true, privateSendError{code: failure.errCode}
		}
		return true, privateSendError{uncertain: true}
	}
	var payload struct {
		Code json.RawMessage `json:"errcode"`
	}
	if json.Unmarshal(raw.body, &payload) != nil {
		return true, privateSendError{uncertain: true}
	}
	code, valid := strictJSONInt(payload.Code)
	if !valid {
		return true, privateSendError{uncertain: true}
	}
	if code != 0 {
		return true, privateSendError{code: code}
	}
	return true, nil
}
