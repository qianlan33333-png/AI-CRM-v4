import { apiRequestOptions } from '../src/api/transport';
import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';

type Status = { id: number; state: string; added: number; existing: number; failed: number; last_success_at?: string };
const busyStates = new Set(['queued', 'running', 'retrying']);
function mountShopAction(actions: Parameters<typeof mountPageHeaderActions>[1]): void {
  mountPageHeaderActions('wechat-shop-refresh', actions);
  const button = document.querySelector<HTMLButtonElement>('[data-page-header-action="shop-refresh"]');
  if (button) { (button as HTMLButtonElement & {__dcBound?:boolean}).__dcBound=true; button.dataset.capabilityState='real'; }
}


// This timer only observes an operator's current refresh. It never initiates a
// product scan and is stopped when the page is left or the scan finishes.
export function installShopRefresh(): void {
  if (document.body.dataset.page !== 'products' || document.querySelector('[data-shop-refresh-status]')) return;
  const stage = document.getElementById('stage');
  if (!stage || !document.querySelector('.admin-topbar')) return;
  const status = document.createElement('p');
  status.dataset.shopRefreshStatus = '';
  status.setAttribute('role', 'status');
  status.hidden = true;
  status.style.cssText = 'margin:12px 22px;color:var(--text-secondary,#646A73);font-size:13px;line-height:1.6';
  stage.prepend(status);
  let active = false;
  let current = 0;
  let timer: number | undefined;
  const showError = (error: unknown) => { active = false; status.hidden = false; mountShopAction( [{ id: 'shop-refresh', label: '重试刷新微信小店商品', variant: 'secondary', onClick: async () => { render(await requestStatus(true)); }, onError: showError }]); status.textContent = error instanceof Error ? error.message : '微信小店刷新暂时不可用，请重试'; };
  const requestStatus = async (submit = false): Promise<Status> => {
    const response = await fetch(`/api/admin/products/wechat-shop/${submit ? 'refresh' : 'refresh-status'}`, apiRequestOptions({ method: submit ? 'POST' : 'GET', credentials: 'same-origin' }));
    if (!response.ok) throw new Error(response.status === 403 ? '需要商品编辑权限' : '微信小店刷新暂时不可用，请检查接入配置或重试');
    const result = await response.json() as Status;
    if (!Number.isSafeInteger(result.id) || result.id < 0 || !['idle', 'queued', 'running', 'retrying', 'succeeded'].includes(result.state) || [result.added, result.existing, result.failed].some(n => !Number.isSafeInteger(n) || n < 0)) throw new Error('微信小店刷新状态不完整，请重试');
    return result;
  };
  const render = (result: Status): void => {
    if (!status.isConnected) return;
    const wasActive = active;
    active = busyStates.has(result.state);
    status.hidden = !active;
    status.textContent = active ? result.state === 'retrying' ? '微信小店商品刷新遇到失败，系统将自动重试；也可点击重试刷新。' : '微信小店商品刷新中' : '';
    mountShopAction( [{ id: 'shop-refresh', label: result.state === 'retrying' ? '重试刷新微信小店商品' : active ? '微信小店刷新中…' : '刷新微信小店商品', variant: 'secondary', disabled: active && result.state !== 'retrying', onError: showError, onClick: async () => { render(await requestStatus(true)); } }]);
    if (timer !== undefined) window.clearTimeout(timer);
    if (active) timer = window.setTimeout(() => { void requestStatus().then(render).catch(showError); }, 2000);
    if (wasActive && !active && result.state === 'succeeded' && current === result.id) location.reload();
    current = result.id;
  };
  mountShopAction( [{ id: 'shop-refresh', label: '刷新微信小店商品', variant: 'secondary', onClick: async () => { render(await requestStatus(true)); }, onError: showError }]);
  void requestStatus().then(render).catch(showError);
  window.addEventListener('pagehide', () => { if (timer !== undefined) window.clearTimeout(timer); }, { once: true });
}

export function presentShopEditor(isShop: boolean): void {
  if (!isShop) return;
  const sale = document.getElementById('product-sale');
  if (!sale) return;
  const title=document.querySelector('.admin-page-title'); if(title && title.textContent !== '编辑微信小店商品') title.textContent='编辑微信小店商品';
  const basic=document.querySelector<HTMLAnchorElement>('a[href$="#product-sale"]');
  if(basic) { const label=basic.querySelector('span:last-child')||basic; if(label.textContent!=='基础信息') label.textContent='基础信息'; }
  const heading=sale.querySelector('h3'); if(heading && heading.textContent!=='基础信息') heading.textContent='基础信息';
  const push=document.querySelector<HTMLAnchorElement>('a[href$="#product-push"] span:first-child'); if(push && push.textContent!=='2') push.textContent='2';
  for(const label of Array.from(document.querySelectorAll('main strong')).map(x=>x.previousElementSibling)) {
    if(label && ['页面素材','企微标签'].includes(label.textContent?.trim()||'')) {
      const card=label.parentElement, grid=card?.parentElement;
      card?.remove(); if(grid) grid.style.gridTemplateColumns='repeat(3,minmax(0,1fr))';
    }
  }
  if (!sale.querySelector('[data-shop-source]')) {
    const note = document.createElement('p'); note.dataset.shopSource = ''; note.textContent = '微信小店商品 · 仅支持基础信息和外部推送';
    sale.prepend(note);
  }
  document.querySelector<HTMLElement>('[data-distribution-policy]')?.remove();
  for (const id of ['product-media', 'product-action', 'product-wecom']) {
    const panel = document.getElementById(id); if (panel) panel.hidden = true;
    document.querySelector<HTMLAnchorElement>(`a[href$="#${id}"]`)?.remove();
  }
  // Preserve required donor controls for basic-info serialization, while
  // hiding sale-only controls. The backend independently refuses local sale.
  for (const id of ['pfBuyButtonText', 'pfRequireMobile']) {
    const field = document.getElementById(id); if (field?.parentElement && field.parentElement.style.display!=='none') field.parentElement.style.display = 'none';
  }
  const alipay=document.querySelector<HTMLElement>('[data-product-alipay-enabled]')?.closest('label')?.parentElement;
  if(alipay && alipay.style.display!=='none') alipay.style.display='none';
}
