// Package bootstrap installs the smallest useful Automation Operations
// catalogue without importing legacy customer or execution data.
package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

const (
	DefaultGroupName = "默认运营人群"
	defaultCronUTC   = "0 1 * * *"
)

var ErrInvalidDependencies = errors.New("automation operations bootstrap dependencies are invalid")

type Catalog interface {
	ListGroups(context.Context) ([]segmentdomain.Group, error)
	CreateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error)
	CreatePackage(context.Context, segmentapp.PackageCreateCommand) (segmentdomain.Package, error)
	GetPackage(context.Context, int64) (segmentdomain.Package, error)
	GetPackageByCode(context.Context, string) (segmentdomain.Package, error)
	CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
	PutConfiguration(context.Context, segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error)
}

type Snapshots interface {
	Preview(context.Context, int64, time.Time) (segmentapp.Preview, error)
	AcceptRefresh(context.Context, segmentapp.RefreshCommand) (segmentdomain.RefreshRun, error)
}

type Default struct {
	Code string
	Name string
	Days int
}

var managedDefaults = []Default{
	{Code: "active-7d", Name: "近7天活跃客户", Days: 7},
	{Code: "active-30d", Name: "近30天活跃客户", Days: 30},
	{Code: "active-90d", Name: "近90天活跃客户", Days: 90},
}

type Report struct {
	Mode          string          `json:"mode"`
	ReferenceTime time.Time       `json:"reference_time"`
	GroupID       int64           `json:"group_id"`
	GroupName     string          `json:"group_name"`
	Packages      []PackageReport `json:"packages"`
}

type PackageReport struct {
	ID                     int64                   `json:"id"`
	Name                   string                  `json:"name"`
	Lifecycle              segmentdomain.Lifecycle `json:"lifecycle"`
	ConfigurationVersion   int64                   `json:"configuration_version"`
	ConfigurationInstalled bool                    `json:"configuration_installed"`
	Preview                *PreviewReport          `json:"preview,omitempty"`
	Refresh                *RefreshReport          `json:"refresh,omitempty"`
	SkippedReason          string                  `json:"skipped_reason,omitempty"`
}

type PreviewReport struct {
	MemberCount     int    `json:"member_count"`
	MemberDigest    string `json:"member_digest"`
	WatermarkDigest string `json:"watermark_digest"`
}

type RefreshReport struct {
	RunID int64                      `json:"run_id"`
	State segmentdomain.RefreshState `json:"state"`
}

// Apply creates three canonical-customer audiences and queues their first
// durable snapshot refresh. It never activates packages and never creates an
// outbound policy, sender, message intent or Provider effect.
func Apply(ctx context.Context, catalog Catalog, snapshots Snapshots, actor int64, reference time.Time) (Report, error) {
	if catalog == nil || snapshots == nil || actor < 1 || reference.IsZero() {
		return Report{}, ErrInvalidDependencies
	}
	reference = reference.UTC()
	group, err := ensureGroup(ctx, catalog, actor)
	if err != nil {
		return Report{}, fmt.Errorf("ensure default audience group: %w", err)
	}
	report := Report{Mode: "semantic_bootstrap_no_legacy_data", ReferenceTime: reference, GroupID: group.ID, GroupName: group.Name, Packages: make([]PackageReport, 0, len(managedDefaults))}
	var bootstrapReference time.Time
	for _, definition := range managedDefaults {
		item, err := ensurePackage(ctx, catalog, definition, group.ID, actor)
		if err != nil {
			return Report{}, fmt.Errorf("ensure package %s: %w", definition.Code, err)
		}
		item, err = catalog.GetPackage(ctx, item.ID)
		if err != nil {
			return Report{}, fmt.Errorf("read package %s: %w", definition.Code, err)
		}
		if bootstrapReference.IsZero() {
			if item.CreatedAt.IsZero() {
				return Report{}, fmt.Errorf("read package %s: %w", definition.Code, ErrInvalidDependencies)
			}
			bootstrapReference = item.CreatedAt.UTC()
			report.ReferenceTime = bootstrapReference
		}
		packageReport := PackageReport{ID: item.ID, Name: item.Name, Lifecycle: item.Lifecycle}
		if item.Lifecycle == segmentdomain.Archived {
			packageReport.SkippedReason = "operator_archived"
			report.Packages = append(report.Packages, packageReport)
			continue
		}
		configuration, err := catalog.CurrentConfiguration(ctx, item.ID)
		if err != nil {
			return Report{}, fmt.Errorf("read package %s configuration: %w", definition.Code, err)
		}
		desired, err := activeContactsDefinition(definition.Days)
		if err != nil {
			return Report{}, err
		}
		if configuration.Version == 1 && (!bytes.Equal(configuration.Definition, desired) || configuration.RefreshCronUTC != defaultCronUTC) {
			configuration, err = catalog.PutConfiguration(ctx, segmentapp.ConfigurationCommand{
				PackageID: item.ID, ExpectedPackageVersion: item.Version, Definition: desired,
				RefreshCronUTC: defaultCronUTC, Actor: actor,
				IdempotencyKey: "automation-operations-bootstrap-config-v2:" + definition.Code,
			})
			if err != nil {
				return Report{}, fmt.Errorf("configure package %s: %w", definition.Code, err)
			}
			packageReport.ConfigurationInstalled = true
		}
		packageReport.ConfigurationVersion = configuration.Version
		// Versions after the bootstrap-owned v2 belong to operators. Their
		// definitions may require sources this defaults-only command does not own.
		if configuration.Version > 2 {
			packageReport.SkippedReason = "operator_configured"
			report.Packages = append(report.Packages, packageReport)
			continue
		}
		preview, err := snapshots.Preview(ctx, item.ID, bootstrapReference)
		if err != nil {
			return Report{}, fmt.Errorf("preview package %s: %w", definition.Code, err)
		}
		packageReport.Preview = &PreviewReport{MemberCount: preview.MemberCount, MemberDigest: preview.MemberDigest, WatermarkDigest: preview.WatermarkDigest}
		run, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{
			PackageID: item.ID, Actor: actor, ReferenceTime: bootstrapReference,
			// v4 supersedes any failed v3 run left by a worker-only release while
			// retaining a stable reference time across partial retries. The first
			// managed package creation time is durable and common to the bootstrap.
			IdempotencyKey: "automation-operations-bootstrap-refresh-v4:" + definition.Code,
		})
		if err != nil {
			return Report{}, fmt.Errorf("queue package %s refresh: %w", definition.Code, err)
		}
		packageReport.Refresh = &RefreshReport{RunID: run.ID, State: run.State}
		report.Packages = append(report.Packages, packageReport)
	}
	return report, nil
}

func ensurePackage(ctx context.Context, catalog Catalog, definition Default, groupID, actor int64) (segmentdomain.Package, error) {
	keys := []string{
		"automation-operations-bootstrap-package-v2:" + definition.Code,
		"automation-operations-bootstrap-package-v1:" + definition.Code,
	}
	for _, key := range keys {
		code, err := segmentapp.PackageCodeForIdempotencyKey(key)
		if err != nil {
			return segmentdomain.Package{}, err
		}
		item, err := catalog.GetPackageByCode(ctx, code)
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, segmentapp.ErrNotFound) {
			return segmentdomain.Package{}, err
		}
	}
	return catalog.CreatePackage(ctx, segmentapp.PackageCreateCommand{
		Name: definition.Name, TemplateKey: "active_contacts", GroupID: &groupID, Actor: actor,
		IdempotencyKey: keys[0],
	})
}

func ensureGroup(ctx context.Context, catalog Catalog, actor int64) (segmentdomain.Group, error) {
	groups, err := catalog.ListGroups(ctx)
	if err != nil {
		return segmentdomain.Group{}, err
	}
	for _, group := range groups {
		if group.Name == DefaultGroupName {
			return group, nil
		}
	}
	return catalog.CreateGroup(ctx, segmentapp.GroupCommand{Name: DefaultGroupName, SortOrder: 100, Actor: actor, IdempotencyKey: "automation-operations-bootstrap-group-v1"})
}

func activeContactsDefinition(days int) (json.RawMessage, error) {
	parameter, err := json.Marshal(fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(segmentapp.DefinitionInput{SchemaVersion: 1, TemplateKey: "active_contacts", Parameters: map[string]json.RawMessage{"within_days": parameter}})
	if err != nil {
		return nil, err
	}
	return segmentapp.CanonicalDefinition(raw)
}
