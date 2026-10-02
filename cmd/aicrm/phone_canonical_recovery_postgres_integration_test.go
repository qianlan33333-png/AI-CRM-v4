package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

// The fixture driver uses existing Owner APIs and the composition migration
// harness in an isolated acceptance database. Every repair under assertion is
// performed by the supplied installed binary, not by an in-process Go app.
func TestPostgreSQLPhoneCanonicalRecoveryMaintenanceJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close()
	wrapped, e := platformpostgres.Wrap(native, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	vault, e := identitysecure.NewPhoneVault(key)
	if e != nil {
		t.Fatal(e)
	}
	owner := identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault).WithCanonicalPhoneRecovery(identityquery.PostgreSQL{})}
	root := func(name string) (identityport.ProvisionResult, identitydomain.VerifiedFact) {
		f, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindExtension, Scope: "ext:installed-phone-recovery-synthetic", Value: name, Source: "synthetic.acceptance"})
		if e != nil {
			t.Fatal(e)
		}
		var r identityport.ProvisionResult
		e = uow.Within(ctx, func(tx context.Context) error {
			var e error
			r, e = owner.ProvisionCustomerFromVerifiedIdentity(tx, f)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return r, f
	}
	merge := func(a, b identityport.ProvisionResult, f identitydomain.VerifiedFact) {
		e := uow.Within(ctx, func(tx context.Context) error {
			c, e := owner.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: a.CustomerID, Target: f, Evidence: identitydomain.LinkEvidence{Type: "synthetic_acceptance", Strength: identitydomain.EvidenceStrong, Source: "synthetic.acceptance", EventID: "fixture", Digest: strings.Repeat("1", 64), PolicyVersion: "v1"}})
			if e != nil {
				return e
			}
			if c.Candidate == nil {
				return fmt.Errorf("synthetic merge candidate missing")
			}
			m, e := owner.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: c.Candidate.ID, SurvivorCustomerID: b.CustomerID, Operator: "synthetic-acceptance"})
			if e == nil && m.Status != identityapp.LinkMerged {
				return fmt.Errorf("synthetic merge not confirmed")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	a, _ := root("alias")
	b, bf := root("middle")
	c, cf := root("canonical")
	merge(a, b, bf)
	merge(b, c, cf)
	conflict, _ := root("conflict-alias")
	target, tf := root("conflict-target")
	merge(conflict, target, tf)
	other, _ := root("other-owner")
	active, _ := root("active-source")
	broken, _ := root("unaudited-source")
	badTarget, _ := root("unaudited-target")
	if _, e = native.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now() WHERE id=$1`, broken.CustomerID, badTarget.CustomerID); e != nil {
		t.Fatal(e)
	}
	if e = uow.Within(ctx, func(tx context.Context) error {
		_, e := owner.AttachDeclaredPhoneToCustomer(tx, identityport.DeclaredPhoneCommand{CustomerID: other.CustomerID, Phone: "13900000888", Source: "order", SourceEventID: "fixture-existing-owner", IdempotencyKey: "fixture-existing-owner"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	cipher, e := ordersecure.NewContactCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	seedOrder := func(customer customerdomain.CustomerID, phone string) int64 {
		var id int64
		e := native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','v3-checkout',$1,$1,$2,$2,9900,0,'CNY','paid','native',TRUE,now(),now()) RETURNING id`, fmt.Sprintf("canonical-phone-%d", customer), customer).Scan(&id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,'phone-fixture','Canonical fixture',9900,1,9900)`, id); e != nil {
			t.Fatal(e)
		}
		raw, e := cipher.Encrypt("+86" + phone)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = native.Exec(ctx, `INSERT INTO order_contact_snapshots(order_id,phone_ciphertext,key_version,created_at) VALUES($1,$2,1,now())`, id, raw); e != nil {
			t.Fatal(e)
		}
		return id
	}
	first := seedOrder(a.CustomerID, "13800000777")
	seedOrder(conflict.CustomerID, "13900000888")
	seedOrder(broken.CustomerID, "13700000666")
	last := seedOrder(active.CustomerID, "13600000555")
	// Frozen historical answers include a known merged alias and an unknown
	// subject. The fixture is isolated; it never targets the shared staging DB.
	var actor, qid, version, submission, answer int64
	actor = surveyJourneyGovernedActor(t, ctx, native, "phone-canonical-synthetic", "Synthetic")
	if e = native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('canonical fixture','canonical fixture','','survey','all_in_one','canonical-fixture','disabled',$1,$1,now(),now()) RETURNING id`, actor).Scan(&qid); e != nil {
		t.Fatal(e)
	}
	if e = native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,created_by,created_at) VALUES($1,1,'survey','all_in_one','canonical fixture','','{}',$2,FALSE,$3,now()) RETURNING id`, qid, make([]byte, 32), actor).Scan(&version); e != nil {
		t.Fatal(e)
	}
	sc, e := surveysecure.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	seedAnswer := func(customer any, n byte) int64 {
		state := "resolved"
		if customer == nil {
			state = "unresolved"
		}
		e := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,$4,$5,$6,'canonical-fixture','fixture','survey',now(),now()) RETURNING id`, qid, version, customer, state, phoneFixtureDigest(n), phoneFixtureDigest(n+1)).Scan(&submission)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := sc.Encrypt("13500000444")
		if e != nil {
			t.Fatal(e)
		}
		if e = native.QueryRow(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,text_value_ciphertext,text_value_masked,answer_digest,created_at) VALUES($1,'mobile','phone',$2,'135****0444',$3,now()) RETURNING id`, submission, raw, phoneFixtureDigest(n+2)).Scan(&answer); e != nil {
			t.Fatal(e)
		}
		return answer
	}
	seedAnswer(a.CustomerID, 31)
	maxAnswer := seedAnswer(nil, 41)
	binary := platformconfig.CollectedPhoneAcceptanceBinary()
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "migrate-collected-phones")
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../migrate-collected-phones")
		if out, e := build.CombinedOutput(); e != nil {
			t.Fatalf("maintenance build %v %s", e, out)
		}
	} else {
		if !filepath.IsAbs(binary) {
			t.Fatal("installed binary requires absolute path")
		}
		b, e := os.ReadFile(binary)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("installed maintenance sha256=%x", sha256.Sum256(b))
	}
	run := func(mode, source string, max int64) map[string]any {
		cmd := exec.CommandContext(ctx, binary, "--mode="+mode, "--source="+source, fmt.Sprintf("--through-id=%d", max))
		cmd.Env = append(os.Environ(), "AICRM_DATABASE_URL="+url, "AICRM_IDENTITY_PHONE_DATA_KEY="+key, "AICRM_ORDER_CONTACT_DATA_KEY="+key, "AICRM_SURVEY_DATA_KEY="+key)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("installed maintenance %s failed %v %s", mode, e, out)
		}
		for _, phone := range []string{"13800000777", "13900000888", "13500000444"} {
			if strings.Contains(string(out), phone) {
				t.Fatal("plaintext leaked")
			}
		}
		var r map[string]any
		if e = json.Unmarshal(out, &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	// Capture immutable source/financial rows and original receipts, excluding
	// Customer/Identity mutations which are the intended recovery itself.
	digest := func(query string) string {
		var v string
		if e := native.QueryRow(ctx, query).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	sourceSQL := `SELECT md5(COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM orders o),'')||COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY order_id)::text FROM order_contact_snapshots c),'')||COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY id)::text FROM survey_submissions s),'')||COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM survey_submission_answers a),'')||COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id)::text FROM payments p),''))`
	if _, e = native.Exec(ctx, `INSERT INTO payments(order_id,provider,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,$3,$4,$4,9900,'CNY','paid',1,now(),now())`, first, fmt.Sprintf("canonical-phone-%d", a.CustomerID), a.IdentityID, a.CustomerID); e != nil {
		t.Fatal(e)
	}
	beforeSource := digest(sourceSQL)
	run("apply", "order", last)
	run("apply", "survey", maxAnswer)
	oldSQL := `SELECT md5(COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM identity_declared_phone_receipts r WHERE outcome='invalid'),'')||COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM survey_phone_binding_receipts r),''))`
	beforeOld := digest(oldSQL)
	inspection := run("inspect", "order", last)
	outcomes := inspection["outcomes"].(map[string]any)
	if outcomes["recoverable"] != float64(1) || outcomes["recoverable_conflict"] != float64(1) || outcomes["unaudited_alias"] != float64(1) || outcomes["original:attached"] != float64(1) {
		t.Fatalf("inspection=%v", outcomes)
	}
	run("recover-canonical", "order", last)
	run("recover-canonical", "survey", maxAnswer)
	if digest(sourceSQL) != beforeSource || digest(oldSQL) != beforeOld {
		t.Fatal("recovery changed source/financial/original receipts")
	}
	var count int
	var mask string
	if e = native.QueryRow(ctx, `SELECT count(*) FROM customer_identities WHERE customer_id=$1 AND kind='phone' AND assurance='declared'`, c.CustomerID).Scan(&count); e != nil || count != 2 {
		t.Fatalf("canonical phones=%d err=%v", count, e)
	}
	if e = native.QueryRow(ctx, `SELECT phone_masked FROM customer_directory_projection WHERE customer_id=$1`, c.CustomerID).Scan(&mask); e != nil || mask == "" {
		t.Fatal("canonical display missing", e)
	}
	allIdentitySQL := `SELECT md5(COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM identity_declared_phone_receipts r),'')||COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY original_key_digest)::text FROM identity_phone_canonical_recoveries r),'')||COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY id)::text FROM customer_identities i),'')||COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY customer_id)::text FROM customer_directory_projection p),''))`
	after := digest(allIdentitySQL)
	replay := run("recover-canonical", "order", last)["outcomes"].(map[string]any)
	if replay["replayed:recovery_attached"] != float64(1) || replay["replayed:recovery_conflict"] != float64(1) {
		t.Fatalf("repeat=%v", replay)
	}
	surveyReplay := run("recover-canonical", "survey", maxAnswer)["outcomes"].(map[string]any)
	if surveyReplay["unknown_source"] != float64(1) || surveyReplay["replayed:recovery_attached"] != float64(1) {
		t.Fatalf("survey repeat=%v", surveyReplay)
	}
	if after != digest(allIdentitySQL) || digest(sourceSQL) != beforeSource || digest(oldSQL) != beforeOld {
		t.Fatal("original source repeat mutated facts")
	}
	if e = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&count); e != nil || count != 0 {
		t.Fatal("Provider effect created", e)
	}
	// Rollback covers recovery receipt + audit + identity, not merely a source
	// snapshot. Original invalid survives the failed projection and retry.
	failed, _ := root("projection-failure")
	merge(failed, c, cf)
	id := seedOrder(failed.CustomerID, "13400000333")
	run("apply", "order", id)
	if _, e = native.Exec(ctx, `CREATE FUNCTION recovery_projection_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic projection failure'; END $$;CREATE TRIGGER recovery_projection_fail BEFORE INSERT OR UPDATE ON customer_directory_projection FOR EACH ROW EXECUTE FUNCTION recovery_projection_fail()`); e != nil {
		t.Fatal(e)
	}
	beforeFailure := digest(allIdentitySQL)
	beforeFailureSource := digest(sourceSQL)
	beforeFailureOld := digest(oldSQL)
	failedCommand := exec.CommandContext(ctx, binary, "--mode=recover-canonical", "--source=order", fmt.Sprintf("--through-id=%d", id))
	failedCommand.Env = append(os.Environ(), "AICRM_DATABASE_URL="+url, "AICRM_IDENTITY_PHONE_DATA_KEY="+key, "AICRM_ORDER_CONTACT_DATA_KEY="+key, "AICRM_SURVEY_DATA_KEY="+key)
	_, e = failedCommand.CombinedOutput()
	if e == nil || digest(allIdentitySQL) != beforeFailure {
		t.Fatal("recovery UoW left partial writes")
	}
	if _, e = native.Exec(ctx, `DROP TRIGGER recovery_projection_fail ON customer_directory_projection;DROP FUNCTION recovery_projection_fail()`); e != nil {
		t.Fatal(e)
	}
	run("recover-canonical", "order", id)
	if digest(sourceSQL) != beforeFailureSource || digest(oldSQL) != beforeFailureOld {
		t.Fatal("recovered retry changed source/financial/original facts")
	}
}
