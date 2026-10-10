package paymenthttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	// The isolated backend checkout has no frontend lane's node_modules.
	// Reuse the existing lockfile-bound preparation; no Host build is needed.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate return journey source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prepare := exec.CommandContext(ctx, "python3", "scripts/ci/check_preparation.py", "npm")
	prepare.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "AICRM_DATABASE_URL=") {
			prepare.Env = append(prepare.Env, value)
		}
	}
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare return journey dependencies: %v\n%s", err, output)
	}
	command := exec.Command("node", filepath.Join("testdata", "alipay_return_journey.mjs"))
	command.Stdin = bytes.NewReader(result.Body.Bytes())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("return journey: %v\n%s", err, output)
	}
}
