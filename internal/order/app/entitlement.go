package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type EntitlementStore interface {
	ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error)
	ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error)
	GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error)
	FindEntitlementReceipt(context.Context, [32]byte) (orderport.Entitlement, [32]byte, string, bool, error)
	UpdateEntitlementRemark(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, time.Time) (orderport.Entitlement, error)
	RecordEntitlementConflict(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, orderport.Entitlement, time.Time) error
	UpdateEntitlementAlliance(context.Context, orderport.AllianceCommand, [32]byte, [32]byte, time.Time) (orderport.Entitlement, error)
	RecordEntitlementAllianceConflict(context.Context, orderport.AllianceCommand, [32]byte, [32]byte, orderport.Entitlement, time.Time) error
	ImportHistoricalEntitlement(context.Context, orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error)
}

// EntitlementFulfillmentStore keeps payment-derived grant/refund facts within
// Order's existing entitlement aggregate. Every method requires the caller's
// PostgreSQL transaction; it does not query Payment or Product tables.
type EntitlementFulfillmentStore interface {
	GrantPaidServicePeriod(context.Context, orderport.ServicePeriodGrantCommand, [32]byte) (orderport.Entitlement, error)
	ApplyServicePeriodRefund(context.Context, orderport.ServicePeriodRefundCommand, [32]byte) (orderport.Entitlement, error)
	RecordHistoricalServicePeriodSource(context.Context, orderport.HistoricalServicePeriodSourceCommand) error
}

type EntitlementApplication struct {
	uow   platformport.UnitOfWork
	store EntitlementStore
	now   func() time.Time
}

func NewEntitlementApplication(uow platformport.UnitOfWork, store EntitlementStore) (*EntitlementApplication, error) {
	if uow == nil || store == nil {
		return nil, errors.New("order entitlement dependencies are required")
	}
	return &EntitlementApplication{uow: uow, store: store, now: time.Now}, nil
}

func (s *EntitlementApplication) ListCustomerEntitlements(ctx context.Context, customerID int64, limit int32) (orderport.EntitlementPage, error) {
	if customerID < 1 || limit < 1 || limit > 100 {
		return orderport.EntitlementPage{}, orderport.ErrConflict
	}
	var page orderport.EntitlementPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		page, err = s.store.ListCustomerEntitlements(txctx, customerID, limit)
		return err
	})
	return page, err
}

func (s *EntitlementApplication) ListServicePeriodMembers(ctx context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	if s == nil || query.ServiceProductID < 1 || query.Limit < 1 || query.Limit > 200 || (query.State != "" && query.State != "all" && query.State != "active" && query.State != "expired" && query.State != "removed") || (query.Source != "" && query.Source != "paid_order" && query.Source != "manual") || (query.Sort != "" && query.Sort != "updated_at_desc" && query.Sort != "starts_at_desc" && query.Sort != "remaining_days_desc" && query.Sort != "remaining_days_asc") || (query.FilterLogic != "" && query.FilterLogic != "and" && query.FilterLogic != "or") || !validMemberGridFilters(query) || len(query.Cursor) > 4096 {
		return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
	}
	var page orderport.ServicePeriodMemberPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		page, readErr = s.store.ListServicePeriodMembers(txctx, query)
		return readErr
	})
	return page, err
}

func validMemberGridFilters(query orderport.ServicePeriodMemberQuery) bool {
	if len(query.GridFilters) > 20 || len(query.GridSorts) > 8 || len(query.GridGroups) > 2 {
		return false
	}
	sortFields, groupFields := map[string]bool{}, map[string]bool{}
	for _, item := range query.GridSorts {
		if !validMemberGridOrder(item) || sortFields[item.Field] {
			return false
		}
		sortFields[item.Field] = true
	}
	for _, item := range query.GridGroups {
		if !validMemberGridOrder(item) || groupFields[item.Field] || sortFields[item.Field] {
			return false
		}
		groupFields[item.Field] = true
	}
	for _, filter := range query.GridFilters {
		if !validMemberGridFilter(filter) {
			return false
		}
	}
	if query.RemainingDays != nil {
		f := query.RemainingDays
		if (f.Operator != "equals" && f.Operator != "not_equals" && f.Operator != "gt" && f.Operator != "gte" && f.Operator != "lt" && f.Operator != "lte" && f.Operator != "between") || len(f.Values) == 0 || len(f.Values) > 2 || (f.Operator == "between" && len(f.Values) != 2) || (f.Operator != "between" && len(f.Values) != 1) {
			return false
		}
	}
	if query.Remark != nil {
		f := query.Remark
		if (f.Operator != "contains" && f.Operator != "not_contains" && f.Operator != "equals" && f.Operator != "not_equals" && f.Operator != "is_empty" && f.Operator != "is_not_empty") || len(f.Value) > 200 {
			return false
		}
	}
	return true
}

func validMemberGridOrder(item orderport.MemberGridOrder) bool {
	return (item.Field == "remaining_days" || item.Field == "renewal_count" || item.Field == "remark" || item.Field == "alliance") && (item.Direction == "asc" || item.Direction == "desc")
}

func validMemberGridFilter(filter orderport.MemberGridFilter) bool {
	switch filter.Field {
	case "remaining_days", "renewal_count":
		if (filter.Operator != "equals" && filter.Operator != "not_equals" && filter.Operator != "gt" && filter.Operator != "gte" && filter.Operator != "lt" && filter.Operator != "lte" && filter.Operator != "between" && filter.Operator != "is_empty" && filter.Operator != "is_not_empty") || len(filter.Numbers) > 2 {
			return false
		}
		if filter.Operator == "is_empty" || filter.Operator == "is_not_empty" {
			return len(filter.Numbers) == 0
		}
		if !((filter.Operator == "between" && len(filter.Numbers) == 2) || (filter.Operator != "between" && len(filter.Numbers) == 1)) {
			return false
		}
		for _, value := range filter.Numbers {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		}
		return true
	case "remark", "alliance":
		if (filter.Operator != "contains" && filter.Operator != "not_contains" && filter.Operator != "equals" && filter.Operator != "not_equals" && filter.Operator != "is_empty" && filter.Operator != "is_not_empty") || len(filter.Text) > 200 {
			return false
		}
		return true
	default:
		return false
	}
}

func (s *EntitlementApplication) GetCustomerServicePeriodEntitlement(ctx context.Context, customerID, serviceProductID int64) (orderport.Entitlement, bool, error) {
	if s == nil || customerID < 1 || serviceProductID < 1 {
		return orderport.Entitlement{}, false, orderport.ErrConflict
	}
	var item orderport.Entitlement
	var found bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		item, found, readErr = s.store.GetCustomerServicePeriodEntitlement(txctx, customerID, serviceProductID)
		return readErr
	})
	return item, found, err
}

func (s *EntitlementApplication) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	command.Remark = strings.TrimSpace(command.Remark)
	if command.EntitlementID < 1 || command.CustomerID < 0 || command.ServiceProductID < 1 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 || len(command.Remark) > 500 || command.ExpectedVersion < 1 || len(command.IdempotencyKey) < 8 || len(command.IdempotencyKey) > 200 {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal([]any{command.EntitlementID, command.CustomerID, command.ServiceProductID, command.EmployeeID, command.Remark, command.ExpectedVersion})
	keyDigest, payloadDigest := sha256.Sum256([]byte(command.IdempotencyKey)), sha256.Sum256(payload)
	var result orderport.Entitlement
	conflicted := false
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		prior, priorPayload, outcome, found, err := s.store.FindEntitlementReceipt(txctx, keyDigest)
		if err != nil {
			return err
		}
		if found {
			if priorPayload != payloadDigest {
				return orderport.ErrConflict
			}
			result = prior
			conflicted = outcome == "version_conflict"
			return nil
		}
		result, err = s.store.UpdateEntitlementRemark(txctx, command, keyDigest, payloadDigest, s.now().UTC())
		if errors.Is(err, orderport.ErrConflict) {
			// The frozen grid sends an opaque member reference and relies on the
			// required Product scope. Without a customer ID we must not perform
			// a broad customer lookup merely to manufacture a conflict snapshot.
			if command.CustomerID == 0 {
				return orderport.ErrConflict
			}
			page, readErr := s.store.ListCustomerEntitlements(txctx, command.CustomerID, 100)
			if readErr != nil {
				return readErr
			}
			for _, item := range page.Items {
				if item.ID == command.EntitlementID && (command.ServiceProductID == 0 || item.ServiceProductID == command.ServiceProductID) {
					result = item
					break
				}
			}
			if result.ID == 0 {
				return orderport.ErrNotFound
			}
			if recErr := s.store.RecordEntitlementConflict(txctx, command, keyDigest, payloadDigest, result, s.now().UTC()); recErr != nil {
				return recErr
			}
			conflicted = true
			return nil
		}
		return err
	})
	if err == nil && conflicted {
		err = orderport.ErrConflict
	}
	return result, err
}

func (s *EntitlementApplication) UpdateEntitlementAlliance(ctx context.Context, command orderport.AllianceCommand) (orderport.Entitlement, error) {
	command.Alliance = strings.TrimSpace(command.Alliance)
	if command.EntitlementID < 1 || command.CustomerID < 0 || command.ServiceProductID < 1 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 || utf8.RuneCountInString(command.Alliance) > 500 || command.ExpectedVersion < 1 || len(command.IdempotencyKey) < 8 || len(command.IdempotencyKey) > 200 {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal([]any{"alliance", command.EntitlementID, command.CustomerID, command.ServiceProductID, command.EmployeeID, command.Alliance, command.ExpectedVersion})
	keyDigest, payloadDigest := sha256.Sum256([]byte(command.IdempotencyKey)), sha256.Sum256(payload)
	var result orderport.Entitlement
	conflicted := false
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		prior, priorPayload, outcome, found, err := s.store.FindEntitlementReceipt(txctx, keyDigest)
		if err != nil {
			return err
		}
		if found {
			if priorPayload != payloadDigest {
				return orderport.ErrConflict
			}
			result = prior
			conflicted = outcome == "version_conflict"
			return nil
		}
		result, err = s.store.UpdateEntitlementAlliance(txctx, command, keyDigest, payloadDigest, s.now().UTC())
		if errors.Is(err, orderport.ErrConflict) {
			// An opaque product-scoped editor cannot ask Order to enumerate an
			// unrelated customer merely to manufacture a conflict snapshot.
			if command.CustomerID == 0 {
				return orderport.ErrConflict
			}
			page, readErr := s.store.ListCustomerEntitlements(txctx, command.CustomerID, 100)
			if readErr != nil {
				return readErr
			}
			for _, item := range page.Items {
				if item.ID == command.EntitlementID && item.ServiceProductID == command.ServiceProductID {
					result = item
					break
				}
			}
			if result.ID == 0 {
				return orderport.ErrNotFound
			}
			if recErr := s.store.RecordEntitlementAllianceConflict(txctx, command, keyDigest, payloadDigest, result, s.now().UTC()); recErr != nil {
				return recErr
			}
			conflicted = true
			return nil
		}
		return err
	})
	if err == nil && conflicted {
		err = orderport.ErrConflict
	}
	return result, err
}

func (s *EntitlementApplication) ImportHistoricalEntitlement(ctx context.Context, input orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error) {
	if input.SourceSystem == "" || input.SourceKey == "" || input.CustomerID < 1 || input.ServiceProductID < 1 || input.ProductName == "" || input.SourceDigest == ([32]byte{}) || input.StartAt.IsZero() || input.EndAt.Before(input.StartAt) || (input.Alliance != nil && utf8.RuneCountInString(*input.Alliance) > 500) {
		return orderport.Entitlement{}, false, orderport.ErrConflict
	}
	var result orderport.Entitlement
	var created bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		result, created, err = s.store.ImportHistoricalEntitlement(txctx, input)
		return err
	})
	return result, created, err
}

var _ orderport.EntitlementService = (*EntitlementApplication)(nil)
var _ orderport.HistoricalEntitlementImporter = (*EntitlementApplication)(nil)

// EntitlementFulfillmentApplication is intentionally separate from the
// sidebar/read-and-remark application so the Payment seam remains a narrow,
// transaction-bound command port.
type EntitlementFulfillmentApplication struct {
	store EntitlementFulfillmentStore
}

var _ orderport.ServicePeriodEntitlementCoordinator = (*EntitlementFulfillmentApplication)(nil)
var _ orderport.HistoricalServicePeriodSourceCoordinator = (*EntitlementFulfillmentApplication)(nil)

func NewEntitlementFulfillmentApplication(store EntitlementFulfillmentStore) (*EntitlementFulfillmentApplication, error) {
	if store == nil {
		return nil, errors.New("order entitlement fulfillment store is required")
	}
	return &EntitlementFulfillmentApplication{store: store}, nil
}

func (s *EntitlementFulfillmentApplication) GrantPaidServicePeriodWithin(ctx context.Context, command orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	command.ProductName = strings.TrimSpace(command.ProductName)
	command.PaidAt = command.PaidAt.UTC().Truncate(time.Microsecond)
	command.ProcessedAt = command.ProcessedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.BeneficiaryCustomerID < 1 || command.ServiceProductID < 1 || command.ProductName == "" || len(command.ProductName) > 500 || command.DurationDays < 1 || command.PaidAt.IsZero() || command.ProcessedAt.IsZero() {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	// Delivery time is not part of the paid fact. Preserve the first successful
	// processing timestamp in the receipt, while retries of the same payment
	// replay even when they arrive at a later wall-clock time.
	payload, err := json.Marshal(struct {
		SourceOrderID         int64
		BeneficiaryCustomerID int64
		ServiceProductID      int64
		ProductName           string
		DurationDays          int32
		PaidAt                time.Time
	}{command.SourceOrderID, command.BeneficiaryCustomerID, command.ServiceProductID, command.ProductName, command.DurationDays, command.PaidAt})
	if err != nil {
		return orderport.Entitlement{}, orderport.ErrUnavailable
	}
	return s.store.GrantPaidServicePeriod(ctx, command, sha256.Sum256(payload))
}

func (s *EntitlementFulfillmentApplication) ApplyServicePeriodRefundWithin(ctx context.Context, command orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	command.ProcessedAt = command.ProcessedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.RefundAmountMinor < 1 || command.ProcessedAt.IsZero() {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	// A source order can revoke its period only once. The first amount and
	// processing time are stored with that receipt; later successful partial or
	// full refunds intentionally replay the same no-op result.
	payload, err := json.Marshal(struct{ SourceOrderID int64 }{command.SourceOrderID})
	if err != nil {
		return orderport.Entitlement{}, orderport.ErrUnavailable
	}
	return s.store.ApplyServicePeriodRefund(ctx, command, sha256.Sum256(payload))
}

func (s *EntitlementFulfillmentApplication) RecordHistoricalServicePeriodSourceWithin(ctx context.Context, command orderport.HistoricalServicePeriodSourceCommand) error {
	command.ServiceProductCode = strings.TrimSpace(command.ServiceProductCode)
	command.StartAt = command.StartAt.UTC().Truncate(time.Microsecond)
	command.EndAt = command.EndAt.UTC().Truncate(time.Microsecond)
	command.ImportedAt = command.ImportedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.SourceLineNo < 1 || command.EntitlementID < 1 || command.ServiceProductID < 1 || command.ServiceProductCode == "" || len(command.ServiceProductCode) > 200 || command.DurationDays < 1 || command.StartAt.IsZero() || command.EndAt.IsZero() || !command.EndAt.After(command.StartAt) || command.ImportedAt.IsZero() {
		return orderport.ErrConflict
	}
	return s.store.RecordHistoricalServicePeriodSource(ctx, command)
}
