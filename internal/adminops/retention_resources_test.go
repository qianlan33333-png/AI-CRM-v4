package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type coverageReaderFixture struct{}

func (coverageReaderFixture) ReadLatest(context.Context) (platformport.HostMaintenanceReport, error) {
	panic("coverage inventory must not read host state")
}
func (coverageReaderFixture) ReadReleaseLatest(context.Context) (platformport.ReleaseMaintenanceReport, error) {
	panic("coverage inventory must not read release state")
}

func bindCoverageOwners(t *testing.T, service *RetentionService) {
	t.Helper()
	for _, id := range []string{"config_usage", "archive_sync_runs"} {
		if err := service.BindProcessPolicy(id, opsport.ProcessRetentionFunc(func(context.Context, opsport.ProcessRetentionCommand) (opsport.ProcessRetentionReport, error) {
			panic("coverage inventory must not execute cleanup")
		})); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.BindHostReader(coverageReaderFixture{}); err != nil {
		t.Fatal(err)
	}
	if err := service.BindReleaseReader(coverageReaderFixture{}); err != nil {
		t.Fatal(err)
	}
}

func coverageByName(t *testing.T, service *RetentionService) (RetentionCoverage, map[string]RetentionResource) {
	t.Helper()
	out, err := service.Resources()
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]RetentionResource{}
	for _, item := range out.Items {
		key := item.Kind + ":" + item.Name
		if _, duplicate := items[key]; duplicate {
			t.Fatal("duplicate coverage resource", key)
		}
		items[key] = item
	}
	return out, items
}

func TestRetentionResourceCatalogBindsCanonicalRegistry(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../docs/governance/retention-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry map[string]json.RawMessage
	if err = json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	out, items := coverageByName(t, &RetentionService{})
	digest := sha256.Sum256(raw)
	if out.RegistrySHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("coverage catalog not bound to canonical registry; regenerate with scripts/check-retention-registry.py --write-runtime-snapshot")
	}
	total := 0
	for section, kind := range map[string]string{"tables": "table", "resources": "resource", "filesystem_prefixes": "filesystem_prefix"} {
		var source map[string]map[string]any
		if err = json.Unmarshal(registry[section], &source); err != nil {
			t.Fatal(err)
		}
		total += len(source)
		for name, row := range source {
			item, exists := items[kind+":"+name]
			if !exists || item.Owner != row["owner"] || item.Policy != row["policy"] || item.Reason != row["reason"] || item.Source == "" {
				t.Fatalf("missing or changed canonical resource %s:%s", kind, name)
			}
		}
	}
	if total != len(items) || out.InventoryScope != "committed_registry" {
		t.Fatal("inventory scope was broadened beyond the committed registry")
	}
	if bundle := items["filesystem_prefix:/opt/aicrm/source-bundles"]; bundle.CoverageStatus != "host_unobserved" || bundle.PolicyID != "release_artifact" || bundle.CleanupEntrypoint != "deploy/cleanup-source-bundles.py#inventory" {
		t.Fatalf("source bundles must stay visible without joining the runtime cleanup allowlist: %+v", bundle)
	}
	// A returned DTO can never modify the embedded source for later requests.
	out.Items[0].Owner = "mutated"
	again, _ := coverageByName(t, &RetentionService{})
	if again.Items[0].Owner == "mutated" {
		t.Fatal("returned catalog aliases shared state")
	}
}

func TestRetentionCoveragePreservesGapsAndExactPolicyBindings(t *testing.T) {
	service := &RetentionService{enabled: true}
	bindCoverageOwners(t, service)
	out, items := coverageByName(t, service)
	for name, policy := range map[string]string{
		"adminops_diagnostic_snapshots": "ops_snapshots", "adminops_diagnostic_events": "ops_diagnostics",
		"adminops_inspection_reports": "ops_report_payloads", "adminops_inspection_results": "ops_results",
		"adminops_inspection_runs": "ops_runs", "adminops_retention_runs": "ops_retention_history",
		"media_attachment_upload_parts": "media_upload_parts", "config_runtime_usage": "config_usage",
		"message_archive_sync_runs": "archive_sync_runs",
	} {
		item := items["table:"+name]
		if item.PolicyID != policy || item.CoverageStatus != "scheduled" || item.CleanupEntrypoint == "" || item.GapCode != "" {
			t.Fatalf("wrong policy binding %s: %+v", name, item)
		}
	}
	if out.AllowlistPolicyCount != 9 || out.Summary.ScheduledResources != 9 || out.Summary.GapResources != 35 || out.Summary.SecurityTTLResources != 11 || out.Summary.MixedPayloadResources != 19 || out.Summary.UnclassifiedResources != 1 || out.Summary.CoordinationResources != 13 {
		t.Fatalf("configured policies hid coverage gaps: %+v", out.Summary)
	}
	for _, item := range out.Items {
		if item.Policy == "security_ttl" && (item.CoverageStatus != "gap" || item.AuthorizationExpiry != "owner_security_ttl" || item.PolicyID != "" || item.CleanupEntrypoint != "") {
			t.Fatal("physical cleanup falsely implies authorization validity", item.Name)
		}
		if (item.Policy == "protected_mixed_payload" || item.Policy == "protected_unclassified") && (item.CoverageStatus != "gap" || item.PolicyID != "" || item.CleanupEntrypoint != "") {
			t.Fatal("unapproved cleanup appeared in coverage", item.Name)
		}
	}
	if receipt := items["table:adminops_cpu_profile_receipts"]; receipt.CoverageStatus != "protected" || receipt.PolicyID != "" {
		t.Fatal("CPU profile receipt confused with disposable artifact", receipt)
	}
	if artifact := items["filesystem_prefix:/var/lib/aicrm/process-diagnostics"]; artifact.PolicyID != "host_process_files" || artifact.CoverageStatus != "host_unobserved" || artifact.Policy != "30_days" {
		t.Fatal("CPU artifact lacks separate host evidence", artifact)
	}
	if items["table:river_job"].CoverageStatus != "native_unobserved" || items["table:message_archive_sync_state"].CoverageStatus != "owner_managed" {
		t.Fatal("native cleaner or required cursor falsely covered by DB allowlist")
	}
	service.enabled = false
	disabled, _ := coverageByName(t, service)
	if disabled.Summary.DisabledResources != 9 || disabled.Summary.ScheduledResources != 0 || disabled.Summary.GapResources != 35 {
		t.Fatal("runtime off lost coverage gaps", disabled.Summary)
	}
	unbound, absent := coverageByName(t, &RetentionService{enabled: true})
	if unbound.Summary.UnboundResources != 8 || absent["table:config_runtime_usage"].GapCode != "owner_port_not_bound" {
		t.Fatal("missing process/host binding marked scheduled", unbound.Summary)
	}
}

type coverageNonSuperAdmin struct{ inspectionTestSecurity }

func (coverageNonSuperAdmin) ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin}, nil
}

func TestRetentionResourcesHTTPIsBoundedReadOnlyAndSuperAdmin(t *testing.T) {
	service := &RetentionService{} // no database; this is metadata-only
	for _, test := range []struct {
		name, method, suffix string
		security             InspectionSecurity
		status               int
	}{
		{"allowed", "GET", "", inspectionTestSecurity{}, 200},
		{"denied", "GET", "", inspectionTestSecurity{deny: true}, 403},
		{"ordinary_admin", "GET", "", coverageNonSuperAdmin{}, 403},
		{"query_rejected", "GET", "?table=orders", inspectionTestSecurity{}, 400},
		{"write_rejected", "POST", "", inspectionTestSecurity{}, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, err := NewRetentionHandler(service, test.security)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(test.method, "/api/admin/ops-retention/resources"+test.suffix, nil))
			if w.Code != test.status || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Code == 200 {
				var result RetentionCoverage
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Summary.GapResources == 0 || result.AutomaticCleanupEnabled {
					t.Fatal("invalid coverage DTO", err)
				}
			}
		})
	}
}

func TestPostgreSQLRetentionAllowlistHealthNeverErasesCoverageGaps(t *testing.T) {
	service, pool := retentionTestService(t)
	bindCoverageOwners(t, service)
	now := time.Now().UTC()
	for _, policy := range service.Policies() {
		if validRetentionPolicy(policy.ID) {
			if _, err := pool.Exec(context.Background(), `INSERT INTO adminops_retention_runs(hour_key,policy,policy_version,cutoff,state,started_at,completed_at) VALUES($1,$2,'fixture',$1::timestamptz-interval '720 hours','completed',$1,$1)`, now, policy.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	health, err := service.RetentionHealth(context.Background(), now)
	if err != nil || health.Status != "ok" || health.Code != "allowlist_policies_fresh" || health.Metrics["fresh_policies"] != 9 || health.Metrics["coverage_gaps"] != 35 {
		t.Fatalf("allowlist success hid resource gaps: %+v %v", health, err)
	}
	if len(health.Metrics) > 32 {
		t.Fatal("inspection metric budget exceeded")
	}
	for key, value := range health.Metrics {
		if !safeInspectionCode.MatchString(key) || value < 0 {
			t.Fatal("unsafe coverage metric", key)
		}
	}
	service.enabled = false
	health, err = service.RetentionHealth(context.Background(), now)
	if err != nil || health.Status != "uncovered" || health.Metrics["coverage_gaps"] != 35 || health.Metrics["coverage_disabled"] != 9 {
		t.Fatal("disabled cleanup lost coverage evidence", health, err)
	}
	var count int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM adminops_retention_runs`).Scan(&count); err != nil || count != 9 {
		t.Fatal("coverage read mutated execution history", count, err)
	}
}
