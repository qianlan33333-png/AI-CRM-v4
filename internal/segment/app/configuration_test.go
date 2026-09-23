package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type unusedStore struct{ Store }
type unusedUOW struct{}

func (unusedUOW) Within(context.Context, func(context.Context) error) error {
	panic("unexpected transaction")
}

type executingUOW struct{}

func (executingUOW) Within(ctx context.Context, apply func(context.Context) error) error {
	return apply(ctx)
}

type diagnosticStore struct {
	Store
	createPackageErr error
}

func (store diagnosticStore) Reserve(context.Context, segmentstore.Reservation) (segmentstore.Receipt, bool, error) {
	return segmentstore.Receipt{ID: 1, State: "reserved"}, true, nil
}

func (store diagnosticStore) CreatePackage(context.Context, segmentdomain.Package) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, store.createPackageErr
}

var _ platformport.UnitOfWork = unusedUOW{}

func TestActivationFailsClosedBeforePersistenceOrProviderWork(t *testing.T) {
	service := NewService(unusedUOW{}, unusedStore{})
	_, err := service.TransitionPackage(context.Background(), VersionCommand{ID: 1, ExpectedVersion: 1, Actor: 1, IdempotencyKey: "1234567890abcdef"}, segmentdomain.Active)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("activation=%v", err)
	}
}

func TestMutationRequiresStrongIdempotencyKey(t *testing.T) {
	service := NewService(unusedUOW{}, unusedStore{})
	_, err := service.CreateGroup(context.Background(), GroupCommand{Name: "新客", Actor: 1, IdempotencyKey: "short"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("short idempotency key=%v", err)
	}
}

func TestCreatePackageClassifiesSafePersistenceDiagnostic(t *testing.T) {
	databaseError := &pgconn.PgError{Code: "23503", ConstraintName: "segment_configuration_fk", Message: "sensitive row value"}
	service := NewService(executingUOW{}, diagnosticStore{createPackageErr: databaseError})
	_, err := service.CreatePackage(context.Background(), PackageCreateCommand{
		Name: "近7天活跃客户", TemplateKey: "active_contacts", Actor: 7,
		IdempotencyKey: "automation-operations-bootstrap-test-key",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	diagnostic, ok := PersistenceFailure(err)
	if !ok || diagnostic.Stage != "create_package_record" || diagnostic.SQLState != "23503" || diagnostic.Constraint != "segment_configuration_fk" {
		t.Fatalf("diagnostic=%+v ok=%t", diagnostic, ok)
	}
	if diagnostic.Error() != ErrUnavailable.Error() || errors.Is(err, databaseError) {
		t.Fatalf("diagnostic leaked persistence error: %v", err)
	}
}
