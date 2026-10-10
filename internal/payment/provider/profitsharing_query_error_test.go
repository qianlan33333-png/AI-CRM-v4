package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
)

func TestProfitSharingOriginalQueryDiagnosticRedactsSDKData(t *testing.T) {
	for _, row := range []struct {
		status     int
		code, want string
	}{
		{400, "PARAM_ERROR", "PARAM_ERROR"}, {400, "INVALID_REQUEST", "INVALID_REQUEST"},
		{401, "SIGN_ERROR", "SIGN_ERROR"}, {403, "NO_AUTH", "NO_AUTH"},
		{404, "RESOURCE_NOT_EXISTS", "RESOURCE_NOT_EXISTS"}, {429, "FREQUENCY_LIMITED", "FREQUENCY_LIMITED"},
		{500, "SYSTEM_ERROR", "SYSTEM_ERROR"}, {200, "RESOURCE_NOT_EXISTS", "UNKNOWN"},
		{500, "RESOURCE_NOT_EXISTS", "UNKNOWN"}, {404, "secret-openid", "UNKNOWN"},
	} {
		t.Run(fmt.Sprintf("%d_%s", row.status, row.code), func(t *testing.T) {
			raw := &core.APIError{StatusCode: row.status, Code: row.code, Message: "secret-openid", Body: "secret-token", Header: http.Header{"Authorization": {"secret-key"}}, Detail: map[string]string{"account": "secret-account"}}
			err := classifyOfficialProfitSharingQueryError(fmt.Errorf("SDK request: %w", raw))
			if got := paymentport.ProfitSharingQueryCode(err); got != row.want || !errors.Is(err, paymentport.ErrUnavailable) {
				t.Fatalf("diagnostic code=%q want=%q", got, row.want)
			}
			var retained *core.APIError
			if errors.As(err, &retained) || strings.Contains(err.Error(), "secret-") {
				t.Fatal("query diagnostic retained provider data")
			}
		})
	}
	if got := paymentport.ProfitSharingQueryCode(classifyOfficialProfitSharingQueryError(errors.New("transport secret-token"))); got != "UNKNOWN" {
		t.Fatalf("transport code=%q", got)
	}
}
