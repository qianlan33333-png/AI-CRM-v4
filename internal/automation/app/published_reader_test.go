package app

import (
	"context"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

func TestPublishedAgentReaderReturnsOpaqueVersionedDigests(t *testing.T) {
	store := newAgentTestStore(automationport.Agent{ID: 7, AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusActive, PublishedVersion: 3, PublishedTaskPrompt: "private", FixedContentPackage: automationport.FixedContentPackage{ContentText: "message", ImageLibraryIDs: []int64{9}}})
	service := NewAgentService(agentTestUOW{}, store, &agentTestEvents{})
	first, found, err := service.PublishedAgent(context.Background(), 7)
	if err != nil || !found || first.PublishedVersion != 3 || first.ContentDigest == ([32]byte{}) || first.MaterialsDigest == ([32]byte{}) {
		t.Fatalf("published=%+v found=%v err=%v", first, found, err)
	}
	second, _, _ := service.PublishedAgent(context.Background(), 7)
	if first.ContentDigest != second.ContentDigest || first.MaterialsDigest != second.MaterialsDigest {
		t.Fatal("published digests are not stable")
	}
}
