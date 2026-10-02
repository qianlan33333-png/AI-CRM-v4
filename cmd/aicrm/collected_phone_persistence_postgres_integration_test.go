package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLCollectedPhonePersistenceAndHistoricalReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	collector := collectedPhoneTestRecorder(t)
	orders, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	if err = orderService.SetCollectedPhoneRecorder(collector); err != nil {
		t.Fatal(err)
	}
	cipher, err := ordersecure.NewContactCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetContactCipher(cipher); err != nil {
		t.Fatal(err)
	}
	newCustomer := func() int64 {
		var id int64
		if e := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	checkPhone := func(customer int64, want int) {
		var n int
		var masked string
		if e := native.QueryRow(ctx, `SELECT count(*) FROM customer_identities WHERE customer_id=$1 AND kind='phone' AND status='active' AND assurance='declared' AND normalized_value=''`, customer).Scan(&n); e != nil || n != want {
			t.Fatalf("phone identities count=%d want=%d err=%v", n, want, e)
		}
		if want > 0 {
			if e := native.QueryRow(ctx, `SELECT phone_masked FROM customer_directory_projection WHERE customer_id=$1`, customer).Scan(&masked); e != nil || masked == "" {
				t.Fatalf("phone projection absent err=%v", e)
			}
		}
	}
	command := func(customer int64, key, phone string) orderport.PaymentOrderCommand {
		return orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: key, PayerCustomerID: customer, BeneficiaryCustomerID: customer, ProductID: 9, ProductCode: "phone-fixture", ProductName: "Collected phone fixture", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 9900, Currency: "CNY", MobileE164: phone, ContactCollectionLevel: "mobile", ActorScope: "collected-phone-fixture", IdempotencyKey: key}
	}
	create := func(c orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
		var v orderdomain.Snapshot
		e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			v, e = orderService.CreatePaymentOrderWithin(tx, c)
			return e
		})
		return v, e
	}

	t.Run("submitted_before_payment_and_both_collection_levels", func(t *testing.T) {
		for i, level := range []string{"mobile", "shipping_address"} {
			customer := newCustomer()
			c := command(customer, fmt.Sprintf("phone-live-contact-%d", i), fmt.Sprintf("+86138000000%02d", i))
			c.ContactCollectionLevel = level
			if level == "shipping_address" {
				c.ShippingAddress = orderport.ShippingAddress{RecipientName: "fixture", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "fixture street"}
			}
			order, e := create(c)
			if e != nil || order.Status != orderdomain.StatusPendingPayment {
				t.Fatalf("new pending order err=%v", e)
			}
			checkPhone(customer, 1)
			again, e := create(c)
			if e != nil || again.ID != order.ID {
				t.Fatal("checkout replay failed", e)
			}
			var result identityport.DeclaredAttachResult
			e = uow.Within(ctx, func(tx context.Context) error {
				var e error
				result, e = orderService.ReplayCollectedPhoneWithin(tx, order.ID)
				return e
			})
			if e != nil || result.Status != identityport.DeclaredReplayed || result.ReplayOf != identityport.DeclaredAttached {
				t.Fatalf("replay=%+v err=%v", result, e)
			}

			checkPhone(customer, 1)
			if i == 0 {
				_, e = orderService.ApplySettlement(ctx, orderport.SettlementCommand{OrderID: order.ID, ExpectedVersion: order.Version, Status: orderdomain.StatusPaymentFailed, OccurredAt: time.Now().Add(time.Second), ActorScope: "phone-retention-fixture", IdempotencyKey: "phone-failed-retention-fixture"})
				if e != nil {
					t.Fatal(e)
				}
				checkPhone(customer, 1)
			}
			// A later provider directory refresh with no phone cannot erase
			// submitted customer information.
			e = uow.Within(ctx, func(tx context.Context) error {
				return customerstore.NewPostgreSQL().UpsertDirectoryProjection(tx, customerport.DirectoryProjection{CustomerID: customerdomain.CustomerID(customer), CustomerStatus: "active", DisplayName: "phone fixture", OneIDLabel: fmt.Sprintf("CID-%d", customer), ActivationState: "active", Source: "wecom_directory_sync", SourceVersion: 2, LastSyncedAt: time.Now(), UpdatedAt: time.Now()})
			})
			if e != nil {
				t.Fatal(e)
			}
			checkPhone(customer, 1)
		}
	})

	t.Run("historical_all_states_and_current_profile_preserved", func(t *testing.T) {
		for i, status := range []string{"pending_payment", "paid", "payment_failed", "closed", "cancelled", "partially_refunded", "refunded"} {
			customer := newCustomer()
			phone := fmt.Sprintf("+86139000000%02d", i)
			raw, e := cipher.Encrypt(phone)
			if e != nil {
				t.Fatal(e)
			}
			var id int64
			refunded := int64(0)
			if status == "partially_refunded" {
				refunded = 100
			}
			if status == "refunded" {
				refunded = 9900
			}
			// Reproduce an older native Order whose immutable contact was stored but
			// whose original application never called OneID. No Product row is needed.
			e = native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','v3-checkout',$1,$1,$2,$2,9900,$3,'CNY',$4,'native',TRUE,now(),now()) RETURNING id`, fmt.Sprintf("phone-history-%d", i), customer, refunded, status).Scan(&id)
			if e != nil {
				t.Fatal(e)
			}
			_, e = native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,'phone-fixture','Historical phone fixture',9900,1,9900)`, id)
			if e != nil {
				t.Fatal(e)
			}
			e = uow.Within(ctx, func(tx context.Context) error { return orders.InsertContactSnapshot(tx, id, raw, 1, time.Now()) })
			if e != nil {
				t.Fatal(e)
			}
			// Existing presentation may be newer or verified. Historic collection
			// must add a phone identity without changing that primary presentation.
			e = uow.Within(ctx, func(tx context.Context) error {
				return customerstore.NewPostgreSQL().UpdateDirectoryPhone(tx, customerdomain.CustomerID(customer), "137****0000", "verified", 1, time.Now())
			})
			if e != nil {
				t.Fatal(e)
			}
			var result identityport.DeclaredAttachResult
			e = uow.Within(ctx, func(tx context.Context) error {
				var e error
				result, e = orderService.ReplayCollectedPhoneWithin(tx, id)
				return e
			})
			if e != nil || result.Status != identityport.DeclaredAttached {
				t.Fatalf("state=%s result=%+v err=%v", status, result, e)
			}
			checkPhone(customer, 1)
			var masked, assurance string
			e = native.QueryRow(ctx, `SELECT phone_masked,phone_assurance FROM customer_directory_projection WHERE customer_id=$1`, customer).Scan(&masked, &assurance)
			if e != nil || masked != "137****0000" || assurance != "verified" {
				t.Fatal("history overwrote primary phone", e)
			}
			e = uow.Within(ctx, func(tx context.Context) error {
				ids, e := orderService.CollectedPhoneOrderIDsWithin(tx, id-1, id)
				if e == nil && (len(ids) != 1 || ids[0] != id) {
					t.Fatal("state filtered source cursor")
				}
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
		}
	})

	t.Run("projection_failure_rolls_back_source_identity_and_receipt", func(t *testing.T) {
		customer := newCustomer()
		_, e := native.Exec(ctx, `CREATE FUNCTION phone_projection_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected projection failure'; END $$; CREATE TRIGGER phone_projection_failure BEFORE INSERT OR UPDATE ON customer_directory_projection FOR EACH ROW EXECUTE FUNCTION phone_projection_failure()`)
		if e != nil {
			t.Fatal(e)
		}
		_, e = create(command(customer, "phone-atomic-rollback-key", "+8613600000000"))
		if e == nil {
			t.Fatal("failed projection committed")
		}
		for _, query := range []string{`SELECT count(*) FROM orders WHERE merchant_order_no='phone-atomic-rollback-key'`, `SELECT count(*) FROM customer_identities WHERE customer_id=$1`, `SELECT count(*) FROM identity_declared_phone_receipts WHERE customer_id=$1`} {
			var count int
			var e error
			if strings.Contains(query, "$1") {
				e = native.QueryRow(ctx, query, customer).Scan(&count)
			} else {
				e = native.QueryRow(ctx, query).Scan(&count)
			}
			if e != nil || count != 0 {
				t.Fatalf("half-committed data count=%d err=%v", count, e)
			}
		}
		_, e = native.Exec(ctx, `DROP TRIGGER phone_projection_failure ON customer_directory_projection; DROP FUNCTION phone_projection_failure()`)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = create(command(customer, "phone-atomic-rollback-key", "+8613600000000")); e != nil {
			t.Fatal("original-key recovery failed", e)
		}
		checkPhone(customer, 1)
	})

	t.Run("survey_multiple_phone_questions_and_disabled_historical_replay", func(t *testing.T) {
		key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
		sc, e := surveysecure.NewCipher(key)
		if e != nil {
			t.Fatal(e)
		}
		store, e := surveystore.NewPostgreSQL(native, uow, sc)
		if e != nil {
			t.Fatal(e)
		}
		service := surveyapp.NewSubmissionService(uow, store, sc)
		if e = service.BindCollectedPhone(collector); e != nil {
			t.Fatal(e)
		}
		var actor, qid, version, first, second, optional int64
		actor = surveyJourneyGovernedActor(t, ctx, native, "phone-survey-fixture", "Phone fixture")
		e = native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('phone fixture','phone fixture','','survey','all_in_one','phone-fixture','published',$1,$1,now(),now()) RETURNING id`, actor).Scan(&qid)
		if e != nil {
			t.Fatal(e)
		}
		e = native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','phone fixture','','{}',$2,FALSE,NULL,$3,now()) RETURNING id`, qid, make([]byte, 32), actor).Scan(&version)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, version, qid); e != nil {
			t.Fatal(e)
		}
		for i, id := range []*int64{&first, &second, &optional} {
			e = native.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,required,sort_order,validation) VALUES($1,'mobile',$2,$3,$4,'{}') RETURNING id`, version, fmt.Sprintf("mobile %d", i), i == 0, i).Scan(id)
			if e != nil {
				t.Fatal(e)
			}
		}
		if _, e = native.Exec(ctx, `UPDATE survey_definition_versions SET is_immutable=TRUE,published_at=now() WHERE id=$1`, version); e != nil {
			t.Fatal(e)
		}
		customer := customerdomain.CustomerID(newCustomer())
		command := surveyport.SubmitCommand{Slug: "phone-fixture", DefinitionVersion: 1, SubmissionKey: strings.Repeat("P", 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customer, EvidenceDigest: strings.Repeat("b", 64)}, Answers: []surveyport.SubmissionAnswer{{QuestionID: surveyport.ID(first), TextValue: "13500000000"}, {QuestionID: surveyport.ID(second), TextValue: "13400000000"}}}
		receipt, e := service.Submit(ctx, command)
		if e != nil {
			t.Fatal(e)
		}
		checkPhone(int64(customer), 2)
		if _, e = service.Submit(ctx, command); e != nil {
			t.Fatal("survey submit replay failed", e)
		}
		checkPhone(int64(customer), 2)
		if _, e = native.Exec(ctx, `UPDATE survey_questionnaires SET status='disabled' WHERE id=$1`, qid); e != nil {
			t.Fatal(e)
		}
		var ids []int64
		e = uow.Within(ctx, func(tx context.Context) error {
			var e error
			ids, e = service.CollectedPhoneAnswerIDsWithin(tx, 0, 0)
			return e
		})
		if e != nil || len(ids) != 2 {
			t.Fatalf("answers=%d err=%v", len(ids), e)
		}
		for _, id := range ids {
			e = uow.Within(ctx, func(tx context.Context) error {
				result, e := service.ReplayCollectedPhoneWithin(tx, id)
				if e == nil && (result.Status != identityport.DeclaredReplayed || result.ReplayOf != identityport.DeclaredAttached) {
					t.Fatal("bad existing answer replay")
				}
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
		}

		// Missing historical binding is repaired even after the questionnaire
		// is disabled. No submission/completion action is replayed.
		historicalCustomer := newCustomer()
		var historicSubmission, historicAnswer int64
		e = native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,'phone-fixture','old phone fixture','survey',now(),now()) RETURNING id`, qid, version, historicalCustomer, phoneFixtureDigest(8), phoneFixtureDigest(9)).Scan(&historicSubmission)
		if e != nil {
			t.Fatal(e)
		}
		encryptedHistory, e := sc.Encrypt("13300000000")
		if e != nil {
			t.Fatal(e)
		}
		e = native.QueryRow(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,text_value_ciphertext,text_value_masked,answer_digest,created_at) VALUES($1,'mobile','historical mobile',$2,'133****0000',$3,now()) RETURNING id`, historicSubmission, encryptedHistory, phoneFixtureDigest(10)).Scan(&historicAnswer)
		if e != nil {
			t.Fatal(e)
		}
		e = uow.Within(ctx, func(tx context.Context) error {
			result, e := service.ReplayCollectedPhoneWithin(tx, historicAnswer)
			if e == nil && result.Status != identityport.DeclaredAttached {
				t.Fatal("historical disabled answer did not bind")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		checkPhone(historicalCustomer, 1)
		var historyStatus string
		if e = native.QueryRow(ctx, `SELECT status FROM survey_phone_binding_receipts WHERE answer_id=$1`, historicAnswer).Scan(&historyStatus); e != nil || historyStatus != "attached" {
			t.Fatal("historical receipt missing", e)
		}
		// Unknown historical ownership remains unresolved; phone input must
		// never provision a Customer or choose a root by phone matching.
		var unresolvedSubmission, unresolvedAnswer int64
		e = native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,submitted_at,created_at) VALUES($1,$2,1,'unresolved',$3,$4,'phone-fixture','unresolved phone fixture','survey',now(),now()) RETURNING id`, qid, version, phoneFixtureDigest(11), phoneFixtureDigest(12)).Scan(&unresolvedSubmission)
		if e != nil {
			t.Fatal(e)
		}
		e = native.QueryRow(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,text_value_ciphertext,text_value_masked,answer_digest,created_at) VALUES($1,'mobile','unresolved mobile',$2,'133****0000',$3,now()) RETURNING id`, unresolvedSubmission, encryptedHistory, phoneFixtureDigest(13)).Scan(&unresolvedAnswer)
		if e != nil {
			t.Fatal(e)
		}
		e = uow.Within(ctx, func(tx context.Context) error {
			result, e := service.ReplayCollectedPhoneWithin(tx, unresolvedAnswer)
			if e == nil && result.Status != identityport.DeclaredInvalid {
				t.Fatal("unresolved answer guessed a customer")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		// A distinct user submits a phone already owned by the first user. Its
		// answer stays encrypted but neither first try nor replay projects ownership.
		if _, e = native.Exec(ctx, `UPDATE survey_questionnaires SET status='published' WHERE id=$1`, qid); e != nil {
			t.Fatal(e)
		}
		other := customerdomain.CustomerID(newCustomer())
		command.Identity.CustomerID = &other
		command.Identity.EvidenceDigest = strings.Repeat("c", 64)
		command.SubmissionKey = strings.Repeat("Q", 43)
		command.Answers = command.Answers[:1]
		conflict, e := service.Submit(ctx, command)
		if e != nil {
			t.Fatal(e)
		}
		checkPhone(int64(other), 0)
		var answer int64
		var encrypted []byte
		var status string
		e = native.QueryRow(ctx, `SELECT a.id,a.text_value_ciphertext,b.status FROM survey_submission_answers a JOIN survey_phone_binding_receipts b ON b.answer_id=a.id WHERE a.submission_id=$1`, conflict.SubmissionID).Scan(&answer, &encrypted, &status)
		if e != nil || status != "conflict" {
			t.Fatal("conflict original missing", e)
		}
		plain, e := sc.Decrypt(encrypted)
		if e != nil || plain != "13500000000" {
			t.Fatal("conflict lost collected value", e)
		}
		e = uow.Within(ctx, func(tx context.Context) error {
			result, e := service.ReplayCollectedPhoneWithin(tx, answer)
			if e == nil && (result.Status != identityport.DeclaredReplayed || result.ReplayOf != identityport.DeclaredConflict) {
				t.Fatal("conflict replay changed outcome")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		checkPhone(int64(other), 0)
		var projection int
		if e = native.QueryRow(ctx, `SELECT count(*) FROM customer_directory_projection WHERE customer_id=$1 AND phone_masked<>''`, other).Scan(&projection); e != nil || projection != 0 {
			t.Fatal("conflict replay projected another customer's phone", e)
		}
		var count int
		if e = native.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE id IN ($1,$2)`, receipt.SubmissionID, conflict.SubmissionID).Scan(&count); e != nil || count != 2 {
			t.Fatal("replay created submissions", e)
		}
	})

	t.Run("real_maintenance_command_inspect_apply_and_replay", func(t *testing.T) {
		binary := filepath.Join(t.TempDir(), "migrate-collected-phones")
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../migrate-collected-phones")
		if out, e := build.CombinedOutput(); e != nil {
			t.Fatalf("maintenance build: %v %s", e, out)
		}
		var before int
		if e := native.QueryRow(ctx, `SELECT count(*) FROM identity_declared_phone_receipts`).Scan(&before); e != nil {
			t.Fatal(e)
		}
		key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
		run := func(mode string) []map[string]any {
			c := exec.CommandContext(ctx, binary, "--mode="+mode, "--source=all")
			c.Env = append(os.Environ(), "AICRM_DATABASE_URL="+url, "AICRM_IDENTITY_PHONE_DATA_KEY="+key, "AICRM_ORDER_CONTACT_DATA_KEY="+key, "AICRM_SURVEY_DATA_KEY="+key)
			out, e := c.CombinedOutput()
			if e != nil {
				t.Fatalf("maintenance %s failed: %v %s", mode, e, out)
			}
			// The report exposes counts/source cursors, never collected values.
			if strings.Contains(string(out), "13800000000") || strings.Contains(string(out), "13500000000") {
				t.Fatal("maintenance exposed phone")
			}
			reports := []map[string]any{}
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				var row map[string]any
				if e = json.Unmarshal([]byte(line), &row); e != nil {
					t.Fatal(e)
				}
				reports = append(reports, row)
			}
			if len(reports) != 2 {
				t.Fatal("both source reports are required")
			}
			return reports
		}
		inspected := run("inspect")
		if inspected[0]["candidates"].(float64) < 9 || inspected[1]["candidates"].(float64) < 4 {
			t.Fatal("inspection omitted source facts")
		}
		var after int
		if e := native.QueryRow(ctx, `SELECT count(*) FROM identity_declared_phone_receipts`).Scan(&after); e != nil || after != before {
			t.Fatal("inspect persisted bindings", e)
		}
		run("apply")
		run("apply")
		if e := native.QueryRow(ctx, `SELECT count(*) FROM identity_declared_phone_receipts`).Scan(&after); e != nil || after != before {
			t.Fatal("repair replay wrote duplicate receipt", e)
		}
	})
	// Repair methods have no external-effect or delivery capability.
	var effects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("repair created external effect count=%d err=%v", effects, err)
	}
}

func phoneFixtureDigest(seed byte) []byte { value := make([]byte, 32); value[0] = seed; return value }
