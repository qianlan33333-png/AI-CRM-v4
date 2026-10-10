package config

import "testing"

func TestLoadPaymentRefundRequiresExplicitBoundCommand(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgresql://fixture:fixture@localhost/fixture")
	t.Setenv("AICRM_ROLE", string(RolePaymentRefund))
	fields := map[string]string{"AICRM_PAYMENT_REFUND_ID": "1244", "AICRM_PAYMENT_REFUND_ORDER_ID": "5626", "AICRM_PAYMENT_REFUND_AMOUNT_MINOR": "680", "AICRM_PAYMENT_REFUND_KEY": "order-5626-refund-680-v1", "AICRM_PAYMENT_REFUND_AUTHORIZATION": "user-task-fixture"}
	for k, v := range fields {
		t.Setenv(k, v)
	}
	cfg, err := Load()
	if err != nil || cfg.PaymentRefundID != 1244 || cfg.PaymentRefundOrderID != 5626 || cfg.PaymentRefundAmountMinor != 680 {
		t.Fatalf("bound refund config=%+v err=%v", cfg.Role, err)
	}
	for k := range fields {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "")
			if _, err := Load(); err == nil {
				t.Fatal("missing explicit refund parameter accepted")
			}
		})
	}
	for _, v := range []string{"0", "-1", "680.0", " 680", "680\n"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AICRM_PAYMENT_REFUND_AMOUNT_MINOR", v)
			if _, err := Load(); err == nil {
				t.Fatal("invalid minor amount accepted")
			}
		})
	}
}
