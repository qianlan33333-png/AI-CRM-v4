import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { JSDOM } from 'jsdom';

const template = await fs.readFile(new URL('../../templates/admin_access.html', import.meta.url), 'utf8');
const script = await fs.readFile(new URL('./admin_access.js', import.meta.url), 'utf8');
const dom = new JSDOM(template, {url:'https://crm.test/admin/config/login-access', runScripts:'outside-only'});
const w = dom.window, d = w.document;
w.Headers = Headers;
d.cookie = 'aicrm_admin_csrf=fixture-csrf';
let users = [
  {admin_user_id:1,display_name:'Existing admin',wecom_userid:'existing',role:'admin',authorized_account:true,login_enabled:true,actions:{}},
  {admin_user_id:2,display_name:'Pending existing',wecom_userid:'pending-existing',role:'viewer',authorized_account:false,login_enabled:false,actions:{provision_access:true}},
];
const calls = [];
let mode = 'success', releaseRead;
const reply = (payload,status=200) => new Response(JSON.stringify(payload),{status});
const listing = () => ({ok:true,users,actor:{admin_user_id:1,role:'admin'},capabilities:{provision_admin:false,provision_viewer:true}});
w.fetch = async (url,options={}) => {
  const path = new URL(String(url),w.location.href).pathname;
  calls.push({path,method:options.method,body:options.body,csrf:options.headers.get('X-CSRF-Token')});
  if (path.endsWith('/refresh')) {
    if (mode === 'forbidden') return reply({error:'permission_denied'},403);
    if (mode === 'failure') return reply({error:'enterprise_directory_unavailable'},503);
    if (mode === 'no-receipt') return reply({ok:true});
    users[0].display_name='Current admin';
    if (!users.some(u=>u.admin_user_id===3)) users.push({admin_user_id:3,display_name:'New employee',wecom_userid:'new-staff',role:'viewer',authorized_account:false,login_enabled:false,actions:{provision_access:true}});
    return reply({ok:true,directory_refreshed:true,discovered:3,created:1,names_updated:1});
  }
  if (path==='/api/admin/access/users' && options.method==='POST') {
    const body=JSON.parse(options.body);
    assert.deepEqual(body,{wecom_userid:'new-staff',role:'viewer'});
    const user=users.find(u=>u.wecom_userid===body.wecom_userid);
    user.authorized_account=true;user.login_enabled=true;user.actions={set_login_enabled:true};
    return reply({ok:true});
  }
  if (path==='/api/admin/access/users' && options.method==='GET') {
    if (mode==='read-failure') return reply({error:'internal_error'},500);
    if (mode==='delayed-read') return new Promise(resolve=>{releaseRead=()=>resolve(reply(listing()));});
    return reply(listing());
  }
  throw new Error(`unexpected request ${path}`);
};
const tick = () => new Promise(resolve=>setTimeout(resolve,15));
const rows = () => [...d.querySelectorAll('#admin-access-users-body tr')];
try {
  w.eval(script);await tick();
  assert.equal(rows().length,2,'previously discovered ungranted staff is visible');
  assert.equal(rows()[1].querySelector('td[data-label="权限"]').textContent,'未开通');
  d.querySelector('#admin-access-refresh').click();await tick();
  assert.equal(calls.find(c=>c.path.endsWith('/refresh')).method,'POST');
  assert.equal(calls.find(c=>c.path.endsWith('/refresh')).csrf,'fixture-csrf');
  assert.equal(rows().length,3);
  assert.match(rows()[0].textContent,/Current admin/);
  const newRow=rows().find(r=>r.textContent.includes('new-staff'));
  assert.equal(newRow.querySelector('td[data-label="登录状态"]').textContent,'未开通');
  assert.doesNotMatch(newRow.textContent,/可登录|管理/);
  assert.match(d.querySelector('#admin-access-alert').textContent,/员工名单已刷新.*新增 1 名/);
  assert.equal(calls.filter(c=>c.path==='/api/admin/access/users' && c.method==='POST').length,0,'refresh does not grant access');
  newRow.querySelector('[data-access-action="provision-user"]').click();
  assert.equal(d.querySelector('#admin-access-provision-dialog').hidden,false);
  assert.equal(d.querySelector('#admin-access-provision-role-step').hidden,false);
  assert.equal(d.querySelector('[data-role-option="admin"]').hidden,true,'ordinary administrator cannot grant admin');
  const role=d.querySelector('input[name="provision-role"][value="viewer"]');role.checked=true;role.dispatchEvent(new w.Event('change'));
  d.querySelector('#admin-access-provision-next').click();
  assert.match(d.querySelector('#admin-access-provision-confirm-step').textContent,/new-staff/);
  d.querySelector('#admin-access-provision-submit').click();await tick();
  assert.equal(rows().length,3,'explicit grant reuses the staff row');
  assert.equal(rows().find(r=>r.textContent.includes('new-staff')).querySelector('td[data-label="登录状态"]').textContent,'可登录');
  mode='failure';d.querySelector('#admin-access-refresh').click();await tick();
  assert.equal(rows().length,3,'Provider failure preserves prior staff');
  assert.doesNotMatch(d.querySelector('#admin-access-alert').textContent,/员工名单已刷新/);
  mode='no-receipt';d.querySelector('#admin-access-refresh').click();await tick();
  assert.doesNotMatch(d.querySelector('#admin-access-alert').textContent,/员工名单已刷新/,'generic OK is not directory success');
  mode='delayed-read';d.querySelector('#admin-access-refresh').click();await tick();
  assert.equal(d.querySelector('#admin-access-alert').textContent,'','success waits for the post-commit list read');
  releaseRead();await tick();
  mode='read-failure';d.querySelector('#admin-access-refresh').click();await tick();
  assert.equal(rows().length,3);assert.doesNotMatch(d.querySelector('#admin-access-alert').textContent,/员工名单已刷新/);
  mode='forbidden';d.querySelector('#admin-access-refresh').click();await tick();
  assert.equal(rows().length,0);assert.equal(d.querySelector('#admin-access-refresh').disabled,true);
  console.log('access complete directory refresh UI: PASS');
} finally {w.close();}
