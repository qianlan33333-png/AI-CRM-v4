package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
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
	"strings"
	"sync"
	"testing"
	"time"
)

type failingContactWriter struct {
	customerport.ContactReader
	customerport.ContactWriter
}

func (w failingContactWriter) UpdateContactWithin(ctx context.Context, c customerport.ContactUpdate) error {
	if err := w.ContactWriter.UpdateContactWithin(ctx, c); err != nil {
		return err
	}
	return errors.New("injected contact failure")
}

func TestPostgreSQLCustomerContactReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wrapped, err := platformpostgres.Wrap(db, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	cipher, _ := surveysecure.NewCipher(key)
	contacts, err := customerapp.NewContactService(customerstore.NewPostgreSQL(), cipher, identityquery.PostgreSQL{})
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(db, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, orders)
	service.SetContactProfiles(contacts)
	oc, _ := ordersecure.NewContactCipher(key)
	if err = service.SetContactCipher(oc); err != nil {
		t.Fatal(err)
	}
	if err = service.SetCollectedPhoneRecorder(collectedPhoneTestRecorder(t)); err != nil {
		t.Fatal(err)
	}
	customer := func() int64 {
		var id int64
		if e := db.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	read := func(id int64) customerport.ContactProfile {
		var v customerport.ContactProfile
		e := uow.Within(ctx, func(tx context.Context) error {
			var e error
			v, e = contacts.ReadContactWithin(tx, customerdomain.CustomerID(id), true)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	write := func(c customerport.ContactUpdate) error {
		return uow.Within(ctx, func(tx context.Context) error { return contacts.UpdateContactWithin(tx, c) })
	}
	addr := orderport.ShippingAddress{RecipientName: "收件测试", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "测试路 1 号"}
	command := func(id int64, key, phone string) orderport.PaymentOrderCommand {
		return orderport.PaymentOrderCommand{Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: key, PayerCustomerID: id, BeneficiaryCustomerID: id, ProductID: 9, ProductCode: "contact-fixture", ProductName: "Contact fixture", ProductVersion: 1, ProductType: "standard_product", UnitAmountMinor: 9900, Currency: "CNY", MobileE164: phone, ContactCollectionLevel: "mobile", ActorScope: "customer-contact-fixture", IdempotencyKey: "fixture-" + key}
	}
	create := func(c orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
		var v orderdomain.Snapshot
		e := uow.Within(ctx, func(tx context.Context) error { var e error; v, e = service.CreatePaymentOrderWithin(tx, c); return e })
		return v, e
	}
	t.Run("phone_preserves_address_replay_and_old_snapshot", func(t *testing.T) {
		id := customer()
		a := command(id, "contact-order-A", "+8613800000001")
		a.ContactCollectionLevel = "shipping_address"
		a.ShippingAddress = addr
		old, e := create(a)
		if e != nil {
			t.Fatal(e)
		}
		b := command(id, "contact-order-B", "+8613800000002")
		if _, e = create(b); e != nil {
			t.Fatal(e)
		}
		replay, e := create(a)
		if e != nil || replay.ID != old.ID {
			t.Fatal("replay", e)
		}
		v := read(id)
		if v.MobileE164 != b.MobileE164 || v.Shipping == nil || v.Shipping.DetailAddress != addr.DetailAddress || v.Shipping.MobileE164 != b.MobileE164 {
			t.Fatal("phone-only or replay lost latest contact")
		}
		e = uow.Within(ctx, func(tx context.Context) error {
			p, found, e := service.ReadCheckoutMobileWithin(tx, old.ID)
			if e != nil || !found || p != a.MobileE164 {
				return errors.New("historical order was rewritten")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		a.IdempotencyKey = "fixture-partial-address"
		a.MerchantOrderNo = "partial-address"
		a.ShippingAddress.DetailAddress = ""
		if _, e = create(a); e == nil {
			t.Fatal("partial address accepted")
		}
		if read(id).MobileE164 != b.MobileE164 {
			t.Fatal("invalid write mutated contact")
		}
		other := customer()
		if _, e = create(command(other, "contact-shared-phone", b.MobileE164)); e != nil {
			t.Fatal(e)
		}
		if read(other).MobileE164 != b.MobileE164 {
			t.Fatal("shared phone not saved")
		}
		var count int
		if e = db.QueryRow(ctx, `SELECT count(*) FROM customer_identities WHERE customer_id=$1 AND kind='phone'`, other).Scan(&count); e != nil || count != 0 {
			t.Fatal("shared contact transferred identity", e)
		}
		var stored []byte
		if e = db.QueryRow(ctx, `SELECT shipping_ciphertext FROM customer_contact_profiles WHERE customer_id=$1`, id).Scan(&stored); e != nil {
			t.Fatal(e)
		}
		raw, e := cipher.Decrypt(stored)
		if e != nil || strings.Contains(raw, b.MobileE164) || strings.Contains(raw, a.MobileE164) {
			t.Fatal("shipping duplicates the phone")
		}
	})
	t.Run("source_and_profile_rollback_together", func(t *testing.T) {
		id := customer()
		service.SetContactProfiles(failingContactWriter{ContactWriter: contacts})
		defer service.SetContactProfiles(contacts)
		if _, e := create(command(id, "contact-rollback", "+8613800000003")); e == nil {
			t.Fatal("injected failure accepted")
		}
		var n int
		if e := db.QueryRow(ctx, `SELECT count(*) FROM customer_contact_profiles WHERE customer_id=$1`, id).Scan(&n); e != nil || n != 0 {
			t.Fatal("contact survived rollback", e)
		}
		if e := db.QueryRow(ctx, `SELECT count(*) FROM orders WHERE merchant_order_no='contact-rollback'`).Scan(&n); e != nil || n != 0 {
			t.Fatal("order survived rollback", e)
		}
	})
	t.Run("concurrent_groups_and_history_never_overwrite_live", func(t *testing.T) {
		id := customer()
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		live := customerport.ContactUpdate{CustomerID: customerdomain.CustomerID(id), MobileE164: "+8613800000004", Source: "live"}
		go func() {
			done <- uow.Within(ctx, func(tx context.Context) error {
				if e := contacts.UpdateContactWithin(tx, live); e != nil {
					return e
				}
				close(entered)
				<-release
				return nil
			})
		}()
		<-entered
		historical := live
		historical.MobileE164 = "+8613800000005"
		historical.Source = "history"
		historical.OnlyIfEmpty = true
		historical.SourceAt = time.Now().Add(-time.Hour)
		waiting := make(chan error, 1)
		go func() { waiting <- write(historical) }()
		close(release)
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		if e := <-waiting; e != nil {
			t.Fatal(e)
		}
		if read(id).MobileE164 != live.MobileE164 {
			t.Fatal("history overwrote live")
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				p := fmt.Sprintf("+86139000000%02d", i)
				a := customerport.ShippingContact{MobileE164: p, RecipientName: p, ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: p}
				errs <- write(customerport.ContactUpdate{CustomerID: customerdomain.CustomerID(id), MobileE164: p, Shipping: &a, Source: fmt.Sprintf("parallel:%d", i)})
			}(i)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		v := read(id)
		if v.Shipping == nil || v.Shipping.DetailAddress != v.MobileE164 || v.Shipping.RecipientName != v.MobileE164 {
			t.Fatal("torn contact group")
		}
	})

	t.Run("merge_then_split_never_copies_contact_facts", func(t *testing.T) {
		a, b := customer(), customer()
		if e := write(customerport.ContactUpdate{CustomerID: customerdomain.CustomerID(a), MobileE164: "+8613300000000", Source: "customer-a"}); e != nil {
			t.Fatal(e)
		}
		if e := write(customerport.ContactUpdate{CustomerID: customerdomain.CustomerID(b), MobileE164: "+8613200000000", Source: "customer-b"}); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=now(),version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, b, a); e != nil {
			t.Fatal(e)
		}
		if read(a).MobileE164 != "+8613200000000" {
			t.Fatal("merged lineage did not expose latest authorized contact")
		}
		if _, e := db.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1 WHERE id=$1`, b); e != nil {
			t.Fatal(e)
		}
		if read(a).MobileE164 != "+8613300000000" || read(b).MobileE164 != "+8613200000000" {
			t.Fatal("split leaked or copied another customer's contact")
		}
	})
	t.Run("mapped_surveys_both_modes_replay_and_rollback", func(t *testing.T) {
		repo, e := surveystore.NewPostgreSQL(db, uow, cipher)
		if e != nil {
			t.Fatal(e)
		}
		surveys := surveyapp.NewSubmissionService(uow, repo, cipher)
		surveys.BindContactProfiles(contacts)
		if e = surveys.BindCollectedPhone(collectedPhoneTestRecorder(t)); e != nil {
			t.Fatal(e)
		}
		actor := surveyJourneyGovernedActor(t, ctx, db, "contact-survey-fixture", "Contact fixture")
		for i, mode := range []string{"all_in_one", "one_by_one"} {
			var qid, version, qphone, qother int64
			slug := fmt.Sprintf("contact-survey-%d", i)
			e = db.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES($1,$1,'','survey',$2,$1,'published',$3,$3,now(),now()) RETURNING id`, slug, mode, actor).Scan(&qid)
			if e != nil {
				t.Fatal(e)
			}
			e = db.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey',$2,$3,'','{}',$4,FALSE,NULL,$5,now()) RETURNING id`, qid, mode, slug, make([]byte, 32), actor).Scan(&version)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, version, qid); e != nil {
				t.Fatal(e)
			}
			kind, target := "mobile", "contact_phone"
			if i == 1 {
				kind, target = "shipping_address", "shipping_contact"
			}
			e = db.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sidebar_profile_field,required,sort_order,validation) VALUES($1,$2,'联系资料',$3,true,0,'{}') RETURNING id`, version, kind, target).Scan(&qphone)
			if e != nil {
				t.Fatal(e)
			}
			e = db.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,required,sort_order,validation) VALUES($1,'mobile','推荐人',false,1,'{}') RETURNING id`, version).Scan(&qother)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(ctx, `UPDATE survey_definition_versions SET is_immutable=true,published_at=now() WHERE id=$1`, version); e != nil {
				t.Fatal(e)
			}
			id := customerdomain.CustomerID(customer())
			a := surveyport.SubmissionAnswer{QuestionID: surveyport.ID(qphone), TextValue: "13600000000"}
			if i == 1 {
				a.TextValue = ""
				a.ShippingAddress = &customerport.ShippingContact{MobileE164: "+8613600000000", RecipientName: "问卷收件人", ProvinceCode: "11", ProvinceName: "北京市", CityCode: "1101", CityName: "北京市", DistrictCode: "110101", DistrictName: "东城区", DetailAddress: "问卷地址"}
			}
			c := surveyport.SubmitCommand{Slug: slug, DefinitionVersion: 1, SubmissionKey: strings.Repeat(fmt.Sprint(i+1), 43), Identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &id, EvidenceDigest: strings.Repeat("c", 64)}, Answers: []surveyport.SubmissionAnswer{a, {QuestionID: surveyport.ID(qother), TextValue: "13700000000"}}}
			receipt, e := surveys.Submit(ctx, c)
			if e != nil {
				t.Fatal(e)
			}
			definition, e := surveys.ReadPublic(ctx, slug)
			if e != nil {
				t.Fatal(e)
			}
			prefill, e := surveys.ContactPrefill(ctx, definition, c.Identity)
			if e != nil {
				t.Fatal(e)
			}
			if len(prefill) != 1 || prefill[surveyport.ID(qphone)].QuestionID != surveyport.ID(qphone) {
				t.Fatal("prefill disclosed unconfigured question")
			}
			e = uow.Within(ctx, func(tx context.Context) error {
				_, e := surveys.ContactHistoryWithin(tx, 0, []surveyport.LegacyContactMapping{{DefinitionVersionID: surveyport.ID(version), QuestionID: surveyport.ID(qother), Target: "contact_phone"}})
				return e
			})
			if e == nil {
				t.Fatal("legacy mapping introduced second phone source")
			}
			if read(int64(id)).MobileE164 != "+8613600000000" {
				t.Fatal("unmapped recommender overwrote contact")
			}
			if e = write(customerport.ContactUpdate{CustomerID: id, MobileE164: "+8613500000000", Source: "newer"}); e != nil {
				t.Fatal(e)
			}
			again, e := surveys.Submit(ctx, c)
			if e != nil || again.SubmissionID != receipt.SubmissionID {
				t.Fatal("survey replay failed", e)
			}
			if read(int64(id)).MobileE164 != "+8613500000000" {
				t.Fatal("survey replay rolled back contact")
			}
			var facts []customerport.ContactHistory
			e = uow.Within(ctx, func(tx context.Context) error {
				var e error
				facts, e = surveys.ContactHistoryWithin(tx, 0, nil)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			hasMapped, hasSkip := false, false
			for _, f := range facts {
				if f.Update.CustomerID == id && f.Update.MobileE164 == "+8613600000000" {
					hasMapped = true
				}
				if f.SkipReason == "unmapped" {
					hasSkip = true
				}
			}
			if !hasMapped || !hasSkip {
				t.Fatal("history source mapping missing")
			}
			failedID := customerdomain.CustomerID(customer())
			c.Identity.CustomerID = &failedID
			c.SubmissionKey = strings.Repeat(fmt.Sprint(i+3), 43)
			surveys.BindContactProfiles(failingContactWriter{ContactReader: contacts, ContactWriter: contacts})
			if _, e = surveys.Submit(ctx, c); e == nil {
				t.Fatal("survey failure accepted")
			}
			surveys.BindContactProfiles(contacts)
			var n int
			if e = db.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE customer_id=$1`, failedID).Scan(&n); e != nil || n != 0 {
				t.Fatal("submission survived rollback", e)
			}
			if read(int64(failedID)).MobileE164 != "" {
				t.Fatal("contact survived survey rollback")
			}
		}
	})
}
