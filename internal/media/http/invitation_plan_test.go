package http

import (
	"bytes"
	"strings"
	"testing"

	mediaPort "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestInvitationPublicTemplateExplainsChannelWelcomeBoundary(t *testing.T) {
	var body bytes.Buffer
	err := invitationPublicTemplate.Execute(&body, mediaPort.InvitationPlan{Title: "测试邀请", Description: "描述"})
	if err != nil {
		t.Fatal(err)
	}
	page := body.String()
	for _, want := range []string{"仅用于加入群聊", "不触发渠道欢迎语或入渠标签", "渠道码中心的渠道二维码或获客链接"} {
		if !strings.Contains(page, want) {
			t.Fatalf("public invitation page missing boundary notice %q: %s", want, page)
		}
	}
}
