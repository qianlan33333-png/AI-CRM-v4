import {
  getCampaignDefinitionHistory,
  readCampaignDefinitionHistory,
  readCampaignDefinitionSteps,
  type CampaignDefinitionHistoryPage,
  type HistoricalCampaignDefinition,
  type HistoricalCampaignDefinitionStep,
} from '../../api/campaignDefinitionHistory';
import { esc } from './util';

const pageSize = 20;
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const text = (value: string | number | null): string => value === null ? 'NULL' : value === '' ? '（空字符串）' : esc(value);

function parse(): number | undefined {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['definition_history', 'history_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('Campaign 定义历史参数无效；未读取数据');
  if (query.get('definition_history') !== '1') throw new Error('Campaign 定义历史入口无效；未读取数据');
  const raw = query.get('history_id');
  if (raw === null) return undefined;
  if (!/^[1-9]\d*$/.test(raw) || !Number.isSafeInteger(Number(raw))) throw new Error('Campaign 定义历史 ID 无效；未读取数据');
  return Number(raw);
}

function url(historyID?: number): string {
  const query = new URLSearchParams({ definition_history: '1' });
  if (historyID !== undefined) query.set('history_id', String(historyID));
  return `campaigns.html?${query.toString()}`;
}

function range(page: CampaignDefinitionHistoryPage<unknown>): string {
  if (!page.items.length) return page.total === 0 ? '共 0 条' : `共 ${page.total} 条 · 当前页为空`;
  return `共 ${page.total} 条 · 当前 ${page.offset + 1}–${page.offset + page.items.length}`;
}

function shell(body: string): string {
  return `<main data-campaign-definition-history style="padding:20px;display:grid;gap:14px"><a href="campaigns.html">返回当前 Campaign</a><nav><a href="${url()}">历史定义列表</a></nav><h1 style="margin:0;font-size:20px">V1 Campaign 定义历史（只读）</h1><p style="margin:0;color:#8F5A16;line-height:1.7">只展示旧 Campaign 定义与步骤的非当前历史事实。原状态、时间、内容、源 ID 或 current parent 仅用于历史观察；不会审批、启动、排队、重发或调用 Provider。</p><section data-campaign-definition-results></section></main>`;
}

function definitionRow(item: HistoricalCampaignDefinition): string {
  return `<tr><td style="${cell}"><a href="${url(item.id)}" style="color:#245BDB">查看定义与步骤</a><br>历史 #${item.id}<br>源 #${text(item.source_id)}</td><td style="${cell}">code：${text(item.code)}<br>名称：${text(item.display_name)}<br>意图：${text(item.intent)}</td><td style="${cell}">原审核/运行：${text(item.review_status)} / ${text(item.run_status)}<br>原终态：${text(item.original_disposition)} · ${text(item.original_reason)}</td><td style="${cell}">创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}</td></tr>`;
}

function stepRow(item: HistoricalCampaignDefinitionStep): string {
  const parent = item.source_parent_state === 'history_definition' ? `历史定义 #${text(item.history_definition_id)}` : item.source_parent_state === 'current_definition' ? `当前 code（仅源关联）：${text(item.current_campaign_code)}` : 'unresolved_definition：源父级未解析';
  return `<tr><td style="${cell}">历史步骤 #${item.id}<br>源步骤 #${text(item.source_id)}<br>源 Campaign #${text(item.campaign_source_id)}</td><td style="${cell}">${parent}<br>源 Segment #${text(item.segment_source_id)}</td><td style="${cell}">index/day：${item.step_index} / ${item.day_offset}<br>time/timezone：${text(item.send_time)} / ${text(item.timezone)}<br>stop_on_reply：${item.stop_on_reply} · skip：${item.skip_recent_days}</td><td style="${cell}"><div style="white-space:pre-wrap">${text(item.content_masked)}</div><small>原终态：${text(item.original_disposition)} · ${text(item.original_reason)}。不会重新发送。</small></td></tr>`;
}

function pager<T>(page: CampaignDefinitionHistoryPage<T>, reload: (offset: number) => void): string {
  return `<div style="margin-top:12px;display:flex;gap:8px;align-items:center;flex-wrap:wrap"><span>${esc(range(page))}</span><button data-campaign-definition-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-campaign-definition-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button><button data-campaign-definition-retry style="${button}">重试本页</button></div>`;
}

async function mountSteps(host: HTMLElement, campaignSourceID?: number): Promise<void> {
  const title = campaignSourceID === undefined ? '全部历史步骤（包含 unresolved/current parent）' : `历史步骤 · 源定义 #${campaignSourceID}`;
  const load = async (offset = 0): Promise<void> => {
    host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="status">正在读取历史步骤…</p>`;
    try {
      const page = await readCampaignDefinitionSteps(campaignSourceID, offset, pageSize);
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><div style="overflow:auto"><table style="width:100%;min-width:920px;border-collapse:collapse"><thead><tr><th style="${cell}">步骤 / 来源</th><th style="${cell}">源父级</th><th style="${cell}">原计划</th><th style="${cell}">历史内容</th></tr></thead><tbody>${page.items.map(stepRow).join('') || `<tr><td colspan="4" style="${cell}">暂无历史步骤</td></tr>`}</tbody></table></div>${pager(page, load)}`;
      host.querySelector<HTMLButtonElement>('[data-campaign-definition-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      host.querySelector<HTMLButtonElement>('[data-campaign-definition-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
      host.querySelector<HTMLButtonElement>('[data-campaign-definition-retry]')?.addEventListener('click', () => { void load(page.offset); });
    } catch (error) {
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史步骤读取失败')}；未显示旧数据或 Mock 数据。</p><button data-campaign-definition-retry style="${button}">重试本页</button>`;
      host.querySelector<HTMLButtonElement>('[data-campaign-definition-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load();
}

async function mountList(results: HTMLElement): Promise<void> {
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取历史定义…</p>';
    try {
      const page = await readCampaignDefinitionHistory(offset, pageSize);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;min-width:850px;border-collapse:collapse"><thead><tr><th style="${cell}">查看 / 来源</th><th style="${cell}">历史定义</th><th style="${cell}">原状态</th><th style="${cell}">源时间</th></tr></thead><tbody>${page.items.map(definitionRow).join('') || `<tr><td colspan="4" style="${cell}">暂无历史定义</td></tr>`}</tbody></table></div>${pager(page, load)}<section data-campaign-definition-all-steps style="margin-top:14px"></section>`;
      results.querySelector<HTMLButtonElement>('[data-campaign-definition-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      results.querySelector<HTMLButtonElement>('[data-campaign-definition-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
      results.querySelector<HTMLButtonElement>('[data-campaign-definition-retry]')?.addEventListener('click', () => { void load(page.offset); });
      await mountSteps(results.querySelector<HTMLElement>('[data-campaign-definition-all-steps]')!);
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史定义读取失败')}；未显示旧数据或 Mock 数据。</p><button data-campaign-definition-retry style="${button}">重试本页</button>`;
      results.querySelector<HTMLButtonElement>('[data-campaign-definition-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load();
}

async function mountDetail(results: HTMLElement, historyID: number): Promise<void> {
  results.innerHTML = '<p role="status">正在读取历史定义…</p>';
  try {
    const definition = await getCampaignDefinitionHistory(historyID);
    results.innerHTML = `<p><a href="${url()}">返回历史定义列表</a></p><article data-campaign-definition-id="${definition.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px"><h2 style="margin:0 0 8px;font-size:16px">历史定义 #${definition.id}</h2><p>源 #${text(definition.source_id)} · code：${text(definition.code)} · 原审核/运行：${text(definition.review_status)} / ${text(definition.run_status)}</p><p>原终态：${text(definition.original_disposition)} · ${text(definition.original_reason)}。不等于当前活动 Campaign。</p></article><section data-campaign-definition-steps style="margin-top:14px"></section>`;
    await mountSteps(results.querySelector<HTMLElement>('[data-campaign-definition-steps]')!, definition.source_id);
  } catch (error) {
    results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史定义读取失败')}；未显示旧数据或 Mock 数据。</p><a href="${url()}">返回历史定义列表</a>`;
  }
}

export async function mountCampaignDefinitionHistory(stage: HTMLElement): Promise<void> {
  stage.innerHTML = shell('');
  const results = stage.querySelector<HTMLElement>('[data-campaign-definition-results]')!;
  try {
    const historyID = parse();
    if (historyID === undefined) return mountList(results);
    return mountDetail(results, historyID);
  } catch (error) {
    results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史定义参数无效')}；未读取数据。</p>`;
  }
}
