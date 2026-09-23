package app

import (
	"context"
	"errors"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

// ActivationPrecheck permits rule computation independently of message
// execution. Packages carrying either execution pointer retain the original
// full execution gate. Transition's expected package version prevents a
// concurrent configuration/binding/sender change from bypassing this check.
func (f *RuntimeFacade) ActivationPrecheck(ctx context.Context, id int64) (Precheck, error) {
	if f == nil || f.Service == nil || !f.Service.ready() || id < 1 {
		return Precheck{}, ErrNotReady
	}
	var pkg segmentdomain.Package
	var config segmentdomain.ConfigurationVersion
	found := false
	err := f.Service.uow.Within(ctx, func(tx context.Context) error {
		var e error
		pkg, e = f.Service.store.LockPackage(tx, id)
		if e != nil {
			return e
		}
		config, e = f.Service.store.CurrentConfiguration(tx, id)
		if errors.Is(e, segmentstore.ErrNotFound) {
			return nil
		}
		if e == nil {
			found = true
		}
		return e
	})
	if err != nil {
		return Precheck{}, classify(err)
	}
	if pkg.CurrentAutomationBindingID != nil || pkg.CurrentSenderSetID != nil {
		if f.Execution == nil {
			return Precheck{}, ErrNotReady
		}
		return f.Execution.Precheck(ctx, id)
	}
	check := Precheck{Reasons: []string{}}
	if !found || pkg.CurrentConfigurationVersionID == nil || *pkg.CurrentConfigurationVersionID != config.ID || config.PackageID != pkg.ID {
		check.Reasons = append(check.Reasons, "configuration_missing")
	} else {
		check.ConfigurationVersionID = config.ID
		if _, e := CanonicalDefinition(config.Definition); e != nil {
			check.Reasons = append(check.Reasons, "definition_unsupported")
		}
		if e := ValidateRefresh(config.RefreshMode, config.RefreshCronUTC); e != nil {
			check.Reasons = append(check.Reasons, "schedule_invalid")
		}
	}
	check.Ready = len(check.Reasons) == 0
	return check, nil
}
