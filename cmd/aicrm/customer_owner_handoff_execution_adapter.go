package main

import (
	"context"
	"errors"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// customerOwnerHandoffExecutionAdapter is the Composition bridge between an
// External Effects worker, which runs outside a Customer transaction, and the
// Customer-owned immutable transfer intent. It opens a short Customer UoW,
// returns only the authenticated frozen execution, and rechecks that the
// frozen staff mapping is still eligible before the Provider boundary.
type customerOwnerHandoffExecutionAdapter struct {
	uow        platformport.UnitOfWork
	executions customerport.OwnerHandoffExecutionReader
	staff      interface {
		UserByID(context.Context, int64, bool) (accessdomain.User, error)
	}
}

func (adapter customerOwnerHandoffExecutionAdapter) ReadOwnerHandoffExecution(ctx context.Context, effectID string) (customerport.OwnerHandoffExecution, error) {
	if adapter.uow == nil || adapter.executions == nil || adapter.staff == nil || effectID == "" {
		return customerport.OwnerHandoffExecution{}, errors.New("owner handoff execution adapter unavailable")
	}
	var execution customerport.OwnerHandoffExecution
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		value, readErr := adapter.executions.ReadOwnerHandoffExecution(tx, effectID)
		if readErr != nil {
			return readErr
		}
		source, sourceErr := adapter.staff.UserByID(tx, value.SourceStaffID, false)
		if sourceErr != nil || source.ID != value.SourceStaffID || source.WeComUserID == "" || source.WeComUserID != value.SourceUserID {
			return errors.New("owner handoff source authorization changed")
		}
		target, targetErr := adapter.staff.UserByID(tx, value.TargetStaffID, false)
		if targetErr != nil || target.ID != value.TargetStaffID || !target.Active || target.WeComUserID == "" || target.WeComUserID != value.TargetUserID {
			return errors.New("owner handoff target authorization changed")
		}
		execution = value
		return nil
	})
	return execution, err
}

var _ customerport.OwnerHandoffExecutionReader = customerOwnerHandoffExecutionAdapter{}
