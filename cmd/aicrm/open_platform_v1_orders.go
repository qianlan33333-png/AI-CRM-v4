package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"strconv"
	"strings"
	"time"
)

type v1OrdersInput struct {
	Provider              string `json:"provider"`
	ProductCode           string `json:"product_code"`
	MerchantOrderNo       string `json:"merchant_order_no"`
	ProviderTransactionNo string `json:"provider_transaction_no"`
	SourceSystem          string `json:"source_system"`
	SourceRecordID        string `json:"source_record_id"`
	CustomerID            int64  `json:"customer_id"`
	CreatedFrom           *int64 `json:"created_from"`
	CreatedTo             *int64 `json:"created_to"`
	PaidFrom              *int64 `json:"paid_from"`
	PaidTo                *int64 `json:"paid_to"`
	IsPaid                *bool  `json:"is_paid"`
	IsRefunded            *bool  `json:"is_refunded"`
	Cursor                string `json:"cursor"`
	Limit                 int32  `json:"limit"`
}
type v1OrderGetInput struct {
	OrderID int64 `json:"order_id"`
}

type v1RefundKnownOrderReader interface {
	ExternalRefundKnownOrderIDs(context.Context, []int64) ([]int64, error)
}
type v1IdentityGetInput struct {
	CustomerID    int64    `json:"customer_id"`
	UnionIDScopes []string `json:"unionid_scopes"`
}
type v1OrderCursor struct {
	V         int       `json:"v"`
	Grant     string    `json:"grant"`
	Filters   string    `json:"filters"`
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}
type v1OrderCursorEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

func (executor *openPlatformExecutor) v1OrdersList(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor.v1OrderUOW == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "orders are unavailable")
	}
	var result openplatformport.Result
	err := executor.v1OrderUOW.Within(ctx, func(tx context.Context) error {
		var inner error
		result, inner = executor.v1OrdersListWithin(tx, principal, raw)
		return inner
	})
	return result, err
}
func (executor *openPlatformExecutor) v1OrdersListWithin(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	var in v1OrdersInput
	if err := decodeV1JSON(raw, &in); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid orders request")
	}
	q, err := v1ExternalOrderQuery(in)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid orders request")
	}
	allowed, err := executor.v1OrderScope(ctx, principal, in.CustomerID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "order scope is not granted")
	}
	q.CustomerIDs = allowed
	if in.IsRefunded != nil {
		ids, scopeErr := executor.v1Refunds.ExternalRefundedOrderIDs(ctx, allowed)
		if scopeErr != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "refunds unavailable")
		}
		q.IsRefunded, q.RefundedOrderIDs = in.IsRefunded, ids
		if !*in.IsRefunded {
			knownReader, ok := executor.v1Refunds.(v1RefundKnownOrderReader)
			if !ok {
				return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "refund filter unavailable")
			}
			known, knownErr := knownReader.ExternalRefundKnownOrderIDs(ctx, allowed)
			if knownErr != nil {
				return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "refund filter unavailable")
			}
			q.RefundKnownOrderIDs = known
		}
	}
	filter := v1OrderFilterDigest(in, allowed)
	grant := v1ActivityGrantDigest(principal)
	if in.Cursor != "" {
		c, e := decodeV1OrderCursor(executor.v1OrderCursorKey, in.Cursor)
		if e != nil || c.V != 1 || c.Grant != grant || c.Filters != filter {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "invalid orders cursor")
		}
		q.AfterCreatedAt, q.AfterID = c.CreatedAt, c.ID
	}
	requestedLimit := q.Limit
	q.Limit++
	page, err := executor.v1Orders.ListExternalRead(ctx, q)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "orders are unavailable")
	}
	hasMore := len(page.Items) > int(requestedLimit)
	if hasMore {
		page.Items = page.Items[:requestedLimit]
	}
	result, err := executor.v1OrderResult(ctx, page.Items)
	if err != nil {
		return openplatformport.Result{}, err
	}
	out := map[string]any{"items": result}
	if hasMore {
		last := page.Items[len(page.Items)-1]
		next, e := encodeV1OrderCursor(executor.v1OrderCursorKey, v1OrderCursor{V: 1, Grant: grant, Filters: filter, CreatedAt: last.CreatedAt.UTC(), ID: last.ID})
		if e != nil {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "orders cursor unavailable")
		}
		out["next_cursor"] = next
	}
	return openplatformport.Result{Data: out}, nil
}
func (executor *openPlatformExecutor) v1OrderGet(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	if executor.v1OrderUOW == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "orders are unavailable")
	}
	var result openplatformport.Result
	err := executor.v1OrderUOW.Within(ctx, func(tx context.Context) error {
		var inner error
		result, inner = executor.v1OrderGetWithin(tx, principal, raw)
		return inner
	})
	return result, err
}
func (executor *openPlatformExecutor) v1OrderGetWithin(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	var in v1OrderGetInput
	if err := decodeV1JSON(raw, &in); err != nil || in.OrderID < 1 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "order_id is required")
	}
	allowed, err := executor.v1OrderScope(ctx, principal, 0)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "order scope is not granted")
	}
	item, err := executor.v1Orders.GetExternalRead(ctx, in.OrderID, allowed)
	if errors.Is(err, orderport.ErrNotFound) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "order not found")
	}
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "orders are unavailable")
	}
	items, e := executor.v1OrderResult(ctx, []orderport.ExternalOrder{item})
	if e != nil {
		return openplatformport.Result{}, e
	}
	timeline, e := executor.v1OrderTimeline.ExternalOrderTimeline(ctx, item.ID)
	if e != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "order timeline unavailable")
	}
	items[0]["timeline"] = v1OrderTimeline(timeline)
	return openplatformport.Result{Data: items[0]}, nil
}
func (executor *openPlatformExecutor) v1IdentityGet(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	var in v1IdentityGetInput
	if err := decodeV1JSON(raw, &in); err != nil || in.CustomerID < 1 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "customer_id is required")
	}
	if err := executor.ensureCustomerScope(ctx, principal, customerdomain.CustomerID(in.CustomerID), nil); err != nil {
		// Identity export is an explicitly capability-gated, machine-only
		// endpoint. Report an insufficient customer grant as 403 so callers do
		// not mistake it for an absent identity record.
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "identity customer scope is not granted")
	}
	reader, ok := executor.identity.(interface {
		MachineIdentityExportForMachine(context.Context, customerdomain.CustomerID, accessdomain.MachinePrincipal) (identityport.MachineIdentityExport, error)
	})
	if !ok {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "identity projection unavailable")
	}
	requested := map[string]bool{}
	allowedScopes := map[string]bool{}
	for _, scope := range executor.scopes.UnionScopes {
		allowedScopes[scope] = true
	}
	for _, scope := range in.UnionIDScopes {
		if strings.TrimSpace(scope) != scope || scope == "" || !allowedScopes[scope] {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "unionid scope is not granted")
		}
		requested[scope] = true
	}
	export, err := reader.MachineIdentityExportForMachine(ctx, customerdomain.CustomerID(in.CustomerID), principal)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "identity projection unavailable")
	}
	// A request scoped to a historical root must not acquire the facts of its
	// canonical survivor merely because Identity followed a merge lineage.
	// Recheck the exact canonical output before rendering or recording success.
	if export.CanonicalCustomerID < 1 || executor.ensureCustomerScope(ctx, principal, export.CanonicalCustomerID, nil) != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "identity canonical customer scope is not granted")
	}
	if export.Status == identityport.MachineIdentityExportConflict {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorConflict, "customer identity is conflicted")
	}
	if export.Status == identityport.MachineIdentityExportMissing {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "customer identity is missing")
	}
	result := map[string]any{"customer_id": strconv.FormatInt(in.CustomerID, 10), "canonical_customer_id": strconv.FormatInt(int64(export.CanonicalCustomerID), 10), "status": export.Status, "identities": []any{}}
	for _, fact := range export.Facts {
		if fact.Kind == identitydomain.KindUnionID && !requested[fact.Scope] {
			continue
		}
		if fact.Kind != identitydomain.KindPhone && fact.Kind != identitydomain.KindUnionID {
			continue
		}
		result["identities"] = append(result["identities"].([]any), map[string]any{"kind": fact.Kind, "scope": fact.Scope, "value": fact.Value, "assurance": fact.Assurance, "source": fact.Source, "status": fact.Status})
	}
	return openplatformport.Result{Data: result}, nil
}
func (executor *openPlatformExecutor) v1OrderScope(_ context.Context, p accessdomain.MachinePrincipal, customerID int64) ([]int64, error) {
	if len(p.OwnerScope) == 0 {
		if customerID > 0 {
			return []int64{customerID}, nil
		}
		return nil, nil
	}
	for k := range p.OwnerScope {
		if k != "corp_id" && k != "customer_id" {
			return nil, errors.New("unsupported order scope")
		}
	}
	if v, ok := p.OwnerScope["corp_id"]; ok && (len(v) != 1 || v[0] != p.CorpID) {
		return nil, errors.New("corp")
	}
	ids := []int64{}
	for _, s := range p.OwnerScope["customer_id"] {
		id, e := strconv.ParseInt(s, 10, 64)
		if e != nil || id < 1 {
			return nil, errors.New("customer")
		}
		ids = append(ids, id)
	}
	if customerID > 0 {
		if len(ids) > 0 {
			found := false
			for _, id := range ids {
				found = found || id == customerID
			}
			if !found {
				return nil, errors.New("customer")
			}
			return []int64{customerID}, nil
		}
		return []int64{customerID}, nil
	}
	return ids, nil
}
func v1ExternalOrderQuery(in v1OrdersInput) (orderport.ExternalReadQuery, error) {
	if in.Limit == 0 {
		in.Limit = 100
	}
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 {
		return orderport.ExternalReadQuery{}, errors.New("limit")
	}
	if (strings.TrimSpace(in.SourceSystem) == "") != (strings.TrimSpace(in.SourceRecordID) == "") || len(in.SourceSystem) > 128 || len(in.SourceRecordID) > 200 || strings.TrimSpace(in.SourceSystem) != in.SourceSystem || strings.TrimSpace(in.SourceRecordID) != in.SourceRecordID {
		return orderport.ExternalReadQuery{}, errors.New("source")
	}
	q := orderport.ExternalReadQuery{Provider: orderdomain.Provider(in.Provider), ProductCode: strings.TrimSpace(in.ProductCode), MerchantOrderNo: strings.TrimSpace(in.MerchantOrderNo), ProviderTransactionNo: strings.TrimSpace(in.ProviderTransactionNo), SourceSystem: in.SourceSystem, SourceRecordID: in.SourceRecordID, IsPaid: in.IsPaid, IsRefunded: in.IsRefunded, Limit: in.Limit}
	if q.Provider != "" && q.Provider != orderdomain.ProviderWeChatPay && q.Provider != orderdomain.ProviderWeChatShop && q.Provider != orderdomain.ProviderAlipay {
		return q, errors.New("provider")
	}
	conv := func(x *int64) (*time.Time, error) {
		if x == nil {
			return nil, nil
		}
		if *x < 0 {
			return nil, errors.New("time")
		}
		v := time.Unix(*x, 0).UTC()
		return &v, nil
	}
	var e error
	if q.CreatedFrom, e = conv(in.CreatedFrom); e != nil {
		return q, e
	}
	if q.CreatedTo, e = conv(in.CreatedTo); e != nil {
		return q, e
	}
	if q.PaidFrom, e = conv(in.PaidFrom); e != nil {
		return q, e
	}
	if q.PaidTo, e = conv(in.PaidTo); e != nil {
		return q, e
	}
	if in.CustomerID < 0 || (q.CreatedFrom != nil && q.CreatedTo != nil && q.CreatedFrom.After(*q.CreatedTo)) || (q.PaidFrom != nil && q.PaidTo != nil && q.PaidFrom.After(*q.PaidTo)) {
		return q, errors.New("range")
	}
	return q, nil
}
func (executor *openPlatformExecutor) v1OrderResult(ctx context.Context, items []orderport.ExternalOrder) ([]map[string]any, error) {
	ids := make([]int64, len(items))
	for i, x := range items {
		ids[i] = x.ID
	}
	refunds, err := executor.v1Refunds.ExternalOrderRefundSummaries(ctx, ids)
	if err != nil {
		return nil, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "refunds unavailable")
	}
	refundDetails, err := executor.v1Refunds.ExternalOrderRefundDetails(ctx, ids)
	if err != nil {
		return nil, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "refund details unavailable")
	}
	out := make([]map[string]any, 0, len(items))
	for _, x := range items {
		var payerID, beneficiaryID any
		if x.PayerCustomerID != nil {
			payerID = strconv.FormatInt(*x.PayerCustomerID, 10)
		}
		if x.BeneficiaryCustomerID != nil {
			beneficiaryID = strconv.FormatInt(*x.BeneficiaryCustomerID, 10)
		}
		m := map[string]any{"order_id": strconv.FormatInt(x.ID, 10), "payer_customer_id": payerID, "beneficiary_customer_id": beneficiaryID, "customer_id": customerIDFor(x), "identity_status": identityStatusFor(x), "provider": x.Provider, "source_system": x.SourceSystem, "source_record_id": x.SourceKey, "merchant_order_no": x.MerchantOrderNo, "provider_transaction_no": x.ProviderTransactionNo, "product_codes": x.ProductCodes, "items": v1OrderItems(x.Items), "created_at": x.CreatedAt.UTC(), "paid_at": x.PaidAt, "paid_at_status": "unavailable", "status": x.Status, "amount_minor": x.Amount.AmountMinor, "amount_yuan": yuan(x.Amount.AmountMinor), "currency": x.Amount.Currency, "is_paid": x.IsPaid, "refund_records": v1RefundRecords(refundDetails[x.ID])}
		if x.PaidAt != nil {
			m["paid_at_status"] = "verified"
		}
		s, ok := refunds[x.ID]
		if !ok {
			m["refund_status"] = "unavailable"
			m["refund_amount_status"] = "unavailable"
		} else {
			m["is_refunded"] = s.CompletedMinor > 0
			m["has_refund_request"] = s.HasRefund
			m["refunded_minor"] = s.CompletedMinor
			m["refunded_yuan"] = yuan(s.CompletedMinor)
			m["refund_requested_minor"] = s.RequestedMinor
			m["refund_processing_minor"] = s.ProcessingMinor
			m["refund_outcome_unknown_minor"] = s.OutcomeUnknownMinor
			m["refund_final_failed_minor"] = s.FinalFailedMinor
			m["refund_status"] = "known"
			m["refund_amount_status"] = "known"
		}
		out = append(out, m)
	}
	return out, nil
}

func v1OrderItems(items []orderport.ExternalOrderItem) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"line_no": item.LineNo, "product_code": item.ProductCode, "product_name": item.ProductName, "unit_amount_minor": item.UnitAmountMinor, "quantity": item.Quantity, "line_amount_minor": item.LineAmountMinor})
	}
	return result
}

func v1RefundRecords(records []paymentport.ExternalOrderRefundDetail) []map[string]any {
	result := make([]map[string]any, 0, len(records))
	for _, record := range records {
		result = append(result, map[string]any{"refund_id": strconv.FormatInt(record.RefundID, 10), "status": record.Status, "amount_minor": record.AmountMinor, "created_at": record.CreatedAt.UTC(), "updated_at": record.UpdatedAt.UTC()})
	}
	return result
}

func v1OrderTimeline(events []orderport.ExternalOrderTimelineEvent) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		result = append(result, map[string]any{"status": event.Status, "refunded_minor": event.RefundedMinor, "occurred_at": event.OccurredAt.UTC()})
	}
	return result
}
func v1OrderFilterDigest(in v1OrdersInput, ids []int64) string {
	in.Cursor = ""
	in.Provider, in.ProductCode, in.MerchantOrderNo, in.ProviderTransactionNo, in.SourceSystem, in.SourceRecordID = strings.TrimSpace(in.Provider), strings.TrimSpace(in.ProductCode), strings.TrimSpace(in.MerchantOrderNo), strings.TrimSpace(in.ProviderTransactionNo), strings.TrimSpace(in.SourceSystem), strings.TrimSpace(in.SourceRecordID)
	if in.Limit == 0 {
		in.Limit = 100
	}
	b, _ := json.Marshal(struct {
		I   v1OrdersInput
		IDs []int64
	}{in, ids})
	h := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func yuan(minor int64) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}
func customerIDFor(x orderport.ExternalOrder) any {
	if x.PayerCustomerID != nil {
		return strconv.FormatInt(*x.PayerCustomerID, 10)
	}
	return nil
}
func identityStatusFor(x orderport.ExternalOrder) string {
	if x.PayerCustomerID == nil && x.BeneficiaryCustomerID == nil {
		return "unresolved"
	}
	return "linked"
}
func encodeV1OrderCursor(key []byte, c v1OrderCursor) (string, error) {
	p, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	m := hmac.New(sha256.New, key)
	m.Write(p)
	raw, e := json.Marshal(v1OrderCursorEnvelope{Payload: p, MAC: base64.RawURLEncoding.EncodeToString(m.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(raw), e
}
func decodeV1OrderCursor(key []byte, raw string) (v1OrderCursor, error) {
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return v1OrderCursor{}, e
	}
	var w v1OrderCursorEnvelope
	if e = json.Unmarshal(b, &w); e != nil {
		return v1OrderCursor{}, e
	}
	mac, e := base64.RawURLEncoding.DecodeString(w.MAC)
	if e != nil {
		return v1OrderCursor{}, e
	}
	m := hmac.New(sha256.New, key)
	m.Write(w.Payload)
	if !hmac.Equal(mac, m.Sum(nil)) {
		return v1OrderCursor{}, errors.New("mac")
	}
	var c v1OrderCursor
	e = json.Unmarshal(w.Payload, &c)
	return c, e
}
