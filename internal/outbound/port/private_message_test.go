package port_test

import (
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func TestPrivateMessageIntentRequiresFourDigests(t *testing.T) {
	command := outboundport.PrivateMessageIntentCommand{
		SourceReference:  "plan:1:recipient:2",
		CustomerID:       customerdomain.CustomerID(3),
		StaffID:          4,
		PayloadReference: "content-version:5",
		SourceDigest:     effectport.Hash("source"),
		TargetDigest:     effectport.Hash("target"),
		PayloadDigest:    effectport.Hash("payload"),
		PolicyHash:       effectport.Hash("policy"),
		ReceiptKey:       effectport.Hash("receipt"),
	}
	if !command.Valid() {
		t.Fatal("expected valid private-message intent")
	}
	command.TargetDigest = ""
	if command.Valid() {
		t.Fatal("target digest is mandatory")
	}
}
