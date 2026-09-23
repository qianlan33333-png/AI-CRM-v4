import { readPushTraceObservabilityDto, type PushTraceObservability } from '../../api/push_observability';
import { esc } from './util';

const button = 'height:30px;padding:0 11px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';

function shell(body: string): string {
  return `<div style="padding:20px;display:grid;gap:16px;align-content:start"><div><div style="font-size:12px;color:#8F959E">运营 / 可观察性</div><h1 style="margin:4px 0 0;font-size:20px">Push Center 与 Cloud audit 可观察性</h1></div>${body}</div>`;
}

function card(label: string, value: number): string {
  return `<div style="padding:10px;border:1px solid #DEE0E3;border-radius:8px;background:#fff"><div style="font-size:12px;color:#667085">${esc(label)}</div><strong style="font-size:19px">${value}</strong></div>`;
}

function observabilityHtml(page: PushTraceObservability): string {
  const sections = page.sections.map((section) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(section.label)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${esc(section.key)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${section.count}</td></tr>`).join('') || '<tr><td colspan="3" style="padding:16px;color:#8F959E">没有可展示的 section 聚合</td></tr>';
  const auditRows = page.audit?.items.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3">#${item.eventID} · ${esc(item.eventType)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.occurredAt)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.dispatched ? '内部已分发' : '内部未分发'}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">pending ${item.pending} / processing ${item.processing} / completed ${item.completed} / final_failed ${item.finalFailed} / outcome_unknown ${item.outcomeUnknown}</td></tr>`).join('') || '<tr><td colspan="4" style="padding:16px;color:#8F959E">当前精确范围内没有本地 audit 事实</td></tr>';
  const summary = page.degraded ? `<div style="padding:10px;border:1px solid #F5D6A7;border-radius:8px;background:#FFF9F0;color:#8F5A16;font-size:13px"><strong>Push Center degraded：</strong>${esc(page.message || '读模型暂不可用')}；不会把空聚合解释为零审计或成功。</div>` : `<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(110px,1fr));gap:8px">${[card('总数（本地）', page.counts!.total), card('pending', page.counts!.pending), card('running', page.counts!.running), card('sent（本地状态）', page.counts!.sent), card('failed', page.counts!.failed)].join('')}</div>`;
  const pushScope = page.traceID ? `Push Center 已按 trace_id <code>${esc(page.traceID)}</code> 筛选` : 'Push Center 保留全局本地聚合';
  const auditScope = page.audit ? `Cloud audit 已按${page.traceID ? ` trace_id <code>${esc(page.traceID)}</code>` : ''}${page.sessionID ? ` session_id <code>${esc(page.sessionID)}</code>` : ''}返回本地事实` : '未输入 trace_id/session_id，本次不请求 audit 列表';
  return shell(`<div style="display:flex;gap:8px;flex-wrap:wrap"><button id="observability-back" style="${button}">返回 Campaign</button><button id="observability-refresh" style="${button}">刷新可观察性</button></div><section style="display:grid;gap:10px;padding:14px;border:1px solid #DEE0E3;border-radius:8px"><div style="display:grid;grid-template-columns:minmax(240px,1fr) minmax(240px,1fr);gap:10px"><label style="display:grid;gap:5px;font-size:13px">trace_id<input id="observability-trace" maxlength="200" value="${esc(page.traceID || '')}" placeholder="可筛选 Push 与 audit" style="padding:8px;border:1px solid #D0D5DD;border-radius:6px"></label><label style="display:grid;gap:5px;font-size:13px">session_id<input id="observability-session" maxlength="200" value="${esc(page.sessionID || '')}" placeholder="筛选 Cloud audit" style="padding:8px;border:1px solid #D0D5DD;border-radius:6px"></label></div><div style="display:flex;gap:8px"><button id="observability-filter" style="${button}">按 trace/session 刷新</button></div><div style="padding:10px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px">${pushScope}；${auditScope}。两者都不证明 Provider 调用或送达。</div>${summary}<h2 style="margin:4px 0 0;font-size:15px">Push Center 聚合</h2><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">section</th><th style="padding:8px">key</th><th style="padding:8px">count</th></tr></thead><tbody>${sections}</tbody></table><h2 style="margin:4px 0 0;font-size:15px">Cloud audit（local facts only）</h2><table data-cloud-audit style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">事件</th><th style="padding:8px">发生时间</th><th style="padding:8px">内部分发</th><th style="padding:8px">本地 delivery 计数</th></tr></thead><tbody>${auditRows}</tbody></table><p style="margin:0;color:#8F5A16;font-size:12px">Cloud audit 只展示精确 trace/session 范围的本地事件与投递计数；completed/dispatched 不等于外部发送或送达。</p></section>`);
}

function route(traceID?: string, sessionID?: string): void {
  const params = new URLSearchParams({ view: 'observability' });
  if (traceID) params.set('trace_id', traceID);
  if (sessionID) params.set('session_id', sessionID);
  history.replaceState(null, '', `campaigns.html?${params.toString()}`);
}

function bindInputs(stage: HTMLElement): void {
  stage.querySelector<HTMLButtonElement>('#observability-filter')?.addEventListener('click', () => {
    const traceID = stage.querySelector<HTMLInputElement>('#observability-trace')?.value || '';
    const sessionID = stage.querySelector<HTMLInputElement>('#observability-session')?.value || '';
    void loadPushObservability(stage, traceID, sessionID).catch((error) => renderError(stage, error));
  });
  stage.querySelector<HTMLButtonElement>('#observability-refresh')?.addEventListener('click', () => void loadPushObservability(stage, pageScope(stage, 'observability-trace'), pageScope(stage, 'observability-session')).catch((error) => renderError(stage, error)));
  stage.querySelector<HTMLButtonElement>('#observability-back')?.addEventListener('click', () => { location.href = 'campaigns.html'; });
}

const pageScope = (stage: HTMLElement, id: string): string => stage.querySelector<HTMLInputElement>(`#${id}`)?.value || '';

function renderError(stage: HTMLElement, error: unknown): void {
  stage.innerHTML = shell(`<div style="padding:12px;border:1px solid #F2B8B5;border-radius:8px;background:#FFF1F0;color:#B42318">${esc(error instanceof Error ? error.message : '可观察性读取失败')}</div><button id="observability-back" style="${button}">返回 Campaign</button>`);
  stage.querySelector<HTMLButtonElement>('#observability-back')?.addEventListener('click', () => { location.href = 'campaigns.html'; });
}

async function loadPushObservability(stage: HTMLElement, inputTraceID?: string, inputSessionID?: string): Promise<void> {
  const page = await readPushTraceObservabilityDto(inputTraceID, inputSessionID);
  route(page.traceID, page.sessionID);
  stage.innerHTML = observabilityHtml(page);
  bindInputs(stage);
}

export async function mountPushObservability(stage: HTMLElement): Promise<void> {
  const params = new URLSearchParams(location.search);
  const sessionID = params.get('session_id');
  const traceID = params.get('trace_id') || undefined;
  await loadPushObservability(stage, traceID, sessionID || undefined);
}
