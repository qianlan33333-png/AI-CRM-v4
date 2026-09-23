package domain

import (
	"errors"
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	"strings"
	"time"
)

var ErrInvitationPlan = errors.New("invalid invitation plan")

func ValidateInvitationInput(v p.InvitationInput) error {
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 200 || strings.TrimSpace(v.Title) == "" || len(v.Title) > 128 || len(v.Description) > 512 || v.CoverImageID < 0 || len(v.ChatIDs) == 0 || len(v.ChatIDs) > 200 {
		return ErrInvitationPlan
	}
	if v.Mode == "single" {
		if len(v.ChatIDs) != 1 || v.Threshold != nil {
			return ErrInvitationPlan
		}
	} else if v.Mode == "sequence" {
		if v.Threshold == nil || *v.Threshold < 1 || *v.Threshold > 200 {
			return ErrInvitationPlan
		}
	} else {
		return ErrInvitationPlan
	}
	seen := map[string]bool{}
	for _, id := range v.ChatIDs {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || seen[id] {
			return ErrInvitationPlan
		}
		seen[id] = true
	}
	return nil
}

// EvaluateInvitation only advances; a retired binding never becomes eligible
// merely because its observed member count dropped or the threshold changed.
func EvaluateInvitation(plan p.InvitationPlan, groups map[string]g.CatalogGroup, now time.Time) p.InvitationPlan {
	plan.Bindings = append([]p.InvitationBinding(nil), plan.Bindings...)
	plan.CurrentChatID = ""
	if !plan.Enabled {
		plan.State = "paused"
		return plan
	}
	threshold := 200
	if plan.Mode == "sequence" && plan.Threshold != nil {
		threshold = *plan.Threshold
	}
	for i, b := range plan.Bindings {
		if b.Retired {
			continue
		}
		fact, ok := groups[b.ChatID]
		if !ok || fact.ObservedAt == nil || now.Sub(*fact.ObservedAt) > 10*time.Minute {
			plan.State = "stale"
			return plan
		}
		if fact.MemberCount >= threshold {
			if plan.Mode == "sequence" {
				plan.Bindings[i].Retired = true
				continue
			}
			plan.State = "full"
			return plan
		}
		if b.CodeState != "executed" && b.CodeState != "reconciled" {
			plan.State = "preparing"
			return plan
		}
		if b.QRCode == "" {
			plan.State = "preparing"
			return plan
		}
		plan.State = "active"
		plan.CurrentChatID = b.ChatID
		return plan
	}
	plan.State = "full"
	return plan
}
