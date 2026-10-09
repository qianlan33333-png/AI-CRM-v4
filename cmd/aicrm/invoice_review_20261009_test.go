package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	ps "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// These review cases are independent of the paused refund/reversal journeys.
// The empty read port touches no refund table and emits no settlement event.
type invoiceReviewNoRefunds struct{}

func (invoiceReviewNoRefunds) RefundRelatedOrderIDsWithin(context.Context, []int64) (map[int64]struct{}, error) {
	return map[int64]struct{}{}, nil
}

func newInvoiceReviewFixture(t *testing.T) *invoiceFixture {
	t.Helper()
	f := newInvoiceFixture(t)
	f.s.Refunds = invoiceReviewNoRefunds{}
	return f
}

type invoiceReviewFileProvider struct {
	p.Provider
	file func(context.Context, p.Application) ([]byte, error)
}

type invoiceReviewQueryProvider struct {
	p.Provider
	query func(context.Context, p.Application) (p.Result, error)
}

func (v invoiceReviewQueryProvider) Query(ctx context.Context, a p.Application) (p.Result, error) {
	return v.query(ctx, a)
}

func (v invoiceReviewFileProvider) File(ctx context.Context, a p.Application) ([]byte, error) {
	return v.file(ctx, a)
}

func invoiceReviewOtherSession(t *testing.T, f *invoiceFixture) string {
	t.Helper()
	var identityID int64
	if e := f.pool.QueryRow(f.ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at)VALUES($1,'oa_openid','wechat-app:wx-fixture','openid-review-other','verified','invoice.review.fixture',1,'active',now())RETURNING id`, f.other).Scan(&identityID); e != nil {
		t.Fatal(e)
	}
	token := "invoice-review-other-session-0001"
	if e := f.s.UoW.Within(f.ctx, func(ctx context.Context) error {
		_, e := ps.NewPostgreSQL().Insert(ctx, ps.Record{TokenDigest: sha256.Sum256([]byte(token)), PayerIdentityID: identityID, PayerCustomerID: customer.CustomerID(f.other), BeneficiarySelection: "unresolved", AppScopeDigest: sha256.Sum256([]byte("wechat-app:wx-fixture")), Channel: "h5_official_account", UnionIDVerified: true, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return token
}

func TestInvoiceReview20261009PDFRejectsLineageSplitDuringRead(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	otherToken := invoiceReviewOtherSession(t, f)
	id := f.order(f.other, f.other, "paid", "隔离归属课程", "wechat_pay", time.Now())
	r, e := f.s.Create(f.ctx, otherToken, id, "review-other-pdf-0001")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE invoice_applications SET state='issued',blue_number='BLUE-fixture' WHERE id=$1`, r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Read(f.ctx, f.token, r.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("unmerged customer read another invoice", e)
	}
	// Construct only local Identity-owned fixture facts; no production merge or
	// invoice owner mutation takes place. The Invoice reader uses the real Port.
	if _, e = f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other, f.customer); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Read(f.ctx, f.token, r.ID); e != nil {
		t.Fatal("canonical merged lineage did not authorize invoice", e)
	}
	f.s.Provider = invoiceReviewFileProvider{Provider: f.provider, file: func(ctx context.Context, _ p.Application) ([]byte, error) {
		if _, e := f.pool.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other); e != nil {
			return nil, e
		}
		return []byte("%PDF-isolated-split"), nil
	}}
	body, e := f.s.File(f.ctx, f.token, r.ID)
	if !errors.Is(e, p.ErrNotFound) || len(body) != 0 {
		t.Fatalf("PDF escaped canonical lineage split: bytes=%d err=%v", len(body), e)
	}
	if _, e = f.s.Read(f.ctx, otherToken, r.ID); e != nil {
		t.Fatal("split lost historical invoice owner", e)
	}
}

func TestInvoiceReview20261009PDFRevalidatesAfterRead(t *testing.T) {
	for _, scenario := range []string{"unchanged", "expired-session", "changed-owner", "changed-invoice", "changed-state"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInvoiceReviewFixture(t)
			id := f.order(f.customer, f.customer, "paid", "隔离PDF课程", "wechat_pay", time.Now())
			r := f.create(id, "review-pdf-local-0001")
			if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET state='issued',blue_number='BLUE-fixture' WHERE id=$1`, r.ID); e != nil {
				t.Fatal(e)
			}
			calls := 0
			f.s.Provider = invoiceReviewFileProvider{Provider: f.provider, file: func(ctx context.Context, a p.Application) ([]byte, error) {
				calls++
				if _, e := pg.RequireTransaction(ctx); e == nil {
					t.Fatal("PDF read held an authorization transaction")
				}
				var e error
				switch scenario {
				case "expired-session":
					_, e = f.pool.Exec(ctx, `UPDATE payment_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE payer_identity_id=$1`, f.identity)
				case "changed-owner":
					_, e = f.pool.Exec(ctx, `UPDATE invoice_applications SET customer_id=$2 WHERE id=$1`, a.ID, f.other)
				case "changed-invoice":
					_, e = f.pool.Exec(ctx, `UPDATE invoice_applications SET invoice_id='inv_review_replacement',blue_number='BLUE-replacement' WHERE id=$1`, a.ID)
				case "changed-state":
					_, e = f.pool.Exec(ctx, `UPDATE invoice_applications SET state='failed' WHERE id=$1`, a.ID)
				}
				if e != nil {
					return nil, e
				}
				return []byte("%PDF-isolated-review"), nil
			}}
			body, e := f.s.File(f.ctx, f.token, r.ID)
			if scenario == "unchanged" {
				if e != nil || string(body) != "%PDF-isolated-review" || calls != 1 {
					t.Fatalf("unchanged authorized PDF rejected: calls=%d err=%v", calls, e)
				}
				return
			}
			want := p.ErrIneligible
			if scenario == "expired-session" {
				want = pp.ErrSessionRequired
			}
			if scenario == "changed-owner" {
				want = p.ErrNotFound
			}
			if !errors.Is(e, want) || len(body) != 0 || calls != 1 {
				t.Fatalf("PDF escaped after authorization/state change: scenario=%s bytes=%d calls=%d err=%v want=%v", scenario, len(body), calls, e, want)
			}
		})
	}
}

func TestInvoiceReview20261009NativeTitleUsesFullValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*p.Buyer)
	}{
		{"blank-name", func(b *p.Buyer) { b.Name = " \t\n" }},
		{"blank-taxpayer", func(b *p.Buyer) { b.TaxpayerID = " \t\n" }},
		{"name", func(b *p.Buyer) { b.Name = strings.Repeat("a", 257) }},
		{"taxpayer", func(b *p.Buyer) { b.TaxpayerID = strings.Repeat("a", 33) }},
		{"address", func(b *p.Buyer) { b.Address = strings.Repeat("a", 129) }},
		{"telephone", func(b *p.Buyer) { b.Telephone = strings.Repeat("a", 33) }},
		{"bank-name", func(b *p.Buyer) { b.BankName = strings.Repeat("a", 129) }},
		{"bank-account", func(b *p.Buyer) { b.BankAccount = strings.Repeat("a", 33) }},
		{"phone", func(b *p.Buyer) { b.Phone = strings.Repeat("a", 33) }},
		{"email", func(b *p.Buyer) { b.Email = strings.Repeat("a", 191) }},
		{"message", func(b *p.Buyer) { b.UserApplyMessage = strings.Repeat("a", 101) }},
		{"invalid-utf8-extra", func(b *p.Buyer) { b.Address = string([]byte{0xff}) }},
		{"individual", func(b *p.Buyer) { b.Type = "INDIVIDUAL" }},
		{"wrong-amount", func(b *p.Buyer) { b.Amount++ }},
		{"wrong-order", func(b *p.Buyer) { b.OrderNo = "other-isolated-order" }},
		{"unsupported-bill-type", func(b *p.Buyer) { b.BillType = "VAT_FAPIAO" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newInvoiceReviewFixture(t)
			id := f.order(f.customer, f.customer, "paid", "官方抬头隔离校验", "wechat_pay", time.Now())
			r := f.create(id, "review-native-title-01")
			a := f.read(r.ID)
			buyer, e := f.provider.ReadTitle(f.ctx, a)
			if e != nil {
				t.Fatal(e)
			}
			tc.mutate(&buyer)
			f.provider.officialBuyer = &buyer
			before := invoiceBillCounts(t, f)
			e = f.s.Reconcile(f.ctx, r.ID)
			if !errors.Is(e, p.ErrInvalid) && !errors.Is(e, p.ErrConflict) {
				t.Fatalf("invalid native title reached issuance queue: field=%s err=%v", tc.name, e)
			}
			current := f.read(r.ID)
			if invoiceBillCounts(t, f) != before || current.State != "awaiting_title" || current.IssueEffect != "" || current.Buyer.Type != "" {
				t.Fatal("rejected native title committed application/audit/effect changes")
			}
		})
	}
}

func TestInvoiceReview20261009ResumeAuthorizesRecoveryAcceptance(t *testing.T) {
	for _, scenario := range []string{"unchanged", "expired-session", "lineage-split"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInvoiceReviewFixture(t)
			ownerToken, owner := f.token, f.customer
			if scenario == "lineage-split" {
				ownerToken, owner = invoiceReviewOtherSession(t, f), f.other
			}
			id := f.order(owner, owner, "paid", "隔离失败申请恢复", "wechat_pay", time.Now())
			r, e := f.s.SubmitTitle(f.ctx, ownerToken, id, "review-recovery-authority", titleFixture())
			if e != nil {
				t.Fatal(e)
			}
			a, e := f.s.Read(f.ctx, ownerToken, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			f.run(a.IssueEffect) // Local issue-only adapter, no real Provider or reversal.
			f.provider.status = "ISSUE_FAILED"
			if e = f.s.Reconcile(f.ctx, r.ID); e != nil {
				t.Fatal(e)
			}
			a, e = f.s.Read(f.ctx, ownerToken, r.ID)
			if e != nil || a.State != "failed" {
				t.Fatal("local failed invoice fixture", e)
			}
			if scenario == "lineage-split" {
				if _, e = f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other, f.customer); e != nil {
					t.Fatal(e)
				}
			}
			var effectsBefore int
			if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM external_effects WHERE owner='invoice'`).Scan(&effectsBefore); e != nil {
				t.Fatal(e)
			}
			queries := 0
			f.s.Provider = invoiceReviewQueryProvider{Provider: f.provider, query: func(ctx context.Context, current p.Application) (p.Result, error) {
				queries++
				// Resume first reconciles, then re-reads authorization. Only the
				// following RecoverFailed proof read revokes that authorization.
				if queries == 2 {
					switch scenario {
					case "expired-session":
						_, e = f.pool.Exec(ctx, `UPDATE payment_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE payer_identity_id=$1`, f.identity)
					case "lineage-split":
						_, e = f.pool.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other)
					}
					if e != nil {
						return p.Result{}, e
					}
				}
				return f.provider.Query(ctx, current)
			}}
			_, e = f.s.Resume(f.ctx, f.token, r.ID)
			var effectsAfter, issueAttempt int
			var currentInvoice string
			if readErr := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM external_effects WHERE owner='invoice'),issue_attempt,invoice_id FROM invoice_applications WHERE id=$1`, r.ID).Scan(&effectsAfter, &issueAttempt, &currentInvoice); readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "unchanged" {
				if e != nil || queries != 2 || effectsAfter != effectsBefore+1 || issueAttempt != 1 || currentInvoice == a.InvoiceID {
					t.Fatalf("authorized explicit recovery rejected: queries=%d effects=%d attempt=%d err=%v", queries, effectsAfter, issueAttempt, e)
				}
				return
			}
			want := pp.ErrSessionRequired
			if scenario == "lineage-split" {
				want = p.ErrNotFound
			}
			if !errors.Is(e, want) || queries != 2 || effectsAfter != effectsBefore || issueAttempt != 0 || currentInvoice != a.InvoiceID {
				t.Fatalf("recovery accepted after current authorization revoked: scenario=%s queries=%d effects_before=%d effects_after=%d attempt=%d err=%v want=%v", scenario, queries, effectsBefore, effectsAfter, issueAttempt, e, want)
			}
		})
	}
}

func TestInvoiceReview20261009ManualTitlePreservesPayerAcrossSplit(t *testing.T) {
	f := newInvoiceReviewFixture(t)
	otherToken := invoiceReviewOtherSession(t, f)
	id := f.order(f.other, f.other, "paid", "隔离合并期间本人开票", "wechat_pay", time.Now())
	if _, e := f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other, f.customer); e != nil {
		t.Fatal(e)
	}
	r, e := f.s.SubmitTitle(f.ctx, otherToken, id, "review-merged-payer-title", titleFixture())
	if e != nil {
		t.Fatal("merged payer could not submit own invoice", e)
	}
	a, e := f.s.Read(f.ctx, otherToken, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	before := invoiceReviewDurableCounts(t, f)
	if _, e = f.pool.Exec(f.ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other); e != nil {
		t.Fatal(e)
	}
	owner, ownerErr := f.s.Read(f.ctx, otherToken, r.ID)
	_, formerCanonicalErr := f.s.Read(f.ctx, f.token, r.ID)
	if a.CustomerID != f.other || ownerErr != nil || owner.ID != r.ID || !errors.Is(formerCanonicalErr, p.ErrNotFound) {
		t.Fatalf("manual invoice moved away from original payer across split: original_payer_retained=%t owner_err=%v former_canonical_err=%v", a.CustomerID == f.other, ownerErr, formerCanonicalErr)
	}
	if invoiceReviewDurableCounts(t, f) != before {
		t.Fatal("split authorization rewrote historical invoice/receipts/effects")
	}
}

type invoiceReviewAfterUoW struct {
	inner platformport.UnitOfWork
	count int
	after func(context.Context, int) error
}

func (u *invoiceReviewAfterUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	if e := u.inner.Within(ctx, fn); e != nil {
		return e
	}
	u.count++
	return u.after(ctx, u.count)
}

func TestInvoiceReview20261009ResumeAuthorizesTitleWakeAcceptance(t *testing.T) {
	for _, scenario := range []string{"unchanged", "expired-session", "lineage-split"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInvoiceReviewFixture(t)
			token := f.token
			payer := f.customer
			if scenario == "lineage-split" {
				token = invoiceReviewOtherSession(t, f)
				payer = f.other
			}
			id := f.order(payer, payer, "paid", "隔离待填抬头唤醒", "wechat_pay", time.Now())
			r, e := f.s.Create(f.ctx, token, id, "review-title-wake-acceptance")
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "lineage-split" {
				if _, e = f.pool.Exec(f.ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other, f.customer); e != nil {
					t.Fatal(e)
				}
			}
			// A finished read-only title query is outside River's active-job
			// uniqueness set. No worker or Provider write is run by this test.
			if _, e = f.pool.Exec(f.ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE queue='invoice-reconciliation' AND args->>'application_id'=$1`, r.ID); e != nil {
				t.Fatal(e)
			}
			before := invoiceReviewDurableCounts(t, f)
			f.s.UoW = &invoiceReviewAfterUoW{inner: f.s.UoW, after: func(ctx context.Context, count int) error {
				if count != 2 {
					return nil
				}
				// Revoke after Resume's final preliminary Read commits, before
				// it opens the transaction that accepts a new query job.
				switch scenario {
				case "expired-session":
					_, e = f.pool.Exec(ctx, `UPDATE payment_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE payer_identity_id=$1`, f.identity)
				case "lineage-split":
					_, e = f.pool.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, f.other)
				}
				return e
			}}
			_, e = f.s.Resume(f.ctx, f.token, r.ID)
			after := invoiceReviewDurableCounts(t, f)
			if scenario == "unchanged" {
				before[5]++
				if e != nil || after != before {
					t.Fatalf("authorized title wake failed: before=%v after=%v err=%v", before, after, e)
				}
				return
			}
			want := pp.ErrSessionRequired
			if scenario == "lineage-split" {
				want = p.ErrNotFound
			}
			if !errors.Is(e, want) || after != before {
				t.Fatalf("title job accepted after authorization revoked: scenario=%s before=%v after=%v err=%v want=%v", scenario, before, after, e, want)
			}
		})
	}
}
