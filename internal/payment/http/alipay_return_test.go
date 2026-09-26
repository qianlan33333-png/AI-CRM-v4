package paymenthttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAlipayReturnUsesSessionAuthorizedStatusOnly(t *testing.T) {
	app := &appStub{}
	handler, err := NewHandler(app, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/pay/alipay/return?out_trade_no=M-return&trade_status=TRADE_SUCCESS", nil))
	if result.Code != 200 || result.Header().Get("Cache-Control") != "no-store" || app.createCalls != 0 {
		t.Fatalf("status=%d creates=%d", result.Code, app.createCalls)
	}
	command := exec.Command("node", filepath.Join("testdata", "alipay_return_journey.mjs"))
	command.Stdin = bytes.NewReader(result.Body.Bytes())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("return journey: %v\n%s", err, output)
	}
}
