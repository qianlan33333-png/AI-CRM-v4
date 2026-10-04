package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerdirectory "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/http"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

func TestPostgreSQLProductActivityContextHTTPIdempotencyAndExpiry(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	h.clock = time.Now().UTC().Truncate(time.Second)
	ctx := context.Background()

	createProductCampaign := func(name, key string, productID int64, endsAt time.Time) referraldomain.Campaign {
		t.Helper()
		startsAt := h.clock.Add(-time.Hour)
		campaign, err := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{
			ActorAdminID: 9001, Name: name, StartsAt: startsAt, EndsAt: endsAt,
			TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationProductPurchase,
			ProductID: productID, ProductType: referraldomain.ProductTypeStandard, LeaderboardMetric: referraldomain.LeaderboardSalesAmount,
			IdempotencyKey: key + "-create",
		})
		if err != nil {
			t.Fatalf("create product campaign: %v", err)
		}
		created := campaign
		campaign, err = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{
			ActorAdminID: 9001, CampaignID: created.ID, ExpectedVersion: created.Version,
			Target: referraldomain.CampaignActive, IdempotencyKey: key + "-activate",
		})
		if err != nil {
			t.Fatalf("activate product campaign %+v: %v", created, err)
		}
		return campaign
	}
	campaign := createProductCampaign("product context expiry", "product-context-expiry", 17001, h.clock.Add(48*time.Hour))
	otherCampaign := createProductCampaign("product context other campaign", "product-context-other", 17002, h.clock.Add(72*time.Hour))

	actors := map[string]distributionport.TrustedSessionActor{
		"dist-101": referralActor(101, h.clock),
		"dist-202": referralActor(202, h.clock),
	}
	handler, err := referralhttp.NewHandler(referralhttp.Config{
		Public: h.service, Admin: h.admin, Sessions: productContextSessions(actors),
		Bridge: productContextBridge{}, Names: productContextNames{}, Security: productContextSecurity{},
		AllowedOrigins: []string{"https://crm.example.test"}, SessionCookieName: "dist",
		CSRFCookieName: "csrf", CSRFHeader: "X-Distribution-CSRF",
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := func(campaignID int64, session, key string) (*httptest.ResponseRecorder, string, time.Time) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/"+strconv.FormatInt(campaignID, 10)+"/product-context", nil)
		request.AddCookie(&http.Cookie{Name: "dist", Value: session})
		request.Header.Set("Origin", "https://crm.example.test")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServePublicHTTP(response, request)
		var body struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Errorf("decode product-context response %q: %v", response.Body.String(), err)
		}
		cookies := response.Result().Cookies()
		if len(cookies) != 1 {
			return response, "", body.ExpiresAt
		}
		return response, cookies[0].Value, body.ExpiresAt
	}

	key := "product-context-same-actor-campaign-key"
	first, firstToken, firstExpiry := issue(campaign.ID, "dist-101", key)
	if first.Code != http.StatusCreated {
		t.Fatalf("initial context status=%d body=%s", first.Code, first.Body.String())
	}
	if firstToken == "" || firstExpiry.IsZero() {
		t.Fatalf("initial context token/expiry missing: token=%q expiry=%v", firstToken, firstExpiry)
	}
	digest := sha256.Sum256([]byte(firstToken))
	var storedExpiry time.Time
	if err := h.pool.QueryRow(ctx, `SELECT expires_at FROM referral_product_activity_contexts WHERE context_digest=$1`, digest[:]).Scan(&storedExpiry); err != nil {
		t.Fatal(err)
	}
	if !firstExpiry.Equal(storedExpiry) {
		t.Errorf("HTTP expires_at=%s, stored context expires_at=%s", firstExpiry.Format(time.RFC3339Nano), storedExpiry.Format(time.RFC3339Nano))
	}
	firstCookies := first.Result().Cookies()
	if len(firstCookies) != 1 || !firstCookies[0].Expires.Equal(storedExpiry.UTC().Truncate(time.Second)) {
		t.Errorf("cookie expiry does not match stored context expiry: cookies=%+v stored=%s", firstCookies, storedExpiry.Format(time.RFC3339Nano))
	}

	replay, replayToken, replayExpiry := issue(campaign.ID, "dist-101", key)
	if replay.Code != http.StatusCreated || replayToken != firstToken || !replayExpiry.Equal(firstExpiry) {
		t.Errorf("same-key replay status=%d token_same=%t expiry=%s want=%s body=%s", replay.Code, replayToken == firstToken, replayExpiry.Format(time.RFC3339Nano), firstExpiry.Format(time.RFC3339Nano), replay.Body.String())
	}
	var contextRows int64
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM referral_product_activity_contexts WHERE campaign_id=$1`, campaign.ID).Scan(&contextRows); err != nil {
		t.Fatal(err)
	}
	if contextRows != 1 {
		t.Errorf("same-key replay created %d context rows, want 1", contextRows)
	}

	crossCampaign, _, _ := issue(otherCampaign.ID, "dist-101", key)
	if crossCampaign.Code != http.StatusConflict {
		t.Errorf("same actor/key with another campaign status=%d body=%s, want conflict", crossCampaign.Code, crossCampaign.Body.String())
	}

	otherActor, otherActorToken, _ := issue(campaign.ID, "dist-202", key)
	if otherActor.Code != http.StatusCreated || otherActorToken == "" || otherActorToken == firstToken {
		t.Errorf("same key for distinct actor status=%d token_same=%t body=%s", otherActor.Code, otherActorToken == firstToken, otherActor.Body.String())
	}

	// Two simultaneous first attempts must commit one context and one receipt,
	// rather than merely returning the same deterministic token to both callers.
	concurrentKey := "product-context-concurrent-first-key"
	start := make(chan struct{})
	tokens := make(chan string, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			response, token, expiry := issue(campaign.ID, "dist-101", concurrentKey)
			if response.Code != http.StatusCreated || token == "" || !expiry.Equal(storedExpiry) {
				t.Errorf("concurrent issuance status=%d expiry=%s", response.Code, expiry)
			}
			tokens <- token
		}()
	}
	close(start)
	workers.Wait()
	close(tokens)
	var concurrentToken string
	for token := range tokens {
		if concurrentToken != "" && concurrentToken != token {
			t.Error("concurrent first attempts returned different contexts")
		}
		concurrentToken = token
	}
	concurrentDigest := sha256.Sum256([]byte(concurrentToken))
	keyDigest := sha256.Sum256([]byte(concurrentKey))
	var receipts int64
	if err := h.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_product_activity_contexts WHERE context_digest=$1),(SELECT count(*) FROM referral_operation_receipts WHERE operation='issue_product_activity_context' AND actor_scope='customer:101' AND key_digest=$2 AND result_kind='campaign' AND result_id=$3)`, concurrentDigest[:], keyDigest[:], campaign.ID).Scan(&contextRows, &receipts); err != nil || contextRows != 1 || receipts != 1 {
		t.Fatalf("concurrent durable facts contexts=%d receipts=%d err=%v", contextRows, receipts, err)
	}

	h.clock = campaign.EndsAt.Add(time.Second)
	expiredReplay, expiredToken, expiredExpiry := issue(campaign.ID, "dist-101", key)
	if expiredReplay.Code != http.StatusCreated || expiredToken != firstToken || !expiredExpiry.Equal(storedExpiry) {
		t.Errorf("expired exact replay status=%d token_same=%t expiry=%s stored=%s body=%s", expiredReplay.Code, expiredToken == firstToken, expiredExpiry.Format(time.RFC3339Nano), storedExpiry.Format(time.RFC3339Nano), expiredReplay.Body.String())
	}
	var lookupErr error
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		_, lookupErr = h.repository.ReadProductSaleContextWithin(tx, digest, 17001, referraldomain.ProductTypeStandard, campaign.EndsAt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if lookupErr != referralport.ErrNotFound {
		t.Errorf("checkout lookup at stored expiry err=%v, want ErrNotFound", lookupErr)
	}
	checkout := orderport.ProductSaleCheckoutContextCommand{OrderID: 17001, OrderVersion: 1, ProductID: 17001, ProductType: referraldomain.ProductTypeStandard, ProductCode: "product-context-expiry", ProductName: "product-context-expiry", ProductVersion: 1, BuyerCustomerID: 101, BeneficiaryCustomerID: 101, ReferralActivityContext: firstToken, OccurredAt: campaign.EndsAt}
	if err = h.uow.Within(ctx, func(tx context.Context) error {
		return h.service.RecordProductSaleCheckoutAttributionWithin(tx, checkout)
	}); err != nil {
		t.Errorf("expired context checkout err=%v", err)
	}
	if err = h.pool.QueryRow(ctx, `SELECT count(*) FROM referral_product_sale_checkouts WHERE order_id=$1`, checkout.OrderID).Scan(&contextRows); err != nil {
		t.Fatal(err)
	}
	if contextRows != 0 {
		t.Errorf("expired context froze %d checkout attribution rows, want 0", contextRows)
	}
	expiredFresh, _, _ := issue(campaign.ID, "dist-101", "product-context-after-expiry-fresh-key")
	if expiredFresh.Code != http.StatusConflict {
		t.Errorf("fresh request after campaign end status=%d body=%s, want campaign unavailable", expiredFresh.Code, expiredFresh.Body.String())
	}
}

type productContextSessions map[string]distributionport.TrustedSessionActor

func (s productContextSessions) Resolve(_ context.Context, value string) (distributionport.TrustedSessionActor, error) {
	actor, ok := s[value]
	if !ok {
		return distributionport.TrustedSessionActor{}, referralport.ErrUnauthorized
	}
	return actor, nil
}

type productContextBridge struct{}

func (productContextBridge) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, referralport.ErrUnauthorized
}

type productContextNames struct{}

func (productContextNames) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}

type productContextSecurity struct{}

func (productContextSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}

func (productContextSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}

var _ customerdirectory.DirectoryDisplayNameReader = productContextNames{}
