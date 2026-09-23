package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var ErrStaffDirectoryRefreshNotReady = errors.New("wecom staff directory refresh is not ready")

type StaffDirectoryRefreshRun struct {
	ID       int64
	RunKey   string
	Trigger  string
	State    string
	Attempts int
}

type StaffDirectoryRefreshStore interface {
	Begin(context.Context, string, string, time.Time) (StaffDirectoryRefreshRun, bool, error)
	Succeed(context.Context, int64, accessport.WeComStaffProjectionResult, [sha256.Size]byte, time.Time) error
	Fail(context.Context, int64, string, bool, time.Time) error
}

type StaffDirectoryRefreshService struct {
	Enabled   bool
	Provider  wecomport.DirectoryProvider
	Projector accessport.WeComStaffProjector
	// DisplayNames is optional trusted migration input. The recurring Provider
	// endpoint supplies user IDs only, so scheduled refreshes preserve existing
	// names and create a safe fallback label for newly discovered staff.
	DisplayNames map[string]string
	Store        StaffDirectoryRefreshStore
	Audit        interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	UOW platformport.UnitOfWork
	Now func() time.Time
}

func (service StaffDirectoryRefreshService) Ready() bool {
	return service.Provider != nil && service.Projector != nil && service.Store != nil && service.Audit != nil && service.UOW != nil
}

func (service StaffDirectoryRefreshService) Refresh(ctx context.Context, runKey, trigger string, terminal bool) error {
	if !service.Enabled {
		return nil
	}
	if !service.Ready() || !service.Provider.DirectoryReady() || !validStaffRefreshKey(runKey) || (trigger != "initial" && trigger != "periodic" && trigger != "manual") {
		return ErrStaffDirectoryRefreshNotReady
	}
	now := service.now()
	var run StaffDirectoryRefreshRun
	var replay bool
	if err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var beginErr error
		run, replay, beginErr = service.Store.Begin(txContext, runKey, trigger, now)
		return beginErr
	}); err != nil || replay {
		return err
	}
	providerIDs, err := service.Provider.ListContactStaff(ctx)
	if err != nil {
		return errors.Join(err, service.recordStaffRefreshFailure(ctx, run.ID, "provider_read_failed", terminal))
	}
	canonical, err := canonicalStaffDirectory(providerIDs)
	if err != nil {
		return errors.Join(err, service.recordStaffRefreshFailure(ctx, run.ID, "provider_response_invalid", terminal))
	}
	projections := make([]accessport.WeComStaffProjection, 0, len(canonical))
	for _, providerID := range canonical {
		projections = append(projections, accessport.WeComStaffProjection{WeComUserID: providerID, DisplayName: strings.TrimSpace(service.DisplayNames[providerID])})
	}
	digest := staffDirectoryDigest(canonical)
	return service.UOW.Within(ctx, func(txContext context.Context) error {
		result, projectErr := service.Projector.ProjectWeComStaffWithin(txContext, runKey, projections, now)
		if projectErr != nil {
			return projectErr
		}
		if err := service.Store.Succeed(txContext, run.ID, result, digest, now); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]int64{"discovered": result.Discovered, "created": result.Created, "existing": result.Existing, "inactive": result.Inactive})
		key, keyErr := idempotency.Parse("wecom-staff-refresh:" + runKey)
		if keyErr != nil {
			return keyErr
		}
		_, auditErr := service.Audit.Append(txContext, platformaudit.Event{IdempotencyKey: key, Action: "wecom.staff_directory_refreshed", ActorType: "system", ResourceType: "wecom_staff_directory", Payload: payload, OccurredAt: now})
		return auditErr
	})
}

func (service StaffDirectoryRefreshService) recordStaffRefreshFailure(ctx context.Context, runID int64, code string, terminal bool) error {
	return service.UOW.Within(ctx, func(txContext context.Context) error {
		return service.Store.Fail(txContext, runID, code, terminal, service.now())
	})
}

func (service StaffDirectoryRefreshService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func validStaffRefreshKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 120 && !strings.ContainsAny(value, "\x00\r\n")
}

func canonicalStaffDirectory(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 10000 {
		return nil, ErrStaffDirectoryRefreshNotReady
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, ErrStaffDirectoryRefreshNotReady
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func staffDirectoryDigest(values []string) [sha256.Size]byte {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
