package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type fakeCatalog struct {
	group       *segmentdomain.Group
	packages    map[string]segmentdomain.Package
	configs     map[int64]segmentdomain.ConfigurationVersion
	putCalls    int
	nextPackage int64
	createKeys  []string
}

var fakePackageCreatedAt = time.Date(2026, 9, 4, 0, 30, 0, 0, time.UTC)

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{packages: map[string]segmentdomain.Package{}, configs: map[int64]segmentdomain.ConfigurationVersion{}, nextPackage: 10}
}

func (f *fakeCatalog) ListGroups(context.Context) ([]segmentdomain.Group, error) {
	if f.group == nil {
		return []segmentdomain.Group{}, nil
	}
	return []segmentdomain.Group{*f.group}, nil
}

func (f *fakeCatalog) CreateGroup(_ context.Context, command segmentapp.GroupCommand) (segmentdomain.Group, error) {
	group := segmentdomain.Group{ID: 3, Name: command.Name, SortOrder: command.SortOrder, Version: 1}
	f.group = &group
	return group, nil
}

func (f *fakeCatalog) CreatePackage(_ context.Context, command segmentapp.PackageCreateCommand) (segmentdomain.Package, error) {
	f.createKeys = append(f.createKeys, command.IdempotencyKey)
	code, _ := segmentapp.PackageCodeForIdempotencyKey(command.IdempotencyKey)
	if item, ok := f.packages[code]; ok {
		return item, nil
	}
	f.nextPackage++
	item := segmentdomain.Package{ID: f.nextPackage, Code: code, Name: command.Name, GroupID: command.GroupID, Lifecycle: segmentdomain.Paused, Version: 2, CreatedAt: fakePackageCreatedAt}
	f.packages[code] = item
	definition, _ := segmentapp.DefaultDefinition(command.TemplateKey)
	f.configs[item.ID] = segmentdomain.ConfigurationVersion{ID: item.ID * 10, PackageID: item.ID, Version: 1, Definition: definition}
	return item, nil
}

func (f *fakeCatalog) GetPackageByCode(_ context.Context, code string) (segmentdomain.Package, error) {
	item, ok := f.packages[code]
	if !ok {
		return segmentdomain.Package{}, segmentapp.ErrNotFound
	}
	return item, nil
}

func (f *fakeCatalog) GetPackage(_ context.Context, id int64) (segmentdomain.Package, error) {
	for _, item := range f.packages {
		if item.ID == id {
			return item, nil
		}
	}
	return segmentdomain.Package{}, errors.New("not found")
}

func (f *fakeCatalog) CurrentConfiguration(_ context.Context, id int64) (segmentdomain.ConfigurationVersion, error) {
	configuration, ok := f.configs[id]
	if !ok {
		return segmentdomain.ConfigurationVersion{}, errors.New("not found")
	}
	return configuration, nil
}

func (f *fakeCatalog) PutConfiguration(_ context.Context, command segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	f.putCalls++
	configuration := segmentdomain.ConfigurationVersion{ID: command.PackageID*10 + 1, PackageID: command.PackageID, Version: 2, Definition: append(json.RawMessage(nil), command.Definition...), RefreshCronUTC: command.RefreshCronUTC}
	f.configs[command.PackageID] = configuration
	return configuration, nil
}

type fakeSnapshots struct {
	previewed  []int64
	refreshed  []int64
	references []time.Time
	keys       []string
}

func (f *fakeSnapshots) Preview(_ context.Context, id int64, reference time.Time) (segmentapp.Preview, error) {
	f.previewed = append(f.previewed, id)
	f.references = append(f.references, reference)
	return segmentapp.Preview{PackageID: id, ReferenceTime: reference, MemberCount: int(id), MemberDigest: "members", WatermarkDigest: "watermark"}, nil
}

func (f *fakeSnapshots) AcceptRefresh(_ context.Context, command segmentapp.RefreshCommand) (segmentdomain.RefreshRun, error) {
	f.refreshed = append(f.refreshed, command.PackageID)
	f.references = append(f.references, command.ReferenceTime)
	f.keys = append(f.keys, command.IdempotencyKey)
	return segmentdomain.RefreshRun{ID: command.PackageID * 100, PackageID: command.PackageID, State: segmentdomain.RefreshQueued}, nil
}

func TestApplyCreatesSafePausedDefaultsAndIsIdempotent(t *testing.T) {
	catalog, snapshots := newFakeCatalog(), &fakeSnapshots{}
	reference := time.Date(2026, 9, 4, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60))
	report, err := Apply(context.Background(), catalog, snapshots, 7, reference)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "semantic_bootstrap_no_legacy_data" || report.GroupName != DefaultGroupName || len(report.Packages) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if catalog.putCalls != 3 || len(snapshots.previewed) != 3 || len(snapshots.refreshed) != 3 {
		t.Fatalf("put=%d preview=%v refresh=%v", catalog.putCalls, snapshots.previewed, snapshots.refreshed)
	}
	if len(catalog.createKeys) != len(managedDefaults) {
		t.Fatalf("create keys=%v", catalog.createKeys)
	}
	for index, definition := range managedDefaults {
		want := "automation-operations-bootstrap-package-v2:" + definition.Code
		if catalog.createKeys[index] != want {
			t.Fatalf("create key[%d]=%q want %q", index, catalog.createKeys[index], want)
		}
	}
	if !report.ReferenceTime.Equal(fakePackageCreatedAt) {
		t.Fatalf("reference=%s", report.ReferenceTime)
	}
	for index, got := range snapshots.references {
		if !got.Equal(fakePackageCreatedAt) {
			t.Fatalf("reference[%d]=%s", index, got)
		}
	}
	for index, key := range snapshots.keys {
		if key != "automation-operations-bootstrap-refresh-v4:"+managedDefaults[index].Code {
			t.Fatalf("refresh key[%d]=%q", index, key)
		}
	}
	for _, item := range report.Packages {
		if item.Lifecycle != segmentdomain.Paused || item.ConfigurationVersion != 2 || item.Preview == nil || item.Refresh == nil {
			t.Fatalf("unsafe or incomplete package report: %+v", item)
		}
	}

	secondSnapshots := &fakeSnapshots{}
	second, err := Apply(context.Background(), catalog, secondSnapshots, 7, reference.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.putCalls != 3 || len(second.Packages) != 3 {
		t.Fatalf("replay overwrote configuration: calls=%d report=%+v", catalog.putCalls, second)
	}
	if !second.ReferenceTime.Equal(report.ReferenceTime) {
		t.Fatalf("replay reference=%s first=%s", second.ReferenceTime, report.ReferenceTime)
	}
}

func TestApplyPreservesOperatorChangesAndArchivedPackage(t *testing.T) {
	catalog, snapshots := newFakeCatalog(), &fakeSnapshots{}
	reference := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	if _, err := Apply(context.Background(), catalog, snapshots, 9, reference); err != nil {
		t.Fatal(err)
	}
	var changedID, archivedID int64
	for key, item := range catalog.packages {
		if changedID == 0 {
			changedID = item.ID
			configuration := catalog.configs[item.ID]
			configuration.Version = 3
			configuration.Definition = json.RawMessage(`{"schema_version":1,"template_key":"core_ai_product","parameters":{"core_product_id":1}}`)
			configuration.RefreshCronUTC = "15 2 * * *"
			catalog.configs[item.ID] = configuration
			continue
		}
		if archivedID == 0 {
			archivedID = item.ID
			item.Lifecycle = segmentdomain.Archived
			catalog.packages[key] = item
		}
	}
	beforePuts := catalog.putCalls
	snapshots = &fakeSnapshots{}
	report, err := Apply(context.Background(), catalog, snapshots, 9, reference.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.putCalls != beforePuts {
		t.Fatalf("operator configuration was overwritten: before=%d after=%d", beforePuts, catalog.putCalls)
	}
	for _, item := range report.Packages {
		if item.ID == changedID && (item.SkippedReason != "operator_configured" || item.Preview != nil || item.Refresh != nil) {
			t.Fatalf("operator configured package was evaluated: %+v", item)
		}
		if item.ID == changedID && item.ConfigurationVersion != 3 {
			t.Fatalf("operator version not preserved: %+v", item)
		}
		if item.ID == archivedID && (item.SkippedReason != "operator_archived" || item.Preview != nil || item.Refresh != nil) {
			t.Fatalf("archived package was evaluated: %+v", item)
		}
	}
}

func TestApplyRejectsInvalidDependencies(t *testing.T) {
	if _, err := Apply(context.Background(), nil, nil, 0, time.Time{}); !errors.Is(err, ErrInvalidDependencies) {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyReusesLegacyArchivedPackageWithoutRecreatingIt(t *testing.T) {
	catalog, snapshots := newFakeCatalog(), &fakeSnapshots{}
	group := segmentdomain.Group{ID: 3, Name: DefaultGroupName, SortOrder: 100, Version: 1}
	catalog.group = &group
	legacyCode, err := segmentapp.PackageCodeForIdempotencyKey("automation-operations-bootstrap-package-v1:active-7d")
	if err != nil {
		t.Fatal(err)
	}
	catalog.packages[legacyCode] = segmentdomain.Package{ID: 7, Code: legacyCode, Name: "近7天活跃客户", GroupID: &group.ID, Lifecycle: segmentdomain.Archived, Version: 3, CreatedAt: fakePackageCreatedAt}

	report, err := Apply(context.Background(), catalog, snapshots, 9, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range report.Packages {
		if item.ID == 7 {
			found = true
			if item.SkippedReason != "operator_archived" {
				t.Fatalf("legacy archived package was not preserved: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("legacy package missing from report: %+v", report.Packages)
	}
	for _, key := range catalog.createKeys {
		if key == "automation-operations-bootstrap-package-v2:active-7d" {
			t.Fatalf("archived legacy package was recreated with %q", key)
		}
	}
}
