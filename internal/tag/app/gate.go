package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

var (
	ErrInvalidExecutionStatus = errors.New("invalid tag execution status")
	ErrExecutionUnavailable   = errors.New("tag execution status unavailable")
)

// ExecutionStatusService reads a local, fail-closed gate projection.  It
// deliberately has no WeCom client, credential, CorpID, or Provider write
// path.  Provider execution and reconciliation belong to a later outbound
// adapter and are not implied by this read.
type ExecutionStatusService struct {
	uow    platformport.UnitOfWork
	reader tagport.ExecutionStatusReader
}

func NewExecutionStatusService(uow platformport.UnitOfWork, reader tagport.ExecutionStatusReader) *ExecutionStatusService {
	return &ExecutionStatusService{uow: uow, reader: reader}
}

func (service *ExecutionStatusService) Get(ctx context.Context) (domain.ExecutionGate, error) {
	if ctx == nil || service == nil || nilGateDependency(service.uow) || nilGateDependency(service.reader) {
		return domain.ExecutionGate{}, ErrExecutionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return domain.ExecutionGate{}, errors.Join(ErrExecutionUnavailable, err)
	}
	var gate domain.ExecutionGate
	err := service.uow.Within(ctx, func(tx context.Context) error {
		status, err := service.reader.ReadExecutionStatus(tx)
		if err != nil {
			return err
		}
		projected, ok := projectExecutionGate(status)
		if !ok {
			return ErrInvalidExecutionStatus
		}
		gate = projected
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrInvalidExecutionStatus) {
			return domain.ExecutionGate{}, err
		}
		return domain.ExecutionGate{}, errors.Join(ErrExecutionUnavailable, err)
	}
	return gate, nil
}

func projectExecutionGate(status tagport.ExecutionStatus) (domain.ExecutionGate, bool) {
	if status.ObservedAt.IsZero() {
		return domain.ExecutionGate{}, false
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(status.Payload, &source) != nil || source == nil {
		return domain.ExecutionGate{}, false
	}
	var mode string
	var accepted, queued, attempted, executed, unknown, reconciled, external, synced bool
	if json.Unmarshal(source["mode"], &mode) != nil || mode != "provider_execution_unavailable" ||
		json.Unmarshal(source["accepted"], &accepted) != nil || !accepted ||
		json.Unmarshal(source["queued"], &queued) != nil || !queued ||
		json.Unmarshal(source["attempted"], &attempted) != nil || attempted ||
		json.Unmarshal(source["executed"], &executed) != nil || executed ||
		json.Unmarshal(source["outcome_unknown"], &unknown) != nil || unknown ||
		json.Unmarshal(source["reconciled"], &reconciled) != nil || reconciled ||
		json.Unmarshal(source["real_external_call_executed"], &external) != nil || external ||
		json.Unmarshal(source["sync_executed"], &synced) != nil || synced {
		return domain.ExecutionGate{}, false
	}
	return domain.ExecutionGate{
		ProviderExecutionEligible:       false,
		LocalCommandAcceptanceAvailable: true,
		LocalQueueAvailable:             true,
		SyncExecuted:                    false,
		ObservedAt:                      status.ObservedAt.UTC(),
		RealExternalCallExecuted:        false,
	}, true
}

func nilGateDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ tagport.ExecutionGateReader = (*ExecutionStatusService)(nil)
