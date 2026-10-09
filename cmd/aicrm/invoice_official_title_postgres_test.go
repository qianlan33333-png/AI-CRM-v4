package main

import (
	"encoding/json"
	"errors"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureBuyerMismatch = "商户提交的购买方信息与用户填写的发票抬头不一致，请检查参数中的购买方信息与【获取用户填写的抬头】接口返回的发票抬头信息是否一致"

func completeOfficialBuyer(a p.Application) p.Buyer {
	b := a.Buyer
	b.Address = "隔离地址"
	b.Telephone = "01000000000"
	b.BankName = "隔离银行"
	b.BankAccount = "isolated-account"
	b.Email = "invoice@fixture.test"
	return b
}
func expireInvoiceEffect(f *invoiceFixture, ref string) {
	f.t.Helper()
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, strings.TrimPrefix(ref, "eer_")); e != nil {
		f.t.Fatal(e)
	}
}
func mismatchInvoiceFixture(t *testing.T) (*invoiceFixture, p.Application) {
	f, a := unknownInvoiceFixture(t)
	expireInvoiceEffect(f, a.IssueEffect)
	if _, e := f.s.RecoverFailed(f.ctx, a.ID, "", true); e != nil {
		t.Fatal(e)
	}
	a = f.read(a.ID)
	payload, _ := json.Marshal(p.OfficialResponse{HTTPStatus: 403, Code: "RULE_LIMIT", Message: fixtureBuyerMismatch})
	f.provider.artifact = effects.ResultArtifact{Kind: p.OfficialResponseArtifact, Payload: payload, Digest: effects.Hash("external-effect.artifact.v1", p.OfficialResponseArtifact, string(payload))}
	f.run(a.IssueEffect)
	a = f.read(a.ID)
	buyer := completeOfficialBuyer(a)
	f.provider.officialBuyer = &buyer
	return f, a
}

func TestInvoicePostgreSQLOfficialTitleSharedAndValidated(t *testing.T) {
	for _, kind := range []string{"complete", "unknown", "other-unit", "too-long", "absent", "beneficiary"} {
		t.Run(kind, func(t *testing.T) {
			f := newInvoiceFixture(t)
			payer := f.customer
			if kind == "beneficiary" {
				payer = f.other
			}
			id := f.order(payer, f.customer, "paid", "完整官方抬头", "wechat_pay", time.Now())
			title := titleFixture()
			var orderNo, txn string
			if e := f.pool.QueryRow(f.ctx, `SELECT merchant_order_no,provider_transaction_no FROM orders WHERE id=$1`, id).Scan(&orderNo, &txn); e != nil {
				t.Fatal(e)
			}
			buyer := completeOfficialBuyer(p.Application{Buyer: p.Buyer{Type: title.Buyer.Type, Name: title.Buyer.Name, TaxpayerID: title.Buyer.TaxpayerID, Amount: 9900, OrderNo: orderNo, BillType: "COMM_FAPIAO"}})
			f.provider.officialBuyer = &buyer
			f.provider.optionalPresent = true
			switch kind {
			case "unknown":
				f.provider.optionalError = p.ErrUnavailable
			case "other-unit":
				buyer.Name = "另一单位"
			case "too-long":
				buyer.Address = strings.Repeat("a", 129)
			case "absent":
				f.provider.optionalPresent = false
			}
			before := invoiceBillCounts(t, f)
			r, e := f.s.SubmitTitle(f.ctx, f.token, id, "official-unit-confirm-01", title)
			if kind != "complete" && kind != "absent" {
				if e == nil || invoiceBillCounts(t, f) != before {
					t.Fatal("invalid official title accepted", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			a := f.read(r.ID)
			if kind == "complete" {
				left, _ := json.Marshal(a.Buyer)
				right, _ := json.Marshal(buyer)
				if string(left) != string(right) {
					t.Fatal("official optional fields lost")
				}
			}
			counts := invoiceBillCounts(t, f)
			f.provider.optionalError = p.ErrUnavailable
			if _, e = f.s.SubmitTitle(f.ctx, f.token, id, "official-unit-confirm-02", title); e != nil || invoiceBillCounts(t, f)[3] != counts[3] {
				t.Fatal("duplicate submission depends on provider or reissued", e)
			}
		})
	}
}

func TestInvoicePostgreSQLBuyerCorrectionAtomicAndOnce(t *testing.T) {
	f, prior := mismatchInvoiceFixture(t)
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); !errors.Is(e, p.ErrPending) {
		t.Fatal("active attempt corrected", e)
	}
	expireInvoiceEffect(f, prior.IssueEffect)
	before := invoiceBillCounts(t, f)
	fault := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = fault
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); e == nil {
		t.Fatal("correction rollback absent")
	}
	if invoiceBillCounts(t, f) != before || f.read(prior.ID).Buyer.Email != "" {
		t.Fatal("material or effects escaped rollback")
	}
	var state string
	if e := f.pool.QueryRow(f.ctx, `SELECT state FROM external_effects WHERE id=$1`, strings.TrimPrefix(prior.IssueEffect, "eer_")).Scan(&state); e != nil || state != "outcome_unknown" {
		t.Fatal("finality escaped correction rollback", e)
	}
	fault.fail = false
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, p.ErrIneligible) {
			t.Fatal(e)
		}
	}
	a := f.read(prior.ID)
	snap, _ := json.Marshal(a.Snapshot)
	oldSnap, _ := json.Marshal(prior.Snapshot)
	if a.InvoiceID != prior.InvoiceID || a.DispatchAttempt != prior.DispatchAttempt || a.IssueAttempt != prior.IssueAttempt || string(snap) != string(oldSnap) || a.Buyer.Email != f.provider.officialBuyer.Email || invoiceBillCounts(t, f)[3] != before[3]+1 {
		t.Fatal("correction changed identity/budget/finance or lost title")
	}
	var auditCount int
	var oldEmail, newEmail string
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*),coalesce(min(material->'Before'->>'email'),''),coalesce(min(material->'After'->>'email'),'') FROM invoice_audit_events WHERE application_id=$1 AND event_type='buyer_corrected'`, a.ID).Scan(&auditCount, &oldEmail, &newEmail); e != nil || auditCount != 1 || oldEmail != "" || newEmail != a.Buyer.Email {
		t.Fatal("correction material audit missing", e)
	}
	var storedInvoice, storedEffect string
	if e := f.pool.QueryRow(f.ctx, `SELECT material->>'invoice_id',material->>'effect_id' FROM invoice_audit_events WHERE application_id=$1 AND event_type='buyer_corrected'`, a.ID).Scan(&storedInvoice, &storedEffect); e != nil || storedInvoice != prior.InvoiceID || storedEffect != prior.IssueEffect {
		t.Fatal("persistent correction marker keys/binding incorrect", e)
	}
	f.run(a.IssueEffect)
	expireInvoiceEffect(f, a.IssueEffect)
	a = f.read(a.ID)
	f.provider.officialBuyer.Address = "后来改变的官方地址"
	before = invoiceBillCounts(t, f)
	var jobsBefore int
	f.pool.QueryRow(f.ctx, `SELECT count(*) FROM river_job`).Scan(&jobsBefore)
	rawBefore, _ := json.Marshal(a)
	if _, e := f.s.RecoverFailed(f.ctx, a.ID, "", true); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("second material correction accepted", e)
	}
	rawAfter, _ := json.Marshal(f.read(a.ID))
	var jobsAfter int
	f.pool.QueryRow(f.ctx, `SELECT count(*) FROM river_job`).Scan(&jobsAfter)
	if invoiceBillCounts(t, f) != before || jobsBefore != jobsAfter || string(rawBefore) != string(rawAfter) {
		t.Fatal("second correction mutated owner/effects/jobs/audit")
	}
}

func TestInvoicePostgreSQLBuyerCorrectionRejectsStaleOrUnsafe(t *testing.T) {
	for _, kind := range []string{"stale-response", "unknown-root", "other-unit", "core-whitespace", "amount", "refund", "invalid-extra"} {
		t.Run(kind, func(t *testing.T) {
			f, a := mismatchInvoiceFixture(t)
			expireInvoiceEffect(f, a.IssueEffect)
			switch kind {
			case "stale-response":
				if _, e := f.pool.Exec(f.ctx, `INSERT INTO invoice_audit_events(application_id,event_type,evidence_digest)VALUES($1,'dispatch_prepared',$2)`, a.ID, string(effects.Hash("invoice.dispatch", a.IssueEffect))); e != nil {
					t.Fatal(e)
				}
			case "unknown-root":
				f.provider.unsubmittedError = p.ErrPending
			case "other-unit":
				f.provider.officialBuyer.Name = "不同单位"
			case "core-whitespace":
				f.provider.officialBuyer.Name = " " + a.Buyer.Name
			case "amount":
				f.provider.officialBuyer.Amount = 9800
			case "refund":
				f.pendingRefund(a.OrderID, "requested")
			case "invalid-extra":
				f.provider.officialBuyer.BankAccount = strings.Repeat("x", 33)
			}
			before := invoiceBillCounts(t, f)
			if _, e := f.s.RecoverFailed(f.ctx, a.ID, "", true); e == nil {
				t.Fatal("unsafe correction accepted")
			}
			if invoiceBillCounts(t, f) != before {
				t.Fatal("rejected correction wrote facts")
			}
		})
	}
}
