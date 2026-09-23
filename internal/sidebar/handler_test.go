package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var testCursorSigningKey = []byte("sidebar-handler-test-cursor-signing-key")

type testContext struct{}

func (testContext) VerifySidebarContext(context.Context, string) (Principal, customerdomain.CustomerID, error) {
	return Principal{CorpID: "corp", EmployeeID: "staff"}, 42, nil
}

type testProfile struct{}

func (testProfile) ReadSidebarProfile(context.Context, customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{CustomerID: 42, DisplayName: "Alice", Status: "active", Version: 1}, nil
}
func (testProfile) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{CustomerID: 42, DisplayName: "Alice", Version: 2}, nil
}
func (testProfile) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{Status: "attached", PhoneMasked: "138****5678"}, nil
}

type testSurveys struct{}

func (testSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testSurveys) CustomerSurveys(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.SurveyPage, error) {
	return customerport.SurveyPage{Items: []customerport.SurveyItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type pagedSurveys struct {
	items []customerport.SurveyItem
	calls []customerport.PageQuery
}

func (reader *pagedSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (reader *pagedSurveys) CustomerSurveys(_ context.Context, _ customerdomain.CustomerID, query customerport.PageQuery) (customerport.SurveyPage, error) {
	reader.calls = append(reader.calls, query)
	items := make([]customerport.SurveyItem, 0, query.Limit)
	for _, item := range reader.items {
		if item.SubmittedAt.After(query.Watermark) || (!query.AfterAt.IsZero() && (item.SubmittedAt.After(query.AfterAt) || (item.SubmittedAt.Equal(query.AfterAt) && item.ID >= query.AfterID))) {
			continue
		}
		items = append(items, item)
		if len(items) == query.Limit {
			break
		}
	}
	asOf := query.Watermark
	return customerport.SurveyPage{Items: items, Total: int64(len(reader.items)), Status: customerport.SectionStatus{State: customerport.SectionReady, AsOf: &asOf}}, nil
}

type pagedTimeline struct {
	items []customerport.TimelineItem
	calls []customerport.PageQuery
}

func (reader *pagedTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}

func (reader *pagedTimeline) CustomerTimeline(_ context.Context, _ customerdomain.CustomerID, query customerport.PageQuery) (customerport.TimelinePage, error) {
	reader.calls = append(reader.calls, query)
	items := make([]customerport.TimelineItem, 0, query.Limit)
	for _, item := range reader.items {
		if item.OccurredAt.After(query.Watermark) || (!query.AfterAt.IsZero() && (item.OccurredAt.After(query.AfterAt) || (item.OccurredAt.Equal(query.AfterAt) && item.ID >= query.AfterID))) {
			continue
		}
		items = append(items, item)
		if len(items) == query.Limit {
			break
		}
	}
	asOf := query.Watermark
	return customerport.TimelinePage{Items: items, Status: customerport.SectionStatus{State: customerport.SectionReady, AsOf: &asOf}}, nil
}

type testTimeline struct{}

func (testTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testTimeline) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	return customerport.TimelinePage{Items: []customerport.TimelineItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testProducts struct{}

func (testProducts) ListProductOptions(context.Context, productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	return productport.ProductOptionPage{Items: []productport.ProductOption{}}, nil
}
func (testProducts) ReadProductTarget(context.Context, productport.ProductOptionType, productport.ID) (productport.ProductOption, error) {
	return productport.ProductOption{}, nil
}
func (testProducts) ReadSidebarShareProduct(context.Context, productport.ProductOptionType, productport.ID) (productport.SidebarShareProduct, error) {
	return productport.SidebarShareProduct{}, errors.New("product unavailable")
}

type fixedProducts struct{ product productport.ProductOption }

func (products fixedProducts) ListProductOptions(context.Context, productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	return productport.ProductOptionPage{Items: []productport.ProductOption{products.product}, Total: 1}, nil
}
func (products fixedProducts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	if kind != products.product.ProductType || id != products.product.ID {
		return productport.ProductOption{}, errors.New("product unavailable")
	}
	return products.product, nil
}
func (products fixedProducts) ReadSidebarShareProduct(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.SidebarShareProduct, error) {
	if kind != products.product.ProductType || id != products.product.ID {
		return productport.SidebarShareProduct{}, errors.New("product unavailable")
	}
	return productport.SidebarShareProduct{ID: products.product.ID, Code: products.product.Code, ProductType: products.product.ProductType, Name: products.product.Name, CoverURL: products.product.CoverURL}, nil
}

type testOrders struct{}

func (testOrders) Get(context.Context, int64) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (testOrders) GetByReference(context.Context, string) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (testOrders) List(context.Context, orderport.ListQuery) (orderport.Page, error) {
	return orderport.Page{Items: []orderdomain.Snapshot{}}, nil
}

type testEntitlements struct{}

func (testEntitlements) ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error) {
	return orderport.EntitlementPage{Items: []orderport.Entitlement{}}, nil
}
func (testEntitlements) ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	return orderport.ServicePeriodMemberPage{Items: []orderport.Entitlement{}}, nil
}
func (testEntitlements) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, nil
}
func (testEntitlements) UpdateEntitlementRemark(context.Context, orderport.RemarkCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, nil
}
func (testEntitlements) UpdateEntitlementAlliance(context.Context, orderport.AllianceCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, nil
}

type testCoupons struct{}

func (testCoupons) ListSidebarClaimable(context.Context, int64, couponport.SidebarClaimableQuery) (couponport.SidebarClaimablePage, error) {
	return couponport.SidebarClaimablePage{Items: []couponport.SidebarClaimableItem{}}, nil
}
func (testCoupons) ReadSidebarClaimable(context.Context, int64, couponport.ID) (couponport.SidebarClaimableItem, error) {
	return couponport.SidebarClaimableItem{}, errors.New("coupon unavailable")
}

type recordingCoupons struct {
	page       couponport.SidebarClaimablePage
	customerID int64
	query      couponport.SidebarClaimableQuery
	item       couponport.SidebarClaimableItem
	readID     couponport.ID
	readUserID int64
}

func (catalog *recordingCoupons) ListSidebarClaimable(_ context.Context, customerID int64, query couponport.SidebarClaimableQuery) (couponport.SidebarClaimablePage, error) {
	catalog.customerID, catalog.query = customerID, query
	return catalog.page, nil
}

func (catalog *recordingCoupons) ReadSidebarClaimable(_ context.Context, customerID int64, couponID couponport.ID) (couponport.SidebarClaimableItem, error) {
	catalog.readUserID, catalog.readID = customerID, couponID
	if catalog.item.CouponID != couponID {
		return couponport.SidebarClaimableItem{}, errors.New("coupon unavailable")
	}
	return catalog.item, nil
}

type testMaterials struct{}

func (testMaterials) ListImages(context.Context, mediaport.ImageListQuery) (mediaport.ImageListPage, error) {
	return mediaport.ImageListPage{Items: []mediaport.ImageListItem{}}, nil
}
func (testMaterials) Facets(context.Context) (mediaport.ImageFacets, error) {
	return mediaport.ImageFacets{}, nil
}
func (testMaterials) LocalImageExists(context.Context, int64) (bool, error) { return true, nil }
func (testMaterials) ReadSidebarImageForSend(context.Context, int64, time.Time) (mediaport.SidebarImageSendMaterial, error) {
	return mediaport.SidebarImageSendMaterial{ImageID: 1, MediaID: "media-1", ReadyUntil: time.Now().Add(time.Hour)}, nil
}

type recordingMaterials struct {
	testMaterials
	queries []mediaport.ImageListQuery
}

func (materials *recordingMaterials) ListImages(_ context.Context, query mediaport.ImageListQuery) (mediaport.ImageListPage, error) {
	materials.queries = append(materials.queries, query)
	return mediaport.ImageListPage{Items: []mediaport.ImageListItem{}, Total: 0, Limit: query.Limit, Offset: query.Offset}, nil
}

type testImageVariants struct {
	variant mediaport.ImageVariant
	err     error
	calls   int
}

func (reader *testImageVariants) GetEnabledImageVariant(context.Context, int64, string) (mediaport.ImageVariant, error) {
	reader.calls++
	return reader.variant, reader.err
}

type unreadyMaterials struct{ testMaterials }

func (unreadyMaterials) ReadSidebarImageForSend(context.Context, int64, time.Time) (mediaport.SidebarImageSendMaterial, error) {
	return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
}

type testRadar struct{}

func (testRadar) List(context.Context, radarport.ListQuery) (radarport.LinkPage, error) {
	return radarport.LinkPage{Items: []radarport.LinkSummary{}}, nil
}
func (testRadar) Get(context.Context, radarport.RadarID) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) Create(context.Context, radarport.CreateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) Update(context.Context, radarport.UpdateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) SetStatus(context.Context, radarport.SetStatusCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}

type testSends struct{}

func (testSends) AcceptSidebarSend(context.Context, outboundport.SidebarSendCommand) (outboundport.SidebarSendAcceptance, error) {
	return outboundport.SidebarSendAcceptance{}, nil
}
func (testSends) CompleteSidebarSend(context.Context, outboundport.SidebarSendOutcomeCommand) (outboundport.SidebarSendAcceptance, error) {
	return outboundport.SidebarSendAcceptance{}, nil
}

func testRoutes(t *testing.T) http.Handler {
	return testRoutesWithCoupons(t, testCoupons{})
}

func testRoutesWithCoupons(t *testing.T, coupons couponport.SidebarClaimableCatalog) http.Handler {
	return testRoutesWithReaders(t, testProfile{}, testSurveys{}, testTimeline{}, coupons)
}

func testRoutesWithReaders(t *testing.T, profiles customerport.SidebarProfileService, surveys customerport.CustomerSurveyReader, timeline customerport.CustomerTimelineReader, coupons couponport.SidebarClaimableCatalog) http.Handler {
	t.Helper()
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: profiles, Surveys: surveys, Timeline: timeline, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: coupons, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func TestEverySidebarCapabilityRequiresContextAndNeverAcceptsCustomerIdentity(t *testing.T) {
	for _, path := range []string{"/api/sidebar/v2/workbench", "/api/sidebar/v2/profile", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/timeline", "/api/sidebar/v2/products", "/api/sidebar/v2/orders", "/api/sidebar/v2/periodic-orders", "/api/sidebar/v2/coupons", "/api/sidebar/v2/materials", "/api/sidebar/v2/radar-links"} {
		response := httptest.NewRecorder()
		testRoutes(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"?external_userid=forbidden", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestReadyReadsExcludeRemovedCapabilitiesAndRawExternalIdentity(t *testing.T) {
	for _, path := range []string{"/api/sidebar/v2/workbench", "/api/sidebar/v2/profile", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/timeline", "/api/sidebar/v2/products", "/api/sidebar/v2/orders", "/api/sidebar/v2/periodic-orders", "/api/sidebar/v2/coupons", "/api/sidebar/v2/materials", "/api/sidebar/v2/radar-links"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer signed")
		response := httptest.NewRecorder()
		testRoutes(t).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		for _, forbidden := range []string{"external_userid", "relationship", "message_summary", "automation_status", `"tags"`, `"owners"`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("path=%s leaked %q: %s", path, forbidden, response.Body.String())
			}
		}
		var payload any
		if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatalf("path=%s invalid JSON", path)
		}
	}
}

func TestMaterialSendFailsClosedWithoutProviderReadyMediaID(t *testing.T) {
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: unreadyMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/sidebar/v2/send-intents", strings.NewReader(`{"resource_kind":"material","resource_id":"7"}`))
	request.Header.Set("Authorization", "Bearer signed")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "material-send-test-0001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"capability_not_ready"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMaterialsUseBoundedV3QueryAndRejectLegacyType(t *testing.T) {
	materials := &recordingMaterials{}
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: materials, MaterialSend: materials, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/materials?limit=5&offset=0&q=%E6%B5%B7%E6%8A%A5", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(materials.queries) != 1 {
		t.Fatalf("bounded materials status=%d queries=%+v body=%s", response.Code, materials.queries, response.Body.String())
	}
	query := materials.queries[0]
	if query.Limit != 5 || query.Offset != 0 || !query.EnabledOnly || query.Search != "海报" || query.Category != "" || query.Tags != "" || len(query.TagGroups) != 0 || query.OnlyUnlabeled {
		t.Fatalf("bounded materials query=%+v", query)
	}
	for _, path := range []string{
		"/api/sidebar/v2/materials?type=image&limit=5&offset=0",
		"/api/sidebar/v2/materials?type=video&limit=5&offset=0",
		"/api/sidebar/v2/materials?type=image&type=image&limit=5&offset=0",
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer signed")
		response = httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("legacy materials query path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if len(materials.queries) != 1 {
		t.Fatalf("rejected material queries reached owner: %+v", materials.queries)
	}
}

func TestMaterialVariantUsesEnabledViewerProjection(t *testing.T) {
	products := testProducts{}
	variants := &testImageVariants{variant: mediaport.ImageVariant{Content: []byte("png"), MediaType: "image/png", ETag: `"fixture"`}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, ImageVariants: variants, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/materials/7/variants/thumb_320", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || variants.calls != 1 {
		t.Fatalf("enabled variant status=%d content_type=%q calls=%d", response.Code, response.Header().Get("Content-Type"), variants.calls)
	}
	variants.err = errors.New("disabled")
	request = httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/materials/8/variants/thumb_320", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"resource_not_available"`) {
		t.Fatalf("unavailable variant status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCouponsExposeCouponRuleDirectoryWithoutCreatingShares(t *testing.T) {
	catalog := &recordingCoupons{page: couponport.SidebarClaimablePage{
		Items: []couponport.SidebarClaimableItem{
			{CouponID: 7, Name: "目录券", DiscountMinor: 990, Currency: "CNY", Targets: []couponport.SidebarClaimableTarget{{Title: "标准商品", ProductType: productport.ProductOptionStandard}}, ClaimEndsAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), PublicSlug: "coupon-7", AvailabilityStatus: "scheduled"},
			{CouponID: 8, Name: "无链接目录券", DiscountMinor: 100, Currency: "CNY", ClaimEndsAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), AvailabilityStatus: "sold_out", UserLimitReached: true},
		}, Total: 2, Limit: 2, Offset: 3,
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/coupons?limit=2&offset=3", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	testRoutesWithCoupons(t, catalog).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if catalog.customerID != 42 || catalog.query != (couponport.SidebarClaimableQuery{Limit: 2, Offset: 3}) {
		t.Fatalf("catalog scope customer=%d query=%+v", catalog.customerID, catalog.query)
	}
	var payload struct {
		Items []struct {
			CouponID           int64  `json:"coupon_id"`
			URL                string `json:"url"`
			AvailabilityStatus string `json:"availability_status"`
			UserLimitReached   bool   `json:"user_limit_reached"`
			Targets            []struct {
				Title string `json:"title"`
			} `json:"targets"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].CouponID != 7 || payload.Items[0].URL != "https://crm.example.com/c/coupon-7" || payload.Items[0].AvailabilityStatus != "scheduled" || len(payload.Items[0].Targets) != 1 || payload.Items[0].Targets[0].Title != "标准商品" {
		t.Fatalf("unexpected coupon directory=%s", response.Body.String())
	}
	if payload.Items[1].URL != "" || payload.Items[1].AvailabilityStatus != "sold_out" || !payload.Items[1].UserLimitReached {
		t.Fatalf("missing-slug or availability mapping changed=%s", response.Body.String())
	}
	for _, forbidden := range []string{"claim_id", "claimed_at", "public_slug", "eligible"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("coupon directory leaked or inferred %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestProductSendIntentUsesStandardNewsCardPayload(t *testing.T) {
	products := fixedProducts{product: productport.ProductOption{ID: 9, Code: "course-9", ProductType: productport.ProductOptionStandard, Name: "标准课程", PriceMinor: 19900, Currency: "CNY", CoverURL: "https://assets.example.test/products/course-9.png"}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handler.sendPayload(context.Background(), 42, "product", "9", productport.ProductOptionStandard)
	if err != nil {
		t.Fatal(err)
	}
	var news struct {
		MessageType string `json:"msgtype"`
		News        struct {
			Link   string `json:"link"`
			Title  string `json:"title"`
			Desc   string `json:"desc"`
			ImgURL string `json:"imgUrl"`
		} `json:"news"`
	}
	if err := json.Unmarshal(payload, &news); err != nil {
		t.Fatal(err)
	}
	if news.MessageType != "news" || news.News.Link != "https://crm.example.com/p/course-9" || news.News.Title != "标准课程" || news.News.Desc != "" || news.News.ImgURL != "https://assets.example.test/products/course-9.png" {
		t.Fatalf("standard product card payload=%s", payload)
	}
}

func TestProductSendIntentAllowsOnlyEstablishedPublicSameOriginCovers(t *testing.T) {
	for _, test := range []struct {
		name, cover, wantCover, wantDescription string
	}{
		{"admin-preview", "/api/admin/image-library/8/variants/original", "https://crm.example.com/static/sidebar_workbench/product-card-cover.png", "商品封面暂缺"},
		{"standard-detail-media", "/api/h5/product-images/course-9/8/variants/original", "https://crm.example.com/api/h5/product-images/course-9/8/variants/original", ""},
		{"service-detail-media", "/api/h5/service-period-products/course-9/images/8/variants/original", "https://crm.example.com/api/h5/service-period-products/course-9/images/8/variants/original", ""},
		{"missing", "", "https://crm.example.com/static/sidebar_workbench/product-card-cover.png", "商品封面暂缺"},
	} {
		t.Run(test.name, func(t *testing.T) {
			products := fixedProducts{product: productport.ProductOption{ID: 9, Code: "course-9", ProductType: productport.ProductOptionStandard, Name: "标准课程", CoverURL: test.cover}}
			handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := handler.sendPayload(context.Background(), 42, "product", "9", productport.ProductOptionStandard)
			if err != nil {
				t.Fatal(err)
			}
			var news struct {
				News struct {
					ImgURL string `json:"imgUrl"`
					Desc   string `json:"desc"`
				} `json:"news"`
			}
			if err = json.Unmarshal(payload, &news); err != nil {
				t.Fatal(err)
			}
			if news.News.ImgURL != test.wantCover || news.News.Desc != test.wantDescription {
				t.Fatalf("payload=%s", payload)
			}
		})
	}
}

func TestCouponSendIntentUsesExistingPublicLinkWithoutClaimOrAllocation(t *testing.T) {
	products := testProducts{}
	coupons := &recordingCoupons{item: couponport.SidebarClaimableItem{CouponID: 7, Name: "目录券", PublicSlug: "coupon-7", AvailabilityStatus: "active"}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: coupons, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handler.sendPayload(context.Background(), 42, "coupon", "7", "")
	if err != nil {
		t.Fatal(err)
	}
	if coupons.readUserID != 42 || coupons.readID != 7 {
		t.Fatalf("coupon was not read for the trusted sidebar customer: customer=%d coupon=%d", coupons.readUserID, coupons.readID)
	}
	var news struct {
		MessageType string `json:"msgtype"`
		News        struct {
			Link   string `json:"link"`
			Title  string `json:"title"`
			Desc   string `json:"desc"`
			ImgURL string `json:"imgUrl"`
		} `json:"news"`
	}
	if err := json.Unmarshal(payload, &news); err != nil {
		t.Fatal(err)
	}
	if news.MessageType != "news" || news.News.Link != "https://crm.example.com/c/coupon-7" || news.News.Title != "目录券" || news.News.Desc != "点击领取优惠券" || news.News.ImgURL != "https://crm.example.com/static/sidebar_workbench/product-card-cover.png" {
		t.Fatalf("coupon card payload=%s", payload)
	}
	// ReadSidebarClaimable is the sole coupon call. Building a card cannot
	// claim, reserve stock, or allocate an entitlement for this customer.
	if coupons.customerID != 0 || coupons.query.Limit != 0 {
		t.Fatalf("coupon share must not list, claim, or allocate: %+v", coupons)
	}
}

func TestCouponSendIntentRejectsMissingExistingPublicLink(t *testing.T) {
	products := testProducts{}
	coupons := &recordingCoupons{item: couponport.SidebarClaimableItem{CouponID: 7, Name: "目录券"}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: coupons, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.sendPayload(context.Background(), 42, "coupon", "7", ""); err == nil {
		t.Fatal("coupon without an explicitly created public link was sendable")
	}
}

type fixedSidebarProfile struct{ profile customerport.SidebarProfile }

func (fixture fixedSidebarProfile) ReadSidebarProfile(context.Context, customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	return fixture.profile, nil
}
func (fixture fixedSidebarProfile) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return fixture.profile, nil
}
func (fixture fixedSidebarProfile) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{}, nil
}

func TestWorkbenchProjectionExposesFullSafeCustomerProfile(t *testing.T) {
	now := time.Date(2026, time.September, 8, 1, 2, 3, 0, time.UTC)
	lastSynced := now.Add(-time.Hour)
	profile := fixedSidebarProfile{profile: customerport.SidebarProfile{
		CustomerID: 42, DisplayName: "客户甲", AvatarURL: "https://cdn.example/avatar.png", PhoneMasked: "138****5678", PhoneAssurance: "declared",
		Status: "active", ActivationState: "active", Gender: 2, ContactType: 1, CorpName: "示例企业", Source: "wecom_sync",
		ProfileSource: "活动报名", ProfileVersion: 3, Industry: "教育", IndustryDescription: "成人教育", NeedsBlockersFollowup: "回访", Version: 9,
		LastSyncedAt: &lastSynced, UpdatedAt: now,
	}}
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: profile, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com", CursorSigningKey: testCursorSigningKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := handler.workbenchProjection(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Profile struct {
			DisplayName           string `json:"display_name"`
			OneID                 string `json:"oneid"`
			PhoneAssurance        string `json:"phone_assurance"`
			ActivationStatus      string `json:"activation_status"`
			ContactType           int16  `json:"contact_type"`
			ProfileSource         string `json:"profile_source"`
			ProfileVersion        int64  `json:"profile_version"`
			Industry              string `json:"industry"`
			IndustryDescription   string `json:"industry_description"`
			NeedsBlockersFollowup string `json:"needs_blockers_followup"`
			LastSyncedAt          string `json:"last_synced_at"`
		} `json:"profile"`
	}
	if err = json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body.Profile.DisplayName != "客户甲" || body.Profile.OneID != "CID-42" || body.Profile.PhoneAssurance != "declared" || body.Profile.ActivationStatus != "active" || body.Profile.ContactType != 1 || body.Profile.ProfileSource != "活动报名" || body.Profile.ProfileVersion != 3 || body.Profile.Industry != "教育" || body.Profile.IndustryDescription != "成人教育" || body.Profile.NeedsBlockersFollowup != "回访" || body.Profile.LastSyncedAt == "" {
		t.Fatalf("workbench profile contract=%s", encoded)
	}
	for _, forbidden := range []string{"external_userid", "openid", "unionid", `"phone":"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("workbench profile leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestQuestionnaireCursorPreservesOwnerKeysetSnapshot(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	surveys := &pagedSurveys{items: []customerport.SurveyItem{
		{ID: 5, Title: "第五份", SubmittedAt: now.Add(-5 * time.Minute)}, {ID: 4, Title: "第四份", SubmittedAt: now.Add(-4 * time.Minute)},
		{ID: 3, Title: "第三份", SubmittedAt: now.Add(-3 * time.Minute)}, {ID: 2, Title: "第二份", SubmittedAt: now.Add(-2 * time.Minute)},
		{ID: 1, Title: "第一份", SubmittedAt: now.Add(-time.Minute)},
	}}
	// The Owner returns newest first; keep the fixture in that order so the
	// handler must only forward the Port's AfterAt/AfterID tuple.
	for left, right := 0, len(surveys.items)-1; left < right; left, right = left+1, right-1 {
		surveys.items[left], surveys.items[right] = surveys.items[right], surveys.items[left]
	}
	routes := testRoutesWithReaders(t, testProfile{}, surveys, testTimeline{}, testCoupons{})
	read := func(path string) struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total      int64  `json:"total"`
		Limit      int    `json:"limit"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer signed")
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("questionnaire page path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		var page struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			Total      int64  `json:"total"`
			Limit      int    `json:"limit"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := read("/api/sidebar/v2/questionnaires?limit=2")
	if len(first.Items) != 2 || first.Items[0].ID != 1 || first.Items[1].ID != 2 || first.Total != 5 || first.Limit != 2 || !first.HasMore || first.NextCursor == "" || len(surveys.calls) != 1 || surveys.calls[0].Limit != 3 {
		t.Fatalf("first questionnaire page=%+v calls=%+v", first, surveys.calls)
	}
	firstWatermark := surveys.calls[0].Watermark
	second := read("/api/sidebar/v2/questionnaires?limit=2&cursor=" + first.NextCursor)
	if len(second.Items) != 2 || second.Items[0].ID != 3 || second.Items[1].ID != 4 || !second.HasMore || second.NextCursor == "" || len(surveys.calls) != 2 || !surveys.calls[1].Watermark.Equal(firstWatermark) || surveys.calls[1].AfterID != 2 {
		t.Fatalf("second questionnaire page=%+v calls=%+v", second, surveys.calls)
	}
	third := read("/api/sidebar/v2/questionnaires?limit=2&cursor=" + second.NextCursor)
	if len(third.Items) != 1 || third.Items[0].ID != 5 || third.HasMore || third.NextCursor != "" || len(surveys.calls) != 3 || surveys.calls[2].AfterID != 4 {
		t.Fatalf("third questionnaire page=%+v calls=%+v", third, surveys.calls)
	}
	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/questionnaires?limit=2&cursor="+first.NextCursor+"x", nil)
	invalidRequest.Header.Set("Authorization", "Bearer signed")
	routes.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest || len(surveys.calls) != 3 {
		t.Fatalf("tampered questionnaire cursor status=%d calls=%d body=%s", invalid.Code, len(surveys.calls), invalid.Body.String())
	}
}

func TestTimelineCursorPreservesOwnerKeysetAndRejectsOtherSection(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	timeline := &pagedTimeline{items: []customerport.TimelineItem{
		{ID: 1, Title: "最新触点", OccurredAt: now.Add(-time.Minute)}, {ID: 2, Title: "次新触点", OccurredAt: now.Add(-2 * time.Minute)}, {ID: 3, Title: "较早触点", OccurredAt: now.Add(-3 * time.Minute)},
	}}
	routes := testRoutesWithReaders(t, testProfile{}, testSurveys{}, timeline, testCoupons{})
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/timeline?limit=2", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	var first struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &first) != nil || len(first.Items) != 2 || first.Items[0].ID != 1 || first.Items[1].ID != 2 || !first.HasMore || first.NextCursor == "" || len(timeline.calls) != 1 || timeline.calls[0].Limit != 3 {
		t.Fatalf("first timeline response status=%d body=%s calls=%+v", response.Code, response.Body.String(), timeline.calls)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/timeline?limit=2&cursor="+first.NextCursor, nil)
	request.Header.Set("Authorization", "Bearer signed")
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(timeline.calls) != 2 || timeline.calls[1].AfterID != 2 || !timeline.calls[1].Watermark.Equal(timeline.calls[0].Watermark) {
		t.Fatalf("second timeline response status=%d calls=%+v body=%s", response.Code, timeline.calls, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/questionnaires?limit=2&cursor="+first.NextCursor, nil)
	request.Header.Set("Authorization", "Bearer signed")
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("timeline cursor accepted by questionnaires status=%d body=%s", response.Code, response.Body.String())
	}
}
