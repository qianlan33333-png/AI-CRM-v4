package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// OfficialAccount is the existing OA credential's narrow open-tag signer.
// Provider tokens stay in memory and are never returned, persisted or logged.
type OfficialAccount struct {
	AppID, Secret string
	Client        *http.Client
	mu            sync.Mutex
	ticket        string
	expires       time.Time
}

func (o *OfficialAccount) request(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var data []byte
	if body != nil {
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			return p.ErrUnavailable
		}
	}
	endpoint := "https://api.weixin.qq.com" + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if e != nil {
		return p.ErrUnavailable
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 10 * time.Second}
	if o.Client != nil {
		client = *o.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		return p.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(out) != nil {
		return p.ErrUnavailable
	}
	return nil
}
func (o *OfficialAccount) SignPage(ctx context.Context, page string) (p.JSConfig, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if o.AppID == "" || o.Secret == "" {
		return p.JSConfig{}, p.ErrDisabled
	}
	if !o.expires.After(now) {
		var token struct {
			AccessToken string `json:"access_token"`
			ErrCode     int    `json:"errcode"`
		}
		// Reuse the same stable-token contract as the current Payment adapter.
		// Do not force-refresh a credential potentially shared by other OA uses.
		if e := o.request(ctx, http.MethodPost, "/cgi-bin/stable_token", nil, map[string]any{"grant_type": "client_credential", "appid": o.AppID, "secret": o.Secret, "force_refresh": false}, &token); e != nil || token.ErrCode != 0 || token.AccessToken == "" {
			return p.JSConfig{}, p.ErrUnavailable
		}
		var ticket struct {
			Ticket  string `json:"ticket"`
			Expires int64  `json:"expires_in"`
			ErrCode int    `json:"errcode"`
		}
		if e := o.request(ctx, http.MethodGet, "/cgi-bin/ticket/getticket", url.Values{"access_token": {token.AccessToken}, "type": {"jsapi"}}, nil, &ticket); e != nil || ticket.ErrCode != 0 || ticket.Ticket == "" || ticket.Expires < 120 {
			return p.JSConfig{}, p.ErrUnavailable
		}
		o.ticket = ticket.Ticket
		o.expires = now.Add(time.Duration(ticket.Expires-60) * time.Second)
	}
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return p.JSConfig{}, p.ErrUnavailable
	}
	n := hex.EncodeToString(nonce)
	ts := now.Unix()
	sum := sha1.Sum([]byte(fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", o.ticket, n, ts, page)))
	return p.JSConfig{AppID: o.AppID, Timestamp: ts, Nonce: n, Signature: hex.EncodeToString(sum[:])}, nil
}
