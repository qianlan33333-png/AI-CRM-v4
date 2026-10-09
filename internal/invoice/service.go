package invoice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identity "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	order "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	payment "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platform "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"math/big"
	"slices"
	"time"
)

type Service struct {
	UoW              platform.UnitOfWork
	Sessions         payment.SessionReader
	Lineage          identity.LockedCanonicalLineageReader
	Identities       identity.PaymentIdentityReader
	Subjects         identity.VerifiedIdentitySubjectReader
	Payments         payment.InvoicePaymentReader
	SelfPayments     payment.SelfCheckoutReader
	Checkouts        order.CheckoutSnapshotReader
	Orders           order.SelfReader
	Refunds          payment.RefundExposureReader
	Effects          effects.TransactionalAccepter
	Jobs             p.QueryJobEnqueuer
	Provider         p.Provider
	CallbackVerifier p.NotificationVerifier
	Config           p.Config
	EffectStates     effects.WithinReader
	Finality         effects.InvoiceFinality
	Store            Repository
}

func (s *Service) actor(ctx context.Context, token string) (payment.SessionActor, []int64, error) {
	a, e := s.Sessions.LookupWithin(ctx, token, time.Now().UTC())
	if e != nil || a.PayerCustomerID < 1 {
		return a, nil, payment.ErrSessionRequired
	}
	roots, e := s.Lineage.LockedCanonicalLineage(ctx, customer.CustomerID(a.PayerCustomerID))
	if e != nil {
		return a, nil, e
	}
	ids := []int64{}
	for _, r := range roots {
		ids = append(ids, int64(r))
	}
	if len(ids) == 0 {
		return a, nil, payment.ErrSessionRequired
	}
	v, ok, e := s.Identities.VerifiedPaymentIdentity(ctx, a.PayerIdentityID, identitydomain.KindOAOpenID, s.Config.AppScope)
	if e != nil {
		return a, nil, e
	}
	if !ok || !slices.Contains(ids, int64(v.CustomerID)) {
		return a, nil, payment.ErrSessionRequired
	}
	return a, ids, nil
}
func (s *Service) reason(x order.SelfOrder, ids []int64, refund bool, a *p.Application) string {
	o := x.Order
	if !s.Config.Enabled || s.Provider == nil {
		return "not_enabled"
	}
	if o.Provider != orderdomain.ProviderWeChatPay || o.RecordOrigin != orderdomain.RecordOriginNative || !o.EffectEligible {
		return "unsupported_channel"
	}
	if o.PayerCustomerID == nil || !slices.Contains(ids, int64(*o.PayerCustomerID)) {
		return "payer_only"
	}
	if refund || o.RefundedMinor > 0 {
		return "refund"
	}
	if o.Status == orderdomain.StatusClosed || o.Status == orderdomain.StatusCancelled || o.Status == orderdomain.StatusPaymentFailed {
		return "closed"
	}
	if o.Status != orderdomain.StatusPaid || x.ConfirmedPaidAt == nil || o.ProviderTransactionNo == "" || o.Amount.Currency != "CNY" || o.Amount.AmountMinor <= 0 {
		return "not_paid"
	}
	if a != nil {
		return "occupied"
	}
	if _, e := s.freeze(o); e != nil {
		return "tax_not_configured"
	}
	return ""
}
func (s *Service) freeze(o orderdomain.Snapshot) (p.Snapshot, error) {
	c := s.Config
	v := p.Snapshot{MerchantID: c.MerchantID, AppID: c.AppID, AppScope: c.AppScope, SellerName: c.SellerName, SellerTaxID: c.SellerTaxID, BillingPersonID: c.BillingPersonID, OrderNo: o.MerchantOrderNo, TransactionID: o.ProviderTransactionNo, Amount: o.Amount.AmountMinor}
	if len(o.Items) == 0 || !p.ValidApplyID(o.ProviderTransactionNo) || c.MerchantID == "" || c.SellerTaxID == "" || c.BillingPersonID == "" {
		return v, p.ErrIneligible
	}
	total := int64(0)
	for _, line := range o.Items {
		if line.LineAmountMinor <= 0 {
			return v, p.ErrIneligible
		}
		if total > int64(^uint64(0)>>1)-line.LineAmountMinor {
			return v, p.ErrIneligible
		}
		total += line.LineAmountMinor
	}
	if total < o.Amount.AmountMinor || total <= 0 {
		return v, p.ErrIneligible
	}
	remainder := o.Amount.AmountMinor
	for i, line := range o.Items {
		tax, ok := c.Taxes[line.ProductCode]
		if !ok || !tax.Valid() {
			return v, p.ErrIneligible
		}
		amount := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(o.Amount.AmountMinor), big.NewInt(line.LineAmountMinor)), big.NewInt(total)).Int64()
		if i == len(o.Items)-1 {
			amount = remainder
		}
		remainder -= amount
		if amount <= 0 {
			return v, p.ErrIneligible
		}
		var policy *int64
		if tax.PreferentialPolicyCode != nil {
			value := *tax.PreferentialPolicyCode
			policy = &value
		}
		v.Lines = append(v.Lines, p.Line{TaxCode: tax.Code, GoodsName: tax.GoodsName, Quantity: int64(line.Quantity) * 100000000, TotalAmount: amount, TaxRate: tax.Rate, PreferentialPolicyCode: policy})
	}
	return v, nil
}

type cursor struct {
	At time.Time `json:"at"`
	ID int64     `json:"id"`
}

func (s *Service) List(ctx context.Context, token string, q p.OrdersQuery) (p.OrdersPage, error) {
	out := p.OrdersPage{Items: []p.OrderView{}, InvoiceEnabled: s.Config.Enabled && s.Provider != nil}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 50 || len(q.Keyword) > 200 || len(q.OrderNo) > 200 || q.From != nil && q.Until != nil && !q.From.Before(*q.Until) {
		return out, p.ErrInvalid
	}
	var after cursor
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || len(b) > 250 || json.Unmarshal(b, &after) != nil || after.ID < 1 || after.At.IsZero() {
			return out, p.ErrInvalid
		}
	}
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		actor, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		// Cursor marks the last scanned row even if this page has no eligible orders.
		query := order.SelfQuery{CustomerIDs: ids, State: q.State, Keyword: q.Keyword, OrderNo: q.OrderNo, From: q.From, Until: q.Until, AfterAt: after.At, AfterID: after.ID, Limit: q.Limit + 1}
		rows, e := s.Orders.ListSelfWithin(tx, query)
		if e != nil {
			return e
		}
		more := len(rows) > int(q.Limit)
		if more {
			rows = rows[:q.Limit]
		}
		orderIDs := []int64{}
		for _, r := range rows {
			orderIDs = append(orderIDs, r.Order.ID)
		}
		refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, orderIDs)
		if e != nil {
			return e
		}
		for _, r := range rows {
			o := r.Order
			v := p.OrderView{ID: o.ID, OrderNo: o.MerchantOrderNo, ProductNames: []string{}, Amount: o.Amount.AmountMinor, Currency: o.Amount.Currency, Provider: string(o.Provider), State: string(o.Status), RefundedMinor: o.RefundedMinor, TimeKind: "ordered", Time: &o.CreatedAt}
			for _, it := range o.Items {
				v.ProductNames = append(v.ProductNames, it.ProductName)
			}
			if o.Provider == orderdomain.ProviderExternalHistory {
				v.Time = nil
				v.TimeKind = "unknown"
				if o.ExternalHistory != nil && o.ExternalHistory.SourcePaidAt != nil {
					v.Time = o.ExternalHistory.SourcePaidAt
					v.TimeKind = "paid"
				}
			}
			a, ae := s.Store.byOrder(tx, o.ID)
			var existing *p.Application
			if ae == nil {
				existing = &a
				safe := p.MarshalReceipt(a)
				if slices.Contains(ids, a.CustomerID) {
					v.Invoice = &safe
				}
			} else if !errors.Is(ae, p.ErrNotFound) {
				return ae
			}
			_, v.RefundPending = refunds[o.ID]
			v.RefundPending = v.RefundPending && o.RefundedMinor == 0
			_, refund := refunds[o.ID]
			v.InvoiceReason = s.reason(r, ids, refund, existing)
			v.InvoiceEligible = v.InvoiceReason == ""
			if !q.EligibleOnly && o.Status == orderdomain.StatusPendingPayment && s.SelfPayments != nil && s.Checkouts != nil {
				continuation, ce := s.continuationWithin(tx, actor, ids, r)
				if ce == nil {
					v.PaymentPath = continuation.Path
				}
				if ce != nil && !errors.Is(ce, p.ErrIneligible) && !errors.Is(ce, p.ErrNotFound) {
					return ce
				}
			}
			if !q.EligibleOnly || v.InvoiceEligible {
				out.Items = append(out.Items, v)
			}
		}
		if more && len(rows) > 0 {
			last := rows[len(rows)-1].Order
			b, _ := json.Marshal(cursor{At: last.CreatedAt, ID: last.ID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
		}
		return nil
	})
	return out, e
}
func newID() (string, error) {
	b := make([]byte, 14)
	_, e := rand.Read(b)
	return "inv_" + hex.EncodeToString(b), e
}
func (s *Service) Create(ctx context.Context, token string, orderID int64, key string) (p.Receipt, error) {
	return s.create(ctx, token, orderID, key, nil, nil)
}
func (s *Service) create(ctx context.Context, token string, orderID int64, key string, title *p.TitleSubmission, official *officialTitle) (p.Receipt, error) {
	var a p.Application
	if orderID < 1 || len(key) < 16 || len(key) > 120 {
		return p.Receipt{}, p.ErrInvalid
	}
	if !s.Config.Enabled || s.Provider == nil {
		return p.Receipt{}, p.ErrDisabled
	}
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		actor, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		o, e := s.Orders.GetSelfWithin(tx, orderID, ids, true)
		if e != nil {
			return p.ErrNotFound
		}
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		digest := effects.Hash("invoice.create", key)
		var priorOrder int64
		var priorID string
		e = t.QueryRow(tx, `SELECT order_id,application_id FROM invoice_operation_receipts WHERE customer_id=ANY($1) AND key_digest=$2`, ids, digest).Scan(&priorOrder, &priorID)
		if e == nil {
			if priorOrder != orderID {
				return p.ErrConflict
			}
			a, e = s.Store.get(tx, priorID, ids, true)
			if e != nil || title == nil {
				return e
			}
			return s.acceptTitleWithin(tx, &a, o, ids, *title, official)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		a, e = s.Store.byOrder(tx, orderID)
		if e == nil {
			if !slices.Contains(ids, a.CustomerID) {
				return p.ErrNotFound
			}
			a, e = s.Store.get(tx, a.ID, ids, true)
			if e != nil {
				return e
			}
			if title != nil {
				if e = s.acceptTitleWithin(tx, &a, o, ids, *title, official); e != nil {
					return e
				}
			}
			_, e = t.Exec(tx, `INSERT INTO invoice_operation_receipts(customer_id,key_digest,order_id,application_id)VALUES($1,$2,$3,$4)`, a.CustomerID, digest, orderID, a.ID)
			return e
		}
		if !errors.Is(e, p.ErrNotFound) {
			return e
		}
		refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{orderID})
		if e != nil {
			return e
		}
		_, refund := refunds[orderID]
		if s.reason(o, ids, refund, nil) != "" {
			return p.ErrIneligible
		}
		// Freeze the current verified OA identity in the configured App scope.
		// Never take identity handles from a browser or query string.
		trusted, ok, e := s.Identities.VerifiedPaymentIdentity(tx, actor.PayerIdentityID, identitydomain.KindOAOpenID, s.Config.AppScope)
		if e != nil {
			return e
		}
		if !ok || !slices.Contains(ids, int64(trusted.CustomerID)) {
			return p.ErrIneligible
		}
		a, e = s.createApplicationWithin(tx, o, int64(trusted.CustomerID), actor.PayerIdentityID)
		if e != nil {
			return e
		}
		_, e = t.Exec(tx, `INSERT INTO invoice_operation_receipts(customer_id,key_digest,order_id,application_id)VALUES($1,$2,$3,$4)`, a.CustomerID, digest, orderID, a.ID)
		if e != nil {
			return e
		}
		if title != nil {
			return s.acceptTitleWithin(tx, &a, o, ids, *title, official)
		}
		return s.Jobs.EnqueueWithin(tx, a.ID, time.Now().Add(30*time.Second))
	})
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			e = p.ErrConflict
		}
		return p.Receipt{}, e
	}
	return p.MarshalReceipt(a), nil
}

// Both entry points hold the Order lock and use this same creator.
func (s *Service) createApplicationWithin(ctx context.Context, o order.SelfOrder, customerID, identityID int64) (p.Application, error) {
	snap, e := s.freeze(o.Order)
	if e != nil {
		return p.Application{}, e
	}
	invoiceID, e := newID()
	if e != nil {
		return p.Application{}, e
	}
	a := p.Application{ID: snap.TransactionID, InvoiceID: invoiceID, OrderID: o.Order.ID, CustomerID: customerID, IdentityID: identityID, State: "awaiting_title", CardState: "pending", Snapshot: snap}
	t, e := pg.RequireTransaction(ctx)
	if e != nil {
		return a, e
	}
	raw, e := json.Marshal(snap)
	if e != nil {
		return a, e
	}
	_, e = t.Exec(ctx, `INSERT INTO invoice_applications(id,order_id,customer_id,identity_id,invoice_id,state,snapshot)VALUES($1,$2,$3,$4,$5,'awaiting_title',$6)`, a.ID, a.OrderID, a.CustomerID, a.IdentityID, a.InvoiceID, raw)
	if e != nil {
		return a, e
	}
	return a, s.Store.update(ctx, a, "created", string(effects.Hash("invoice.snapshot", string(raw))))
}
func (s *Service) Read(ctx context.Context, token, id string) (p.Application, error) {
	var a p.Application
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		_, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		a, e = s.Store.get(tx, id, ids, false)
		return e
	})
	return a, e
}
func (s *Service) Resume(ctx context.Context, token, id string) (p.Receipt, error) {
	a, e := s.Read(ctx, token, id)
	if e != nil {
		return p.Receipt{}, e
	}
	if s.Provider == nil || !s.Config.Enabled {
		return p.MarshalReceipt(a), p.ErrDisabled
	}
	// Explicit recovery always reads the original application first. It never
	// changes identifiers for an ambiguous attempt.
	if a.State != "awaiting_title" {
		e = s.Reconcile(ctx, id)
		if e != nil && !errors.Is(e, p.ErrPending) {
			return p.MarshalReceipt(a), e
		}
	}
	a, e = s.Read(ctx, token, id)
	if e != nil {
		return p.Receipt{}, e
	}
	if a.State == "failed" {
		return s.RecoverFailed(ctx, id, a.InvoiceID, false)
	}
	if a.State == "awaiting_title" {
		e = s.UoW.Within(ctx, func(tx context.Context) error { return s.Jobs.EnqueueWithin(tx, id, time.Time{}) })
		if e != nil {
			return p.Receipt{}, e
		}
	}
	a, e = s.Read(ctx, token, id)
	return p.MarshalReceipt(a), e
}
func (s *Service) File(ctx context.Context, token, id string) ([]byte, error) {
	a, e := s.Read(ctx, token, id)
	if e != nil {
		return nil, e
	}
	if a.State != "issued" {
		return nil, p.ErrIneligible
	}
	if s.Provider == nil {
		return nil, p.ErrDisabled
	}
	return s.Provider.File(ctx, a)
}

func (s *Service) queue(ctx context.Context, a *p.Application, reverse bool, at time.Time) error {
	return s.queueRevision(ctx, a, reverse, at, "")
}
func (s *Service) queueRevision(ctx context.Context, a *p.Application, reverse bool, at time.Time, revision string) error {
	kind := effects.KindInvoiceIssue
	operation := "issue"
	if reverse {
		kind = effects.KindInvoiceReverse
		operation = "reverse"
	}
	raw, _ := json.Marshal(struct {
		Snapshot  p.Snapshot
		Buyer     p.Buyer
		InvoiceID string
	}{a.Snapshot, a.Buyer, a.InvoiceID})
	receipt := effects.Hash("invoice.effect", a.ID, operation, a.InvoiceID, fmt.Sprint(a.ReverseAttempt), fmt.Sprint(a.DispatchAttempt))
	envelope := effects.Envelope{Owner: effects.OwnerInvoice, Kind: kind, SourceRefDigest: effects.Hash("invoice.intent", a.ID, a.InvoiceID, operation, fmt.Sprint(a.ReverseAttempt), fmt.Sprint(a.DispatchAttempt)), TargetRefDigest: effects.Hash("invoice.merchant", a.Snapshot.MerchantID), PayloadDigest: effects.Hash("invoice.payload", string(raw)), PolicyVersionHash: effects.Hash("invoice.policy", "one-order-one-invoice.v1")}
	if revision != "" {
		receipt = effects.Hash("invoice.corrected.effect", string(receipt), revision)
		envelope.SourceRefDigest = effects.Hash("invoice.corrected.intent", string(envelope.SourceRefDigest), revision)
	}
	projection, _, e := s.Effects.AcceptAndQueueWithin(ctx, effects.AcceptCommand{ReceiptKey: receipt, Envelope: envelope, ScheduledAt: at})
	if e != nil {
		return e
	}
	if reverse {
		a.ReverseEffect = projection.ID
		a.State = "reversing"
	} else {
		a.IssueEffect = projection.ID
		a.State = "queued"
	}
	return s.Store.update(ctx, *a, operation+"_accepted", string(envelope.Fingerprint()))
}
func (s *Service) withOrder(ctx context.Context, id string, fn func(context.Context, *p.Application, order.SelfOrder) error) error {
	return s.UoW.Within(ctx, func(tx context.Context) error {
		a, e := s.Store.get(tx, id, nil, false)
		if e != nil {
			return e
		}
		ids, e := s.customerIDs(tx, a.CustomerID)
		if e != nil {
			return e
		}
		o, e := s.Orders.GetSelfWithin(tx, a.OrderID, ids, true)
		if e != nil {
			return e
		}
		a, e = s.Store.get(tx, id, nil, true)
		if e != nil {
			return e
		}
		return fn(tx, &a, o)
	})
}
func (s *Service) customerIDs(ctx context.Context, id int64) ([]int64, error) {
	roots, e := s.Lineage.LockedCanonicalLineage(ctx, customer.CustomerID(id))
	if e != nil {
		return nil, e
	}
	ids := make([]int64, 0, len(roots))
	for _, r := range roots {
		ids = append(ids, int64(r))
	}
	return ids, nil
}
func (s *Service) Reconcile(ctx context.Context, id string) error {
	if s.Provider == nil || !s.Config.Enabled {
		return p.ErrDisabled
	}
	var a p.Application
	e := s.UoW.Within(ctx, func(tx context.Context) error { var e error; a, e = s.Store.get(tx, id, nil, false); return e })
	if e != nil {
		return e
	}
	if a.State == "cancelled" || a.State == "reversed" && a.CardState == "DISCARDED" {
		return nil
	}
	if a.State == "awaiting_title" {
		buyer := a.Buyer
		if a.IssueEffect == "" || !buyer.Valid(a.Snapshot) {
			buyer, e = s.Provider.ReadTitle(ctx, a)
			if e != nil {
				return e
			}
			// New first acceptance is organization-only, including legacy
			// official title notifications. Already accepted history is frozen.
			if buyer.Type != "ORGANIZATION" {
				return p.ErrInvalid
			}
		}
		if !buyer.Valid(a.Snapshot) {
			return p.ErrConflict
		}
		return s.withOrder(ctx, id, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
			if a.State != "awaiting_title" {
				return nil
			}
			refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{a.OrderID})
			if e != nil {
				return e
			}
			_, refund := refunds[a.OrderID]
			if a.RefundConfirmed || o.Order.RefundedMinor > 0 {
				a.State = "cancelled"
				return s.Store.update(tx, *a, "cancelled_before_issue", string(effects.Hash("invoice.refund", a.ID)))
			}
			if refund {
				return p.ErrPending
			}
			ids, e := s.customerIDs(tx, a.CustomerID)
			if e != nil {
				return e
			}
			if s.reason(o, ids, false, nil) != "" {
				return p.ErrIneligible
			}
			if a.IssueEffect == "" {
				a.Buyer = buyer
			}
			return s.queue(tx, a, false, time.Time{})
		})
	}
	priorReverse := a.ReverseEffect
	result, e := s.Provider.Query(ctx, a)
	if e != nil {
		return e
	}
	pending := false
	e = s.withOrder(ctx, id, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if result.InvoiceID != a.InvoiceID {
			return p.ErrPending
		}
		if (result.TotalAmount != 0 && result.TotalAmount != a.Snapshot.Amount) || (result.Seller.TaxpayerID != "" && result.Seller.TaxpayerID != a.Snapshot.SellerTaxID) || ((result.Status == "ISSUED" || result.Status == "REVERSED") && (result.TotalAmount != a.Snapshot.Amount || result.Seller.TaxpayerID != a.Snapshot.SellerTaxID)) {
			return p.ErrConflict
		}
		if s.Finality != nil {
			var ref string
			if result.Status == "ISSUED" || result.Status == "ISSUE_FAILED" {
				ref = a.IssueEffect
			}
			if result.Status == "REVERSED" || result.Status == "REVERSE_FAILED" {
				ref = a.ReverseEffect
			}
			if ref != "" {
				if e := s.Finality.ReconcileInvoiceWithin(tx, ref, effects.Hash("invoice.verified.query", fmt.Sprint(result))); e != nil {
					return e
				}
			}
		}
		previous := a.State
		if a.State == "reversed" && result.Status != "REVERSED" {
			return nil
		}
		switch result.Status {
		case "ISSUED":
			a.BlueNumber = result.Blue.Number
			if a.BlueNumber == "" {
				return p.ErrConflict
			}
			if a.State != "reversing" {
				a.State = "issued"
			}
		case "REVERSED":
			a.BlueNumber = result.Blue.Number
			a.RedNumber = result.Red.Number
			if a.RedNumber == "" {
				return p.ErrConflict
			}
			a.State = "reversed"
		case "ISSUE_FAILED":
			if a.BlueNumber == "" && a.State != "issued" && a.State != "reversing" {
				a.State = "failed"
			}
		case "REVERSE_FAILED":
			if a.ReverseEffect != priorReverse {
				pending = true
				return nil
			}
			if a.ReverseEffect != "" && s.EffectStates != nil {
				effect, e := s.EffectStates.GetWithin(tx, a.ReverseEffect)
				if e != nil {
					return e
				}
				if effect.State == effects.StateQueued || effect.State == effects.StateAttempted || effect.State == effects.StateUnknown {
					pending = true
					return nil
				}
			}
			if a.State == "reversing" && time.Since(a.UpdatedAt) < 5*time.Minute {
				pending = true
				return nil
			}
			a.State = "reverse_failed"
		case "ISSUE_ACCEPTED", "REVERSE_ACCEPTED":
			pending = true
		default:
			return p.ErrConflict
		}
		// A card operation accepted alongside a rejected invoice is not delivery.
		if a.BlueNumber == "" {
			a.CardState = "pending"
		} else if cardRank(result.Card.Status) >= cardRank(a.CardState) {
			a.CardState = result.Card.Status
		}
		a.RefundConfirmed = a.RefundConfirmed || o.Order.RefundedMinor > 0
		if e := s.Store.update(tx, *a, "provider_query", string(effects.Hash("invoice.result", fmt.Sprint(result)))); e != nil {
			return e
		}
		if a.RefundConfirmed && (a.State == "issued" || a.State == "reverse_failed") {
			if a.ReverseAttempt >= 4 && a.State == "reverse_failed" {
				return nil
			}
			if previous == "reverse_failed" || a.State == "reverse_failed" {
				a.ReverseAttempt++
				return s.queue(tx, a, true, time.Now().Add(5*time.Minute))
			}
			return s.queue(tx, a, true, time.Time{})
		}
		pending = pending || (a.State == "issued" && a.CardState != "INSERTED") || (a.State == "reversing") || (a.State == "reversed" && a.CardState != "DISCARDED")
		return nil
	})
	if e != nil {
		return e
	}
	if pending {
		return p.ErrPending
	}
	return nil
}
func cardRank(s string) int {
	switch s {
	case "INSERT_ACCEPTED":
		return 1
	case "INSERTED":
		return 2
	case "DISCARD_ACCEPTED":
		return 3
	case "DISCARDED":
		return 4
	}
	return 0
}

func (s *Service) ConsumeRefundSettlementWithin(ctx context.Context, event order.RefundSettlementEvent) error {
	if !event.Valid() {
		return p.ErrInvalid
	}
	a, e := s.Store.byOrder(ctx, event.Order.ID)
	if errors.Is(e, p.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	a, e = s.Store.get(ctx, a.ID, nil, true)
	if e != nil {
		return e
	}
	a.RefundConfirmed = true
	if a.State == "awaiting_title" || a.State == "queued" || a.State == "failed" {
		a.State = "cancelled"
		if a.IssueEffect != "" && s.Finality != nil {
			if e = s.Finality.CancelInvoiceWithin(ctx, a.IssueEffect, effects.Hash("invoice.refund", event.ReceiptKey)); e != nil {
				return e
			}
		}
	} else if a.State == "issued" {
		return s.queue(ctx, &a, true, time.Time{})
	}
	if e = s.Store.update(ctx, a, "refund_confirmed", string(effects.Hash("invoice.refund", event.ReceiptKey))); e != nil {
		return e
	}
	if a.State == "issuing" || a.State == "reversing" || a.State == "reverse_failed" {
		return s.Jobs.EnqueueWithin(ctx, a.ID, time.Time{})
	}
	return nil
}

// An opened refund serializes on the same Order lock as issue preparation.
// Confirmed settlement owns cancellation/reversal; a failed refund may recover.
func (s *Service) ConsumeRefundExposureWithin(ctx context.Context, event payment.RefundExposureEvent) error {
	if !event.Valid() {
		return p.ErrInvalid
	}
	_, e := s.Store.byOrder(ctx, event.OrderID)
	if errors.Is(e, p.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	// Order/Payment already serialize refund exposure on the Order lock.
	return nil
}
func (s *Service) PrepareInvoiceEffect(ctx context.Context, effectID string, reverse bool) (p.Application, error) {
	var id string
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		return t.QueryRow(tx, `SELECT id FROM invoice_applications WHERE issue_effect=$1 OR reverse_effect=$1`, effectID).Scan(&id)
	})
	if e != nil {
		return p.Application{}, e
	}
	var out p.Application
	e = s.withOrder(ctx, id, func(tx context.Context, a *p.Application, o order.SelfOrder) error {
		if reverse {
			if a.State != "reversing" || !a.RefundConfirmed || a.BlueNumber == "" || a.ReverseEffect != effectID {
				return p.ErrIneligible
			}
		} else {
			if a.State != "queued" || a.RefundConfirmed || a.IssueEffect != effectID {
				return p.ErrIneligible
			}
			r, e := s.Refunds.RefundRelatedOrderIDsWithin(tx, []int64{a.OrderID})
			if e != nil {
				return e
			}
			if _, refund := r[a.OrderID]; refund || o.Order.RefundedMinor > 0 {
				return p.ErrIneligible
			}
			a.State = "issuing"
		}
		if e := s.Store.update(tx, *a, "dispatch_prepared", string(effects.Hash("invoice.dispatch", effectID))); e != nil {
			return e
		}
		out = *a
		return nil
	})
	return out, e
}
func (s *Service) CompleteEffect(ctx context.Context, effectID string, envelope effects.Envelope, attempt effects.Attempt, result effects.AdapterResult) error {
	t, e := pg.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	var id string
	e = t.QueryRow(ctx, `SELECT id FROM invoice_applications WHERE issue_effect=$1 OR reverse_effect=$1`, effectID).Scan(&id)
	if e != nil {
		return e
	}
	a, e := s.Store.get(ctx, id, nil, true)
	if e != nil {
		return e
	}
	if result.Artifact.Kind != "" {
		if e = s.Store.officialResponse(ctx, a.ID, result.Artifact); e != nil {
			return e
		}
	}
	if !result.CallAttempted && result.Completion == effects.StateFinalFailed {
		changed := false
		if envelope.Kind == effects.KindInvoiceIssue && a.IssueEffect == effectID && (a.State == "queued" || a.State == "issuing") {
			if a.RefundConfirmed || result.FailureCode == "invoice_payment_unverified" {
				a.State = "cancelled"
			} else {
				a.State = "awaiting_title"
				a.DispatchAttempt++
			}
			changed = true
		} else if envelope.Kind == effects.KindInvoiceReverse && a.ReverseEffect == effectID && a.State == "reversing" {
			a.State = "reverse_failed"
			changed = true
		}
		// A signed query can finish before a queued retry runs. Its terminal
		// issued/reversed fact must survive a later locally stopped attempt.
		if changed {
			if e = s.Store.update(ctx, a, "dispatch_stopped", string(result.ReceiptDigest)); e != nil {
				return e
			}
		}
	}
	// Success means accepted; verified query/notification owns final state.
	return s.Jobs.EnqueueWithin(ctx, a.ID, time.Time{})
}
