package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
	"github.com/riverqueue/river"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPostgreSQLShopBuyerOneIDOrdersAndLateContact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, _ := postgres.Wrap(pool, time.Second)
	uow, _ := postgres.NewUnitOfWork(wrapped)
	workers := river.NewWorkers()
	if err = effects.NewModuleRegistration().RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	client, err := jobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	eer, err := effects.NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	cipher, _ := outbound.NewCommercePayloadAESGCM(key)
	vault, _ := identitysecure.NewPhoneVault(key)
	identityStore := identitystore.NewPostgresStore(vault)
	bridge := &shopBuyerIdentityBridge{}
	oneID := identityapp.OneIDService{Store: identityStore, VerifiedIdentity: bridge}
	queries := identityquery.NewPostgreSQL(vault)
	sends := 0
	var received [][]byte
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			w.WriteHeader(400)
			return
		}
		received = append(received, body)
		sends++
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	target := protocolRuntimeTarget(receiver.URL)
	targets := &commerceFundsPushTargets{target: target}
	push, err := outbound.NewCommercePushService(pool, uow, eer, commerceFundsProductConfigurationReader{repository: products}, queries, targets, cipher)
	if err != nil {
		t.Fatal(err)
	}
	sink, _ := outbound.NewCommercePushCompletionSink(push)
	if err = eer.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	provider, err := outbound.NewCommercePushProvider(true, push, targets, cipher)
	if err != nil {
		t.Fatal(err)
	}
	const scope = "wechat-open-platform:shared-platform"
	service := orderapp.ShopOrders{Store: orders, Products: products, Consumer: push, Cipher: cipher, Identity: oneID, Subjects: queries, BuyerKeys: identityStore, UnionScope: scope}
	bridge.Orders = service
	productPush, _ := productapp.NewCommerceExternalPushService(uow, products, push, commerceFundsProductPushStatuses{}, &commerceFundsProductPushEvents{})
	var product productport.ID
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		product, _, e = products.ImportShopSKUWithin(tx, productport.ShopSKU{AppID: "shop-app", ProductID: "parent", SKUID: "sku", Name: "课程", PriceMinor: 29900}, "buyer-fixture", time.Now())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	_, err = productPush.SaveExternalPushConfiguration(ctx, productport.SaveExternalPushConfigurationCommand{ProductID: product, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: target.Reference, Actor: 1, IdempotencyKey: "buyer-push-save-0001", BusinessParametersSet: true, PushType: "member_open"})
	if err != nil {
		t.Fatal(err)
	}
	fact := func(kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
		t.Helper()
		f, e := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: kind, Scope: scope, Value: value, Source: "provider-fixture"})
		if e != nil {
			t.Fatal(e)
		}
		return f
	}
	union := func(value string) identitydomain.VerifiedFact { return fact(identitydomain.KindUnionID, scope, value) }
	provision := func(f identitydomain.VerifiedFact) customerdomain.CustomerID {
		t.Helper()
		var id customerdomain.CustomerID
		e := uow.Within(ctx, func(tx context.Context) error {
			v, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, f)
			id = v.CustomerID
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	linkWithin := func(tx context.Context, id customerdomain.CustomerID, f identitydomain.VerifiedFact) error {
		r, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: id, Target: f, Evidence: identitydomain.LinkEvidence{Type: "provider_contact", Strength: identitydomain.EvidenceStrong, Source: "wecom", EventID: "contact-fixture", Digest: "fixture", PolicyVersion: "v1"}})
		if e == nil && r.Status != identityapp.LinkAttached && r.Status != identityapp.LinkAlreadyLinked {
			return fmt.Errorf("unexpected link result")
		}
		return e
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	source := func(id string, f identitydomain.VerifiedFact) orderport.ShopOrder {
		return orderport.ShopOrder{AppID: "shop-app", ID: id, TransactionID: "tx-" + id, CreatedAt: t0.Add(-time.Hour), PaidAt: t0, UpdatedAt: t0, PaidMinor: 29900, Phone: "13800138000", Nickname: "小店昵称", Buyer: fact(identitydomain.KindOAOpenID, "wechat-app:shop-app", "openid-"+id), BuyerUnion: f, Lines: []orderport.ShopOrderLine{{ProductID: "parent", SKUID: "sku", Name: "课程", UnitMinor: 29900, PaidMinor: 29900, Quantity: 1}}}
	}
	importOrder := func(v orderport.ShopOrder) error {
		return uow.Within(ctx, func(tx context.Context) error { return service.ImportShopOrderWithin(tx, v, t0) })
	}
	readBuyer := func(id string) (int64, string, int64) {
		t.Helper()
		var payer, order int64
		var state string
		e := pool.QueryRow(ctx, `SELECT COALESCE(o.payer_customer_id,0),f.buyer_association_state,o.id FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id WHERE o.merchant_order_no=$1`, id).Scan(&payer, &state, &order)
		if e != nil {
			t.Fatal(e)
		}
		return payer, state, order
	}
	readFrozen := func(id string) []byte {
		t.Helper()
		var b []byte
		e := pool.QueryRow(ctx, `SELECT f.paid_snapshot::text||':'||encode(f.contact_ciphertext,'hex')||':'||encode(i.payload_digest,'hex')||':'||i.envelope_fingerprint||':'||i.effect_id::text FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id JOIN order_paid_events p ON p.order_id=o.id JOIN outbound_commerce_push_intents i ON i.order_paid_event_id=p.id WHERE o.merchant_order_no=$1`, id).Scan(&b)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}

	t.Run("order first then WeCom with accepted send", func(t *testing.T) {
		v := source("3739797044778671872", union("union-late-contact"))
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		payer, state, orderID := readBuyer(v.ID)
		if payer != 0 || state != "pending" {
			t.Fatal("Shop implicitly created/guessed a customer")
		}
		var effect, generation, job int64
		if err = pool.QueryRow(ctx, `SELECT e.id,e.generation,j.river_job_id FROM external_effects e JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation`).Scan(&effect, &generation, &job); err != nil {
			t.Fatal(err)
		}
		if err = eer.RunAttempt(ctx, effect, generation, job, provider); err != nil {
			t.Fatal(err)
		}
		before := readFrozen(v.ID)
		id := provision(fact(identitydomain.KindWeComExternalUserID, "wecom-corp:fixture", "external-late-contact"))
		abort := errors.New("rollback buyer association")
		err = uow.Within(ctx, func(tx context.Context) error {
			if e := linkWithin(tx, id, v.BuyerUnion); e != nil {
				return e
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatal(err)
		}
		payer, state, _ = readBuyer(v.ID)
		if payer != 0 || state != "pending" {
			t.Fatal("association escaped rollback")
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			r, e := oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: scope, Value: v.BuyerUnion.Reference().NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: "provider-fixture"})
			if e == nil && r.Status != identityport.ResolveNotFound {
				return errors.New("identity escaped rollback")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error { return linkWithin(tx, id, v.BuyerUnion) }); err != nil {
			t.Fatal(err)
		}
		payer, state, _ = readBuyer(v.ID)
		if payer != int64(id) || state != "linked" {
			t.Fatal("late WeCom did not attribute pending order")
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- importOrder(v) }()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		// Official refund/detail responses can omit optional buyer fields.
		missing := v
		missing.Phone = ""
		missing.Nickname = ""
		missing.Buyer = identitydomain.VerifiedFact{}
		missing.BuyerUnion = identitydomain.VerifiedFact{}
		missing.RefundedMinor = 29900
		if err = importOrder(missing); err != nil {
			t.Fatal(err)
		}
		displays, e := service.ReadShopBuyerDisplays(ctx, []int64{orderID})
		if e != nil || displays[orderID].PhoneMasked != "138****8000" || displays[orderID].Nickname != "小店昵称" {
			t.Fatal("optional response erased contact", e)
		}
		if !bytes.Equal(before, readFrozen(v.ID)) {
			t.Fatal("buyer correction rewrote accepted payload or paid facts")
		}
		if err = eer.RunAttempt(ctx, effect, generation, job, provider); err != nil {
			t.Fatal(err)
		}
		if sends != 1 {
			t.Fatal("buyer correction sent twice")
		}
		var audits int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM order_audit_events WHERE order_id=$1 AND event_type='order.shop_buyer_linked'`, orderID).Scan(&audits); err != nil || audits != 1 {
			t.Fatal("association audit replay/rollback failed", err)
		}
	})
	t.Run("customer first", func(t *testing.T) {
		f := union("union-existing-customer")
		id := provision(f)
		v := source("customer-first", f)
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		payer, state, _ := readBuyer(v.ID)
		if payer != int64(id) || state != "linked" {
			t.Fatal("verified UnionID not reused")
		}
	})
	t.Run("conflicting OpenID and UnionID", func(t *testing.T) {
		f := union("union-other-root")
		provision(f)
		v := source("cross-root", f)
		provision(v.Buyer)
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		payer, state, _ := readBuyer(v.ID)
		if payer != 0 || state != "conflict" {
			t.Fatal("cross-root buyer was attributed")
		}
	})
	t.Run("confirmed OneID merge preserves paid dispatch", func(t *testing.T) {
		f := union("union-confirmed-merge")
		originalCustomer := provision(f)
		v := source("confirmed-merge-order", f)
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		before := readFrozen(v.ID)
		canonical := provision(fact(identitydomain.KindWeComExternalUserID, "wecom-corp:fixture", "external-confirmed-merge"))
		if err = uow.Within(ctx, func(tx context.Context) error {
			candidate, e := oneID.LinkVerifiedIdentity(tx, identityapp.LinkCommand{SourceCustomerID: canonical, Target: f, Evidence: identitydomain.LinkEvidence{Type: "provider_contact", Strength: identitydomain.EvidenceStrong, Source: "wecom", EventID: "merge-fixture", Digest: "fixture", PolicyVersion: "v1"}})
			if e != nil {
				return e
			}
			if candidate.Candidate == nil {
				return errors.New("missing explicit merge candidate")
			}
			result, e := oneID.ConfirmMerge(tx, identityapp.ConfirmMergeCommand{CandidateID: candidate.Candidate.ID, SurvivorCustomerID: canonical, Operator: "fixture-admin"})
			if e == nil && result.Status != identityapp.LinkMerged {
				return errors.New("OneID merge rejected")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err = importOrder(v); err != nil {
			t.Fatal("legal OneID lineage blocked daily detail refresh", err)
		}
		payer, state, _ := readBuyer(v.ID)
		if payer != int64(canonical) || payer == int64(originalCustomer) || state != "linked" {
			t.Fatal("buyer did not follow trusted canonical lineage")
		}
		if !bytes.Equal(before, readFrozen(v.ID)) {
			t.Fatal("canonical association rewrote paid/dispatch facts")
		}
	})
	t.Run("wrong platform", func(t *testing.T) {
		v := source("cross-platform", fact(identitydomain.KindUnionID, "wechat-open-platform:other", "union-late-contact"))
		if !errors.Is(importOrder(v), orderport.ErrConflict) {
			t.Fatal("foreign platform accepted")
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE merchant_order_no=$1`, v.ID).Scan(&n)
		if n != 0 {
			t.Fatal("rejected identity committed order")
		}
	})
	t.Run("late OpenID peer conflict", func(t *testing.T) {
		v := source("late-peer-conflict", union("union-late-peer-conflict"))
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		provision(v.Buyer)
		provision(v.BuyerUnion)
		payer, state, _ := readBuyer(v.ID)
		if payer != 0 || state != "conflict" {
			t.Fatal("observer ignored another OpenID owner")
		}
	})
	t.Run("metadata failure rolls back identity", func(t *testing.T) {
		v := source("tampered-metadata", union("union-tampered-metadata"))
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		_, _, orderID := readBuyer(v.ID)
		var original []byte
		if err = pool.QueryRow(ctx, `SELECT buyer_details_ciphertext FROM order_wechat_shop_facts WHERE order_id=$1`, orderID).Scan(&original); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_details_ciphertext=$2 WHERE order_id=$1`, orderID, []byte("invalid fixture cipher")); err != nil {
			t.Fatal(err)
		}
		err = uow.Within(ctx, func(tx context.Context) error {
			_, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, v.BuyerUnion)
			return e
		})
		if !errors.Is(err, orderport.ErrUnavailable) {
			t.Fatal("corrupt metadata did not abort attribution", err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			r, e := oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: scope, Value: v.BuyerUnion.Reference().NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: "provider-fixture"})
			if e == nil && r.Status != identityport.ResolveNotFound {
				return errors.New("observer failure committed identity")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE order_wechat_shop_facts SET buyer_details_ciphertext=$2 WHERE order_id=$1`, orderID, original); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing contact still sends", func(t *testing.T) {
		v := source("empty-contact", identitydomain.VerifiedFact{})
		v.Phone = ""
		v.Nickname = ""
		v.Buyer = identitydomain.VerifiedFact{}
		if err = importOrder(v); err != nil {
			t.Fatal(err)
		}
		var n int
		err = pool.QueryRow(ctx, `SELECT count(*) FROM outbound_commerce_push_intents i JOIN order_paid_events p ON p.id=i.order_paid_event_id JOIN orders o ON o.id=p.order_id WHERE o.merchant_order_no=$1 AND i.effect_id IS NOT NULL`, v.ID).Scan(&n)
		if err != nil || n != 1 {
			t.Fatal("missing fields gated dispatch", err)
		}
	})
	t.Run("concurrent first purchase and UnionID provision", func(t *testing.T) {
		for i := range 4 {
			f := union(fmt.Sprintf("union-concurrent-%d", i))
			v := source(fmt.Sprintf("concurrent-%d", i), f)
			start := make(chan struct{})
			errs := make(chan error, 2)
			var customer customerdomain.CustomerID
			go func() { <-start; errs <- importOrder(v) }()
			go func() {
				<-start
				errs <- uow.Within(ctx, func(tx context.Context) error {
					r, e := oneID.ProvisionCustomerFromVerifiedIdentity(tx, f)
					customer = r.CustomerID
					return e
				})
			}()
			close(start)
			for range 2 {
				if e := <-errs; e != nil {
					t.Fatal(e)
				}
			}
			payer, state, _ := readBuyer(v.ID)
			if payer != int64(customer) || state != "linked" {
				t.Fatal("contact/order race missed association")
			}
		}
	})
	t.Run("first send uses reread details and current verified buyer", func(t *testing.T) {
		originalTarget := targets.target
		defer func() { targets.target = originalTarget }()
		targets.target.Reference = "unavailable-target"
		v := source("reread-before-first-send", identitydomain.VerifiedFact{})
		v.Phone, v.Account = "", ""
		v.Buyer = identitydomain.VerifiedFact{}
		if e := importOrder(v); e != nil {
			t.Fatal(e)
		}
		frozenContact := func() []byte {
			t.Helper()
			var raw []byte
			if e := pool.QueryRow(ctx, `SELECT f.paid_snapshot::text||':'||encode(f.contact_ciphertext,'hex') FROM orders o JOIN order_wechat_shop_facts f ON f.order_id=o.id WHERE o.merchant_order_no=$1`, v.ID).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			return raw
		}
		originalContact := frozenContact()
		v.BuyerUnion = union("union-before-first-send")
		customer := provision(v.BuyerUnion)
		v.Phone, v.Account = "13900139000", "reread-account"
		if e := importOrder(v); e != nil {
			t.Fatal(e)
		}
		// A later official response can omit optional contacts. Previously read
		// details survive, even when the original contact snapshot was empty.
		v.Phone, v.Account = "", ""
		targets.target = originalTarget
		targets.target.BuyerUnionID.Scope = scope
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- importOrder(v) }()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		payer, state, orderID := readBuyer(v.ID)
		if payer != int64(customer) || state != "linked" || !bytes.Equal(originalContact, frozenContact()) {
			t.Fatal("current attribution or immutable paid/contact facts changed incorrectly")
		}
		var count int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_commerce_push_intents i JOIN order_paid_events p ON p.id=i.order_paid_event_id WHERE p.order_id=$1 AND i.effect_id IS NOT NULL`, orderID).Scan(&count); e != nil || count != 1 {
			t.Fatal("concurrent first send not unique", e)
		}
		frozenIntent := readFrozen(v.ID)
		// Once queued, a newer detail must not mutate the scheduled request.
		v.Phone, v.Account = "13700137000", "changed-after-queue"
		if e := importOrder(v); e != nil || !bytes.Equal(frozenIntent, readFrozen(v.ID)) {
			t.Fatal("queued payload was rewritten", e)
		}
		var effect, generation, job int64
		if e := pool.QueryRow(ctx, `SELECT e.id,e.generation,j.river_job_id FROM outbound_commerce_push_intents i JOIN order_paid_events p ON p.id=i.order_paid_event_id JOIN external_effects e ON i.effect_id='eer_'||e.id::text JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation WHERE p.order_id=$1`, orderID).Scan(&effect, &generation, &job); e != nil {
			t.Fatal(e)
		}
		beforeSends := sends
		if e := eer.RunAttempt(ctx, effect, generation, job, provider); e != nil || sends != beforeSends+1 {
			t.Fatal("first send did not reach receiver once", e)
		}
		var body struct {
			Phone   string `json:"phone_number"`
			Account string `json:"account"`
			Buyer   struct {
				UnionID string `json:"unionid"`
			} `json:"buyer"`
			Order struct {
				Paid int64 `json:"paid_amount"`
			} `json:"order"`
		}
		if e := json.Unmarshal(received[len(received)-1], &body); e != nil || body.Phone != "13900139000" || body.Account != "reread-account" || body.Buyer.UnionID != "union-before-first-send" || body.Order.Paid != 29900 {
			t.Fatal("receiver did not receive details read before first send", e)
		}
		if e := importOrder(v); e != nil || !bytes.Equal(frozenIntent, readFrozen(v.ID)) {
			t.Fatal("accepted payload changed", e)
		}
		if e := eer.RunAttempt(ctx, effect, generation, job, provider); e != nil || sends != beforeSends+1 {
			t.Fatal("accepted effect resent", e)
		}
	})
}
