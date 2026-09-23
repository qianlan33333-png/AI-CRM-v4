package port

import (
	"strings"
	"testing"
)

func TestValidateGroupOpsMaterialPlanEnforcesLegacyCountsAndStableOrder(t *testing.T) {
	valid := GroupOpsMaterialPlan{References: []GroupOpsMaterialReference{
		{Kind: "image", ID: 1}, {Kind: "image", ID: 2}, {Kind: "image", ID: 3},
		{Kind: "miniprogram", ID: 4}, {Kind: "attachment", ID: 5}, {Kind: "attachment", ID: 6},
		{Kind: "attachment", ID: 7}, {Kind: "attachment", ID: 8}, {Kind: "group_invite", ID: 9},
	}}
	if err := ValidateGroupOpsMaterialPlan(valid); err != nil {
		t.Fatalf("valid plan err=%v", err)
	}
	if err := ValidateGroupOpsMaterialPlan(GroupOpsMaterialPlan{References: append(valid.References, GroupOpsMaterialReference{Kind: "attachment", ID: 10})}); err == nil {
		t.Fatal("ten attachments must fail")
	}
	if err := ValidateGroupOpsMaterialPlan(GroupOpsMaterialPlan{References: []GroupOpsMaterialReference{{Kind: "image", ID: 1}, {Kind: "image", ID: 1}}}); err == nil {
		t.Fatal("duplicate stable reference must fail")
	}
}

func TestValidateGroupOpsProviderAttachmentUsesUTF8ByteLimits(t *testing.T) {
	title128 := strings.Repeat("中", 42) + "ab"
	attachment := GroupOpsProviderReadyAttachment{MsgType: "link", Title: title128, URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"}
	if len(title128) != 128 || ValidateGroupOpsProviderReadyAttachments([]GroupOpsProviderReadyAttachment{attachment}) != nil {
		t.Fatalf("128 byte Chinese title rejected: bytes=%d", len(title128))
	}
	attachment.Title += "x"
	if ValidateGroupOpsProviderReadyAttachments([]GroupOpsProviderReadyAttachment{attachment}) == nil {
		t.Fatal("129 byte Chinese title accepted")
	}
}
