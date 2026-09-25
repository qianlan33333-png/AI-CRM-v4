package http

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

const maxInvitationQRBytes = 2 << 20

var officialJoinPath = regexp.MustCompile(`^/gm/[A-Za-z0-9_-]{8,128}$`)

func officialQRCodeReady(plan p.InvitationPlan) bool {
	return plan.Token != "" && plan.Enabled && plan.State == "active" && plan.CurrentChatID != "" &&
		(plan.ProviderState == "executed" || plan.ProviderState == "reconciled") &&
		plan.ProviderConfigID != "" && plan.ProviderQRCode != ""
}

// Fetch only the Provider's QR image URL. The API exposes no documented
// direct gm URL, so this endpoint must never derive one from config_id.
func fetchOfficialQRCode(ctx context.Context, raw string, injected *http.Client) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" || u.Fragment != "" ||
		(u.Hostname() != "wework.qpic.cn" && u.Hostname() != "p.qpic.cn") {
		return nil, errors.New("invalid official QR host")
	}
	// WeCom documents p.qpic.cn QR URLs with an http scheme. Fetch the
	// allowlisted image over TLS even when that is the scheme it returns.
	u.Scheme = "https"
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	if injected != nil {
		client.Transport = injected.Transport
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxInvitationQRBytes {
		return nil, errors.New("official QR response unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInvitationQRBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxInvitationQRBytes {
		return nil, errors.New("invalid official QR size")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 32 || config.Height < 32 || config.Width > 2048 || config.Height > 2048 {
		return nil, errors.New("invalid official QR dimensions")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// WeCom only documents a QR image URL. Decode the actual image rather than
// constructing a join link from config_id or a current group's chat ID.
func officialJoinURL(qrImage []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(qrImage))
	if err != nil {
		return "", err
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		return "", err
	}
	target := result.GetText()
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host != "work.weixin.qq.com" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !officialJoinPath.MatchString(u.EscapedPath()) {
		return "", errors.New("invalid official join URL")
	}
	return target, nil
}
