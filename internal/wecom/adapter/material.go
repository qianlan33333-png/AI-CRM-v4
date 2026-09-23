package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// MaterialUploader is the WeCom transport leaf for Outbound-owned temporary
// media preparation. It has no cache, queue, retry loop, or message writer.
type MaterialUploader struct {
	client      *Client
	scopeDigest string
}

func NewMaterialUploader(client *Client, scopeDigest string) (*MaterialUploader, error) {
	if client == nil || !client.Ready() || strings.TrimSpace(scopeDigest) == "" {
		return nil, ErrUnavailable
	}
	return &MaterialUploader{client: client, scopeDigest: scopeDigest}, nil
}

var _ outboundport.MaterialUploader = (*MaterialUploader)(nil)

func (u *MaterialUploader) UploadMaterial(ctx context.Context, source outboundport.MaterialSourceSnapshot, content outboundport.MaterialSourceContent, scopeDigest string) (outboundport.MaterialUploadReceipt, bool, error) {
	if u == nil || u.client == nil || !u.client.Ready() || scopeDigest != u.scopeDigest || !validMaterialUpload(source, content) {
		return outboundport.MaterialUploadReceipt{}, false, ErrUnavailable
	}
	token, err := u.client.accessToken(ctx)
	if err != nil {
		// Token acquisition happens before the upload request; it never creates
		// an uncertain media-write outcome.
		return outboundport.MaterialUploadReceipt{}, false, err
	}
	return u.upload(ctx, token, source.SourceType, content.FileName, content.MediaType, content.Bytes)
}

func validMaterialUpload(source outboundport.MaterialSourceSnapshot, content outboundport.MaterialSourceContent) bool {
	if (source.SourceType != "image" && source.SourceType != "file" && source.SourceType != "video" && source.SourceType != "voice") || source.SizeBytes != int64(len(content.Bytes)) || source.FileName != content.FileName || source.MediaType != content.MediaType || len(content.Bytes) == 0 || strings.TrimSpace(content.FileName) == "" || strings.ContainsAny(content.FileName, "\r\n\x00") {
		return false
	}
	return sha256.Sum256(content.Bytes) == source.ContentDigest
}

type materialUploadFailure struct {
	unknown, retryable bool
	code               string
}

func (e materialUploadFailure) Error() string        { return e.code }
func (e materialUploadFailure) OutcomeUnknown() bool { return e.unknown }
func (e materialUploadFailure) Retryable() bool      { return e.retryable }
func (e materialUploadFailure) FailureCode() string  { return e.code }

var _ outboundport.MaterialUploadError = materialUploadFailure{}

func (u *MaterialUploader) upload(ctx context.Context, token, kind, name, mediaType string, content []byte) (outboundport.MaterialUploadReceipt, bool, error) {
	fail := func(code string, attempted, unknown, retryable bool) (outboundport.MaterialUploadReceipt, bool, error) {
		return outboundport.MaterialUploadReceipt{}, attempted, materialUploadFailure{code: code, unknown: unknown, retryable: retryable}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	escapedName := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(name)
	header.Set("Content-Disposition", "form-data; name=\"media\"; filename=\""+escapedName+"\"; filelength="+strconv.Itoa(len(content)))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fail("upload_request_invalid", false, false, false)
	}
	if _, err = part.Write(content); err != nil {
		return fail("upload_request_invalid", false, false, false)
	}
	if err = writer.Close(); err != nil {
		return fail("upload_request_invalid", false, false, false)
	}
	endpoint := *u.client.apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/cgi-bin/media/upload"
	endpoint.RawQuery = url.Values{"access_token": {token}, "type": {kind}}.Encode()
	timeout := u.client.config.UploadTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(body.Bytes()))
	if err != nil {
		return fail("upload_request_invalid", false, false, false)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	transport := *u.client.http
	// The normal client is intentionally short-lived for ordinary reads and
	// messages. A media upload must use its own bounded deadline instead.
	transport.Timeout = timeout
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := transport.Do(request)
	if err != nil {
		return fail("upload_transport_unknown", true, true, false)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || len(raw) > maxResponseBody {
		return fail("upload_response_unreadable", true, true, false)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		// A gateway HTTP status is not a provider receipt. It cannot prove that
		// the upstream did not accept the multipart body, including 429/5xx.
		return fail("upload_http_"+strconv.Itoa(response.StatusCode), true, true, false)
	}
	var result struct {
		ErrCode   json.RawMessage `json:"errcode"`
		MediaID   string          `json:"media_id"`
		CreatedAt json.RawMessage `json:"created_at"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return fail("upload_response_invalid", true, true, false)
	}
	if len(result.ErrCode) > 0 && !bytes.Equal(bytes.TrimSpace(result.ErrCode), []byte("null")) {
		var code int64
		if err = json.Unmarshal(result.ErrCode, &code); err != nil {
			return fail("upload_response_invalid", true, true, false)
		}
		if code != 0 {
			return fail("wecom_errcode_"+strconv.FormatInt(code, 10), true, false, materialErrcodeRetryable(code))
		}
	}
	if strings.TrimSpace(result.MediaID) == "" || len(result.MediaID) > 1024 {
		return fail("upload_receipt_missing", true, true, false)
	}
	createdAt, ok := parseMaterialCreatedAt(result.CreatedAt, u.client.now())
	if !ok {
		return fail("upload_created_at_missing", true, true, false)
	}
	return outboundport.MaterialUploadReceipt{MediaID: strings.TrimSpace(result.MediaID), ProviderCreatedAt: createdAt}, true, nil
}

func parseMaterialCreatedAt(raw json.RawMessage, now time.Time) (time.Time, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 {
		return time.Time{}, false
	}
	if value[0] == '"' {
		var encoded string
		if json.Unmarshal(value, &encoded) != nil {
			return time.Time{}, false
		}
		value = []byte(encoded)
	}
	if len(value) == 0 {
		return time.Time{}, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return time.Time{}, false
		}
	}
	seconds, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil || seconds < 1 || seconds > now.UTC().Add(5*time.Minute).Unix() {
		return time.Time{}, false
	}
	createdAt := time.Unix(seconds, 0).UTC()
	return createdAt, true
}

func materialErrcodeRetryable(code int64) bool {
	// WeCom documents these as request-frequency/system-busy conditions. The
	// caller persists the retry classification; this adapter never loops.
	return code == 45009 || code == 45011 || code == -1
}
