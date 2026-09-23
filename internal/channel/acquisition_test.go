package channel

import (
	"context"
	"errors"
	"testing"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

func TestAcquisitionCandidatesAreProviderAndActiveLocalIntersection(t *testing.T) {
	provider := acquisitionProvider{ids: []string{"u-2", "u-1", "provider-only"}}
	directory := acquisitionDirectory{items: []AcquisitionStaff{
		{ID: 1, WeComUserID: "u-1", DisplayName: "Beta", Active: true},
		{ID: 2, WeComUserID: "u-2", DisplayName: "Alpha", Active: true},
		{ID: 3, WeComUserID: "u-3", DisplayName: "Disabled", Active: false},
	}}
	service := NewAcquisitionService(catalogDirectUOW{}, nil, directory, provider)
	items, err := service.Candidates(context.Background())
	if err != nil || len(items) != 2 || items[0].ID != 2 || items[1].ID != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestAcquisitionCandidatesFailClosedOnProviderRead(t *testing.T) {
	service := NewAcquisitionService(catalogDirectUOW{}, nil, acquisitionDirectory{}, acquisitionProvider{err: errors.New("disabled")})
	if _, err := service.Candidates(context.Background()); !errors.Is(err, ErrAcquisitionUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestAcquisitionLocalCandidatesRemainVisibleWhenProviderReadFails(t *testing.T) {
	service := NewAcquisitionService(catalogDirectUOW{}, nil, acquisitionDirectory{items: []AcquisitionStaff{{ID: 9, WeComUserID: "saved-user", DisplayName: "Saved Owner", Active: true}}}, acquisitionProvider{err: errors.New("disabled")})
	items, err := service.LocalCandidates(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != 9 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if _, err = service.Candidates(context.Background()); !errors.Is(err, ErrAcquisitionUnavailable) {
		t.Fatalf("publish validation should remain closed: %v", err)
	}
}

func TestAcquisitionReplaceMapsProviderStaffToLocalCatalogCAS(t *testing.T) {
	store := &catalogMemoryStore{channels: map[int64]channeldomain.Channel{}, receipts: map[[32]byte]channelport.OperationReceipt{}}
	catalog := NewCatalogService(catalogDirectUOW{}, store, store, &catalogMemoryEvents{}, catalogMaterialRefs{}, catalogTagRefs{}, catalogStaffRefs{})
	created, err := catalog.Create(context.Background(), CatalogMutation{ActorID: 7, IdempotencyKey: "create-acquisition", Create: validCatalogCreate()})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAcquisitionService(catalogDirectUOW{}, catalog, acquisitionDirectory{items: []AcquisitionStaff{
		{ID: 9, WeComUserID: "owner", DisplayName: "Owner", Active: true},
		{ID: 10, WeComUserID: "backup", DisplayName: "Backup", Active: true},
	}}, acquisitionProvider{ids: []string{"owner", "backup"}})
	updated, selected, err := service.Replace(context.Background(), created.ID, AssignmentMutation{ActorID: 7, IdempotencyKey: "replace-acquisition", ExpectedVersion: created.Version,
		Mode: channeldomain.AssignmentMulti, Strategy: channeldomain.StrategyRatio, Assignees: []AssignmentMember{{WeComUserID: "owner", Ratio: 70}, {WeComUserID: "backup", Ratio: 30}}})
	if err != nil || updated.Version != 2 || len(selected) != 2 || updated.Config.Assignment.Assignees[1].StaffID != 10 {
		t.Fatalf("updated=%#v selected=%#v err=%v", updated, selected, err)
	}
}

type acquisitionProvider struct {
	ids []string
	err error
}

func (provider acquisitionProvider) ListContactStaff(context.Context) ([]string, error) {
	return provider.ids, provider.err
}

type acquisitionDirectory struct {
	items []AcquisitionStaff
	err   error
}

func (directory acquisitionDirectory) ListAcquisitionStaff(context.Context) ([]AcquisitionStaff, error) {
	return directory.items, directory.err
}
