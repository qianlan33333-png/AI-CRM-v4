package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

const weChatShopCallbackEvent = "channels_ec_aftersale_update"

var ErrInvalidWeChatShopCallback = errors.New("invalid wechat shop callback")

type ShopCallbackCredential struct {
	appID string
	token string
	key   []byte
}

func NewShopCallbackCredential(appID, token, encodingAESKey string) (*ShopCallbackCredential, error) {
	key, err := decodeShopAESKey(encodingAESKey)
	if !validCallbackPart(appID, 128) || !validCallbackPart(token, 256) || err != nil {
		return nil, ErrInvalidConfig
	}
	return &ShopCallbackCredential{appID: appID, token: token, key: key}, nil
}

func (*ShopCallbackCredential) String() string   { return "wechat-shop-callback-credential[redacted]" }
func (*ShopCallbackCredential) GoString() string { return "wechat-shop-callback-credential[redacted]" }

type ShopCallbackVerifier struct {
	credential *ShopCallbackCredential
	now        func() time.Time
}

func NewShopCallbackVerifier(credential *ShopCallbackCredential) (*ShopCallbackVerifier, error) {
	if credential == nil || len(credential.key) != 32 {
		return nil, ErrInvalidConfig
	}
	return &ShopCallbackVerifier{credential: credential, now: time.Now}, nil
}

func (*ShopCallbackVerifier) String() string   { return "wechat-shop-callback-verifier[redacted]" }
func (*ShopCallbackVerifier) GoString() string { return "wechat-shop-callback-verifier[redacted]" }

func (verifier *ShopCallbackVerifier) VerifyURL(ctx context.Context, query map[string]string) (string, error) {
	if !verifier.ready(ctx) {
		return "", ErrInvalidWeChatShopCallback
	}
	signature, timestamp, nonce, echo := query["signature"], query["timestamp"], query["nonce"], query["echostr"]
	if echo == "" || len(echo) > 64<<10 || !verifier.validTimestamp(timestamp) || !validSHA1Signature(signature) || !validCallbackPart(nonce, 128) || !shopSHA1(signature, verifier.credential.token, timestamp, nonce) {
		return "", ErrInvalidWeChatShopCallback
	}
	return echo, nil
}

func (verifier *ShopCallbackVerifier) VerifyRefund(ctx context.Context, body []byte, query map[string]string) (paymentport.ShopRefundCallback, error) {
	if !verifier.ready(ctx) || len(body) == 0 || len(body) > 128<<10 {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	signature, timestamp, nonce := query["msg_signature"], query["timestamp"], query["nonce"]
	if !verifier.validTimestamp(timestamp) || !validSHA1Signature(signature) || !validCallbackPart(nonce, 128) {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	var envelope struct {
		ToUserName string `json:"ToUserName"`
		Encrypt    string `json:"Encrypt"`
	}
	if !decodeShopObject(body, &envelope) || !validCallbackPart(envelope.ToUserName, 128) || envelope.Encrypt == "" || len(envelope.Encrypt) > 128<<10 || !shopSHA1(signature, verifier.credential.token, timestamp, nonce, envelope.Encrypt) {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	plain, err := decryptShopCallback(envelope.Encrypt, verifier.credential.key, verifier.credential.appID)
	if err != nil || len(plain) == 0 || len(plain) > 64<<10 {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	var message struct {
		CreateTime exactShopInteger `json:"CreateTime"`
		MsgType    string           `json:"MsgType"`
		Event      string           `json:"Event"`
		Update     struct {
			Status           string             `json:"status"`
			AfterSaleOrderID exactShopReference `json:"after_sale_order_id"`
			OrderID          exactShopReference `json:"order_id"`
		} `json:"finder_shop_aftersale_status_update"`
	}
	if !decodeShopObject(plain, &message) || !message.CreateTime.set || message.CreateTime.value < 1 || message.MsgType != "event" || message.Event != weChatShopCallbackEvent || !message.Update.AfterSaleOrderID.set || !message.Update.OrderID.set || !validShopStatus(message.Update.Status) {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	occurred := time.Unix(message.CreateTime.value, 0).UTC()
	if occurred.After(verifier.now().UTC().Add(5 * time.Minute)) {
		return paymentport.ShopRefundCallback{}, ErrInvalidWeChatShopCallback
	}
	payloadDigest := sha256.Sum256(body)
	eventDigest := sha256.Sum256([]byte(strings.Join([]string{"wechat-shop/aftersale-callback-event/v1", message.Update.AfterSaleOrderID.value, message.Update.OrderID.value, message.Update.Status, strconv.FormatInt(message.CreateTime.value, 10), string(plain)}, "\x00")))
	return paymentport.ShopRefundCallback{AfterSaleID: message.Update.AfterSaleOrderID.value, ProviderOrderID: message.Update.OrderID.value, Status: message.Update.Status, EventDigest: eventDigest, PayloadDigest: payloadDigest, OccurredAt: occurred}, nil
}

func (verifier *ShopCallbackVerifier) ready(ctx context.Context) bool {
	return verifier != nil && verifier.credential != nil && verifier.now != nil && ctx != nil && ctx.Err() == nil
}

func (verifier *ShopCallbackVerifier) validTimestamp(value string) bool {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	delta := verifier.now().UTC().Sub(time.Unix(seconds, 0).UTC())
	return delta >= -5*time.Minute && delta <= 5*time.Minute
}

func shopSHA1(signature string, parts ...string) bool {
	values := append([]string(nil), parts...)
	sort.Strings(values)
	digest := sha1.Sum([]byte(strings.Join(values, "")))
	return subtle.ConstantTimeCompare([]byte(signature), []byte(hex.EncodeToString(digest[:]))) == 1
}

func validSHA1Signature(value string) bool {
	if len(value) != sha1.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCallbackPart(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
}

func decryptShopCallback(encoded string, key []byte, appID string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 || len(key) != 32 {
		return nil, ErrInvalidWeChatShopCallback
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidWeChatShopCallback
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)
	plain, err = unpadShopPKCS7(plain)
	if err != nil || len(plain) < 21 {
		return nil, ErrInvalidWeChatShopCallback
	}
	messageLength := int(binary.BigEndian.Uint32(plain[16:20]))
	messageEnd := 20 + messageLength
	if messageLength < 1 || messageEnd < 21 || messageEnd >= len(plain) || string(plain[messageEnd:]) != appID {
		return nil, ErrInvalidWeChatShopCallback
	}
	return append([]byte(nil), plain[20:messageEnd]...), nil
}

func unpadShopPKCS7(value []byte) ([]byte, error) {
	if len(value) == 0 || len(value)%aes.BlockSize != 0 {
		return nil, ErrInvalidWeChatShopCallback
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > 32 || padding > len(value) {
		return nil, ErrInvalidWeChatShopCallback
	}
	for _, character := range value[len(value)-padding:] {
		if int(character) != padding {
			return nil, ErrInvalidWeChatShopCallback
		}
	}
	return value[:len(value)-padding], nil
}

func decodeShopAESKey(value string) ([]byte, error) {
	if len(value) != 43 {
		return nil, ErrInvalidConfig
	}
	decoded, err := base64.StdEncoding.DecodeString(value + "=")
	if err != nil || len(decoded) != 32 {
		return nil, ErrInvalidConfig
	}
	return decoded, nil
}

var _ paymentport.ShopCallbackVerifier = (*ShopCallbackVerifier)(nil)
