/** Shared administrative error evidence. Raw errors, URLs, request bodies,
 * identities and stack traces never leave the browser through this adapter. */
export function installDiagnosticFeedback(): void {
  if (typeof window === 'undefined' || typeof window.fetch !== 'function' || !location.pathname.startsWith('/admin')) return;
  const host = window as Window & { __aicrmDiagnosticFeedback?: boolean };
  if (host.__aicrmDiagnosticFeedback) return;
  host.__aicrmDiagnosticFeedback = true;
  const original = window.fetch.bind(window);
  function show(id: string): void {
    if (!/^[a-f0-9]{32}$/.test(id)) return;
    let panel = document.querySelector<HTMLElement>('[data-diagnostic-feedback]');
    if (!panel) {
      panel = document.createElement('section'); panel.dataset.diagnosticFeedback = ''; panel.className = 'admin-alert admin-alert--error'; panel.setAttribute('role', 'alert');
      (document.querySelector('.admin-page, .main-content, .admin-main-wrap, main') || document.body).prepend(panel);
    }
    panel.replaceChildren();
    const message = document.createElement('span'); message.textContent = `操作异常，排查编号：${id} `;
    const copy = document.createElement('button'); copy.className = 'admin-button'; copy.type = 'button'; copy.textContent = '复制排查编号';
    copy.addEventListener('click', () => { void navigator.clipboard?.writeText(id).then(() => { copy.textContent = '已复制'; }).catch(() => { copy.textContent = '请选中编号复制'; }); });
    const link = document.createElement('a'); link.href = `/admin/ops?correlation=${id}`; link.textContent = '管理员排查'; link.className = 'admin-button';
    const close = document.createElement('button'); close.type = 'button'; close.className = 'admin-button'; close.textContent = '关闭'; close.addEventListener('click', () => panel?.remove());
    panel.append(message, copy, link, close);
  }
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const response = await original(input, init);
    try {
      const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      if (new URL(raw, location.origin).origin === location.origin && response.status >= 500) show(response.headers.get('X-AICRM-Diagnostic-ID') || '');
    } catch { /* Diagnostic rendering cannot change request completion. */ }
    return response;
  };
  let budget = 4;
  const report = (code: 'frontend_error' | 'frontend_unhandled_rejection'): void => {
    if (budget <= 0) return; budget -= 1;
    let token = '';
    try { const match = document.cookie.split(';').map((v) => v.trim()).find((v) => v.startsWith('aicrm_csrf=') || v.startsWith('aicrm_admin_csrf=')); token = match ? decodeURIComponent(match.slice(match.indexOf('=') + 1)) : ''; } catch { return; }
    if (!token) return;
    void original('/api/admin/ops-diagnostics/client-events', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token }, body: JSON.stringify({ code }) }).then((response) => { if (response.ok) show(response.headers.get('X-AICRM-Diagnostic-ID') || ''); }).catch(() => {});
  };
  window.addEventListener('error', () => report('frontend_error'));
  window.addEventListener('unhandledrejection', () => report('frontend_unhandled_rejection'));
}
