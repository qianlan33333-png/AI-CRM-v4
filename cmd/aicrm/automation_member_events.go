package main

import (
	"context"
	"errors"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

// automationMemberEventSink is a composition-only adapter. Segment publishes
// a canonical versioned event; Automation owns enrollment and action state.
type automationMemberEventSink struct{ runtime *automationapp.RuntimeService }

func (s automationMemberEventSink) HandleAudienceMemberEntered(ctx context.Context, event segmentport.MemberEnteredV1) error {
	if s.runtime == nil {
		return errors.New("automation member-event sink is unavailable")
	}
	_, err := s.runtime.EnrollAudienceMember(ctx, event)
	return err
}
