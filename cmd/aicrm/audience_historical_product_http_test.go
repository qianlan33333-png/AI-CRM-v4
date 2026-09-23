package main

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type historicalConfigurationCapture struct {
	segmenthttp.ConfigurationApplication
	raw json.RawMessage
}

func (c *historicalConfigurationCapture) PutConfiguration(_ context.Context, in segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	c.raw = in.Definition
	return segmentdomain.ConfigurationVersion{ID: 1, PackageID: in.PackageID, Version: 1}, nil
}

func TestHistoricalAudienceProductConfigurationUsesOrderOwnerTransaction(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	pool, err := pg.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := pg.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := orderstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = native.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','commerce-history','fixture','fixture',100,'CNY','paid','history',false,$1,now(),now()) RETURNING id`, make([]byte, 32)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,'retired-code','Old title',100,1,100)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.HistoricalAudienceProductCodeExists(ctx, "retired-code"); err == nil {
		t.Fatal("unbound Owner read must fail")
	}
	capture := &historicalConfigurationCapture{}
	handler, err := segmenthttp.NewHandler(capture, commerceFundsSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	handler.BindAudienceProductReferences(audienceProductReferenceAdapter{products: &audienceProductOptionsStub{}, historical: repo, uow: uow})
	for _, test := range []struct {
		value  string
		status int
	}{{"retired-code", 200}, {"Old title", 422}, {"retired", 422}, {"unknown", 422}} {
		body := `{"expected_package_version":1,"refresh_mode":"every_3m","definition":{"schema_version":1,"template_key":"paid_order","parameters":{"product_codes":["` + test.value + `"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}}}`
		req := httptest.NewRequest(http.MethodPut, "/api/admin/ai-audience/packages/1/configuration", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "historical-code-test-"+test.value)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != test.status {
			t.Fatalf("reference %q status %d wanted %d", test.value, rec.Code, test.status)
		}
	}
}
