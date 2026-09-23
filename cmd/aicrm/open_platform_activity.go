package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

const (
	v1ActivityDefaultLimit int32 = 50
	v1ActivityMaximumLimit int32 = 100
	v1ActivityCursorV            = 1
)

var v1ActivityTypes = map[string]struct{}{
	"message": {},
	"survey":  {},
	"radar":   {},
	"order":   {},
}

type openPlatformActivityReaders struct {
	messages   archiveport.CustomerMessageReader
	survey     surveyport.CustomerHistoryReader
	radar      radarport.CustomerActivityReader
	orders     orderport.CustomerActivityReader
	signingKey []byte
}

type v1CustomerActivitiesInput struct {
	CustomerID int64    `json:"customer_id"`
	Types      []string `json:"types"`
	Cursor     string   `json:"cursor"`
	Limit      int32    `json:"limit"`
}

type v1ActivityPosition struct {
	OccurredAt time.Time `json:"occurred_at"`
	ID         int64     `json:"id"`
}

type v1ActivityCursor struct {
	Version     int                           `json:"v"`
	CustomerID  int64                         `json:"customer_id"`
	Types       []string                      `json:"types"`
	GrantDigest string                        `json:"grant_digest"`
	Watermark   time.Time                     `json:"watermark"`
	Positions   map[string]v1ActivityPosition `json:"positions"`
}

type v1ActivityCursorEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

type v1ActivityItem struct {
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       any       `json:"data"`
	position   v1ActivityPosition
}

func (executor *openPlatformExecutor) v1CustomerActivities(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.activities == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "customer activities are unavailable")
	}
	var input v1CustomerActivitiesInput
	if err := decodeV1JSON(raw, &input); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid activity request")
	}
	types, err := normalizeV1ActivityTypes(input.Types)
	if err != nil || input.CustomerID < 1 || input.Limit < 0 || input.Limit > v1ActivityMaximumLimit || len(input.Cursor) > 4096 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid activity request")
	}
	if input.Limit == 0 {
		input.Limit = v1ActivityDefaultLimit
	}
	customerID := customerdomain.CustomerID(input.CustomerID)
	if err := executor.ensureCustomerScope(ctx, principal, customerID, nil); err != nil {
		return openplatformport.Result{}, v1CustomerScopeError(err)
	}
	grant := v1ActivityGrantDigest(principal)
	cursor, err := executor.v1ActivityCursor(input, types, grant)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid activity cursor")
	}
	items, err := executor.readV1Activities(ctx, customerID, types, input.Limit, cursor)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "customer activity projection is unavailable")
	}
	limit := int(input.Limit)
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	for _, item := range items {
		cursor.Positions[item.Type] = item.position
	}
	resultItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resultItems = append(resultItems, map[string]any{
			"activity_id": item.Type + ":" + item.ID,
			"type":        item.Type,
			"occurred_at": item.OccurredAt.UTC(),
			"source":      v1ActivitySource(item.Type),
			"payload":     item.Data,
		})
	}
	result := map[string]any{"customer_id": input.CustomerID, "types": types, "items": resultItems, "as_of": cursor.Watermark.UTC()}
	if hasMore {
		next, encodeErr := encodeV1ActivityCursor(executor.activities.signingKey, cursor)
		if encodeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "activity cursor is unavailable")
		}
		result["next_cursor"] = next
	}
	return openplatformport.Result{Data: result}, nil
}

func (executor *openPlatformExecutor) v1ActivityCursor(input v1CustomerActivitiesInput, types []string, grant string) (v1ActivityCursor, error) {
	if input.Cursor == "" {
		now := time.Now
		if executor.activityNow != nil {
			now = executor.activityNow
		}
		return v1ActivityCursor{Version: v1ActivityCursorV, CustomerID: input.CustomerID, Types: append([]string(nil), types...), GrantDigest: grant, Watermark: now().UTC(), Positions: map[string]v1ActivityPosition{}}, nil
	}
	cursor, err := decodeV1ActivityCursor(executor.activities.signingKey, input.Cursor)
	if err != nil || cursor.Version != v1ActivityCursorV || cursor.CustomerID != input.CustomerID || cursor.GrantDigest != grant || cursor.Watermark.IsZero() || !sameV1ActivityTypes(cursor.Types, types) {
		return v1ActivityCursor{}, errors.New("activity cursor does not match invocation")
	}
	if cursor.Positions == nil {
		cursor.Positions = map[string]v1ActivityPosition{}
	}
	for activityType, position := range cursor.Positions {
		if _, allowed := v1ActivityTypes[activityType]; !allowed || position.ID < 1 || position.OccurredAt.IsZero() || position.OccurredAt.After(cursor.Watermark) {
			return v1ActivityCursor{}, errors.New("invalid activity cursor position")
		}
	}
	return cursor, nil
}

func (executor *openPlatformExecutor) readV1Activities(ctx context.Context, customerID customerdomain.CustomerID, types []string, limit int32, cursor v1ActivityCursor) ([]v1ActivityItem, error) {
	readLimit := limit + 1
	items := make([]v1ActivityItem, 0, int(readLimit)*len(types))
	for _, activityType := range types {
		position := cursor.Positions[activityType]
		switch activityType {
		case "message":
			page, err := executor.activities.messages.CustomerMessages(ctx, archiveport.CustomerQuery{CustomerID: customerID, Limit: int(readLimit), Watermark: cursor.Watermark, AfterAt: position.OccurredAt, AfterID: position.ID})
			if err != nil {
				return nil, err
			}
			for _, value := range page.Items {
				items = append(items, v1ActivityItem{Type: activityType, ID: strconv.FormatInt(value.ID, 10), OccurredAt: value.OccurredAt, position: v1ActivityPosition{OccurredAt: value.OccurredAt, ID: value.ID}, Data: map[string]any{
					"chat_type": value.ChatType, "message_type": value.MessageType, "direction": value.Direction, "render_type": value.RenderType,
				}})
			}
		case "survey":
			page, err := executor.activities.survey.CustomerHistoryWindow(ctx, surveyport.CustomerHistoryQuery{CustomerID: int64(customerID), Limit: readLimit, Watermark: cursor.Watermark, AfterAt: position.OccurredAt, AfterID: surveyport.ID(position.ID)})
			if err != nil {
				return nil, err
			}
			for _, value := range page.Items {
				items = append(items, v1ActivityItem{Type: activityType, ID: strconv.FormatInt(int64(value.ID), 10), OccurredAt: value.SubmittedAt, position: v1ActivityPosition{OccurredAt: value.SubmittedAt, ID: int64(value.ID)}, Data: map[string]any{
					"questionnaire_id": value.QuestionnaireID, "questionnaire_title": value.QuestionnaireTitle, "total_score": value.TotalScore, "assessment_result": value.Result,
				}})
			}
		case "radar":
			page, err := executor.activities.radar.CustomerActivities(ctx, radarport.CustomerActivityQuery{CustomerID: customerID, Limit: readLimit, Watermark: cursor.Watermark, AfterAt: position.OccurredAt, AfterID: position.ID})
			if err != nil {
				return nil, err
			}
			for _, value := range page.Items {
				items = append(items, v1ActivityItem{Type: activityType, ID: strconv.FormatInt(value.EventID, 10), OccurredAt: value.OccurredAt, position: v1ActivityPosition{OccurredAt: value.OccurredAt, ID: value.EventID}, Data: map[string]any{
					"radar_id": value.RadarID, "stage": value.Stage,
				}})
			}
		case "order":
			page, err := executor.activities.orders.CustomerActivities(ctx, orderport.CustomerActivityQuery{CustomerID: int64(customerID), Limit: readLimit, Watermark: cursor.Watermark, AfterAt: position.OccurredAt, AfterID: position.ID})
			if err != nil {
				return nil, err
			}
			for _, value := range page.Items {
				items = append(items, v1ActivityItem{Type: activityType, ID: strconv.FormatInt(value.OrderID, 10), OccurredAt: value.OccurredAt, position: v1ActivityPosition{OccurredAt: value.OccurredAt, ID: value.OrderID}, Data: map[string]any{
					"relationship": value.Relationship, "provider": value.Provider, "status": value.Status, "amount": value.Amount, "refunded_minor": value.RefundedMinor, "record_origin": value.RecordOrigin,
				}})
			}
		default:
			return nil, fmt.Errorf("unknown activity type")
		}
	}
	sort.Slice(items, func(left, right int) bool {
		if !items[left].OccurredAt.Equal(items[right].OccurredAt) {
			return items[left].OccurredAt.After(items[right].OccurredAt)
		}
		if items[left].Type != items[right].Type {
			return items[left].Type < items[right].Type
		}
		return items[left].position.ID > items[right].position.ID
	})
	return items, nil
}

func v1ActivitySource(activityType string) string {
	switch activityType {
	case "message":
		return "message_archive"
	case "survey", "radar", "order":
		return activityType
	default:
		return ""
	}
}

func normalizeV1ActivityTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"message", "order", "radar", "survey"}, nil
	}
	if len(values) > len(v1ActivityTypes) {
		return nil, errors.New("too many activity types")
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value != raw {
			return nil, errors.New("activity type is not normalized")
		}
		if _, known := v1ActivityTypes[value]; !known {
			return nil, errors.New("unknown activity type")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, errors.New("duplicate activity type")
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func sameV1ActivityTypes(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func v1ActivityGrantDigest(principal accessdomain.MachinePrincipal) string {
	scopes := append([]string(nil), principal.Scopes...)
	capabilities := append([]string(nil), principal.Capabilities...)
	sort.Strings(scopes)
	sort.Strings(capabilities)
	payload, _ := json.Marshal(struct {
		ClientID     string   `json:"client_id"`
		ClientRecord int64    `json:"client_record"`
		Audience     string   `json:"audience"`
		Scopes       []string `json:"scopes"`
		Capabilities []string `json:"capabilities"`
		CorpID       string   `json:"corp_id"`
		OwnerScope   []byte   `json:"owner_scope"`
		AuthVersion  int64    `json:"auth_version"`
		DirectKey    bool     `json:"direct_key"`
	}{principal.ClientID, principal.ClientRecord, principal.Audience, scopes, capabilities, principal.CorpID, principal.OwnerScope.JSON(), principal.AuthVersion, principal.DirectKey})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func encodeV1ActivityCursor(key []byte, cursor v1ActivityCursor) (string, error) {
	cursor.Types = append([]string(nil), cursor.Types...)
	sort.Strings(cursor.Types)
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	envelope, err := json.Marshal(v1ActivityCursorEnvelope{Payload: payload, MAC: hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

func decodeV1ActivityCursor(key []byte, value string) (v1ActivityCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > 4096 || !openplatformport.ValidJSONObject(decoded) {
		return v1ActivityCursor{}, errors.New("invalid activity cursor")
	}
	var envelope v1ActivityCursorEnvelope
	if err = json.Unmarshal(decoded, &envelope); err != nil || !openplatformport.ValidJSONObject(envelope.Payload) {
		return v1ActivityCursor{}, errors.New("invalid activity cursor")
	}
	provided, err := hex.DecodeString(envelope.MAC)
	if err != nil || len(provided) != sha256.Size {
		return v1ActivityCursor{}, errors.New("invalid activity cursor")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(envelope.Payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return v1ActivityCursor{}, errors.New("invalid activity cursor")
	}
	var cursor v1ActivityCursor
	if err = json.Unmarshal(envelope.Payload, &cursor); err != nil {
		return v1ActivityCursor{}, errors.New("invalid activity cursor")
	}
	return cursor, nil
}
