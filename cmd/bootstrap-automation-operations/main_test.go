package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	segmentbootstrap "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/bootstrap"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type testUoW struct{ err error }

func (u testUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	if u.err != nil {
		return u.err
	}
	return fn(ctx)
}

type refreshReaderStub struct {
	runs       map[int64]segmentdomain.RefreshRun
	err        error
	processErr error
	processed  []int64
}

func (s refreshReaderStub) GetRefresh(_ context.Context, id int64) (segmentdomain.RefreshRun, error) {
	if s.err != nil {
		return segmentdomain.RefreshRun{}, s.err
	}
	return s.runs[id], nil
}

func (s *refreshReaderStub) ProcessRefresh(_ context.Context, id int64) error {
	s.processed = append(s.processed, id)
	if s.processErr != nil {
		return s.processErr
	}
	run := s.runs[id]
	run.State = segmentdomain.RefreshPublished
	s.runs[id] = run
	return nil
}

func TestWaitForPublishedRefreshesUpdatesDeploymentReport(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{
		{Refresh: &segmentbootstrap.RefreshReport{RunID: 10, State: segmentdomain.RefreshQueued}},
		{SkippedReason: "operator_archived"},
	}}
	err := waitForPublishedRefreshes(context.Background(), &report, refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{
		10: {ID: 10, State: segmentdomain.RefreshPublished},
	}}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Packages[0].Refresh.State; got != segmentdomain.RefreshPublished {
		t.Fatalf("state=%s", got)
	}
}

func TestWaitForPublishedRefreshesFailsClosed(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{{Refresh: &segmentbootstrap.RefreshReport{RunID: 11, State: segmentdomain.RefreshQueued}}}}
	err := waitForPublishedRefreshes(context.Background(), &report, refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{
		11: {ID: 11, State: segmentdomain.RefreshFailed, ErrorCode: "persistence_conflict"},
	}}, time.Millisecond)
	if err == nil {
		t.Fatal("expected failed refresh to fail deployment bootstrap")
	}
}

func TestPublishAndWaitProcessesDurableRefreshInline(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{{Refresh: &segmentbootstrap.RefreshReport{RunID: 12, State: segmentdomain.RefreshQueued}}}}
	publisher := &refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{12: {ID: 12, State: segmentdomain.RefreshQueued}}}
	if err := publishAndWaitForRefreshes(context.Background(), &report, publisher, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(publisher.processed) != 1 || publisher.processed[0] != 12 || report.Packages[0].Refresh.State != segmentdomain.RefreshPublished {
		t.Fatalf("processed=%v report=%+v", publisher.processed, report)
	}
}

func TestPublishAndWaitSurfacesInlineStageFailure(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{{Refresh: &segmentbootstrap.RefreshReport{RunID: 13, State: segmentdomain.RefreshQueued}}}}
	want := errors.New("evaluate failed")
	publisher := &refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{13: {ID: 13, State: segmentdomain.RefreshEvaluating}}, processErr: want}
	err := publishAndWaitForRefreshes(context.Background(), &report, publisher, time.Millisecond)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

type usersStub struct {
	users []accessdomain.User
	err   error
}

func (s usersStub) ListUsers(context.Context) ([]accessdomain.User, error) { return s.users, s.err }

func TestBootstrapActorPrefersActiveSuperAdmin(t *testing.T) {
	id, err := bootstrapActor(context.Background(), testUoW{}, usersStub{users: []accessdomain.User{
		{ID: 1, Active: true, Roles: []accessdomain.Role{accessdomain.RoleAdmin}},
		{ID: 2, Active: false, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
		{ID: 3, Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
	}})
	if err != nil || id != 3 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}

func TestBootstrapActorFailsClosed(t *testing.T) {
	if _, err := bootstrapActor(context.Background(), testUoW{}, usersStub{users: []accessdomain.User{{ID: 1, Active: true, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}}); err == nil {
		t.Fatal("expected missing administrator error")
	}
	want := errors.New("read failed")
	if _, err := bootstrapActor(context.Background(), testUoW{err: want}, usersStub{}); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
