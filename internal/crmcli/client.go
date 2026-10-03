// Package crmcli is a transport-only client: no database, Provider, or domain composition.
package crmcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	catalog "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/zalando/go-keyring"
)

type Credential struct {
	Origin  string `json:"origin"`
	Kind    string `json:"kind"`
	Token   string `json:"token,omitempty"`
	Session string `json:"session,omitempty"`
	CSRF    string `json:"csrf,omitempty"`
}

type Profile struct {
	Origin string `json:"origin"`
}
type Config struct {
	Current  string             `json:"current"`
	Profiles map[string]Profile `json:"profiles"`
}
type Client struct {
	Origin     string
	Credential Credential
	HTTP       *http.Client
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"http_status,omitempty"`
	Exit    int    `json:"-"`
	Result  any    `json:"-"`
}

func (e *Failure) Error() string { return e.Message }
func fail(code, message string, exit int) error {
	return &Failure{Code: code, Message: message, Exit: exit}
}

func Origin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fail("validation", "server must be an absolute origin without credentials, path or query", 2)
	}
	if u.Scheme != "https" {
		return "", fail("validation", "authenticated CRM origins require HTTPS", 2)
	}
	u.Path = ""
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

func NewClient(origin string, credential Credential, timeout time.Duration) (*Client, error) {
	origin, e := Origin(origin)
	if e != nil {
		return nil, e
	}
	if credential.Origin != "" && credential.Origin != origin {
		return nil, fail("authentication", "credential origin does not match profile", 3)
	}
	var transport http.RoundTripper = http.DefaultTransport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		t := base.Clone()
		// Go transports may replay a request carrying Idempotency-Key on a
		// reused connection. HTTP/1 and fresh write connections keep replay
		// decisions in the business Owner rather than the transport.
		t.Protocols = new(http.Protocols)
		t.Protocols.SetHTTP1(true)
		// Clone can retain the default transport's initialized HTTP/2 ALPN.
		// Keep TLS negotiation consistent with the HTTP/1-only wire transport.
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.NextProtos = []string{"http/1.1"}
		transport = t
	}
	return &Client{Origin: origin, Credential: credential, HTTP: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func configPath() (string, error) {
	if dir := platformconfig.LoadCLIEnvironment().ConfigDirectory; dir != "" {
		return filepath.Join(dir, "profiles.json"), nil
	}
	dir, e := os.UserConfigDir()
	return filepath.Join(dir, "aicrm-cli", "profiles.json"), e
}
func LoadConfig() (Config, error) {
	path, e := configPath()
	if e != nil {
		return Config{}, e
	}
	data, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return Config{Profiles: map[string]Profile{}}, nil
	}
	if e != nil {
		return Config{}, e
	}
	var c Config
	e = json.Unmarshal(data, &c)
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, e
}
func SaveConfig(c Config) error {
	path, e := configPath()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return privateFile(path, b, true)
}
func privateFile(path string, data []byte, replace bool) error {
	if stat, e := os.Lstat(path); e == nil && stat.Mode()&os.ModeSymlink != 0 {
		return fail("io", "refusing symbolic-link output", 1)
	}
	if replace {
		f, e := os.CreateTemp(filepath.Dir(path), ".aicrm-cli-*")
		if e != nil {
			return e
		}
		name := f.Name()
		defer os.Remove(name)
		if e = f.Chmod(0600); e == nil {
			_, e = f.Write(data)
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(name, path)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	f, e := os.OpenFile(path, flags, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	_, e = f.Write(data)
	return e
}

func reserveOutput(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, fail("io", "output must be a new writable file", 1)
	}
	return f, nil
}
func key(origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(sum[:])
}
func StoreCredential(c Credential) error {
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	return keyring.Set("aicrm-cli", key(c.Origin), string(b))
}
func DeleteCredential(origin string) error { return keyring.Delete("aicrm-cli", key(origin)) }
func LoadCredential(origin string) (Credential, error) {
	environment := platformconfig.LoadCLIEnvironment()
	if token := environment.Token; token != "" {
		if environment.CredentialOrigin != origin {
			return Credential{}, fail("authentication", "environment token requires matching AICRM_CLI_CREDENTIAL_ORIGIN", 3)
		}
		return Credential{Origin: origin, Kind: "machine", Token: token}, nil
	}
	b, e := keyring.Get("aicrm-cli", key(origin))
	if e != nil {
		return Credential{}, fail("authentication", "no credential for this origin; run auth login or supply an origin-bound token", 3)
	}
	var c Credential
	if e = json.Unmarshal([]byte(b), &c); e != nil || c.Origin != origin {
		return Credential{}, fail("authentication", "invalid stored credential", 3)
	}
	return c, nil
}

func (c *Client) request(ctx context.Context, method, path, content string, body io.Reader, idempotency string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fail("validation", "invalid registered path", 2)
	}
	u, e := url.Parse(c.Origin + path)
	if e != nil || u.Scheme+"://"+u.Host != c.Origin {
		return nil, fail("validation", "request cannot leave configured origin", 2)
	}
	r, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Accept", "application/json")
	if method != "GET" {
		r.GetBody = nil
		r.Close = true
	}
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	if idempotency != "" {
		r.Header.Set("Idempotency-Key", idempotency)
	}
	if c.Credential.Kind == "machine" {
		r.Header.Set("Authorization", "Bearer "+c.Credential.Token)
	}
	if c.Credential.Kind == "human" {
		r.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: c.Credential.Session})
		if method != "GET" {
			r.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: c.Credential.CSRF})
			r.Header.Set("X-CSRF-Token", c.Credential.CSRF)
		}
	}
	resp, e := c.HTTP.Do(r)
	if e != nil {
		if method != "GET" {
			return nil, fail("outcome_unknown", "request outcome is unknown; query the original operation before retrying", 7)
		}
		return nil, fail("transport", "read request failed", 1)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		exit := 1
		code := "http_error"
		switch resp.StatusCode {
		case 401:
			exit = 3
			code = "authentication"
		case 403:
			exit = 4
			code = "permission"
		case 409:
			exit = 5
			code = "conflict"
		case 400, 422:
			exit = 2
			code = "validation"
		case 429:
			code = "rate_limited"
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			code = "redirect_refused"
		}
		if resp.StatusCode >= 500 && method != "GET" {
			code = "outcome_unknown"
			exit = 7
		}
		// Do not echo arbitrary server bodies: they can contain credentials or PII.
		return nil, &Failure{Code: code, Message: "server rejected request; inspect authorized operation status or server audit", Status: resp.StatusCode, Exit: exit}
	}
	return resp, nil
}

type CallInput struct {
	Parameters     map[string]string
	Query          map[string]string
	Body           []byte
	Uploads        map[string]string
	IdempotencyKey string
}

func BuildRequest(op catalog.Operation, input CallInput) (string, string, io.Reader, error) {
	if op.Binding == nil {
		return "", "", nil, fail("unavailable", op.BlockedReason, 4)
	}
	b := op.Binding
	if b.BodyRequired && len(input.Body) == 0 && len(input.Uploads) == 0 {
		return "", "", nil, fail("validation", "request body is required; use --input or --upload", 2)
	}
	path := b.Path
	for name, value := range input.Parameters {
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\?#") {
			return "", "", nil, fail("validation", "invalid path parameter", 2)
		}
		placeholder := "{" + name + "}"
		if !strings.Contains(path, placeholder) {
			return "", "", nil, fail("validation", "unknown path parameter", 2)
		}
		path = strings.ReplaceAll(path, placeholder, url.PathEscape(value))
	}
	if strings.ContainsAny(path, "{}") {
		return "", "", nil, fail("validation", "required path parameter missing", 2)
	}
	q := url.Values{}
	encoded := map[string]bool{}
	for _, p := range b.Parameters {
		if p["in"] != "query" {
			continue
		}
		name, _ := p["name"].(string)
		v, present := input.Query[name]
		if p["required"] == true && !present {
			return "", "", nil, fail("validation", "required query parameter missing", 2)
		}
		if present {
			if s, ok := p["schema"].(map[string]any); ok {
				resolved, err := resolveParameterSchema(s, catalog.Load().Schemas)
				if err != nil {
					return "", "", nil, err
				}
				s = resolved
				var value any = v
				switch s["type"] {
				case "array":
					if e := StrictJSON([]byte(v), &value); e != nil {
						return "", "", nil, fail("validation", "array query values require a JSON array", 2)
					}
				case "integer", "number":
					value = json.Number(v)
				case "boolean":
					if v != "true" && v != "false" {
						return "", "", nil, fail("validation", "invalid boolean query value", 2)
					}
					value = v == "true"
				}
				if e := Validate(value, s, catalog.Load().Schemas); e != nil {
					return "", "", nil, e
				}
				if s["type"] == "array" {
					items := value.([]any)
					values := make([]string, 0, len(items))
					for _, item := range items {
						switch item := item.(type) {
						case string:
							values = append(values, item)
						case json.Number:
							values = append(values, string(item))
						case bool:
							values = append(values, fmt.Sprint(item))
						default:
							return "", "", nil, fail("validation", "array query items must be scalars", 2)
						}
					}
					if p["explode"] == false {
						q.Set(name, strings.Join(values, ","))
					} else {
						for _, item := range values {
							q.Add(name, item)
						}
					}
					encoded[name] = true
				}
			}
		}
	}
	for k, v := range input.Query {
		if !encoded[k] {
			q.Set(k, v)
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	if b.IdempotencyRequired && input.IdempotencyKey == "" {
		return "", "", nil, fail("validation", "Idempotency-Key is required; reuse it for this logical operation", 2)
	}
	if b.Method == "GET" && len(input.Body) > 0 {
		return "", "", nil, fail("validation", "GET input belongs in --param or --query, not a body", 2)
	}
	if len(input.Uploads) > 0 {
		if b.ContentType != "multipart/form-data" {
			return "", "", nil, fail("validation", "operation does not accept multipart upload", 2)
		}
		fields := map[string]any{}
		if len(input.Body) > 0 {
			if e := StrictJSON(input.Body, &fields); e != nil {
				return "", "", nil, e
			}
		}
		for _, file := range input.Uploads {
			f, e := os.Open(file)
			if e != nil {
				return "", "", nil, fail("io", "cannot open upload", 1)
			}
			info, e := f.Stat()
			f.Close()
			if e != nil || !info.Mode().IsRegular() {
				return "", "", nil, fail("io", "upload must be a readable regular file", 1)
			}
		}
		reader, pipe := io.Pipe()
		writer := multipart.NewWriter(pipe)
		go func() {
			var err error
			defer func() { pipe.CloseWithError(err) }()
			for k, v := range fields {
				if err = writer.WriteField(k, fmt.Sprint(v)); err != nil {
					return
				}
			}
			for name, file := range input.Uploads {
				var f *os.File
				f, err = os.Open(file)
				if err != nil {
					return
				}
				var part io.Writer
				part, err = writer.CreateFormFile(name, filepath.Base(file))
				if err == nil {
					_, err = io.Copy(part, f)
				}
				f.Close()
				if err != nil {
					return
				}
			}
			err = writer.Close()
		}()
		return path, writer.FormDataContentType(), reader, nil
	}
	if b.ContentType == "multipart/form-data" {
		return "", "", nil, fail("validation", "operation requires --upload field=path", 2)
	}
	if len(input.Body) > 0 && b.ContentType != "application/octet-stream" {
		var v any
		if e := StrictJSON(input.Body, &v); e != nil {
			return "", "", nil, e
		}
		if e := Validate(v, b.Body, catalog.Load().Schemas); e != nil {
			return "", "", nil, e
		}
	}
	return path, b.ContentType, bytes.NewReader(input.Body), nil
}

func (c *Client) Call(ctx context.Context, op catalog.Operation, input CallInput) (*http.Response, error) {
	if op.Binding == nil {
		return nil, fail("unavailable", op.BlockedReason, 4)
	}
	if c.Credential.Kind != op.Binding.Auth {
		return nil, fail("permission", "operation is registered for a different identity kind", 4)
	}
	path, content, body, e := BuildRequest(op, input)
	if e != nil {
		return nil, e
	}
	if closer, ok := body.(io.Closer); ok {
		defer closer.Close()
	}
	return c.request(ctx, op.Binding.Method, path, content, body, input.IdempotencyKey)
}
