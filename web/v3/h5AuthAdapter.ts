// V3 owns this mobile presentation seam. The frozen H5 template retains its actual
// OAuth controller and error states; remove only the donor's device-demo
// chrome before the frozen runtime mounts it.
if (document.body.dataset.page === 'auth') {
  const screen = document.getElementById('screen');
  const template = document.getElementById('tpl') as HTMLTemplateElement | null;
  if (!screen || !template) throw new Error('微信授权页面缺少运行容器');

  const fragment = template.content;
  const blocked = fragment.querySelector<HTMLElement>('[data-h5-blocked]');
  fragment.firstElementChild?.remove();
  for (const node of Array.from(fragment.querySelectorAll('span,p'))) {
    const value = node.textContent?.trim() || '';
    if (value === '微信身份验证' || value.includes('验证 UnionID 后才可填写并提交问卷')) node.remove();
  }
  const title = fragment.querySelector('h1') as HTMLElement | null;
  if (title) title.style.margin = '0';
  const card = title?.parentElement;
  // The frozen controller uses this same error binding for OAuth conflict,
  // unavailable and network failures. Keep it, but make it conditional so the
  // two ordinary informational blockedReason strings leave no top-banner gap.
  if (blocked && card) {
    blocked.textContent = '{{ error }}';
    blocked.style.cssText = 'margin:14px 0 0;padding:10px 12px;border-radius:10px;background:#FFF1F0;color:#B42318;font-size:13px;line-height:20px;text-align:left';
    const conditional = document.createElement('template');
    conditional.setAttribute('data-sc-if', '{{ error }}');
    conditional.content.appendChild(blocked);
    const firstAction = Array.from(card.children).find((node) => node.tagName === 'TEMPLATE') || null;
    card.insertBefore(conditional, firstAction);
  }

}

if (['auth', 'all', 'one', 'result', 'error', 'done'].includes(document.body.dataset.page || '')) {
  const screen = document.getElementById('screen');
  const template = document.getElementById('tpl') as HTMLTemplateElement | null;
  if (!screen || !template) throw new Error('问卷页面缺少运行容器');
  if (!['auth', 'done'].includes(document.body.dataset.page || '')) template.content.firstElementChild?.remove();
  const phone = screen.closest('.phone');
  if (phone) phone.replaceWith(screen);
  document.querySelector('a[href="index.html"]')?.parentElement?.remove();
  document.querySelector('.h5-backdrop')?.setAttribute('style', 'min-height:100vh;background:#F5F6F7;display:block;padding:0;');
  screen.classList.remove('phone-screen');
  screen.setAttribute('style', 'display:flex;flex-direction:column;box-sizing:border-box;min-height:100vh;min-height:100dvh;width:100%;max-width:720px;margin:0 auto;background:#F5F6F7;overflow-wrap:anywhere;padding-bottom:env(safe-area-inset-bottom);');
}
