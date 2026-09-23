package paymenthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type receiverRecoveryHTTPStub struct {
	appStub
	command paymentport.ProfitSharingReceiverRecoveryCommand
	calls   int
	err     error
}

type reconciliationPreviewHTTPStub struct {
	appStub
	preview      paymentport.PaymentReconciliationPreview
	previewID    int64
	previewCalls int
}

func (stub *reconciliationPreviewHTTPStub) PreviewReconcileWeChatPayPayment(_ context.Context, paymentID int64) (paymentport.PaymentReconciliationPreview, error) {
	stub.previewCalls++
	stub.previewID = paymentID
	return stub.preview, nil
}

func (stub *receiverRecoveryHTTPStub) RecoverProfitSharingReceiver(_ context.Context, command paymentport.ProfitSharingReceiverRecoveryCommand) (paymentport.ReceiverReadiness, error) {
	stub.calls++
	stub.command = command
	if stub.err != nil {
		return paymentport.ReceiverReadiness{}, stub.err
	}
	return paymentport.ReceiverReadiness{Reference: command.ReceiverReference, State: "accepted", EffectRef: "eer_42", UpdatedAt: time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)}, nil
}

func TestProfitSharingReceiverRecoveryHTTPUsesCSRFAdminIdempotencyAndSafeResponse(t *testing.T) {
	application := &receiverRecoveryHTTPStub{}
	handler, err := NewHandler(application, nil, recoverySecurityStub{principal: accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/profit-sharing/receivers/psrecv_7/recover", strings.NewReader(`{"evidence_reference":"review-2026-09-14"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "receiver-recovery-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || application.calls != 1 || application.command.ReceiverReference != "psrecv_7" || application.command.ActorAdminUserID != 1 || application.command.EvidenceReference != "review-2026-09-14" || strings.Contains(response.Body.String(), "effect") {
		t.Fatalf("status=%d command=%+v body=%s", response.Code, application.command, response.Body.String())
	}
}

func TestProfitSharingReceiverRecoveryHTTPRequiresSuperAdminBeforeApplication(t *testing.T) {
	application := &receiverRecoveryHTTPStub{}
	handler, err := NewHandler(application, nil, recoverySecurityStub{principal: accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/profit-sharing/receivers/psrecv_7/recover", strings.NewReader(`{"evidence_reference":"review-2026-09-14"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "receiver-recovery-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || application.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, application.calls, response.Body.String())
	}
}

func TestProfitSharingReceiverRecoveryHTTPRejectsInvalidOrUnavailableCommandsBeforeApplication(t *testing.T) {
	for _, test := range []struct {
		name   string
		writes bool
		path   string
		body   string
		key    string
		want   int
	}{
		{name: "disabled", writes: false, path: "psrecv_7", body: `{"evidence_reference":"review"}`, key: "receiver-recovery-key-0001", want: http.StatusServiceUnavailable},
		{name: "leading zero", writes: true, path: "psrecv_07", body: `{"evidence_reference":"review"}`, key: "receiver-recovery-key-0001", want: http.StatusBadRequest},
		{name: "missing key", writes: true, path: "psrecv_7", body: `{"evidence_reference":"review"}`, want: http.StatusBadRequest},
		{name: "unknown json", writes: true, path: "psrecv_7", body: `{"other":"review"}`, key: "receiver-recovery-key-0001", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &receiverRecoveryHTTPStub{}
			handler, err := NewHandler(application, nil, recoverySecurityStub{principal: accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}, test.writes)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/profit-sharing/receivers/"+test.path+"/recover", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", test.key)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || application.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, application.calls, response.Body.String())
			}
		})
	}
}

func TestPaymentReconcileDryRunUsesReadOnlyPreview(t *testing.T) {
	application := &reconciliationPreviewHTTPStub{preview: paymentport.PaymentReconciliationPreview{PaymentID: 9, WouldRestorePaidConfirmation: true, Reason: "payment_confirmation_missing"}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/payments/9/reconcile", strings.NewReader(`{"dry_run":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "payment-reconcile-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || application.previewCalls != 1 || application.previewID != 9 || !strings.Contains(response.Body.String(), `"would_restore_paid_confirmation":true`) || strings.Contains(response.Body.String(), "merchant_order_no") {
		t.Fatalf("status=%d calls=%d id=%d body=%s", response.Code, application.previewCalls, application.previewID, response.Body.String())
	}
}
