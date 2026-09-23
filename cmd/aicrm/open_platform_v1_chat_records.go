package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

const v1ChatRecordsOperation = "chat.records.list"

type v1ChatRecordsInput struct {
	CustomerID       int64  `json:"customer_id"`
	ChatType         string `json:"chat_type"`
	StaffUserID      int64  `json:"staff_user_id"`
	StaffWeComUserID string `json:"staff_wecom_userid"`
	OccurredFrom     *int64 `json:"occurred_from"`
	OccurredTo       *int64 `json:"occurred_to"`
	SourceSystem     string `json:"source_system"`
	SourceRecordID   string `json:"source_record_id"`
	MessageID        string `json:"message_id"`
	Limit            int32  `json:"limit"`
	Cursor           string `json:"cursor"`
}

// v1ChatRecordsCursor binds the caller's effective scope and filters to an
// archive-only keyset. It contains no chat content, participant identity, or
// Provider record identifier.
type v1ChatRecordsCursor struct {
	V                int       `json:"v"`
	Operation        string    `json:"operation"`
	Grant            string    `json:"grant"`
	Filters          string    `json:"filters"`
	OccurredTo       time.Time `json:"occurred_to"`
	BeforeOccurredAt time.Time `json:"before_occurred_at"`
	BeforeMessageID  int64     `json:"before_message_id"`
	StaffUserID      int64     `json:"staff_user_id"`
}

func (executor *openPlatformExecutor) v1ChatRecords(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.archive == nil || len(executor.v1ExternalCursorKey) < 16 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
	}
	reader, ok := executor.archive.(archiveport.V1ChatRecordReader)
	if !ok || reader == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
	}
	var in v1ChatRecordsInput
	if err := decodeV1JSON(raw, &in); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records request")
	}
	query, err := v1ChatRecordsQuery(in)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records request")
	}
	customerID := customerdomain.CustomerID(in.CustomerID)
	if err = executor.ensureCustomerScope(ctx, principal, customerID, nil); err != nil {
		return openplatformport.Result{}, v1CustomerScopeError(err)
	}
	query.CustomerID = customerID
	if in.StaffWeComUserID != "" {
		resolver, resolverOK := executor.archive.(archiveport.V1ChatStaffResolver)
		if !resolverOK || resolver == nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
		}
		resolvedStaffID, resolveErr := resolver.V1ChatStaffID(ctx, in.StaffWeComUserID)
		if errors.Is(resolveErr, archiveport.ErrStaffNotFound) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records request")
		}
		if resolveErr != nil || resolvedStaffID < 1 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
		}
		if query.StaffUserID != 0 && query.StaffUserID != resolvedStaffID {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records request")
		}
		query.StaffUserID = resolvedStaffID
	}
	grant, filters := v1ActivityGrantDigest(principal), v1ChatRecordsFilterDigest(in)
	if in.Cursor != "" {
		cursor, decodeErr := decodeV1ChatRecordsCursor(executor.v1ExternalCursorKey, in.Cursor)
		if decodeErr != nil || cursor.V != v1ExternalRecordsCursorV || cursor.Operation != v1ChatRecordsOperation || cursor.Grant != grant || cursor.Filters != filters || cursor.OccurredTo.IsZero() || cursor.BeforeOccurredAt.IsZero() || cursor.BeforeMessageID < 1 || cursor.StaffUserID != query.StaffUserID {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records cursor")
		}
		if !query.EndAt.IsZero() && !query.EndAt.Equal(cursor.OccurredTo) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid chat records cursor")
		}
		query.EndAt = cursor.OccurredTo.UTC()
		query.BeforeOccurredAt = cursor.BeforeOccurredAt.UTC()
		query.BeforeMessageID = cursor.BeforeMessageID
	} else if query.EndAt.IsZero() {
		now := time.Now().UTC()
		if executor.activityNow != nil {
			now = executor.activityNow().UTC()
		}
		query.EndAt = now
	}
	page, err := reader.V1ChatRecords(ctx, query)
	if err != nil {
		if errors.Is(err, archiveport.ErrNotReady) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
		}
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records are unavailable")
	}
	if len(page.Items) == 0 && page.HasMore {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records page is unavailable")
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		staff := make([]map[string]any, 0, len(item.Staff))
		for _, member := range item.Staff {
			if member.ID < 1 || strings.TrimSpace(member.DisplayName) == "" {
				return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records staff projection is unavailable")
			}
			staff = append(staff, map[string]any{
				"staff_id":     strconv.FormatInt(member.ID, 10),
				"display_name": member.DisplayName,
			})
		}
		items = append(items, map[string]any{
			"message_id":           item.MessageID,
			"customer_id":          strconv.FormatInt(int64(customerID), 10),
			"identity_status":      "resolved",
			"chat_type":            item.ChatType,
			"message_type":         item.MessageType,
			"content":              item.Content,
			"render_type":          item.RenderType,
			"direction":            item.Direction,
			"occurred_at":          item.OccurredAt.UTC(),
			"conversation_id":      item.ConversationID,
			"group_name":           item.GroupName,
			"media_archive_status": item.MediaArchiveStatus,
			"media_availability":   item.MediaAvailability,
			"staff":                staff,
			"source_system":        item.SourceSystem,
			"source_record_id":     item.SourceRecordID,
		})
	}
	result := map[string]any{
		"customer_id": strconv.FormatInt(int64(customerID), 10),
		"items":       items,
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		next, encodeErr := encodeV1ChatRecordsCursor(executor.v1ExternalCursorKey, v1ChatRecordsCursor{
			V:                v1ExternalRecordsCursorV,
			Operation:        v1ChatRecordsOperation,
			Grant:            grant,
			Filters:          filters,
			OccurredTo:       query.EndAt.UTC(),
			BeforeOccurredAt: last.OccurredAt.UTC(),
			BeforeMessageID:  parseV1ArchiveSourceID(last.SourceRecordID),
			StaffUserID:      query.StaffUserID,
		})
		if encodeErr != nil || parseV1ArchiveSourceID(last.SourceRecordID) < 1 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "chat records cursor is unavailable")
		}
		result["next_cursor"] = next
	}
	return openplatformport.Result{Data: result}, nil
}

func v1ChatRecordsQuery(in v1ChatRecordsInput) (archiveport.V1ChatRecordQuery, error) {
	chatType := in.ChatType
	if chatType == "" {
		chatType = "private"
	}
	if in.CustomerID < 1 || (chatType != "private" && chatType != "group") || in.StaffUserID < 0 ||
		strings.TrimSpace(in.StaffWeComUserID) != in.StaffWeComUserID || len(in.StaffWeComUserID) > 128 || (chatType == "private" && in.StaffUserID == 0 && in.StaffWeComUserID == "") ||
		(in.SourceSystem == "") != (in.SourceRecordID == "") || (in.SourceSystem != "" && in.SourceSystem != "message_archive") ||
		strings.TrimSpace(in.SourceRecordID) != in.SourceRecordID || len(in.SourceRecordID) > 128 || strings.TrimSpace(in.MessageID) != in.MessageID || len(in.MessageID) > 512 || in.Limit < 0 || (in.Limit != 0 && in.Limit != 20) || len(in.Cursor) > 4096 {
		return archiveport.V1ChatRecordQuery{}, errors.New("invalid chat records request")
	}
	start, err := v1ExternalRecordsUnixTime(in.OccurredFrom)
	if err != nil {
		return archiveport.V1ChatRecordQuery{}, err
	}
	end, err := v1ExternalRecordsUnixTime(in.OccurredTo)
	if err != nil || (!start.IsZero() && !end.IsZero() && !start.Before(end)) {
		return archiveport.V1ChatRecordQuery{}, errors.New("invalid chat records time range")
	}
	return archiveport.V1ChatRecordQuery{
		ChatType:       chatType,
		StaffUserID:    in.StaffUserID,
		StartAt:        start,
		EndAt:          end,
		SourceSystem:   in.SourceSystem,
		SourceRecordID: in.SourceRecordID,
		MessageID:      in.MessageID,
		Limit:          20,
	}, nil
}

func v1ChatRecordsFilterDigest(in v1ChatRecordsInput) string {
	in.Cursor = ""
	in.Limit = 20
	return v1ExternalRecordsDigest(in)
}

func parseV1ArchiveSourceID(raw string) int64 {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0
	}
	return id
}

func encodeV1ChatRecordsCursor(key []byte, cursor v1ChatRecordsCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	envelope, err := json.Marshal(v1ExternalRecordsCursorEnvelope{Payload: payload, MAC: hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

func decodeV1ChatRecordsCursor(key []byte, raw string) (v1ChatRecordsCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return v1ChatRecordsCursor{}, err
	}
	var envelope v1ExternalRecordsCursorEnvelope
	if err = json.Unmarshal(decoded, &envelope); err != nil || len(envelope.Payload) == 0 || envelope.MAC == "" {
		return v1ChatRecordsCursor{}, errors.New("invalid chat records cursor")
	}
	provided, err := hex.DecodeString(envelope.MAC)
	if err != nil {
		return v1ChatRecordsCursor{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(envelope.Payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return v1ChatRecordsCursor{}, errors.New("invalid chat records cursor")
	}
	var cursor v1ChatRecordsCursor
	if err = json.Unmarshal(envelope.Payload, &cursor); err != nil {
		return v1ChatRecordsCursor{}, err
	}
	return cursor, nil
}
