package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// UploadSidebarImage is a transport leaf invoked only by Outbound's leased
// media-preparation Provider. It uploads bytes and never sends a message.
func (c *Client) UploadSidebarImage(ctx context.Context, source outboundport.SidebarImagePreparationSource) (outboundport.SidebarImageUploadReceipt, bool, error) {
	if c == nil || !c.config.Enabled || source.Scope != string(effectport.Hash("sidebar.image.scope.config.v1", c.config.CorpID+":"+c.config.AgentID)) || len(source.Content) <= 5 || len(source.Content) > 2<<20 || (source.MediaType != "image/jpeg" && source.MediaType != "image/png") || strings.TrimSpace(source.FileName) == "" || strings.ContainsAny(source.FileName, "\r\n\x00") {
		return outboundport.SidebarImageUploadReceipt{}, false, ErrUnavailable
	}
	// Use the same application credential that signs the sidebar's JSSDK.
	token, err := c.accessToken(ctx)
	if err != nil {
		return outboundport.SidebarImageUploadReceipt{}, false, err
	}
	started := c.now()
	mediaID, attempted, err := c.uploadSidebarImage(ctx, token, source.FileName, source.MediaType, source.Content)
	if err != nil {
		return outboundport.SidebarImageUploadReceipt{}, attempted, err
	}
	// WeCom temporary media lasts three days. Reserve two hours for clock and
	// queue margins; callers also demand validity through the SDK grant expiry.
	return outboundport.SidebarImageUploadReceipt{MediaID: mediaID, ReadyUntil: started.Add(70 * time.Hour)}, true, nil
}

// sidebarImageUploadFailure holds protocol categories only. Never retain the
// transport error, URL, token, response body or provider diagnostic text.
type sidebarImageUploadFailure struct {
	unknown      bool
	code         string
	providerCode int64
	status       int
}

func (e sidebarImageUploadFailure) Error() string        { return e.FailureCode() }
func (e sidebarImageUploadFailure) OutcomeUnknown() bool { return e.unknown }
func (e sidebarImageUploadFailure) FailureCode() string {
	if e.providerCode != 0 {
		return "wecom_errcode_" + strconv.FormatInt(e.providerCode, 10)
	}
	return e.code
}
func (e sidebarImageUploadFailure) ProviderErrorCode() int64 { return e.providerCode }
func (e sidebarImageUploadFailure) HTTPStatusCode() int      { return e.status }

var _ outboundport.SidebarImageUploadError = sidebarImageUploadFailure{}

// Keep this parser separate from private-message uploads: a request attempt is
// not the same fact as an uncertain outcome. No retry occurs in this transport.
func (c *Client) uploadSidebarImage(ctx context.Context, token, name, mediaType string, content []byte) (string, bool, error) {
	fail := func(code string, status int, unknown, attempted bool) (string, bool, error) {
		return "", attempted, sidebarImageUploadFailure{unknown: unknown, code: code, status: status}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	escapedName := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(name)
	header.Set("Content-Disposition", "form-data; name=\"media\"; filename=\""+escapedName+"\"; filelength="+strconv.Itoa(len(content)))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fail("upload_request_invalid", 0, false, false)
	}
	if _, err = part.Write(content); err != nil {
		return fail("upload_request_invalid", 0, false, false)
	}
	if err = writer.Close(); err != nil {
		return fail("upload_request_invalid", 0, false, false)
	}
	endpoint := *c.apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/cgi-bin/media/upload"
	endpoint.RawQuery = url.Values{"access_token": {token}, "type": {"image"}}.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(body.Bytes()))
	if err != nil {
		return fail("upload_request_invalid", 0, false, false)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	// Never follow a redirect with an upload or reveal its credential-bearing URL.
	transport := *c.http
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := transport.Do(req)
	if err != nil {
		return fail("upload_transport_unknown", 0, true, true)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil || len(raw) > maxResponseBody {
		return fail("upload_response_unreadable", resp.StatusCode, true, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail("upload_http_unknown", resp.StatusCode, true, true)
	}
	var result struct {
		ErrCode json.RawMessage `json:"errcode"`
		MediaID string          `json:"media_id"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return fail("upload_response_invalid", resp.StatusCode, true, true)
	}
	if len(result.ErrCode) > 0 {
		if bytes.Equal(bytes.TrimSpace(result.ErrCode), []byte("null")) {
			return fail("upload_response_invalid", resp.StatusCode, true, true)
		}
		var code int64
		if json.Unmarshal(result.ErrCode, &code) != nil {
			var value string
			if json.Unmarshal(result.ErrCode, &value) != nil {
				return fail("upload_response_invalid", resp.StatusCode, true, true)
			}
			code, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fail("upload_response_invalid", resp.StatusCode, true, true)
			}
		}
		if code != 0 {
			if strings.TrimSpace(result.MediaID) != "" {
				return fail("upload_response_conflict", resp.StatusCode, true, true)
			}
			return "", true, sidebarImageUploadFailure{providerCode: code, status: resp.StatusCode}
		}
	}
	id := strings.TrimSpace(result.MediaID)
	if id == "" || len(id) > 1024 {
		return fail("upload_receipt_missing", resp.StatusCode, true, true)
	}
	return id, true, nil
}
