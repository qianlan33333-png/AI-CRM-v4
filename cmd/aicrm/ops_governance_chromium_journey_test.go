package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

// OneID: diagnostics only; no identity resolution or assignment. Persistence:
// real Owner services and Composition HTTP use PostgreSQL; notifications remain
// disabled, so this browser journey cannot contact a Feishu group.
func TestPostgreSQLOpsGovernanceChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f := newProductExternalPushChromiumFixtureWithTimeout(t, 3*time.Minute)
	session, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	uow, err := platformpostgres.NewUnitOfWork(f.application.pool)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-76 * time.Minute)
	service, err := adminops.NewInspectionService(f.application.pool.Native(), uow, []opsport.InspectionCollector{
		opsport.CollectorFunc{ID: "runtime.endpoints", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
			return opsport.CheckObservation{Status: "ok", Code: "fixture_observed", ObservedAt: old}, nil
		}},
		opsport.CollectorFunc{ID: "runtime.resources", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
			return opsport.CheckObservation{Status: "unknown", Code: "fixture_source_unavailable", ObservedAt: old}, nil
		}},
	}, nil, adminops.InspectionOptions{ReleaseSHA: "governance-browser-fixture", Now: func() time.Time { return old }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ManualScan(f.ctx, "governance-browser-seed"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PrepareReport(f.ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = service.RecordDiagnosticObservation(f.ctx, opsport.DiagnosticObservation{Component: "runtime", Code: "fixture_error", Correlation: strings.Repeat("a", 32), RouteTemplate: "/admin/ops", JobRef: "river_1", EffectRef: "eer_1"}); err != nil {
		t.Fatal(err)
	}
	anonymous := httptest.NewRecorder()
	f.application.handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/admin/ops-inspections", nil))
	if anonymous.Code != http.StatusForbidden {
		t.Fatalf("anonymous ops read=%d", anonymous.Code)
	}
	forbidden := httptest.NewRequest(http.MethodPost, "/api/admin/ops-inspections/runs", strings.NewReader(`{}`))
	forbidden.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	forbidden.Header.Set("Idempotency-Key", "forbidden-csrf-run")
	denied := httptest.NewRecorder()
	f.application.handler.ServeHTTP(denied, forbidden)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF ops mutation=%d", denied.Code)
	}
	read := authenticatedAdminGet(t, f.application.handler, session, "/api/admin/ops-inspections")
	var snapshot opsport.InspectionOverview
	if err = json.Unmarshal(read.Body.Bytes(), &snapshot); err != nil || read.Code != http.StatusOK || snapshot.Fresh {
		t.Fatalf("stale composed overview status=%d fresh=%t err=%v", read.Code, snapshot.Fresh, err)
	}
	// Hold only this fixture's real River inspection queue. A different real
	// scheduled scan will complete while the browser's own command still waits.
	workers := river.NewWorkers()
	river.AddWorker(workers, adminops.NewInspectionWorker())
	queue, err := platformjobqueue.NewInsertClient(f.application.pool.Native(), workers)
	if err != nil {
		t.Fatal(err)
	}
	pauseDeadline := time.Now().Add(5 * time.Second)
	for {
		err = queue.QueuePause(f.ctx, adminops.InspectionQueue, nil)
		if err == nil {
			break
		}
		if time.Now().After(pauseDeadline) {
			t.Fatalf("pause fixture inspection queue: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = queue.QueueResume(ctx, adminops.InspectionQueue, nil)
	})
	unrelated, err := adminops.NewInspectionService(f.application.pool.Native(), uow, nil, nil, adminops.InspectionOptions{ReleaseSHA: "unrelated-scheduled-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	// This loopback test control only coordinates Owner/River execution. All
	// user-facing acceptance, completion, overview and Host reads remain real.
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/complete-unrelated":
			run, e := unrelated.Scan(r.Context(), time.Now())
			if e != nil {
				http.Error(w, "fixture scan failed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]int64{"run_id": run.ID})
		case "/resume":
			if e := queue.QueueResume(r.Context(), adminops.InspectionQueue, nil); e != nil {
				http.Error(w, "fixture resume failed", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(control.Close)
	screenshots := t.TempDir()
	if configured := platformconfig.AdminLayoutScreenshotDirectory(); configured != "" {
		screenshots = configured
		if err = os.MkdirAll(screenshots, 0700); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.CommandContext(f.ctx, "node", "--input-type=module", "-")
	command.Stdin = strings.NewReader(opsGovernanceChromiumScript)
	command.Env = append(os.Environ(), "AICRM_OPS_BROWSER_URL="+f.server.URL, "AICRM_OPS_BROWSER_SESSION="+session, "AICRM_OPS_BROWSER_CSRF="+csrf, "AICRM_OPS_BROWSER_SCREENSHOTS="+screenshots, "AICRM_OPS_BROWSER_CONTROL="+control.URL)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "ops_governance_chromium: PASS") {
		t.Fatalf("ops governance Chromium=%v\n%s", err, output)
	}
	t.Log(string(output))
	for _, name := range []string{"ops-overview-1440.png", "ops-overview-390.png", "ops-report-drawer.png"} {
		info, e := os.Stat(filepath.Join(screenshots, name))
		if e != nil || info.Size() < 512 {
			t.Fatalf("missing screenshot=%s err=%v", name, e)
		}
	}
	// The fixture runs the real composed River runtime. Verify that the
	// browser's acceptance became one completed job plus its permanent receipt,
	// rather than accepting HTTP 202 as proof of a finished scan.
	deadline := time.Now().Add(5 * time.Second)
	var commands, completions int
	for {
		err = f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*),count(*) FILTER(WHERE c.completed_at IS NOT NULL AND r.state IN ('completed','partial_failed') AND j.state='completed') FROM adminops_inspection_commands c LEFT JOIN adminops_inspection_runs r ON r.id=c.run_id LEFT JOIN river_job j ON j.id=c.job_id`).Scan(&commands, &completions)
		if err != nil {
			t.Fatal(err)
		}
		if commands == 1 && completions == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("manual acceptance=%d completed receipt/run/River=%d", commands, completions)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var actions, effects int
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adminops_inspection_issue_actions),(SELECT count(*) FROM external_effects WHERE owner='adminops')`).Scan(&actions, &effects); err != nil || actions != 1 || effects != 0 {
		t.Fatalf("browser durable actions=%d external effects=%d err=%v", actions, effects, err)
	}
}

const opsGovernanceChromiumScript = `
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { chromiumStartupTimeoutMS,chromiumStartupDiagnostic } from './internal/webshell/chromium_launch.mjs';
const origin=process.env.AICRM_OPS_BROWSER_URL,session=process.env.AICRM_OPS_BROWSER_SESSION,csrf=process.env.AICRM_OPS_BROWSER_CSRF,output=process.env.AICRM_OPS_BROWSER_SCREENSHOTS,control=process.env.AICRM_OPS_BROWSER_CONTROL;
const profile=await fs.mkdtemp(path.join(os.tmpdir(),'aicrm-ops-browser-'));
const executable=process.env.AICRM_CHROMIUM_BINARY||(process.platform==='darwin'?'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome':'chromium');
const child=spawn(executable,['--headless=new','--no-sandbox','--disable-gpu','--disable-background-networking','--no-first-run','--ignore-certificate-errors','--remote-debugging-port=0','--user-data-dir='+profile,'about:blank'],{stdio:['ignore','ignore','pipe']});
let stderr='',launchError,ws,seq=0;const waiting=new Map();
child.stderr.on('data',c=>stderr=(stderr+String(c)).slice(-4096));child.on('error',e=>launchError=e);
const delay=(ms=50)=>new Promise(r=>setTimeout(r,ms));
async function poll(test,message){for(let i=0;i<200;i++){if(await test())return;await delay();}throw new Error(message);}
try{
 let port;const deadline=Date.now()+chromiumStartupTimeoutMS;
 while(Date.now()<deadline){try{const candidate=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];if(/^\d+$/.test(candidate)){port=candidate;break;}}catch{}if(launchError||child.exitCode!==null||child.signalCode)break;await delay();}
 if(!port)throw new Error(chromiumStartupDiagnostic({profile,stderr,launchError,exitCode:child.exitCode,signalCode:child.signalCode}));
 const page=await(await fetch('http://127.0.0.1:'+port+'/json/new?about:blank',{method:'PUT'})).json();
 ws=new WebSocket(page.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true});});
 const exceptions=[];
 ws.addEventListener('message',event=>{const m=JSON.parse(event.data);if(m.id){const p=waiting.get(m.id);waiting.delete(m.id);m.error?p?.reject(new Error('CDP '+m.error.code)):p?.resolve(m.result);}else if(m.method==='Runtime.exceptionThrown'){exceptions.push(m.params.exceptionDetails.exception?.className||'runtime_exception');}});
 const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;waiting.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}));});
 const evaluate=async(expression)=>{const result=await call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(result.exceptionDetails)throw new Error(result.exceptionDetails.exception?.description||'page evaluation failed');return result.result?.value;};
 await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
 await call('Network.setCookies',{cookies:[{name:'aicrm_admin_session',value:session,url:origin,secure:true},{name:'aicrm_admin_csrf',value:csrf,url:origin,secure:true}]});
 await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await call('Page.navigate',{url:origin+'/admin/ops'});
 await poll(()=>evaluate("document.querySelector('#governance-admin-root [data-status=stale]')"),'stale Owner evidence must render in actual Host');
 assert.match(await evaluate("document.querySelector('.governance-freshness').textContent"),/过期/);
 assert.equal(await evaluate("document.querySelectorAll('.admin-topbar').length"),1);
 assert.equal(await evaluate("document.querySelectorAll('.admin-topbar [data-page-header-actions=governance] button').length"),2);
 assert.equal(await evaluate("fetch('/api/admin/ops-inspections',{credentials:'omit'}).then(r=>r.status)"),403);
 assert.equal(await evaluate("fetch('/api/admin/ops-inspections/commands/1',{credentials:'omit'}).then(r=>r.status)"),403);
 for(const width of [1440,390]){
  await call('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:width<600});await delay(100);
  assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth+2'),true,'overview must remain within '+width+'px viewport');
  const shot=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(output,'ops-overview-'+width+'.png'),Buffer.from(shot.data,'base64'));
 }
 await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await evaluate("document.querySelector('[data-tab=checks]').click()");await poll(()=>evaluate("document.querySelector('.governance-content table')"),'check table');
 assert.ok(await evaluate("document.querySelector('[data-status=stale]') && document.querySelector('[data-status=uncovered]')"));
 await evaluate("document.querySelector('[data-tab=issues]').click()");await poll(()=>evaluate("document.querySelector('[data-ack]')"),'actionable issue');
 const issue=await evaluate("fetch('/api/admin/ops-inspections').then(r=>r.json()).then(v=>v.issues.find(i=>i.id===Number(document.querySelector('[data-ack]').dataset.ack)))");
 await evaluate("document.querySelector('[data-ack]').click()");
 await poll(()=>evaluate("document.querySelector('[data-status=acknowledged]')"),'acknowledged issue readback');
 const current=await evaluate("fetch('/api/admin/ops-inspections').then(r=>r.json()).then(v=>v.issues.find(i=>i.id==="+issue.id+"))");assert.equal(current.status,'acknowledged');assert.equal(current.version,issue.version+1);
 const conflict=await evaluate("fetch('/api/admin/ops-inspections/issues/"+issue.id+"',{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':"+JSON.stringify(csrf)+",'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({version:"+issue.version+",status:'open'})}).then(r=>r.status)");assert.equal(conflict,409,'stale CAS cannot overwrite acknowledged issue');
 await evaluate("document.querySelector('[data-tab=reports]').click()");await poll(()=>evaluate("document.querySelector('[data-report]')"),'immutable report');
 await evaluate("document.querySelector('[data-report]').focus();document.querySelector('[data-report]').click()");await poll(()=>evaluate("document.querySelector('dialog[open] .governance-report')"),'shared report drawer');
 assert.match(await evaluate("document.querySelector('dialog[open]').textContent"),/CRM 每小时巡查/);
 const drawer=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(output,'ops-report-drawer.png'),Buffer.from(drawer.data,'base64'));
 await evaluate("document.querySelector('dialog[open] .shared-detail-drawer__close').click()");assert.equal(await evaluate("document.activeElement.hasAttribute('data-report')"),true);
 await evaluate("document.querySelector('[data-tab=diagnostics]').click()");await poll(()=>evaluate("document.querySelector('.governance-content').textContent.includes('fixture_error')"),'diagnostic readback');
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),/river_1.*eer_1/);
 await evaluate("document.querySelector('[name=correlation]').value='a'.repeat(32);document.querySelector('.governance-query').requestSubmit()");
 await poll(()=>evaluate("document.querySelector('.governance-content tbody')?.textContent.includes('fixture_error')"),'correlation query reads the same persisted diagnostic');
 assert.equal(await evaluate("document.querySelectorAll('.governance-content tbody tr').length"),1);
 await evaluate("document.querySelector('[data-tab=retention]').click()");await poll(()=>evaluate("document.querySelector('[data-preview=ops_results]')"),'read-only retention preview control');
 await evaluate("document.querySelector('[data-preview=ops_results]').click()");await poll(()=>evaluate("document.querySelector('dialog[open]')?.textContent.includes('清理候选预览')"),'real Owner cleanup preview');
 assert.match(await evaluate("document.querySelector('dialog[open]').textContent"),/候选：0/);
 await evaluate("document.querySelector('dialog[open] .shared-detail-drawer__close').click()");
 assert.equal(await evaluate("fetch('/api/admin/ops-retention/resources',{credentials:'omit'}).then(r=>r.status)"),403);
 assert.equal(await evaluate("fetch('/api/admin/ops-retention/resources?owner=adminops').then(r=>r.status)"),400,'registry endpoint does not accept unbounded resource queries');
 const coverage=await evaluate("fetch('/api/admin/ops-retention/resources').then(r=>r.json())");
 assert.equal(coverage.inventory_scope,'committed_registry');assert.ok(coverage.items.length>100);assert.ok(coverage.summary.gap_resources>0);
 await evaluate("document.querySelector('[data-tab=resources]').click()");await poll(()=>evaluate("document.querySelector('[data-resource-count]')"),'real registered resource coverage');
 assert.equal(await evaluate("document.querySelectorAll('[data-resource-table] tbody tr').length"),25);
 assert.match(await evaluate("document.querySelector('[data-resource-count]').textContent"),new RegExp('/ '+coverage.items.length+' 项'));
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),/白名单健康不代表全部资源已覆盖/);
 await evaluate("document.querySelector('[data-resource-page=next]').click()");assert.match(await evaluate("document.querySelector('[data-resource-count]').textContent"),/第 2/);
 await evaluate("document.querySelector('[name=resource_status]').value='native_unobserved';document.querySelector('[data-resource-filters]').requestSubmit()");
 assert.match(await evaluate("document.querySelector('[data-resource-table]').textContent"),/river_job.*原生执行未观察/s);
 assert.equal(await evaluate("!!document.querySelector('[data-coverage-status=native_unobserved][data-status=unknown]')"),true,'native cleaner is never green without observation');
 await evaluate("document.querySelector('[data-resource-detail]').focus();document.querySelector('[data-resource-detail]').click()");await poll(()=>evaluate("document.querySelector('dialog[open]')?.textContent.includes('资源治理依据')"),'shared resource evidence drawer');
 assert.match(await evaluate("document.querySelector('dialog[open]').textContent"),/Owner：platform.*原生清理器.*登记来源.*SHA256/s);
 await evaluate("document.querySelector('dialog[open] .shared-detail-drawer__close').click()");assert.equal(await evaluate("document.activeElement.hasAttribute('data-resource-detail')"),true);
 await evaluate("document.querySelector('[data-resource-reset]').click()");
 for(const width of [1440,390]){
  await call('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:width<600});await delay(100);
  assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth+2'),true,'resource coverage must remain within '+width+'px viewport');
  const shot=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(output,'ops-resources-'+width+'.png'),Buffer.from(shot.data,'base64'));
 }
 await evaluate("document.querySelector('[data-tab=overview]').click()");await poll(()=>evaluate("document.querySelector('.governance-freshness')"),'overview restored');
 const before=await evaluate("fetch('/api/admin/ops-inspections').then(r=>r.json()).then(v=>v.latest.id)");
 await evaluate("window.__opsOriginalFetch=window.fetch;window.fetch=async(...args)=>{const response=await window.__opsOriginalFetch(...args);if(String(args[0])==='/api/admin/ops-inspections/runs'){window.__opsManualAccepted={status:response.status,body:await response.clone().json()};}return response;}");
 await evaluate("[...document.querySelectorAll('[data-page-header-actions=governance] button')].find(b=>b.textContent==='立即巡查').click()");
 await poll(()=>evaluate("!!window.__opsManualAccepted"),'manual request receives durable acceptance');
 const accepted=await evaluate("window.__opsManualAccepted");assert.equal(accepted.status,202);assert.equal(accepted.body.state,'accepted');assert.ok(accepted.body.job_id>0);assert.equal(accepted.body.results,undefined);
 await evaluate("window.fetch=window.__opsOriginalFetch");
 const otherResponse=await fetch(control+'/complete-unrelated',{method:'POST'});assert.equal(otherResponse.status,200);const other=await otherResponse.json();assert.ok(other.run_id>before);
 const commandPath='/api/admin/ops-inspections/commands/'+accepted.body.job_id;
 const pendingCommand=await evaluate("fetch("+JSON.stringify(commandPath)+").then(r=>r.json())");assert.equal(pendingCommand.state,'accepted');assert.equal(pendingCommand.run_id,undefined);
 await evaluate("[...document.querySelectorAll('[data-page-header-actions=governance] button')].find(b=>b.textContent==='刷新').click()");
 await poll(()=>evaluate("document.querySelector('.governance-freshness')?.textContent.includes('unrelated-sc')"),'unrelated completed scan appears in latest overview');
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),/巡查已受理.*等待执行结果/,'another completed scan must not finish this manual command');
 const resume=await fetch(control+'/resume',{method:'POST'});assert.equal(resume.status,204);
 await poll(()=>evaluate("fetch("+JSON.stringify(commandPath)+").then(r=>r.json()).then(v=>v.state==='completed' && v.job_id==="+accepted.body.job_id+" && v.run_id>0 && !!v.completed_at)"),'matching manual command durable completion');
 await evaluate("[...document.querySelectorAll('[data-page-header-actions=governance] button')].find(b=>b.textContent==='刷新').click()");
 await poll(()=>evaluate("!!document.querySelector('.governance-freshness') && !document.querySelector('.governance-content').textContent.includes('等待执行结果')"),'only the completed command receipt clears its waiting indicator');
 assert.deepEqual(exceptions,[],'actual Host has no runtime exceptions');
 assert.equal(await evaluate("fetch('/api/admin/ops-governance/outcomes',{credentials:'omit'}).then(r=>r.status)"),403);
 await evaluate("document.querySelector('[data-tab=outcomes]').click()");await poll(()=>evaluate("document.querySelector('[data-outcomes-episode]')"),'real governance episode list');
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),/全量|完整/);
 const episodeID=await evaluate("Number(document.querySelector('[data-outcomes-episode]').dataset.outcomesEpisode)");
 const episodeBefore=await evaluate("fetch('/api/admin/ops-governance/episodes/"+episodeID+"').then(r=>r.json())");
 await evaluate("document.querySelector('[data-outcomes-episode]').click()");await poll(()=>evaluate("document.querySelector('dialog[open] .governance-attribution')"),'shared attribution drawer');
 await evaluate("const f=document.querySelector('dialog[open] form');f.querySelector('[name=classification]').value='observation_gap';f.querySelector('[name=effort_minutes]').value='3';f.requestSubmit()");
 await poll(()=>evaluate("fetch('/api/admin/ops-governance/episodes/"+episodeID+"').then(r=>r.json()).then(e=>e.classification==='observation_gap'&&e.effort_minutes===3)"),'attribution durable readback');
 const staleAttribution={expected_version:episodeBefore.version,classification:'observation_gap',escaped_defect:'unclassified',change_failure:'unclassified',caused_by_release_sequence:null,root_cause:'unknown',remediation:'unknown',fault_started_at:null,fault_start_basis:'unknown',effort_minutes:4};
 assert.equal(await evaluate("fetch('/api/admin/ops-governance/episodes/"+episodeID+"/attribution',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':"+JSON.stringify(csrf)+",'Idempotency-Key':crypto.randomUUID()},body:"+JSON.stringify(JSON.stringify(staleAttribution))+"}).then(r=>r.status)"),409,'attribution stale CAS must fail');
 await evaluate("document.querySelector('dialog[open] .shared-detail-drawer__close').click()");
 const outcomes=await evaluate("fetch('/api/admin/ops-governance/outcomes').then(r=>r.json())");assert.equal(outcomes.deployments.full_change_failure_rate_available,false);assert.ok(outcomes.effort_minutes_total>=3);assert.equal(outcomes.mttd.mean_seconds,null);
 for(const width of [1440,390]){await call('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:width<600});await delay(100);assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth+2'),true,'outcomes fit '+width);}
 console.log('ops_governance_chromium: PASS — real Host, stale/unknown, authorization, CAS, drawer, manual scan, 1440/390px');
}finally{
 ws?.close();if(child.exitCode===null&&!child.signalCode){child.kill('SIGTERM');await Promise.race([new Promise(r=>child.once('exit',r)),delay(3000)]);if(child.exitCode===null&&!child.signalCode){child.kill('SIGKILL');await delay(500);}}
 await fs.rm(profile,{recursive:true,force:true,maxRetries:10,retryDelay:100});
}
`
