import { getStaticHistory, readStaticHistory, type StaticHistoryItem, type StaticHistoryKind } from '../../api/staticHistory';
import { esc } from './util';

type Options = { kind?: string; historyID?: string; parentID?: string };
type Row = Record<string, unknown>;
const kinds: StaticHistoryKind[] = ['GroupInvite', 'ProductPageSlice', 'CycleStrategy', 'CycleVersion', 'CycleDocument', 'CycleMetric', 'CycleReference'];
const labels: Record<StaticHistoryKind, string> = { GroupInvite: '群邀请元数据', ProductPageSlice: '商品页切片', CycleStrategy: '周期策略', CycleVersion: '周期版本', CycleDocument: '周期文档', CycleMetric: '周期指标观察', CycleReference: '周期引用观察' };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';

function numberID(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error('静态历史 ID 无效');
  return Number(value);
}
function parse(options: Options): { kind: StaticHistoryKind; id?: number; parent?: number } {
  const query = new URLSearchParams(location.search), allowed = new Set(['static_history', 'history_kind', 'history_id', 'history_parent_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('静态历史参数无效');
  if (query.get('static_history') !== '1') throw new Error('静态历史入口无效');
  const kind = options.kind ?? query.get('history_kind') ?? 'GroupInvite';
  if (!kinds.includes(kind as StaticHistoryKind)) throw new Error('静态历史类别无效');
  const id = numberID(options.historyID ?? query.get('history_id') ?? undefined);
  const parent = numberID(options.parentID ?? query.get('history_parent_id') ?? undefined);
  if (parent !== undefined && !['CycleVersion', 'CycleDocument'].includes(kind)) throw new Error('静态历史父级无效');
  if (id !== undefined && parent !== undefined) throw new Error('静态历史详情不能同时携带父级筛选');
  return { kind: kind as StaticHistoryKind, id, parent };
}
function url(kind: StaticHistoryKind, id?: number, parent?: number): string {
  const query = new URLSearchParams({ static_history: '1', history_kind: kind });
  if (id !== undefined) query.set('history_id', String(id));
  if (parent !== undefined) query.set('history_parent_id', String(parent));
  return `config.html?${query.toString()}`;
}
function text(value: unknown): string {
  if (value === null) return 'NULL';
  if (value === '') return '（空字符串）';
  return esc(String(value));
}
function row(item: StaticHistoryItem): Row { return item as unknown as Row; }
function digest(item: StaticHistoryItem): string {
  const value = row(item).source_payload_digest;
  return Array.isArray(value) ? `摘要 ${value.slice(0, 4).map((part) => Number(part).toString(16).padStart(2, '0')).join('')}…` : '摘要无效';
}
function source(item: StaticHistoryItem, kind: StaticHistoryKind): string {
 const value = row(item);
 return `历史 #${text(value.id)} · 源行 #${text(value.source_id)}${['CycleMetric', 'CycleReference'].includes(kind) ? '' : ` · ${digest(item)}`}`;
}
function json(value: unknown): string { return esc(JSON.stringify(value)); }
function summary(kind: StaticHistoryKind, item: StaticHistoryItem): string {
 const value = row(item), summarySource = source(item, kind);
 if (kind === 'GroupInvite') return `${summarySource}<br>名称/标题：${text(value.name)} / ${text(value.title)}<br>原状态/启用：${text(value.original_state)} / ${text(value.original_enabled)}<br>创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
 if (kind === 'ProductPageSlice') return `${summarySource}<br>商品/图片源引用：${text(value.product_source_id)} / ${text(value.image_source_id)} · 排序：${text(value.sort_order)}<br>原启用：${text(value.original_enabled)}<br>创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
 if (kind === 'CycleStrategy') return `${summarySource}<br>策略：${text(value.strategy_key)} · ${text(value.title)}<br>节奏/时区/状态：${text(value.cadence)} / ${text(value.timezone)} / ${text(value.original_status)}<br>创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
 if (kind === 'CycleVersion') return `${summarySource}<br>策略历史 #${text(value.strategy_history_id)} · 版本：${text(value.version)} · ${text(value.label)}<br>原治理状态：${text(value.original_governance)} · 生效：${text(value.effective_from)}<br>创建：${text(value.created_at)}`;
 if (kind === 'CycleDocument') return `${summarySource}<br>版本历史 #${text(value.version_history_id)} · schema：${text(value.schema_version)}<br>文档包摘要：${text(value.document_pack_hash)}<br>创建：${text(value.created_at)}`;
 if (kind === 'CycleMetric') return `${summarySource}<br>指标：${text(value.metric_key)} · ${text(value.label)}<br>源 run/snapshot：${text(value.run_source_id)} / ${text(value.last_snapshot_source_id)} · 数值：${text(value.numerator)} / ${text(value.denominator)} / ${text(value.value)}<br>限制：${json(value.limitations)}<br>创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
 return `${summarySource}<br>引用：${text(value.reference_key)} · ${text(value.reference_type)} · ${text(value.label)}<br>源 run/snapshot：${text(value.run_source_id)} / ${text(value.last_snapshot_source_id)} · 来源：${text(value.source_system)} / ${text(value.reference_source_id)}<br>证据摘要/状态：${text(value.evidence_hash)} / ${text(value.data_status)}<br>创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
}
function children(kind: StaticHistoryKind, item: StaticHistoryItem): string {
  if (kind === 'CycleStrategy') return `<a data-static-history-child="CycleVersion" href="${url('CycleVersion', undefined, item.id)}" style="color:#245BDB">查看该策略的历史版本</a>`;
  if (kind === 'CycleVersion') return `<a data-static-history-child="CycleDocument" href="${url('CycleDocument', undefined, item.id)}" style="color:#245BDB">查看该版本的历史文档</a>`;
  return '';
}
function detail(item: StaticHistoryItem): string {
  return `<dl>${Object.entries(item).map(([key, value]) => {
    const shown = key === 'source_key_digest' || key === 'source_payload_digest' ? (value as number[]).map((part) => Number(part).toString(16).padStart(2, '0')).join('') : key === 'limitations' ? JSON.stringify(value) : value;
    return `<dt>${esc(key)}</dt><dd style="white-space:pre-wrap;overflow-wrap:anywhere">${text(shown)}</dd>`;
  }).join('')}</dl>`;
}

export async function mountStaticHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  stage.innerHTML = `<main data-static-history style="padding:20px;display:grid;gap:14px"><a href="config.html">返回当前配置</a><nav aria-label="静态历史类别" style="display:flex;gap:8px;flex-wrap:wrap">${kinds.map((kind) => `<a data-static-history-kind="${kind}" href="${url(kind)}">${labels[kind]}</a>`).join('')}</nav><h1 style="margin:0;font-size:20px">V1 静态历史（只读）</h1><p style="color:#8F5A16">只展示已封存的静态历史事实。源 ID、空字符串、NULL、负数及原时间保持来源含义；不会创建群邀请、变更商品页、调用 Provider 或执行任何周期动作。</p><section data-static-history-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-static-history-results]')!;
  let input: { kind: StaticHistoryKind; id?: number; parent?: number };
  try { input = parse(options); } catch (error) { results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '静态历史参数无效')}；未读取数据。</p>`; return; }
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取静态历史…</p>';
    try {
      if (input.id !== undefined) {
        const item = await getStaticHistory(input.kind, input.id);
        results.innerHTML = `<p><a href="${url(input.kind, undefined, input.parent)}">返回${esc(labels[input.kind])}列表</a></p><article data-static-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(input.kind, item)}<p>${children(input.kind, item)}</p>${detail(item)}</article>`;
        return;
      }
      const page = await readStaticHistory(input.kind, offset, 20, input.parent);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">${esc(labels[input.kind])}</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}"><a href="${url(input.kind, item.id)}" style="color:#245BDB">查看详情</a><br>${summary(input.kind, item)}<br>${children(input.kind, item)}</td></tr>`).join('') || `<tr><td style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · offset=${page.offset}</span> <button data-static-history-prev style="${button}" ${offset === 0 ? 'disabled' : ''}>上一页</button> <button data-static-history-next style="${button}" ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button> <button data-static-history-retry style="${button}">重试本页</button></div>`;
      results.querySelector<HTMLButtonElement>('[data-static-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, offset - 20)); });
      results.querySelector<HTMLButtonElement>('[data-static-history-next]')?.addEventListener('click', () => { void load(offset + 20); });
      results.querySelector<HTMLButtonElement>('[data-static-history-retry]')?.addEventListener('click', () => { void load(offset); });
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '静态历史读取失败')}；未显示旧数据或 Mock 数据。</p><button data-static-history-retry style="${button}">重试本页</button>`;
      results.querySelector<HTMLButtonElement>('[data-static-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load();
}
