package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCampaignInsertValidationAndLifecycleFreeze(t *testing.T) {
	created := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	campaign := Campaign{Name: "邀请活动", State: CampaignDraft, StartsAt: created.Add(time.Hour), EndsAt: created.Add(25 * time.Hour), Version: 1, CreatedBy: 7, CreatedAt: created, UpdatedAt: created}
	if !campaign.ValidForInsert() {
		t.Fatal("new campaign must be valid for insertion before its database ID exists")
	}
	withID := campaign
	withID.ID = 1
	updated, err := withID.Update(1, "邀请活动二", "", "", "", withID.StartsAt, withID.EndsAt.Add(time.Hour), created.Add(time.Minute))
	if err != nil || updated.Version != 2 {
		t.Fatalf("draft update=%+v err=%v", updated, err)
	}
	active, err := updated.Transition(2, CampaignActive, created.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = active.Update(active.Version, "不能修改时间", "", "", "", active.StartsAt, active.EndsAt.Add(time.Hour), created.Add(3*time.Hour)); !errors.Is(err, ErrTransition) {
		t.Fatalf("active campaign update error=%v", err)
	}
	if edited, editErr := active.Update(active.Version, "可修改文案", "", "活动介绍", "奖励说明", active.StartsAt, active.EndsAt, created.Add(3*time.Hour)); editErr != nil || edited.Description != "活动介绍" {
		t.Fatalf("active copy update=%+v err=%v", edited, editErr)
	}
	if active.EffectiveState(active.EndsAt) != CampaignEnded || active.AcceptingAt(active.EndsAt) {
		t.Fatalf("deadline state=%s accepting=%t", active.EffectiveState(active.EndsAt), active.AcceptingAt(active.EndsAt))
	}
}

func TestImmutableFactsRejectMalformedStates(t *testing.T) {
	at := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	if (Participation{ID: 1, CampaignID: 1, CustomerID: 2, TeamID: 3, InvitationID: 0, InviterCustomerID: 1, State: ParticipationActive, JoinedAt: at}).Valid() {
		t.Fatal("direct participation cannot carry an inviter without an invitation")
	}
	if (ScoreEvent{ID: 1, CampaignID: 1, ParticipationID: 1, InviterCustomerID: 2, TeamID: 3, Delta: 1, Kind: ScoreReverse, ReversesScoreEventID: 1, Reason: "reason", OccurredAt: at}).Valid() {
		t.Fatal("reversal must use a negative immutable delta")
	}
	if !(ScoreEvent{ID: 1, CampaignID: 1, ParticipationID: 1, InviterCustomerID: 2, TeamID: 3, Delta: -1, Kind: ScoreReverse, ReversesScoreEventID: 9, Reason: "reason", OccurredAt: at}).Valid() {
		t.Fatal("complete reversal fact must validate")
	}
}
