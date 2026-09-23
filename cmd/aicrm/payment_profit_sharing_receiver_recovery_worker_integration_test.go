package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type profitSharingRecoveryWorkerIdentityReader struct {
	value identityport.VerifiedCommerceIdentity
}

func (reader profitSharingRecoveryWorkerIdentityReader) VerifiedPaymentIdentity(_ context.Context, id int64, kind identitydomain.Kind, scope string) (identityport.VerifiedCommerceIdentity, bool, error) {
	return reader.value, id == reader.value.IdentityID && kind == reader.value.Kind && scope == reader.value.Scope, nil
}

type profitSharingRecoveryWorkerLineage struct{ root customerdomain.CustomerID }

func (lineage profitSharingRecoveryWorkerLineage) CanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return []customerdomain.CustomerID{lineage.root}, nil
}

type profitSharingRecoveryWorkerSDK struct {
	mu       sync.Mutex
	addCalls int
	material paymentprovider.ProfitSharingMaterial
}

// profitSharingRecoveryWorkerAdapter lets the real typed River worker be
// registered before the Payment service and its DB material loader complete
// their intentional composition cycle. It is set before the worker runtime
// starts and delegates only to the actual WeChat Pay adapter.
type profitSharingRecoveryWorkerAdapter struct{ payment *paymentprovider.WeChatPay }

func (adapter *profitSharingRecoveryWorkerAdapter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if adapter == nil || adapter.payment == nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("fixture-payment-adapter-unwired", attempt.EffectID)}, nil
	}
	return adapter.payment.Execute(ctx, envelope, attempt)
}

func (sdk *profitSharingRecoveryWorkerSDK) AddReceiver(_ context.Context, material paymentprovider.ProfitSharingMaterial) error {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	sdk.addCalls++
	sdk.material = material
	return nil
}

func (*profitSharingRecoveryWorkerSDK) CreateOrder(context.Context, paymentprovider.ProfitSharingMaterial) error {
	return fmt.Errorf("unexpected split instruction")
}

func (*profitSharingRecoveryWorkerSDK) QueryOrder(context.Context, paymentprovider.ProfitSharingMaterial) (paymentprovider.ProfitSharingQuery, error) {
	return paymentprovider.ProfitSharingQuery{}, fmt.Errorf("unexpected profit-sharing query")
}

func (*profitSharingRecoveryWorkerSDK) UnfreezeOrder(context.Context, paymentprovider.ProfitSharingMaterial) error {
	return fmt.Errorf("unexpected unfreeze instruction")
}

func (sdk *profitSharingRecoveryWorkerSDK) snapshot() (int, paymentprovider.ProfitSharingMaterial) {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	return sdk.addCalls, sdk.material
}

// TestPostgreSQLProfitSharingReceiverRecoveryRunsPersistedMaterial proves the
// complete reviewed-recovery path through the actual EER queue and worker. It
// uses a local fake SDK only at the final Provider leaf, so the test cannot
// contact WeChat but still proves the persisted source resolves to one AddReceiver call.
func TestPostgreSQLProfitSharingReceiverRecoveryRunsPersistedMaterial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	pool, err := pgxpool.New(ctx, databaseURL)
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
	keyPath, certPath := distributionFixturePaymentCredentials(t)
	privateKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := paymentprovider.ParseMerchantPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	platformSerial, platformKey, err := paymentprovider.ParsePlatformCertificate(certificate)
	if err != nil {
		t.Fatal(err)
	}

	workerAdapter := &profitSharingRecoveryWorkerAdapter{}
	worker := effects.NewWorker(nil, workerAdapter)
	if err = river.AddWorkerSafely[effects.EffectJobArgs](workers, worker); err != nil {
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

	const (
		customerID = int64(8101)
		identityID = int64(9101)
		appID      = "wx-recovery-worker"
		appScope   = "wechat-app:wx-recovery-worker"
		openID     = "fixture-recovery-openid"
	)
	identity := identityport.VerifiedCommerceIdentity{IdentityID: identityID, CustomerID: customerdomain.CustomerID(customerID), Kind: identitydomain.KindOAOpenID, Scope: appScope, Value: openID}
	service := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, effectStore, effectStore)
	if err = service.SetProfitSharingEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err = service.SetPaymentChannelAppIDs("wx-mini-recovery-worker", appID); err != nil {
		t.Fatal(err)
	}
	if err = service.SetProfitSharingIdentityReader(profitSharingRecoveryWorkerIdentityReader{value: identity}); err != nil {
		t.Fatal(err)
	}
	service.SetCanonicalLineageReader(profitSharingRecoveryWorkerLineage{root: customerdomain.CustomerID(customerID)})

	loader := paymentprovider.DBMaterialLoader{ProfitSharing: service}
	wechatPay, err := paymentprovider.NewWeChatPay(paymentprovider.Config{
		Enabled: true, AppID: "wx-mini-recovery-worker", AppScope: "wechat-app:wx-mini-recovery-worker", H5AppID: appID, H5AppScope: appScope,
		APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: "https://fixture.example/payment", RefundNotifyURL: "https://fixture.example/refund",
		Credential: paymentprovider.Credential{MerchantID: "fixture-merchant", Serial: "fixture-merchant-serial", Signer: signer, PlatformKeys: map[string]*rsa.PublicKey{platformSerial: platformKey}},
	}, loader, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	sdk := &profitSharingRecoveryWorkerSDK{}
	if err = wechatPay.SetProfitSharingSDK(sdk); err != nil {
		t.Fatal(err)
	}
	if err = service.SetProfitSharingReconciler(wechatPay); err != nil {
		t.Fatal(err)
	}
	workerAdapter.payment = wechatPay
	if err = worker.BindRepository(effectStore); err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(service); err != nil {
		t.Fatal(err)
	}

	// The historic failure must predate Service.now(), which writes the
	// recovered state and is checked by PostgreSQL's updated_at invariant.
	now := time.Now().UTC().Add(-time.Minute)
	var oldEffectID, receiverID int64
	if err = pool.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state,attempt_count,generation,updated_at) VALUES('payment',$1,$2,$3,$4,$5,$6,'final_failed',1,1,$7) RETURNING id`, effectport.KindWeChatPayReceiverAdd, effectport.Hash("recovery-worker-old-source"), effectport.Hash("recovery-worker-old-target"), effectport.Hash("recovery-worker-old-payload"), effectport.Hash("recovery-worker-old-policy"), effectport.Hash("recovery-worker-old-envelope"), now).Scan(&oldEffectID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state,call_attempted,real_external_call_executed,completed_at) VALUES($1,1,1,1,'final_failed',false,false,$2)`, oldEffectID, now); err != nil {
		t.Fatal(err)
	}
	accountDigest := effectport.Hash("payment.profit-sharing.receiver.account.v1", appID, appScope, openID)
	if err = pool.QueryRow(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,external_effect_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,'h5_official_account',$5,'final_failed',$6,2,$7,$7) RETURNING id`, customerID, identityID, appID, appScope, accountDigest, oldEffectID, now).Scan(&receiverID); err != nil {
		t.Fatal(err)
	}

	recovery, err := service.RecoverProfitSharingReceiver(ctx, paymentport.ProfitSharingReceiverRecoveryCommand{ReceiverReference: "psrecv_" + strconv.FormatInt(receiverID, 10), ActorAdminUserID: 71, IdempotencyKey: "receiver-worker-recovery-key-0001", EvidenceReference: "fixture-reviewed-no-provider-call"})
	if err != nil || recovery.State != string(paymentdomain.ProfitSharingReceiverAccepted) || recovery.EffectRef == "" {
		t.Fatalf("recover=%+v err=%v", recovery, err)
	}
	effectID, err := strconv.ParseInt(recovery.EffectRef[len("eer_"):], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var source, payload string
	if err = pool.QueryRow(ctx, `SELECT source_ref_digest,payload_digest FROM external_effects WHERE id=$1`, effectID).Scan(&source, &payload); err != nil {
		t.Fatal(err)
	}
	material, err := service.LoadProfitSharingEffectMaterial(ctx, effectport.KindWeChatPayReceiverAdd, effectport.Digest(source))
	if err != nil || string(material.PayloadDigest) != payload || material.AppID != appID || material.ReceiverAccount != openID {
		t.Fatalf("loaded material=%+v err=%v", material, err)
	}

	runtimeService, err := platformjobqueue.NewRuntime(pool, workers, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtimeService.Run(runCtx) }()
	waitForProfitSharingRecoveryEffect(t, ctx, effectStore, recovery.EffectRef, effectport.StateExecuted)
	stop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out stopping receiver recovery effect worker")
	}

	calls, invokedMaterial := sdk.snapshot()
	if calls != 1 || invokedMaterial.AppID != appID || invokedMaterial.ReceiverAccount != openID || string(invokedMaterial.PayloadDigest) != payload {
		t.Fatalf("sdk calls=%d material=%+v", calls, invokedMaterial)
	}
	var state string
	var attempted, external bool
	if err = pool.QueryRow(ctx, `SELECT r.state,a.call_attempted,a.real_external_call_executed FROM payment_profit_sharing_receivers r JOIN external_effect_attempts a ON a.effect_id=$2 WHERE r.id=$1`, receiverID, effectID).Scan(&state, &attempted, &external); err != nil || state != "ready" || !attempted || !external {
		t.Fatalf("receiver state=%q attempted=%t external=%t err=%v", state, attempted, external, err)
	}
}

func waitForProfitSharingRecoveryEffect(t *testing.T, ctx context.Context, repository *effects.Repository, effectRef string, want effectport.State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		projection, err := repository.Get(ctx, effectRef)
		if err == nil && projection.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	projection, err := repository.Get(ctx, effectRef)
	t.Fatalf("effect %s state=%+v err=%v want=%s", effectRef, projection, err, want)
}
