package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestWelcomeMessageTemplateOnlyExpandsExactCustomerNameMarker(t *testing.T) {
	message, err := RenderWelcomeMessage("欢迎{{客户名}}，{{客户名}}", "测试{{不解析}}")
	if err != nil || message != "欢迎测试{{不解析}}，测试{{不解析}}" {
		t.Fatalf("message=%q err=%v", message, err)
	}
	fallback, err := RenderWelcomeMessage("欢迎{{客户名}}", " \t")
	if err != nil || fallback != "欢迎朋友" {
		t.Fatalf("fallback=%q err=%v", fallback, err)
	}
	for _, malformed := range []string{"欢迎{{姓名}}", "欢迎{{ 客户名 }}", "欢迎{{客户名", "欢迎客户名}}"} {
		if err := ValidateWelcomeMessageTemplate(malformed); !errors.Is(err, ErrInvalidWelcomeTemplate) {
			t.Fatalf("template %q err=%v", malformed, err)
		}
	}
}

func TestRenderedWelcomeMessageUsesProviderRuneLimitAfterRepeatedChineseExpansion(t *testing.T) {
	withinLimit, err := RenderWelcomeMessage(strings.Repeat("甲", WelcomeMessageMaxRunes-2)+WelcomeCustomerNameVariable+WelcomeCustomerNameVariable, "春")
	if err != nil || ValidateRenderedWelcomeMessage(withinLimit) != nil || len([]rune(withinLimit)) != WelcomeMessageMaxRunes {
		t.Fatalf("within limit runes=%d render=%v validate=%v", len([]rune(withinLimit)), err, ValidateRenderedWelcomeMessage(withinLimit))
	}
	overLimit, err := RenderWelcomeMessage(strings.Repeat("甲", WelcomeMessageMaxRunes-1)+WelcomeCustomerNameVariable+WelcomeCustomerNameVariable, "春")
	if err != nil || !errors.Is(ValidateRenderedWelcomeMessage(overLimit), ErrWelcomeMessageTooLong) || len([]rune(overLimit)) != WelcomeMessageMaxRunes+1 {
		t.Fatalf("over limit runes=%d render=%v validate=%v", len([]rune(overLimit)), err, ValidateRenderedWelcomeMessage(overLimit))
	}
}

func TestValidateConfigPreservesTemplateSpecificError(t *testing.T) {
	config := validCatalogConfig()
	config.WelcomeMessage = "{{未知变量}}"
	err := ValidateConfig(config)
	if !errors.Is(err, ErrInvalidChannel) || !errors.Is(err, ErrInvalidWelcomeTemplate) {
		t.Fatalf("err=%v", err)
	}
}
