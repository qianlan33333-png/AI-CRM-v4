import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./referralCenter.ts', import.meta.url)));
for (const qualification_mode of ['free_signup', 'product_purchase']) {
  const dom = new JSDOM('<main id="referral-root"></main>', {url:'https://crm.example/referral?campaign=5',runScripts:'outside-only',beforeParse(window) {
    window.Response=Response;window.Headers=Headers;window.URL=URL;
    window.fetch=async input => new Response(JSON.stringify(String(input).includes('/me') ? {error:'unauthorized'} : {id:5,name:'活动',qualification_mode}), {status:String(input).includes('/me') ? 401 : 200,headers:{'Content-Type':'application/json'}});
  }});
  try {
    dom.window.eval(bundle);
    for(let i=0;i<100&&!dom.window.document.querySelector('.referral-primary');i++) await new Promise(resolve=>setTimeout(resolve,10));
    const text=dom.window.document.querySelector('#referral-root').textContent;
    assert.match(text,/查看活动规则并参加活动/);
    assert.doesNotMatch(text,/活动不要求购买/,'anonymous paid campaign must not claim free qualification before its configuration is known');
    assert.match(text,/无需额外注册分销员或完成收款准备/);
    assert.match(dom.window.document.querySelector('a').href,/return_url=%2Freferral%3Fcampaign%3D5/);
  } finally { dom.window.close(); }
}
