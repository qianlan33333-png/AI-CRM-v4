package app

import (
	"context"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type manualAudienceRunApprovalReader interface {
	RunByAIPlanWithin(context.Context, int64) (automationport.ManualAudienceRunApprovalSource, bool, error)
}

// ValidateManualAudienceRunApproval rechecks only Survey's retired owner
// selection at the two lifecycle points that matter for a pending manual run:
// its frozen preview configuration and the package's current configuration.
// It is called inside AI Assistant's acceptance transaction.
func (s *RuntimeService) ValidateManualAudienceRunApproval(ctx context.Context, planID aiassistantport.PlanID) error {
	if s == nil || planID < 1 {
		return ErrRuntimeUnavailable
	}
	reader, ok := s.store.(manualAudienceRunApprovalReader)
	if !ok || s.audiences == nil {
		return ErrRuntimeUnavailable
	}
	linked, found, err := reader.RunByAIPlanWithin(ctx, int64(planID))
	if err != nil {
		return ErrRuntimeUnavailable
	}
	if !found || linked.AIPlanID != int64(planID) || linked.State != automationport.RunPendingReview || linked.PackageID < 1 || linked.SnapshotID < 1 || linked.ConfigurationVersionID < 1 {
		return ErrRuntimeNotReady
	}
	retiredReader, ok := s.audiences.(segmentport.RetiredSurveyConfigurationReader)
	if !ok {
		return ErrRuntimeUnavailable
	}
	retired, err := retiredReader.SurveyOwnerSelectionRetiredAtWithin(ctx, linked.ConfigurationVersionID)
	if err != nil {
		return ErrRuntimeUnavailable
	}
	if retired {
		return ErrRuntimeNotReady
	}
	retired, err = retiredReader.SurveyOwnerSelectionRetiredForPackageWithin(ctx, linked.PackageID)
	if err != nil {
		return ErrRuntimeUnavailable
	}
	if retired {
		return ErrRuntimeNotReady
	}
	return nil
}
