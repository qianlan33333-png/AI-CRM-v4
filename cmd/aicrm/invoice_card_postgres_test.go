package main

import (
	"context"
	"encoding/json"
	"errors"
	cp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	ep "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strings"
	"sync"
	"testing"
)

type cardConfigReader struct{ checksum string }

func (r *cardConfigReader) EffectiveSnapshot(context.Context) (cp.EffectiveSnapshot, error) {
	return cp.EffectiveSnapshot{Checksum: r.checksum}, nil
}
func (r *cardConfigReader) EffectiveSnapshotWithin(ctx context.Context) (cp.EffectiveSnapshot, error) {
	if _, e := pg.RequireTransaction(ctx); e != nil {
		return cp.EffectiveSnapshot{}, e
	}
	return r.EffectiveSnapshot(ctx)
}

type cardFixtureAdapter struct {
	f        *invoiceFixture
	unknown  bool
	rejected bool
	calls    int
}

func (a *cardFixtureAdapter) Execute(ctx context.Context, e ep.Envelope, attempt ep.Attempt) (ep.AdapterResult, error) {
	if _, err := pg.RequireTransaction(ctx); err == nil {
		return ep.AdapterResult{}, errors.New("provider called inside transaction")
	}
	c, err := a.f.s.PrepareCardEffect(ctx, attempt.EffectID)
	if err != nil {
		return ep.AdapterResult{}, err
	}
	if c.LogoURL != "https://mmbiz.qpic.cn/mmbiz_png/first/0" {
		return ep.AdapterResult{}, errors.New("material changed")
	}
	a.calls++
	if a.unknown {
		return ep.AdapterResult{Completion: ep.StateUnknown, CallAttempted: true, ReceiptDigest: ep.Hash("fixture", "unknown")}, nil
	}
	if a.rejected {
		return ep.AdapterResult{Completion: ep.StateFinalFailed, CallAttempted: true, ReceiptDigest: ep.Hash("fixture", "rejected"), FailureCode: "card_configuration_rejected"}, nil
	}
	raw := json.RawMessage(`{"card_id":"card-fixture","card_appid":"wx-fixture"}`)
	return ep.AdapterResult{Completion: ep.StateExecuted, CallAttempted: true, RealExternalCallExecuted: true, ReceiptDigest: ep.Hash("fixture", "card"), Artifact: ep.ResultArtifact{Kind: "invoice_card_template_v1", Payload: raw, Digest: ep.Hash("external-effect.artifact.v1", "invoice_card_template_v1", string(raw))}}, nil
}
func TestInvoiceCardPostgreSQLAtomicConcurrentAndUnknown(t *testing.T) {
	for _, mode := range []string{"executed", "unknown", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			unknown := mode == "unknown"
			rejected := mode == "rejected"
			f := newInvoiceFixture(t)
			reader := &cardConfigReader{checksum: "applied"}
			f.s.Configuration = reader
			f.s.AppliedConfigurationChecksum = "applied"
			f.s.CardConfig = p.CardConfig{MerchantID: f.s.Config.MerchantID, AppID: f.s.Config.AppID, PayeeName: "商户", LogoURL: "https://mmbiz.qpic.cn/mmbiz_png/first/0", JumpURL: "https://crm.test/my/orders", Confirmed: true}
			if _, e := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_card_material() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture rollback'; END $$; CREATE TRIGGER fail_card_material BEFORE INSERT ON invoice_card_setup_intents FOR EACH ROW EXECUTE FUNCTION fail_card_material()`); e != nil {
				t.Fatal(e)
			}
			if _, e := f.s.AcceptCardSetup(f.ctx, 7); e == nil {
				t.Fatal("material failure did not roll back")
			}
			if _, e := f.pool.Exec(f.ctx, `DROP TRIGGER fail_card_material ON invoice_card_setup_intents; DROP FUNCTION fail_card_material()`); e != nil {
				t.Fatal(e)
			}
			var materials, effects, jobs int
			counts := func() {
				if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM invoice_card_setup_intents),(SELECT count(*) FROM external_effects WHERE kind='wechat_invoice_card_configure_v1'),(SELECT count(*) FROM external_effect_jobs)`).Scan(&materials, &effects, &jobs); e != nil {
					t.Fatal(e)
				}
			}
			counts()
			if materials != 0 || effects != 0 || jobs != 0 {
				t.Fatal("split rollback")
			}
			reader.checksum = "pending"
			if _, e := f.s.AcceptCardSetup(f.ctx, 7); !errors.Is(e, p.ErrConflict) {
				t.Fatal("unapplied configuration accepted")
			}
			reader.checksum = "applied"
			var wg sync.WaitGroup
			refs := make(chan string, 8)
			errs := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); out, e := f.s.AcceptCardSetup(f.ctx, 7); refs <- out.EffectID; errs <- e }()
			}
			wg.Wait()
			close(refs)
			close(errs)
			ref := ""
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			for got := range refs {
				if ref == "" {
					ref = got
				}
				if ref != got {
					t.Fatal("duplicate effect accepted")
				}
			}
			counts()
			if materials != 1 || effects != 1 || jobs != 1 {
				t.Fatal("duplicate queued writes")
			}
			var id, generation, job int64
			if e := f.pool.QueryRow(f.ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON e.id=j.effect_id AND e.generation=j.generation WHERE 'eer_'||e.id::text=$1`, ref).Scan(&id, &generation, &job); e != nil {
				t.Fatal(e)
			}
			adapter := &cardFixtureAdapter{f: f, unknown: unknown, rejected: rejected}
			if e := f.effects.RunAttempt(f.ctx, id, generation, job, adapter); e != nil {
				t.Fatal(e)
			}
			if e := f.effects.RunAttempt(f.ctx, id, generation, job, adapter); e != nil {
				t.Fatal(e)
			}
			if adapter.calls != 1 {
				t.Fatal("ambiguous card write repeated")
			}
			out, e := f.s.ReadCardSetup(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			if !unknown && !rejected && (out.State != "executed" || out.CardID != "card-fixture") {
				t.Fatal("official card receipt missing")
			}
			if unknown && out.State != "outcome_unknown" {
				t.Fatal("unknown lost")
			}
			if rejected {
				if out.State != "final_failed" || out.CardID != "" {
					t.Fatal("signed rejection lost")
				}
				retry, err := f.s.AcceptCardSetup(f.ctx, 7)
				if err != nil || retry.EffectID == ref {
					t.Fatal("explicit retry after rejection unavailable")
				}
				again, err := f.s.AcceptCardSetup(f.ctx, 7)
				if err != nil || again.EffectID != retry.EffectID {
					t.Fatal("retry click duplicated effect")
				}
				return
			}
			f.s.CardConfig.LogoURL = "https://mmbiz.qpic.cn/mmbiz_png/second/0"
			_, e = f.s.AcceptCardSetup(f.ctx, 7)
			if unknown && !errors.Is(e, p.ErrPending) {
				t.Fatal("overwrote unknown effect")
			}
			if !unknown && e != nil {
				t.Fatal(e)
			}
			original, e := f.s.PrepareCardEffect(f.ctx, ref)
			if e != nil || !strings.Contains(original.LogoURL, "first") {
				t.Fatal("in-flight material not immutable")
			}
		})
	}
}
