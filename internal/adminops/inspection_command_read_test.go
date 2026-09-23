package adminops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"github.com/riverqueue/river"
)

type inspectionCommandReadSecurity struct {
	principal accessdomain.Principal
}

func (s inspectionCommandReadSecurity) ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (inspectionCommandReadSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	panic("command receipt GET must not call a mutation authorizer")
}

func TestPostgreSQLInspectionCommandReadRequiresActorAndOwnCompletion(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	service, err := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "command-read-test"})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := inspectionCommandQueue(t, pool, uow, nil).EnqueueManualInspection(ctx, 7, "command-read-owned-key")
	if err != nil {
		t.Fatal(err)
	}
	owner := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	read := func(principal accessdomain.Principal, id string) *httptest.ResponseRecorder {
		t.Helper()
		handler, err := NewInspectionHandler(service, inspectionCommandReadSecurity{principal: principal})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/ops-inspections/commands/"+id, nil))
		return response
	}
	jobID := fmt.Sprint(accepted.JobID)
	for _, principal := range []accessdomain.Principal{{}, {InternalID: 7, Kind: accessdomain.KindAdmin}} {
		if response := read(principal, jobID); response.Code != http.StatusForbidden {
			t.Fatalf("non-superadmin=%d", response.Code)
		}
	}
	other := owner
	other.InternalID = 8
	for _, test := range []struct {
		principal accessdomain.Principal
		id        string
		status    int
	}{{other, jobID, 404}, {owner, fmt.Sprint(accepted.JobID + 1000), 404}, {owner, "0", 400}, {owner, "invalid", 400}} {
		if response := read(test.principal, test.id); response.Code != test.status {
			t.Fatalf("read %s=%d want=%d", test.id, response.Code, test.status)
		}
	}
	readOwn := func() opsport.ManualInspectionCommand {
		t.Helper()
		response := read(owner, jobID)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("own command=%d %s", response.Code, response.Body.String())
		}
		for _, forbidden := range []string{"command-read-owned-key", "request_digest", "actor_id", "sha256:"} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("receipt exposed %q", forbidden)
			}
		}
		var out opsport.ManualInspectionCommand
		if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := readOwn(); out.State != "accepted" || out.JobID != accepted.JobID || out.RunID != nil || out.CompletedAt != nil {
		t.Fatalf("accepted=%+v", out)
	}
	unrelated, err := service.Scan(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out := readOwn(); out.State != "accepted" || out.RunID != nil || out.CompletedAt != nil {
		t.Fatalf("scheduled run %d completed another command: %+v", unrelated.ID, out)
	}
	worker := NewInspectionWorker()
	if err = worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(ctx, &river.Job[InspectionJobArgs]{Args: acceptedInspectionArgs(t, pool, accepted.JobID)}); err != nil {
		t.Fatal(err)
	}
	out := readOwn()
	if out.State != "completed" || out.RunID == nil || *out.RunID == unrelated.ID || out.CompletedAt == nil {
		t.Fatalf("own completion=%+v", out)
	}
	// Disposable run details are not required to retain the permanent receipt.
	if _, err = pool.Exec(ctx, `DELETE FROM adminops_inspection_results WHERE run_id=$1`, *out.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM adminops_inspection_runs WHERE id=$1`, *out.RunID); err != nil {
		t.Fatal(err)
	}
	if after := readOwn(); after.State != "completed" || after.RunID == nil || *after.RunID != *out.RunID || after.CompletedAt == nil || !after.CompletedAt.Equal(*out.CompletedAt) {
		t.Fatalf("pruned details lost permanent command completion: %+v", after)
	}
}

func TestPostgreSQLInspectionCommandReadLockTimeoutDoesNotClaimCompletion(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	service, err := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "command-read-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE adminops_inspection_commands IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	handler, err := NewInspectionHandler(service, inspectionTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/ops-inspections/commands/1", nil))
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > 3*time.Second || strings.Contains(response.Body.String(), "completed") {
		t.Fatalf("unavailable receipt status=%d duration=%s body=%s", response.Code, time.Since(started), response.Body.String())
	}
}
