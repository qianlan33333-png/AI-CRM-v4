package config

import "testing"

func TestRuntimeCatalogRetainsTwelveLegacyCategoriesAndReferencePresence(t *testing.T) {
	catalog := RuntimeCatalog(map[string]bool{
		"environment://AICRM_WECOM_SECRET":                 true,
		"environment://AICRM_AI_GENERATION_API_KEY":        true,
		"environment://AICRM_WECHAT_SHOP_APP_SECRET":       true,
		"environment://AICRM_WECHAT_SHOP_CALLBACK_TOKEN":   true,
		"environment://AICRM_WECHAT_SHOP_CALLBACK_AES_KEY": false,
	})
	if len(catalog) != 12 || catalog[0].Key != "wecom_base" || catalog[11].Key != "wechat_oauth" {
		t.Fatalf("catalog categories=%#v", catalog)
	}
	var secretConfigured, apiBase, workerLimit, scopeBound, shopAppSecret, shopToken, shopAES, generationKey, generationEndpoint, accessManaged bool
	for _, category := range catalog {
		if category.Key == "admin_access" {
			accessManaged = category.ManagedURL == "/admin/config/login-access"
		}
		for _, field := range category.Fields {
			switch field.Key {
			case "WECOM_SECRET":
				secretConfigured = field.Configured != nil && *field.Configured
			case "WECOM_API_BASE":
				apiBase = field.Input == "deployment" && field.Unsupported != ""
			case "stability.worker_limit":
				workerLimit = field.Label == "Inbox 单次 claim 处理条数"
			case "wecom.corp_id":
				scopeBound = field.Input == "scope-bound"
			case "WECHAT_SHOP_APP_SECRET":
				shopAppSecret = field.Configured != nil && *field.Configured
			case "WECHAT_SHOP_CALLBACK_TOKEN":
				shopToken = field.Configured != nil && *field.Configured
			case "WECHAT_SHOP_CALLBACK_AES_KEY":
				shopAES = field.Configured != nil && *field.Configured
			case "AICRM_AI_GENERATION_API_KEY":
				generationKey = field.Configured != nil && *field.Configured
			case "AICRM_AI_GENERATION_BASE_URL":
				generationEndpoint = field.Input == "deployment" && field.Unsupported != ""
			}
		}
	}
	if !secretConfigured || !apiBase || !workerLimit || !scopeBound || !shopAppSecret || !shopToken || shopAES || !generationKey || !generationEndpoint || !accessManaged {
		t.Fatalf("catalog presence/field mapping secret=%t apiBase=%t worker=%t scope=%t shop app/token/aes=%t/%t/%t generation=%t/%t access_managed=%t", secretConfigured, apiBase, workerLimit, scopeBound, shopAppSecret, shopToken, shopAES, generationKey, generationEndpoint, accessManaged)
	}
}
