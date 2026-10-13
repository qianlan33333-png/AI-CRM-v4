package http

import (
	"context"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type shopDisplayStub struct{ calls int }

func (s *shopDisplayStub) ReadShopBuyerDisplays(_ context.Context, ids []int64) (map[int64]orderport.ShopBuyerDisplay, error) {
	s.calls++
	return map[int64]orderport.ShopBuyerDisplay{ids[0]: {Nickname: "小店昵称", PhoneMasked: "138****8000"}}, nil
}
func TestShopBuyerDisplayBeforeAndAfterCustomerLink(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "linked"}[linked], func(t *testing.T) {
			order := sampleOrder()
			order.Provider = orderdomain.ProviderWeChatShop
			order.RecordOrigin = orderdomain.RecordOriginExternal
			if !linked {
				order.PayerCustomerID = nil
				order.BeneficiaryCustomerID = nil
			}
			application := &appStub{page: orderport.Page{Items: []orderdomain.Snapshot{order}}, detail: order}
			h, _ := NewHandler(application, adminSecurity(), customerDisplaysStub{11: {DisplayName: "客户档案姓名", CustomerNumber: "1023798"}})
			reader := &shopDisplayStub{}
			_ = h.SetShopBuyerDisplayReader(reader)
			for _, url := range []string{"/api/admin/orders", "/api/admin/orders/1"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
				body := w.Body.String()
				if w.Code != 200 || !strings.Contains(body, `"payer_name":"小店昵称"`) || !strings.Contains(body, `"payer_phone_masked":"138****8000"`) || strings.Contains(body, "13800138000") {
					t.Fatal("shop display not populated")
				}
				if linked != strings.Contains(body, `"payer_customer_number":"1023798"`) {
					t.Fatal("display invented/lost customer association")
				}
			}
			application.page.Items[0] = sampleOrder()
			application.detail = sampleOrder()
			for _, url := range []string{"/api/admin/orders", "/api/admin/orders/1"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
				if w.Code != 200 || !strings.Contains(w.Body.String(), "客户档案姓名") {
					t.Fatal("native order changed")
				}
			}
			if reader.calls != 2 {
				t.Fatal("shop metadata queried for native orders")
			}
		})
	}
}
