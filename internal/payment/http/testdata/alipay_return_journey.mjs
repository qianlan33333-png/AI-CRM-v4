import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {JSDOM} from 'jsdom';
const html=readFileSync(0,'utf8');
async function visit(query, answer){
 const dom=new JSDOM(html,{url:'https://crm.example.test/pay/alipay/return'+query,runScripts:'outside-only',pretendToBeVisual:true});let reads=0;
 dom.window.fetch=async(url,options)=>{reads++;assert.equal(url,'/api/v1/alipay/checkouts/M-return');assert.equal(options.credentials,'same-origin');return {status:answer.status||200,ok:!answer.status||answer.status===200,json:async()=>answer.body}};
 dom.window.eval(dom.window.document.querySelector('script').textContent);for(let i=0;i<8;i++)await new Promise(resolve=>setImmediate(resolve));
 return {dom,reads};
}
for(const query of ['', '?out_trade_no=M-return&out_trade_no=other','?out_trade_no=../other']){const v=await visit(query,{});assert.equal(v.reads,0);assert.match(v.dom.window.document.body.textContent,/返回微信中的原付款页/);v.dom.window.close()}
const missing=await visit('?out_trade_no=M-return&trade_status=TRADE_SUCCESS',{status:401});assert.match(missing.dom.window.document.body.textContent,/返回微信中的原付款页/);missing.dom.window.close();
const paid=await visit('?out_trade_no=M-return',{body:{status:'paid',completion_action:{state:'none'}}});assert.equal(paid.dom.window.document.getElementById('title').textContent,'已支付');paid.dom.window.close();
const qr=await visit('?out_trade_no=M-return',{body:{status:'paid',completion_action:{state:'available',mode:'qr',lead_qr:{url:'https://fixture.test/qr.png',title:'后续资料',subtitle:'已有二维码指引'}}}});assert.equal(qr.dom.window.document.querySelector('img').getAttribute('src'),'https://fixture.test/qr.png');assert.match(qr.dom.window.document.body.textContent,/已有二维码指引/);qr.dom.window.close();
const pending=await visit('?out_trade_no=M-return&trade_status=TRADE_SUCCESS',{body:{status:'awaiting_payment'}});assert.notEqual(pending.dom.window.document.getElementById('title').textContent,'已支付','query parameter is never settlement evidence');pending.dom.window.dispatchEvent(new pending.dom.window.Event('pagehide'));pending.dom.window.close();
const resumed=new JSDOM(html,{url:'https://crm.example.test/pay/alipay/return?out_trade_no=M-return',runScripts:'outside-only',pretendToBeVisual:true});let hidden=true,reads=0,release;
Object.defineProperty(resumed.window.document,'hidden',{get:()=>hidden});
resumed.window.setTimeout=callback=>{release=callback;return 1};resumed.window.clearTimeout=()=>{};
resumed.window.fetch=async()=>{reads++;return {status:200,ok:true,json:async()=>({status:'paid',completion_action:{state:'none'}})}};
resumed.window.eval(resumed.window.document.querySelector('script').textContent);
for(let i=0;i<95;i++){release();await new Promise(resolve=>setImmediate(resolve))}
assert.equal(reads,0,'a hidden return page does not query or exhaust its retry budget');
hidden=false;resumed.window.document.dispatchEvent(new resumed.window.Event('visibilitychange'));
for(let i=0;i<8;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(reads,1);assert.equal(resumed.window.document.getElementById('title').textContent,'已支付');resumed.window.close();
console.log('Alipay return session, terminal status and guidance: PASS');
