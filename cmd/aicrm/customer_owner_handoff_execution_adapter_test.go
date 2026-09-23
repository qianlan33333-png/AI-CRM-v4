package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

type ownerHandoffExecutionUOW struct{ calls int }

func (uow *ownerHandoffExecutionUOW) Within(ctx context.Context, run func(context.Context) error) error {
	uow.calls++
	return run(ctx)
}

type ownerHandoffExecutionStub struct {
	value customerport.OwnerHandoffExecution
	err   error
}

func (stub ownerHandoffExecutionStub) ReadOwnerHandoffExecution(context.Context, string) (customerport.OwnerHandoffExecution, error) {
	return stub.value, stub.err
}

type ownerHandoffExecutionStaff map[int64]accessdomain.User

func (staff ownerHandoffExecutionStaff) UserByID(_ context.Context, id int64, _ bool) (accessdomain.User, error) {
	user, found := staff[id]
	if !found {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return user, nil
}

func TestCustomerOwnerHandoffExecutionAdapterUsesUOWAndCurrentFrozenStaffGate(t *testing.T) {
	uow := &ownerHandoffExecutionUOW{}
	execution := customerport.OwnerHandoffExecution{EffectID: "effect", SourceStaffID: 10, TargetStaffID: 20, SourceUserID: "former", TargetUserID: "next"}
	adapter := customerOwnerHandoffExecutionAdapter{
		uow: uow, executions: ownerHandoffExecutionStub{value: execution},
		staff: ownerHandoffExecutionStaff{
			10: {ID: 10, WeComUserID: "former", Active: false}, // donor permits a retired source.
			20: {ID: 20, WeComUserID: "next", Active: true},
		},
	}
	actual, err := adapter.ReadOwnerHandoffExecution(context.Background(), "effect")
	if err != nil || !reflect.DeepEqual(actual, execution) || uow.calls != 1 {
		t.Fatalf("execution=%+v err=%v uow=%d", actual, err, uow.calls)
	}

	adapter.staff = ownerHandoffExecutionStaff{10: {ID: 10, WeComUserID: "former"}, 20: {ID: 20, WeComUserID: "changed", Active: true}}
	if _, err = adapter.ReadOwnerHandoffExecution(context.Background(), "effect"); err == nil || errors.Is(err, accessdomain.ErrNotFound) {
		t.Fatalf("changed frozen target must be rejected, err=%v", err)
	}
}
