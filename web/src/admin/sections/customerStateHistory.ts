import { getCustomerStateHistory, readCustomerStateHistory, type CustomerStateHistoryItem, type CustomerStateHistoryKind } from '../../api/customerStateHistory';
import { esc } from './util';

type Options = { kind?: string; historyID?: string };
type Row = Record<string, unknown>;
const kinds: CustomerStateHistoryKind[] = ['snapshot', 'change', 'term_tag'];
const labels: Record<CustomerStateHistoryKind, string> = { snapshot: '状态快照', change: '状态变更', term_tag: '班期标签映射' };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';

function numberID(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error('客户状态历史 ID 无效');
  return Number(value);
}
function parse(options: Options): { kind: CustomerStateHistoryKind; id?: number } {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['customer_state_history', 'history_kind', 'history_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('客户状态历史参数无效');
  if (query.get('customer_state_history') !== '1') throw new Error('客户状态历史入口无效');
  const kind = options.kind ?? query.get('history_kind') ?? 'snapshot';
  if (!kinds.includes(kind as CustomerStateHistoryKind)) throw new Error('客户状态历史类别无效');
  return { kind: kind as CustomerStateHistoryKind, id: numberID(options.historyID ?? query.get('history_id') ?? undefined) };
}
function url(kind: CustomerStateHistoryKind, id?: number): string {
  const query = new URLSearchParams({ customer_state_history: '1', history_kind: kind });
  if (id !== undefined) query.set('history_id', String(id));
  return `config.html?${query.toString()}`;
}
function shown(value: unknown): string {
  if (value === null) return 'NULL';
  if (value === '') return '（空字符串）';
  return esc(String(value));
}
function row(item: CustomerStateHistoryItem): Row { return item as unknown as Row; }
function digest(value: unknown): string {
  return Array.isArray(value) ? `摘要 ${value.slice(0, 4).map((part) => Number(part).toString(16).padStart(2, '0')).join('')}…` : '摘要无效';
}
function source(kind: CustomerStateHistoryKind, value: Row): string {
  return kind === 'snapshot' ? `历史 #${shown(value.id)} · 源快照` : `历史 #${shown(value.id)} · 源行 #${shown(value.source_id)}`;
}
function summary(kind: CustomerStateHistoryKind, item: CustomerStateHistoryItem): string {
  const value = row(item), base = `${source(kind, value)} · ${digest(value.source_payload_digest)}`;
  if (kind === 'snapshot') return `${base}<br>状态/标签：${shown(value.signup_status)} / ${shown(value.signup_label_name)}<br>设置/企微标签同步：${shown(value.set_at)} / ${shown(value.wecom_tag_sync_status)}<br>创建/更新：${shown(value.created_at)} / ${shown(value.updated_at)}`;
  if (kind === 'change') return `${base}<br>状态：${shown(value.old_signup_status)} → ${shown(value.new_signup_status)}<br>标签：${shown(value.old_label_name)} → ${shown(value.new_label_name)}<br>设置/同步：${shown(value.set_at)} / ${shown(value.wecom_tag_sync_status)}`;
  return `${base}<br>标签组/标签：${shown(value.tag_group_name)} / ${shown(value.tag_name)}<br>班期：${shown(value.class_term_no)} · ${shown(value.class_term_label)}<br>原启用：${shown(value.original_active)} · 创建/更新：${shown(value.created_at)} / ${shown(value.updated_at)}`;
}
function detail(item: CustomerStateHistoryItem): string {
  return `<dl>${Object.entries(row(item)).map(([key, value]) => `<dt>${esc(key)}</dt><dd style="white-space:pre-wrap;overflow-wrap:anywhere">${Array.isArray(value) ? digest(value) : shown(value)}</dd>`).join('')}</dl>`;
}

export async function mountCustomerStateHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  stage.innerHTML = `<main data-customer-state-history style="padding:20px;display:grid;gap:14px"><a href="config.html">返回当前配置</a><nav aria-label="客户状态历史类别" style="display:flex;gap:8px;flex-wrap:wrap">${kinds.map((kind) => `<a data-customer-state-history-kind="${kind}" href="${url(kind)}">${labels[kind]}</a>`).join('')}</nav><h1 style="margin:0;font-size:20px">V1 客户状态历史（只读）</h1><p style="color:#8F5A16">只展示封存的历史观察，不会更新当前客户状态、同步标签、调用 Provider 或执行自动化动作。</p><section data-customer-state-history-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-customer-state-history-results]')!;
  let input: { kind: CustomerStateHistoryKind; id?: number };
  try { input = parse(options); } catch (error) { results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '客户状态历史参数无效')}；未读取数据。</p>`; return; }
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取客户状态历史…</p>';
    try {
      if (input.id !== undefined) {
        const item = await getCustomerStateHistory(input.kind, input.id);
        results.innerHTML = `<p><a href="${url(input.kind)}">返回${esc(labels[input.kind])}列表</a></p><article data-customer-state-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(input.kind, item)}${detail(item)}</article>`;
        return;
      }
      const page = await readCustomerStateHistory(input.kind, offset, 20);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">${esc(labels[input.kind])}</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}"><a href="${url(input.kind, item.id)}" style="color:#245BDB">查看详情</a><br>${summary(input.kind, item)}</td></tr>`).join('') || `<tr><td style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · offset=${page.offset}</span> <button data-customer-state-history-prev style="${button}" ${offset === 0 ? 'disabled' : ''}>上一页</button> <button data-customer-state-history-next style="${button}" ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button> <button data-customer-state-history-retry style="${button}">重试本页</button></div>`;
      results.querySelector<HTMLButtonElement>('[data-customer-state-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, offset - 20)); });
      results.querySelector<HTMLButtonElement>('[data-customer-state-history-next]')?.addEventListener('click', () => { void load(offset + 20); });
      results.querySelector<HTMLButtonElement>('[data-customer-state-history-retry]')?.addEventListener('click', () => { void load(offset); });
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '客户状态历史读取失败')}；未显示旧数据或 Mock 数据。</p><button data-customer-state-history-retry style="${button}">重试本页</button>`;
      results.querySelector<HTMLButtonElement>('[data-customer-state-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load();
}
