package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type fakeSecurity struct{ csrf error }

func (fakeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s fakeSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.csrf
}

type fakeOptions struct{}

func (fakeOptions) ListProductOptions(_ context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	kind := productport.ProductOptionStandard
	id := productport.ID(6)
	name := "标准商品"
	if query.ProductType == productport.ProductOptionServicePeriod {
		kind, id, name = productport.ProductOptionServicePeriod, 8, "周期商品"
	}
	return productport.ProductOptionPage{Items: []productport.ProductOption{{ID: id, Name: name, PriceMinor: 1000, Currency: "CNY", ProductType: kind}}, Total: 1, Limit: query.Limit, Offset: query.Offset}, nil
}

func (fakeOptions) ReadProductTargets(_ context.Context, references []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	items := make([]productport.ProductTargetLookup, 0, len(references))
	for _, reference := range references {
		name := "标准商品"
		if reference.ProductType == productport.ProductOptionServicePeriod {
			name = "周期商品"
		}
		items = append(items, productport.ProductTargetLookup{Reference: reference, Name: name, Found: true})
	}
	return items, nil
}

type recordingTargetOptions struct {
	fakeOptions
	err     error
	missing map[productport.ProductTargetReference]bool
	calls   [][]productport.ProductTargetReference
}

func (f *recordingTargetOptions) ReadProductTargets(_ context.Context, references []productport.ProductTargetReference) ([]productport.ProductTargetLookup, error) {
	call := append([]productport.ProductTargetReference(nil), references...)
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	items := make([]productport.ProductTargetLookup, 0, len(references))
	for _, reference := range references {
		if f.missing[reference] {
			items = append(items, productport.ProductTargetLookup{Reference: reference, Found: false})
			continue
		}
		name := "标准商品"
		if reference.ProductType == productport.ProductOptionServicePeriod {
			name = "周期商品"
		}
		items = append(items, productport.ProductTargetLookup{Reference: reference, Name: name, Found: true})
	}
	return items, nil
}

type fakeRules struct {
	created        couponport.UpsertCommand
	page           couponport.Page
	item           couponport.Coupon
	publishKey     string
	archiveID      couponport.ID
	archiveVersion int64
	archiveKey     string
}

type fakeClaims struct {
	page couponport.AdminCouponClaimPage
}

type fakePublic struct {
	share couponport.PublicCouponShare
	actor int64
}

func (f *fakePublic) GetPublicCoupon(context.Context, string) (couponport.PublicCoupon, error) {
	return couponport.PublicCoupon{}, errors.New("not used")
}
func (f *fakePublic) PublicClaimState(context.Context, int64, couponport.ID) (couponport.PublicCouponClaimState, error) {
	return couponport.PublicCouponClaimState{}, errors.New("not used")
}
func (f *fakePublic) ListAvailableClaims(context.Context, int64, string, time.Time) ([]couponport.CustomerCoupon, error) {
	return nil, errors.New("not used")
}
func (f *fakePublic) EnsurePublicShare(_ context.Context, couponID couponport.ID, actorID int64) (couponport.PublicCouponShare, error) {
	f.actor = actorID
	if f.share.CouponID != couponID {
		return couponport.PublicCouponShare{}, errors.New("unexpected coupon")
	}
	return f.share, nil
}

func (f fakeClaims) ListCouponClaims(_ context.Context, couponID couponport.ID, limit, offset int32) (couponport.AdminCouponClaimPage, error) {
	if couponID != 3 {
		return couponport.AdminCouponClaimPage{}, errors.New("unexpected coupon")
	}
	f.page.Limit, f.page.Offset = limit, offset
	return f.page, nil
}

func (f *fakeRules) List(context.Context, int32, int32, string, string) (couponport.Page, error) {
	return f.page, nil
}
func (f *fakeRules) Get(context.Context, couponport.ID) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Stats(context.Context, couponport.ID) (couponport.RuleStats, error) {
	return couponport.RuleStats{}, nil
}
func (f *fakeRules) Create(_ context.Context, x couponport.UpsertCommand) (couponport.Coupon, error) {
	f.created = x
	return f.item, nil
}
func (f *fakeRules) Update(context.Context, couponport.UpsertCommand) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) UpdateDraft(context.Context, couponport.UpsertCommand) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Publish(_ context.Context, _ couponport.ID, _ int64, key string) (couponport.Coupon, error) {
	f.publishKey = key
	return f.item, nil
}
func (f *fakeRules) Stop(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Archive(_ context.Context, id couponport.ID, expected, _ int64, key string) (couponport.Coupon, error) {
	f.archiveID, f.archiveVersion, f.archiveKey = id, expected, key
	return f.item, nil
}
func (f *fakeRules) Delete(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Copy(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}

func couponFixture() couponport.Coupon {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	days := int32(7)
	return couponport.Coupon{ID: 3, Name: "券", DiscountAmountTotal: 100, Currency: "CNY", Status: "draft", AvailabilityStatus: "draft", TotalIssueLimit: 5, PerUserIssueLimit: 1, ClaimStartsAt: start, ClaimEndsAt: start.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:6"}, CreatedBy: 7, UpdatedBy: 7, Version: 1, CreatedAt: start, UpdatedAt: start}
}
func TestCreateCouponUsesAuthenticatedActorCSRFAndHeaderKey(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, err := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"券","discount_amount_total":100,"total_issue_limit":5,"per_user_issue_limit":1,"claim_starts_at":"2026-01-02T03:04:05Z","claim_ends_at":"2026-01-02T04:04:05Z","validity_mode":"relative_days","relative_validity_days":7,"target_refs":["standard_product:6"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "1234567890abcdef")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	if rules.created.Actor != 7 || rules.created.IdempotencyKey != "1234567890abcdef" {
		t.Fatalf("command=%+v", rules.created)
	}
	if !strings.Contains(res.Body.String(), `"create_replay_safe":true`) {
		t.Fatalf("body=%s", res.Body.String())
	}
}
func TestCouponWriteCSRFAndUnknownFieldsFailClosed(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{csrf: errors.New("no")})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(`{}`)))
	if r.Code != 403 {
		t.Fatalf("csrf=%d", r.Code)
	}
	h, _ = NewHandler(rules, fakeOptions{}, fakeSecurity{})
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(`{"unknown":1}`)))
	if r.Code != 400 {
		t.Fatalf("unknown=%d", r.Code)
	}
}

// The generated browser client sends POST with no JSON body. The Host supplies
// its idempotency key; preserve that contract without weakening missing-key or
// CSRF rejection (the original publish HTTP 400 failure mode).
func TestGeneratedCouponPublishEmptyBodyWithHostKey(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	for _, key := range []string{"", "coupon-publish-browser-key"} {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/coupons/3/publish", nil)
		req.Header.Set("Idempotency-Key", key)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if key == "" {
			if res.Code != http.StatusBadRequest || rules.publishKey != "" {
				t.Fatalf("missing key: %d", res.Code)
			}
		} else if res.Code != http.StatusOK || rules.publishKey != key || !strings.Contains(res.Body.String(), `"id":3`) {
			t.Fatalf("publish receipt: status=%d key=%q body=%s", res.Code, rules.publishKey, res.Body.String())
		}
	}
}

func TestCouponDeleteArchivesFrozenVersion(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, err := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/coupons/3", strings.NewReader(`{"expected_version":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "coupon-archive-http-0001")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || rules.archiveID != 3 || rules.archiveVersion != 1 || rules.archiveKey != "coupon-archive-http-0001" || !strings.Contains(response.Body.String(), `"status":"draft"`) {
		t.Fatalf("archive response=%d body=%s command=%+v", response.Code, response.Body.String(), rules)
	}

	invalid := httptest.NewRequest(http.MethodDelete, "/api/admin/coupons/3", nil)
	invalid.Header.Set("Idempotency-Key", "coupon-archive-http-0002")
	invalidResponse := httptest.NewRecorder()
	h.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || rules.archiveKey != "coupon-archive-http-0001" {
		t.Fatalf("missing version archive status=%d body=%s command=%+v", invalidResponse.Code, invalidResponse.Body.String(), rules)
	}
}
func TestCouponProductOptionsAndExcludedClaims(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/product-options?product_type=standard_product", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "standard_product:6") {
		t.Fatalf("options %d %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/claims", nil))
	if r.Code != 404 {
		t.Fatalf("claims=%d", r.Code)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/product-options?product_type=service_period", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "service_period:8") {
		t.Fatalf("service-period options %d %s", r.Code, r.Body.String())
	}
}

func TestCouponClaimListUsesDedicatedMaskedReadPort(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	from, until := now.Add(-time.Hour), now.Add(time.Hour)
	h, err := NewHandlerWithClaims(&fakeRules{item: couponFixture()}, fakeOptions{}, fakeClaims{page: couponport.AdminCouponClaimPage{Items: []couponport.AdminCouponClaim{{ClaimID: 9, CustomerID: 11, CouponID: 3, Status: "available", ClaimNoMasked: "***7", ClaimedAt: now, ValidFrom: &from, ValidUntil: &until}}, Total: 1}}, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/claims?limit=10&offset=0", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"claim_no_masked":"***7"`) || !strings.Contains(r.Body.String(), `"valid_from":"2026-01-02T02:04:05Z"`) || !strings.Contains(r.Body.String(), `"valid_until":"2026-01-02T04:04:05Z"`) || strings.Contains(r.Body.String(), "unionid") {
		t.Fatalf("claims status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestCouponShareUsesFrozenGETWithAdminAndCSRF(t *testing.T) {
	public := &fakePublic{share: couponport.PublicCouponShare{CouponID: 3, PublicSlug: "cp-a1b2c3", URL: "/c/cp-a1b2c3"}}
	h, err := NewHandlerWithClaimsAndPublic(&fakeRules{item: couponFixture()}, fakeOptions{}, fakeClaims{}, public, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/share", nil))
	if r.Code != http.StatusOK || public.actor != 7 || !strings.Contains(r.Body.String(), `"url":"/c/cp-a1b2c3"`) || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("share status=%d actor=%d headers=%v body=%s", r.Code, public.actor, r.Header(), r.Body.String())
	}
	h, _ = NewHandlerWithClaimsAndPublic(&fakeRules{item: couponFixture()}, fakeOptions{}, fakeClaims{}, public, fakeSecurity{csrf: errors.New("missing")})
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/share", nil))
	if r.Code != http.StatusForbidden {
		t.Fatalf("share csrf=%d", r.Code)
	}
}

// Exercise the public DTO consumed by the unchanged standard picker. The two
// product domains can use the same numeric ID; type remains part of the ref.
func TestStandardCouponEditorReadProjection(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	for _, kind := range []string{"standard_product", "service_period"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/product-options?product_type="+kind, nil))
		var data struct {
			Items []struct {
				Ref         string `json:"target_ref"`
				Type        string `json:"product_type"`
				Title       string `json:"title"`
				Price       int64  `json:"price_cents"`
				LegacyPrice int64  `json:"price_minor"`
			} `json:"items"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &data) != nil || len(data.Items) != 1 {
			t.Fatalf("invalid product options: %d %s", r.Code, r.Body.String())
		}
		item := data.Items[0]
		if item.Type != kind || !strings.HasPrefix(item.Ref, kind+":") || item.Title == "" || item.Price != 1000 || item.Price != item.LegacyPrice {
			t.Fatalf("standard picker lost product identity or price: %+v", item)
		}
	}
	for _, issued := range []int64{0, 1} {
		rules.item.IssuedCount = issued
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3", nil))
		var data struct {
			Coupon struct {
				Frozen *bool `json:"rules_frozen"`
			} `json:"coupon"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &data) != nil || data.Coupon.Frozen == nil || *data.Coupon.Frozen != (issued > 0) {
			t.Fatalf("standard editor freeze state mismatch: %d %s", r.Code, r.Body.String())
		}
	}
}

func TestCouponListProjectsBoundedTypedProductNames(t *testing.T) {
	first := couponFixture()
	first.TargetRefs = []string{"standard_product:6", "service_period:8"}
	second := couponFixture()
	second.ID = 4
	second.TargetRefs = []string{"standard_product:6", "standard_product:99"}
	missing := productport.ProductTargetReference{ProductType: productport.ProductOptionStandard, ID: 99}
	options := &recordingTargetOptions{missing: map[productport.ProductTargetReference]bool{missing: true}}
	h, err := NewHandler(&fakeRules{page: couponport.Page{Items: []couponport.Coupon{first, second}, Total: 2, Limit: 2}}, options, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/coupons?limit=2", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Coupons []struct {
			TargetRefs []string `json:"target_refs"`
			Products   []struct {
				TargetRef string `json:"target_ref"`
				Name      string `json:"name"`
				State     string `json:"state"`
			} `json:"target_products"`
		} `json:"coupons"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(options.calls) != 1 || len(options.calls[0]) != 3 {
		t.Fatalf("expected one deduplicated Product read, calls=%+v", options.calls)
	}
	if len(payload.Coupons) != 2 || strings.Join(payload.Coupons[0].TargetRefs, ",") != "standard_product:6,service_period:8" {
		t.Fatalf("target refs must retain their API contract: %+v", payload.Coupons)
	}
	if got := payload.Coupons[0].Products; len(got) != 2 || got[0].TargetRef != "standard_product:6" || got[0].Name != "标准商品" || got[0].State != "available" || got[1].TargetRef != "service_period:8" || got[1].Name != "周期商品" || got[1].State != "available" {
		t.Fatalf("first projection=%+v", got)
	}
	if got := payload.Coupons[1].Products; len(got) != 2 || got[1].TargetRef != "standard_product:99" || got[1].Name != "" || got[1].State != "not_found" {
		t.Fatalf("missing product must remain an explicit Product fact: %+v", got)
	}
}

func TestCouponListRejectsUnavailableProductPresentation(t *testing.T) {
	for _, requestPath := range []string{"/api/admin/coupons?limit=1", "/api/admin/coupons/3"} {
		t.Run(requestPath, func(t *testing.T) {
			options := &recordingTargetOptions{err: errors.New("Product unavailable")}
			h, err := NewHandler(&fakeRules{page: couponport.Page{Items: []couponport.Coupon{couponFixture()}, Total: 1, Limit: 1}, item: couponFixture()}, options, fakeSecurity{})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "standard_product:") {
				t.Fatalf("must fail closed without raw Product refs: status=%d body=%s", response.Code, response.Body.String())
			}
			if len(options.calls) != 1 {
				t.Fatalf("Product reads=%d want=1", len(options.calls))
			}
		})
	}
}

func TestCouponListReadsEveryValidTargetInOneProductBatch(t *testing.T) {
	items := couponPageWithUniqueTargets()
	options := &recordingTargetOptions{}
	h, err := NewHandler(&fakeRules{page: couponport.Page{Items: items, Total: int64(len(items)), Limit: int32(len(items))}}, options, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/coupons?limit=200", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if len(options.calls) != 1 || len(options.calls[0]) != productport.ProductTargetBatchMaximum {
		t.Fatalf("Product reads must cover the whole legal page once, calls=%d batch=%d", len(options.calls), len(options.calls[0]))
	}
}

func couponPageWithUniqueTargets() []couponport.Coupon {
	items := make([]couponport.Coupon, 0, 200)
	nextID := 1
	for couponID := 1; couponID <= 200; couponID++ {
		item := couponFixture()
		item.ID = couponport.ID(couponID)
		item.TargetRefs = make([]string, 0, 100)
		for target := 0; target < 100; target++ {
			item.TargetRefs = append(item.TargetRefs, "standard_product:"+strconv.Itoa(nextID))
			nextID++
		}
		items = append(items, item)
	}
	return items
}
