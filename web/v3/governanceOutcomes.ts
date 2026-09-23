import { openDetailDrawer } from './shared/ui/detailDrawer';

type Item = Record<string, unknown>;
type Request = (path: string, method?: string, body?: unknown, requestKey?: string) => Promise<unknown>;
type Window = { from: string; to: string };
type Page = { items: Item[]; has_more: boolean; next_before_id?: number };
const base = '/api/admin/ops-governance';
const pageSize = 25;
let preferredDays = 30;
const object = (v: unknown): v is Item => !!v && typeof v === 'object' && !Array.isArray(v);
const esc = (v: unknown): string => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const validDate = (v: unknown): v is string => typeof v === 'string' && Number.isFinite(Date.parse(v));
const positive = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) > 0;
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);
const date = (v: unknown): string => validDate(v) ? new Date(v).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '未知';
const number = (v: unknown): string => finite(v) ? String(v) : '未知';
const table = (headers: string[], rows: string[][]): string => rows.length ? `<div class="governance-table-wrap"><table class="admin-table"><thead><tr>${headers.map(h => `<th>${esc(h)}</th>`).join('')}</tr></thead><tbody>${rows.map(r => `<tr>${r.map(c => `<td>${c}</td>`).join('')}</tr>`).join('')}</tbody></table></div>` : '<p class="governance-empty">当前窗口无已记录周期，不能据此认定没有故障。</p>';
const classifications: Record<string, string> = { unclassified: '未分类', confirmed_defect: '确认缺陷', expected_rejection: '预期业务拒绝', legacy_backlog: '历史积压', observation_gap: '观测缺口', non_defect: '非缺陷' };
const judgments: Record<string, string> = { unclassified: '尚未判断', yes: '是', no: '否' };
const causes: Record<string, string> = { unknown: '未知', code: '代码', configuration: '配置', dependency: '外部依赖', data: '数据', infrastructure: '基础设施', expected_behavior: '预期行为', instrumentation: '观测机制' };
const remedies: Record<string, string> = { unknown: '未知', code_fix: '代码修复', configuration_fix: '配置修复', rollback: '回滚', hotfix: '紧急修复', data_reconciliation: '人工对账', dependency_recovery: '依赖恢复', no_action: '无需处理' };
const bases: Record<string, string> = { unknown: '没有起点证据', monitor_evidence: '监控证据', provider_receipt: 'Provider 回执', operator_confirmed: '负责人确认' };
const enums: Record<string, Record<string, string>> = { classification: classifications, escaped_defect: judgments, change_failure: judgments, root_cause: causes, remediation: remedies, fault_start_basis: bases };
function select(name: string, title: string, choices: Record<string, string>, value: unknown): string {
  return `<label>${title}<select class="admin-select" name="${name}">${Object.entries(choices).map(([k, v]) => `<option value="${k}"${k === value ? ' selected' : ''}>${esc(v)}</option>`).join('')}</select></label>`;
}
function duration(title: string, v: unknown): string[] { if (!object(v)) return [title, '未验证', '—', '—']; return [title, finite(v.mean_seconds) ? `${(v.mean_seconds / 60).toFixed(1)} 分钟` : '证据不足', number(v.sample_count), number(v.missing_count)]; }
function ratio(title: string, v: unknown): string[] { if (!object(v)) return [title, '未验证', '—', '—']; return [title, finite(v.ratio) ? `${(v.ratio * 100).toFixed(1)}%` : '证据不足', `${number(v.numerator)} / ${number(v.denominator)}`, number(v.unclassified)]; }
function windowFor(days: number): Window { const to = new Date(); return { from: new Date(to.getTime() - days * 86400000).toISOString(), to: to.toISOString() }; }
function query(window: Window, cursor?: number): string { const q = new URLSearchParams(window); if (cursor !== undefined) { q.set('limit', String(pageSize)); if (cursor) q.set('before_id', String(cursor)); } return q.toString(); }
function validEpisode(v: unknown): v is Item {
  return object(v) && positive(v.id) && positive(v.version) && typeof v.check_id === 'string' && typeof v.code === 'string'
    && validDate(v.detected_at) && validDate(v.last_observed_at) && Date.parse(v.last_observed_at) >= Date.parse(v.detected_at)
    && (v.recovered_at === null || (validDate(v.recovered_at) && Date.parse(v.recovered_at) >= Date.parse(v.last_observed_at)))
    && Object.entries(enums).every(([key, choices]) => typeof v[key] === 'string' && Object.prototype.hasOwnProperty.call(choices, String(v[key])))
    && (v.fault_started_at === null || validDate(v.fault_started_at))
    && (v.effort_minutes === null || (Number.isSafeInteger(v.effort_minutes) && Number(v.effort_minutes) >= 0 && Number(v.effort_minutes) <= 525600))
    && (v.caused_by_release_sequence === null || positive(v.caused_by_release_sequence));
}
function readPage(v: unknown, cursor: number): Page {
  if (!object(v) || !Array.isArray(v.items) || v.items.length > pageSize || !v.items.every(validEpisode) || typeof v.has_more !== 'boolean') throw new Error('周期分页数据不完整，不能确认已读完。');
  let previous = cursor || Number.MAX_SAFE_INTEGER;
  for (const item of v.items) { if (Number(item.id) >= previous) throw new Error('周期游标不一致，请重新读取。'); previous = Number(item.id); }
  if (v.has_more && (!v.items.length || !positive(v.next_before_id) || v.next_before_id !== previous)) throw new Error('后续周期游标缺失，不能确认已读完。');
  return { items: v.items, has_more: v.has_more, next_before_id: v.has_more ? Number(v.next_before_id) : undefined };
}
function readMetrics(v: unknown, window: Window): Item {
  if (!object(v) || !object(v.window) || !object(v.deployments) || !validDate(v.window.from) || !validDate(v.window.to)
    || Date.parse(v.window.from) !== Date.parse(window.from) || Date.parse(v.window.to) !== Date.parse(window.to)) throw new Error('治理统计窗口不一致，暂时无法确认。');
  const count = (n: unknown): n is number => Number.isSafeInteger(n) && Number(n) >= 0;
  for (const key of ['episode_count', 'confirmed_defects', 'unclassified_episodes', 'open_count', 'effort_minutes_total', 'effort_known_count', 'effort_missing_count']) if (!count(v[key])) throw new Error('治理样本数量不完整，暂时无法确认。');
  for (const key of ['mttd', 'detected_to_recovered', 'fault_to_recovered']) {
    const m = v[key]; if (!object(m) || !count(m.sample_count) || !count(m.missing_count) || Number(m.sample_count) + Number(m.missing_count) !== v.confirmed_defects || (m.sample_count === 0 ? m.mean_seconds !== null : !finite(m.mean_seconds) || m.mean_seconds < 0)) throw new Error('治理耗时样本不完整，不能把未知当作零。');
  }
  for (const key of ['escaped_defects', 'repeated_defects']) {
    const m = v[key]; if (!object(m) || !count(m.numerator) || !count(m.denominator) || !count(m.unclassified) || m.numerator > m.denominator || (m.denominator === 0 ? m.ratio !== null : !finite(m.ratio) || Math.abs(m.ratio - m.numerator / m.denominator) > 1e-9)) throw new Error('治理比例缺少可信分母，暂时无法确认。');
  }
  const d = v.deployments;
  if (!['verified_subset', 'unavailable'].includes(String(d.evidence_state)) || !count(d.verified_successful_deployments) || !count(d.confirmed_failed_deployments) || d.confirmed_failed_deployments > d.verified_successful_deployments || (d.evidence_state !== 'verified_subset' || d.verified_successful_deployments === 0 ? d.verified_success_cohort_failure_ratio !== null : !finite(d.verified_success_cohort_failure_ratio) || Math.abs(d.verified_success_cohort_failure_ratio - d.confirmed_failed_deployments / d.verified_successful_deployments) > 1e-9)) throw new Error('发布证据或分母不一致，暂时无法确认。');
  return v;
}
function message(error: unknown): string { return error instanceof Error ? error.message : '读取或操作失败，请重试。'; }

// Preserve the host's async-view contract. The host owns authentication, CSRF
// transport and tab generation; this leaf owns only its window/page/drawer state.
export async function governanceOutcomesView(request: Request): Promise<{ html: string; bind: (root: HTMLElement, current: () => boolean, refresh: () => Promise<void>) => void }> {
  let days = preferredDays; let window = windowFor(days); let cursors = [0]; let index = 0;
  const initial = await Promise.all([request(`${base}/outcomes?${query(window)}`), request(`${base}/episodes?${query(window, 0)}`)]);
  let metrics = readMetrics(initial[0], window); let page = readPage(initial[1], 0);
  const content = (): string => {
    const d = metrics.deployments as Item;
    return `<div class="governance-query" style="display:flex;flex-wrap:wrap;gap:12px;align-items:end;margin:16px 0"><label>统计窗口 <select class="admin-select" data-outcomes-days>${[7, 30, 90].map(n => `<option value="${n}"${days === n ? ' selected' : ''}>最近 ${n} 天</option>`).join('')}</select></label><button type="button" class="admin-button" data-outcomes-refresh>刷新当前窗口</button></div>
      <p data-outcomes-status role="status"></p><p>${esc(date(window.from))} 至 ${esc(date(window.to))}（北京时间）。按首次检测落入窗口的独立异常周期统计；健康后再异常形成新周期，部署前历史不补造。</p>
      <div class="governance-metrics">${[['异常周期', metrics.episode_count], ['确认缺陷', metrics.confirmed_defects], ['尚未分类', metrics.unclassified_episodes], ['仍未恢复', metrics.open_count]].map(([k, v]) => `<div class="admin-panel"><span>${k}</span><strong>${number(v)}</strong></div>`).join('')}</div>
      <section class="admin-panel"><h2>发现与恢复</h2>${table(['指标', '均值', '有证据样本', '缺少证据'], [duration('故障开始 → 发现（MTTD）', metrics.mttd), duration('发现 → 确认恢复', metrics.detected_to_recovered), duration('故障开始 → 确认恢复', metrics.fault_to_recovered)])}<p class="governance-note">起点未知不按 0 计算。只对确认缺陷计算耗时，未恢复周期单列；健康间隔不计入复发周期。这里分别显示两种恢复时长，不混称完整 MTTR。</p></section>
      <section class="admin-panel"><h2>回归与人工投入</h2>${table(['指标', '比例', '数量 / 已判断范围', '未判断'], [ratio('线上逃逸缺陷', metrics.escaped_defects), ratio('本机制已观察到的重复缺陷', metrics.repeated_defects)])}<p>已记录人工投入 ${number(metrics.effort_minutes_total)} 分钟；${number(metrics.effort_known_count)} 个周期有记录，${number(metrics.effort_missing_count)} 个缺少记录。包含排查后确认非缺陷的投入。</p></section>
      <section class="admin-panel"><h2>发布证据覆盖</h2><p>正式摘要${d.evidence_state === 'verified_subset' ? '已验证（仅证据子集）' : '不可用或未验证'}，证据截至 ${esc(date(d.evidence_at))}。有正式成功回执 ${number(d.verified_successful_deployments)} 次，确认引发故障 ${number(d.confirmed_failed_deployments)} 次；该子集比例 ${finite(d.verified_success_cohort_failure_ratio) ? (d.verified_success_cohort_failure_ratio * 100).toFixed(1) + '%' : '证据不足'}。</p><p>当前缺少完整失败发布分母，不能报告全量变更失败率。历史收据缺号 ${number(d.historical_sequence_gaps)}；未归因缺陷 ${number(d.unclassified_defect_attributions)}；失效发布归因 ${number(d.revoked_release_attributions)}。</p></section>
      <section class="admin-panel"><h2>异常周期与归因</h2>${table(['检查 / 原因', '分类 / 状态', '首次检测 / 恢复', '操作'], page.items.map(e => [`${esc(e.check_id)}<br>${esc(e.code)}`, `${esc(classifications[String(e.classification)])}<br>${e.recovered_at === null ? '尚未恢复' : '已确认恢复'}`, `${esc(date(e.detected_at))}<br>${e.recovered_at === null ? '未恢复' : esc(date(e.recovered_at))}`, `<button class="admin-button" type="button" data-outcomes-episode="${e.id}">查看与归因</button>`]))}
      <p data-outcomes-page>第 ${index + 1} 页 · 本页 ${page.items.length} 条 · ${page.has_more ? '还有后续周期' : '已到当前窗口末页'}</p><nav aria-label="异常周期分页" style="display:flex;gap:12px"><button class="admin-button" type="button" data-outcomes-page-action="previous"${index === 0 ? ' disabled' : ''}>上一页</button><button class="admin-button" type="button" data-outcomes-page-action="next"${page.has_more ? '' : ' disabled'}>下一页</button></nav></section>
      <p class="governance-note">只永久保存最小分类结论和处理投入，排障详情仍按 30 天清理。人工归因不会修复业务数据，也不会把异常巡查改成正常。</p>`;
  };
  return { html: `<section data-governance-outcomes>${content()}</section>`, bind(root, current, _refresh) {
    const found = root.querySelector<HTMLElement>('[data-governance-outcomes]'); if (!found) return; const container: HTMLElement = found;
    let readSerial = 0; let detailSerial = 0; let loading = false; let activeDrawer: HTMLDialogElement | null = null;
    const alive = (): boolean => current() && container.isConnected;
    const status = (text: string, error = false): void => { if (!alive()) return; const target = container.querySelector<HTMLElement>('[data-outcomes-status]'); if (target) { target.textContent = text; target.setAttribute('role', error ? 'alert' : 'status'); } };
    const render = (): void => { if (alive()) container.innerHTML = content(); };
    const closeDrawer = (): void => { ++detailSerial; activeDrawer?.close(); activeDrawer = null; };
    async function load(nextDays: number, nextIndex: number, newWindow: boolean, preserveDrawer = false): Promise<void> {
      const serial = ++readSerial; if (!preserveDrawer) closeDrawer(); loading = true;
      const nextWindow = newWindow ? windowFor(nextDays) : window;
      const cursor = newWindow ? 0 : (nextIndex > index ? page.next_before_id : cursors[nextIndex]);
      if (cursor === undefined) { loading = false; status('后续游标尚未确认，请刷新当前窗口。', true); return; }
      status('正在读取，请稍候…'); container.setAttribute('aria-busy', 'true');
      try {
        const values = await Promise.all([request(`${base}/outcomes?${query(nextWindow)}`), request(`${base}/episodes?${query(nextWindow, cursor)}`)]);
        if (!alive() || serial !== readSerial) return;
        const nextMetrics = readMetrics(values[0], nextWindow); const nextPage = readPage(values[1], cursor);
        metrics = nextMetrics; page = nextPage; window = nextWindow; days = nextDays; preferredDays = days;
        if (newWindow) { cursors = [0]; index = 0; } else { cursors[nextIndex] = cursor; cursors = cursors.slice(0, nextIndex + 1); index = nextIndex; }
        render();
      } catch (error) { if (alive() && serial === readSerial) { status(message(error), true); const select = container.querySelector<HTMLSelectElement>('[data-outcomes-days]'); if (select) select.value = String(days); } }
      finally { if (alive() && serial === readSerial) { loading = false; container.removeAttribute('aria-busy'); } }
    }
    async function showEpisode(id: number): Promise<void> {
      const serial = ++detailSerial; const pageSerial = readSerial; activeDrawer?.close(); activeDrawer = null; status('正在读取周期详情…');
      try {
        const e = await request(`${base}/episodes/${id}`);
        if (!alive() || serial !== detailSerial || pageSerial !== readSerial) return;
        if (!validEpisode(e) || e.id !== id) throw new Error('周期详情不完整，请重新读取。');
        status('');
        const body = document.createElement('div'); body.style.padding = '20px';
        const d = metrics.deployments as Item; const releases = Array.isArray(d.releases) ? d.releases.filter(object) : [];
        body.innerHTML = `<p>${esc(e.check_id)} · ${esc(e.code)}。检测版本 ${esc(e.detected_release)}，首次检测 ${esc(date(e.detected_at))}；${e.recovered_at === null ? '尚未确认恢复' : `确认恢复 ${esc(date(e.recovered_at))}`}。</p>
          <form class="governance-attribution" style="display:grid;gap:14px">${select('classification', '故障分类', classifications, e.classification)}${select('escaped_defect', '是否线上逃逸缺陷', judgments, e.escaped_defect)}${select('change_failure', '是否由发布引起', judgments, e.change_failure)}
          <label>有正式证据的发布序号 <input class="admin-input" name="caused_by_release_sequence" type="number" min="1" step="1" list="governance-release-choices" value="${esc(e.caused_by_release_sequence ?? '')}"><datalist id="governance-release-choices">${releases.filter(r => positive(r.sequence)).map(r => `<option value="${r.sequence}">${esc(String(r.release_sha).slice(0, 12))} · ${esc(date(r.succeeded_at))}</option>`).join('')}</datalist></label><small>建议项来自本窗口的已验证发布${d.releases_truncated === true ? '（仅前 100 条）' : ''}。其他序号必须已有正式成功收据，保存时由服务端校验；没有证据时保留“尚未判断”。</small>
          ${select('root_cause', '原因分类', causes, e.root_cause)}${select('remediation', '处理分类', remedies, e.remediation)}<label>故障开始时间（未知留空；必须带时区）<input class="admin-input" name="fault_started_at" value="${esc(e.fault_started_at || '')}" placeholder="2026-09-18T12:00:00+08:00"></label>${select('fault_start_basis', '起点依据（管理员确认）', bases, e.fault_start_basis)}<label>人工投入分钟（未知留空）<input class="admin-input" name="effort_minutes" type="number" min="0" max="525600" step="1" value="${esc(e.effort_minutes ?? '')}"></label><p>只填写固定分类与必要时间，不记录日志、客户信息或凭据。归因不能关闭周期，恢复需要新的成功巡查确认。</p>
          <div style="display:flex;gap:12px;flex-wrap:wrap"><button class="admin-button" type="submit" data-attribution-save>保存归因</button><button class="admin-button" type="button" data-attribution-reload>重新读取并核对记录</button></div><p role="status" data-attribution-status></p></form>`;
        const form = body.querySelector<HTMLFormElement>('form')!; const notice = body.querySelector<HTMLElement>('[data-attribution-status]')!;
        const save = form.querySelector<HTMLButtonElement>('[data-attribution-save]')!; const reload = form.querySelector<HTMLButtonElement>('[data-attribution-reload]')!;
        const field = (name: string): HTMLInputElement | HTMLSelectElement => form.querySelector(`[name="${name}"]`)!;
        let attempt: { key: string; payload: Item } | null = null; let saving = false; let saved = false;
        const drawer = openDetailDrawer('异常周期归因', body); activeDrawer = drawer;
        const drawerCurrent = (): boolean => alive() && drawer.isConnected && activeDrawer === drawer && serial === detailSerial;
        const observer = new MutationObserver(() => { if (!alive() && drawer.isConnected) drawer.close(); }); observer.observe(root, { childList: true, subtree: true });
        drawer.addEventListener('close', () => { observer.disconnect(); if (activeDrawer === drawer) activeDrawer = null; }, { once: true });
        function syncDependentFields(): void {
          if (attempt || saved) return;
          const defect = field('classification').value === 'confirmed_defect';
          for (const name of ['escaped_defect', 'change_failure', 'fault_started_at', 'fault_start_basis']) field(name).disabled = !defect;
          if (!defect) { field('escaped_defect').value = 'unclassified'; field('change_failure').value = 'unclassified'; field('fault_started_at').value = ''; field('fault_start_basis').value = 'unknown'; }
          field('caused_by_release_sequence').disabled = !defect || field('change_failure').value !== 'yes';
          if (field('caused_by_release_sequence').disabled) field('caused_by_release_sequence').value = '';
        }
        syncDependentFields(); form.addEventListener('change', syncDependentFields);
        reload.addEventListener('click', () => { if (drawerCurrent() && !saving) void showEpisode(id); });
        form.addEventListener('submit', async event => {
          event.preventDefault(); if (!drawerCurrent() || saving || saved) return;
          if (!attempt) {
            const data: Item = { expected_version: e.version };
            for (const [name, choices] of Object.entries(enums)) { data[name] = field(name).value; if (!Object.prototype.hasOwnProperty.call(choices, String(data[name]))) { notice.textContent = '请选择有效分类。'; return; } }
            const start = field('fault_started_at').value.trim();
            if (start && (!/(Z|[+-]\d{2}:\d{2})$/i.test(start) || !validDate(start) || Date.parse(start) > Date.parse(String(e.detected_at)) || Date.parse(start) > Date.now())) { notice.textContent = '故障开始时间须带时区，且不能晚于首次检测或当前时间。'; return; }
            data.fault_started_at = start ? new Date(start).toISOString() : null;
            if ((start === '') !== (data.fault_start_basis === 'unknown')) { notice.textContent = '故障起点与依据需同时填写，未知时同时留空和选择“没有起点证据”。'; return; }
            const minutes = field('effort_minutes').value.trim(); data.effort_minutes = minutes ? Number(minutes) : null;
            if (minutes && (!Number.isSafeInteger(data.effort_minutes) || Number(data.effort_minutes) < 0 || Number(data.effort_minutes) > 525600)) { notice.textContent = '人工投入须为 0 至 525600 的整数分钟。'; return; }
            const release = field('caused_by_release_sequence').value.trim(); data.caused_by_release_sequence = release ? Number(release) : null;
            if ((data.change_failure === 'yes') !== positive(data.caused_by_release_sequence)) { notice.textContent = '确认发布故障时须填写有正式成功证据的发布序号。'; return; }
            if (data.classification !== 'confirmed_defect' && (data.escaped_defect !== 'unclassified' || data.change_failure !== 'unclassified' || start)) { notice.textContent = '只有确认缺陷才可判断逃逸、发布故障和故障起点。'; return; }
            attempt = { key: crypto.randomUUID(), payload: Object.freeze(data) }; form.querySelectorAll<HTMLInputElement | HTMLSelectElement>('input,select').forEach(control => { control.disabled = true; });
          }
          saving = true; save.disabled = true; reload.disabled = true; notice.textContent = '正在保存，等待正式归因收据…';
          try {
            const receipt = await request(`${base}/episodes/${id}/attribution`, 'PUT', attempt.payload, attempt.key);
            if (!drawerCurrent()) return;
            if (!object(receipt) || receipt.episode_id !== id || receipt.version !== Number(attempt.payload.expected_version) + 1 || !positive(receipt.action_id) || typeof receipt.replay !== 'boolean') throw new Error('保存收据不完整，结果尚未确认。');
            saved = true; notice.textContent = receipt.replay ? '原归因已保存，已核对重放收据。' : '归因已保存。'; save.textContent = '已保存';
            void load(days, index, false, true);
          } catch (error) {
            if (drawerCurrent()) { const conflict = object(error) && error.status === 409; notice.textContent = `${message(error)} ${conflict ? '请重新读取并核对版本后再编辑。' : '本次字段已冻结；可重试原请求，或重新读取核对后再编辑。'}`; save.textContent = '重试原归因'; }
          } finally { if (drawerCurrent()) { saving = false; save.disabled = saved; reload.disabled = false; } }
        });
      } catch (error) { if (alive() && serial === detailSerial && pageSerial === readSerial) status(message(error), true); }
    }
    container.addEventListener('change', event => { const target = event.target; if (target instanceof HTMLSelectElement && target.matches('[data-outcomes-days]') && alive()) { const next = Number(target.value); if ([7, 30, 90].includes(next)) void load(next, 0, true); } });
    container.addEventListener('click', event => {
      const target = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('button') : null; if (!target || !container.contains(target) || !alive() || target.disabled) return;
      if (target.hasAttribute('data-outcomes-refresh')) { void load(days, 0, true); return; }
      if (loading) return;
      if (target.dataset.outcomesPageAction === 'previous' && index > 0) { void load(days, index - 1, false); return; }
      if (target.dataset.outcomesPageAction === 'next' && page.has_more) { void load(days, index + 1, false); return; }
      const id = Number(target.dataset.outcomesEpisode); if (positive(id) && page.items.some(e => e.id === id)) void showEpisode(id);
    });
  } };
}
