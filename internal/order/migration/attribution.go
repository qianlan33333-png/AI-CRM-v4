package migration

import (
	"context"
	"errors"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type AttributionRunStore interface {
	BeginAttribution(context.Context, AttributionManifest, string) (int64, error)
	CompleteAttribution(context.Context, int64, int64) (AttributionRunResult, error)
	FailAttribution(context.Context, int64) error
	ReconcileAttribution(context.Context, AttributionManifest, string) (AttributionRunResult, error)
}

type AttributionRunResult struct {
	RunID          int64 `json:"run_id"`
	Input          int64 `json:"input"`
	Linked         int64 `json:"linked"`
	AlreadyLinked  int64 `json:"already_linked"`
	Quarantined    int64 `json:"quarantined"`
	Replayed       int64 `json:"replayed"`
	WrongBindings  int64 `json:"wrong_bindings"`
	EffectEligible int64 `json:"effect_eligible"`
	Matched        bool  `json:"matched"`
}

type AttributionRunner struct {
	UOW      platformport.UnitOfWork
	Resolver identityport.CommerceResolver
	Orders   orderport.HistoricalAttributionWriter
	Query    orderport.Query
	Runs     AttributionRunStore
	Scope    string
	Now      func() time.Time
}

func (runner AttributionRunner) DryRun(ctx context.Context, manifest AttributionManifest) (AttributionRunResult, error) {
	if err := runner.ready(manifest); err != nil {
		return AttributionRunResult{}, err
	}
	result := AttributionRunResult{Input: int64(len(manifest.Rows))}
	for _, row := range manifest.Rows {
		if row.EvidenceState != AttributionCandidate {
			result.Quarantined++
			continue
		}
		order, err := runner.Query.GetByReference(ctx, row.MerchantOrderNo)
		if err != nil {
			if errors.Is(err, orderport.ErrNotFound) || errors.Is(err, orderport.ErrConflict) {
				result.Quarantined++
				continue
			}
			return result, err
		}
		resolution, err := runner.resolve(ctx, row)
		if err != nil {
			return result, err
		}
		if resolution.Status != identityport.CommerceResolved || resolution.CustomerID < 1 || len(resolution.Matches) != 1 {
			result.Quarantined++
			continue
		}
		if order.PayerCustomerID == nil {
			result.Linked++
		} else if *order.PayerCustomerID == int64(resolution.CustomerID) {
			result.AlreadyLinked++
		} else {
			result.Quarantined++
		}
	}
	result.Matched = result.Input == result.Linked+result.AlreadyLinked+result.Quarantined
	return result, nil
}

func (runner AttributionRunner) Apply(ctx context.Context, manifest AttributionManifest) (result AttributionRunResult, err error) {
	if err = runner.ready(manifest); err != nil {
		return result, err
	}
	runID, err := runner.Runs.BeginAttribution(ctx, manifest, runner.Scope)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = runner.Runs.FailAttribution(context.Background(), runID)
		}
	}()
	replayed := int64(0)
	for _, row := range manifest.Rows {
		var receipt orderport.HistoricalAttributionResult
		err = runner.UOW.Within(ctx, func(tx context.Context) error {
			command, commandErr := runner.command(tx, runID, row)
			if commandErr != nil {
				return commandErr
			}
			receipt, commandErr = runner.Orders.RecordHistoricalAttributionWithin(tx, command)
			return commandErr
		})
		if err != nil {
			return result, err
		}
		if receipt.Replayed {
			replayed++
		}
	}
	result, err = runner.Runs.CompleteAttribution(ctx, runID, replayed)
	return result, err
}

func (runner AttributionRunner) Reconcile(ctx context.Context, manifest AttributionManifest) (AttributionRunResult, error) {
	if err := runner.ready(manifest); err != nil {
		return AttributionRunResult{}, err
	}
	return runner.Runs.ReconcileAttribution(ctx, manifest, runner.Scope)
}

func (runner AttributionRunner) command(ctx context.Context, runID int64, row AttributionRow) (orderport.HistoricalAttributionCommand, error) {
	digest, err := row.Digest()
	if err != nil {
		return orderport.HistoricalAttributionCommand{}, err
	}
	command := orderport.HistoricalAttributionCommand{RunID: runID, SourceKey: row.SourceKey, OrderReference: row.MerchantOrderNo, EvidenceDigest: digest, OccurredAt: runner.now()}
	switch row.EvidenceState {
	case AttributionSourceIdentityMissing:
		command.Outcome = orderport.AttributionSourceIdentityMissing
	case AttributionSourceIdentityNotFound:
		command.Outcome = orderport.AttributionSourceIdentityNotFound
	case AttributionSourceExternalAmbiguous:
		command.Outcome = orderport.AttributionSourceIdentityAmbiguous
	case AttributionCandidate:
		resolution, resolveErr := runner.resolveWithin(ctx, row)
		if resolveErr != nil {
			return command, resolveErr
		}
		switch resolution.Status {
		case identityport.CommerceResolved:
			if resolution.CustomerID < 1 || len(resolution.Matches) != 1 || resolution.Matches[0].IdentityID < 1 || resolution.Matches[0].CustomerID != resolution.CustomerID {
				return command, errors.New("invalid OneID commerce resolution")
			}
			command.Outcome = orderport.AttributionLinked
			command.PayerCustomerID = int64(resolution.CustomerID)
			command.PayerIdentityID = resolution.Matches[0].IdentityID
		case identityport.CommerceConflict, identityport.CommercePartial, identityport.CommerceInvalid:
			command.Outcome = orderport.AttributionTargetIdentityConflict
		case identityport.CommerceNotFound:
			command.Outcome = orderport.AttributionTargetIdentityNotFound
		default:
			return command, errors.New("unknown OneID commerce resolution")
		}
	default:
		return command, ErrInvalidAttributionManifest
	}
	return command, nil
}

func (runner AttributionRunner) resolve(ctx context.Context, row AttributionRow) (identityport.CommerceResolution, error) {
	var result identityport.CommerceResolution
	err := runner.UOW.Within(ctx, func(tx context.Context) error {
		var resolveErr error
		result, resolveErr = runner.resolveWithin(tx, row)
		return resolveErr
	})
	return result, err
}

func (runner AttributionRunner) resolveWithin(ctx context.Context, row AttributionRow) (identityport.CommerceResolution, error) {
	return runner.Resolver.ResolveCommerce(ctx, identityport.CommerceReferenceSet{References: []identitydomain.Reference{{Kind: identitydomain.KindWeComExternalUserID, Scope: runner.Scope, Value: row.ExternalUserID, Assurance: identitydomain.AssuranceVerified, Source: "aicrm_production.wecom_directory"}}})
}

func (runner AttributionRunner) ready(manifest AttributionManifest) error {
	if runner.UOW == nil || runner.Resolver == nil || runner.Orders == nil || runner.Query == nil || runner.Runs == nil || identitydomain.ValidateNamespace(identitydomain.KindWeComExternalUserID, runner.Scope) != nil {
		return errors.New("order history attribution runner is not configured")
	}
	return manifest.Validate()
}

func (runner AttributionRunner) now() time.Time {
	if runner.Now != nil {
		return runner.Now().UTC()
	}
	return time.Now().UTC()
}
