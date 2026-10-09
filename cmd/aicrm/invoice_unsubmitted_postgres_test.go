package main

import (
	"encoding/json"
	"errors"
	ep "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	"strings"
	"sync"
	"testing"
	"time"
)

func unknownInvoiceFixture(t *testing.T) (*invoiceFixture, p.Application) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "未受理原单恢复", "wechat_pay", time.Now())
	f.billPayment(id)
	r, e := f.s.SubmitTitle(f.ctx, f.token, id, "unknown-original-title-key", titleFixture())
	if e != nil {
		t.Fatal(e)
	}
	a := f.read(r.ID)
	f.provider.unknown = true
	f.run(a.IssueEffect)
	return f, f.read(a.ID)
}

func TestInvoicePostgreSQLUnsubmittedReplayKeepsInvoiceAndIsAtomic(t *testing.T) {
	f, prior := unknownInvoiceFixture(t)
	before := invoiceBillCounts(t, f)
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); !errors.Is(e, p.ErrPending) {
		t.Fatal("active lease replayed", e)
	}
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, strings.TrimPrefix(prior.IssueEffect, "eer_")); e != nil {
		t.Fatal(e)
	}
	f.provider.unsubmittedError = p.ErrPending
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); !errors.Is(e, p.ErrPending) {
		t.Fatal("unknown official application replayed", e)
	}
	f.provider.unsubmittedError = nil
	fault := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = fault
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); e == nil {
		t.Fatal("replay rollback absent")
	}
	if invoiceBillCounts(t, f) != before || f.read(prior.ID).DispatchAttempt != prior.DispatchAttempt {
		t.Fatal("partial original replay")
	}
	var state string
	if e := f.pool.QueryRow(f.ctx, `SELECT state FROM external_effects WHERE id=$1`, strings.TrimPrefix(prior.IssueEffect, "eer_")).Scan(&state); e != nil || state != "outcome_unknown" {
		t.Fatal("finality escaped rollback", e)
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
		if e != nil {
			t.Fatal(e)
		}
	}
	a := f.read(prior.ID)
	if a.InvoiceID != prior.InvoiceID || a.ID != prior.ID || a.IssueAttempt != prior.IssueAttempt || a.DispatchAttempt != prior.DispatchAttempt+1 || a.IssueEffect == prior.IssueEffect || invoiceBillCounts(t, f)[3] != before[3]+1 {
		t.Fatal("original identifiers or bounded effect changed")
	}
	if e := f.s.RecoverNativeTitle(f.ctx, a.ID, a.InvoiceID); e != nil && !errors.Is(e, p.ErrPending) {
		t.Fatal(e)
	}
	if invoiceBillCounts(t, f)[3] != before[3]+1 {
		t.Fatal("native job implicitly replayed original")
	}
}

func TestInvoicePostgreSQLUnsubmittedReplayRejectsRefundAndExisting(t *testing.T) {
	for _, kind := range []string{"existing", "refund", "individual", "exhausted", "amount", "seller"} {
		t.Run(kind, func(t *testing.T) {
			f, a := unknownInvoiceFixture(t)
			if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, strings.TrimPrefix(a.IssueEffect, "eer_")); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "existing":
				f.provider.unsubmittedError = p.ErrConflict
			case "refund":
				f.pendingRefund(a.OrderID, "requested")
			case "individual":
				if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET buyer=jsonb_set(buyer,'{type}','"INDIVIDUAL"') WHERE id=$1`, a.ID); e != nil {
					t.Fatal(e)
				}
			case "exhausted":
				if _, e := f.pool.Exec(f.ctx, `UPDATE invoice_applications SET dispatch_attempt=1 WHERE id=$1`, a.ID); e != nil {
					t.Fatal(e)
				}
			case "amount":
				if _, e := f.pool.Exec(f.ctx, `UPDATE orders SET amount_minor=9800 WHERE id=$1`, a.OrderID); e != nil {
					t.Fatal(e)
				}
			case "seller":
				f.s.Config.SellerTaxID = "DIFFERENT-SELLER"
			}
			before := invoiceBillCounts(t, f)
			if _, e := f.s.RecoverFailed(f.ctx, a.ID, "", true); e == nil {
				t.Fatal("unsafe original replay accepted")
			}
			if invoiceBillCounts(t, f) != before {
				t.Fatal("rejected original replay mutated facts")
			}
		})
	}
}

func TestInvoicePostgreSQLUnsubmittedReplayStopsAfterSecondUnknown(t *testing.T) {
	f, prior := unknownInvoiceFixture(t)
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, strings.TrimPrefix(prior.IssueEffect, "eer_")); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.RecoverFailed(f.ctx, prior.ID, "", true); e != nil {
		t.Fatal(e)
	}
	a := f.read(prior.ID)
	f.run(a.IssueEffect) // The sole original replay is again outcome_unknown.
	if _, e := f.pool.Exec(f.ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, strings.TrimPrefix(a.IssueEffect, "eer_")); e != nil {
		t.Fatal(e)
	}
	a = f.read(a.ID)
	before := invoiceBillCounts(t, f)
	jobCounts := func() [2]int {
		var counts [2]int
		if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM river_job),(SELECT count(*) FROM external_effect_jobs)`).Scan(&counts[0], &counts[1]); e != nil {
			t.Fatal(e)
		}
		return counts
	}
	beforeJobs := jobCounts()
	beforeJSON, _ := json.Marshal(a)
	if _, e := f.s.RecoverFailed(f.ctx, a.ID, "", true); !errors.Is(e, p.ErrIneligible) {
		t.Fatal("second original replay was not rejected", e)
	}
	afterJSON, _ := json.Marshal(f.read(a.ID))
	if string(afterJSON) != string(beforeJSON) || invoiceBillCounts(t, f) != before || jobCounts() != beforeJobs {
		t.Fatal("rejected replay changed invoice, snapshot, effect, job or audit")
	}
	f.provider.mu.Lock()
	calls := f.provider.calls
	f.provider.mu.Unlock()
	if calls != 2 {
		t.Fatal("provider calls exceed original plus single recovery", calls)
	}
}

func TestInvoicePostgreSQLOfficialResponseSharesCompletionTransaction(t *testing.T) {
	f := newInvoiceFixture(t)
	id := f.order(f.customer, f.customer, "paid", "官方回执", "wechat_pay", time.Now())
	r, e := f.s.SubmitTitle(f.ctx, f.token, id, "official-receipt-title-key", titleFixture())
	if e != nil {
		t.Fatal(e)
	}
	a := f.read(r.ID)
	payload, _ := json.Marshal(p.OfficialResponse{HTTPStatus: 403, Code: "RULE_LIMIT", Message: "isolated private invoice response"})
	f.provider.artifact = ep.ResultArtifact{Kind: p.OfficialResponseArtifact, Payload: payload, Digest: ep.Hash("external-effect.artifact.v1", p.OfficialResponseArtifact, string(payload))}
	f.provider.unknown = true
	// The provider read may finish, but owner receipt and EER completion must
	// roll back together when the existing query job fails in that transaction.
	fault := &invoiceFaultJobs{inner: f.s.Jobs, fail: true}
	f.s.Jobs = fault
	var effectID, generation, job int64
	if e = f.pool.QueryRow(f.ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation WHERE 'eer_'||e.id::text=$1`, a.IssueEffect).Scan(&effectID, &generation, &job); e != nil {
		t.Fatal(e)
	}
	if e = f.effects.RunAttempt(f.ctx, effectID, generation, job, f.provider); e == nil {
		t.Fatal("completion rollback absent")
	}
	var count int
	var rollbackState string
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM invoice_audit_events WHERE application_id=$1 AND event_type='official_response'`, a.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("official response escaped rollback", e)
	}
	if e = f.pool.QueryRow(f.ctx, `SELECT state FROM external_effects WHERE id=$1`, effectID).Scan(&rollbackState); e != nil || rollbackState != "attempted" {
		t.Fatal("completion escaped rollback", e, rollbackState)
	}
	// A fresh isolated application proves successful completion; do not blindly
	// run the attempted provider request again after the injected rollback.
	fault.fail = false
	id = f.order(f.customer, f.customer, "paid", "官方回执成功提交", "wechat_pay", time.Now())
	r, e = f.s.SubmitTitle(f.ctx, f.token, id, "official-receipt-success-key", titleFixture())
	if e != nil {
		t.Fatal(e)
	}
	a = f.read(r.ID)
	f.run(a.IssueEffect)
	var code, message, state string
	if e = f.pool.QueryRow(f.ctx, `SELECT material->>'code',material->>'message' FROM invoice_audit_events WHERE application_id=$1 AND event_type='official_response'`, a.ID).Scan(&code, &message); e != nil || code != "RULE_LIMIT" || message != "isolated private invoice response" {
		t.Fatal("owner receipt missing", e)
	}
	if e = f.pool.QueryRow(f.ctx, `SELECT state FROM external_effects WHERE id=$1`, strings.TrimPrefix(a.IssueEffect, "eer_")).Scan(&state); e != nil || state != "outcome_unknown" {
		t.Fatal("receipt silently allowed retry", e)
	}
}
