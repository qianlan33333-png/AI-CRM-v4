package channel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestAcquisitionLinkHTTPFrozenContractAndRoles(t *testing.T) {
	app := &acquisitionLinkHTTPApplication{link: wecomport.CustomerAcquisitionLink{LinkID: "link-1", LinkName: "Campaign", URL: "https://work.weixin.qq.com/link", UserIDs: []string{"staff-1"}, DepartmentIDs: []int64{}, SkipVerify: true}}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewAcquisitionLinkHTTPHandler(app, security)
	if err != nil {
		t.Fatal(err)
	}
	response := catalogHTTPRequest(handler, http.MethodGet, acquisitionLinksPath+"?limit=50", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Body.String() != "{\"items\":[{\"link_id\":\"link-1\"}],\"next_cursor\":\"\"}\n" {
		t.Fatalf("list status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodGet, acquisitionLinksPath+"/link-1", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"url":"https://work.weixin.qq.com/link"`) {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	body := `{"link_name":"Campaign","user_ids":["staff-1"],"department_ids":[],"skip_verify":true}`
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath, body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-create-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation status=%d body=%s", response.Code, response.Body.String())
	}
	security.principal.Roles = []accessdomain.Role{accessdomain.RoleAdmin}
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath, body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-create-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusAccepted || app.command.Operation != "create" || app.command.ActorID != 7 || !strings.Contains(response.Body.String(), `"state":"accepted"`) {
		t.Fatalf("create status=%d command=%+v body=%s", response.Code, app.command, response.Body.String())
	}
	mutationsBeforeUnknown := app.mutationCount
	response = catalogHTTPRequest(handler, http.MethodPatch, acquisitionLinksPath+"/link-1", strings.TrimSuffix(body, "}")+`,"unknown":true}`, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-update-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusBadRequest || app.mutationCount != mutationsBeforeUnknown {
		t.Fatalf("unknown JSON status=%d mutations=%d body=%s", response.Code, app.mutationCount, response.Body.String())
	}
	evidence := strings.Repeat("a", 64)
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath+"/link-1/reconcile", `{"receipt_id":9,"resolution":"provider_applied","evidence_digest":"`+evidence+`"}`, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-reconcile-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusOK || app.reconcile.EvidenceDigest != "sha256:"+evidence {
		t.Fatalf("reconcile status=%d command=%+v body=%s", response.Code, app.reconcile, response.Body.String())
	}
}

func TestAcquisitionLinkPatchAndDeleteForwardAuthenticatedCommand(t *testing.T) {
	app := &acquisitionLinkHTTPApplication{link: wecomport.CustomerAcquisitionLink{LinkID: "link-1"}}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 8, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAcquisitionLinkHTTPHandler(app, security)
	if err != nil {
		t.Fatal(err)
	}

	updated := catalogHTTPRequest(
		handler,
		http.MethodPatch,
		acquisitionLinksPath+"/link-1",
		`{"link_name":"Updated Campaign","user_ids":["staff-2"],"department_ids":[9],"skip_verify":false}`,
		map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-update-route-0001"}, "X-CSRF-Token": {"valid"}},
	)
	if updated.Code != http.StatusAccepted || app.command.Operation != "update" || app.command.LinkID != "link-1" || app.command.ActorID != 8 || app.command.IdempotencyKey != "channel-link-update-route-0001" || app.command.Input.LinkName != "Updated Campaign" || len(app.command.Input.UserIDs) != 1 || app.command.Input.UserIDs[0] != "staff-2" || len(app.command.Input.DepartmentIDs) != 1 || app.command.Input.DepartmentIDs[0] != 9 || app.command.Input.SkipVerify {
		t.Fatalf("patch status=%d command=%+v body=%s", updated.Code, app.command, updated.Body.String())
	}

	deleted := catalogHTTPRequest(
		handler,
		http.MethodDelete,
		acquisitionLinksPath+"/link-1",
		"",
		map[string][]string{"Idempotency-Key": {"channel-link-delete-route-0001"}, "X-CSRF-Token": {"valid"}},
	)
	if deleted.Code != http.StatusAccepted || app.command.Operation != "delete" || app.command.LinkID != "link-1" || app.command.ActorID != 8 || app.command.IdempotencyKey != "channel-link-delete-route-0001" || app.command.Input.LinkName != "" {
		t.Fatalf("delete status=%d command=%+v body=%s", deleted.Code, app.command, deleted.Body.String())
	}

	mutationsBeforeInvalid := app.mutationCount
	duplicateKey := catalogHTTPRequest(
		handler,
		http.MethodDelete,
		acquisitionLinksPath+"/link-1",
		"",
		map[string][]string{"Idempotency-Key": {"channel-link-duplicate-route-0001", "channel-link-duplicate-route-0002"}, "X-CSRF-Token": {"valid"}},
	)
	if duplicateKey.Code != http.StatusBadRequest || !strings.Contains(duplicateKey.Body.String(), `"code":"MALFORMED_REQUEST"`) || app.mutationCount != mutationsBeforeInvalid {
		t.Fatalf("duplicate idempotency key status=%d mutations=%d body=%s", duplicateKey.Code, app.mutationCount, duplicateKey.Body.String())
	}

	missingCSRF := catalogHTTPRequest(
		handler,
		http.MethodDelete,
		acquisitionLinksPath+"/link-1",
		"",
		map[string][]string{"Idempotency-Key": {"channel-link-delete-route-0002"}},
	)
	if missingCSRF.Code != http.StatusUnauthorized || app.mutationCount != mutationsBeforeInvalid {
		t.Fatalf("missing csrf status=%d mutations=%d body=%s", missingCSRF.Code, app.mutationCount, missingCSRF.Body.String())
	}
}

type acquisitionLinkHTTPApplication struct {
	link          wecomport.CustomerAcquisitionLink
	command       AcquisitionLinkCommand
	reconcile     AcquisitionLinkReconcileCommand
	mutationCount int
}

func (app *acquisitionLinkHTTPApplication) List(context.Context, string, int) ([]wecomport.CustomerAcquisitionLink, string, error) {
	return []wecomport.CustomerAcquisitionLink{app.link}, "", nil
}
func (app *acquisitionLinkHTTPApplication) Get(context.Context, string) (wecomport.CustomerAcquisitionLink, error) {
	return app.link, nil
}
func (app *acquisitionLinkHTTPApplication) Mutate(_ context.Context, command AcquisitionLinkCommand) (AcquisitionLinkReceipt, error) {
	app.mutationCount++
	app.command = command
	return AcquisitionLinkReceipt{ID: 8, State: "accepted"}, nil
}
func (app *acquisitionLinkHTTPApplication) Reconcile(_ context.Context, command AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, error) {
	app.reconcile = command
	link := app.link
	return AcquisitionLinkReceipt{ID: command.ReceiptID, State: "reconciled", OutcomeDigest: command.EvidenceDigest, Resolution: command.Resolution, BusinessEndpointDispatched: true, RealExternalCallExecuted: true, Link: &link}, nil
}

var _ AcquisitionLinkApplication = (*acquisitionLinkHTTPApplication)(nil)
