package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

const (
	v1ExternalRecordsCursorV            = 1
	v1ExternalRecordsDefaultLimit int32 = 100
	v1ExternalRecordsMaximumLimit int32 = 100
	v1QuestionnaireOperation            = "questionnaire.submissions.list"
)

type v1QuestionnaireSubmissionsInput struct {
	CustomerID      int64  `json:"customer_id"`
	QuestionnaireID int64  `json:"questionnaire_id"`
	SourceSystem    string `json:"source_system"`
	SourceRecordID  string `json:"source_record_id"`
	SubmittedFrom   *int64 `json:"submitted_from"`
	SubmittedTo     *int64 `json:"submitted_to"`
	Limit           int32  `json:"limit"`
	Cursor          string `json:"cursor"`
}

// v1ExternalRecordsCursor freezes a submitted-at upper bound and moves only
// backward through the stable (submitted_at, submission_id) order. It never
// embeds historic identity values, result text, or a database offset.
type v1ExternalRecordsCursor struct {
	V                  int       `json:"v"`
	Operation          string    `json:"operation"`
	Grant              string    `json:"grant"`
	Filters            string    `json:"filters"`
	SubmittedTo        time.Time `json:"submitted_to"`
	BeforeSubmittedAt  time.Time `json:"before_submitted_at"`
	BeforeSubmissionID int64     `json:"before_submission_id"`
}

type v1ExternalRecordsCursorEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

// v1QuestionnaireSubmissions exposes Survey's existing Owner projection only
// after the caller has supplied a canonical customer ID. Identity selectors
// remain a separate customer.resolve step: this route never interprets a phone
// number or UnionID, creates a Customer, or binds an identity.
func (executor *openPlatformExecutor) v1QuestionnaireSubmissions(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.survey == nil || executor.surveyAliases == nil || len(executor.v1ExternalCursorKey) < 16 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "questionnaire submissions are unavailable")
	}
	var in v1QuestionnaireSubmissionsInput
	if err := decodeV1JSON(raw, &in); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid questionnaire submissions request")
	}
	query, err := v1QuestionnaireSubmissionQuery(in)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid questionnaire submissions request")
	}
	customerID := customerdomain.CustomerID(in.CustomerID)
	unionIDs, unionReferences, err := executor.surveyHistoricalUnionIDs(ctx, customerID, nil)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "questionnaire identity projection is unavailable")
	}
	if err = executor.ensureCustomerScope(ctx, principal, customerID, unionReferences); err != nil {
		return openplatformport.Result{}, v1CustomerScopeError(err)
	}
	query.CustomerID, query.HistoricalUnionIDs = int64(customerID), unionIDs
	grant := v1ActivityGrantDigest(principal)
	filters := v1QuestionnaireFilterDigest(in, unionIDs)

	if in.Cursor != "" {
		cursor, decodeErr := decodeV1ExternalRecordsCursor(executor.v1ExternalCursorKey, in.Cursor)
		if decodeErr != nil || cursor.V != v1ExternalRecordsCursorV || cursor.Operation != v1QuestionnaireOperation || cursor.Grant != grant || cursor.Filters != filters || cursor.SubmittedTo.IsZero() || cursor.BeforeSubmittedAt.IsZero() || cursor.BeforeSubmissionID < 1 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid questionnaire submissions cursor")
		}
		// A caller cannot move a signed page to a different temporal window.
		if !query.SubmittedTo.IsZero() && !query.SubmittedTo.Equal(cursor.SubmittedTo) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid questionnaire submissions cursor")
		}
		query.SubmittedTo = cursor.SubmittedTo.UTC()
		query.BeforeSubmittedAt = cursor.BeforeSubmittedAt.UTC()
		query.BeforeSubmissionID = surveyport.ID(cursor.BeforeSubmissionID)
	} else if query.SubmittedTo.IsZero() {
		now := time.Now().UTC()
		if executor.activityNow != nil {
			now = executor.activityNow().UTC()
		}
		query.SubmittedTo = now
	}

	requestedLimit := query.Limit
	// Ask Survey for one additional item. This avoids total/offset arithmetic
	// and makes a signed keyset page stop exactly when it is exhausted.
	query.Limit = requestedLimit + 1
	page, err := executor.survey.ExternalSubmissions(ctx, query)
	if err != nil {
		switch {
		case errors.Is(err, surveyport.ErrConflict):
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorIdentityConflict, "questionnaire source mapping conflicts")
		case errors.Is(err, surveyport.ErrInvalid):
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid questionnaire submissions request")
		default:
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "questionnaire submissions are unavailable")
		}
	}
	if len(page.Items) == 0 && page.Total > 0 {
		// The Owner count and page must advance under the same stable keyset.
		// Do not mint a cursor that can loop forever if a dependency violates it.
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "questionnaire submissions page is unavailable")
	}
	hasMore := len(page.Items) > int(requestedLimit)
	if hasMore {
		page.Items = page.Items[:requestedLimit]
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		answers := make([]map[string]any, 0, len(item.Answers))
		for _, answer := range item.Answers {
			answers = append(answers, map[string]any{
				"question_title_snapshot":        answer.QuestionTitle,
				"selected_option_texts_snapshot": answer.SelectedOptionTexts,
				"text_value":                     answer.TextValue,
				"score_contribution":             answer.ScoreContribution,
			})
		}
		items = append(items, map[string]any{
			"submission_id":              strconv.FormatInt(int64(item.SubmissionID), 10),
			"questionnaire_id":           strconv.FormatInt(item.QuestionnaireSourceID, 10),
			"definition_version":         item.DefinitionVersion,
			"questionnaire_title":        item.QuestionnaireTitle,
			"submitted_at":               item.SubmittedAt.UTC(),
			"answers":                    answers,
			"final_tags":                 item.FinalTags,
			"assessment_result_snapshot": item.AssessmentResult,
			"source_system":              item.SourceSystem,
			"source_record_id":           item.SourceRecordID,
			"customer_id":                strconv.FormatInt(int64(customerID), 10),
			"identity_status":            "resolved",
		})
	}
	result := map[string]any{
		"customer_id": strconv.FormatInt(int64(customerID), 10),
		"items":       items,
	}
	if hasMore {
		last := page.Items[len(page.Items)-1]
		next, encodeErr := encodeV1ExternalRecordsCursor(executor.v1ExternalCursorKey, v1ExternalRecordsCursor{
			V:                  v1ExternalRecordsCursorV,
			Operation:          v1QuestionnaireOperation,
			Grant:              grant,
			Filters:            filters,
			SubmittedTo:        query.SubmittedTo.UTC(),
			BeforeSubmittedAt:  last.SubmittedAt.UTC(),
			BeforeSubmissionID: int64(last.SubmissionID),
		})
		if encodeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "questionnaire cursor is unavailable")
		}
		result["next_cursor"] = next
	}
	return openplatformport.Result{Data: result}, nil
}

func v1QuestionnaireSubmissionQuery(in v1QuestionnaireSubmissionsInput) (surveyport.ExternalSubmissionQuery, error) {
	if in.CustomerID < 1 || in.QuestionnaireID < 0 || in.Limit < 0 || in.Limit > v1ExternalRecordsMaximumLimit || len(in.Cursor) > 4096 ||
		(in.SourceSystem == "") != (in.SourceRecordID == "") || len(in.SourceSystem) > 200 || len(in.SourceRecordID) > 500 {
		return surveyport.ExternalSubmissionQuery{}, errors.New("invalid request")
	}
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	query := surveyport.ExternalSubmissionQuery{
		QuestionnaireSourceID: in.QuestionnaireID,
		SourceSystem:          in.SourceSystem,
		SourceRecordID:        in.SourceRecordID,
		SubmittedEndExclusive: true,
		Limit:                 in.Limit,
	}
	convertTime := func(value *int64) (time.Time, error) {
		if value == nil {
			return time.Time{}, nil
		}
		if *value < 0 {
			return time.Time{}, errors.New("invalid time")
		}
		return time.Unix(*value, 0).UTC(), nil
	}
	var err error
	if query.SubmittedFrom, err = convertTime(in.SubmittedFrom); err != nil {
		return surveyport.ExternalSubmissionQuery{}, err
	}
	if query.SubmittedTo, err = convertTime(in.SubmittedTo); err != nil {
		return surveyport.ExternalSubmissionQuery{}, err
	}
	if !query.SubmittedFrom.IsZero() && !query.SubmittedTo.IsZero() && !query.SubmittedFrom.Before(query.SubmittedTo) {
		return surveyport.ExternalSubmissionQuery{}, errors.New("invalid time range")
	}
	return query, nil
}

func v1QuestionnaireFilterDigest(in v1QuestionnaireSubmissionsInput, unionIDs []string) string {
	in.Cursor = ""
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	aliases := append([]string(nil), unionIDs...)
	sort.Strings(aliases)
	payload, _ := json.Marshal(struct {
		Input   v1QuestionnaireSubmissionsInput `json:"input"`
		UnionID []string                        `json:"historical_union_ids"`
	}{Input: in, UnionID: aliases})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func encodeV1ExternalRecordsCursor(key []byte, cursor v1ExternalRecordsCursor) (string, error) {
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

func decodeV1ExternalRecordsCursor(key []byte, raw string) (v1ExternalRecordsCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return v1ExternalRecordsCursor{}, err
	}
	var envelope v1ExternalRecordsCursorEnvelope
	if err = json.Unmarshal(decoded, &envelope); err != nil || len(envelope.Payload) == 0 || envelope.MAC == "" {
		return v1ExternalRecordsCursor{}, errors.New("invalid cursor")
	}
	provided, err := hex.DecodeString(envelope.MAC)
	if err != nil {
		return v1ExternalRecordsCursor{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(envelope.Payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return v1ExternalRecordsCursor{}, errors.New("cursor signature")
	}
	var cursor v1ExternalRecordsCursor
	if err = json.Unmarshal(envelope.Payload, &cursor); err != nil {
		return v1ExternalRecordsCursor{}, err
	}
	return cursor, nil
}

type v1RadarClicksInput struct {
	CustomerID  int64  `json:"customer_id"`
	RadarID     int64  `json:"radar_id"`
	RadarCode   string `json:"radar_code"`
	SessionID   int64  `json:"session_id"`
	ClickedFrom *int64 `json:"clicked_from"`
	ClickedTo   *int64 `json:"clicked_to"`
	Limit       int32  `json:"limit"`
	Cursor      string `json:"cursor"`
}

type v1RadarLinksInput struct {
	RadarID   int64  `json:"radar_id"`
	RadarCode string `json:"radar_code"`
	Limit     int32  `json:"limit"`
	Cursor    string `json:"cursor"`
}

// v1RadarCursor is deliberately separate from questionnaire cursors: Radar
// pages use an opening event position, while links use their immutable link ID.
// It binds the effective grant and complete request filter before any Owner
// query runs.
type v1RadarCursor struct {
	V         int       `json:"v"`
	Operation string    `json:"operation"`
	Grant     string    `json:"grant"`
	Filters   string    `json:"filters"`
	OpenedTo  time.Time `json:"opened_to,omitempty"`
	OpenedAt  time.Time `json:"opened_at,omitempty"`
	EventID   int64     `json:"event_id,omitempty"`
	BeforeID  int64     `json:"before_id,omitempty"`
}

type v1RadarCursorEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

const (
	v1RadarClicksOperation = "radar.clicks.list"
	v1RadarLinksOperation  = "radar.links.list"
)

func (executor *openPlatformExecutor) v1RadarClicks(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.radarClicks == nil || len(executor.v1ExternalCursorKey) < 16 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar clicks are unavailable")
	}
	var in v1RadarClicksInput
	if err := decodeV1JSON(raw, &in); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar clicks request")
	}
	query, err := v1RadarClicksQuery(in)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar clicks request")
	}
	if in.CustomerID > 0 {
		customerID := customerdomain.CustomerID(in.CustomerID)
		if scopeErr := executor.ensureCustomerScope(ctx, principal, customerID, nil); scopeErr != nil {
			return openplatformport.Result{}, v1CustomerScopeError(scopeErr)
		}
		query.FilterApplied = true
		query.CustomerIDs = []customerdomain.CustomerID{customerID}
	} else if !executor.allowsUnboundScope(principal) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "radar click scope is not granted")
	}
	grant, filters := v1ActivityGrantDigest(principal), v1RadarClicksFilterDigest(in)
	if in.Cursor != "" {
		cursor, decodeErr := decodeV1RadarCursor(executor.v1ExternalCursorKey, in.Cursor)
		if decodeErr != nil || cursor.V != v1ExternalRecordsCursorV || cursor.Operation != v1RadarClicksOperation || cursor.Grant != grant || cursor.Filters != filters || cursor.OpenedTo.IsZero() || cursor.OpenedAt.IsZero() || cursor.EventID < 1 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar clicks cursor")
		}
		if query.End != nil && !query.End.Equal(cursor.OpenedTo) {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar clicks cursor")
		}
		end, before := cursor.OpenedTo.UTC(), cursor.OpenedAt.UTC()
		query.End, query.BeforeOpened, query.BeforeEventID = &end, before, cursor.EventID
	} else if query.End == nil {
		now := time.Now().UTC()
		if executor.activityNow != nil {
			now = executor.activityNow().UTC()
		}
		query.End = &now
	}
	page, err := executor.radarClicks.ExternalClicks(ctx, query)
	if err != nil {
		return openplatformport.Result{}, v1RadarReadError(err, "radar clicks")
	}
	if len(page.Items) == 0 && page.HasMore {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar clicks page is unavailable")
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		identityStatus, customerValue, valid := v1RadarClickIdentity(item)
		if !valid {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar click attribution is unavailable")
		}
		items = append(items, map[string]any{
			"click_id":           strconv.FormatInt(item.EventID, 10),
			"event_id":           strconv.FormatInt(item.EventID, 10),
			"session_id":         strconv.FormatInt(item.SessionID, 10),
			"radar_id":           strconv.FormatInt(int64(item.RadarID), 10),
			"radar_code":         item.RadarCode,
			"clicked_at":         item.OpenedAt.UTC(),
			"open_stage":         string(item.OpenStage),
			"source_system":      "aicrm_v3",
			"source_record_id":   strconv.FormatInt(item.SessionID, 10),
			"customer_id":        customerValue,
			"identity_status":    identityStatus,
			"attribution_status": string(item.AttributionStatus),
		})
	}
	result := map[string]any{"items": items}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		next, encodeErr := encodeV1RadarCursor(executor.v1ExternalCursorKey, v1RadarCursor{
			V:         v1ExternalRecordsCursorV,
			Operation: v1RadarClicksOperation,
			Grant:     grant,
			Filters:   filters,
			OpenedTo:  query.End.UTC(),
			OpenedAt:  last.OpenedAt.UTC(),
			EventID:   last.EventID,
		})
		if encodeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar clicks cursor is unavailable")
		}
		result["next_cursor"] = next
	}
	return openplatformport.Result{Data: result}, nil
}

func (executor *openPlatformExecutor) v1RadarLinks(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor == nil || executor.radarLinks == nil || len(executor.v1ExternalCursorKey) < 16 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar links are unavailable")
	}
	if !executor.allowsUnboundScope(principal) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "radar link scope is not granted")
	}
	var in v1RadarLinksInput
	if err := decodeV1JSON(raw, &in); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar links request")
	}
	query, err := v1RadarLinksQuery(in)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar links request")
	}
	grant, filters := v1ActivityGrantDigest(principal), v1RadarLinksFilterDigest(in)
	if in.Cursor != "" {
		cursor, decodeErr := decodeV1RadarCursor(executor.v1ExternalCursorKey, in.Cursor)
		if decodeErr != nil || cursor.V != v1ExternalRecordsCursorV || cursor.Operation != v1RadarLinksOperation || cursor.Grant != grant || cursor.Filters != filters || cursor.BeforeID < 1 {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid radar links cursor")
		}
		query.BeforeRadarID = radarport.RadarID(cursor.BeforeID)
	}
	page, err := executor.radarLinks.ExternalLinkMappings(ctx, query)
	if err != nil {
		return openplatformport.Result{}, v1RadarReadError(err, "radar links")
	}
	if len(page.Items) == 0 && page.HasMore {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar links page is unavailable")
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, map[string]any{
			"link_id":          strconv.FormatInt(int64(item.RadarID), 10),
			"radar_id":         strconv.FormatInt(int64(item.RadarID), 10),
			"radar_code":       item.RadarCode,
			"title":            item.Title,
			"status":           string(item.Status),
			"is_enabled":       item.Status == radarport.StatusEnabled,
			"source_system":    "aicrm_v3",
			"source_record_id": strconv.FormatInt(int64(item.RadarID), 10),
		})
	}
	result := map[string]any{"items": items}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		next, encodeErr := encodeV1RadarCursor(executor.v1ExternalCursorKey, v1RadarCursor{V: v1ExternalRecordsCursorV, Operation: v1RadarLinksOperation, Grant: grant, Filters: filters, BeforeID: int64(last.RadarID)})
		if encodeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "radar links cursor is unavailable")
		}
		result["next_cursor"] = next
	}
	return openplatformport.Result{Data: result}, nil
}

func v1RadarClicksQuery(in v1RadarClicksInput) (radarport.ExternalClickQuery, error) {
	if in.CustomerID < 0 || in.RadarID < 0 || in.SessionID < 0 || in.Limit < 0 || in.Limit > v1ExternalRecordsMaximumLimit || len(in.Cursor) > 4096 || strings.TrimSpace(in.RadarCode) != in.RadarCode || len(in.RadarCode) > 128 {
		return radarport.ExternalClickQuery{}, errors.New("invalid radar click request")
	}
	if in.RadarCode != "" && !radar.PublicCode(in.RadarCode).Valid() {
		return radarport.ExternalClickQuery{}, errors.New("invalid radar code")
	}
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	from, err := v1ExternalRecordsUnixTime(in.ClickedFrom)
	if err != nil {
		return radarport.ExternalClickQuery{}, err
	}
	to, err := v1ExternalRecordsUnixTime(in.ClickedTo)
	if err != nil || (!from.IsZero() && !to.IsZero() && !from.Before(to)) {
		return radarport.ExternalClickQuery{}, errors.New("invalid radar time range")
	}
	query := radarport.ExternalClickQuery{RadarID: radarport.RadarID(in.RadarID), RadarCode: in.RadarCode, SessionID: in.SessionID, Limit: in.Limit}
	if !from.IsZero() {
		query.Start = &from
	}
	if !to.IsZero() {
		query.End = &to
	}
	return query, nil
}

func v1RadarLinksQuery(in v1RadarLinksInput) (radarport.ExternalLinkMappingQuery, error) {
	if in.RadarID < 0 || in.Limit < 0 || in.Limit > v1ExternalRecordsMaximumLimit || len(in.Cursor) > 4096 || strings.TrimSpace(in.RadarCode) != in.RadarCode || len(in.RadarCode) > 128 {
		return radarport.ExternalLinkMappingQuery{}, errors.New("invalid radar links request")
	}
	if in.RadarCode != "" && !radar.PublicCode(in.RadarCode).Valid() {
		return radarport.ExternalLinkMappingQuery{}, errors.New("invalid radar code")
	}
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	return radarport.ExternalLinkMappingQuery{RadarID: radarport.RadarID(in.RadarID), RadarCode: in.RadarCode, Limit: in.Limit}, nil
}

func v1RadarClickIdentity(item radarport.ExternalClick) (string, any, bool) {
	switch item.AttributionStatus {
	case radarport.AttributionResolved:
		if item.CustomerID == nil || *item.CustomerID < 1 {
			return "", nil, false
		}
		return "resolved", strconv.FormatInt(int64(*item.CustomerID), 10), true
	case radarport.AttributionPending:
		if item.CustomerID != nil {
			return "", nil, false
		}
		return "pending", nil, true
	case radarport.AttributionConflict:
		if item.CustomerID != nil {
			return "", nil, false
		}
		return "conflict", nil, true
	default:
		return "", nil, false
	}
}

func v1RadarClicksFilterDigest(in v1RadarClicksInput) string {
	in.Cursor = ""
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	return v1ExternalRecordsDigest(in)
}

func v1RadarLinksFilterDigest(in v1RadarLinksInput) string {
	in.Cursor = ""
	if in.Limit == 0 {
		in.Limit = v1ExternalRecordsDefaultLimit
	}
	return v1ExternalRecordsDigest(in)
}

func v1ExternalRecordsDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func v1ExternalRecordsUnixTime(value *int64) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if *value < 0 {
		return time.Time{}, errors.New("invalid time")
	}
	return time.Unix(*value, 0).UTC(), nil
}

func v1RadarReadError(err error, resource string) error {
	switch {
	case errors.Is(err, radarport.ErrNotFound):
		return openplatformport.NewError(openplatformport.ErrorNotFound, resource+" not found")
	case errors.Is(err, radarport.ErrConflict):
		return openplatformport.NewError(openplatformport.ErrorConflict, resource+" conflict")
	case errors.Is(err, radar.ErrInvalidArgument):
		return openplatformport.NewError(openplatformport.ErrorValidation, "invalid "+resource+" request")
	default:
		return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, resource+" are unavailable")
	}
}

func encodeV1RadarCursor(key []byte, cursor v1RadarCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	envelope, err := json.Marshal(v1RadarCursorEnvelope{Payload: payload, MAC: hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

func decodeV1RadarCursor(key []byte, raw string) (v1RadarCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return v1RadarCursor{}, err
	}
	var envelope v1RadarCursorEnvelope
	if err = json.Unmarshal(decoded, &envelope); err != nil || len(envelope.Payload) == 0 || envelope.MAC == "" {
		return v1RadarCursor{}, errors.New("invalid cursor")
	}
	provided, err := hex.DecodeString(envelope.MAC)
	if err != nil {
		return v1RadarCursor{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(envelope.Payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return v1RadarCursor{}, errors.New("invalid cursor")
	}
	var cursor v1RadarCursor
	if err = json.Unmarshal(envelope.Payload, &cursor); err != nil {
		return v1RadarCursor{}, err
	}
	return cursor, nil
}
