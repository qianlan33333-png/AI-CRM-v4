import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';
import { openDetailDrawer } from './shared/ui/detailDrawer';
import { governanceOutcomesView } from './governanceOutcomes';
import { renderTableReadState } from './shared/ui/tableReadState';

type Check = { id: string; owner: string; title: string; scope?: string; status: string; code: string; observed_at: string; metrics: Record<string, number> };
type Issue = { id: number; check_id: string; code: string; status: string; severity: string; version: number; first_seen: string; last_seen: string; occurrences: number };
type Overview = { fresh: boolean; observed_at: string; latest?: { id: number; release_sha: string; completed_at?: string }; checks: Check[]; issues: Issue[] };
type Tab = 'outcomes' | 'overview' | 'checks' | 'issues' | 'reports' | 'diagnostics' | 'profiles' | 'retention' | 'resources';
const root = document.querySelector<HTMLElement>('#governance-admin-root');
const labels: Record<Tab, string> = { overview: '治理总览', outcomes: '治理成效', checks: '检查详情', issues: '问题跟踪', reports: '巡查报告', diagnostics: '错误聚合', profiles: '性能采样', retention: '数据生命周期', resources: '资源覆盖' };
const statusLabels: Record<string, string> = { ok: '正常', warning: '需关注', critical: '严重', unknown: '未知', uncovered: '未覆盖', stale: '过期', open: '待处理', acknowledged: '已确认', resolved: '已恢复', queued: '待发送', executed: '飞书已接受', outcome_unknown: '发送结果未知', final_failed: '发送失败', disabled: '发送未启用' };
let tab: Tab = 'overview';
let serial = 0;
let diagnosticQuery = new URLSearchParams(location.search).get('correlation') || '';
if (diagnosticQuery) tab = 'diagnostics';
let overview: Overview | null = null;
let acceptedScan: {job: number} | null = null;
let profilesEnabled = false;
let profileBusy = false;
let profileRequestKey: string | null = null;
const resourceKinds = { table: '数据库表', resource: '登记资源', filesystem_prefix: '主机目录' };
const resourcePolicies = { permanent: '永久保留', never_delete_by_retention: '禁止按保留期删除', operational_detail_30d: '过程明细 · 30 天', report_payload_30d: '报告载荷 · 30 天', temporary_upload_parts_30d: '上传临时分片 · 30 天', river_terminal_jobs_30d: 'River 终态任务 · 30 天', '30_days': '主机过程文件 · 30 天', verified_release_allowlist: '发布包与回滚保护', security_ttl: '安全有效期', protected_mixed_payload: '业务与过程混合 · 保护', protected_unclassified: '尚未分类 · 保护', owner_projection: 'Owner 投影', runtime_coordination: '运行协调状态' };
const resourceStates = { protected: '受保护', owner_managed: 'Owner 管理', scheduled: '已排程', disabled: '自动执行未启用', owner_not_bound: '执行入口未绑定', native_unobserved: '原生执行未观察', host_unobserved: '主机执行未观察', gap: '治理缺口' };
const resourceGaps: Record<string, string> = { classification_unverified: '分类尚未确认，保持保护', mixed_payload_owner_split_required: '需要 Owner 拆分业务事实与过程载荷', security_ttl_physical_cleanup_missing: '缺少物理清理；授权是否有效由安全 TTL 独立判断', owner_cleanup_contract_missing: '缺少 Owner 清理契约', owner_port_not_bound: 'Owner 清理入口未绑定', host_evidence_reader_not_bound: '主机执行证据读取未绑定' };
type RetentionResource = { kind: keyof typeof resourceKinds; name: string; owner: string; policy: keyof typeof resourcePolicies; reason: string; source: string; cleanup_entrypoint: string; policy_id: string; coverage_status: keyof typeof resourceStates; gap_code: string; authorization_expiry: 'owner_security_ttl' | 'not_assessed' };
const coverageCounts = ['registered_tables', 'registered_resources', 'registered_filesystem_prefixes', 'gap_resources', 'protected_resources', 'owner_managed_resources', 'scheduled_resources', 'disabled_resources', 'unbound_resources', 'unobserved_resources', 'security_ttl_resources', 'mixed_payload_resources', 'unclassified_resources', 'coordination_resources'] as const;
type RetentionCoverage = { registry_version: number; registry_sha256: string; inventory_scope: 'committed_registry'; automatic_cleanup_enabled: boolean; allowlist_policy_count: number; summary: Record<typeof coverageCounts[number], number>; items: RetentionResource[] };
let resourceFilters = { query: '', owner: '', policy: '', status: '', page: 1 };
const resourcePageSize = 25;
const esc = (v: unknown): string => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const date = (v: unknown): string => typeof v === 'string' && !Number.isNaN(Date.parse(v)) ? new Date(v).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—';
const badge = (s: string): string => `<span class="governance-status" data-status="${esc(s)}">${esc(statusLabels[s] || s)}</span>`;
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
function isOverview(v: unknown): v is Overview {
  return record(v) && typeof v.fresh === 'boolean' && Array.isArray(v.checks) && Array.isArray(v.issues)
    && v.checks.every((c: unknown) => record(c) && typeof c.id === 'string' && typeof c.status === 'string' && typeof c.title === 'string' && record(c.metrics))
    && v.issues.every((i: unknown) => record(i) && Number.isSafeInteger(i.id) && Number.isSafeInteger(i.version));
}
function isRetentionCoverage(v: unknown): v is RetentionCoverage {
  if (!record(v) || v.registry_version !== 1 || typeof v.registry_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(v.registry_sha256)
    || v.inventory_scope !== 'committed_registry' || typeof v.automatic_cleanup_enabled !== 'boolean' || !Number.isSafeInteger(v.allowlist_policy_count) || Number(v.allowlist_policy_count) < 0
    || !record(v.summary) || !Array.isArray(v.items) || !v.items.length) return false;
  const summary = v.summary;
  if (!coverageCounts.every((key) => Number.isSafeInteger(summary[key]) && Number(summary[key]) >= 0)) return false;
  const keys = new Set<string>();
  if (!v.items.every((item: unknown) => {
    if (!record(item) || !['kind', 'name', 'owner', 'policy', 'reason', 'source', 'cleanup_entrypoint', 'policy_id', 'coverage_status', 'gap_code', 'authorization_expiry'].every((key) => typeof item[key] === 'string')
      || !Object.prototype.hasOwnProperty.call(resourceKinds, String(item.kind)) || !Object.prototype.hasOwnProperty.call(resourcePolicies, String(item.policy)) || !Object.prototype.hasOwnProperty.call(resourceStates, String(item.coverage_status))
      || !item.name || !item.owner || !item.reason || !item.source || !['owner_security_ttl', 'not_assessed'].includes(String(item.authorization_expiry))) return false;
    const key = `${item.kind}:${item.name}`; if (keys.has(key)) return false; keys.add(key); return true;
  })) return false;
  return Number(v.summary.registered_tables) + Number(v.summary.registered_resources) + Number(v.summary.registered_filesystem_prefixes) === v.items.length;
}
function csrf(): string {
  for (const part of document.cookie.split(';')) { const [name, ...rest] = part.trim().split('='); if (name === 'aicrm_csrf' || name === 'aicrm_admin_csrf') { try { return decodeURIComponent(rest.join('=')); } catch { return ''; } } }
  return '';
}
class AccessError extends Error {}
async function request(path: string, method = 'GET', body?: unknown, requestKey?: string): Promise<unknown> {
  const headers = new Headers({ Accept: 'application/json' });
  if (method !== 'GET') { headers.set('Content-Type', 'application/json'); headers.set('X-CSRF-Token', csrf()); headers.set('Idempotency-Key', requestKey || crypto.randomUUID()); }
  const response = await fetch(path, { method, headers, credentials: 'same-origin', cache: 'no-store', body: body === undefined ? undefined : JSON.stringify(body) });
  if (response.status === 401 || response.status === 403) throw new AccessError('需要超级管理员权限，请确认登录身份。');
  if (!response.ok) {
    const diagnosticID = response.headers.get('X-AICRM-Diagnostic-ID');
    const suffix = diagnosticID && /^[a-f0-9]{32}$/.test(diagnosticID) ? ` 排查编号：${diagnosticID}` : '';
    throw new Error((response.status === 409 ? '记录已更新或另一操作正在执行，请刷新后查看。' : response.status === 429 ? '已达到本小时次数或存储限额，请稍后再试。' : response.status === 410 ? '采样已超过 30 天，详情和文件不再提供。' : '读取或操作失败，请稍后重试。') + suffix);
  }
  return response.json();
}
function table(headers: string[], rows: string[][]): string {
  return rows.length ? `<div class="governance-table-wrap"><table class="admin-table"><thead><tr>${headers.map((h) => `<th>${esc(h)}</th>`).join('')}</tr></thead><tbody>${rows.map((row) => `<tr>${row.map((cell) => `<td>${cell}</td>`).join('')}</tr>`).join('')}</tbody></table></div>` : '<p class="governance-empty">当前没有记录。未采集不代表系统正常。</p>';
}
function storageEvidence(value: unknown): string {
  if (!record(value)) return '<p>暂无执行证据</p>';
  const n = (v: unknown): string => typeof v === 'number' ? esc(v) : '未知';
  const samples = Array.isArray(value.space_observations) ? value.space_observations.filter(record) : [];
  return `<p>${esc(value.status || value.state || 'unknown')} · ${esc(value.reason || value.code || '')} · ${esc(date(value.observed_at || value.finished_at))}</p>${'deleted_count' in value ? table(['删除发布包', '逻辑字节', '可用回滚版本'], [[n(value.deleted_count), n(value.deleted_bytes), n(value.rollback_count)]]) : ''}${samples.length ? table(['存储范围', '采样状态', '清理前可用字节', '清理后可用字节', '可用空间净变化'], samples.map((v) => [esc(v.resource), esc(v.state), n(v.available_bytes_before), n(v.available_bytes_after), n(v.available_bytes_net_change)])) : ''}`;
}
function metrics(c: Check): string { return Object.entries(c.metrics || {}).map(([name, value]) => `${esc(name)}：${esc(value)}`).join('<br>') || '—'; }
function layout(content: string): void {
  if (!root) return;
  renderHeader();
  root.innerHTML = `<nav class="governance-tabs" aria-label="运行治理">${Object.entries(labels).map(([key, title]) => `<button type="button" class="admin-button" data-tab="${key}" aria-current="${tab === key ? 'page' : 'false'}">${title}</button>`).join('')}</nav><div class="governance-content" aria-live="polite">${acceptedScan ? `<p class="governance-note" role="status">巡查已受理，任务 ${acceptedScan.job}，等待执行结果。可刷新查看最新结果。</p>` : ""}${content}</div>`;
  root.querySelectorAll<HTMLButtonElement>('[data-tab]').forEach((button) => button.addEventListener('click', () => { tab = button.dataset.tab as Tab; void refresh(); }));
}
function overviewContent(data: Overview): string {
  const count = (statuses: string[]) => data.checks.filter((c) => statuses.includes(c.status)).length;
  return `<p class="governance-freshness">${data.fresh ? '最新巡查' : '巡查结果已过期或尚未采集'} · ${esc(date(data.observed_at))} · 运行版本 ${esc(data.latest?.release_sha?.slice(0, 12) || '未知')}</p>
  <div class="governance-metrics">${[['检查目录', data.checks.length], ['异常', count(['warning', 'critical'])], ['未知或过期', count(['unknown', 'stale'])], ['未覆盖', count(['uncovered'])]].map(([title, n]) => `<div class="admin-panel"><span>${title}</span><strong>${n}</strong></div>`).join('')}</div>
  <section class="admin-panel"><h2>需要处理</h2>${table(['检查项', '状态', '原因', '最近观察'], data.checks.filter((c) => c.status !== 'ok').map((c) => [esc(c.title), badge(c.status), esc(c.code), esc(date(c.observed_at))]))}</section>
  <p class="governance-note">业务故障只报告和跟踪。纯过程明细保留 30 天；业务数据及防重依据永久保留。飞书接受与群内可见分别验证。</p>`;
}
function resourceStatus(item: RetentionResource): string {
  const tone = ['native_unobserved', 'host_unobserved'].includes(item.coverage_status) ? 'unknown' : ['gap', 'owner_not_bound'].includes(item.coverage_status) ? 'warning' : item.coverage_status;
  return `<span class="governance-status" data-status="${tone}" data-coverage-status="${item.coverage_status}">${resourceStates[item.coverage_status]}</span>`;
}
function resourceGap(item: RetentionResource): string {
  if (item.gap_code) return resourceGaps[item.gap_code] || `未识别的缺口：${item.gap_code}`;
  return ({ native_unobserved: '需要原生清理器的执行证据', host_unobserved: '需要另查宿主执行记录与新鲜度', disabled: '策略已登记，当前不会自动执行', scheduled: '已绑定并启用，不代表已经清理成功', owner_managed: '由 Owner 管理，未纳入统一定时清理', protected: '按保护规则保留' } as Record<string, string>)[item.coverage_status] || '尚无可验证依据';
}
function renderResources(data: RetentionCoverage): void {
  const s = data.summary;
  const needle = resourceFilters.query.toLocaleLowerCase();
  const matched = data.items.filter((item) => (!needle || `${item.name}\n${item.owner}\n${item.reason}\n${item.gap_code}`.toLocaleLowerCase().includes(needle))
    && (!resourceFilters.owner || item.owner === resourceFilters.owner) && (!resourceFilters.policy || item.policy === resourceFilters.policy)
    && (!resourceFilters.status || (resourceFilters.status === 'attention' ? ['gap', 'owner_not_bound', 'native_unobserved', 'host_unobserved', 'disabled'].includes(item.coverage_status) : item.coverage_status === resourceFilters.status)));
  const pages = Math.max(1, Math.ceil(matched.length / resourcePageSize));
  resourceFilters.page = Math.min(resourceFilters.page, pages);
  const start = (resourceFilters.page - 1) * resourcePageSize;
  const visible = matched.slice(start, start + resourcePageSize);
  const options = (values: Record<string, string>, selected: string): string => Object.entries(values).map(([value, label]) => `<option value="${esc(value)}"${value === selected ? ' selected' : ''}>${esc(label)}</option>`).join('');
  const owners = Object.fromEntries([...new Set(data.items.map((item) => item.owner))].sort().map((owner) => [owner, owner]));
  const rows = visible.map((item, n) => [`<span style="overflow-wrap:anywhere">${esc(item.name)}</span><br><small>${resourceKinds[item.kind]}</small>`, esc(item.owner), esc(resourcePolicies[item.policy]), resourceStatus(item), `${esc(resourceGap(item))}${item.gap_code ? `<br><small>${esc(item.gap_code)}</small>` : ''}`, `<button type="button" class="admin-button" data-resource-detail="${n}">查看依据</button>`]);
  layout(`<h2>全资源登记与覆盖</h2><p class="governance-note">范围为已提交的资源登记簿，包含 ${s.registered_tables} 张表、${s.registered_resources} 项资源和 ${s.registered_filesystem_prefixes} 个主机目录；不代表已扫描生产全部文件或已执行删除。</p>
    <div class="governance-metrics">${[['已登记资源', data.items.length], ['治理缺口（含未绑定）', s.gap_resources], ['执行未观察', s.unobserved_resources], ['已排程（非执行结果）', s.scheduled_resources]].map(([title, count]) => `<div class="admin-panel"><span>${title}</span><strong>${count}</strong></div>`).join('')}</div>
    <section class="admin-panel"><h2>白名单执行与全资源覆盖分别判断</h2><p>数据库清理白名单已绑定 ${data.allowlist_policy_count} 项策略；自动执行${data.automatic_cleanup_enabled ? '已启用' : '未启用'}。白名单健康不代表全部资源已覆盖，排程也不代表清理成功。</p><p>受保护 ${s.protected_resources} 项 · Owner 管理 ${s.owner_managed_resources} 项 · 执行未启用 ${s.disabled_resources} 项 · 未绑定 ${s.unbound_resources} 项。</p><button type="button" class="admin-button" data-tab="retention">查看清理执行记录</button> <button type="button" class="admin-button" data-tab="checks">查看执行健康检查</button></section>
    <p class="governance-note">安全有效期 ${s.security_ttl_resources} 项 · 混合载荷 ${s.mixed_payload_resources} 项 · 未分类 ${s.unclassified_resources} 项 · 运行协调状态 ${s.coordination_resources} 项。安全有效期由 Owner 判断，物理清理缺口不能用来推断会话或令牌仍然有效。</p>
    <form class="governance-query" data-resource-filters style="display:flex;flex-wrap:wrap;gap:12px;margin:16px 0;align-items:end">
      <label>资源搜索 <input name="resource_query" value="${esc(resourceFilters.query)}" maxlength="200" placeholder="名称、Owner 或缺口原因" autocomplete="off"></label>
      <label>Owner <select name="resource_owner">${options({ '': '全部 Owner', ...owners }, resourceFilters.owner)}</select></label>
      <label>登记分类 <select name="resource_policy" style="max-width:240px">${options({ '': '全部分类', ...resourcePolicies }, resourceFilters.policy)}</select></label>
      <label>覆盖状态 <select name="resource_status">${options({ '': '全部状态', attention: '缺口、未观察或未启用', ...resourceStates }, resourceFilters.status)}</select></label>
      <button type="submit" class="admin-button">筛选</button><button type="button" class="admin-button" data-resource-reset>重置筛选</button></form>
    <p data-resource-count role="status">匹配 ${matched.length} / ${data.items.length} 项；当前第 ${resourceFilters.page} / ${pages} 页，每页 ${resourcePageSize} 项。</p>
    <section data-resource-table>${table(['资源 / 类型', 'Owner', '登记分类 / 策略', '覆盖状态', '缺口或边界', '依据'], rows.length ? rows : [['', '', '', '', '', '']])}</section>
    <nav aria-label="资源分页" style="display:flex;flex-wrap:wrap;gap:12px;margin:16px 0"><button type="button" class="admin-button" data-resource-page="previous"${resourceFilters.page === 1 ? ' disabled' : ''}>上一页</button><button type="button" class="admin-button" data-resource-page="next"${resourceFilters.page === pages ? ' disabled' : ''}>下一页</button></nav>
    <p class="governance-note">只读登记簿版本 ${data.registry_version} · 摘要 ${esc(data.registry_sha256.slice(0, 16))}。业务事实与防重依据永久保留；未分类和混合载荷保持保护。此页不提供删除操作。</p>`);
  if (!visible.length) {
    const body = root?.querySelector<HTMLTableSectionElement>('[data-resource-table] tbody');
    if (body) renderTableReadState(body, { state: 'no-match', colSpan: 6, message: '没有匹配资源，请调整筛选；这不代表系统没有治理缺口。' });
  }
  const form = root?.querySelector<HTMLFormElement>('[data-resource-filters]');
  let composing = false;
  form?.addEventListener('compositionstart', () => { composing = true; });
  form?.addEventListener('compositionend', () => { composing = false; });
  form?.addEventListener('keydown', (event) => { if (event.key === 'Enter' && (event.isComposing || composing)) event.preventDefault(); });
  form?.addEventListener('submit', (event) => {
    event.preventDefault(); if (composing || tab !== 'resources') return;
    const value = (name: string): string => form.querySelector<HTMLInputElement | HTMLSelectElement>(`[name="${name}"]`)?.value.trim() || '';
    resourceFilters = { query: value('resource_query'), owner: value('resource_owner'), policy: value('resource_policy'), status: value('resource_status'), page: 1 };
    renderResources(data);
  });
  root?.querySelector('[data-resource-reset]')?.addEventListener('click', () => { resourceFilters = { query: '', owner: '', policy: '', status: '', page: 1 }; renderResources(data); });
  root?.querySelectorAll<HTMLButtonElement>('[data-resource-page]').forEach((button) => button.addEventListener('click', () => { resourceFilters.page += button.dataset.resourcePage === 'next' ? 1 : -1; renderResources(data); root?.querySelector<HTMLButtonElement>(`[data-resource-page="${button.dataset.resourcePage}"]`)?.focus(); }));
  root?.querySelectorAll<HTMLButtonElement>('[data-resource-detail]').forEach((button) => button.addEventListener('click', () => {
    const item = visible[Number(button.dataset.resourceDetail)]; if (!item) return;
    const body = document.createElement('div'); body.className = 'governance-report';
    body.textContent = [`资源：${item.name}`, `类型：${resourceKinds[item.kind]}；Owner：${item.owner}`, `登记分类：${resourcePolicies[item.policy]} (${item.policy})`, `覆盖状态：${resourceStates[item.coverage_status]}`, `边界：${resourceGap(item)}`, `登记原因：${item.reason}`, `策略 ID：${item.policy_id || '未绑定执行策略'}`, `清理入口：${item.cleanup_entrypoint || '无自动清理入口'}`, `授权有效期：${item.authorization_expiry === 'owner_security_ttl' ? '由 Owner 安全 TTL 独立判断' : '本登记不评估授权有效期'}`, `登记来源：${item.source}`, `登记簿 SHA256：${data.registry_sha256}`].join('\n');
    openDetailDrawer('资源治理依据', body);
  }));
}
async function refresh(): Promise<void> {
  const id = ++serial; const selected = tab;
  if (selected === 'profiles') profilesEnabled = false;
  layout('<p class="governance-empty" role="status">正在读取治理数据…</p>');
  try {
    if (acceptedScan) {
      const job = acceptedScan.job;
      const command = await request(`/api/admin/ops-inspections/commands/${job}`);
      if (id !== serial) return;
      if (!record(command) || command.job_id !== job || !['accepted', 'completed'].includes(String(command.state))
        || (command.state === 'completed' && (!Number.isSafeInteger(command.run_id) || Number(command.run_id) < 1 || typeof command.completed_at !== 'string' || Number.isNaN(Date.parse(command.completed_at))))) {
        throw new Error('本次巡查的完成状态尚未确认，请刷新后查看。');
      }
      if (command.state === 'completed') acceptedScan = null;
    }
    if (selected === 'resources') {
      const data = await request('/api/admin/ops-retention/resources');
      if (id !== serial) return;
      if (!isRetentionCoverage(data)) throw new Error('资源登记数据不完整，暂时无法确认覆盖情况。');
      renderResources(data);
      return;
    }
    if (selected === 'outcomes') {
      const view = await governanceOutcomesView(request);
      if (id !== serial) return;
      layout(view.html);
      if (root) view.bind(root, () => id === serial, refresh);
      return;
    }
    if (['overview', 'checks', 'issues'].includes(selected)) {
      const data = await request('/api/admin/ops-inspections');
      if (id !== serial) return;
      if (!isOverview(data)) throw new Error('巡查数据格式不完整，暂时无法确认状态。');
      overview = data;
      if (selected === 'overview') layout(overviewContent(data));
      if (selected === 'checks') {
        layout(table(['检查项 / 负责人', '状态', '检查范围 / 原因', '指标', '观察时间'], data.checks.map((c) => [`${esc(c.title)}<br><small>${esc(c.owner)} · ${esc(c.id)}</small>`, badge(c.status), `${esc(c.scope || '范围见资源清单')}<br>${esc(c.code)}`, metrics(c), esc(date(c.observed_at))])));
      }
      if (selected === 'issues') {
        layout(table(['问题', '状态', '首次 / 最近', '出现次数', '操作'], data.issues.map((i) => [`${esc(i.check_id)}<br>${esc(i.code)}`, badge(i.status), `${esc(date(i.first_seen))}<br>${esc(date(i.last_seen))}`, esc(i.occurrences), i.status === 'open' ? `<button class="admin-button" data-ack="${i.id}">确认跟进</button>` : '—'])));
        root?.querySelectorAll<HTMLButtonElement>('[data-ack]').forEach((button) => button.addEventListener('click', async () => {
          const issue = overview?.issues.find((i) => i.id === Number(button.dataset.ack)); if (!issue) return;
          const mutationSerial = serial;
          button.disabled = true;
          try { await request(`/api/admin/ops-inspections/issues/${issue.id}`, 'PATCH', { version: issue.version, status: 'acknowledged' }); if (mutationSerial === serial) await refresh(); } catch (error) { if (mutationSerial === serial) showError(error); }
        }));
      }
      return;
    }
    const path = selected === 'reports' ? '/api/admin/ops-inspections/reports' : selected === 'profiles' ? '/api/admin/ops-diagnostics/cpu-profiles' : selected === 'diagnostics' ? `/api/admin/ops-diagnostics${diagnosticQuery ? `?correlation=${encodeURIComponent(diagnosticQuery)}` : ''}` : '/api/admin/ops-retention';
    const data = await request(path);
    if (id !== serial) return;
    if (!record(data) || !Array.isArray(data.items)) throw new Error('返回数据不完整，请稍后重试。');
    const items = data.items.filter(record);
    if (selected === 'reports') {
      layout(`<p>每小时一个报告窗口。飞书已接受不等于群内已确认可见。</p>${table(['类型 / 报告窗口', '发送状态', '关联效果', '详情'], items.map((v, n) => [`${esc(({ hourly: '小时报告', critical: '严重告警', recovery: '恢复通知' } as Record<string, string>)[String(v.notification_kind)] || '巡查报告')}<br>${esc(date(v.hour_key))}`, badge(String(v.effect_state)), esc(v.effect_id || '—'), `<button class="admin-button" data-report="${n}">查看报告</button>`]))}`);
      root?.querySelectorAll<HTMLButtonElement>('[data-report]').forEach((button) => button.addEventListener('click', () => { const v = items[Number(button.dataset.report)]; const body = document.createElement('pre'); body.className = 'governance-report'; body.textContent = record(v.content) && record(v.content.content) ? String(v.content.content.text ?? '') : '内容已到期或不可读取'; openDetailDrawer('巡查报告', body); }));
    } else if (selected === 'diagnostics') {
      layout(`<form class="governance-query"><label>页面排查编号 <input name="correlation" value="${esc(diagnosticQuery)}" maxlength="128" autocomplete="off" placeholder="粘贴报错时的排查编号"></label><button class="admin-button" type="submit">定位问题</button><button class="admin-button" type="button" data-clear-query>查看全部</button></form>${table(['错误类别 / 路由', '关联摘要', '版本', '关联任务 / 效果', '次数 / 首次 / 最近'], items.map((v) => [`${esc(v.code || v.error_code)}<br>${esc(v.route_template || '—')}`, esc(v.correlation_digest), esc(v.release_sha), `${esc(v.job_ref || '—')} / ${esc(v.effect_ref || '—')}`, `${esc(v.occurrences ?? 1)}<br>${esc(date(v.first_seen || v.occurred_at))}<br>${esc(date(v.last_seen || v.occurred_at))}`]))}`);
      root?.querySelector<HTMLFormElement>('form')?.addEventListener('submit', (event) => { event.preventDefault(); const value = root.querySelector<HTMLInputElement>('[name="correlation"]')?.value.trim() || ''; if (value && !/^[a-f0-9]{32}$/.test(value)) { showError(new Error('排查编号应为 32 位字母和数字。')); return; } diagnosticQuery = value; void refresh(); });
      root?.querySelector('[data-clear-query]')?.addEventListener('click', () => { diagnosticQuery = ''; void refresh(); });
    } else if (selected === 'profiles') {
      if (typeof data.enabled !== 'boolean' || data.target !== 'api' || data.duration_seconds !== 5 || data.worker_coverage !== 'not_supported') throw new Error('采样范围未确认，暂时无法启动。');
      profilesEnabled = data.enabled;
      const states: Record<string, string> = { sampling: '正在采样', outcome_unknown: '结果未知', completed: '已完成', failed: '采样失败' };
      layout(`<p>采集当前 API 服务的 5 秒 CPU 性能样本，用于定位耗时函数。Worker 进程尚未覆盖。</p><p>每人每小时最多 3 次，全局每小时最多 6 次；文件和在线详情 30 天后到期。</p>${profilesEnabled ? '' : '<p class="governance-note">采样暂未启用。需要先启用运行巡查与过程清理。</p>'}${profileBusy ? '<p role="status">正在采样，请稍候…</p>' : ''}${table(['采样编号 / 状态', '运行版本', '采样时间 / 到期时间', '文件', '操作'], items.map((v) => [esc(v.id) + '<br>' + esc(states[String(v.state)] || '未知'), esc(v.release_sha), esc(date(v.accepted_at)) + '<br>' + esc(date(v.expires_at)), typeof v.bytes === 'number' ? esc(v.bytes) + ' 字节' : '未知', v.state === 'completed' && typeof v.id === 'string' && /^[a-f0-9]{32}$/.test(v.id) && typeof v.expires_at === 'string' && Date.parse(v.expires_at) > Date.now() ? `<a class="admin-button" href="/api/admin/ops-diagnostics/cpu-profiles/${v.id}/download" download>下载样本</a>` : esc(v.failure_code || '暂无可下载文件')]))}`);
    } else {
      const history = await request('/api/admin/ops-retention/runs');
      if (id !== serial) return;
      if (!record(history) || !Array.isArray(history.items)) throw new Error('清理记录暂不可读，无法确认清理结果。');
      layout(`<p>纯过程数据保留 720 小时；业务事实、防重依据、在途状态和备份不参与按时间自动删除。</p>${table(['策略', '资源', '保留', '状态', '预览'], items.map((v) => [esc(v.id), esc(v.resource), esc(v.retention), esc(v.status), ['enabled', 'preview_only'].includes(String(v.status)) ? `<button class="admin-button" data-preview="${esc(v.id)}">预览候选</button>` : '受原生机制或保护规则管理']))}<h2>宿主过程文件与日志</h2>${record(history.host) && record(history.host.runtime) ? table(['删除文件', '逻辑载荷字节', '受保护文件'], [[esc(history.host.runtime.deleted),esc(history.host.runtime.bytes),esc(history.host.runtime.protected)]]) : ''}${storageEvidence(history.host)}<h2>发布包与回滚保护</h2>${storageEvidence(history.release)}<p>磁盘净变化包含同时发生的写入，可能为负，不能直接归因为清理回收量。</p><h2>最近数据库清理记录</h2>${table(['策略', '截止时间', '状态', '删除行数', '过程载荷字节', '完成时间'], history.items.filter(record).map((v) => [esc(v.policy), esc(date(v.cutoff)), esc(v.state), esc(v.deleted_rows), esc(v.payload_bytes), esc(date(v.completed_at))]))}<p class="governance-note">数据库载荷释放可供复用，不代表文件系统回收。清理仅执行已登记白名单；未分类资源保持保护。</p>`);
      root?.querySelectorAll<HTMLButtonElement>('[data-preview]').forEach((button) => button.addEventListener('click', async () => { const current = serial; button.disabled = true; try { const value = await request(`/api/admin/ops-retention/preview?policy=${encodeURIComponent(button.dataset.preview || '')}`); if (current !== serial) return; if (!record(value)) throw new Error('候选预览格式错误。'); const body = document.createElement('div'); body.textContent = `截止：${date(value.cutoff)}；${value.has_more === true ? '本批候选' : '候选'}：${value.candidates}${value.has_more === true ? '（仍有更多，未统计全部积压）' : ''}；估算载荷：${value.estimated_payload_bytes} 字节。${value.protected_reason || ''}`; openDetailDrawer('清理候选预览', body); } catch (error) { if (current === serial) showError(error); } finally { if (current === serial) button.disabled = false; } }));
    }
  } catch (error) { if (id === serial) showError(error); }
}
function showError(error: unknown): void { if (error instanceof AccessError) overview = null; layout(`<p class="governance-error" role="alert">${esc(error instanceof Error ? error.message : '操作失败')}</p>`); }
function renderHeader(): void {
  mountPageHeaderActions('governance', [{ label: '刷新', variant: 'secondary', onClick: refresh }, tab === 'profiles' ? { label: profileRequestKey ? '查看上次采样结果' : '采样 5 秒', variant: 'primary', disabled: !profilesEnabled || profileBusy, onClick: async () => {
    const current = serial;
    profileRequestKey ||= crypto.randomUUID();
    profileBusy = true;
    renderHeader();
    try {
      const result = await request('/api/admin/ops-diagnostics/cpu-profiles', 'POST', {}, profileRequestKey);
      if (!record(result) || !['sampling', 'outcome_unknown', 'completed', 'failed'].includes(String(result.state))) throw new Error('采样结果未确认，请查看上次结果。');
      if (result.state === 'completed' || result.state === 'failed') profileRequestKey = null;
      if (current === serial) await refresh();
    } catch (error) { if (current === serial) showError(error); }
    finally { profileBusy = false; renderHeader(); }
  } } : { label: '立即巡查', variant: 'primary', onClick: async () => { const current = serial; try { const result = await request('/api/admin/ops-inspections/runs', 'POST', {}); if (current === serial) { if (record(result) && result.state === 'accepted' && Number.isSafeInteger(result.job_id) && Number(result.job_id) > 0) acceptedScan = {job: Number(result.job_id)}; await refresh(); } } catch (error) { if (current === serial) showError(error); } } }]);
}
if (root) void refresh();
