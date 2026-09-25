package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type v1CustomerListQuery struct {
	Limit  *int   `json:"limit"`
	From   string `json:"updated_from"`
	To     string `json:"updated_to"`
	Cursor string `json:"cursor"`
}

type v1CustomerListCursor struct {
	Version int    `json:"v"`
	Window  string `json:"w"`
	Offset  int    `json:"o"`
}

func (executor *openPlatformExecutor) v1ListCustomers(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor.contacts == nil || executor.contactStatuses == nil || executor.contactStaff == nil || executor.contactWindows == nil || executor.contactReadUOW == nil || executor.contactWriteUOW == nil || len(executor.v1ExternalCursorKey) < 16 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact sync unavailable")
	}
	var query v1CustomerListQuery
	if decodeV1JSON(raw, &query) != nil || principal.CorpID == "" || len(query.Cursor) > 1024 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid contact query")
	}
	limit := 100
	if query.Limit != nil {
		if *query.Limit < 1 || *query.Limit > 100 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid contact limit")
		}
		limit = *query.Limit
	}
	if !stringIn(principal.OwnerScope["corp_id"], principal.CorpID) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "corporate owner scope required")
	}
	grant := v1ActivityGrantDigest(principal)
	windowID, offset := "", 0
	if query.Cursor != "" {
		cursor, err := decodeV1CustomerListCursor(executor.v1ExternalCursorKey, query.Cursor)
		if err != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid contact cursor")
		}
		windowID, offset = cursor.Window, cursor.Offset
	} else {
		from, to, err := v1CustomerListBounds(query, time.Now().UTC())
		if err != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid contact window")
		}
		window, err := executor.freezeCustomerList(ctx, principal, grant, from, to)
		if err != nil {
			return openplatformport.Result{}, err
		}
		if err = executor.contactWriteUOW.Within(ctx, func(tx context.Context) error { return executor.contactWindows.FreezeCustomerWindow(tx, window) }); err != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact snapshot unavailable")
		}
		windowID = window.ID
	}
	var page openplatformport.CustomerWindow
	err := executor.contactWriteUOW.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = executor.contactWindows.ReadCustomerWindow(tx, windowID, offset, limit)
		return readErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "contact cursor expired")
	}
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact page unavailable")
	}
	if page.ClientID != principal.ClientID || page.GrantDigest != grant || offset > page.ItemCount {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "contact cursor outside grant")
	}
	if !v1CustomerListBoundsMatch(query, page.From, page.To) || (offset < page.ItemCount && len(page.Items) == 0) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "contact cursor/window mismatch")
	}
	items := make([]json.RawMessage, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, item.Data)
	}
	data := map[string]any{"items": items, "item_count": page.ItemCount, "updated_from": page.From, "updated_to": page.To, "window_complete": offset+len(items) == page.ItemCount}
	if offset+len(items) < page.ItemCount {
		next, encodeErr := encodeV1CustomerListCursor(executor.v1ExternalCursorKey, v1CustomerListCursor{Version: 1, Window: windowID, Offset: offset + len(items)})
		if encodeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact cursor unavailable")
		}
		data["next_cursor"] = next
	}
	return openplatformport.Result{Data: data}, nil
}

func stringIn(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func v1CustomerListBounds(query v1CustomerListQuery, now time.Time) (time.Time, time.Time, error) {
	from, to := time.Unix(0, 0).UTC(), now
	var err error
	if query.From != "" {
		from, err = time.Parse(time.RFC3339Nano, query.From)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if query.To != "" {
		to, err = time.Parse(time.RFC3339Nano, query.To)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if !from.Before(to) || to.After(now) {
		return time.Time{}, time.Time{}, errors.New("invalid interval")
	}
	return from.UTC(), to.UTC(), nil
}

func v1CustomerListBoundsMatch(query v1CustomerListQuery, from, to time.Time) bool {
	if query.From != "" {
		parsed, err := time.Parse(time.RFC3339Nano, query.From)
		if err != nil || !parsed.Equal(from) {
			return false
		}
	}
	if query.To != "" {
		parsed, err := time.Parse(time.RFC3339Nano, query.To)
		if err != nil || !parsed.Equal(to) {
			return false
		}
	}
	return true
}

func (executor *openPlatformExecutor) freezeCustomerList(ctx context.Context, principal accessdomain.MachinePrincipal, grant string, from, to time.Time) (openplatformport.CustomerWindow, error) {
	items := make([]openplatformport.CustomerWindowItem, 0)
	err := executor.contactReadUOW.Within(ctx, func(tx context.Context) error {
		contacts, unresolved, err := executor.contacts.MachineContactRows(tx, "wecom-corp:"+principal.CorpID)
		if err != nil || len(contacts)+len(unresolved) > 100000 {
			return errors.New("contact source unavailable")
		}
		ids := make([]int64, 0, len(contacts))
		ownerIDs := make([]string, 0)
		ownersSeen := map[string]bool{}
		for _, contact := range contacts {
			if contact.CustomerID < 1 || contact.ChangedAt.IsZero() {
				return errors.New("invalid contact source")
			}
			ids = append(ids, contact.CustomerID)
			if contact.OwnerUserID != "" && !ownersSeen[contact.OwnerUserID] {
				ownerIDs = append(ownerIDs, contact.OwnerUserID)
				ownersSeen[contact.OwnerUserID] = true
			}
		}
		states, err := executor.contactStatuses.MachineContactStatuses(tx, ids)
		if err != nil || len(states) != len(contacts) {
			return errors.New("customer state unavailable")
		}
		touches := map[int64]archiveport.MachineContactTouch{}
		if executor.contactTouches != nil {
			touches, err = executor.contactTouches.MachineContactTouchTimes(tx, "wecom-corp:"+principal.CorpID, ids)
			if err != nil {
				return errors.New("contact touch projection unavailable")
			}
		}
		names := map[string]string{}
		for start := 0; start < len(ownerIDs); start += 50 {
			end := start + 50
			if end > len(ownerIDs) {
				end = len(ownerIDs)
			}
			users, readErr := executor.contactStaff.UsersByWeComUserIDs(tx, ownerIDs[start:end])
			if readErr != nil {
				return readErr
			}
			for _, user := range users {
				if user.Active {
					names[user.WeComUserID] = user.DisplayName
				}
			}
		}
		for _, contact := range contacts {
			state := states[contact.CustomerID]
			touch := touches[contact.CustomerID]
			changed := contact.ChangedAt.UTC()
			if state.ChangedAt.After(changed) {
				changed = state.ChangedAt.UTC()
			}
			if touch.LastStaffMessageAt != nil && touch.LastStaffMessageAt.After(changed) {
				changed = touch.LastStaffMessageAt.UTC()
			}
			if touch.LastCustomerMessageAt != nil && touch.LastCustomerMessageAt.After(changed) {
				changed = touch.LastCustomerMessageAt.UTC()
			}
			if changed.Before(from) || !changed.Before(to) {
				continue
			}
			if !principal.OwnerScope.Allows(map[string]string{"corp_id": principal.CorpID, "customer_id": strconv.FormatInt(contact.CustomerID, 10), "owner_userid": contact.OwnerUserID}) {
				continue
			}
			removed := contact.RemovedAt
			if state.State == "closed" || state.State == "merged" {
				at := state.ChangedAt.UTC()
				removed = &at
			}
			key := "v4:customer:" + strconv.FormatInt(contact.CustomerID, 10)
			tags := append([]string{}, contact.Tags...)
			sort.Strings(tags)
			projection, marshalErr := json.Marshal(map[string]any{"record_id": key, "safe_user_ref": customerdomain.CanonicalOneIDLabel(customerdomain.CustomerID(contact.CustomerID)), "identity_status": contact.IdentityStatus, "owner_userid": contact.OwnerUserID, "owner_display_name": names[contact.OwnerUserID], "owner_status": contact.OwnerStatus, "tags": tags, "binding_status": contact.BindingStatus, "contact_permission": "unknown", "do_not_disturb": "unknown", "human_handoff": "unknown", "last_real_touch_at": touch.LastStaffMessageAt, "last_interaction_at": touch.LastCustomerMessageAt, "updated_at": changed, "deleted_at": removed})
			if marshalErr != nil {
				return marshalErr
			}
			items = append(items, openplatformport.CustomerWindowItem{Key: key, ChangedAt: changed, Data: projection})
		}
		for _, row := range unresolved {
			if len(row.SourceDigest) != sha256.Size || row.ChangedAt.IsZero() {
				return errors.New("invalid unresolved source")
			}
			if row.ChangedAt.Before(from) || !row.ChangedAt.Before(to) || !principal.OwnerScope.Allows(map[string]string{"corp_id": principal.CorpID}) {
				continue
			}
			mac := hmac.New(sha256.New, executor.v1ExternalCursorKey)
			_, _ = mac.Write([]byte("v4:contact-unresolved:" + principal.CorpID + ":"))
			_, _ = mac.Write(row.SourceDigest)
			key := "v4:unresolved:" + hex.EncodeToString(mac.Sum(nil)[:16])
			var removed *time.Time
			if row.Outcome == "activated" || row.Outcome == "already_linked" {
				at := row.ChangedAt.UTC()
				removed = &at
			} else if row.Outcome != "conflict" && row.Outcome != "terminal_failed" {
				return errors.New("invalid unresolved outcome")
			}
			projection, marshalErr := json.Marshal(map[string]any{"record_id": key, "safe_user_ref": nil, "identity_status": "unresolved", "owner_userid": "", "owner_display_name": "", "owner_status": "unknown", "tags": []string{}, "binding_status": "unknown", "contact_permission": "unknown", "do_not_disturb": "unknown", "human_handoff": "unknown", "last_real_touch_at": nil, "last_interaction_at": nil, "updated_at": row.ChangedAt.UTC(), "deleted_at": removed})
			if marshalErr != nil {
				return marshalErr
			}
			items = append(items, openplatformport.CustomerWindowItem{Key: key, ChangedAt: row.ChangedAt.UTC(), Data: projection})
		}
		return nil
	})
	if err != nil {
		return openplatformport.CustomerWindow{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact facts could not be frozen")
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ChangedAt.Equal(items[j].ChangedAt) {
			return items[i].Key < items[j].Key
		}
		return items[i].ChangedAt.Before(items[j].ChangedAt)
	})
	identifier := make([]byte, 16)
	if _, err = rand.Read(identifier); err != nil {
		return openplatformport.CustomerWindow{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "contact snapshot identifier unavailable")
	}
	return openplatformport.CustomerWindow{ID: hex.EncodeToString(identifier), ClientID: principal.ClientID, GrantDigest: grant, From: from, To: to, ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Items: items, ItemCount: len(items)}, nil
}

func encodeV1CustomerListCursor(key []byte, cursor v1CustomerListCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("v4-contact-list\x00"))
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeV1CustomerListCursor(key []byte, value string) (v1CustomerListCursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return v1CustomerListCursor{}, errors.New("invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return v1CustomerListCursor{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return v1CustomerListCursor{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("v4-contact-list\x00"))
	_, _ = mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return v1CustomerListCursor{}, errors.New("invalid signature")
	}
	var cursor v1CustomerListCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || len(cursor.Window) != 32 || cursor.Offset < 1 {
		return v1CustomerListCursor{}, errors.New("invalid cursor payload")
	}
	return cursor, nil
}
