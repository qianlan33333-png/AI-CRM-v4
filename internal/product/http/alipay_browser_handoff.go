package http

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// The signed handoff stays in the fragment so our HTTP server and access logs
// never receive it. This page owns no order, payment, or Provider effect.
func serveAlipayBrowserHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	digest := sha256.Sum256([]byte(alipayBrowserHandoffScript))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_, _ = io.WriteString(w, strings.Replace(alipayBrowserHandoffHTML, "<!-- SCRIPT -->", "<script>"+alipayBrowserHandoffScript+"</script>", 1))
}

const alipayBrowserHandoffHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>继续支付宝付款</title>
<style>*{box-sizing:border-box}body{margin:0;background:#f5f6f8;color:#20242b;font:15px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}.wrap{max-width:560px;margin:0 auto;min-height:100dvh;padding:56px 20px calc(32px + env(safe-area-inset-bottom))}.panel{background:#fff;border-radius:18px;padding:24px}.eyebrow{color:#3268ff;font-size:13px;font-weight:600}h1{font-size:23px;line-height:1.4;margin:12px 0}p{margin:0 0 18px;color:#687282}.steps{margin:22px 0;padding:18px 20px;background:#eff4ff;border-radius:12px}.steps ol{margin:0;padding-left:22px}.steps li+li{margin-top:8px}.action{display:block;width:100%;min-height:48px;margin-top:14px;border:0;border-radius:12px;background:#3268ff;color:white;font:inherit;font-size:16px;font-weight:600;text-decoration:none;text-align:center;padding:13px 16px;cursor:pointer}.secondary{background:#fff;color:#3268ff;border:1px solid #d8e2ff}.hint{font-size:13px;color:#858b95;margin:20px 0 0}.fallback{width:100%;height:66px;margin-top:12px;padding:10px;border:1px solid #d8e2ff;border-radius:10px;font:12px/1.4 monospace;word-break:break-all}.error{color:#ba3039}[hidden]{display:none!important}:focus-visible{outline:3px solid #a6bfff;outline-offset:3px}</style></head>
<body><main class="wrap"><section class="panel"><span class="eyebrow">支付宝付款</span><h1 id="title">正在准备原订单链接…</h1><p id="message" role="status" aria-live="polite"></p><div id="wechatSteps" class="steps" hidden><ol><li>点击右上角「···」菜单</li><li>选择「在浏览器打开」</li><li>系统浏览器将自动打开原订单的支付宝付款页</li></ol></div><a id="openPay" class="action" href="#" rel="noreferrer noopener" hidden>继续支付宝付款</a><button id="copyPay" class="action secondary" type="button" hidden>复制原付款链接（备用）</button><textarea id="fallbackURL" class="fallback" aria-label="原订单支付宝付款链接" readonly hidden></textarea><p id="note" class="hint" hidden>付款后返回微信中的原订单页，点击「我已支付」查询结果。跳转本身不会确认付款。</p></section></main><!-- SCRIPT --></body></html>`

const alipayBrowserHandoffScript = `(function(){
const title=document.getElementById('title'),message=document.getElementById('message'),steps=document.getElementById('wechatSteps'),open=document.getElementById('openPay'),copy=document.getElementById('copyPay'),fallback=document.getElementById('fallbackURL'),note=document.getElementById('note');
const fail=text=>{title.textContent='付款链接暂不可用';message.textContent=text;message.classList.add('error')};
let target;
try{
  if(!location.hash||location.hash.length>12000)throw new Error();
  target=new URL(decodeURIComponent(location.hash.slice(1)));
  if(target.protocol!=='https:'||target.host!=='openapi.alipay.com'||target.pathname!=='/gateway.do'||target.username||target.password||target.hash)throw new Error();
  for(const key of ['method','app_id','sign','biz_content'])if(target.searchParams.getAll(key).length!==1||!target.searchParams.get(key))throw new Error();
  if(target.searchParams.get('method')!=='alipay.trade.wap.pay')throw new Error();
}catch(_){fail('请返回微信中的原订单页，重新打开付款引导或复制原付款链接。');return}
open.href=target.href;copy.hidden=false;note.hidden=false;
copy.addEventListener('click',async()=>{try{await navigator.clipboard.writeText(target.href);message.textContent='已复制原付款链接，可在系统浏览器地址栏粘贴打开。'}catch(_){fallback.value=target.href;fallback.hidden=false;fallback.focus();fallback.select();message.textContent='请长按或手动复制下方原付款链接。'}});
if(/MicroMessenger/i.test(navigator.userAgent)){
  title.textContent='请在系统浏览器继续付款';message.textContent='原订单已保留。请按下面步骤打开，无需复制长链接。';steps.hidden=false;
}else{
  title.textContent='正在打开支付宝付款…';message.textContent='如未自动打开，请点击下方按钮继续。';open.hidden=false;location.replace(target.href);
}
})();`
