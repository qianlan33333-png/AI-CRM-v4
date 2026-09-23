// Package adapter contains the opt-in HTTP implementation of the WeCom OAuth
// and JSSDK contracts. It never enables a provider by itself.
package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const (
	productionAPIBase = "https://qyapi.weixin.qq.com"
	// A legal batch/get_by_user page can exceed 64 KiB when 100 customer
	// profiles contain follow-user metadata. Keep the read bounded while
	// leaving enough headroom for the Provider's maximum page size.
	maxResponseBody = 2 << 20
)

var (
	ErrUnavailable = errors.New("wecom provider unavailable")
	ErrResponse    = errors.New("wecom provider response rejected")
)

// providerResponseError deliberately retains only protocol classification.
// It never carries a response body, request URL, token, or external identity.
// Existing callers can continue to match ErrResponse through Unwrap.
type providerResponseError struct {
	statusCode int
	errCode    int64
	retryable  bool
}

func (err *providerResponseError) Error() string { return ErrResponse.Error() }
func (err *providerResponseError) Unwrap() error { return ErrResponse }

type directoryReadError struct {
	cause       error
	code        string
	retryable   bool
	maxAttempts int
}

func (err *directoryReadError) Error() string                    { return err.cause.Error() }
func (err *directoryReadError) Unwrap() error                    { return err.cause }
func (err *directoryReadError) DirectoryFailureCode() string     { return err.code }
func (err *directoryReadError) DirectoryFailureRetryable() bool  { return err.retryable }
func (err *directoryReadError) DirectoryFailureMaxAttempts() int { return err.maxAttempts }

// Config is injected by the composition root. Secrets are intentionally only
// held in memory and no method in this package logs request parameters.
type Config struct {
	Enabled            bool
	CorpID             string
	AgentID            string
	Secret             string
	ContactSecret      string
	AdminCallbackURI   string
	SidebarCallbackURI string
	APIBase            string
	HTTPClient         *http.Client // explicit test injection also permits httptest bases
	// UploadTimeout is isolated from message/read request timeouts. Zero keeps
	// the production-safe 120 second default for large media uploads.
	UploadTimeout time.Duration
	Now           func() time.Time
	Random        func([]byte) error
	JSAPIList     []string
}

type credential struct {
	value     string
	expiresAt time.Time
}

// Client implements both the OAuth client and the typed JSSDK signer.
// Cache reads/writes are mutex-protected; it has neither a ticker nor a
// background goroutine.
type Client struct {
	config  Config
	apiBase *url.URL
	http    *http.Client
	mu      sync.Mutex
	tokens  map[string]credential
}

func New(config Config) (*Client, error) {
	if !config.Enabled {
		return &Client{config: config, http: config.HTTPClient, tokens: map[string]credential{}}, nil
	}
	if invalid(config.CorpID) || invalid(config.AgentID) || invalid(config.Secret) || !validCallback(config.AdminCallbackURI) || !validCallback(config.SidebarCallbackURI) {
		return nil, ErrUnavailable
	}
	return newConfiguredClient(config)
}

// NewDirectory creates the narrower external-contact client used by readback
// and directory adapters. OAuth callback and application-secret validation do
// not belong to this capability; its token is issued from ContactSecret.
func NewDirectory(config Config) (*Client, error) {
	if !config.Enabled {
		return &Client{config: config, http: config.HTTPClient, tokens: map[string]credential{}}, nil
	}
	if invalid(config.CorpID) || invalid(config.ContactSecret) {
		return nil, ErrUnavailable
	}
	return newConfiguredClient(config)
}

func newConfiguredClient(config Config) (*Client, error) {
	base, err := validatedAPIBase(config.APIBase, config.HTTPClient != nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	} else {
		// Do not follow an untrusted provider redirect to another host. Copying
		// keeps injected test transports intact without mutating a shared client.
		copy := *client
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	return &Client{config: config, apiBase: base, http: client, tokens: map[string]credential{}}, nil
}

func (client *Client) Ready() bool {
	return client != nil && client.config.Enabled && client.apiBase != nil && client.http != nil
}

func (client *Client) DirectoryReady() bool {
	return client.Ready() && !invalid(client.config.ContactSecret)
}

func (client *Client) ListContactStaff(ctx context.Context) ([]string, error) {
	if !client.DirectoryReady() {
		return nil, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return nil, classifyDirectoryReadError(err)
	}
	payload, err := client.listContactStaff(ctx, token)
	if directoryTokenExpired(err) {
		token, err = client.refreshDirectoryToken(ctx)
		if err == nil {
			payload, err = client.listContactStaff(ctx, token)
		}
		if err != nil {
			return nil, classifyDirectoryRefreshError(err)
		}
	}
	if err != nil || len(payload.FollowUser) == 0 {
		if err != nil {
			return nil, classifyDirectoryReadError(err)
		}
		return nil, classifyDirectoryReadError(ErrResponse)
	}
	var followUsers []string
	if json.Unmarshal(payload.FollowUser, &followUsers) != nil || followUsers == nil {
		return nil, classifyDirectoryReadError(ErrResponse)
	}
	seen := map[string]struct{}{}
	staff := make([]string, 0, len(followUsers))
	for _, value := range followUsers {
		value = strings.TrimSpace(value)
		if value == "" || invalid(value) {
			return nil, classifyDirectoryReadError(ErrResponse)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		staff = append(staff, value)
	}
	return staff, nil
}

// ReadContactStaffProfiles reads display names only for the already-authorized
// follow-user subset supplied by the caller. It never changes local staff
// records, roles, or eligibility. A failed profile read is returned as an
// aggregate status so callers can retain an existing verified display name.
func (client *Client) ReadContactStaffProfiles(ctx context.Context, userIDs []string) (wecomport.ContactStaffProfileSnapshot, error) {
	if !client.DirectoryReady() {
		return wecomport.ContactStaffProfileSnapshot{}, wecomport.ErrDirectoryDisabled
	}
	if len(userIDs) == 0 || len(userIDs) > 100 {
		return wecomport.ContactStaffProfileSnapshot{}, classifyDirectoryReadError(ErrResponse)
	}
	seen := make(map[string]struct{}, len(userIDs))
	ids := make([]string, 0, len(userIDs))
	for _, raw := range userIDs {
		id := strings.TrimSpace(raw)
		if id == "" || id != raw || invalid(id) {
			return wecomport.ContactStaffProfileSnapshot{}, classifyDirectoryReadError(ErrResponse)
		}
		if _, duplicate := seen[id]; duplicate {
			return wecomport.ContactStaffProfileSnapshot{}, classifyDirectoryReadError(ErrResponse)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	// One bounded refresh budget covers the entire page, rather than allowing
	// one eight-second request budget per employee. Token refresh retries only
	// the exact user/get that reported an expired token.
	readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	token, err := client.contactAccessToken(readCtx)
	if err != nil {
		return wecomport.ContactStaffProfileSnapshot{}, classifyDirectoryReadError(err)
	}
	// Bound concurrency as well as elapsed time: sequential reads repeatedly
	// exhausted the page budget before reaching employees later in the list.
	type profileResult struct {
		profile wecomport.ContactStaffProfile
		err     error
	}
	results := make([]profileResult, len(ids))
	jobs := make(chan int)
	var tokenMu sync.Mutex
	var workers sync.WaitGroup
	for worker := 0; worker < min(4, len(ids)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				id := ids[index]
				tokenMu.Lock()
				readToken := token
				tokenMu.Unlock()
				payload, readErr := client.request(readCtx, "/cgi-bin/user/get", url.Values{"access_token": {readToken}, "userid": {id}})
				if directoryTokenExpired(readErr) {
					tokenMu.Lock()
					if token == readToken {
						readToken, readErr = client.refreshDirectoryToken(readCtx)
						if readErr == nil {
							token = readToken
						}
					} else {
						readToken, readErr = token, nil
					}
					tokenMu.Unlock()
					if readErr == nil {
						payload, readErr = client.request(readCtx, "/cgi-bin/user/get", url.Values{"access_token": {readToken}, "userid": {id}})
					}
					if readErr != nil {
						readErr = classifyDirectoryRefreshError(readErr)
					}
				}
				if readErr != nil {
					results[index].err = readErr
					continue
				}
				returnedID := strings.TrimSpace(payload.UserIDLower)
				name := strings.TrimSpace(payload.Name)
				if (returnedID != "" && returnedID != id) || !validDisplayName(name) {
					results[index].err = ErrResponse
					continue
				}
				results[index].profile = wecomport.ContactStaffProfile{UserID: id, DisplayName: name}
			}
		}()
	}
	for index := range ids {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	snapshot := wecomport.ContactStaffProfileSnapshot{Items: make([]wecomport.ContactStaffProfile, 0, len(ids)), ProfileReadState: "ready"}
	for _, result := range results {
		if result.err != nil {
			recordProfileReadFailure(&snapshot, result.err)
			continue
		}
		snapshot.Items = append(snapshot.Items, result.profile)
	}
	return snapshot, nil
}

func recordProfileReadFailure(snapshot *wecomport.ContactStaffProfileSnapshot, cause error) {
	if snapshot == nil {
		return
	}
	snapshot.ProfileReadState = "unavailable"
	classified := classifyDirectoryReadError(cause)
	var failure wecomport.DirectoryFailure
	if errors.As(classified, &failure) && failure.DirectoryFailureCode() != "" {
		snapshot.ProfileErrorCode = failure.DirectoryFailureCode()
		return
	}
	snapshot.ProfileErrorCode = "provider_profile_unavailable"
}

func validDisplayName(value string) bool {
	return value != "" && len([]rune(value)) <= 160 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (client *Client) BatchExternalContacts(ctx context.Context, staffID, cursor string, limit int) (wecomport.ExternalContactPage, error) {
	if !client.DirectoryReady() {
		return wecomport.ExternalContactPage{}, wecomport.ErrDirectoryDisabled
	}
	if invalid(staffID) || strings.TrimSpace(cursor) != cursor || limit < 1 || limit > 100 {
		return wecomport.ExternalContactPage{}, ErrResponse
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.ExternalContactPage{}, classifyDirectoryReadError(err)
	}
	body, err := json.Marshal(map[string]any{"userid_list": []string{staffID}, "cursor": cursor, "limit": limit})
	if err != nil {
		return wecomport.ExternalContactPage{}, ErrResponse
	}
	payload, err := client.batchExternalContacts(ctx, token, body)
	if directoryTokenExpired(err) {
		token, err = client.refreshDirectoryToken(ctx)
		if err == nil {
			payload, err = client.batchExternalContacts(ctx, token, body)
		}
		if err != nil {
			return wecomport.ExternalContactPage{}, classifyDirectoryRefreshError(err)
		}
	}
	if err != nil {
		return wecomport.ExternalContactPage{}, classifyDirectoryReadError(err)
	}
	page := wecomport.ExternalContactPage{Contacts: make([]wecomport.ExternalContact, 0, len(payload.ExternalContactList)), NextCursor: strings.TrimSpace(payload.NextCursor)}
	for _, item := range payload.ExternalContactList {
		contact := item.ExternalContact
		contact.ExternalUserID = strings.TrimSpace(contact.ExternalUserID)
		if contact.ExternalUserID == "" || contact.Gender < 0 || contact.Gender > 2 || contact.Type < 0 || contact.Type > 3 {
			return wecomport.ExternalContactPage{}, classifyDirectoryReadError(ErrResponse)
		}
		followInfo := make([]wecomport.ExternalContactFollowInfo, 0, 1)
		if item.FollowInfo != nil {
			follow := *item.FollowInfo
			follow.UserID = strings.TrimSpace(follow.UserID)
			if follow.UserID == "" || invalid(follow.UserID) {
				return wecomport.ExternalContactPage{}, classifyDirectoryReadError(ErrResponse)
			}
			remark, remarkErr := externalContactRemark(follow.Remark)
			if remarkErr != nil {
				return wecomport.ExternalContactPage{}, classifyDirectoryReadError(ErrResponse)
			}
			description, descriptionProjected, descriptionErr := externalContactDescription(follow.Description)
			if descriptionErr != nil {
				return wecomport.ExternalContactPage{}, classifyDirectoryReadError(ErrResponse)
			}
			value := wecomport.ExternalContactFollowInfo{EmployeeID: follow.UserID, Remark: remark, Description: description, DescriptionProjected: descriptionProjected, Tags: make([]wecomport.ExternalContactTag, 0, len(follow.Tags))}
			for _, tag := range follow.Tags {
				tag.ID, tag.Name = strings.TrimSpace(tag.ID), strings.TrimSpace(tag.Name)
				if tag.ID == "" || invalid(tag.ID) || invalidOptional(tag.Name) || tag.Type < 1 || tag.Type > 2 {
					return wecomport.ExternalContactPage{}, classifyDirectoryReadError(ErrResponse)
				}
				value.Tags = append(value.Tags, wecomport.ExternalContactTag{ProviderTagID: tag.ID, Name: tag.Name, Type: tag.Type})
			}
			followInfo = append(followInfo, value)
		}
		page.Contacts = append(page.Contacts, wecomport.ExternalContact{ExternalUserID: contact.ExternalUserID,
			Name: strings.TrimSpace(contact.Name), AvatarURL: strings.TrimSpace(contact.Avatar), Gender: contact.Gender,
			Type: contact.Type, CorpName: strings.TrimSpace(contact.CorpName), UnionID: strings.TrimSpace(contact.UnionID), FollowInfo: followInfo})
	}
	return page, nil
}

// ReadExternalContact reads one known external contact for a post-write
// observation. It shares the directory read credential path and does not make
// any Provider write.
func (client *Client) ReadExternalContact(ctx context.Context, externalUserID string) (wecomport.ExternalContact, error) {
	if !client.DirectoryReady() || invalid(externalUserID) {
		return wecomport.ExternalContact{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.ExternalContact{}, classifyDirectoryReadError(err)
	}
	payload, err := client.request(ctx, "/cgi-bin/externalcontact/get", url.Values{"access_token": {token}, "external_userid": {externalUserID}})
	if directoryTokenExpired(err) {
		token, err = client.refreshDirectoryToken(ctx)
		if err == nil {
			payload, err = client.request(ctx, "/cgi-bin/externalcontact/get", url.Values{"access_token": {token}, "external_userid": {externalUserID}})
		}
		if err != nil {
			return wecomport.ExternalContact{}, classifyDirectoryRefreshError(err)
		}
	}
	if err != nil {
		return wecomport.ExternalContact{}, classifyDirectoryReadError(err)
	}
	contact := payload.ExternalContact
	contact.ExternalUserID = strings.TrimSpace(contact.ExternalUserID)
	if contact.ExternalUserID != externalUserID || contact.Gender < 0 || contact.Gender > 2 || contact.Type < 0 || contact.Type > 3 {
		return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
	}
	var rawFollows []struct {
		UserID      string          `json:"userid"`
		Remark      *string         `json:"remark"`
		Description json.RawMessage `json:"description"`
		Tags        []struct {
			ID   string `json:"tag_id"`
			Name string `json:"name"`
			Type int16  `json:"type"`
		} `json:"tags"`
	}
	if len(payload.FollowUser) == 0 || json.Unmarshal(payload.FollowUser, &rawFollows) != nil || rawFollows == nil {
		return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
	}
	followInfo := make([]wecomport.ExternalContactFollowInfo, 0, len(rawFollows))
	for _, follow := range rawFollows {
		follow.UserID = strings.TrimSpace(follow.UserID)
		if invalid(follow.UserID) {
			return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
		}
		remark, remarkErr := externalContactRemark(follow.Remark)
		if remarkErr != nil {
			return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
		}
		description, descriptionProjected, descriptionErr := externalContactDescription(follow.Description)
		if descriptionErr != nil {
			return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
		}
		entry := wecomport.ExternalContactFollowInfo{EmployeeID: follow.UserID, Remark: remark, Description: description, DescriptionProjected: descriptionProjected, Tags: make([]wecomport.ExternalContactTag, 0, len(follow.Tags))}
		for _, tag := range follow.Tags {
			tag.ID, tag.Name = strings.TrimSpace(tag.ID), strings.TrimSpace(tag.Name)
			if invalid(tag.ID) || invalidOptional(tag.Name) || tag.Type < 1 || tag.Type > 2 {
				return wecomport.ExternalContact{}, classifyDirectoryReadError(ErrResponse)
			}
			entry.Tags = append(entry.Tags, wecomport.ExternalContactTag{ProviderTagID: tag.ID, Name: tag.Name, Type: tag.Type})
		}
		followInfo = append(followInfo, entry)
	}
	return wecomport.ExternalContact{ExternalUserID: contact.ExternalUserID, Name: strings.TrimSpace(contact.Name), AvatarURL: strings.TrimSpace(contact.Avatar), Gender: contact.Gender, Type: contact.Type, CorpName: strings.TrimSpace(contact.CorpName), UnionID: strings.TrimSpace(contact.UnionID), FollowInfo: followInfo}, nil
}

// ReadExternalContactDescriptionTarget intentionally projects only the detail
// needed by the description effect. The general contact reader remains strict
// for consumers which rely on all follow relationships and tags; a malformed
// unrelated tag must not invalidate a target description read.
func (client *Client) ReadExternalContactDescriptionTarget(ctx context.Context, externalUserID, employeeUserID string) (wecomport.ExternalContactDescriptionTarget, error) {
	if !client.DirectoryReady() || invalid(externalUserID) || invalid(employeeUserID) {
		return wecomport.ExternalContactDescriptionTarget{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(err)
	}
	payload, err := client.requestExternalContactDescriptionTarget(ctx, externalUserID, token)
	if directoryTokenExpired(err) {
		token, err = client.refreshDirectoryToken(ctx)
		if err == nil {
			payload, err = client.requestExternalContactDescriptionTarget(ctx, externalUserID, token)
		}
		if err != nil {
			return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryRefreshError(err)
		}
	}
	if err != nil {
		return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(err)
	}
	if payload.ExternalContact.ExternalUserID != externalUserID {
		return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(ErrResponse)
	}
	var follows []json.RawMessage
	if len(payload.FollowUser) == 0 || json.Unmarshal(payload.FollowUser, &follows) != nil || follows == nil {
		return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(ErrResponse)
	}

	var result wecomport.ExternalContactDescriptionTarget
	matched := 0
	for _, rawFollow := range follows {
		var follow struct {
			UserID      json.RawMessage `json:"userid"`
			Description json.RawMessage `json:"description"`
		}
		if json.Unmarshal(rawFollow, &follow) != nil {
			continue
		}
		var userID string
		if len(follow.UserID) == 0 || json.Unmarshal(follow.UserID, &userID) != nil || userID != employeeUserID {
			continue
		}
		matched++
		if matched > 1 {
			return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(ErrResponse)
		}
		description, projected, descriptionErr := externalContactDescription(follow.Description)
		if descriptionErr != nil {
			return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(ErrResponse)
		}
		if projected {
			result.Description = *description
			result.Projected = true
		}
	}
	if matched != 1 {
		return wecomport.ExternalContactDescriptionTarget{}, classifyDirectoryReadError(ErrResponse)
	}
	return result, nil
}

type externalContactDescriptionTargetResponse struct {
	ErrCode         json.RawMessage `json:"errcode"`
	ExternalContact struct {
		ExternalUserID string `json:"external_userid"`
	} `json:"external_contact"`
	FollowUser json.RawMessage `json:"follow_user"`
}

func (client *Client) requestExternalContactDescriptionTarget(ctx context.Context, externalUserID, token string) (externalContactDescriptionTargetResponse, error) {
	raw, err := client.requestRawJSON(ctx, http.MethodGet, "/cgi-bin/externalcontact/get", url.Values{"access_token": {token}, "external_userid": {externalUserID}}, nil)
	if err != nil {
		return externalContactDescriptionTargetResponse{}, err
	}
	var payload externalContactDescriptionTargetResponse
	if json.Unmarshal(raw.body, &payload) != nil {
		return externalContactDescriptionTargetResponse{}, &providerResponseError{statusCode: raw.statusCode}
	}
	if !successErrCode(payload.ErrCode) {
		return externalContactDescriptionTargetResponse{}, &providerResponseError{statusCode: raw.statusCode, errCode: providerErrCode(payload.ErrCode)}
	}
	return payload, nil
}

func (client *Client) listContactStaff(ctx context.Context, token string) (response, error) {
	return client.request(ctx, "/cgi-bin/externalcontact/get_follow_user_list", url.Values{"access_token": {token}})
}

func (client *Client) batchExternalContacts(ctx context.Context, token string, body []byte) (response, error) {
	return client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/batch/get_by_user", url.Values{"access_token": {token}}, body)
}

type MessageWriteError struct {
	Err           error
	CallAttempted bool
}

func (e *MessageWriteError) Error() string               { return e.Err.Error() }
func (e *MessageWriteError) Unwrap() error               { return e.Err }
func (e *MessageWriteError) ProviderCallAttempted() bool { return e.CallAttempted }

// SendExternalContactText performs the sole Automation Operations WeCom write.
// The caller keeps sender/customer provider identifiers ephemeral and EER
// records only the returned receipt digest.
func (client *Client) SendExternalContactText(ctx context.Context, senderUserID, externalUserID, content string) (string, error) {
	if !client.DirectoryReady() || invalid(senderUserID) || invalid(externalUserID) || strings.TrimSpace(content) == "" || len([]rune(content)) > 4000 || strings.ContainsRune(content, '\x00') {
		return "", &MessageWriteError{Err: ErrUnavailable}
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return "", &MessageWriteError{Err: err}
	}
	body, err := json.Marshal(map[string]any{"chat_type": "single", "external_userid": []string{externalUserID}, "sender": senderUserID, "text": map[string]string{"content": content}})
	if err != nil {
		return "", &MessageWriteError{Err: ErrResponse}
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/add_msg_template", url.Values{"access_token": {token}}, body)
	if err != nil {
		return "", &MessageWriteError{Err: err, CallAttempted: true}
	}
	payload.MessageID = strings.TrimSpace(payload.MessageID)
	if payload.MessageID == "" || invalid(payload.MessageID) {
		return "", &MessageWriteError{Err: ErrResponse, CallAttempted: true}
	}
	return payload.MessageID, nil
}

func (client *Client) AuthorizationURL(_ context.Context, purpose wecom.OAuthPurpose, mode wecom.OAuthMode, state, _ string) (string, error) {
	if !client.Ready() || state == "" {
		return "", ErrUnavailable
	}
	callback := client.config.AdminCallbackURI
	if purpose == wecom.OAuthSidebar {
		callback = client.config.SidebarCallbackURI
	}
	if !validCallback(callback) {
		return "", ErrUnavailable
	}
	values := url.Values{"appid": {client.config.CorpID}, "redirect_uri": {callback}, "state": {state}}
	switch {
	case purpose == wecom.OAuthAdmin && mode == wecom.OAuthModeQR:
		values.Set("agentid", client.config.AgentID)
		return "https://open.work.weixin.qq.com/wwopen/sso/qrConnect?" + values.Encode(), nil
	case (purpose == wecom.OAuthAdmin || purpose == wecom.OAuthSidebar) && mode == wecom.OAuthModeWeb:
		values.Set("response_type", "code")
		values.Set("scope", "snsapi_base")
		return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect", nil
	default:
		return "", ErrUnavailable
	}
}

func (client *Client) ExchangeCode(ctx context.Context, _ wecom.OAuthPurpose, _ wecom.OAuthMode, code string) (wecom.OAuthIdentity, error) {
	if !client.Ready() || invalid(code) {
		return wecom.OAuthIdentity{}, ErrUnavailable
	}
	token, err := client.accessToken(ctx)
	if err != nil {
		return wecom.OAuthIdentity{}, err
	}
	payload, err := client.request(ctx, "/cgi-bin/user/getuserinfo", url.Values{"access_token": {token}, "code": {code}})
	userID := payload.UserID
	if userID == "" {
		userID = payload.UserIDLower
	}
	if err != nil || userID == "" {
		return wecom.OAuthIdentity{}, ErrResponse
	}
	return wecom.OAuthIdentity{CorpID: client.config.CorpID, EmployeeID: userID}, nil
}

func (client *Client) ConfigForURL(ctx context.Context, rawURL string) (wecom.JSSDKConfig, error) {
	if !client.Ready() {
		return wecom.JSSDKConfig{}, ErrUnavailable
	}
	signedURL, err := exactNoFragmentURL(rawURL)
	if err != nil {
		return wecom.JSSDKConfig{}, ErrResponse
	}
	token, err := client.accessToken(ctx)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	corpTicket, err := client.ticket(ctx, token, "corp")
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	agentTicket, err := client.ticket(ctx, token, "agent")
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	config, err := client.sign(signedURL, corpTicket)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	agentConfig, err := client.sign(signedURL, agentTicket)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	return wecom.JSSDKConfig{CorpID: client.config.CorpID, AgentID: client.config.AgentID, Config: config, AgentConfig: agentConfig}, nil
}

func (client *Client) accessToken(ctx context.Context) (string, error) {
	if value, ok := client.cached("access_token"); ok {
		return value, nil
	}
	payload, err := client.request(ctx, "/cgi-bin/gettoken", url.Values{"corpid": {client.config.CorpID}, "corpsecret": {client.config.Secret}})
	if err != nil || payload.AccessToken == "" || payload.ExpiresIn <= 0 {
		return "", ErrResponse
	}
	client.store("access_token", payload.AccessToken, payload.ExpiresIn)
	return payload.AccessToken, nil
}

func (client *Client) contactAccessToken(ctx context.Context) (string, error) {
	if value, ok := client.cached("contact_access_token"); ok {
		return value, nil
	}
	payload, err := client.request(ctx, "/cgi-bin/gettoken", url.Values{"corpid": {client.config.CorpID}, "corpsecret": {client.config.ContactSecret}})
	if err != nil || payload.AccessToken == "" || payload.ExpiresIn <= 0 {
		return "", ErrResponse
	}
	client.store("contact_access_token", payload.AccessToken, payload.ExpiresIn)
	return payload.AccessToken, nil
}

func (client *Client) ticket(ctx context.Context, token, kind string) (string, error) {
	key, path := "jsapi_ticket", "/cgi-bin/get_jsapi_ticket"
	query := url.Values{"access_token": {token}}
	if kind == "agent" {
		key, path = "agent_jsapi_ticket", "/cgi-bin/ticket/get"
		query.Set("type", "agent_config")
	}
	if value, ok := client.cached(key); ok {
		return value, nil
	}
	payload, err := client.request(ctx, path, query)
	if err != nil || payload.Ticket == "" || payload.ExpiresIn <= 0 {
		return "", ErrResponse
	}
	client.store(key, payload.Ticket, payload.ExpiresIn)
	return payload.Ticket, nil
}

func (client *Client) sign(signedURL, ticket string) (wecom.JSSDKSignature, error) {
	bytes := make([]byte, 16)
	if err := client.random()(bytes); err != nil {
		return wecom.JSSDKSignature{}, ErrUnavailable
	}
	nonce := hex.EncodeToString(bytes)
	timestamp := client.now().Unix()
	plain := "jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + itoa(timestamp) + "&url=" + signedURL
	sum := sha1.Sum([]byte(plain))
	apis := append([]string(nil), client.config.JSAPIList...)
	if len(apis) == 0 {
		apis = []string{"getCurExternalContact", "sendChatMessage"}
	}
	return wecom.JSSDKSignature{Timestamp: timestamp, NonceStr: nonce, Signature: hex.EncodeToString(sum[:]), JSAPIList: apis}, nil
}

type response struct {
	DepartmentUsers []struct {
		UserID string `json:"userid"`
	} `json:"dept_user"`
	Departments []struct {
		ID       int64 `json:"id"`
		ParentID int64 `json:"parentid"`
	} `json:"department_id"`
	// agent/get has a different shape from department/simplelist. Keep these
	// raw until the enterprise-directory reader validates its bounded,
	// minimum projection; an unknown permission shape must never be treated as
	// an empty visible scope.
	AllowUserInfos json.RawMessage `json:"allow_userinfos"`
	AllowPartys    json.RawMessage `json:"allow_partys"`
	AllowTags      json.RawMessage `json:"allow_tags"`
	Users          []struct {
		UserID string `json:"userid"`
		Name   string `json:"name"`
	} `json:"userlist"`
	ErrCode     json.RawMessage `json:"errcode"`
	AccessToken string          `json:"access_token"`
	UserID      string          `json:"UserId"`
	UserIDLower string          `json:"userid"`
	Name        string          `json:"name"`
	Ticket      string          `json:"ticket"`
	ExpiresIn   int64           `json:"expires_in"`
	TagGroups   json.RawMessage `json:"tag_group"`
	FollowUser  json.RawMessage `json:"follow_user"`
	NextCursor  string          `json:"next_cursor"`
	JoinWay     struct {
		ConfigID       string   `json:"config_id"`
		Scene          int      `json:"scene"`
		AutoCreateRoom int      `json:"auto_create_room"`
		ChatIDs        []string `json:"chat_id_list"`
		QRCode         string   `json:"qr_code"`
	} `json:"join_way"`
	ConfigID   string `json:"config_id"`
	QRCode     string `json:"qr_code"`
	ContactWay struct {
		ConfigID string `json:"config_id"`
		QRCode   string `json:"qr_code"`
	} `json:"contact_way"`
	LinkID     string   `json:"link_id"`
	URL        string   `json:"url"`
	LinkIDList []string `json:"link_id_list"`
	Link       struct {
		LinkID     string `json:"link_id"`
		LinkName   string `json:"link_name"`
		URL        string `json:"url"`
		SkipVerify bool   `json:"skip_verify"`
	} `json:"link"`
	Range struct {
		UserIDs       []string `json:"user_list"`
		DepartmentIDs []int64  `json:"department_list"`
	} `json:"range"`
	MediaID   string   `json:"media_id"`
	MessageID string   `json:"msgid"`
	FailList  []string `json:"fail_list"`
	SendList  []struct {
		ExternalUserID string `json:"external_userid"`
		SendTime       int64  `json:"send_time"`
		UserID         string `json:"userid"`
		ChatID         string `json:"chat_id"`
		Status         int    `json:"status"`
	} `json:"send_list"`
	GroupChatList []struct {
		ChatID string `json:"chat_id"`
		Status int    `json:"status"`
	} `json:"group_chat_list"`
	GroupChat struct {
		ChatID  string `json:"chat_id"`
		Owner   string `json:"owner"`
		Name    string `json:"name"`
		Members []struct {
			UserID string `json:"userid"`
			Type   int    `json:"type"`
		} `json:"member_list"`
	} `json:"group_chat"`
	ExternalContact struct {
		ExternalUserID string `json:"external_userid"`
		Name           string `json:"name"`
		Avatar         string `json:"avatar"`
		Type           int16  `json:"type"`
		Gender         int16  `json:"gender"`
		UnionID        string `json:"unionid"`
		CorpName       string `json:"corp_name"`
	} `json:"external_contact"`
	Customer []struct {
		ExternalUserID string          `json:"external_userid"`
		ErrCode        json.RawMessage `json:"errcode"`
		Status         json.RawMessage `json:"status"`
		TakeoverTime   json.RawMessage `json:"takeover_time"`
	} `json:"customer"`
	ExternalContactList []struct {
		ExternalContact struct {
			ExternalUserID string `json:"external_userid"`
			Name           string `json:"name"`
			Avatar         string `json:"avatar"`
			Type           int16  `json:"type"`
			Gender         int16  `json:"gender"`
			UnionID        string `json:"unionid"`
			CorpName       string `json:"corp_name"`
		} `json:"external_contact"`
		// batch/get_by_user is called with one staff ID, and WeCom returns the
		// corresponding relationship as one object rather than an array.
		FollowInfo *struct {
			UserID      string          `json:"userid"`
			Remark      *string         `json:"remark"`
			Description json.RawMessage `json:"description"`
			Tags        []struct {
				ID   string `json:"tag_id"`
				Name string `json:"tag_name"`
				Type int16  `json:"type"`
			} `json:"tags"`
		} `json:"follow_info"`
	} `json:"external_contact_list"`
}

// ListGroupChats and GetGroupChat are the narrow, read-only group directory
// protocol used by Group Ops. They use the customer-contact token and do not
// mutate any provider record.
func (client *Client) ListGroupChats(ctx context.Context, ownerUserID, cursor string, limit int) (result wecomport.GroupChatPage, err error) {
	defer func() { err = classifyGroupDirectoryReadError(err) }()
	if !client.DirectoryReady() || invalid(ownerUserID) || strings.TrimSpace(cursor) != cursor || limit < 1 || limit > 100 {
		return wecomport.GroupChatPage{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.GroupChatPage{}, err
	}
	body, err := json.Marshal(map[string]any{"status_filter": 0, "owner_filter": map[string]any{"userid_list": []string{ownerUserID}}, "cursor": cursor, "limit": limit})
	if err != nil {
		return wecomport.GroupChatPage{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/list", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.GroupChatPage{}, err
	}
	page := wecomport.GroupChatPage{Items: make([]wecomport.GroupChatListItem, 0, len(payload.GroupChatList)), NextCursor: strings.TrimSpace(payload.NextCursor)}
	seen := make(map[string]struct{}, len(payload.GroupChatList))
	for _, item := range payload.GroupChatList {
		chatID := strings.TrimSpace(item.ChatID)
		if invalid(chatID) || item.Status < 0 || item.Status > 3 {
			return wecomport.GroupChatPage{}, ErrResponse
		}
		if _, exists := seen[chatID]; exists {
			return wecomport.GroupChatPage{}, ErrResponse
		}
		seen[chatID] = struct{}{}
		page.Items = append(page.Items, wecomport.GroupChatListItem{ChatID: chatID, Status: item.Status})
	}
	return page, nil
}

func (client *Client) ListAllGroupChats(ctx context.Context, cursor string, limit int) (result wecomport.GroupChatPage, err error) {
	defer func() { err = classifyGroupDirectoryReadError(err) }()
	if !client.DirectoryReady() || strings.TrimSpace(cursor) != cursor || limit < 1 || limit > 100 {
		return wecomport.GroupChatPage{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.GroupChatPage{}, err
	}
	body, err := json.Marshal(map[string]any{"status_filter": 0, "cursor": cursor, "limit": limit})
	if err != nil {
		return wecomport.GroupChatPage{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/list", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.GroupChatPage{}, err
	}
	page := wecomport.GroupChatPage{Items: make([]wecomport.GroupChatListItem, 0, len(payload.GroupChatList)), NextCursor: strings.TrimSpace(payload.NextCursor)}
	seen := make(map[string]struct{}, len(payload.GroupChatList))
	for _, item := range payload.GroupChatList {
		chatID := strings.TrimSpace(item.ChatID)
		if invalid(chatID) || item.Status < 0 || item.Status > 3 {
			return wecomport.GroupChatPage{}, ErrResponse
		}
		if _, exists := seen[chatID]; exists {
			return wecomport.GroupChatPage{}, ErrResponse
		}
		seen[chatID] = struct{}{}
		page.Items = append(page.Items, wecomport.GroupChatListItem{ChatID: chatID, Status: item.Status})
	}
	return page, nil
}

func (client *Client) GetGroupChat(ctx context.Context, chatID string) (result wecomport.GroupChat, err error) {
	defer func() { err = classifyGroupDirectoryReadError(err) }()
	if !client.DirectoryReady() || invalid(chatID) {
		return wecomport.GroupChat{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.GroupChat{}, err
	}
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "need_name": 1})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.GroupChat{}, err
	}
	var externalCount int32
	knownTypes := true
	for _, member := range payload.GroupChat.Members {
		if member.Type == 2 {
			externalCount++
		} else if member.Type != 1 {
			knownTypes = false
		}
	}
	value := wecomport.GroupChat{ChatID: strings.TrimSpace(payload.GroupChat.ChatID), OwnerUserID: strings.TrimSpace(payload.GroupChat.Owner), Name: strings.TrimSpace(payload.GroupChat.Name), MemberCount: len(payload.GroupChat.Members)}
	if knownTypes {
		value.ExternalMemberCount = &externalCount
	}
	// An unnamed group is still a valid directory record. Keep its name empty
	// rather than inventing a title from the chat ID or rejecting the snapshot.
	if invalid(value.ChatID) || invalid(value.OwnerUserID) || strings.TrimSpace(value.Name) != value.Name || value.MemberCount < 0 {
		return wecomport.GroupChat{}, ErrResponse
	}
	return value, nil
}

// CreateContactWay is the bounded customer-contact QR write used only by the
// Outbound adapter after EER has durably recorded an attempted state.
func (client *Client) CreateContactWay(ctx context.Context, input wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	if !client.DirectoryReady() || !validAcquisitionRequest(input) {
		return wecomport.AcquisitionAssetResult{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(err, false)
	}
	body, err := json.Marshal(map[string]any{"type": 2, "scene": 2, "style": 1, "remark": input.Name, "skip_verify": input.SkipVerify, "state": input.State, "user": input.StaffUserIDs})
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/add_contact_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, wrapAcquisitionWriteError(err)
	}
	payload.ConfigID = strings.TrimSpace(payload.ConfigID)
	payload.QRCode = strings.TrimSpace(payload.QRCode)
	if invalid(payload.ConfigID) || !validProviderHTTPS(payload.QRCode) {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
	}
	return wecomport.AcquisitionAssetResult{ProviderAssetRef: payload.ConfigID, URL: payload.QRCode}, nil
}

// wrapAcquisitionWriteError keeps a completed WeCom rejection distinct from a
// transport ambiguity. The old generic wrapper marked every post-request
// error outcome_unknown, which hid actionable permission/configuration codes
// and left every newly generated channel QR without a diagnosable result.
func wrapAcquisitionWriteError(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *providerResponseError
	if errors.As(err, &providerErr) && providerErr.errCode != 0 {
		return wecomport.WrapProviderWriteDispositionWithCode(err, true, false, providerErr.retryable, providerErr.errCode)
	}
	return wecomport.WrapProviderWriteError(err, true)
}

func (client *Client) GetContactWay(ctx context.Context, configID string) (wecomport.AcquisitionAssetResult, error) {
	if !client.DirectoryReady() || invalid(configID) {
		return wecomport.AcquisitionAssetResult{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, err
	}
	body, _ := json.Marshal(map[string]string{"config_id": configID})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/get_contact_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, err
	}
	returnedID := strings.TrimSpace(payload.ContactWay.ConfigID)
	if returnedID == "" {
		returnedID = strings.TrimSpace(payload.ConfigID)
	}
	qrCode := strings.TrimSpace(payload.ContactWay.QRCode)
	if qrCode == "" {
		qrCode = strings.TrimSpace(payload.QRCode)
	}
	if returnedID != configID || !validProviderHTTPS(qrCode) {
		return wecomport.AcquisitionAssetResult{}, ErrResponse
	}
	return wecomport.AcquisitionAssetResult{ProviderAssetRef: returnedID, URL: qrCode}, nil
}

func (client *Client) UpdateContactWay(ctx context.Context, configID string, input wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	if !client.DirectoryReady() || invalid(configID) || !validAcquisitionRequest(input) {
		return wecomport.AcquisitionAssetResult{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]any{"config_id": configID, "type": 2, "scene": 2, "style": 1, "remark": input.Name, "skip_verify": input.SkipVerify, "state": input.State, "user": input.StaffUserIDs})
	if _, err = client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/update_contact_way", url.Values{"access_token": {token}}, body); err != nil {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(err, true)
	}
	result, err := client.GetContactWay(ctx, configID)
	return result, wecomport.WrapProviderWriteError(err, true)
}

func (client *Client) DeleteContactWay(ctx context.Context, configID string) error {
	if !client.DirectoryReady() || invalid(configID) {
		return ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]string{"config_id": configID})
	_, err = client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/del_contact_way", url.Values{"access_token": {token}}, body)
	return wecomport.WrapProviderWriteError(err, true)
}

func (client *Client) CreateCustomerAcquisitionLink(ctx context.Context, input wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	if !client.DirectoryReady() || !validAcquisitionRequest(input) {
		return wecomport.AcquisitionAssetResult{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(err, false)
	}
	body, err := json.Marshal(map[string]any{"link_name": input.Name, "range": map[string]any{"user_list": input.StaffUserIDs}})
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/create_link", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(err, true)
	}
	payload.LinkID = strings.TrimSpace(payload.LinkID)
	payload.URL = strings.TrimSpace(payload.URL)
	if invalid(payload.LinkID) || !validProviderHTTPS(payload.URL) {
		return wecomport.AcquisitionAssetResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
	}
	return wecomport.AcquisitionAssetResult{ProviderAssetRef: payload.LinkID, URL: payload.URL}, nil
}

// SendWelcomeMessage consumes the callback-issued WelcomeCode exactly once.
// The code is never logged or persisted by this adapter.
func (client *Client) SendWelcomeMessage(ctx context.Context, welcomeCode, text string, attachments []wecomport.WelcomeAttachment) error {
	if !client.DirectoryReady() || invalid(welcomeCode) || len([]rune(text)) > 4000 || len(attachments) > 9 || (text == "" && len(attachments) == 0) {
		return wecomport.WrapProviderWriteError(ErrUnavailable, false)
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.WrapProviderWriteError(err, false)
	}
	request := map[string]any{"welcome_code": welcomeCode}
	if text != "" {
		request["text"] = map[string]string{"content": text}
	}
	if len(attachments) > 0 {
		providerAttachments := make([]map[string]any, len(attachments))
		for index, item := range attachments {
			var payload any
			switch item.MsgType {
			case "image":
				if invalid(item.MediaID) {
					return wecomport.WrapProviderWriteError(ErrUnavailable, false)
				}
				payload = map[string]string{"media_id": item.MediaID}
			case "file":
				if invalid(item.MediaID) {
					return wecomport.WrapProviderWriteError(ErrUnavailable, false)
				}
				payload = map[string]string{"media_id": item.MediaID}
			case "miniprogram":
				if invalid(item.MediaID) || invalid(item.AppID) || invalid(item.PagePath) || invalid(item.Title) {
					return wecomport.WrapProviderWriteError(ErrUnavailable, false)
				}
				payload = map[string]string{"pic_media_id": item.MediaID, "appid": item.AppID, "page": item.PagePath, "title": item.Title}
			case "link":
				if invalid(item.URL) || invalid(item.Title) {
					return wecomport.WrapProviderWriteError(ErrUnavailable, false)
				}
				payload = map[string]string{"title": item.Title, "url": item.URL, "desc": item.Description, "picurl": item.PicURL}
			default:
				return wecomport.WrapProviderWriteError(ErrUnavailable, false)
			}
			providerAttachments[index] = map[string]any{"msgtype": item.MsgType, item.MsgType: payload}
		}
		request["attachments"] = providerAttachments
	}
	body, err := json.Marshal(request)
	if err != nil {
		return wecomport.WrapProviderWriteError(ErrResponse, false)
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/send_welcome_msg", url.Values{"access_token": {token}}, body)
	if err != nil {
		return classifyWelcomeWriteError(err)
	}
	// requestJSON is shared with historical read paths, where omitted or string
	// errcode values remain compatible. A welcome write needs stricter proof:
	// only the JSON integer 0 proves acceptance. Every other 2xx shape may be a
	// proxy/body mismatch after a write and stays outcome_unknown.
	if code, ok := strictJSONInt(payload.ErrCode); !ok || code != 0 {
		return wecomport.WrapProviderWriteDisposition(ErrResponse, true, true, false)
	}
	return nil
}

// classifyWelcomeWriteError treats only a completed 2xx JSON response with a
// nonzero numeric errcode as a definite rejection. Transport errors, non-2xx
// responses, unreadable bodies, and malformed/missing/string errcodes remain
// unknown because the one-time welcome write may already have reached WeCom.
func classifyWelcomeWriteError(err error) error {
	if err == nil {
		return nil
	}
	var responseErr *providerResponseError
	if errors.As(err, &responseErr) && responseErr.statusCode >= http.StatusOK && responseErr.statusCode < http.StatusMultipleChoices && responseErr.errCode != 0 {
		return wecomport.WrapProviderWriteDispositionWithCode(err, true, false, false, responseErr.errCode)
	}
	return wecomport.WrapProviderWriteDisposition(err, true, true, false)
}

func (client *Client) TransferCustomer(ctx context.Context, sourceUserID, targetUserID string, externalUserIDs []string, welcomeMessage string) (wecomport.CustomerTransferResult, error) {
	if !client.DirectoryReady() || invalid(sourceUserID) || invalid(targetUserID) || sourceUserID == targetUserID || len(externalUserIDs) < 1 || len(externalUserIDs) > 100 || len([]rune(welcomeMessage)) > 4000 {
		return wecomport.CustomerTransferResult{}, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(externalUserIDs))
	for _, externalUserID := range externalUserIDs {
		if invalid(externalUserID) {
			return wecomport.CustomerTransferResult{}, ErrUnavailable
		}
		if _, exists := seen[externalUserID]; exists {
			return wecomport.CustomerTransferResult{}, ErrUnavailable
		}
		seen[externalUserID] = struct{}{}
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(err, false)
	}
	body := map[string]any{"handover_userid": sourceUserID, "takeover_userid": targetUserID, "external_userid": externalUserIDs}
	if welcomeMessage != "" {
		body["transfer_success_msg"] = welcomeMessage
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return wecomport.CustomerTransferResult{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/transfer_customer", url.Values{"access_token": {token}}, raw)
	if err != nil {
		// Only a strict, non-zero top-level errcode proves that WeCom rejected
		// this write. HTTP failures, HTML, malformed JSON and missing/invalid
		// errcodes may all happen after acceptance and therefore retain the
		// original EER key as outcome_unknown.
		if definitiveTransferRejection(err) {
			return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteOutcome(err, true, false)
		}
		return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteOutcome(err, true, true)
	}
	// transfer_customer is a write: unlike older read endpoints, only the JSON
	// number 0 proves top-level acceptance. null, strings and fractions can be
	// produced by a proxy/body mismatch after a write and must retain the EER
	// idempotency key as outcome_unknown.
	topLevelCode, topLevelOK := strictJSONInt(payload.ErrCode)
	if !topLevelOK || topLevelCode != 0 {
		return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteOutcome(ErrResponse, true, true)
	}
	// A batched response can conclusively report some rows while omitting
	// others. Preserve those exact row identities for the owner-handoff
	// artifact; an omitted or non-strict per-row code remains unclassified.
	// Any cross-row ambiguity is unsafe for every member and remains a whole
	// call outcome_unknown.
	if len(payload.Customer) > len(externalUserIDs) {
		return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
	}
	result := wecomport.CustomerTransferResult{AcceptedExternalUserIDs: make([]string, 0, len(payload.Customer)), RejectedExternalUserIDs: make([]string, 0, len(payload.Customer))}
	reported := make(map[string]struct{}, len(payload.Customer))
	for _, item := range payload.Customer {
		item.ExternalUserID = strings.TrimSpace(item.ExternalUserID)
		if invalid(item.ExternalUserID) {
			return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
		}
		if _, exists := seen[item.ExternalUserID]; !exists {
			return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
		}
		if _, exists := reported[item.ExternalUserID]; exists {
			return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
		}
		reported[item.ExternalUserID] = struct{}{}
		code, valid := transferSubmissionCode(item.ErrCode)
		if !valid {
			continue
		}
		if code == 0 {
			result.AcceptedExternalUserIDs = append(result.AcceptedExternalUserIDs, item.ExternalUserID)
		} else {
			result.FailedCount++
			result.RejectedExternalUserIDs = append(result.RejectedExternalUserIDs, item.ExternalUserID)
		}
	}
	// Keep the established one-row contract: it has no independently proven
	// peer result to retain, so an omitted/invalid row is reported as unknown.
	if len(externalUserIDs) == 1 && len(result.AcceptedExternalUserIDs)+len(result.RejectedExternalUserIDs) != 1 {
		return wecomport.CustomerTransferResult{}, wecomport.WrapProviderWriteError(ErrResponse, true)
	}
	return result, nil
}

// TransferResult is read-only provider evidence.  It intentionally does not
// mutate local ownership; its rows are exposed to the owning Customer service
// for later observation/reconciliation.
func (client *Client) TransferResult(ctx context.Context, sourceUserID, targetUserID, cursor string) (wecomport.CustomerTransferResult, error) {
	if !client.DirectoryReady() || invalid(sourceUserID) || invalid(targetUserID) || sourceUserID == targetUserID || strings.TrimSpace(cursor) != cursor {
		return wecomport.CustomerTransferResult{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerTransferResult{}, err
	}
	raw, err := json.Marshal(map[string]any{"handover_userid": sourceUserID, "takeover_userid": targetUserID, "cursor": cursor})
	if err != nil {
		return wecomport.CustomerTransferResult{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/transfer_result", url.Values{"access_token": {token}}, raw)
	if err != nil {
		return wecomport.CustomerTransferResult{}, err
	}
	result := wecomport.CustomerTransferResult{Cursor: strings.TrimSpace(payload.NextCursor), Observations: make([]wecomport.CustomerTransferObservation, 0, len(payload.Customer))}
	seen := make(map[string]struct{}, len(payload.Customer))
	for _, item := range payload.Customer {
		item.ExternalUserID = strings.TrimSpace(item.ExternalUserID)
		if invalid(item.ExternalUserID) {
			return wecomport.CustomerTransferResult{}, ErrResponse
		}
		if _, exists := seen[item.ExternalUserID]; exists {
			return wecomport.CustomerTransferResult{}, ErrResponse
		}
		status, statusOK := transferResultStatus(item.Status)
		takeoverTime, timeOK := strictJSONInt(item.TakeoverTime)
		if !statusOK || !timeOK || takeoverTime < 0 {
			return wecomport.CustomerTransferResult{}, ErrResponse
		}
		seen[item.ExternalUserID] = struct{}{}
		result.Observations = append(result.Observations, wecomport.CustomerTransferObservation{ExternalUserID: item.ExternalUserID, Status: status, TakeoverTime: takeoverTime})
	}
	return result, nil
}

func strictJSONInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || string(raw) != strconv.FormatInt(value, 10) {
		return 0, false
	}
	return value, true
}

func transferSubmissionCode(raw json.RawMessage) (int64, bool) {
	return strictJSONInt(raw)
}

func transferResultStatus(raw json.RawMessage) (int, bool) {
	value, ok := strictJSONInt(raw)
	if !ok || value < 1 || value > 5 {
		return 0, false
	}
	return int(value), true
}

// AddContactTag is the only customer-tag mutation exposed by the WeCom
// adapter. Every identifier is obtained from trusted local adapters.
func classifyContactTagWriteError(err error) error {
	if err == nil {
		return nil
	}
	var responseErr *providerResponseError
	if errors.As(err, &responseErr) {
		// A response without a concrete nonzero errcode cannot prove that
		// mark_tag was rejected. That includes a 2xx malformed body and a
		// gateway 429/5xx response with HTML, {}, or null: the request may
		// already have reached WeCom. Preserve the original key as unknown.
		if responseErr.errCode == 0 {
			return wecomport.WrapProviderWriteDisposition(err, true, true, false)
		}
		// A parsed nonzero errcode is a definite business result. A 429/5xx
		// response remains retry-safe only before an outbound boundary.
		return wecomport.WrapProviderWriteDisposition(err, true, false, responseErr.retryable)
	}
	// A transport error after submit has no trustworthy Provider outcome.
	return wecomport.WrapProviderWriteDisposition(err, true, true, false)
}

func (client *Client) AddContactTag(ctx context.Context, employeeID, externalUserID, providerTagID string) error {
	return client.MarkContactTags(ctx, employeeID, externalUserID, []string{providerTagID}, nil)
}

// MarkContactTags is the single WeCom mark_tag leaf shared by legacy Channel
// entry_tag and Customer-owned generic tag commands.
func (client *Client) MarkContactTags(ctx context.Context, employeeID, externalUserID string, addTagIDs, removeTagIDs []string) error {
	if !client.DirectoryReady() || invalid(employeeID) || invalid(externalUserID) || (len(addTagIDs) == 0 && len(removeTagIDs) == 0) || len(addTagIDs)+len(removeTagIDs) > 100 {
		return ErrUnavailable
	}
	for _, id := range append(append([]string(nil), addTagIDs...), removeTagIDs...) {
		if invalid(id) {
			return ErrUnavailable
		}
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.WrapProviderWriteError(err, false)
	}
	body, err := json.Marshal(map[string]any{"userid": employeeID, "external_userid": externalUserID, "add_tag": addTagIDs, "remove_tag": removeTagIDs})
	if err != nil {
		return ErrResponse
	}
	payload, requestErr := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/mark_tag", url.Values{"access_token": {token}}, body)
	if requestErr == nil && !confirmedMarkTagSuccess(payload.ErrCode) {
		requestErr = &providerResponseError{statusCode: http.StatusOK}
	}
	return classifyContactTagWriteError(requestErr)
}

// UpdateExternalContactDescription is the narrow WeCom leaf for the
// externalcontact/remark endpoint's description field. It is not a general
// profile writer: Outbound owns the durable effect and is responsible for the
// compare-before-write/readback protocol. This method neither logs nor retains
// the follow employee, external contact ID, or description.
func (client *Client) UpdateExternalContactDescription(ctx context.Context, update wecomport.ExternalContactDescriptionUpdate) error {
	if !client.DirectoryReady() || invalid(update.EmployeeID) || invalid(update.ExternalUserID) || !validExternalContactDescription(update.Description) {
		return wecomport.WrapProviderWriteError(ErrUnavailable, false)
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.WrapProviderWriteError(err, false)
	}
	body, err := json.Marshal(map[string]string{"userid": update.EmployeeID, "external_userid": update.ExternalUserID, "description": update.Description})
	if err != nil {
		return wecomport.WrapProviderWriteError(ErrResponse, false)
	}
	payload, requestErr := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/remark", url.Values{"access_token": {token}}, body)
	if requestErr == nil && !confirmedMarkTagSuccess(payload.ErrCode) {
		requestErr = &providerResponseError{statusCode: http.StatusOK}
	}
	return classifyContactTagWriteError(requestErr)
}

func validExternalContactDescription(value string) bool {
	return value != "" && len([]rune(value)) <= 150 && !strings.ContainsRune(value, '\x00')
}
func (client *Client) ListManagedAcquisitionLinks(ctx context.Context, cursor string, limit int) (wecomport.CustomerAcquisitionLinkPage, error) {
	if !client.DirectoryReady() || strings.TrimSpace(cursor) != cursor || limit < 1 || limit > 100 {
		return wecomport.CustomerAcquisitionLinkPage{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerAcquisitionLinkPage{}, err
	}
	body, _ := json.Marshal(map[string]any{"cursor": cursor, "limit": limit})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/list_link", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.CustomerAcquisitionLinkPage{}, err
	}
	if payload.LinkIDList == nil || len(payload.LinkIDList) > limit {
		return wecomport.CustomerAcquisitionLinkPage{}, ErrResponse
	}
	result := wecomport.CustomerAcquisitionLinkPage{Links: make([]wecomport.CustomerAcquisitionLink, 0, len(payload.LinkIDList)), NextCursor: payload.NextCursor}
	seen := map[string]struct{}{}
	for _, id := range payload.LinkIDList {
		if invalid(id) {
			return wecomport.CustomerAcquisitionLinkPage{}, ErrResponse
		}
		if _, ok := seen[id]; ok {
			return wecomport.CustomerAcquisitionLinkPage{}, ErrResponse
		}
		seen[id] = struct{}{}
		result.Links = append(result.Links, wecomport.CustomerAcquisitionLink{LinkID: id})
	}
	return result, nil
}
func (client *Client) GetManagedAcquisitionLink(ctx context.Context, linkID string) (wecomport.CustomerAcquisitionLink, error) {
	if !client.DirectoryReady() || invalid(linkID) {
		return wecomport.CustomerAcquisitionLink{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, err
	}
	body, _ := json.Marshal(map[string]string{"link_id": linkID})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/get", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, err
	}
	result := wecomport.CustomerAcquisitionLink{LinkID: linkID, LinkName: strings.TrimSpace(payload.Link.LinkName), URL: strings.TrimSpace(payload.Link.URL), UserIDs: payload.Range.UserIDs, DepartmentIDs: payload.Range.DepartmentIDs, SkipVerify: payload.Link.SkipVerify}
	if !validManagedLink(result) {
		return wecomport.CustomerAcquisitionLink{}, ErrResponse
	}
	return result, nil
}
func (client *Client) CreateManagedAcquisitionLink(ctx context.Context, input wecomport.CustomerAcquisitionLinkInput) (wecomport.CustomerAcquisitionLink, error) {
	if !client.DirectoryReady() || !validManagedLinkInput(input) {
		return wecomport.CustomerAcquisitionLink{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, wecomport.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]any{"link_name": input.LinkName, "range": map[string]any{"user_list": input.UserIDs, "department_list": input.DepartmentIDs}, "skip_verify": input.SkipVerify})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/create_link", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, wecomport.WrapProviderWriteError(err, true)
	}
	result := wecomport.CustomerAcquisitionLink{LinkID: strings.TrimSpace(payload.Link.LinkID), LinkName: input.LinkName, URL: strings.TrimSpace(payload.Link.URL), UserIDs: append([]string(nil), input.UserIDs...), DepartmentIDs: append([]int64(nil), input.DepartmentIDs...), SkipVerify: input.SkipVerify}
	if !validManagedLink(result) {
		return wecomport.CustomerAcquisitionLink{}, wecomport.WrapProviderWriteError(ErrResponse, true)
	}
	return result, nil
}
func (client *Client) UpdateManagedAcquisitionLink(ctx context.Context, linkID string, input wecomport.CustomerAcquisitionLinkInput) (wecomport.CustomerAcquisitionLink, error) {
	if !client.DirectoryReady() || invalid(linkID) || !validManagedLinkInput(input) {
		return wecomport.CustomerAcquisitionLink{}, ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.CustomerAcquisitionLink{}, wecomport.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]any{"link_id": linkID, "link_name": input.LinkName, "range": map[string]any{"user_list": input.UserIDs, "department_list": input.DepartmentIDs}, "skip_verify": input.SkipVerify})
	if _, err = client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/update_link", url.Values{"access_token": {token}}, body); err != nil {
		return wecomport.CustomerAcquisitionLink{}, wecomport.WrapProviderWriteError(err, true)
	}
	link, err := client.GetManagedAcquisitionLink(ctx, linkID)
	return link, wecomport.WrapProviderWriteError(err, true)
}
func (client *Client) DeleteManagedAcquisitionLink(ctx context.Context, linkID string) error {
	if !client.DirectoryReady() || invalid(linkID) {
		return ErrUnavailable
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]string{"link_id": linkID})
	_, err = client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/delete_link", url.Values{"access_token": {token}}, body)
	return wecomport.WrapProviderWriteError(err, true)
}
func validManagedLinkInput(input wecomport.CustomerAcquisitionLinkInput) bool {
	if invalid(input.LinkName) || len([]rune(input.LinkName)) > 30 || len(input.UserIDs) > 500 || len(input.DepartmentIDs) > 500 || len(input.UserIDs)+len(input.DepartmentIDs) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, id := range input.UserIDs {
		if invalid(id) {
			return false
		}
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
	}
	for _, id := range input.DepartmentIDs {
		if id < 1 {
			return false
		}
	}
	return true
}
func validManagedLink(link wecomport.CustomerAcquisitionLink) bool {
	return !invalid(link.LinkID) && !invalid(link.LinkName) && len([]rune(link.LinkName)) <= 30 && validProviderHTTPS(link.URL) && validManagedLinkInput(wecomport.CustomerAcquisitionLinkInput{LinkName: link.LinkName, UserIDs: link.UserIDs, DepartmentIDs: link.DepartmentIDs, SkipVerify: link.SkipVerify})
}

func validAcquisitionRequest(input wecomport.AcquisitionAssetRequest) bool {
	if invalid(input.Name) || invalid(input.State) || len(input.StaffUserIDs) < 1 || len(input.StaffUserIDs) > 5 {
		return false
	}
	seen := map[string]struct{}{}
	for _, id := range input.StaffUserIDs {
		if invalid(id) {
			return false
		}
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}
func validProviderHTTPS(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

type TagCatalogGroup = wecomport.TagCatalogGroup
type TagCatalogTag = wecomport.TagCatalogTag
type tagGroupWire struct {
	ID    string     `json:"group_id"`
	Name  string     `json:"group_name"`
	Order int32      `json:"order"`
	Tags  *[]tagWire `json:"tag"`
}
type tagWire struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Order   int32  `json:"order"`
	Deleted bool   `json:"deleted"`
}

// CatalogReadError describes only whether a network boundary may have been
// crossed. It deliberately omits Provider status/body details.
type CatalogReadError struct {
	Err           error
	CallAttempted bool
}

func (e *CatalogReadError) Error() string               { return e.Err.Error() }
func (e *CatalogReadError) Unwrap() error               { return e.Err }
func (e *CatalogReadError) ProviderCallAttempted() bool { return e.CallAttempted }

// ListTagCatalog is the narrow read-only WeCom catalog endpoint. The caller
// decides whether an enabled client may be used; this method never enables
// network access itself and never logs Provider responses or credentials.
func (client *Client) ListTagCatalog(ctx context.Context) ([]TagCatalogGroup, error) {
	if !client.Ready() {
		return nil, &CatalogReadError{Err: ErrUnavailable}
	}
	token, err := client.accessToken(ctx)
	if err != nil {
		// The catalog endpoint itself has not been attempted; this can be
		// retried under the original effect key.
		return nil, &CatalogReadError{Err: err}
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/get_corp_tag_list", url.Values{"access_token": {token}}, []byte(`{}`))
	if err != nil {
		return nil, &CatalogReadError{Err: err, CallAttempted: true}
	}
	var sourceGroups []tagGroupWire
	if len(payload.TagGroups) == 0 || json.Unmarshal(payload.TagGroups, &sourceGroups) != nil || sourceGroups == nil {
		return nil, &CatalogReadError{Err: ErrResponse, CallAttempted: true}
	}
	groups := make([]TagCatalogGroup, 0, len(sourceGroups))
	for _, group := range sourceGroups {
		tags := []tagWire{}
		if group.Tags != nil {
			tags = *group.Tags
		}
		value := TagCatalogGroup{ID: group.ID, Name: group.Name, Order: group.Order, Tags: make([]TagCatalogTag, 0, len(tags))}
		for _, tag := range tags {
			value.Tags = append(value.Tags, TagCatalogTag{ID: tag.ID, Name: tag.Name, Order: tag.Order, Deleted: tag.Deleted})
		}
		groups = append(groups, value)
	}
	return groups, nil
}

// MutateTagCatalog is the sole WeCom leaf for provider tag-directory writes.
// It classifies every post-request ambiguity as outcome_unknown, preserving
// the original EER effect and forbidding a second create under a new key.
func (client *Client) MutateTagCatalog(ctx context.Context, mutation wecomport.TagCatalogMutation) (wecomport.TagCatalogMutationResult, error) {
	if !client.DirectoryReady() || !validCatalogMutation(mutation) {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteError(ErrUnavailable, false)
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteError(err, false)
	}
	var endpoint string
	var body any
	switch mutation.Operation {
	case "group_create":
		endpoint, body = "/cgi-bin/externalcontact/add_corp_tag", map[string]any{"group_name": mutation.GroupName, "tag": []map[string]string{{"name": mutation.TagName}}}
	case "tag_create":
		endpoint, body = "/cgi-bin/externalcontact/add_corp_tag", map[string]any{"group_id": mutation.ProviderGroupID, "tag": []map[string]string{{"name": mutation.TagName}}}
	case "group_update", "tag_update":
		id := mutation.ProviderGroupID
		if mutation.Operation == "tag_update" {
			id = mutation.ProviderTagID
		}
		endpoint, body = "/cgi-bin/externalcontact/edit_corp_tag", map[string]string{"id": id, "name": mutation.GroupName + mutation.TagName}
	case "group_archive":
		endpoint, body = "/cgi-bin/externalcontact/del_corp_tag", map[string]any{"group_id": []string{mutation.ProviderGroupID}}
	case "tag_archive":
		endpoint, body = "/cgi-bin/externalcontact/del_corp_tag", map[string]any{"tag_id": []string{mutation.ProviderTagID}}
	default:
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteError(ErrUnavailable, false)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteError(ErrResponse, false)
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, endpoint, url.Values{"access_token": {token}}, encoded)
	if err != nil || !confirmedMarkTagSuccess(payload.ErrCode) {
		if err == nil {
			err = &providerResponseError{statusCode: http.StatusOK}
		}
		return wecomport.TagCatalogMutationResult{}, classifyContactTagWriteError(err)
	}
	result := wecomport.TagCatalogMutationResult{ProviderGroupID: mutation.ProviderGroupID, ProviderTagID: mutation.ProviderTagID}
	if mutation.Operation != "group_create" && mutation.Operation != "tag_create" {
		return result, nil
	}
	var group tagGroupWire
	if json.Unmarshal(payload.TagGroups, &group) != nil || invalid(group.ID) || len(group.TagsOrEmpty()) != 1 {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteDisposition(ErrResponse, true, true, false)
	}
	created := group.TagsOrEmpty()[0]
	if invalid(created.ID) || created.Name != mutation.TagName || (mutation.Operation == "group_create" && group.Name != mutation.GroupName) || (mutation.Operation == "tag_create" && group.ID != mutation.ProviderGroupID) {
		return wecomport.TagCatalogMutationResult{}, wecomport.WrapProviderWriteDisposition(ErrResponse, true, true, false)
	}
	return wecomport.TagCatalogMutationResult{ProviderGroupID: group.ID, ProviderTagID: created.ID}, nil
}

func (value tagGroupWire) TagsOrEmpty() []tagWire {
	if value.Tags == nil {
		return nil
	}
	return *value.Tags
}

func validCatalogMutation(value wecomport.TagCatalogMutation) bool {
	if value.GroupName != strings.TrimSpace(value.GroupName) || value.TagName != strings.TrimSpace(value.TagName) || value.ProviderGroupID != strings.TrimSpace(value.ProviderGroupID) || value.ProviderTagID != strings.TrimSpace(value.ProviderTagID) {
		return false
	}
	switch value.Operation {
	case "group_create":
		return value.GroupName != "" && value.TagName != ""
	case "tag_create":
		return !invalid(value.ProviderGroupID) && value.TagName != ""
	case "group_update":
		return !invalid(value.ProviderGroupID) && value.GroupName != ""
	case "tag_update":
		return !invalid(value.ProviderTagID) && value.TagName != ""
	case "group_archive":
		return !invalid(value.ProviderGroupID)
	case "tag_archive":
		return !invalid(value.ProviderTagID)
	default:
		return false
	}
}

type privateSendError struct {
	uncertain bool
	code      int64
}

func (e privateSendError) Error() string {
	if e.uncertain {
		return "wecom private message outcome unknown"
	}
	return "wecom private message rejected"
}
func (e privateSendError) OutcomeUnknown() bool { return e.uncertain }
func (e privateSendError) Retryable() bool {
	return !e.uncertain && (e.code == 45009 || e.code == 45011 || e.code == -1)
}

// SendGroupMessage creates one WeCom customer-group task for the exact frozen
// chat list. A msgid is acceptance evidence only; fail_list is always a
// rejection, never a partial success. Transport ambiguity is surfaced as
// outcome_unknown so External Effects keeps the original idempotency key.
func (client *Client) SendGroupMessage(ctx context.Context, request wecomport.GroupMessageRequest) (wecomport.GroupMessageReceipt, bool, error) {
	if !client.DirectoryReady() || invalid(request.SenderUserID) || len(request.ChatIDs) != 1 || (strings.TrimSpace(request.Text) == "" && len(request.Attachments) == 0) || len(request.Attachments) > 9 {
		return wecomport.GroupMessageReceipt{}, false, privateSendError{}
	}
	chatID := strings.TrimSpace(request.ChatIDs[0])
	if invalid(chatID) {
		return wecomport.GroupMessageReceipt{}, false, privateSendError{}
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.GroupMessageReceipt{}, false, privateSendError{}
	}
	attachments, err := groupMessageAttachments(request.Attachments)
	if err != nil {
		return wecomport.GroupMessageReceipt{}, false, privateSendError{}
	}
	body := map[string]any{
		"chat_type":    "group",
		"sender":       request.SenderUserID,
		"chat_id_list": []string{chatID},
		"allow_select": false,
	}
	if text := strings.TrimSpace(request.Text); text != "" {
		body["text"] = map[string]string{"content": text}
	}
	if len(attachments) > 0 {
		body["attachments"] = attachments
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return wecomport.GroupMessageReceipt{}, false, privateSendError{}
	}
	result, uncertain, err := client.privateJSON(ctx, "/cgi-bin/externalcontact/add_msg_template", url.Values{"access_token": {token}}, raw, "application/json")
	if err != nil {
		return wecomport.GroupMessageReceipt{}, true, privateSendError{uncertain: uncertain}
	}
	result.MessageID = strings.TrimSpace(result.MessageID)
	if result.MessageID == "" || len(result.FailList) != 0 {
		return wecomport.GroupMessageReceipt{}, true, privateSendError{}
	}
	return wecomport.GroupMessageReceipt{MessageID: result.MessageID}, true, nil
}

// GetGroupMessageSendResult is the documented, read-only task result query.
// It never treats an empty/partial page as delivery proof; the Group Ops
// evidence adapter matches the frozen sender and chat before any local CAS.
func (client *Client) GetGroupMessageSendResult(ctx context.Context, messageID, senderUserID, cursor string, limit int) (wecomport.GroupMessageSendResultPage, error) {
	if !client.DirectoryReady() || invalid(messageID) || invalid(senderUserID) || strings.TrimSpace(cursor) != cursor || limit < 1 || limit > 100 {
		return wecomport.GroupMessageSendResultPage{}, wecomport.ErrDirectoryDisabled
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return wecomport.GroupMessageSendResultPage{}, err
	}
	body, err := json.Marshal(map[string]any{"msgid": messageID, "userid": senderUserID, "cursor": cursor, "limit": limit})
	if err != nil {
		return wecomport.GroupMessageSendResultPage{}, ErrResponse
	}
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/get_groupmsg_send_result", url.Values{"access_token": {token}}, body)
	if err != nil {
		return wecomport.GroupMessageSendResultPage{}, err
	}
	page := wecomport.GroupMessageSendResultPage{Items: make([]wecomport.GroupMessageSendResult, 0, len(payload.SendList)), NextCursor: strings.TrimSpace(payload.NextCursor)}
	for _, item := range payload.SendList {
		value := wecomport.GroupMessageSendResult{SenderUserID: strings.TrimSpace(item.UserID), ChatID: strings.TrimSpace(item.ChatID), Status: item.Status}
		if invalid(value.SenderUserID) || invalid(value.ChatID) || value.Status < 0 {
			return wecomport.GroupMessageSendResultPage{}, ErrResponse
		}
		page.Items = append(page.Items, value)
	}
	return page, nil
}

func groupMessageAttachments(items []wecomport.GroupMessageAttachment) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch item.MsgType {
		case "image":
			if invalid(item.MediaID) {
				return nil, ErrResponse
			}
			result = append(result, map[string]any{"msgtype": "image", "image": map[string]string{"media_id": item.MediaID}})
		case "file":
			if invalid(item.MediaID) {
				return nil, ErrResponse
			}
			result = append(result, map[string]any{"msgtype": "file", "file": map[string]string{"media_id": item.MediaID}})
		case "miniprogram":
			if invalid(item.MediaID) || invalid(item.AppID) || invalid(item.PagePath) || invalid(item.Title) {
				return nil, ErrResponse
			}
			result = append(result, map[string]any{"msgtype": "miniprogram", "miniprogram": map[string]string{"title": item.Title, "pic_media_id": item.MediaID, "appid": item.AppID, "page": item.PagePath}})
		case "link":
			if invalid(item.Title) || invalid(item.URL) || strings.TrimSpace(item.Description) != item.Description || strings.TrimSpace(item.PicURL) != item.PicURL {
				return nil, ErrResponse
			}
			result = append(result, map[string]any{"msgtype": "link", "link": map[string]string{"title": item.Title, "url": item.URL, "desc": item.Description, "picurl": item.PicURL}})
		default:
			return nil, ErrResponse
		}
	}
	return result, nil
}

func (client *Client) SendPrivateMessage(ctx context.Context, target outboundport.PrivateMessageTarget, payload outboundport.PrivateMessagePayload) (outboundport.PrivateMessageProviderReceipt, bool, error) {
	if !client.DirectoryReady() || invalid(target.ExternalUserID) || invalid(target.StaffUserID) || len(payload.Attachments) > 9 || (strings.TrimSpace(payload.Text) == "" && len(payload.Attachments) == 0) {
		return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return outboundport.PrivateMessageProviderReceipt{}, false, err
	}
	attachments := make([]map[string]any, 0, len(payload.Attachments))
	for _, item := range payload.Attachments {
		switch item.Kind {
		case "image":
			mediaID := strings.TrimSpace(item.MediaID)
			if mediaID != "" {
				if len(item.Content) != 0 || invalid(mediaID) {
					return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
				}
			} else {
				var uploadErr error
				mediaID, uploadErr = client.uploadPrivateImage(ctx, token, item.FileName, item.MediaType, item.Content)
				if uploadErr != nil {
					return outboundport.PrivateMessageProviderReceipt{}, false, uploadErr
				}
			}
			attachments = append(attachments, map[string]any{"msgtype": "image", "image": map[string]string{"media_id": mediaID}})
		case "mini_program":
			mediaID := strings.TrimSpace(item.MediaID)
			if mediaID != "" {
				if len(item.Content) != 0 || invalid(mediaID) {
					return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
				}
			} else {
				var uploadErr error
				mediaID, uploadErr = client.uploadPrivateImage(ctx, token, item.FileName, item.MediaType, item.Content)
				if uploadErr != nil {
					return outboundport.PrivateMessageProviderReceipt{}, false, uploadErr
				}
			}
			attachments = append(attachments, map[string]any{"msgtype": "miniprogram", "miniprogram": map[string]string{"title": item.Title, "pic_media_id": mediaID, "appid": item.AppID, "page": item.PagePath}})
		case "file":
			mediaID := strings.TrimSpace(item.MediaID)
			if mediaID != "" {
				if len(item.Content) != 0 || invalid(mediaID) {
					return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
				}
			} else {
				var uploadErr error
				mediaID, uploadErr = client.uploadPrivateFile(ctx, token, item.FileName, item.MediaType, item.Content)
				if uploadErr != nil {
					return outboundport.PrivateMessageProviderReceipt{}, false, uploadErr
				}
			}
			attachments = append(attachments, map[string]any{"msgtype": "file", "file": map[string]string{"media_id": mediaID}})
		case "link":
			if strings.TrimSpace(item.MediaID) != "" || len(item.Content) != 0 {
				return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
			}
			attachments = append(attachments, map[string]any{"msgtype": "link", "link": map[string]string{"title": item.Title, "picurl": item.PicURL, "desc": item.Description, "url": item.URL}})
		default:
			return outboundport.PrivateMessageProviderReceipt{}, false, privateSendError{}
		}
	}
	body := map[string]any{"chat_type": "single", "external_userid": []string{target.ExternalUserID}, "sender": target.StaffUserID, "allow_select": false, "attachments": attachments}
	if text := strings.TrimSpace(payload.Text); text != "" {
		body["text"] = map[string]string{"content": text}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return outboundport.PrivateMessageProviderReceipt{}, false, err
	}
	result, uncertain, err := client.privateJSON(ctx, "/cgi-bin/externalcontact/add_msg_template", url.Values{"access_token": {token}}, raw, "application/json")
	if err != nil {
		var coded *providerResponseError
		code := int64(0)
		if errors.As(err, &coded) {
			code = coded.errCode
		}
		return outboundport.PrivateMessageProviderReceipt{}, true, privateSendError{uncertain: uncertain, code: code}
	}
	result.MessageID = strings.TrimSpace(result.MessageID)
	if result.MessageID == "" || len(result.FailList) != 0 {
		return outboundport.PrivateMessageProviderReceipt{}, true, privateSendError{}
	}
	return outboundport.PrivateMessageProviderReceipt{MessageID: result.MessageID}, true, nil
}

func (client *Client) uploadPrivateImage(ctx context.Context, token, name, mediaType string, content []byte) (string, error) {
	if len(content) <= 5 || len(content) > 2<<20 || (mediaType != "image/jpeg" && mediaType != "image/png") || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\x00") {
		return "", privateSendError{}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "media", "filename": name}))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(content); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	result, _, err := client.privateJSON(ctx, "/cgi-bin/media/upload", url.Values{"access_token": {token}, "type": {"image"}}, body.Bytes(), writer.FormDataContentType())
	if err != nil {
		return "", err
	}
	result.MediaID = strings.TrimSpace(result.MediaID)
	if result.MediaID == "" {
		return "", privateSendError{}
	}
	return result.MediaID, nil
}

func (client *Client) uploadPrivateFile(ctx context.Context, token, name, mediaType string, content []byte) (string, error) {
	if len(content) <= 5 || len(content) > 20<<20 || mediaType != "application/pdf" || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\x00") {
		return "", privateSendError{}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "media", "filename": name}))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(content); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	result, _, err := client.privateJSON(ctx, "/cgi-bin/media/upload", url.Values{"access_token": {token}, "type": {"file"}}, body.Bytes(), writer.FormDataContentType())
	if err != nil {
		return "", err
	}
	result.MediaID = strings.TrimSpace(result.MediaID)
	if result.MediaID == "" {
		return "", privateSendError{}
	}
	return result.MediaID, nil
}

func (client *Client) privateJSON(ctx context.Context, path string, query url.Values, body []byte, contentType string) (response, bool, error) {
	endpoint := *client.apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return response{}, false, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := client.http.Do(req)
	if err != nil {
		return response{}, true, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil || len(raw) > maxResponseBody {
		return response{}, true, ErrResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return response{}, resp.StatusCode >= 500, ErrResponse
	}
	var result response
	if json.Unmarshal(raw, &result) != nil {
		return response{}, true, ErrResponse
	}
	if !successErrCode(result.ErrCode) {
		return response{}, false, &providerResponseError{errCode: providerErrCode(result.ErrCode)}
	}
	return result, false, nil
}

var _ outboundport.PrivateMessageSender = (*Client)(nil)
var _ wecomport.GroupMessageSender = (*Client)(nil)
var _ wecomport.GroupMessageTaskReader = (*Client)(nil)
var _ wecomport.GroupChatReader = (*Client)(nil)

func (client *Client) request(ctx context.Context, path string, query url.Values) (response, error) {
	return client.requestJSON(ctx, http.MethodGet, path, query, nil)
}

func (client *Client) requestJSON(ctx context.Context, method, path string, query url.Values, body []byte) (response, error) {
	raw, err := client.requestRawJSON(ctx, method, path, query, body)
	if err != nil {
		return response{}, err
	}
	var payload response
	if json.Unmarshal(raw.body, &payload) != nil {
		return response{}, &providerResponseError{statusCode: raw.statusCode}
	}
	if !successErrCode(payload.ErrCode) {
		return response{}, &providerResponseError{statusCode: raw.statusCode, errCode: providerErrCode(payload.ErrCode)}
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.UserID = strings.TrimSpace(payload.UserID)
	payload.UserIDLower = strings.TrimSpace(payload.UserIDLower)
	payload.Ticket = strings.TrimSpace(payload.Ticket)
	return payload, nil
}

// requestRawJSON owns the common transport contract: timeout, bounded reads,
// HTTP status handling, and provider error classification. Narrow reader
// projections decode its successful payload without bypassing that contract.
type rawJSONResponse struct {
	statusCode int
	body       []byte
}

func (client *Client) requestRawJSON(ctx context.Context, method, path string, query url.Values, body []byte) (rawJSONResponse, error) {
	endpoint := *client.apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(requestCtx, method, endpoint.String(), input)
	if err != nil {
		return rawJSONResponse{}, ErrUnavailable
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.http.Do(req)
	if err != nil {
		if path == "/cgi-bin/externalcontact/groupchat/list" || path == "/cgi-bin/externalcontact/groupchat/get" {
			var timeout interface{ Timeout() bool }
			if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
				return rawJSONResponse{}, &directoryReadError{cause: ErrUnavailable, code: "provider_timeout", retryable: true}
			}
		}
		return rawJSONResponse{}, ErrUnavailable
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBody+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil || len(responseBody) > maxResponseBody {
		return rawJSONResponse{}, &providerResponseError{statusCode: resp.StatusCode, retryable: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return rawJSONResponse{}, providerError(resp.StatusCode, responseBody)
	}
	return rawJSONResponse{statusCode: resp.StatusCode, body: responseBody}, nil
}

// confirmedMarkTagSuccess is intentionally stricter than the historical
// generic reader helper: the write leaf must never treat a missing, null, or
// string errcode as an accepted mutation.
func confirmedMarkTagSuccess(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value int64
	return json.Unmarshal(raw, &value) == nil && value == 0
}

func definitiveTransferRejection(err error) bool {
	var responseErr *providerResponseError
	return errors.As(err, &responseErr) && responseErr.errCode != 0
}

func providerError(statusCode int, body []byte) error {
	var payload response
	if json.Unmarshal(body, &payload) != nil {
		return &providerResponseError{statusCode: statusCode, retryable: statusCode == http.StatusTooManyRequests || statusCode >= 500}
	}
	return &providerResponseError{statusCode: statusCode, errCode: providerErrCode(payload.ErrCode), retryable: statusCode == http.StatusTooManyRequests || statusCode >= 500}
}

func providerErrCode(raw json.RawMessage) int64 {
	var value int64
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return 0
}

func classifyGroupDirectoryReadError(cause error) error {
	var failure wecomport.DirectoryFailure
	if errors.As(cause, &failure) {
		return cause
	}
	return classifyDirectoryReadError(cause)
}

func classifyDirectoryReadError(cause error) error {
	if cause == nil || errors.Is(cause, wecomport.ErrDirectoryDisabled) {
		return cause
	}
	classification := &directoryReadError{cause: cause, code: "provider_response_invalid"}
	if errors.Is(cause, ErrUnavailable) {
		classification.code, classification.retryable = "provider_unavailable", true
		return classification
	}
	var providerFailure *providerResponseError
	if !errors.As(cause, &providerFailure) {
		return classification
	}
	if providerFailure.statusCode == http.StatusUnauthorized || providerFailure.statusCode == http.StatusForbidden || directoryPermissionErrCode(providerFailure.errCode) {
		classification.code = "provider_permission_denied"
		return classification
	}
	if directoryCredentialErrCode(providerFailure.errCode) || directoryTokenExpired(cause) {
		classification.code = "provider_credentials_invalid"
		return classification
	}
	if providerFailure.statusCode == http.StatusTooManyRequests || providerFailure.errCode == 45009 {
		classification.code, classification.retryable = "provider_rate_limited", true
		return classification
	}
	// WeCom documents errcode -1 as a transient system-busy response even
	// when the HTTP status is 200. Keep the retry decision on the existing
	// durable customer-sync job instead of adding an adapter-local retry loop.
	if providerFailure.statusCode == http.StatusOK && providerFailure.errCode == -1 {
		classification.code, classification.retryable, classification.maxAttempts = "provider_unavailable", true, 3
		return classification
	}
	if providerFailure.retryable || providerFailure.statusCode >= 500 {
		classification.code, classification.retryable = "provider_unavailable", true
	}
	return classification
}

func classifyDirectoryRefreshError(cause error) error {
	if directoryTokenExpired(cause) {
		return &directoryReadError{cause: cause, code: "provider_credentials_invalid"}
	}
	return classifyDirectoryReadError(cause)
}

func directoryTokenExpired(cause error) bool {
	var providerFailure *providerResponseError
	return errors.As(cause, &providerFailure) && (providerFailure.errCode == 40014 || providerFailure.errCode == 42001)
}

func directoryPermissionErrCode(code int64) bool {
	switch code {
	case 48001, 48002, 48003, 48004, 60011:
		return true
	default:
		return false
	}
}

func directoryCredentialErrCode(code int64) bool {
	switch code {
	case 40001, 40013:
		return true
	default:
		return false
	}
}

func (client *Client) cached(key string) (string, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	entry, ok := client.tokens[key]
	return entry.value, ok && entry.value != "" && entry.expiresAt.After(client.now())
}

func (client *Client) refreshDirectoryToken(ctx context.Context) (string, error) {
	client.mu.Lock()
	delete(client.tokens, "contact_access_token")
	client.mu.Unlock()
	return client.contactAccessToken(ctx)
}

func (client *Client) store(key, value string, expiresIn int64) {
	validFor := time.Duration(expiresIn)*time.Second - time.Minute
	if validFor < 0 {
		validFor = 0
	}
	client.mu.Lock()
	client.tokens[key] = credential{value: value, expiresAt: client.now().Add(validFor)}
	client.mu.Unlock()
}

func (client *Client) now() time.Time {
	if client.config.Now != nil {
		return client.config.Now().UTC()
	}
	return time.Now().UTC()
}

func (client *Client) random() func([]byte) error {
	if client.config.Random != nil {
		return client.config.Random
	}
	return func(value []byte) error { _, err := rand.Read(value); return err }
}

func invalid(value string) bool { return value == "" || strings.TrimSpace(value) != value }
func invalidOptional(value string) bool {
	return strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") || len([]rune(value)) > 200
}

// externalContactRemark keeps the provider-projected text. A remark may be
// multi-line; only NUL is rejected because PostgreSQL cannot retain it.
func externalContactRemark(value *string) (*string, error) {
	return externalContactProfileText(value)
}

// externalContactDescription keeps the provider description distinct from
// remark. A historical/manual value may already exceed the endpoint's 150
// rune write limit; retain it for the compare-before-write decision so the
// caller can report too_long rather than failing an entire directory page.
func externalContactDescription(raw json.RawMessage) (*string, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.ContainsRune(value, '\x00') {
		return nil, false, ErrResponse
	}
	return &value, true, nil
}

func externalContactProfileText(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if len([]rune(*value)) > 2000 || strings.ContainsRune(*value, '\x00') {
		return nil, ErrResponse
	}
	copy := *value
	return &copy, nil
}

func validCallback(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validatedAPIBase(raw string, testTransport bool) (*url.URL, error) {
	if raw == "" {
		raw = productionAPIBase
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return nil, ErrUnavailable
	}
	if parsed.Scheme == "https" && parsed.Host == "qyapi.weixin.qq.com" {
		return parsed, nil
	}
	if testTransport && parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1") {
		return parsed, nil
	}
	return nil, ErrUnavailable
}

func exactNoFragmentURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", ErrResponse
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func successErrCode(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number == 0
	}
	var text string
	return json.Unmarshal(raw, &text) == nil && text == "0"
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

var _ wecom.OAuthClient = (*Client)(nil)
var _ wecom.JSSDKSigner = (*Client)(nil)
var _ wecomport.DirectoryProvider = (*Client)(nil)
var _ wecomport.ContactStaffProfileReader = (*Client)(nil)
var _ wecomport.ExternalContactReader = (*Client)(nil)
var _ wecomport.ExternalContactDescriptionTargetReader = (*Client)(nil)
var _ wecomport.AcquisitionAssetWriter = (*Client)(nil)
var _ wecomport.TagCatalogMutationWriter = (*Client)(nil)

// GetPrivateMessageSendResult reads every page at the caller. An absent target
// never constitutes delivery proof. msgid + sender + external_userid are matched
// to the frozen outbound receipt before projection.
func (client *Client) GetPrivateMessageSendResult(ctx context.Context, messageID, sender, cursor string) (outboundport.PrivateMessageDeliveryPage, error) {
	var page outboundport.PrivateMessageDeliveryPage
	if !client.DirectoryReady() || invalid(messageID) || invalid(sender) {
		return page, ErrResponse
	}
	token, err := client.contactAccessToken(ctx)
	if err != nil {
		return page, err
	}
	body, _ := json.Marshal(map[string]any{"msgid": messageID, "userid": sender, "cursor": cursor, "limit": 100})
	payload, err := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/get_groupmsg_send_result", url.Values{"access_token": {token}}, body)
	if err != nil {
		return page, err
	}
	page.NextCursor = payload.NextCursor
	for _, item := range payload.SendList {
		if invalid(item.ExternalUserID) || item.Status < 0 || item.Status > 4 || (item.UserID != "" && item.UserID != sender) {
			return page, ErrResponse
		}
		status := item.Status
		value := outboundport.PrivateMessageDelivery{MessageID: messageID, SenderUserID: sender, ExternalUserID: item.ExternalUserID, Status: &status, ObservedAt: time.Now().UTC()}
		if item.SendTime > 0 {
			t := time.Unix(item.SendTime, 0).UTC()
			value.SentAt = &t
		}
		if status == 1 && value.SentAt == nil {
			return page, ErrResponse
		}
		page.Items = append(page.Items, value)
	}
	return page, nil
}

func (e privateSendError) FailureCode() string {
	if e.code != 0 {
		return "wecom_errcode_" + strconv.FormatInt(e.code, 10)
	}
	if e.uncertain {
		return "outcome_unknown"
	}
	return "provider_rejected"
}

// ReadGroupMembership is a read-only complete Provider snapshot. A missing or
// malformed member_list must never be interpreted as an empty group.
func (client *Client) ReadGroupMembership(ctx context.Context, chatID string) (result wecomport.GroupMembership, err error) {
	defer func() { err = classifyGroupDirectoryReadError(err) }()
	if !client.DirectoryReady() || invalid(chatID) {
		return result, wecomport.ErrDirectoryDisabled
	}
	token, e := client.contactAccessToken(ctx)
	if e != nil {
		return result, e
	}
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "need_name": 0})
	payload, e := client.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get", url.Values{"access_token": {token}}, body)
	if e != nil {
		return result, e
	}
	if payload.GroupChat.ChatID != chatID || payload.GroupChat.Members == nil || len(payload.GroupChat.Members) > 10000 {
		return result, ErrResponse
	}
	result.ChatID = chatID
	result.ExternalUserIDs = []string{}
	seen := map[string]bool{}
	for _, member := range payload.GroupChat.Members {
		if invalid(member.UserID) || (member.Type != 1 && member.Type != 2) || seen[member.UserID] {
			return wecomport.GroupMembership{}, ErrResponse
		}
		seen[member.UserID] = true
		if member.Type == 2 {
			result.ExternalUserIDs = append(result.ExternalUserIDs, member.UserID)
		}
	}
	return result, nil
}

// EnterpriseDirectoryReady reports whether the normal application credential
// can perform read-only corporate-directory calls. It is intentionally
// separate from DirectoryReady, which uses the customer-contact secret.
func (client *Client) EnterpriseDirectoryReady() bool {
	return client != nil && client.Ready() && !invalid(client.config.Secret)
}

// ListEnterpriseEmployees reads the application-visible corporate directory
// through the read-only department/simplelist + user/simplelist APIs. The
// newer user/list_id endpoint is not universally granted to the existing
// application credential, so this adapter deliberately uses the standard
// member-reading APIs and returns a failure rather than a partial directory.
func (client *Client) ListEnterpriseEmployees(ctx context.Context) ([]wecomport.EnterpriseEmployee, error) {
	if !client.EnterpriseDirectoryReady() {
		return nil, wecomport.ErrDirectoryDisabled
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	token, err := client.accessToken(readCtx)
	if err != nil {
		return nil, classifyDirectoryReadError(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		employees, readErr := client.listEnterpriseEmployees(readCtx, token)
		if !directoryTokenExpired(readErr) {
			if readErr != nil {
				return nil, classifyDirectoryReadError(readErr)
			}
			return employees, nil
		}
		if attempt == 1 {
			return nil, classifyDirectoryRefreshError(readErr)
		}
		token, err = client.refreshAccessToken(readCtx)
		if err != nil {
			return nil, classifyDirectoryRefreshError(err)
		}
	}
	return nil, classifyDirectoryReadError(ErrUnavailable)
}

// EnterpriseDirectoryPreflight exposes only safe aggregate evidence for an
// operator-run, read-only release gate. It retains all provider identifiers,
// names, tokens, responses and errors inside the adapter.
type EnterpriseDirectoryPreflight struct {
	Complete                bool   `json:"complete"`
	FailureStage            string `json:"failure_stage,omitempty"`
	AgentUserInfosShape     string `json:"agent_userinfos_shape,omitempty"`
	AgentPartysShape        string `json:"agent_partys_shape,omitempty"`
	AgentTagsShape          string `json:"agent_tags_shape,omitempty"`
	AgentUserInfosDetail    string `json:"agent_userinfos_detail,omitempty"`
	AgentPartysDetail       string `json:"agent_partys_detail,omitempty"`
	AgentTagsDetail         string `json:"agent_tags_detail,omitempty"`
	ScopeUsers              int    `json:"scope_user_count"`
	ScopeDepartments        int    `json:"scope_department_count"`
	DirectoryDepartments    int    `json:"directory_department_count"`
	DirectoryComponents     int    `json:"directory_component_count"`
	DepartmentEmployeeCount int    `json:"department_employee_count"`
	DirectEmployeeCount     int    `json:"direct_employee_count"`
	EmployeeCount           int    `json:"employee_count"`
}

// PreflightEnterpriseDirectory proves that the application credential can
// enumerate its whole visible scope. A non-empty FailureStage is deliberately
// coarse: it distinguishes configuration, token, scope, and page families
// without disclosing a Provider error code, response body, or identifier.
func (client *Client) PreflightEnterpriseDirectory(ctx context.Context) EnterpriseDirectoryPreflight {
	result := EnterpriseDirectoryPreflight{}
	if !client.EnterpriseDirectoryReady() {
		result.FailureStage = "config"
		return result
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	token, err := client.accessToken(readCtx)
	if err != nil {
		result.FailureStage = "token"
		return result
	}
	scope, stage, evidence := client.enterpriseAgentScopePreflight(readCtx, token)
	result.AgentUserInfosShape = evidence.userInfos
	result.AgentPartysShape = evidence.partys
	result.AgentTagsShape = evidence.tags
	result.AgentUserInfosDetail = evidence.userInfosDetail
	result.AgentPartysDetail = evidence.partysDetail
	result.AgentTagsDetail = evidence.tagsDetail
	if stage != "" {
		result.FailureStage = stage
		return result
	}
	result.ScopeUsers = len(scope.userIDs)
	result.ScopeDepartments = len(scope.departmentIDs)
	departments, err := client.enterpriseDepartments(readCtx, token)
	if err != nil {
		result.FailureStage = "departments"
		return result
	}
	result.DirectoryDepartments = len(departments)
	authorizedDepartments, err := enterpriseAuthorizedDepartments(departments, scope.departmentIDs)
	if err != nil {
		result.FailureStage = "scope_coverage"
		return result
	}
	scopes, err := enterpriseDirectoryScopes(authorizedDepartments)
	if err != nil {
		result.FailureStage = "department_topology"
		return result
	}
	result.DirectoryComponents = len(scopes)
	departmentEmployees, err := client.enterpriseMembers(readCtx, token, scopes)
	if err != nil {
		result.FailureStage = "department_members"
		return result
	}
	result.DepartmentEmployeeCount = len(departmentEmployees)
	directEmployees, err := client.enterpriseDirectEmployees(readCtx, token, scope.userIDs)
	if err != nil {
		result.FailureStage = "direct_members"
		return result
	}
	result.DirectEmployeeCount = len(directEmployees)
	employees, err := mergeEnterpriseEmployees(departmentEmployees, directEmployees)
	if err != nil {
		result.FailureStage = "merge"
		return result
	}
	result.Complete = true
	result.EmployeeCount = len(employees)
	return result
}

type enterpriseAgentScopeEvidence struct {
	userInfos       string
	partys          string
	tags            string
	userInfosDetail string
	partysDetail    string
	tagsDetail      string
}

func enterpriseScopeFieldShape(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "missing"
	}
	if bytes.Equal(raw, []byte("null")) {
		return "null"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "malformed"
	}
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

// enterpriseScopeEnvelopeDetail is deliberately value-free preflight
// evidence. It reveals only a known envelope field's type, collection size,
// element types, and the enclosing object key count.
func enterpriseScopeEnvelopeDetail(raw json.RawMessage, expectedField string) string {
	raw = bytes.TrimSpace(raw)
	if enterpriseScopeFieldShape(raw) != "object" {
		return "envelope=" + enterpriseScopeFieldShape(raw)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "envelope=malformed"
	}
	field, exists := envelope[expectedField]
	if !exists {
		return "object_keys=" + strconv.Itoa(len(envelope)) + ";" + expectedField + "=missing"
	}
	return "object_keys=" + strconv.Itoa(len(envelope)) + ";" + expectedField + "=" + enterpriseScopeValueDetail(field)
}

func enterpriseScopeValueDetail(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "missing"
	}
	if bytes.Equal(raw, []byte("null")) {
		return "null"
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return enterpriseScopeFieldShape(raw)
	}
	if len(values) == 0 {
		return "array_len=0;element_types=none"
	}
	types := make(map[string]struct{}, len(values))
	for _, value := range values {
		types[enterpriseScopeFieldShape(value)] = struct{}{}
	}
	orderedTypes := make([]string, 0, len(types))
	for kind := range types {
		orderedTypes = append(orderedTypes, kind)
	}
	sort.Strings(orderedTypes)
	return "array_len=" + strconv.Itoa(len(values)) + ";element_types=" + strings.Join(orderedTypes, ",")
}

func (client *Client) enterpriseAgentScopePreflight(ctx context.Context, token string) (enterpriseAgentScope, string, enterpriseAgentScopeEvidence) {
	payload, err := client.request(ctx, "/cgi-bin/agent/get", url.Values{"access_token": {token}, "agentid": {client.config.AgentID}})
	if err != nil {
		return enterpriseAgentScope{}, "agent_get", enterpriseAgentScopeEvidence{}
	}
	evidence := enterpriseAgentScopeEvidence{
		userInfos: enterpriseScopeFieldShape(payload.AllowUserInfos), partys: enterpriseScopeFieldShape(payload.AllowPartys), tags: enterpriseScopeFieldShape(payload.AllowTags),
		userInfosDetail: enterpriseScopeEnvelopeDetail(payload.AllowUserInfos, "user"),
		partysDetail:    enterpriseScopeEnvelopeDetail(payload.AllowPartys, "partyid"),
		tagsDetail:      enterpriseScopeEnvelopeDetail(payload.AllowTags, "tagid"),
	}
	if len(bytes.TrimSpace(payload.AllowUserInfos)) == 0 || len(bytes.TrimSpace(payload.AllowPartys)) == 0 {
		return enterpriseAgentScope{}, "agent_scope_shape", evidence
	}
	if !enterpriseAllowTagsAreEmpty(payload.AllowTags) {
		return enterpriseAgentScope{}, "agent_tags", evidence
	}
	userIDs, err := enterpriseScopeUserIDs(payload.AllowUserInfos)
	if err != nil {
		return enterpriseAgentScope{}, "agent_users", evidence
	}
	departmentIDs, err := enterpriseScopeDepartmentIDs(payload.AllowPartys)
	if err != nil {
		return enterpriseAgentScope{}, "agent_departments", evidence
	}
	return enterpriseAgentScope{userIDs: userIDs, departmentIDs: departmentIDs}, "", evidence
}

func (client *Client) listEnterpriseEmployees(ctx context.Context, token string) ([]wecomport.EnterpriseEmployee, error) {
	scope, err := client.enterpriseAgentScope(ctx, token)
	if err != nil {
		return nil, err
	}
	departments, err := client.enterpriseDepartments(ctx, token)
	if err != nil {
		return nil, err
	}
	authorizedDepartments, err := enterpriseAuthorizedDepartments(departments, scope.departmentIDs)
	if err != nil {
		// The app scope says this department is visible, but the current
		// directory projection omitted it. Returning partial results would
		// turn an authorization/read failure into a false "not found".
		return nil, ErrResponse
	}
	scopes, err := enterpriseDirectoryScopes(authorizedDepartments)
	if err != nil {
		return nil, err
	}
	// Every authorized connected component gets exactly one recursive read. The
	// department projection may include branches outside allow_partys, so it is
	// first reduced to the allowed roots and their descendants. Any cycle in
	// that selected subtree is rejected by enterpriseDirectoryScopes.
	departmentEmployees, err := client.enterpriseMembers(ctx, token, scopes)
	if err != nil {
		return nil, err
	}
	directEmployees, err := client.enterpriseDirectEmployees(ctx, token, scope.userIDs)
	if err != nil {
		return nil, err
	}
	return mergeEnterpriseEmployees(departmentEmployees, directEmployees)
}

type enterpriseAgentScope struct {
	userIDs       []string
	departmentIDs []int64
}

// enterpriseAgentScope reads the application permission envelope before
// enumerating members. Department reads alone omit employees who are visible
// through allow_userinfos, while non-empty allow_tags cannot be expanded
// completely by this API and must therefore fail closed.
func (client *Client) enterpriseAgentScope(ctx context.Context, token string) (enterpriseAgentScope, error) {
	payload, err := client.request(ctx, "/cgi-bin/agent/get", url.Values{"access_token": {token}, "agentid": {client.config.AgentID}})
	if err != nil {
		return enterpriseAgentScope{}, err
	}
	// Missing user/department scope fields must never turn into an accidental
	// all-directory read. agent/get may omit allow_tags when no tag scope is
	// granted; that one omission is safely equivalent to an empty tag envelope.
	if len(bytes.TrimSpace(payload.AllowUserInfos)) == 0 || len(bytes.TrimSpace(payload.AllowPartys)) == 0 {
		return enterpriseAgentScope{}, ErrResponse
	}
	if !enterpriseAllowTagsAreEmpty(payload.AllowTags) {
		return enterpriseAgentScope{}, ErrResponse
	}
	userIDs, err := enterpriseScopeUserIDs(payload.AllowUserInfos)
	if err != nil {
		return enterpriseAgentScope{}, err
	}
	departmentIDs, err := enterpriseScopeDepartmentIDs(payload.AllowPartys)
	if err != nil {
		return enterpriseAgentScope{}, err
	}
	return enterpriseAgentScope{userIDs: userIDs, departmentIDs: departmentIDs}, nil
}

// enterpriseAllowTagsAreEmpty accepts only the permitted omitted/empty tag
// envelope. Any non-empty, malformed, or otherwise unknown value remains a
// fail-closed condition because this reader cannot enumerate tag members.
func enterpriseAllowTagsAreEmpty(raw json.RawMessage) bool {
	return !rawJSONHasItems(raw)
}

func rawJSONHasItems(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("{}")) || bytes.Equal(raw, []byte("[]")) {
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		// An unrecognized non-empty permission envelope is not safely empty.
		return true
	}
	for _, child := range value {
		child = bytes.TrimSpace(child)
		if len(child) > 0 && !bytes.Equal(child, []byte("null")) && !bytes.Equal(child, []byte("[]")) && !bytes.Equal(child, []byte("{}")) {
			return true
		}
	}
	return false
}

func enterpriseScopeUserIDs(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrResponse
	}
	users, exists := envelope["user"]
	if !exists {
		if len(envelope) == 0 {
			return nil, nil
		}
		return nil, ErrResponse
	}
	users = bytes.TrimSpace(users)
	if len(users) == 0 || bytes.Equal(users, []byte("null")) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(users, &entries); err != nil {
		return nil, ErrResponse
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		var legacyUserID string
		if err := json.Unmarshal(entry, &legacyUserID); err == nil {
			values = append(values, legacyUserID)
			continue
		}
		// agent/get's official envelope uses objects. Additional official
		// metadata is deliberately ignored; only the exact userid is trusted
		// for the directory read.
		var person struct {
			UserID string `json:"userid"`
		}
		if err := json.Unmarshal(entry, &person); err != nil || person.UserID == "" {
			return nil, ErrResponse
		}
		values = append(values, person.UserID)
	}
	return normalizeEnterpriseScopeUserIDs(values)
}

func normalizeEnterpriseScopeUserIDs(values []string) ([]string, error) {
	if len(values) > 10000 {
		return nil, ErrResponse
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		userID := strings.TrimSpace(value)
		if userID == "" || userID != value || invalid(userID) {
			return nil, ErrResponse
		}
		if _, duplicate := seen[userID]; duplicate {
			return nil, ErrResponse
		}
		seen[userID] = struct{}{}
		result = append(result, userID)
	}
	sort.Strings(result)
	return result, nil
}

func enterpriseScopeDepartmentIDs(raw json.RawMessage) ([]int64, error) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrResponse
	}
	parties, exists := envelope["partyid"]
	if !exists {
		if len(envelope) == 0 {
			return nil, nil
		}
		return nil, ErrResponse
	}
	var departmentIDs []int64
	if json.Unmarshal(parties, &departmentIDs) != nil || len(departmentIDs) > 500 {
		return nil, ErrResponse
	}
	seen := make(map[int64]struct{}, len(departmentIDs))
	for _, departmentID := range departmentIDs {
		if departmentID < 1 {
			return nil, ErrResponse
		}
		if _, duplicate := seen[departmentID]; duplicate {
			return nil, ErrResponse
		}
		seen[departmentID] = struct{}{}
	}
	result := make([]int64, 0, len(seen))
	for departmentID := range seen {
		result = append(result, departmentID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (client *Client) enterpriseDepartments(ctx context.Context, token string) ([]enterpriseDepartment, error) {
	payload, err := client.request(ctx, "/cgi-bin/department/simplelist", url.Values{"access_token": {token}})
	if err != nil {
		return nil, err
	}
	if len(payload.Departments) > 500 {
		return nil, ErrResponse
	}
	departments := make([]enterpriseDepartment, len(payload.Departments))
	seen := make(map[int64]struct{}, len(payload.Departments))
	for index, value := range payload.Departments {
		if value.ID < 1 || value.ParentID < 0 {
			return nil, ErrResponse
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return nil, ErrResponse
		}
		seen[value.ID] = struct{}{}
		departments[index] = enterpriseDepartment{id: value.ID, parentID: value.ParentID}
	}
	return departments, nil
}

type enterpriseDepartment struct {
	id       int64
	parentID int64
}

// enterpriseAuthorizedDepartments reduces the Provider's department snapshot
// to the explicit allow_partys roots and their descendants. The snapshot can
// contain other branches; they are not evidence that this application may
// enumerate them.
func enterpriseAuthorizedDepartments(departments []enterpriseDepartment, allowedIDs []int64) ([]enterpriseDepartment, error) {
	byID := make(map[int64]enterpriseDepartment, len(departments))
	children := make(map[int64][]int64, len(departments))
	for _, department := range departments {
		byID[department.id] = department
		children[department.parentID] = append(children[department.parentID], department.id)
	}
	selected := make(map[int64]struct{}, len(departments))
	pending := append([]int64(nil), allowedIDs...)
	for len(pending) > 0 {
		departmentID := pending[0]
		pending = pending[1:]
		if _, alreadySelected := selected[departmentID]; alreadySelected {
			continue
		}
		if _, exists := byID[departmentID]; !exists {
			return nil, ErrResponse
		}
		selected[departmentID] = struct{}{}
		pending = append(pending, children[departmentID]...)
	}
	if len(selected) > 500 {
		return nil, ErrResponse
	}
	result := make([]enterpriseDepartment, 0, len(selected))
	for departmentID := range selected {
		result = append(result, byID[departmentID])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func enterpriseDirectoryScopes(departments []enterpriseDepartment) ([]int64, error) {
	byID := make(map[int64]enterpriseDepartment, len(departments))
	for _, department := range departments {
		byID[department.id] = department
	}
	// Resolve every returned department to exactly one visible component root.
	// The root is either a normal parent_id=0 department or the highest visible
	// department beneath a parent that the application is not allowed to see.
	// A loop cannot be safely represented by user/simplelist and therefore
	// fails the complete-directory contract instead of silently losing members.
	roots := make(map[int64]struct{}, len(departments))
	for _, start := range departments {
		current := start
		seen := make(map[int64]struct{}, len(departments))
		for {
			if _, cycle := seen[current.id]; cycle {
				return nil, ErrResponse
			}
			seen[current.id] = struct{}{}
			if current.parentID == 0 {
				roots[current.id] = struct{}{}
				break
			}
			parent, parentVisible := byID[current.parentID]
			if !parentVisible {
				roots[current.id] = struct{}{}
				break
			}
			current = parent
		}
	}
	if len(roots) > 500 {
		return nil, ErrResponse
	}
	result := make([]int64, 0, len(roots))
	for id := range roots {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (client *Client) enterpriseMembers(ctx context.Context, token string, departmentIDs []int64) ([]wecomport.EnterpriseEmployee, error) {
	if len(departmentIDs) > 500 {
		return nil, classifyDirectoryReadError(ErrResponse)
	}
	if len(departmentIDs) == 0 {
		return []wecomport.EnterpriseEmployee{}, nil
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pages := make([][]wecomport.EnterpriseEmployee, len(departmentIDs))
	jobs := make(chan int)
	workers := 4
	if workers > len(departmentIDs) {
		workers = len(departmentIDs)
	}
	var group sync.WaitGroup
	var once sync.Once
	var firstErr error
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				page, err := client.enterpriseDepartmentMembers(readCtx, token, departmentIDs[index])
				if err != nil {
					fail(err)
					return
				}
				pages[index] = page
			}
		}()
	}
	for index := range departmentIDs {
		select {
		case <-readCtx.Done():
		case jobs <- index:
		}
		if readCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	group.Wait()
	if firstErr != nil || ctx.Err() != nil {
		if firstErr != nil {
			return nil, classifyDirectoryReadError(firstErr)
		}
		return nil, classifyDirectoryReadError(ErrUnavailable)
	}
	seen := make(map[string]struct{})
	result := make([]wecomport.EnterpriseEmployee, 0)
	for _, page := range pages {
		for _, employee := range page {
			if _, duplicate := seen[employee.UserID]; duplicate {
				continue
			}
			seen[employee.UserID] = struct{}{}
			result = append(result, employee)
		}
	}
	if len(result) > 10000 {
		return nil, classifyDirectoryReadError(ErrResponse)
	}
	// Provider ordering is not a pagination contract. Sorting the complete
	// snapshot by exact employee userid makes the locally signed cursor stable
	// across otherwise identical reads.
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	return result, nil
}

// enterpriseDirectEmployees completes the application-visible scope for
// employees granted directly to the app rather than through a department.
// It deliberately uses bounded, read-only exact lookups; no directory row is
// written and a single unreadable direct member invalidates the whole list.
func (client *Client) enterpriseDirectEmployees(ctx context.Context, token string, userIDs []string) ([]wecomport.EnterpriseEmployee, error) {
	if len(userIDs) == 0 {
		return []wecomport.EnterpriseEmployee{}, nil
	}
	if len(userIDs) > 10000 {
		return nil, ErrResponse
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]wecomport.EnterpriseEmployee, len(userIDs))
	jobs := make(chan int)
	workers := 4
	if workers > len(userIDs) {
		workers = len(userIDs)
	}
	var group sync.WaitGroup
	var once sync.Once
	var firstErr error
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				employee, err := client.enterpriseEmployeeWithToken(readCtx, token, userIDs[index])
				if err != nil {
					fail(err)
					return
				}
				results[index] = employee
			}
		}()
	}
	for index := range userIDs {
		select {
		case <-readCtx.Done():
		case jobs <- index:
		}
		if readCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	group.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return results, nil
}

func mergeEnterpriseEmployees(groups ...[]wecomport.EnterpriseEmployee) ([]wecomport.EnterpriseEmployee, error) {
	byID := make(map[string]wecomport.EnterpriseEmployee)
	for _, group := range groups {
		for _, employee := range group {
			if existing, duplicate := byID[employee.UserID]; duplicate {
				if existing.DisplayName != employee.DisplayName {
					return nil, ErrResponse
				}
				continue
			}
			byID[employee.UserID] = employee
		}
	}
	result := make([]wecomport.EnterpriseEmployee, 0, len(byID))
	for _, employee := range byID {
		result = append(result, employee)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	return result, nil
}

func (client *Client) enterpriseDepartmentMembers(ctx context.Context, token string, departmentID int64) ([]wecomport.EnterpriseEmployee, error) {
	values := url.Values{"access_token": {token}, "department_id": {strconv.FormatInt(departmentID, 10)}}
	values.Set("fetch_child", "1")
	payload, err := client.request(ctx, "/cgi-bin/user/simplelist", values)
	if err != nil {
		return nil, err
	}
	if len(payload.Users) > 10000 {
		return nil, ErrResponse
	}
	result := make([]wecomport.EnterpriseEmployee, 0, len(payload.Users))
	seen := make(map[string]struct{}, len(payload.Users))
	for _, value := range payload.Users {
		userID, name := strings.TrimSpace(value.UserID), strings.TrimSpace(value.Name)
		if userID == "" || userID != value.UserID || invalid(userID) || !validDisplayName(name) {
			return nil, ErrResponse
		}
		if _, duplicate := seen[userID]; duplicate {
			return nil, ErrResponse
		}
		seen[userID] = struct{}{}
		result = append(result, wecomport.EnterpriseEmployee{UserID: userID, DisplayName: name})
	}
	return result, nil
}

// ReadEnterpriseEmployee verifies one exact employee against the same
// application-visible corporate directory. It never mutates WeCom or local
// Access state.
func (client *Client) ReadEnterpriseEmployee(ctx context.Context, userID string) (wecomport.EnterpriseEmployee, error) {
	if !client.EnterpriseDirectoryReady() || invalid(userID) || strings.TrimSpace(userID) != userID {
		return wecomport.EnterpriseEmployee{}, wecomport.ErrDirectoryDisabled
	}
	readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	token, err := client.accessToken(readCtx)
	if err != nil {
		return wecomport.EnterpriseEmployee{}, classifyDirectoryReadError(err)
	}
	employee, err := client.enterpriseEmployeeWithToken(readCtx, token, userID)
	if directoryTokenExpired(err) {
		token, err = client.refreshAccessToken(readCtx)
		if err == nil {
			employee, err = client.enterpriseEmployeeWithToken(readCtx, token, userID)
		}
	}
	if enterpriseEmployeeNotFound(err) {
		return wecomport.EnterpriseEmployee{}, wecomport.ErrEnterpriseEmployeeNotFound
	}
	if err != nil {
		return wecomport.EnterpriseEmployee{}, classifyDirectoryReadError(err)
	}
	return employee, nil
}

func (client *Client) enterpriseEmployeeWithToken(ctx context.Context, token, userID string) (wecomport.EnterpriseEmployee, error) {
	payload, err := client.request(ctx, "/cgi-bin/user/get", url.Values{"access_token": {token}, "userid": {userID}})
	if err != nil {
		return wecomport.EnterpriseEmployee{}, err
	}
	returnedID, name := strings.TrimSpace(payload.UserIDLower), strings.TrimSpace(payload.Name)
	if returnedID != userID || !validDisplayName(name) {
		return wecomport.EnterpriseEmployee{}, ErrResponse
	}
	return wecomport.EnterpriseEmployee{UserID: returnedID, DisplayName: name}, nil
}

func enterpriseEmployeeNotFound(cause error) bool {
	var providerFailure *providerResponseError
	if !errors.As(cause, &providerFailure) {
		return false
	}
	// Both codes are documented/returned by user/get for an unknown member.
	// They are safe to distinguish from credential, permission and transient
	// failures, which remain unavailable.
	return providerFailure.errCode == 40003 || providerFailure.errCode == 60111
}

func (client *Client) refreshAccessToken(ctx context.Context) (string, error) {
	client.mu.Lock()
	delete(client.tokens, "access_token")
	client.mu.Unlock()
	return client.accessToken(ctx)
}

var _ wecomport.EnterpriseEmployeeDirectory = (*Client)(nil)
