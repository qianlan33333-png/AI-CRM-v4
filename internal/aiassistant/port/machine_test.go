package port

import (
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func TestMachineActorRequiresCanonicalAuthenticatedReference(t *testing.T) {
	actor, err := MachineActorFromAuthenticatedPrincipal("machine:open.review-client")
	if err != nil || !actor.Valid() || actor.Kind != MachineActorKind || actor.Reference != "machine:open.review-client" || actor.StaffID != 0 {
		t.Fatalf("actor=%+v err=%v", actor, err)
	}
	for _, value := range []string{"", "open.review-client", "machine:", "machine:client id", "machine:client\n", "machine:client:other"} {
		if _, err = MachineActorFromAuthenticatedPrincipal(value); err == nil {
			t.Fatalf("reference %q accepted", value)
		}
	}
	if (MachineActor{Kind: MachineActorKind, Reference: "machine:open.review-client", StaffID: 9}).Valid() {
		t.Fatal("machine actor accepted a numeric staff identity")
	}
}

func TestMachineCreatePlanCommandRejectsUntrustedSubject(t *testing.T) {
	actor, err := MachineActorFromAuthenticatedPrincipal("machine:review-client")
	if err != nil {
		t.Fatal(err)
	}
	command := MachineCreatePlanCommand{
		Actor: actor, IdempotencyKey: "machine-plan-command-0001", Name: "review", SourceKind: "open.review_plan.v1",
		SourceDigest: effectport.Hash("machine-plan"),
		Recipients:   []RecipientCandidate{{CustomerID: customerdomain.CustomerID(1), StaffID: 2, Content: []ContentBlock{{Kind: ContentText, Text: "hello"}}}},
		OccurredAt:   time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC),
	}
	if !command.Valid() {
		t.Fatal("valid canonical machine command rejected")
	}
	command.Actor.StaffID = 7
	if command.Valid() {
		t.Fatal("machine command accepted an actor/principal override")
	}
}
