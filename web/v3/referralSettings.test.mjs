import assert from 'node:assert/strict';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./referralAdmin.ts', import.meta.url)));
const picker = fs.readFileSync(new URL('../../internal/webshell/static/admin_console/admin_search_select.js', import.meta.url),'utf8');
const json = body => new Response(JSON.stringify(body),{status:200,headers:{'Content-Type':'application/json'}});
const wait = async fn => { for(let i=0;i<150;i++){if(fn())return;await new Promise(r=>setTimeout(r,10));} throw new Error('UI did not settle'); };
for(const active of [false,true]) {
 const writes=[];
 const campaign={id:2,name:'购买活动',state:active?'active':'draft',effective_state:active?'active':'draft',starts_at:active?'2026-01-01T00:00:12Z':'2099-01-01T00:00:12Z',ends_at:'2099-12-01T00:00:12Z',version:2,team_mode:'individual',qualification_mode:'product_purchase',product_id:51,product_type:'standard_product',leaderboard_metric:'sales_amount',description:'介绍',reward_rules:'奖励'};
 const dom=new JSDOM('<section id="referral-admin-root"></section>',{url:'https://crm.example/admin/referral/settings?campaign=2',runScripts:'dangerously',beforeParse(w){ w.Headers=Headers; w.Response=Response; w.fetch=async (input,init={})=>{ const u=new URL(String(input),'https://crm.example'); if(init.method==='PUT'){writes.push(JSON.parse(init.body));return json({...campaign,...writes.at(-1),version:3});} if(u.pathname.endsWith('/product-options'))return json({items:[{id:51,code:'122331',name:'测试',product_type:'standard'},{id:52,code:'period',name:'周期测试',product_type:'service_period'},{id:53,code:'unknown',name:'未知类型',product_type:'unknown'}],total:3}); if(u.pathname.endsWith('/campaigns'))return json({items:[campaign]}); if(u.pathname.endsWith('/campaigns/2'))return json(campaign);return json({items:[]});}; }});
 dom.window.eval(picker); dom.window.eval(bundle);
 await wait(()=>dom.window.document.querySelector('[data-testid="referral-admin-settings-page"]'));
 assert.equal(dom.window.history.length,1,"direct settings load must not create duplicate history entries");
 const doc=dom.window.document;
 await wait(()=>doc.body.textContent.includes('122331'));
 assert.equal(doc.querySelector('dialog'),null);
 assert.equal(doc.querySelector('[aria-label="资格商品"]').value,'51:standard_product');
 assert.equal(doc.querySelector('select[name="战队模式"]').value,'individual');
 assert.equal(doc.querySelector('select[name="参加条件"]').disabled,active);
 if(!active){
  const productSelect=doc.querySelector('[aria-label="资格商品"]');
  assert.ok([...productSelect.options].some(option=>option.value==='52:service_period'),'service-period option must keep its Referral type');
  assert.equal([...productSelect.options].some(option=>option.textContent.includes('未知类型')),false,'unknown Product types must fail closed');
  const liveOption=[...productSelect.options].find(option=>option.textContent.includes('122331'));
  assert.ok(liveOption,'real Product option did not load');
  productSelect.value=liveOption.value;
  productSelect.dispatchEvent(new dom.window.Event('change',{bubbles:true}));
  assert.equal(productSelect.value,'51:standard_product','Product standard must be adapted to Referral standard_product');
 }
 if(active){
  const clear=[...doc.querySelectorAll('button')].find(b=>b.textContent==='清空选择');
  assert.ok(clear.matches(':disabled'),'active product clear must remain disabled after asynchronous directory loading');
  clear.click();
  assert.equal(doc.querySelector('[aria-label="资格商品"]').value,'51:standard_product');
 }
 doc.querySelector('textarea[name="活动介绍"]').value='新介绍';
 doc.querySelector('[data-testid="referral-admin-save-campaign"]').click();
 await wait(()=>writes.length===1);
 assert.equal(writes[0].qualification_mode,'product_purchase'); assert.equal(writes[0].product_id,51); assert.equal(writes[0].product_type,'standard_product'); assert.equal(writes[0].team_mode,'individual'); assert.equal(writes[0].description,'新介绍');
 if(active)assert.equal(writes[0].starts_at,campaign.starts_at,'locked timestamp seconds must remain exact');
 await wait(()=>dom.window.location.pathname==='/admin/referral');
 dom.window.close();
}
{
 const writes=[];
 const dom=new JSDOM('<section id="referral-admin-root"></section>',{url:'https://crm.example/admin/referral/settings',runScripts:'dangerously',beforeParse(w){ w.Headers=Headers; w.Response=Response; w.fetch=async (input,init={})=>{ const u=new URL(String(input),'https://crm.example'); if(init.method==='POST'){writes.push(JSON.parse(init.body));return json({id:3,state:'draft',version:1,...writes.at(-1)});} if(u.pathname.endsWith('/product-options'))return json({items:[{id:51,code:'122331',name:'测试',product_type:'standard'}],total:1}); if(u.pathname.endsWith('/campaigns'))return json({items:[]}); return json({items:[]});}; }});
 dom.window.eval(picker); dom.window.eval(bundle);
 const doc=dom.window.document;
 await wait(()=>doc.querySelector('[data-testid="referral-admin-settings-page"]'));
 const set=(selector,value)=>{const field=doc.querySelector(selector);field.value=value;field.dispatchEvent(new dom.window.Event('change',{bubbles:true}));};
 set('input[name="名称"]','创建活动回归');
 set('input[name="开始时间（北京时间）"]','2099-01-01T10:00');
 set('input[name="结束时间（北京时间）"]','2099-01-02T10:00');
 set('select[name="参加条件"]','product_purchase');
 await wait(()=>[...doc.querySelector('[aria-label="资格商品"]').options].some(option=>option.textContent.includes('122331')));
 set('[aria-label="资格商品"]','51:standard_product');
 doc.querySelector('[data-testid="referral-admin-save-campaign"]').click();
 await wait(()=>writes.length===1);
 assert.equal(writes[0].product_id,51);
 assert.equal(writes[0].product_type,'standard_product','create must submit Referral canonical product type');
 dom.window.close();
}
{
 const writes=[];
 const campaign={id:2,name:'海报活动',state:'active',effective_state:'active',starts_at:'2026-01-01T00:00:12Z',ends_at:'2099-12-01T00:00:12Z',version:2,team_mode:'individual',qualification_mode:'product_purchase',product_id:51,product_type:'standard_product',leaderboard_metric:'sales_amount',posters:[]};
 const dom=new JSDOM('<section id="referral-admin-root"></section>',{url:'https://crm.example/admin/referral/settings?campaign=2',runScripts:'dangerously',beforeParse(w){
  w.Headers=Headers;w.Response=Response;
  w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
  w.HTMLDialogElement.prototype.close=function(){this.removeAttribute('open');this.dispatchEvent(new w.Event('close'));};
  w.fetch=async(input,init={})=>{const u=new URL(String(input),'https://crm.example');
   if(u.pathname==='/api/admin/image-library')return json({items:[{id:11,name:'第一张',enabled:true,thumb_320_url:'/api/admin/image-library/11/variants/thumb_320'}],has_more:false});
   if(u.pathname.endsWith('/campaigns/2/posters')&&init.method==='PUT'){writes.push(JSON.parse(init.body));return json({posters:[]});}
   if(u.pathname.endsWith('/product-options'))return json({items:[{id:51,code:'122331',name:'测试',product_type:'standard'}],total:1});
   if(u.pathname.endsWith('/campaigns'))return json({items:[campaign]});
   if(u.pathname.endsWith('/campaigns/2'))return json(campaign);
   return json({items:[]});
  };
 }});
 dom.window.eval(picker);dom.window.eval(bundle);
 const doc=dom.window.document;
 await wait(()=>doc.querySelector('[data-testid="referral-admin-settings-page"]'));
 const add=[...doc.querySelectorAll('button')].find(x=>x.textContent==='选择图片素材');
 for(let i=0;i<3;i++){
  add.click();
  await wait(()=>doc.querySelector('.referral-admin-poster-materials button'));
  doc.querySelector('.referral-admin-poster-materials button').click();
  await wait(()=>!doc.querySelector('.referral-admin-poster-picker'));
  await wait(()=>doc.querySelectorAll('.referral-admin-poster-list > div').length===i+1);
 }
 assert.equal(doc.querySelectorAll('.referral-admin-poster-list > div').length,3,'admin may select three posters');
 add.click();
 assert.match(doc.querySelector('[data-testid="referral-admin-form-message"]').textContent,/最多配置 3 张/,'fourth poster is rejected');
 [...doc.querySelectorAll('button')].find(x=>x.textContent==='发布海报配置').click();
 await wait(()=>writes.length===1);
 assert.equal(writes[0].expected_version,2);
 assert.deepEqual(writes[0].posters,[{image_id:11,description:'第一张'},{image_id:11,description:'第一张'},{image_id:11,description:'第一张'}]);
 dom.window.close();
}
console.log('referral settings route, product selection, active copy save and readback passed');
