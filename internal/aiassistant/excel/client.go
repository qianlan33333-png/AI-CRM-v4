// Package excel connects the existing AI Assistant to the independent
// preparation/observation component. It never calls a Provider writer.
package excel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("excel component unavailable")

type InputError struct{ Message string }

func (e *InputError) Error() string { return "invalid excel input" }

// ComponentError retains safe, user-actionable component validation details.
// Callers must not flatten a rejected import or duplicate into a 503.
type ComponentError struct {
	Status  int
	Code    string
	Message string
	Body    json.RawMessage
}

func (e *ComponentError) Error() string { return "excel component rejected request" }

type Client struct {
	Base, Token string
	HTTP        *http.Client
	MediaCovers mediaport.ExcelCoverReader
}

func NewClient(base, token string) (*Client, error) {
	if base == "" && token == "" {
		return nil, nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil || u.RawQuery != "" || u.Path != "" || len(token) < 32 {
		return nil, ErrUnavailable
	}
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Call(ctx context.Context, method, path, key string, body []byte, out any) error {
	return c.call(ctx, method, path, key, "application/json", body, out)
}

func (c *Client) Raw(ctx context.Context, method, path, key, contentType string, body []byte, out any) error {
	return c.call(ctx, method, path, key, contentType, body, out)
}

func (c *Client) call(ctx context.Context, method, path, key, contentType string, body []byte, out any) error {
	if c == nil {
		return ErrUnavailable
	}
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", contentType)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	response, err := c.HTTP.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		raw, readErr := readBounded(response.Body, 128<<10)
		if readErr != nil {
			return ErrUnavailable
		}
		var input struct {
			Code    string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &input)
		if input.Code == "" {
			input.Code = "invalid_input"
		}
		return &ComponentError{Status: response.StatusCode, Code: input.Code, Message: input.Message, Body: raw}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	raw, err := readBounded(response.Body, 32<<20)
	if err != nil {
		return ErrUnavailable
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = raw
		return nil
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	return nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	if maximum < 1 {
		return nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, ErrUnavailable
	}
	return raw, nil
}
func (c *Client) JSON(ctx context.Context, path string, input, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return c.Call(ctx, http.MethodPost, path, "", raw, out)
}
func (c *Client) LoadExcelCard(ctx context.Context, card ai.ExcelCard) (outbound.PrivateMessageAttachment, error) {
	var raw []byte
	if strings.TrimSpace(card.Title) == "" {
		return outbound.PrivateMessageAttachment{}, outbound.PayloadPreparationError("title_missing")
	}
	if card.CoverDigest == "" {
		return outbound.PrivateMessageAttachment{}, outbound.PayloadPreparationError("cover_missing")
	}
	if !card.Valid() {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	if card.CoverImageID > 0 {
		if c == nil || c.MediaCovers == nil {
			return outbound.PrivateMessageAttachment{}, ErrUnavailable
		}
		digest, decodeErr := digestBytes(card.CoverDigest)
		if decodeErr != nil {
			return outbound.PrivateMessageAttachment{}, ErrUnavailable
		}
		cover, readErr := c.MediaCovers.ReadExcelCover(ctx, card.CoverImageID, digest)
		if readErr != nil {
			return outbound.PrivateMessageAttachment{}, ErrUnavailable
		}
		raw = cover.Bytes
	} else if err := c.Call(ctx, http.MethodGet, "/covers/"+url.PathEscape(string(card.CoverDigest)), "", nil, &raw); err != nil {
		return outbound.PrivateMessageAttachment{}, err
	}
	hash := sha256.Sum256(raw)
	if effect.Digest("sha256:"+hex.EncodeToString(hash[:])) != card.CoverDigest || len(raw) > 2<<20 {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	mime, name := "image/png", "cover.png"
	if len(raw) > 3 && bytes.Equal(raw[:3], []byte{255, 216, 255}) {
		mime, name = "image/jpeg", "cover.jpg"
	} else if !bytes.HasPrefix(raw, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	return outbound.PrivateMessageAttachment{Kind: "mini_program", Content: raw, MediaType: mime, FileName: name, AppID: card.AppID, PagePath: card.Path, Title: card.Title}, nil
}

func digestBytes(value effect.Digest) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if !effect.ValidDigest(value) {
		return result, ErrUnavailable
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(string(value), "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return result, ErrUnavailable
	}
	copy(result[:], decoded)
	return result, nil
}
func snapshotKey(id ai.PlanID, version int64) string {
	return strings.Join([]string{fmtInt(int64(id)), fmtInt(version)}, ":")
}
