// Command bootstrap-automation-operations installs safe, paused audience
// defaults in v3. It copies product semantics, never legacy rows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/riverqueue/river"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segment "github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentbootstrap "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/bootstrap"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

func main() {
	if err := execute(context.Background(), os.Stdout); err != nil {
		attributes := []any{"error", err}
		if diagnostic, ok := segmentapp.PersistenceFailure(err); ok {
			attributes = append(attributes,
				"failure_stage", diagnostic.Stage,
				"sqlstate", diagnostic.SQLState,
				"constraint", diagnostic.Constraint,
			)
		}
		slog.Error("automation operations semantic bootstrap failed", attributes...)
		os.Exit(1)
	}
}

func execute(parent context.Context, output *os.File) error {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	actor, err := bootstrapActor(ctx, uow, accessstore.NewPostgreSQL())
	if err != nil {
		return err
	}
	repository, err := segmentstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	catalog := segmentapp.NewService(uow, repository)
	queries := identityquery.NewPostgreSQL()
	evaluator, err := segmentapp.NewEvaluator(
		segmentcompiler.Compiler{},
		segmentadapter.CustomerSource{UoW: uow, Customers: customerstore.NewPostgreSQL()},
		segmentadapter.CanonicalCustomers{UoW: uow, Resolver: canonicalRoot{reader: queries}},
	)
	if err != nil {
		return err
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](workers, segment.NewAudienceRefreshWorker()); err != nil {
		return err
	}
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](workers, segment.NewAudienceMemberEventDispatchWorker()); err != nil {
		return err
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool.Native(), workers)
	if err != nil {
		return err
	}
	refreshes, err := segment.NewRiverRefreshEnqueuer(insertClient)
	if err != nil {
		return err
	}
	events, err := segment.NewRiverMemberEventEnqueuer(insertClient)
	if err != nil {
		return err
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, repository, evaluator, refreshes, events)
	if err != nil {
		return err
	}
	report, err := segmentbootstrap.Apply(ctx, catalog, snapshots, actor, time.Now().UTC())
	if err != nil {
		return err
	}
	if err = publishAndWaitForRefreshes(ctx, &report, snapshots, 500*time.Millisecond); err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}

type refreshReader interface {
	GetRefresh(context.Context, int64) (segmentdomain.RefreshRun, error)
}

type refreshPublisher interface {
	refreshReader
	ProcessRefresh(context.Context, int64) error
}

// publishAndWaitForRefreshes preserves the accepted River job but also lets the
// release bootstrap perform the same idempotent work inline. This prevents a
// temporarily unhealthy worker runtime from turning a safe semantic bootstrap
// into an opaque ten-minute deployment hang. A queued River replay observes the
// published run and becomes a no-op.
func publishAndWaitForRefreshes(ctx context.Context, report *segmentbootstrap.Report, publisher refreshPublisher, pollEvery time.Duration) error {
	if report == nil || publisher == nil || pollEvery <= 0 {
		return errors.New("refresh publication verifier unavailable")
	}
	for index := range report.Packages {
		refresh := report.Packages[index].Refresh
		if refresh == nil || refresh.State == segmentdomain.RefreshPublished {
			continue
		}
		if err := publisher.ProcessRefresh(ctx, refresh.RunID); err != nil {
			// Another worker may have won the publication race. Accept that only
			// after reading the durable terminal state; otherwise surface the
			// original stage error immediately.
			run, readErr := publisher.GetRefresh(ctx, refresh.RunID)
			if readErr == nil && run.State == segmentdomain.RefreshPublished {
				refresh.State = run.State
				continue
			}
			if readErr != nil {
				return fmt.Errorf("process audience refresh run %d: %w", refresh.RunID, errors.Join(err, readErr))
			}
			return fmt.Errorf("process audience refresh run %d in state %s: %w", refresh.RunID, run.State, err)
		}
	}
	return waitForPublishedRefreshes(ctx, report, publisher, pollEvery)
}

func waitForPublishedRefreshes(ctx context.Context, report *segmentbootstrap.Report, reader refreshReader, pollEvery time.Duration) error {
	if report == nil || reader == nil || pollEvery <= 0 {
		return errors.New("refresh publication verifier unavailable")
	}
	for {
		pending := 0
		for index := range report.Packages {
			refresh := report.Packages[index].Refresh
			if refresh == nil || refresh.State == segmentdomain.RefreshPublished {
				continue
			}
			run, err := reader.GetRefresh(ctx, refresh.RunID)
			if err != nil {
				return fmt.Errorf("verify audience refresh run %d: %w", refresh.RunID, err)
			}
			refresh.State = run.State
			if run.State == segmentdomain.RefreshFailed {
				return fmt.Errorf("audience refresh run %d failed with code %s", run.ID, run.ErrorCode)
			}
			if run.State != segmentdomain.RefreshPublished {
				pending++
			}
		}
		if pending == 0 {
			return nil
		}
		timer := time.NewTimer(pollEvery)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("wait for audience refresh publication: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

type userLister interface {
	ListUsers(context.Context) ([]accessdomain.User, error)
}

func bootstrapActor(ctx context.Context, uow platformport.UnitOfWork, users userLister) (int64, error) {
	if uow == nil || users == nil {
		return 0, errors.New("access reader unavailable")
	}
	var candidates []accessdomain.User
	if err := uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		candidates, listErr = users.ListUsers(tx)
		return listErr
	}); err != nil {
		return 0, err
	}
	for _, role := range []accessdomain.Role{accessdomain.RoleSuperAdmin, accessdomain.RoleAdmin} {
		for _, user := range candidates {
			if user.Active && user.HasRole(role) {
				return user.ID, nil
			}
		}
	}
	return 0, errors.New("no active administrator is available for bootstrap audit attribution")
}

type canonicalRoot struct{ reader identityquery.Reader }

func (adapter canonicalRoot) ResolveCanonicalCustomer(ctx context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	detail, err := adapter.reader.Customer(ctx, id)
	if err != nil {
		if errors.Is(err, identityquery.ErrNotFound) {
			return customerport.CanonicalCustomer{}, customerapp.ErrNotFound
		}
		return customerport.CanonicalCustomer{}, fmt.Errorf("resolve canonical customer: %w", err)
	}
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: detail.CanonicalCustomerID, Merged: detail.CanonicalCustomerID != id}, nil
}

func (adapter canonicalRoot) ResolveCanonicalCustomers(ctx context.Context, ids []customerdomain.CustomerID) ([]customerport.CanonicalCustomer, error) {
	reader, ok := adapter.reader.(identityquery.CanonicalCustomerReader)
	if !ok {
		result := make([]customerport.CanonicalCustomer, 0, len(ids))
		for _, id := range ids {
			item, err := adapter.ResolveCanonicalCustomer(ctx, id)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	}
	resolved, err := reader.CanonicalCustomers(ctx, ids)
	if err != nil {
		if errors.Is(err, identityquery.ErrNotFound) {
			return nil, customerapp.ErrNotFound
		}
		return nil, fmt.Errorf("resolve canonical customers: %w", err)
	}
	result := make([]customerport.CanonicalCustomer, len(resolved))
	for index, item := range resolved {
		result[index] = customerport.CanonicalCustomer{RequestedCustomerID: item.RequestedCustomerID, CustomerID: item.CustomerID, Merged: item.RequestedCustomerID != item.CustomerID}
	}
	return result, nil
}
