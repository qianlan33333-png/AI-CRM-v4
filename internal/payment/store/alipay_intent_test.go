package store

import (
	"errors"
	"strings"
	"testing"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestAlipayIntentSubjectAllowsOrderPortRecoveryOnlyWhenSnapshotKeyIsMissing(t *testing.T) {
	tests := []struct {
		name     string
		snapshot string
		want     string
		wantErr  bool
	}{
		{name: "valid current snapshot", snapshot: `{"subject":"冻结标题"}`, want: "冻结标题"},
		{name: "missing old snapshot field", snapshot: `{"payment_id":7}`},
		{name: "present empty subject", snapshot: `{"subject":""}`, wantErr: true},
		{name: "present null subject", snapshot: `{"subject":null}`, wantErr: true},
		{name: "present wrong type", snapshot: `{"subject":7}`, wantErr: true},
		{name: "present subject with controls", snapshot: `{"subject":"bad\ntitle"}`, wantErr: true},
		{name: "present overlong subject", snapshot: `{"subject":"` + strings.Repeat("课", paymentport.AlipayMaxSubjectRunes+1) + `"}`, wantErr: true},
		{name: "present padded subject", snapshot: `{"subject":" 标题 "}`, wantErr: true},
		{name: "malformed snapshot", snapshot: `not-json`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := alipayIntentSubject([]byte(test.snapshot))
			if test.wantErr {
				if !errors.Is(err, paymentport.ErrConflict) {
					t.Fatalf("expected a fail-closed conflict, got subject=%q err=%v", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("subject=%q want=%q err=%v", got, test.want, err)
			}
		})
	}
}
