import { getBroadcastJobHistoryItem, readBroadcastJobHistory, type BroadcastJobHistory } from '../../api/broadcastJobHistory';
import { esc } from './util';

type Options = { historyID?: string };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';

function input(options: Options): number | undefined {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['broadcast_job_history', 'history_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('群发任务历史参数无效');
  if (query.get('broadcast_job_history') !== '1') throw new Error('群发任务历史入口无效');
  const raw = options.historyID ?? query.get('history_id') ?? undefined;
  if (raw === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(raw) || !Number.isSafeInteger(Number(raw))) throw new Error('群发任务历史 ID 无效');
  return Number(raw);
}
function url(id?: number): string {
  const query = new URLSearchParams({ broadcast_job_history: '1' });
  if (id !== undefined) query.set('history_id', String(id));
  return `automation.html?${query.toString()}`;
}
function shown(value: unknown): string { return value === null ? 'NULL' : value === '' ? '（空字符串）' : esc(String(value)); }
function summary(item: BroadcastJobHistory): string {
  return `历史 #${shown(item.id)} · 源任务 #${shown(item.source_id)}<br>来源：${shown(item.source_table)} / ${shown(item.original_source_type)} · 原状态：${shown(item.original_status)}<br>计划：${shown(item.scheduled_for)} · 优先级：${shown(item.priority)} · 目标/尝试/发送/失败：${shown(item.target_count)} / ${shown(item.attempt_count)} / ${shown(item.sent_count)} / ${shown(item.failed_count)}`;
}
function detail(item: BroadcastJobHistory): string {
  return `<dl>${Object.entries(item).map(([key, value]) => `<dt>${esc(key)}</dt><dd style="white-space:pre-wrap;overflow-wrap:anywhere">${shown(value)}</dd>`).join('')}</dl>`;
}

export async function mountBroadcastJobHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  stage.innerHTML = `<main data-broadcast-job-history style="padding:20px;display:grid;gap:14px"><a href="automation.html">返回当前自动化</a><h1 style="margin:0;font-size:20px">V1 群发任务历史（只读）</h1><p style="color:#8F5A16">仅展示 V1 封存的原始任务观察；原状态、审批、计数、Provider 标记与时间不代表本次发送、重试或外部效果。页面不会创建任务、发送消息、重试或调用 Provider。</p><section data-broadcast-job-history-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-broadcast-job-history-results]')!;
  let id: number | undefined;
  try { id = input(options); } catch (error) { results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '群发任务历史参数无效')}；未读取数据。</p>`; return; }
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取群发任务历史…</p>';
    try {
      if (id !== undefined) {
        const item = await getBroadcastJobHistoryItem(id);
        results.innerHTML = `<p><a href="${url()}">返回历史列表</a></p><article data-broadcast-job-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(item)}${detail(item)}</article>`;
        return;
      }
      const page = await readBroadcastJobHistory(offset, 50);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">封存群发任务</th><th style="${cell}">只读详情</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}">${summary(item)}</td><td style="${cell}"><a href="${url(item.id)}" style="color:#245BDB">查看详情</a></td></tr>`).join('') || `<tr><td colspan="2" style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span> <button data-broadcast-job-history-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button> <button data-broadcast-job-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div>`;
      results.querySelector<HTMLButtonElement>('[data-broadcast-job-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      results.querySelector<HTMLButtonElement>('[data-broadcast-job-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '群发任务历史读取失败')}；未显示历史数据。</p>`;
    }
  };
  await load();
}
