package main

import (
	"context"
	"encoding/base64"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestCommercePushTargetsAreProtectedTypedWhitelist(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte("fixture-signing-key"))
	raw := `{"product-paid":{"slot":"paid","endpoint":"https://push.example.test","signing_key":"` + key + `","version":"legacy-v1","tenant_id":"aicrm","buyer_id":{"kind":"wecom_external_userid","scope":"wecom-corp:main"},"buyer_openid":{"kind":"mp_openid","scope":"wechat-app:mp"},"buyer_unionid":{"kind":"unionid","scope":"wechat-open-platform:main"},"buyer_phone":{"kind":"phone","scope":"phone:cn11"},"beneficiary_phone":{"kind":"phone","scope":"phone:cn11"},"type":"member_open","remark":"fixture","custom_params":{"campaign":"control"}}}`
	resolver, err := commercePushTargetsFromRuntime(platformconfig.CommercePush{ProviderEnabled: true, TargetsJSON: raw})
	if err != nil || !resolver.CommercePushProviderEnabled() {
		t.Fatalf("resolver err=%v enabled=%t", err, resolver.CommercePushProviderEnabled())
	}
	first, found, err := resolver.CommercePushTarget(context.Background(), "product-paid")
	if err != nil || !found || string(first.SigningKey) != "fixture-signing-key" || first.Endpoint != "https://push.example.test" {
		t.Fatalf("target=%+v found=%t err=%v", first, found, err)
	}
	if first.PushType != "" || first.Remark != "" || first.Day != nil || first.Frequency != nil || len(first.CustomParams) != 0 {
		t.Fatalf("runtime target retained Product-owned business parameters: %+v", first)
	}
	first.SigningKey[0] = 'X'
	second, found, err := resolver.CommercePushTarget(context.Background(), "product-paid")
	if err != nil || !found || string(second.SigningKey) != "fixture-signing-key" {
		t.Fatalf("target leaked mutable protected data=%+v found=%t err=%v", second, found, err)
	}
	if _, found, err = resolver.CommercePushTarget(context.Background(), "unknown"); err != nil || found {
		t.Fatalf("unknown target found=%t err=%v", found, err)
	}
}

func TestCommercePushTargetsRejectInvalidProtectedConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{"target":{"slot":"paid","endpoint":"https://push.example.test"}}`,
		`{"target":{"slot":"paid","endpoint":"http://127.0.0.1","version":"v1","buyer_id":{"kind":"wecom_external_userid","scope":"wecom-corp:main"},"buyer_openid":{"kind":"mp_openid","scope":"wechat-app:mp"},"buyer_unionid":{"kind":"unionid","scope":"wechat-open-platform:main"},"buyer_phone":{"kind":"phone","scope":"phone:cn11"},"beneficiary_phone":{"kind":"phone","scope":"phone:cn11"}}}`,
		`{"target":{"slot":"paid","endpoint":"https://push.example.test","version":"v1","buyer_id":{"kind":"wecom_external_userid","scope":"bad"},"buyer_openid":{"kind":"mp_openid","scope":"wechat-app:mp"},"buyer_unionid":{"kind":"unionid","scope":"wechat-open-platform:main"},"buyer_phone":{"kind":"phone","scope":"phone:cn11"},"beneficiary_phone":{"kind":"phone","scope":"phone:cn11"}}}`,
	} {
		if _, err := commercePushTargetsFromRuntime(platformconfig.CommercePush{ProviderEnabled: true, TargetsJSON: raw}); err == nil {
			t.Fatalf("accepted invalid protected target %s", raw)
		}
	}
}
