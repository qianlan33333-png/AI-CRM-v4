package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type commercePhoneVaultSession struct {
	actors map[string]paymentport.SessionActor
}

func (s commercePhoneVaultSession) actor(token string) (paymentport.SessionActor, error) {
	actor, found := s.actors[token]
	if !found {
		return paymentport.SessionActor{}, paymentport.ErrSessionRequired
	}
	return actor, nil
}
func (s commercePhoneVaultSession) ConsumeWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return s.actor(token)
}
func (s commercePhoneVaultSession) LookupWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return s.actor(token)
}
func (s commercePhoneVaultSession) SelectPayerSelfWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	return s.actor(token)
}

type commercePhoneVaultConfiguration struct {
	value productport.ExternalPushConfiguration
}

func (c commercePhoneVaultConfiguration) ReadExternalPushConfigurationForOrder(_ context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if id != c.value.ProductID {
		return productport.ExternalPushConfiguration{}, productport.ErrProductReadNotFound
	}
	return c.value, nil
}

type commercePhoneVaultTargets struct{ target outbound.CommercePushTarget }

func (commercePhoneVaultTargets) CommercePushProviderEnabled() bool { return true }
func (t commercePhoneVaultTargets) CommercePushTarget(_ context.Context, reference string) (outbound.CommercePushTarget, bool, error) {
	if reference != t.target.Reference {
		return outbound.CommercePushTarget{}, false, nil
	}
	return t.target, true, nil
}

// TestPostgreSQLCommercePhoneVaultPaymentSettlement exercises the live
// callback-shaped UoW with Identity's actual PostgreSQL query and Phone Vault.
// A declared or ambiguous phone is never sent: the Order, Payment and callback
// receipt commit with a planned identity-unavailable intent. A verified vault
// fact retains the encrypted EER acceptance path.
func TestPostgreSQLCommercePhoneVaultPaymentSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = effects.NewModuleRegistration().RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	vault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	identities := identityquery.NewPostgreSQL(vault)
	target := outbound.CommercePushTarget{
		Reference: "phone-vault-target", Slot: "phone-vault-slot", Endpoint: "https://push.example.test/paid", Version: "v1",
		BuyerID:          outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:phone-vault"},
		BuyerOpenID:      outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:phone-vault"},
		BuyerUnionID:     outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:phone-vault"},
		BuyerPhone:       outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		BeneficiaryPhone: outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
	}
	if err = outbound.ValidateCommercePushTarget(target); err != nil {
		t.Fatal(err)
	}
	commerceCipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	commerce, err := outbound.NewCommercePushService(pool, uow, effectStore, commercePhoneVaultConfiguration{value: productport.ExternalPushConfiguration{ProductID: 20, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: target.Reference, Revision: 1, UpdatedAt: time.Now().UTC()}}, identities, commercePhoneVaultTargets{target: target}, commerceCipher)
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetPaidEventConsumer(commerce); err != nil {
		t.Fatal(err)
	}
	sessions := commercePhoneVaultSession{actors: map[string]paymentport.SessionActor{}}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderService, sessions, effectStore, effectStore)

	cases := []struct {
		name              string
		assurances        []identitydomain.Assurance
		storePhoneSecrets bool
		wantIntent        string
		wantEffectID      bool
	}{
		{name: "declared_phone_is_planned", assurances: []identitydomain.Assurance{identitydomain.AssuranceDeclared}, storePhoneSecrets: true, wantIntent: "planned_identity_unavailable"},
		{name: "verified_phone_queues_encrypted_effect", assurances: []identitydomain.Assurance{identitydomain.AssuranceVerified}, storePhoneSecrets: true, wantIntent: "queued", wantEffectID: true},
		{name: "multiple_verified_phones_are_planned", assurances: []identitydomain.Assurance{identitydomain.AssuranceVerified, identitydomain.AssuranceVerified}, storePhoneSecrets: true, wantIntent: "planned_identity_unavailable"},
		{name: "verified_phone_without_vault_ciphertext_is_planned", assurances: []identitydomain.Assurance{identitydomain.AssuranceVerified}, wantIntent: "planned_identity_unavailable"},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			customerID, payerIdentityID := commercePhoneVaultCustomer(t, ctx, pool, vault, testCase.assurances, testCase.storePhoneSecrets, index)
			token := fmt.Sprintf("commerce-phone-vault-session-%02d", index)
			sessions.actors[token] = paymentport.SessionActor{PayerIdentityID: payerIdentityID, PayerCustomerID: customerID, BeneficiaryCustomerID: customerID, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted, Channel: paymentdomain.ChannelMiniProgram}
			productID := int64(20)
			order, createErr := orderService.Create(ctx, orderport.CreateCommand{Actor: 1, IdempotencyKey: fmt.Sprintf("commerce-phone-order-create-%02d", index), Input: orderdomain.NewOrderInput{Provider: orderdomain.ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: fmt.Sprintf("commerce-phone-source-%02d", index), MerchantOrderNo: fmt.Sprintf("commerce-phone-order-%02d", index), PayerCustomerID: &customerID, BeneficiaryCustomerID: &customerID, Amount: orderdomain.Money{AmountMinor: 29900, Currency: "CNY"}, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productID, ProductCode: "commerce-phone-product", ProductName: "电话金库商品", Quantity: 1, UnitAmountMinor: 29900, LineAmountMinor: 29900}}, RecordOrigin: orderdomain.RecordOriginNative}})
			if createErr != nil {
				t.Fatal(createErr)
			}
			payment, createErr := paymentService.Create(ctx, paymentport.CreateCommand{OrderID: order.ID, SessionToken: token, CheckoutSessionBinding: paymentport.CheckoutSessionBinding(token), ActorScope: "commerce-phone-payment", IdempotencyKey: fmt.Sprintf("commerce-phone-payment-create-%02d", index)})
			if createErr != nil {
				t.Fatal(createErr)
			}
			// Payment.Settle correctly rejects Provider facts older than the
			// freshly-created payment. Keep this callback-shaped fixture after
			// its local checkout while retaining non-zero sub-microsecond input.
			at := time.Now().UTC().Add(time.Second).Add(time.Duration(index) * time.Nanosecond).Add(789 * time.Nanosecond)
			eventDigest := sha256.Sum256([]byte("commerce-phone-event-" + testCase.name))
			bodyDigest := sha256.Sum256([]byte("commerce-phone-body-" + testCase.name))
			callbackErr := paymentService.ApplyVerifiedCallback(ctx, paymentprovider.CallbackResult{Kind: "payment", AppID: "fixture-app", MerchantOrderNo: payment.MerchantOrderNo, ProviderTransactionReference: fmt.Sprintf("commerce-phone-transaction-%02d", index), ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", fmt.Sprintf("commerce-phone-transaction-%02d", index))), AmountMinor: 29900, Currency: "CNY", OccurredAt: at, EventDigest: eventDigest, BodyDigest: bodyDigest})
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			var paymentStatus, orderStatus, intentState, effectID string
			var receiptCount int
			if err = pool.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, payment.ID).Scan(&paymentStatus); err != nil || paymentStatus != "paid" {
				t.Fatalf("payment status=%q err=%v", paymentStatus, err)
			}
			if err = pool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, order.ID).Scan(&orderStatus); err != nil || orderStatus != "paid" {
				t.Fatalf("order status=%q err=%v", orderStatus, err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_callback_receipts WHERE payment_id=$1`, payment.ID).Scan(&receiptCount); err != nil || receiptCount != 1 {
				t.Fatalf("callback receipts=%d err=%v", receiptCount, err)
			}
			if err = pool.QueryRow(ctx, `SELECT i.state,COALESCE(i.effect_id,'') FROM outbound_commerce_push_intents i JOIN order_paid_events e ON e.id=i.order_paid_event_id WHERE e.order_id=$1`, order.ID).Scan(&intentState, &effectID); err != nil {
				t.Fatal(err)
			}
			if intentState != testCase.wantIntent || (effectID != "") != testCase.wantEffectID {
				t.Fatalf("commerce intent state=%q effect=%t want state=%q effect=%t", intentState, effectID != "", testCase.wantIntent, testCase.wantEffectID)
			}
		})
	}
	t.Run("merged_lower_id_reads_only_the_canonical_root_phone", func(t *testing.T) {
		var mergedID int64
		if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&mergedID); err != nil {
			t.Fatal(err)
		}
		rootID, _ := commercePhoneVaultCustomer(t, ctx, pool, vault, []identitydomain.Assurance{identitydomain.AssuranceVerified}, true, 50)
		if rootID <= mergedID {
			t.Fatalf("fixture IDs do not establish lower merged ID: merged=%d root=%d", mergedID, rootID)
		}
		if _, err = pool.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP WHERE id=$1`, mergedID, rootID); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			_, found, readErr := identities.VerifiedOutboundPhone(tx, customerdomain.CustomerID(mergedID), "phone:cn11")
			if readErr != nil {
				return readErr
			}
			if !found {
				return errors.New("canonical root phone was not found")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("corrupt_vault_ciphertext_is_an_error_not_identity_absence", func(t *testing.T) {
		customerID, _ := commercePhoneVaultCustomer(t, ctx, pool, vault, []identitydomain.Assurance{identitydomain.AssuranceVerified}, true, 51)
		// Preserve the storage invariant (at least nonce+tag length) while
		// making an authentication-invalid ciphertext for the real Vault read.
		if _, err = pool.Exec(ctx, `UPDATE identity_phone_secrets p SET ciphertext=$2 FROM customer_identities i WHERE p.identity_id=i.id AND i.customer_id=$1 AND i.kind='phone' AND i.scope_key='phone:cn11'`, customerID, bytes.Repeat([]byte{0}, 39)); err != nil {
			t.Fatal(err)
		}
		if err = uow.Within(ctx, func(tx context.Context) error {
			_, _, readErr := identities.VerifiedOutboundPhone(tx, customerdomain.CustomerID(customerID), "phone:cn11")
			if readErr == nil {
				return errors.New("corrupt phone ciphertext was treated as identity absence")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, readErr := identities.VerifiedExternalIdentityValue(tx, 1, identitydomain.KindPhone, "phone:cn11")
		if !errors.Is(readErr, identityquery.ErrInvalidQuery) {
			return fmt.Errorf("generic phone reader error=%v", readErr)
		}
		_, _, readErr = identities.VerifiedOutboundPhone(tx, 1, "phone:e164")
		if !errors.Is(readErr, identityquery.ErrInvalidQuery) {
			return fmt.Errorf("outbound phone scope error=%v", readErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = identityquery.NewPostgreSQL().VerifiedOutboundPhone(ctx, 1, "phone:cn11"); err == nil {
		t.Fatal("missing Vault was treated as an unavailable identity")
	}
}

func commercePhoneVaultCustomer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, vault *identitysecure.PhoneVault, assurances []identitydomain.Assurance, storePhoneSecrets bool, suffix int) (int64, int64) {
	t.Helper()
	var customerID, payerIdentityID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	insertIdentity := func(kind, scope, value string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES($1,$2,$3,$4,'verified','commerce_phone_vault_test',1,'active',CURRENT_TIMESTAMP) RETURNING id`, customerID, kind, scope, value).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	payerIdentityID = insertIdentity("mp_openid", "wechat-app:phone-vault", fmt.Sprintf("fixture-openid-%02d", suffix))
	_ = insertIdentity("wecom_external_userid", "wecom-corp:phone-vault", fmt.Sprintf("fixture-external-%02d", suffix))
	_ = insertIdentity("unionid", "wechat-open-platform:phone-vault", fmt.Sprintf("fixture-union-%02d", suffix))
	for phoneIndex, assurance := range assurances {
		phone := fmt.Sprintf("13800138%03d", suffix*10+phoneIndex)
		ciphertext, err := vault.Encrypt(phone)
		if err != nil {
			t.Fatal(err)
		}
		digest := vault.LookupDigest(phone)
		var identityID int64
		var verifiedAt any
		if assurance == identitydomain.AssuranceVerified {
			verifiedAt = time.Now().UTC()
		}
		if err = pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,normalized_value_digest,assurance,source,normalizer_version,status,verified_at) VALUES($1,'phone','phone:cn11','',$2,$3,'commerce_phone_vault_test',1,'active',$4) RETURNING id`, customerID, digest[:], string(assurance), verifiedAt).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		if storePhoneSecrets {
			if _, err = pool.Exec(ctx, `INSERT INTO identity_phone_secrets(identity_id,ciphertext,masked_value,key_version) VALUES($1,$2,$3,$4)`, identityID, ciphertext, identitysecure.MaskPhone(phone), identitysecure.PhoneKeyVersion); err != nil {
				t.Fatal(err)
			}
		}
	}
	return customerID, payerIdentityID
}
