import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { JSDOM } from 'jsdom';
const template = await fs.readFile(new URL('../../templates/admin_access.html', import.meta.url), 'utf8');
const script = await fs.readFile(new URL('./admin_access.js', import.meta.url), 'utf8');
const users = [
 {admin_user_id:27, username:'wecom-staff-upper-fixture', display_name:'员工甲', wecom_userid:'StaffFixture', role:'admin', login_enabled:true, actions:{reset_password:true}},
 {admin_user_id:46, username:'wecom-staff-lower-fixture', display_name:'待验证员工', wecom_userid:'stafffixture', role:'admin', login_enabled:true, actions:{reset_password:true}}
];
const dom = new JSDOM(template,{url:'https://crm.test/admin/config/login-access',runScripts:'outside-only'});
const w=dom.window,d=w.document; w.Headers=Headers;
let responseStatus=200, writes=[], copied='';
Object.defineProperty(w.navigator,'clipboard',{value:{writeText:async value=>{copied=value;}}});
w.fetch=async (url,options={})=>{
 if (String(url).endsWith('/password')) {writes.push({url,options});return new Response(JSON.stringify(responseStatus===200?{ok:true}:{error:'invalid_request'}),{status:responseStatus});}
 return new Response(JSON.stringify({ok:true,users,actor:{admin_user_id:1},capabilities:{}}));
};
const tick=()=>new Promise(resolve=>setTimeout(resolve,10));
const open=index=>d.querySelectorAll('[data-access-action="manage"]')[index].click();
const submit=password=>{d.querySelector('#admin-access-new-password').value=password;d.querySelector('#admin-access-password-form').dispatchEvent(new w.Event('submit',{bubbles:true,cancelable:true}));};
try {
 w.eval(script);await tick();open(1);
 assert.equal(d.querySelector('#admin-access-login-username').value,users[1].username);
 assert.match(d.querySelector('#admin-access-similar-account').textContent,/独立账号（员工编号 46）/);
 d.querySelector('#admin-access-copy-username').click();await tick();assert.equal(copied,users[1].username);
 submit('short');await tick();assert.equal(writes.length,0);assert.match(d.querySelector('#admin-access-password-result').textContent,/至少 12/);
 submit('fixture-password-123');await tick();assert.equal(writes.length,1);assert.equal(writes[0].url,'/api/admin/access/users/46/password');assert.equal(writes[0].options.method,'PUT');assert.ok(writes[0].options.headers.get('Idempotency-Key'));
 assert.equal(d.querySelector('#admin-access-new-password').value,'');assert.equal(d.querySelector('#admin-access-password-result').hidden,false);assert.match(d.querySelector('#admin-access-password-result').textContent,/密码已重置.*旧密码及旧会话已失效/);
 responseStatus=400;submit('fixture-password-456');await tick();assert.doesNotMatch(d.querySelector('#admin-access-password-result').textContent,/密码已重置/);assert.equal(d.querySelector('#admin-access-new-password').value,'');
 responseStatus=500;submit('fixture-password-789');await tick();assert.match(d.querySelector('#admin-access-password-result').textContent,/未能确认/);
 d.querySelector('#admin-access-new-password').value='fixture-draft-secret';d.querySelector('#admin-access-drawer-close').click();assert.equal(d.querySelector('#admin-access-new-password').value,'');
 open(0);assert.equal(d.querySelector('#admin-access-login-username').value,users[0].username);assert.equal(d.querySelector('#admin-access-password-result').hidden,true);
 let release;
 w.fetch=()=>new Promise(resolve=>{release=resolve;});
 submit('fixture-delayed-password');await tick();
 d.querySelector('#admin-access-drawer-close').click();open(1);
 d.querySelector('#admin-access-new-password').value='different-employee-draft';
 release(new Response(JSON.stringify({ok:true})));await tick();
 assert.equal(d.querySelector('#admin-access-new-password').value,'different-employee-draft');assert.equal(d.querySelector('#admin-access-password-result').hidden,true);
 console.log('access password reset UI: PASS');
}finally{w.close();}
