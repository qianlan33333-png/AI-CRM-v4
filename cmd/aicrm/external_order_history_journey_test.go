package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// This uses the real maintenance command, PostgreSQL owner transactions,
// authentication and rendered Host. Installed mode serves package bytes only.
func TestPostgreSQLExternalOrderHistoryChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	f := newAudienceConfirmationChromiumFixture(t)
	pool := f.application.pool.Native()
	var id int64
	if e := pool.QueryRow(f.ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&id); e != nil {
		t.Fatal(e)
	}
	const scope = "wechat-open-platform:history-browser-fixture"
	if _, e := pool.Exec(f.ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'unionid',$2,'history-browser-union','verified','test',1,now())`, id, scope); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(t.TempDir(), "history.csv")
	csvFile, e := os.Create(source)
	if e != nil {
		t.Fatal(e)
	}
	w := csv.NewWriter(csvFile)
	w.Write([]string{"内部订单号", "用户UNION_ID", "买家手机号", "商品ID", "商品名称", "支付时间", "订单状态", "售后状态", "订单实收金额", "订单已成功退款金额（出）"})
	w.WriteAll([][]string{{"external-browser-known", "history-browser-union", "13800000077", "bundle/A", "原组合商品", "2024-01-02 03:04:05", "已完成", "", "123.45", "0"}, {"external-browser-unknown", "history-browser-union", "13800000077", "free", "历史零元", "", "已关闭", "", "0", "0"},
		{"external-browser-refund", "history-browser-union", "13800000077", "refund", "退款只补号", "2024-01-01 03:04:05", "已完成", "全额退款", "20.01", "20.01"}})
	w.Flush()
	csvFile.Close()
	if e = w.Error(); e != nil {
		t.Fatal(e)
	}
	binary := os.Getenv("AICRM_INSTALLED_EXTERNAL_HISTORY_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "migrate-external-orders")
		c := exec.Command("go", "build", "-o", binary, "./cmd/migrate-external-orders")
		c.Dir = root
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("maintenance build %v %s", e, out)
		}
	} else {
		if !filepath.IsAbs(binary) {
			t.Fatal("installed tool path must be absolute")
		}
		b, e := os.ReadFile(binary)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("installed_external_history_tool_sha256=%x", sha256.Sum256(b))
	}
	run := func(mode string, manifest, plan string) map[string]any {
		args := []string{"--mode", mode, "--csv", source, "--union-scope", scope}
		if manifest != "" {
			args = append(args, "--manifest-sha256", manifest)
		}
		if plan != "" {
			args = append(args, "--plan-sha256", plan)
		}
		c := exec.CommandContext(f.ctx, binary, args...)
		c.Env = []string{"AICRM_DATABASE_URL=" + pool.Config().ConnString(), "AICRM_IDENTITY_PHONE_DATA_KEY=" + base64.RawStdEncoding.EncodeToString(f.dataKey)}
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%s command %v %s", mode, e, out)
		}
		r := map[string]any{}
		if e = json.Unmarshal(out, &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	initial := run("inspect", "", "")
	manifest, plan := initial["manifest_sha256"].(string), initial["plan_sha256"].(string)
	applied := run("apply", manifest, plan)
	if applied["Imported"] != float64(1) || applied["Filled"] != float64(1) {
		t.Fatalf("apply %v", applied)
	}
	reread := run("inspect", "", "")
	replay := run("apply", manifest, reread["plan_sha256"].(string))
	if replay["Replayed"] != float64(1) || replay["Imported"] != float64(0) {
		t.Fatalf("replay %v", replay)
	}
	reconciled := run("reconcile", manifest, "")
	if reconciled["Reconciled"] != float64(1) || reconciled["refund_phone_only_reconciled"] != float64(1) {
		t.Fatal("command reconciliation failed")
	}
	c := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(file), "external_order_history_chromium_journey.mjs"))
	c.Env = append(os.Environ(), "AICRM_EXTERNAL_HISTORY_TEST_URL="+f.server.URL, "AICRM_EXTERNAL_HISTORY_SCREENSHOTS="+f.screenshots)
	out, e := c.CombinedOutput()
	if e != nil || !strings.Contains(string(out), "external_order_history_chromium: PASS") {
		t.Fatalf("browser journey %v %s", e, out)
	}
	t.Log(string(out))
}
