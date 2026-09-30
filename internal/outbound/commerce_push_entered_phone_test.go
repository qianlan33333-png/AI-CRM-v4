package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

func TestCommercePaidPayloadEnteredPhone(t *testing.T) {
	payer, recipient, product := int64(1), int64(2), int64(20)
	target := CommercePushTarget{BuyerPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}, BeneficiaryPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}}
	readErr := errors.New("contact unavailable")
	for _, tc := range []struct {
		name                     string
		mobile                   *mappingMobile
		identityPhone            string
		gift, missing, wantError bool
		buyer, beneficiary       string
	}{
		{name: "entered_phone_without_verified_identity", mobile: &mappingMobile{value: "+8613800138000", found: true}, buyer: "13800138000", beneficiary: "13800138000"},
		{name: "frozen_contact_overrides_current_identity", mobile: &mappingMobile{value: "+8613800138000", found: true}, identityPhone: "13900139000", buyer: "13800138000", beneficiary: "13800138000"},
		{name: "gift_contact_is_not_payer_identity", mobile: &mappingMobile{value: "+8613800138000", found: true}, gift: true, beneficiary: "13800138000"},
		{name: "gift_keeps_payers_own_phone", mobile: &mappingMobile{value: "+8613800138000", found: true}, gift: true, identityPhone: "13900139000", buyer: "13900139000", beneficiary: "13800138000"},
		{name: "missing_contact_keeps_legacy_policy", mobile: &mappingMobile{}, missing: true},
		{name: "missing_contact_with_identity", mobile: &mappingMobile{}, identityPhone: "13900139000", buyer: "13900139000", beneficiary: "13900139000"},
		{name: "unreadable_contact_does_not_fall_back", mobile: &mappingMobile{err: readErr}, identityPhone: "13900139000", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identities := commercePushIdentityStub{}
			if tc.identityPhone != "" {
				identities[identitydomain.KindPhone] = tc.identityPhone
			}
			service := &CommercePushService{identities: identities, checkoutMobile: tc.mobile}
			beneficiary := &payer
			if tc.gift {
				beneficiary = &recipient
			}
			event := orderport.PaidEvent{OrderID: 7, OccurredAt: time.Now(), Order: orderdomain.Snapshot{PayerCustomerID: &payer, BeneficiaryCustomerID: beneficiary}}
			raw, missing, err := service.paidPayload(context.Background(), event, orderdomain.ItemSnapshot{ProductID: &product}, target, "entered-phone")
			if (err != nil) != tc.wantError || missing != tc.missing {
				t.Fatalf("missing=%t err=%v", missing, err)
			}
			if tc.wantError {
				if !errors.Is(err, readErr) {
					t.Fatal("read error was hidden")
				}
				return
			}
			if missing {
				return
			}
			var body struct {
				Phone string `json:"phone_number"`
				Buyer struct {
					Phone string `json:"phone"`
				} `json:"buyer"`
			}
			if json.Unmarshal(raw, &body) != nil || body.Phone != tc.beneficiary || body.Buyer.Phone != tc.buyer {
				t.Fatal("incorrect contact ownership or source")
			}
			if tc.mobile.calls != 1 {
				t.Fatal("order contact was not read exactly once")
			}
		})
	}
}
