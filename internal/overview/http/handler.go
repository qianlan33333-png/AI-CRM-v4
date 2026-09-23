// Package http exposes the admin-only operating overview. It performs Access
// authentication and role gating before any domain aggregate is called.
package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

const (
	overviewPath            = "/api/admin/overview"
	paidRecordsPath         = "/api/admin/overview/paid-records"
	maxPaidRecordsCursorLen = 2048
	paidRecordsCursorV1     = 1
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Reader interface {
	Read(context.Context, overviewapp.Query) (overviewapp.Response, error)
	ReadPaidRecords(context.Context, overviewapp.PaidRecordsQuery) (overviewapp.PaidRecordsResponse, error)
}

type Config struct {
	Reader   Reader
	Security RequestSecurity
	Now      func() time.Time
}

type Handler struct {
	reader   Reader
	security RequestSecurity
	now      func() time.Time
}

func NewHandler(config Config) (*Handler, error) {
	if config.Reader == nil || config.Security == nil {
		return nil, errors.New("overview HTTP dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Handler{reader: config.Reader, security: config.Security, now: config.Now}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.reader == nil || handler.security == nil || handler.now == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	switch request.URL.Path {
	case overviewPath:
		handler.serveOverview(writer, request)
	case paidRecordsPath:
		handler.servePaidRecords(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found")
	}
}

func (handler *Handler) serveOverview(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	query, ok := parseQuery(request, handler.now())
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	response, err := handler.reader.Read(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

type paidRecordsCursor struct {
	Version         int    `json:"v"`
	Period          string `json:"period"`
	Start           string `json:"start"`
	End             string `json:"end"`
	PaidConfirmedAt string `json:"paid_confirmed_at"`
	PaymentID       int64  `json:"payment_id"`
}

type paidRecordsItemResponse struct {
	Provider        string    `json:"provider"`
	OrderReference  string    `json:"order_reference"`
	PayerCustomerID *int64    `json:"payer_customer_id"`
	AmountMinor     int64     `json:"amount_minor"`
	Currency        string    `json:"currency"`
	PaidConfirmedAt time.Time `json:"paid_confirmed_at"`
}

type paidRecordsResponse struct {
	Range      overviewapp.Range         `json:"range"`
	Items      []paidRecordsItemResponse `json:"items"`
	NextCursor string                    `json:"next_cursor"`
}

func (handler *Handler) servePaidRecords(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	query, ok := parsePaidRecordsQuery(request, handler.now())
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := handler.reader.ReadPaidRecords(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	next, ok := encodePaidRecordsCursor(page.Range, page.NextCursor)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	items := make([]paidRecordsItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, paidRecordsItemResponse{Provider: item.Provider, OrderReference: item.OrderReference, PayerCustomerID: item.PayerCustomerID, AmountMinor: item.AmountMinor, Currency: item.Currency, PaidConfirmedAt: item.PaidConfirmedAt})
	}
	writeJSON(writer, http.StatusOK, paidRecordsResponse{Range: page.Range, Items: items, NextCursor: next})
}

func (handler *Handler) authorize(writer http.ResponseWriter, request *http.Request) (accessdomain.Principal, bool) {
	principal, err := handler.security.Authenticate(request.Context(), request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	// Access's current browser Principal contains an employee role but no
	// per-record data scope. These roles are therefore the existing global
	// admin-read scope used by Distribution's admin read model; no overview
	// handler may manufacture a narrower or broader predicate itself.
	if !canReadAdmin(principal) {
		writeError(writer, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func canReadAdmin(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func parseQuery(request *http.Request, now time.Time) (overviewapp.Query, bool) {
	values := request.URL.Query()
	for key, entries := range values {
		if (key != "period" && key != "from" && key != "to") || len(entries) != 1 {
			return overviewapp.Query{}, false
		}
	}
	period := values.Get("period")
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil || now.IsZero() {
		return overviewapp.Query{}, false
	}
	local := now.In(location)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	start, end := time.Time{}, time.Time{}
	switch period {
	case "today":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday, startOfToday.AddDate(0, 0, 1)
	case "7d":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday.AddDate(0, 0, -6), startOfToday.AddDate(0, 0, 1)
	case "30d":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday.AddDate(0, 0, -29), startOfToday.AddDate(0, 0, 1)
	case "custom":
		from, fromOK := parseDate(values.Get("from"), location)
		to, toOK := parseDate(values.Get("to"), location)
		if !fromOK || !toOK || to.Before(from) {
			return overviewapp.Query{}, false
		}
		start, end = from, to.AddDate(0, 0, 1)
	default:
		return overviewapp.Query{}, false
	}
	return overviewapp.Query{Range: overviewapp.Range{Period: period, Timezone: "Asia/Shanghai", Start: start.UTC(), End: end.UTC()}}, true
}

func parsePaidRecordsQuery(request *http.Request, now time.Time) (overviewapp.PaidRecordsQuery, bool) {
	values := request.URL.Query()
	if raw, hasCursor := values["cursor"]; hasCursor {
		if len(raw) != 1 || raw[0] == "" || len(values) != 1 {
			return overviewapp.PaidRecordsQuery{}, false
		}
		return decodePaidRecordsCursor(raw[0])
	}
	query, ok := parseQuery(request, now)
	if !ok {
		return overviewapp.PaidRecordsQuery{}, false
	}
	return overviewapp.PaidRecordsQuery{Range: query.Range}, true
}

func encodePaidRecordsCursor(reportingRange overviewapp.Range, cursor *paymentport.PaidOverviewRecordCursor) (string, bool) {
	if cursor == nil {
		return "", true
	}
	if !validReportingRange(reportingRange) || !validPaidRecordsCursor(reportingRange, *cursor) {
		return "", false
	}
	payload, err := json.Marshal(paidRecordsCursor{
		Version: paidRecordsCursorV1, Period: reportingRange.Period,
		Start: reportingRange.Start.UTC().Format(time.RFC3339Nano), End: reportingRange.End.UTC().Format(time.RFC3339Nano),
		PaidConfirmedAt: cursor.PaidConfirmedAt.UTC().Format(time.RFC3339Nano), PaymentID: cursor.PaymentID,
	})
	if err != nil || len(payload) > maxPaidRecordsCursorLen {
		return "", false
	}
	return base64.RawURLEncoding.EncodeToString(payload), true
}

func decodePaidRecordsCursor(raw string) (overviewapp.PaidRecordsQuery, bool) {
	if raw == "" || len(raw) > maxPaidRecordsCursorLen {
		return overviewapp.PaidRecordsQuery{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(payload) == 0 || len(payload) > maxPaidRecordsCursorLen {
		return overviewapp.PaidRecordsQuery{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var value paidRecordsCursor
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return overviewapp.PaidRecordsQuery{}, false
	}
	start, startErr := time.Parse(time.RFC3339Nano, value.Start)
	end, endErr := time.Parse(time.RFC3339Nano, value.End)
	paidAt, paidErr := time.Parse(time.RFC3339Nano, value.PaidConfirmedAt)
	reportingRange := overviewapp.Range{Period: value.Period, Timezone: "Asia/Shanghai", Start: start.UTC(), End: end.UTC()}
	cursor := paymentport.PaidOverviewRecordCursor{PaidConfirmedAt: paidAt.UTC(), PaymentID: value.PaymentID}
	if value.Version != paidRecordsCursorV1 || startErr != nil || endErr != nil || paidErr != nil || value.Start != start.UTC().Format(time.RFC3339Nano) || value.End != end.UTC().Format(time.RFC3339Nano) || value.PaidConfirmedAt != paidAt.UTC().Format(time.RFC3339Nano) || !validReportingRange(reportingRange) || !validPaidRecordsCursor(reportingRange, cursor) {
		return overviewapp.PaidRecordsQuery{}, false
	}
	return overviewapp.PaidRecordsQuery{Range: reportingRange, Cursor: &cursor}, true
}

func validReportingRange(value overviewapp.Range) bool {
	if value.Timezone != "Asia/Shanghai" || !value.Start.Before(value.End) {
		return false
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return false
	}
	start := value.Start.In(location)
	end := value.End.In(location)
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 || start.Nanosecond() != 0 || end.Hour() != 0 || end.Minute() != 0 || end.Second() != 0 || end.Nanosecond() != 0 {
		return false
	}
	switch value.Period {
	case "today":
		return end.Equal(start.AddDate(0, 0, 1))
	case "7d":
		return end.Equal(start.AddDate(0, 0, 7))
	case "30d":
		return end.Equal(start.AddDate(0, 0, 30))
	case "custom":
		return true
	default:
		return false
	}
}

func validPaidRecordsCursor(reportingRange overviewapp.Range, cursor paymentport.PaidOverviewRecordCursor) bool {
	return cursor.Valid() && !cursor.PaidConfirmedAt.Before(reportingRange.Start) && cursor.PaidConfirmedAt.Before(reportingRange.End)
}

func parseDate(value string, location *time.Location) (time.Time, bool) {
	if len(value) != len("2006-01-02") || strings.TrimSpace(value) != value {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	return parsed, err == nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}
