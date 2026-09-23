import { getMarketingStateHistory, readMarketingStateHistory, type MarketingStateHistoryItem, type MarketingStateHistoryKind } from '../../api/marketingStateHistory';
import { esc } from './util';

type Options = { kind?: string; historyID?: string };
type Row = Record<string, unknown>;
const kinds: MarketingStateHistoryKind[] = ['state_snapshot', 'state_change', 'value_snapshot', 'value_change'];
const labels: Record<MarketingStateHistoryKind, string> = { state_snapshot: '营销状态快照', state_change: '营销状态变更', value_snapshot: '价值分群快照', value_change: '价值分群变更' };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';

function numberID(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error('营销状态历史 ID 无效');
  return Number(value);
}
function parse(options: Options): { kind: MarketingStateHistoryKind; id?: number } {
  const query = new URLSearchParams(location.search), allowed = new Set(['marketing_state_history', 'history_kind', 'history_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('营销状态历史参数无效');
  if (query.get('marketing_state_history') !== '1') throw new Error('营销状态历史入口无效');
  const kind = options.kind ?? query.get('history_kind') ?? 'state_snapshot';
  if (!kinds.includes(kind as MarketingStateHistoryKind)) throw new Error('营销状态历史类别无效');
  return { kind: kind as MarketingStateHistoryKind, id: numberID(options.historyID ?? query.get('history_id') ?? undefined) };
}
function url(kind: MarketingStateHistoryKind, id?: number): string {
  const query = new URLSearchParams({ marketing_state_history: '1', history_kind: kind });
  if (id !== undefined) query.set('history_id', String(id));
  return `config.html?${query.toString()}`;
}
function shown(value: unknown): string { return value === null ? 'NULL' : value === '' ? '（空字符串）' : esc(String(value)); }
function row(item: MarketingStateHistoryItem): Row { return item as unknown as Row; }
function source(value: Row): string { return `历史 #${shown(value.id)} · 源行 #${shown(value.source_id)}`; }
function summary(kind: MarketingStateHistoryKind, item: MarketingStateHistoryItem): string {
  const value = row(item), base = source(value);
  if (kind === 'state_snapshot') return `${base}<br>自动化/阶段：${shown(value.automation_key)} · ${shown(value.main_stage)} / ${shown(value.sub_stage)}<br>激活/转化/可转化：${shown(value.activated)} / ${shown(value.converted)} / ${shown(value.eligible_for_conversion)}<br>生命周期/进入/退出：${shown(value.lifecycle_status)} / ${shown(value.entered_at)} / ${shown(value.exited_at)}<br>最近激活原值：${shown(value.last_activation_at)}`;
  if (kind === 'state_change') return `${base}<br>自动化/阶段：${shown(value.automation_key)} · ${shown(value.main_stage)} / ${shown(value.sub_stage)}<br>变更原因：${shown(value.change_reason)} · 记录：${shown(value.recorded_at)}`;
  if (kind === 'value_snapshot') return `${base}<br>分群/排名/分数：${shown(value.segment)} / ${shown(value.segment_rank)} / ${shown(value.score)}<br>版本/原因：${shown(value.scoring_version)} / ${shown(value.computed_reason)}<br>评估/计算：${shown(value.evaluated_at)} / ${shown(value.computed_at)}`;
  return `${base}<br>分群/排名/分数：${shown(value.segment)} / ${shown(value.segment_rank)} / ${shown(value.score)}<br>版本/变更原因：${shown(value.scoring_version)} / ${shown(value.change_reason)}<br>评估/记录：${shown(value.evaluated_at)} / ${shown(value.recorded_at)}`;
}
function detail(item: MarketingStateHistoryItem): string { return `<dl>${Object.entries(row(item)).map(([key, value]) => `<dt>${esc(key)}</dt><dd style="white-space:pre-wrap;overflow-wrap:anywhere">${shown(value)}</dd>`).join('')}</dl>`; }

export async function mountMarketingStateHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  stage.innerHTML = `<main data-marketing-state-history style="padding:20px;display:grid;gap:14px"><a href="config.html">返回当前配置</a><nav aria-label="营销状态历史类别" style="display:flex;gap:8px;flex-wrap:wrap">${kinds.map((kind) => `<a data-marketing-state-history-kind="${kind}" href="${url(kind)}">${labels[kind]}</a>`).join('')}</nav><h1 style="margin:0;font-size:20px">V1 营销状态历史（只读）</h1><p style="color:#8F5A16">只展示封存观察；不会变更当前营销状态、价值分群、客户绑定，亦不会启动自动化、发送消息或调用 Provider。</p><section data-marketing-state-history-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-marketing-state-history-results]')!;
  let input: { kind: MarketingStateHistoryKind; id?: number };
  try { input = parse(options); } catch (error) { results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '营销状态历史参数无效')}；未读取数据。</p>`; return; }
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取营销状态历史…</p>';
    try {
      if (input.id !== undefined) {
        const item = await getMarketingStateHistory(input.kind, input.id);
        results.innerHTML = `<p><a href="${url(input.kind)}">返回${esc(labels[input.kind])}列表</a></p><article data-marketing-state-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(input.kind, item)}${detail(item)}</article>`;
        return;
      }
      const page = await readMarketingStateHistory(input.kind, offset, 20);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">${esc(labels[input.kind])}</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}"><a href="${url(input.kind, item.id)}" style="color:#245BDB">查看详情</a><br>${summary(input.kind, item)}</td></tr>`).join('') || `<tr><td style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · offset=${page.offset}</span> <button data-marketing-state-history-prev style="${button}" ${offset === 0 ? 'disabled' : ''}>上一页</button> <button data-marketing-state-history-next style="${button}" ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button> <button data-marketing-state-history-retry style="${button}">重试本页</button></div>`;
      results.querySelector<HTMLButtonElement>('[data-marketing-state-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, offset - 20)); });
      results.querySelector<HTMLButtonElement>('[data-marketing-state-history-next]')?.addEventListener('click', () => { void load(offset + 20); });
      results.querySelector<HTMLButtonElement>('[data-marketing-state-history-retry]')?.addEventListener('click', () => { void load(offset); });
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '营销状态历史读取失败')}；未显示旧数据或 Mock 数据。</p><button data-marketing-state-history-retry style="${button}">重试本页</button>`;
      results.querySelector<HTMLButtonElement>('[data-marketing-state-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load();
}
