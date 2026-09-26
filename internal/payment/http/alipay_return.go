package paymenthttp

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// The provider return is presentation only. Its query parameters never grant a
// session, settle a payment, or select a customer. All facts and completion
// actions come from the existing session-authorized checkout read API.
func (handler *Handler) alipayReturn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	hash := sha256.Sum256([]byte(alipayReturnScript))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src https:; script-src 'sha256-"+base64.StdEncoding.EncodeToString(hash[:])+"'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	_, _ = io.WriteString(w, strings.Replace(alipayReturnHTML, "<!-- SCRIPT -->", "<script>"+alipayReturnScript+"</script>", 1))
}

const alipayReturnHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>付款结果</title><style>*{box-sizing:border-box}body{margin:0;background:#f5f6f8;color:#20242b;font:16px/1.7 -apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif}main{max-width:560px;margin:auto;padding:64px 20px}section{background:#fff;padding:32px 24px;border-radius:18px;text-align:center}h1{font-size:24px;margin:0 0 16px}p{color:#747d89;margin:0 0 16px}img{width:100%;max-width:220px;height:auto}button{min-height:48px;border:0;border-radius:12px;background:#3268ff;color:#fff;padding:12px 24px;font:inherit}[hidden]{display:none!important}</style></head><body><main><section><h1 id="title">正在确认付款结果</h1><p id="message" role="status" aria-live="polite">请稍候…</p><div id="guidance"></div><button id="retry" hidden>重新查询原订单</button></section></main><!-- SCRIPT --></body></html>`

const alipayReturnScript = `(function(){
const title=document.getElementById('title'),message=document.getElementById('message'),guidance=document.getElementById('guidance'),retry=document.getElementById('retry');
const refs=new URLSearchParams(location.search).getAll('out_trade_no');
let finished=false;
const fallback=()=>{finished=true;title.textContent='请返回原付款页';message.textContent='请返回微信中的原付款页查看支付结果。原订单已保留，请勿重新下单。';retry.hidden=true};
if(refs.length!==1||!/^[A-Za-z0-9_-]{1,64}$/.test(refs[0])){fallback();return}
let running=false,stopped=false,timer,wake;
function delay(){return new Promise(resolve=>{wake=()=>{clearTimeout(timer);wake=null;resolve()};timer=setTimeout(wake,1500)})}
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&!stopped&&!finished){if(wake)wake();else void query()}});window.addEventListener('pagehide',()=>{stopped=true;if(wake)wake()});window.addEventListener('pageshow',event=>{if(event.persisted){stopped=false;if(!finished)void query()}});
async function query(){if(running||stopped||finished)return;running=true;retry.hidden=true;try{for(let i=0;i<90&&!stopped;){if(document.hidden){await delay();continue}const response=await fetch('/api/v1/alipay/checkouts/'+encodeURIComponent(refs[0]),{credentials:'same-origin'});i++;if(response.status===401||response.status===404){fallback();return}if(!response.ok)throw new Error();const value=await response.json();if(value.status==='paid'){finished=true;title.textContent='已支付';message.textContent='支付成功';const action=value.completion_action;if(action?.state==='available'&&action.mode==='redirect'&&action.redirect_url){location.assign(action.redirect_url);return}if(action?.state==='available'&&action.mode==='qr'&&action.lead_qr?.url){guidance.replaceChildren();const img=document.createElement('img');img.src=action.lead_qr.url;img.alt=action.lead_qr.title||'后续指引二维码';const caption=document.createElement('p');caption.textContent=action.lead_qr.subtitle||'长按识别二维码，领取后续资料';guidance.append(img,caption)}else if(!action||action.state==='unavailable')message.textContent='支付成功，后续指引暂不可用，请返回原付款页查看。';return}if(value.status==='failed'||value.status==='cancelled'||value.checkout_abandoned){finished=true;title.textContent='付款尚未完成';message.textContent='请返回微信中的原付款页继续核对原订单。';return}message.textContent='正在等待服务端确认付款，请稍候。';await delay()}if(!stopped)throw new Error()}catch(_){title.textContent='付款结果暂未确认';message.textContent='原订单已保留。请稍后重试，或返回微信中的原付款页查看。';retry.hidden=false}finally{running=false}}
retry.addEventListener('click',query);void query();
})();`
