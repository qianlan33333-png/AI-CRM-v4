package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/adminops"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// OneID: not involved; this is an administrator-only diagnostic command.
// Persistence: real Access sessions/CSRF, AdminOps PostgreSQL receipts, and HTTP.
// The sampler is an explicit Port fixture returning a valid label-free pprof;
// the platform suite separately proves real five-second capture and scrubbing.
// No Provider call or recoverable task is introduced by this browser journey.
func TestPostgreSQLOpsCPUProfileChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f := newProductExternalPushChromiumFixtureWithTimeout(t, 3*time.Minute)
	session, csrf := adminAccessLogin(t, f.application.handler, "product-browser-owner", "product-browser-owner-password")
	const base = "/api/admin/ops-diagnostics/cpu-profiles"
	// Before substituting only the sampler seam, the real Composition must
	// expose the route and keep new capture disabled by default.
	defaultRead := authenticatedAdminGet(t, f.application.handler, session, base)
	var defaults struct {
		Enabled bool   `json:"enabled"`
		Target  string `json:"target"`
	}
	if err := json.Unmarshal(defaultRead.Body.Bytes(), &defaults); err != nil || defaultRead.Code != http.StatusOK || defaults.Enabled || defaults.Target != "api" {
		t.Fatalf("default composed profile route=%d enabled=%t target=%s err=%v", defaultRead.Code, defaults.Enabled, defaults.Target, err)
	}
	// Seed only a local fixture identity, then authenticate it through the real
	// Access login. A non-superadmin principal is never injected by the test.
	_, err := f.application.pool.Native().Exec(f.ctx, `WITH ordinary AS (
	 INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,login_enabled,access_granted_at)
	 SELECT 'cpu-profile-viewer',password_hash,'CPU profile viewer','cpu-profile-viewer',true,true,clock_timestamp()
	 FROM admin_users WHERE username='product-browser-owner' RETURNING id)
	 INSERT INTO admin_user_roles(admin_user_id,role_code) SELECT id,'viewer' FROM ordinary`)
	if err != nil {
		t.Fatal(err)
	}
	viewerSession, viewerCSRF := adminAccessLogin(t, f.application.handler, "cpu-profile-viewer", "product-browser-owner-password")
	fixture := newOpsCPUProfileBrowserSampler(t)
	const release = "0123456789abcdef0123456789abcdef01234567"
	service, err := adminops.NewCPUProfileService(f.application.pool.Native(), fixture, release, true)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := adminops.NewCPUProfileHandler(service, opsSecurity{requestAccessSecurity{authentication: f.application.authentication}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(base, handler)
	mux.Handle(base+"/", handler)
	mux.Handle("/", f.application.handler)
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	// The expired file still exists in the fixture: denial must come from the
	// Owner's 720h policy, rather than a coincidental missing-file response.
	const expiredID = "ffffffffffffffffffffffffffffffff"
	fixture.payloads[expiredID] = fixture.payload
	digest := sha256.Sum256(fixture.payload)
	_, err = f.application.pool.Native().Exec(f.ctx, `INSERT INTO adminops_cpu_profile_receipts
	 (id,request_digest,actor_id,release_sha,state,artifact_sha256,artifact_bytes,accepted_at,expires_at,completed_at)
	 SELECT $1,repeat('e',64),id,$2,'completed',$3,$4,statement_timestamp()-interval '721 hours',statement_timestamp()-interval '1 hour',statement_timestamp()-interval '721 hours'
	 FROM admin_users WHERE username='product-browser-owner'`, expiredID, release, hex.EncodeToString(digest[:]), len(fixture.payload))
	if err != nil {
		t.Fatal(err)
	}
	// Also prove the real session cannot mutate without its CSRF cookie/header.
	request := httptest.NewRequest(http.MethodPost, base, strings.NewReader(`{}`))
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.Header.Set("Idempotency-Key", "missing-real-csrf")
	denied := httptest.NewRecorder()
	mux.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("real session without CSRF=%d", denied.Code)
	}
	downloads := t.TempDir()
	command := exec.CommandContext(f.ctx, "node", "--input-type=module", "-")
	command.Stdin = strings.NewReader(opsCPUProfileChromiumScript)
	command.Env = append(os.Environ(),
		"AICRM_CPU_BROWSER_URL="+server.URL, "AICRM_CPU_BROWSER_SESSION="+session, "AICRM_CPU_BROWSER_CSRF="+csrf,
		"AICRM_CPU_BROWSER_VIEWER_SESSION="+viewerSession, "AICRM_CPU_BROWSER_VIEWER_CSRF="+viewerCSRF,
		"AICRM_CPU_BROWSER_RELEASE="+release, "AICRM_CPU_BROWSER_DOWNLOADS="+downloads,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "ops_cpu_profile_chromium: PASS") {
		t.Fatalf("CPU profile Chromium=%v\n%s", err, output)
	}
	t.Log(string(output))
	var count int
	var completedID string
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT count(*),min(id) FROM adminops_cpu_profile_receipts WHERE id<>$1 AND state='completed'`, expiredID).Scan(&count, &completedID); err != nil || count != 1 {
		t.Fatalf("expected one durable completed command, got=%d err=%v", count, err)
	}
	fixture.mu.Lock()
	captures, expiredRead := fixture.captures, fixture.reads[expiredID]
	fixture.mu.Unlock()
	if captures != 1 || expiredRead != 0 {
		t.Fatalf("lost response/denial triggered sampling=%d expired file reads=%d", captures, expiredRead)
	}
	data, err := os.ReadFile(filepath.Join(downloads, completedID+".cpu.pprof"))
	if err != nil || !bytes.Equal(data, fixture.payload) {
		t.Fatalf("actual browser download differs from verified artifact: %v", err)
	}
	parsed, err := profile.ParseData(data)
	if err != nil || parsed.CheckValid() != nil {
		t.Fatalf("download is not a standard pprof: %v", err)
	}
	for _, sample := range parsed.Sample {
		if len(sample.Label)+len(sample.NumLabel)+len(sample.NumUnit) != 0 {
			t.Fatal("download fixture must have no diagnostic labels")
		}
	}
}

// This fixture has no raw profiler, network target, path, duration or PID input.
// It enforces opaque IDs and one artifact per accepted command, and returns
// exact bytes so the Owner's digest verification is exercised on downloads.
type opsCPUProfileBrowserSampler struct {
	mu       sync.Mutex
	payload  []byte
	payloads map[string][]byte
	reads    map[string]int
	captures int
}

func newOpsCPUProfileBrowserSampler(t *testing.T) *opsCPUProfileBrowserSampler {
	t.Helper()
	function := &profile.Function{ID: 1, Name: "fixture.safeCPU", SystemName: "fixture.safeCPU", Filename: "fixture.go"}
	location := &profile.Location{ID: 1, Line: []profile.Line{{Function: function, Line: 1}}}
	p := &profile.Profile{SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}}, PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}, Period: 10000000, DurationNanos: int64(5 * time.Second), Function: []*profile.Function{function}, Location: []*profile.Location{location}, Sample: []*profile.Sample{{Location: []*profile.Location{location}, Value: []int64{1, 10000000}}}}
	if err := p.CheckValid(); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := p.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	return &opsCPUProfileBrowserSampler{payload: buffer.Bytes(), payloads: map[string][]byte{}, reads: map[string]int{}}
}

func (p *opsCPUProfileBrowserSampler) Capture(ctx context.Context, id string) (platformport.CPUProfileArtifact, error) {
	if ctx.Err() != nil {
		return platformport.CPUProfileArtifact{}, ctx.Err()
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 || strings.ToLower(id) != id {
		return platformport.CPUProfileArtifact{}, errors.New("fixture received invalid artifact id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.payloads[id]; exists {
		return platformport.CPUProfileArtifact{}, errors.New("fixture received duplicate capture")
	}
	p.captures++
	p.payloads[id] = p.payload
	digest := sha256.Sum256(p.payload)
	return platformport.CPUProfileArtifact{SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(p.payload))}, nil
}

func (p *opsCPUProfileBrowserSampler) Read(ctx context.Context, id string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads[id]++
	data, exists := p.payloads[id]
	if !exists {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	return append([]byte(nil), data...), nil
}

const opsCPUProfileChromiumScript = `
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { chromiumStartupTimeoutMS, chromiumStartupDiagnostic } from './internal/webshell/chromium_launch.mjs';
const origin=process.env.AICRM_CPU_BROWSER_URL,session=process.env.AICRM_CPU_BROWSER_SESSION,csrf=process.env.AICRM_CPU_BROWSER_CSRF,downloads=process.env.AICRM_CPU_BROWSER_DOWNLOADS;
const base='/api/admin/ops-diagnostics/cpu-profiles',expired='f'.repeat(32),release=process.env.AICRM_CPU_BROWSER_RELEASE;
const profileDir=await fs.mkdtemp(path.join(os.tmpdir(),'aicrm-cpu-browser-'));
const executable=process.env.AICRM_CHROMIUM_BINARY||(process.platform==='darwin'?'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome':'chromium');
const child=spawn(executable,['--headless=new','--no-sandbox','--disable-gpu','--disable-background-networking','--no-first-run','--ignore-certificate-errors','--remote-debugging-port=0','--user-data-dir='+profileDir,'about:blank'],{stdio:['ignore','ignore','pipe']});
let stderr='',launchError,ws,seq=0;const waiting=new Map();
child.stderr.on('data',c=>stderr=(stderr+String(c)).slice(-4096));child.on('error',e=>launchError=e);
const delay=(ms=50)=>new Promise(r=>setTimeout(r,ms));
async function poll(test,message){for(let i=0;i<200;i++){if(await test())return;await delay();}throw new Error(message);}
try {
 let port;const deadline=Date.now()+chromiumStartupTimeoutMS;
 while(Date.now()<deadline){try{const candidate=(await fs.readFile(path.join(profileDir,'DevToolsActivePort'),'utf8')).split('\n')[0];if(/^\d+$/.test(candidate)){port=candidate;break;}}catch{}if(launchError||child.exitCode!==null||child.signalCode)break;await delay();}
 if(!port)throw new Error(chromiumStartupDiagnostic({profile:profileDir,stderr,launchError,exitCode:child.exitCode,signalCode:child.signalCode}));
 const page=await(await fetch('http://127.0.0.1:'+port+'/json/new?about:blank',{method:'PUT'})).json();
 ws=new WebSocket(page.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true});});
 const exceptions=[];
 ws.addEventListener('message',event=>{const m=JSON.parse(event.data);if(m.id){const p=waiting.get(m.id);waiting.delete(m.id);m.error?p?.reject(new Error('CDP '+m.error.code)):p?.resolve(m.result);}else if(m.method==='Runtime.exceptionThrown'){exceptions.push(m.params.exceptionDetails.exception?.className||'runtime_exception');}});
 const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;waiting.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}));});
 const evaluate=async expression=>{const result=await call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(result.exceptionDetails)throw new Error(result.exceptionDetails.exception?.description||'page evaluation failed');return result.result?.value;};
 const cookies=async(s,c)=>call('Network.setCookies',{cookies:[{name:'aicrm_admin_session',value:s,url:origin,secure:true},{name:'aicrm_admin_csrf',value:c,url:origin,secure:true}]});
 await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
 await call('Browser.setDownloadBehavior',{behavior:'allow',downloadPath:downloads});
 await cookies(session,csrf);
 await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await call('Page.navigate',{url:origin+'/admin/ops'});
 await poll(()=>evaluate("!!document.querySelector('[data-tab=profiles]')"),'actual governance Host performance tab');
 assert.equal(await evaluate("document.querySelector('[data-tab=profiles]').textContent"),'性能采样');
 await evaluate("document.querySelector('[data-tab=profiles]').click()");
 const button=label=>"[...document.querySelectorAll('[data-page-header-actions=governance] button')].find(b=>b.textContent==="+JSON.stringify(label)+")";
 await poll(()=>evaluate("!!("+button('采样 5 秒')+" && !"+button('采样 5 秒')+".disabled)"),'enabled fixed five-second capture');
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),/Worker 进程尚未覆盖/);
 assert.equal(await evaluate("fetch('"+base+"',{credentials:'omit'}).then(r=>r.status)"),403);
 assert.equal(await evaluate("fetch('"+base+"',{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':'browser-no-csrf'},body:'{}'}).then(r=>r.status)"),403);
 // Let the real command finish, then simulate only loss of its HTTP response.
 // The UI must reuse its key when the user asks for the previous result.
 await evaluate("window.__cpuFetch=window.fetch;window.__cpuPosts=[];window.fetch=async(...args)=>{const response=await window.__cpuFetch(...args);if(String(args[0])==='"+base+"' && args[1]?.method==='POST'){window.__cpuPosts.push({key:new Headers(args[1].headers).get('Idempotency-Key'),status:response.status,result:await response.clone().json()});if(window.__cpuPosts.length===1)throw new TypeError('fixture response unavailable');}return response;}");
 await evaluate(button('采样 5 秒')+'.click()');
 await poll(()=>evaluate("window.__cpuPosts.length===1 && !!document.querySelector('[role=alert]')"),'lost response remains unconfirmed');
 await poll(()=>evaluate("!!("+button('查看上次采样结果')+" && !"+button('查看上次采样结果')+".disabled)"),'same-key recovery control');
 await evaluate(button('查看上次采样结果')+'.click()');
 await poll(()=>evaluate("window.__cpuPosts.length===2 && !!document.querySelector('.governance-content a[download]')"),'confirmed replay becomes downloadable');
 const posts=await evaluate('window.__cpuPosts');
 assert.equal(posts[0].status,200);assert.equal(posts[0].result.state,'completed');assert.equal(posts[0].result.replay,false);
 assert.ok(posts[0].key);assert.equal(posts[1].key,posts[0].key);assert.equal(posts[1].result.id,posts[0].result.id);assert.equal(posts[1].result.replay,true);
 assert.equal(posts[1].result.target,'api');assert.equal(posts[1].result.duration_seconds,5);assert.equal(posts[1].result.release_sha,release);
 assert.match(await evaluate("document.querySelector('.governance-content').textContent"),new RegExp(release));
 assert.equal(await evaluate("document.querySelectorAll('.governance-content tbody tr').length"),1,'expired metadata excluded');
 const id=posts[1].result.id;
 assert.equal(await evaluate("document.querySelector('.governance-content a[download]').getAttribute('href')"),base+'/'+id+'/download');
 await evaluate("document.querySelector('.governance-content a[download]').click()");
 const download=path.join(downloads,id+'.cpu.pprof');
 await poll(async()=>{try{return (await fs.stat(download)).size===posts[1].result.bytes;}catch{return false;}},'actual browser file download');
 const payload=await fs.readFile(download);assert.equal(payload[0],0x1f);assert.equal(payload[1],0x8b);
 const ttl=await evaluate("Promise.all(['"+base+'/'+expired+"','"+base+'/'+expired+"/download'].map(p=>fetch(p).then(r=>r.status)))");
 assert.deepEqual(ttl,[410,410],'expired receipt and existing artifact both refused');
 await evaluate('window.fetch=window.__cpuFetch');
 await cookies(process.env.AICRM_CPU_BROWSER_VIEWER_SESSION,process.env.AICRM_CPU_BROWSER_VIEWER_CSRF);
 const viewerCSRF=JSON.stringify(process.env.AICRM_CPU_BROWSER_VIEWER_CSRF);
 const denied=await evaluate("Promise.all([fetch('"+base+"').then(r=>r.status),fetch('"+base+'/'+id+"/download').then(r=>r.status),fetch('"+base+"',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':"+viewerCSRF+",'Idempotency-Key':'viewer-valid-csrf'},body:'{}'}).then(r=>r.status)])");
 assert.deepEqual(denied,[403,403,403],'real ordinary session denied list/download/capture');
 await evaluate(button('刷新')+'.click()');
 await poll(()=>evaluate("document.querySelector('[role=alert]')?.textContent.includes('超级管理员权限')"),'ordinary user sees actual authorization error');
 assert.deepEqual(exceptions,[],'actual governance Host has no uncaught browser errors');
 console.log('ops_cpu_profile_chromium: PASS — actual Access/CSRF/Host/PG, lost-response same-key replay, version, standard pprof fixture download, 720h, viewer denial; no real CPU capture in this fixture');
} finally {
 ws?.close();if(child.exitCode===null&&!child.signalCode){child.kill('SIGTERM');await Promise.race([new Promise(r=>child.once('exit',r)),delay(3000)]);if(child.exitCode===null&&!child.signalCode){child.kill('SIGKILL');await delay(500);}}
 await fs.rm(profileDir,{recursive:true,force:true,maxRetries:10,retryDelay:100});
}
`
