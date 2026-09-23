package channel

import (
	"context"
	"errors"
	"sort"
	"strings"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrAcquisitionUnavailable = errors.New("channel acquisition staff unavailable")

// FollowUserReader is a provider-read-only boundary. Implementations must not
// perform any provider mutation and must fail closed when WeCom is disabled.
type FollowUserReader interface {
	ListContactStaff(context.Context) ([]string, error)
}

type AcquisitionDirectory interface {
	ListAcquisitionStaff(context.Context) ([]AcquisitionStaff, error)
}

type AcquisitionStaff struct {
	ID          int64
	WeComUserID string
	DisplayName string
	Active      bool
}

type AcquisitionCandidate struct {
	ID          int64
	WeComUserID string
	DisplayName string
}

type AcquisitionService struct {
	uow       platformport.UnitOfWork
	catalog   *CatalogService
	directory AcquisitionDirectory
	provider  FollowUserReader
}

func NewAcquisitionService(uow platformport.UnitOfWork, catalog *CatalogService, directory AcquisitionDirectory, provider FollowUserReader) *AcquisitionService {
	return &AcquisitionService{uow: uow, catalog: catalog, directory: directory, provider: provider}
}

// LocalCandidates is the durable projection used for rendering saved channel
// configuration. It intentionally performs no provider read.
func (service *AcquisitionService) LocalCandidates(ctx context.Context) ([]AcquisitionCandidate, error) {
	if service == nil || service.uow == nil || service.directory == nil || ctx == nil {
		return nil, ErrAcquisitionUnavailable
	}
	var staff []AcquisitionStaff
	if err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		staff, readErr = service.directory.ListAcquisitionStaff(tx)
		return readErr
	}); err != nil {
		return nil, errors.Join(ErrAcquisitionUnavailable, err)
	}
	result := make([]AcquisitionCandidate, 0, len(staff))
	for _, item := range staff {
		if item.Active && item.ID > 0 && strings.TrimSpace(item.WeComUserID) != "" && strings.TrimSpace(item.DisplayName) != "" {
			result = append(result, AcquisitionCandidate{ID: item.ID, WeComUserID: item.WeComUserID, DisplayName: item.DisplayName})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DisplayName == result[j].DisplayName {
			return result[i].ID < result[j].ID
		}
		return result[i].DisplayName < result[j].DisplayName
	})
	return result, nil
}

func (service *AcquisitionService) Candidates(ctx context.Context) ([]AcquisitionCandidate, error) {
	if service == nil || service.uow == nil || service.directory == nil || service.provider == nil || ctx == nil {
		return nil, ErrAcquisitionUnavailable
	}
	providerIDs, err := service.provider.ListContactStaff(ctx)
	if err != nil {
		return nil, errors.Join(ErrAcquisitionUnavailable, err)
	}
	providerSet := make(map[string]struct{}, len(providerIDs))
	for _, id := range providerIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			providerSet[id] = struct{}{}
		}
	}
	local, err := service.LocalCandidates(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AcquisitionCandidate, 0, len(local))
	for _, item := range local {
		if _, ok := providerSet[item.WeComUserID]; !ok {
			continue
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DisplayName == result[j].DisplayName {
			return result[i].ID < result[j].ID
		}
		return result[i].DisplayName < result[j].DisplayName
	})
	return result, nil
}

// ValidatePublish proves that every assignee in the immutable configuration is
// still both an active local staff member and a current WeCom follow user. The
// provider read happens before AssetService opens its write transaction.
func (service *AcquisitionService) ValidatePublish(ctx context.Context, channelID int64) (int64, error) {
	if service == nil || service.catalog == nil || channelID < 1 {
		return 0, ErrAcquisitionUnavailable
	}
	channel, err := service.catalog.Get(ctx, channelID)
	if err != nil {
		return 0, err
	}
	eligible, err := service.Candidates(ctx)
	if err != nil {
		return 0, err
	}
	ids := make(map[int64]struct{}, len(eligible))
	for _, candidate := range eligible {
		ids[candidate.ID] = struct{}{}
	}
	for _, assignee := range channel.Config.Assignment.Assignees {
		if _, ok := ids[assignee.StaffID]; !ok {
			return 0, ErrAcquisitionUnavailable
		}
	}
	return channel.ConfigVersion, nil
}

func (service *AcquisitionService) Preview(ctx context.Context, channelID int64) (channeldomain.Channel, []AcquisitionCandidate, error) {
	if service == nil || service.catalog == nil {
		return channeldomain.Channel{}, nil, ErrAcquisitionUnavailable
	}
	channel, err := service.catalog.Get(ctx, channelID)
	if err != nil {
		return channeldomain.Channel{}, nil, err
	}
	candidates, err := service.LocalCandidates(ctx)
	if err != nil {
		return channeldomain.Channel{}, nil, err
	}
	return channel, candidates, nil
}

type AssignmentMutation struct {
	ActorID         int64
	IdempotencyKey  string
	ExpectedVersion int64
	Mode            channeldomain.AssignmentMode
	Strategy        channeldomain.AssignmentStrategy
	OverflowPolicy  string
	Assignees       []AssignmentMember
}

type AssignmentMember struct {
	WeComUserID string
	Priority    int
	Ratio       int
	MaxScans24h int
}

func (service *AcquisitionService) Replace(ctx context.Context, channelID int64, command AssignmentMutation) (channeldomain.Channel, []AcquisitionCandidate, error) {
	if service == nil || service.catalog == nil || channelID < 1 {
		return channeldomain.Channel{}, nil, ErrCatalogNotFound
	}
	candidates, err := service.Candidates(ctx)
	if err != nil {
		return channeldomain.Channel{}, nil, err
	}
	byProviderID := make(map[string]AcquisitionCandidate, len(candidates))
	for _, candidate := range candidates {
		byProviderID[candidate.WeComUserID] = candidate
	}
	assignment := channeldomain.Assignment{Mode: command.Mode, Strategy: command.Strategy, OverflowPolicy: command.OverflowPolicy}
	selected := make([]AcquisitionCandidate, 0, len(command.Assignees))
	for index, item := range command.Assignees {
		candidate, ok := byProviderID[item.WeComUserID]
		if !ok {
			return channeldomain.Channel{}, nil, ErrInvalidCatalogCommand
		}
		priority := item.Priority
		if priority == 0 {
			priority = index + 1
		}
		assignment.Assignees = append(assignment.Assignees, channeldomain.Assignee{StaffID: candidate.ID, Priority: priority, Ratio: item.Ratio, MaxScans24h: item.MaxScans24h})
		selected = append(selected, candidate)
	}
	if command.Mode == "" {
		assignment.Mode = channeldomain.AssignmentSingle
	}
	if command.Strategy == "" {
		assignment.Strategy = channeldomain.StrategyRatio
	}
	if err = channeldomain.ValidateAssignment(assignment); err != nil {
		return channeldomain.Channel{}, nil, errors.Join(ErrInvalidCatalogCommand, err)
	}
	current, err := service.catalog.Get(ctx, channelID)
	if err != nil {
		return channeldomain.Channel{}, nil, err
	}
	updated := current.Config
	updated.Assignment = assignment
	result, err := service.catalog.Update(ctx, channelID, CatalogMutation{ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, Update: channeldomain.UpdateChannel{ExpectedVersion: command.ExpectedVersion, Code: current.Code, Status: current.Status, Config: updated}})
	if err != nil {
		return channeldomain.Channel{}, nil, err
	}
	return result, selected, nil
}
