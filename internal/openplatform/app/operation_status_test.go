package app

import (
	"context"
	"encoding/json"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	"strings"
	"testing"
	"time"
)

type snapshotKey struct{}
type statusUOW struct {
	calls int
	fail  error
}

func (u *statusUOW) Within(ctx context.Context, work func(context.Context) error) error {
	u.calls++
	err := work(context.WithValue(ctx, snapshotKey{}, "one snapshot"))
	if err != nil {
		return err
	}
	return u.fail
}

type statusFixture struct {
	t             *testing.T
	reads, checks int
	reject        customer.CustomerID
	err           error
}

func (f *statusFixture) GetMachineOperationWithin(ctx context.Context, actor ai.MachineActor, id ai.PlanID) (ai.MachineOperationSnapshot, error) {
	f.t.Helper()
	f.reads++
	if ctx.Value(snapshotKey{}) != "one snapshot" || actor.Reference != "machine:client-a" || id != 12 {
		f.t.Fatal("reader escaped snapshot or changed machine identity")
	}
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.FixedZone("fixture", 8*3600))
	return ai.MachineOperationSnapshot{Status: ai.MachineOperationStatus{PlanID: 12, ReviewState: ai.ReviewApproved, OperationState: ai.MachineOperationOutcomeUnknown, OutcomeUnknownCount: 1, CreatedAt: now, UpdatedAt: now}, Recipients: []ai.MachineRecipientResult{{RecipientID: 31, CustomerID: 42, ReviewState: "approved", ExecutionState: "provider_accepted", ProviderAccepted: true, DeliveryProven: false, EffectID: "eer_fixture", UpdatedAt: now}, {RecipientID: 32, CustomerID: 43, ExecutionState: "outcome_unknown", UpdatedAt: now}}}, f.err
}
func (f *statusFixture) AuthorizeOperationCustomer(ctx context.Context, p accessdomain.MachinePrincipal, id customer.CustomerID) error {
	f.t.Helper()
	f.checks++
	if ctx.Value(snapshotKey{}) != "one snapshot" || p.ClientID != "client-a" {
		f.t.Fatal("scope check escaped snapshot")
	}
	if id == f.reject {
		return errors.New("private scope detail")
	}
	return nil
}
func statusService(t *testing.T) (*OperationStatus, *statusFixture, *statusUOW) {
	t.Helper()
	f := &statusFixture{t: t}
	u := &statusUOW{}
	s, e := NewOperationStatus(f, u, f)
	if e != nil {
		t.Fatal(e)
	}
	return s, f, u
}

func TestOperationStatusRetainsOneSnapshotAndSafeWireProjection(t *testing.T) {
	s, f, u := statusService(t)
	result, e := s.Read(context.Background(), accessdomain.MachinePrincipal{ClientID: "client-a"}, "ai_review_plan:12")
	if e != nil {
		t.Fatal(e)
	}
	if f.reads != 1 || f.checks != 2 || u.calls != 1 {
		t.Fatal("aggregate/scope must share one transaction")
	}
	d := result.Data.(map[string]any)
	items := d["recipients"].([]map[string]any)
	if d["operation_state"] != ai.MachineOperationOutcomeUnknown || d["outcome_unknown_count"] != 1 || items[0]["provider_accepted"] != true || items[0]["delivery_proven"] != false {
		t.Fatal("Provider acceptance/unknown/delivery states collapsed")
	}
	if items[0]["safe_user_ref"] != customer.CanonicalOneIDLabel(42) || items[0]["updated_at"].(time.Time).Location() != time.UTC {
		t.Fatal("safe reference/UTC projection changed")
	}
	raw, _ := json.Marshal(d)
	for _, field := range []string{"customer_id", "staff_id", "private scope detail"} {
		if strings.Contains(string(raw), field) {
			t.Fatal("unsafe projection")
		}
	}
}
func TestOperationStatusRejectsNonCanonicalIdentifierAndMachineBeforeReading(t *testing.T) {
	for _, id := range []string{"", "ai_review_plan:0", "ai_review_plan:-1", "ai_review_plan:+12", "ai_review_plan:012", "ai_review_plan:12 ", "ai_review_plan:9223372036854775808", "other:12"} {
		s, f, u := statusService(t)
		r, e := s.Read(context.Background(), accessdomain.MachinePrincipal{ClientID: "client-a"}, id)
		var failure *port.OperationError
		if !errors.As(e, &failure) || failure.Code != port.ErrorNotFound || r.Data != nil || f.reads != 0 || u.calls != 0 {
			t.Fatalf("invalid ID was observed: %q", id)
		}
	}
	s, f, u := statusService(t)
	r, e := s.Read(context.Background(), accessdomain.MachinePrincipal{}, "ai_review_plan:12")
	var failure *port.OperationError
	if !errors.As(e, &failure) || failure.Code != port.ErrorAuthentication || r.Data != nil || f.reads != 0 || u.calls != 0 {
		t.Fatal("invalid actor reached reader")
	}
}
func TestOperationStatusScopeFailureAndTransactionFailureDiscardPartialResult(t *testing.T) {
	s, f, u := statusService(t)
	f.reject = 43
	r, e := s.Read(context.Background(), accessdomain.MachinePrincipal{ClientID: "client-a"}, "ai_review_plan:12")
	var failure *port.OperationError
	if !errors.As(e, &failure) || failure.Code != port.ErrorPermission || r.Data != nil || f.checks != 2 || u.calls != 1 || strings.Contains(e.Error(), "private") {
		t.Fatal("scope failure exposed partial recipients")
	}
	s, _, u = statusService(t)
	u.fail = errors.New("fixture commit failure")
	r, e = s.Read(context.Background(), accessdomain.MachinePrincipal{ClientID: "client-a"}, "ai_review_plan:12")
	if !errors.Is(e, u.fail) || r.Data != nil {
		t.Fatal("failed transaction exposed result")
	}
}
func TestMachineAIErrorPreservesProtocolCodesWithoutConcreteOwnerDependency(t *testing.T) {
	for _, c := range []struct {
		err  error
		code port.ErrorCode
	}{{ai.ErrInvalidMachineActor, port.ErrorAuthentication}, {ai.ErrInvalid, port.ErrorValidation}, {ai.ErrConflict, port.ErrorConflict}, {ai.ErrIdempotencyConflict, port.ErrorConflict}, {ai.ErrNotFound, port.ErrorNotFound}, {errors.New("private identifier"), port.ErrorDependencyUnavailable}} {
		var failure *port.OperationError
		e := MachineAIError(c.err)
		if !errors.As(e, &failure) || failure.Code != c.code || strings.Contains(e.Error(), "private identifier") {
			t.Fatal("wire error contract changed")
		}
	}
	e := port.NewDetailedError(port.ErrorConflict, "existing safe failure", map[string]any{"created": false})
	if MachineAIError(e) != e {
		t.Fatal("existing protocol error changed")
	}
}
func TestOperationStatusMissingDependenciesFailClosed(t *testing.T) {
	f := &statusFixture{t: t}
	for _, c := range []struct {
		reader *statusFixture
		uow    *statusUOW
		scope  *statusFixture
	}{{nil, &statusUOW{}, f}, {f, nil, f}, {f, &statusUOW{}, nil}} {
		// Convert only nonnil pointers to interfaces to preserve a real nil dependency.
		var reader ai.MachineTransactionalReader
		var scope port.OperationCustomerScopeAuthorizer
		if c.reader != nil {
			reader = c.reader
		}
		if c.scope != nil {
			scope = c.scope
		}
		if c.uow == nil {
			if _, e := NewOperationStatus(reader, nil, scope); e == nil {
				t.Fatal("nil UoW accepted")
			}
			continue
		}
		if _, e := NewOperationStatus(reader, c.uow, scope); e == nil {
			t.Fatal("nil Owner dependency accepted")
		}
	}
}
