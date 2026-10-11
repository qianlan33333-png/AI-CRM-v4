package provider

import (
	"context"
	"crypto/sha256"
	shopport "github.com/qianlan33333-png/AI-CRM-v3/internal/wechatshop/port"
)

// VerifyEvent authenticates payment and aftersale hints. The hint itself can
// never settle an order: the worker must read the Provider's authoritative API.
func (v *ShopCallbackVerifier) VerifyEvent(ctx context.Context, body []byte, query map[string]string) (shopport.Callback, error) {
	bad := ErrInvalidWeChatShopCallback
	if !v.ready(ctx) || len(body) == 0 || len(body) > 128<<10 || !v.validTimestamp(query["timestamp"]) || !validSHA1Signature(query["msg_signature"]) || !validCallbackPart(query["nonce"], 128) {
		return shopport.Callback{}, bad
	}
	var envelope struct {
		ToUserName string `json:"ToUserName"`
		Encrypt    string `json:"Encrypt"`
	}
	if !decodeShopObject(body, &envelope) || !validCallbackPart(envelope.ToUserName, 128) || envelope.Encrypt == "" || !shopSHA1(query["msg_signature"], v.credential.token, query["timestamp"], query["nonce"], envelope.Encrypt) {
		return shopport.Callback{}, bad
	}
	plain, e := decryptShopCallback(envelope.Encrypt, v.credential.key, v.credential.appID)
	if e != nil {
		return shopport.Callback{}, bad
	}
	var msg struct {
		Type  string `json:"MsgType"`
		Event string `json:"Event"`
		Order struct {
			ID exactShopReference `json:"order_id"`
		} `json:"order_info"`
		After struct {
			ID exactShopReference `json:"order_id"`
		} `json:"finder_shop_aftersale_status_update"`
	}
	if !decodeShopObject(plain, &msg) || msg.Type != "event" {
		return shopport.Callback{}, bad
	}
	result := shopport.Callback{EventDigest: sha256.Sum256(append([]byte(v.credential.appID+"\x00"), plain...)), BodyDigest: sha256.Sum256(body)}
	switch msg.Event {
	case "channels_ec_order_pay":
		if !msg.Order.ID.set {
			return result, bad
		}
		result.OrderID = msg.Order.ID.value
		result.Relevant = true
	case weChatShopCallbackEvent:
		if !msg.After.ID.set {
			return result, bad
		}
		result.OrderID = msg.After.ID.value
		result.Relevant = true
	}
	return result, nil
}
