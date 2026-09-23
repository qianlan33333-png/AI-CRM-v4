// V3 adapter for the existing shared group-invitation selection dialog.
// Invitation consumers select saved plans, never raw groups or provider scans.
(function(window,document){
 const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 function open(options={}){
  const focus=document.activeElement,mask=document.createElement('div');mask.className='aicrm-group-chat-picker-mask is-open';
  mask.innerHTML='<section class="aicrm-group-chat-picker" role="dialog" aria-modal="true" aria-label="选择邀请计划"><header class="aicrm-group-chat-picker__head"><h2>选择邀请计划</h2><button type="button" data-close>取消</button></header><div class="aicrm-group-chat-picker__search-wrap"><input type="search" placeholder="搜索邀请计划" aria-label="搜索邀请计划"></div><div class="aicrm-group-chat-picker__body"><p data-status>正在加载…</p><div class="aicrm-group-chat-picker__list"></div><a href="/admin/group-invitations" target="_blank" rel="noopener">管理邀请计划与群聊目录</a></div></section>';
  document.body.append(mask);const input=mask.querySelector('input'),list=mask.querySelector('.aicrm-group-chat-picker__list'),status=mask.querySelector('[data-status]');let plans=[],closed=false;
  function close(cancel){if(closed)return;closed=true;mask.remove();document.removeEventListener('keydown',key);focus?.focus();if(cancel)options.onCancel?.();}
  function key(e){if(e.key==='Escape'){e.preventDefault();close(true);}if(e.key==='Tab'){const nodes=[...mask.querySelectorAll('input,button,a')].filter(x=>!x.disabled);if(!nodes.length)return;if(e.shiftKey&&document.activeElement===nodes[0]){e.preventDefault();nodes.at(-1).focus();}else if(!e.shiftKey&&document.activeElement===nodes.at(-1)){e.preventDefault();nodes[0].focus();}}}
  function render(){const q=input.value.trim().toLowerCase(),rows=plans.filter(p=>p.token&&p.enabled&&p.name.toLowerCase().includes(q));status.textContent=rows.length?'':'暂无可选邀请计划，请先在群邀请板块创建';list.innerHTML=rows.map(p=>`<button type="button" class="aicrm-group-chat-picker__row" data-plan="${p.id}"><span class="aicrm-group-chat-picker__main"><strong class="aicrm-group-chat-picker__name">${esc(p.name)}</strong><span class="aicrm-group-chat-picker__meta">${p.mode==='single'?'固定单群':`顺序轮替 · ${p.threshold} 人切换`} · ${p.bindings.length} 个群</span></span></button>`).join('');}
  input.addEventListener('input',render);document.addEventListener('keydown',key);mask.addEventListener('click',e=>{if(e.target===mask||e.target.closest('[data-close]')){close(true);return;}const b=e.target.closest('[data-plan]');if(!b)return;const p=plans.find(x=>x.id===Number(b.dataset.plan));options.onConfirm?.({type:'group_invite',library_id:p.id,title:p.title,subtitle:p.name,enabled:p.enabled,selectable:true,metadata:{plan_id:p.id,join_url:p.join_url}});close(false);});
  fetch('/api/admin/group-invitations',{credentials:'same-origin',cache:'no-store'}).then(async r=>{if(!r.ok)throw new Error(r.status===403?'没有查看邀请计划的权限':'邀请计划加载失败');return r.json();}).then(v=>{if(closed)return;plans=v.items||[];render();}).catch(e=>{if(!closed)status.textContent=e.message;});input.focus();
 }
 window.AICRMGroupChatPicker={open};
})(window,document);
