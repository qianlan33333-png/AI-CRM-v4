package invoice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	order "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (s *Service) SubmitTitle(ctx context.Context, token string, orderID int64, key string, title p.TitleSubmission) (p.Receipt, error) {
	title.Buyer.Name = strings.TrimSpace(title.Buyer.Name)
	title.Buyer.TaxpayerID = strings.TrimSpace(title.Buyer.TaxpayerID)
	// Official issue-general contract: name string(256), taxpayer_id string(32).
	if title.Buyer.Type != "ORGANIZATION" || !utf8.ValidString(title.Buyer.Name) || !utf8.ValidString(title.Buyer.TaxpayerID) || len(title.Buyer.Name) > 256 || len(title.Buyer.TaxpayerID) > 32 || title.ExpectedAmount <= 0 || !(p.Buyer{Type: title.Buyer.Type, Name: title.Buyer.Name, TaxpayerID: title.Buyer.TaxpayerID}).Valid(p.Snapshot{}) {
		return p.Receipt{}, p.ErrInvalid
	}
	return s.create(ctx, token, orderID, key, &title)
}

// Caller holds the same Order lock used by refund and legacy title processing.
// The application lock/state check prevents an in-flight official title read
// from overwriting a confirmed title or accepting a second effect.
func (s *Service) acceptTitleWithin(ctx context.Context, a *p.Application, o order.SelfOrder, ids []int64, title p.TitleSubmission) error {
	if !slices.Contains(ids, a.CustomerID) {
		return p.ErrNotFound
	}
	if title.ExpectedAmount != a.Snapshot.Amount {
		return p.ErrConflict
	}
	if a.State != "awaiting_title" || a.IssueEffect != "" && a.Buyer.Valid(a.Snapshot) {
		if a.Buyer.Type != title.Buyer.Type || a.Buyer.Name != title.Buyer.Name || a.Buyer.TaxpayerID != title.Buyer.TaxpayerID {
			return p.ErrConflict
		}
		return nil
	}
	refunds, e := s.Refunds.RefundRelatedOrderIDsWithin(ctx, []int64{a.OrderID})
	if e != nil {
		return e
	}
	_, refund := refunds[a.OrderID]
	if a.RefundConfirmed || s.reason(o, ids, refund, nil) != "" {
		return p.ErrIneligible
	}
	snap := a.Snapshot
	if snap.Amount != o.Order.Amount.AmountMinor || snap.OrderNo != o.Order.MerchantOrderNo || snap.TransactionID != o.Order.ProviderTransactionNo || snap.MerchantID != s.Config.MerchantID || snap.AppID != s.Config.AppID || snap.AppScope != s.Config.AppScope || snap.SellerTaxID != s.Config.SellerTaxID {
		return p.ErrConflict
	}
	a.Buyer = p.Buyer{Type: title.Buyer.Type, Name: title.Buyer.Name, TaxpayerID: title.Buyer.TaxpayerID, Amount: snap.Amount, OrderNo: snap.OrderNo, BillType: "COMM_FAPIAO"}
	raw, _ := json.Marshal(a.Buyer)
	if e = s.Store.update(ctx, *a, "title_confirmed", string(effects.Hash("invoice.title", string(raw)))); e != nil {
		return e
	}
	if e = s.queue(ctx, a, false, time.Time{}); e != nil {
		return e
	}
	return s.Jobs.EnqueueWithin(ctx, a.ID, time.Time{})
}

type historyCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func (s *Service) History(ctx context.Context, token string, q p.HistoryQuery) (p.HistoryPage, error) {
	out := p.HistoryPage{Items: []p.HistoryItem{}}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 50 {
		return out, p.ErrInvalid
	}
	var after historyCursor
	if q.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || len(raw) > 250 || json.Unmarshal(raw, &after) != nil || !validApplicationID(after.ID) || after.At.IsZero() {
			return out, p.ErrInvalid
		}
	}
	e := s.UoW.Within(ctx, func(tx context.Context) error {
		_, ids, e := s.actor(tx, token)
		if e != nil {
			return e
		}
		t, e := pg.RequireTransaction(tx)
		if e != nil {
			return e
		}
		rows, e := t.Query(tx, `SELECT id,created_at FROM invoice_applications WHERE customer_id=ANY($1) AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3)) ORDER BY created_at DESC,id DESC LIMIT $4`, ids, nullableTime(after.At), after.ID, q.Limit+1)
		if e != nil {
			return e
		}
		type entry struct {
			id string
			at time.Time
		}
		entries := []entry{}
		for rows.Next() {
			var v entry
			if e = rows.Scan(&v.id, &v.at); e != nil {
				rows.Close()
				return e
			}
			entries = append(entries, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		more := len(entries) > q.Limit
		if more {
			entries = entries[:q.Limit]
		}
		for _, v := range entries {
			a, e := s.Store.get(tx, v.id, ids, false)
			if e != nil {
				return e
			}
			item := p.HistoryItem{Receipt: p.MarshalReceipt(a), OrderNo: a.Snapshot.OrderNo, ProductNames: []string{}, CreatedAt: v.at}
			o, e := s.Orders.GetSelfWithin(tx, a.OrderID, ids, false)
			if e != nil && !errors.Is(e, order.ErrNotFound) {
				return e
			}
			if e == nil {
				for _, line := range o.Order.Items {
					item.ProductNames = append(item.ProductNames, line.ProductName)
				}
			}
			out.Items = append(out.Items, item)
		}
		if more && len(entries) > 0 {
			last := entries[len(entries)-1]
			raw, _ := json.Marshal(historyCursor{last.at, last.id})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}
		return nil
	})
	return out, e
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
