import {
  getCampaignHistoryPlanItem,
  getCampaignHistorySegmentItem,
  readCampaignHistoryMembers,
  readCampaignHistoryMessages,
  readCampaignHistoryPlans,
  readCampaignHistoryRecipients,
  readCampaignHistorySegments,
  type CampaignHistoryPage,
  type HistoricalBroadcastMessage,
  type HistoricalBroadcastPlan,
  type HistoricalBroadcastRecipient,
  type HistoricalCampaignMember,
  type HistoricalCampaignSegment,
} from '../../api/campaignHistory';
import { esc } from './util';

const pageSize = 20;
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const text = (value: string | number | null): string => value === null ? '未关联' : value === '' ? '（空）' : esc(value);
const customer = (id: number | null): string => id === null ? '未关联客户（不猜测）' : `<a href="customerDetail.html?id=${id}" style="color:#245BDB">Customer360 #${id}</a>`;
const row = (...cells: string[]): string => `<tr>${cells.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`;

type HistoryParams = { segment?: number; plan?: number; recipient?: number };

function requireID(value: string | null, label: string): number | undefined {
  if (value === null) return undefined;
  if (!/^[1-9]\d*$/.test(value)) throw new Error(`${label} 无效；未读取历史数据`);
  const id = Number(value);
  if (!Number.isSafeInteger(id)) throw new Error(`${label} 无效；未读取历史数据`);
  return id;
}

function parseParams(): HistoryParams {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['history', 'segment', 'plan', 'recipient']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('V1 Campaign 历史参数无效；未读取历史数据');
  if (query.get('history') !== '1') throw new Error('V1 Campaign 历史入口无效；未读取历史数据');
  const result = { segment: requireID(query.get('segment'), '历史分群 ID'), plan: requireID(query.get('plan'), '历史计划 ID'), recipient: requireID(query.get('recipient'), '历史收件人 ID') };
  if (result.segment !== undefined && (result.plan !== undefined || result.recipient !== undefined)) throw new Error('历史分群与群发计划不能同时查看');
  if (result.recipient !== undefined && result.plan === undefined) throw new Error('历史收件人必须指定所属历史计划');
  return result;
}

function url(params: HistoryParams = {}): string {
  const query = new URLSearchParams({ history: '1' });
  if (params.segment !== undefined) query.set('segment', String(params.segment));
  if (params.plan !== undefined) query.set('plan', String(params.plan));
  if (params.recipient !== undefined) query.set('recipient', String(params.recipient));
  return `campaigns.html?${query.toString()}`;
}

function range(page: CampaignHistoryPage<unknown>): string {
  if (!page.items.length) return page.total === 0 ? '共 0 条' : `共 ${page.total} 条 · 当前页为空`;
  return `共 ${page.total} 条 · 当前 ${page.offset + 1}–${page.offset + page.items.length}`;
}

async function mountPage<T>(host: HTMLElement, title: string, columns: string[], read: (offset: number) => Promise<CampaignHistoryPage<T>>, render: (item: T) => string): Promise<void> {
  const load = async (offset: number): Promise<void> => {
    host.innerHTML = `<h2 style="margin:0;font-size:16px">${esc(title)}</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page = await read(offset);
      host.innerHTML = `<h2 style="margin:0;font-size:16px">${esc(title)}</h2><div style="overflow:auto"><table style="width:100%;min-width:850px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085">${columns.map((column) => `<th style="${cell}">${esc(column)}</th>`).join('')}</tr></thead><tbody>${page.items.map(render).join('') || `<tr><td colspan="${columns.length}" style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px;display:flex;gap:8px;align-items:center;flex-wrap:wrap"><span>${esc(range(page))}</span><button data-history-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button><button data-history-retry style="${button}">刷新本页</button></div>`;
      host.querySelector<HTMLButtonElement>('[data-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      host.querySelector<HTMLButtonElement>('[data-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
      host.querySelector<HTMLButtonElement>('[data-history-retry]')?.addEventListener('click', () => { void load(page.offset); });
    } catch (error) {
      host.innerHTML = `<h2 style="margin:0;font-size:16px">${esc(title)}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示旧数据或演示数据。</p><button data-history-retry style="${button}">重试本页</button>`;
      host.querySelector<HTMLButtonElement>('[data-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load(0);
}

function segmentRow(item: HistoricalCampaignSegment): string {
  const parent = item.source_parent_state === 'missing_campaign' ? '<strong style="color:#8F5A16">missing_campaign：源父 Campaign 缺失</strong>' : 'observed：仅源父关系已观察';
  return row(`<a href="${url({ segment: item.id })}" style="color:#245BDB">查看成员</a><br>历史分群 #${item.id}<br>源分群 #${item.source_id}`, `源 Campaign #${item.campaign_source_id}<br>源 Segment #${item.segment_source_id}<br>${parent}`, `code：${text(item.code)}<br>label：${text(item.label)}<br>priority：${item.priority}`, text(item.created_at));
}

function memberRow(item: HistoricalCampaignMember): string {
  return row(`历史成员 #${item.id}<br>源成员 #${item.source_id}<br>${customer(item.customer_id)}`, `历史分群 #${item.segment_history_id}<br>源 Campaign #${item.campaign_source_id}<br>源 Campaign Segment #${item.campaign_segment_source_id}<br>源 Segment #${item.segment_source_id}`, `原状态：${text(item.original_status)}<br>停止原因：${text(item.stop_reason)}<br>step：${item.current_step_index} · retry：${item.retry_count}`, `加入：${text(item.joined_at)}<br>下次：${text(item.next_due_at)}<br>最后步骤：${text(item.last_step_sent_at)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`);
}

function planRow(item: HistoricalBroadcastPlan): string {
  return row(`<a href="${url({ plan: item.id })}" style="color:#245BDB">查看收件人与历史消息</a><br>历史计划 #${item.id}<br>源计划 #${item.source_id}<br>源 plan_id：${text(item.source_plan_id)}`, `源 Campaign：${text(item.campaign_source_id)}<br>源 Segment：${text(item.segment_source_id)}<br>策略：${text(item.content_strategy)}<br>人工复制：${item.requires_manual_copy ? '是' : '否'}`, `原状态：${text(item.original_status)}<br>审核：${text(item.original_review_status)}<br>运行：${text(item.original_run_status)}<br>候选/跳过/上限：${item.candidate_count}/${item.skipped_count}/${item.max_recipients}`, `提交：${text(item.committed_at)}<br>过期：${text(item.expires_at)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`);
}

function recipientRow(item: HistoricalBroadcastRecipient, planID: number): string {
  return row(`<a href="${url({ plan: planID, recipient: item.id })}" style="color:#245BDB">查看历史消息</a><br>历史收件人 #${item.id}<br>源收件人 #${item.source_id}<br>${customer(item.customer_id)}`, `历史计划 #${item.plan_history_id}<br>显示名：${text(item.display_name)}`, `原审核：${text(item.original_approval_status)}<br>原发送：${text(item.original_send_status)}<br>计划消息数：${item.planned_message_count}`, `批准：${text(item.approved_at)}<br>拒绝：${text(item.rejected_at)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`);
}

function messageRow(item: HistoricalBroadcastMessage): string {
  return row(`历史消息 #${item.id}<br>源消息 #${item.source_id}<br>${customer(item.customer_id)}`, `历史计划 #${item.plan_history_id}<br>历史收件人 #${item.recipient_history_id}<br>序号：${item.sequence_index} · day_offset：${item.day_offset}`, `原状态：${text(item.original_status)}<br>原 send_time：${text(item.original_send_time)}<br>sent_at：${text(item.sent_at)}`, `<div style="white-space:pre-wrap">${text(item.content_masked)}</div><div style="color:#8F5A16;font-size:12px">历史文本仅展示；不表示新的外部发送或送达。</div>`);
}

function historyShell(body: string): string {
  return `<div style="padding:20px;display:grid;gap:16px;align-content:start"><header><a href="campaigns.html">返回当前 Campaign</a><h1 style="font-size:20px;margin:12px 0 8px">V1 Campaign 历史（只读）</h1><p style="margin:0;color:#8F5A16;line-height:1.7">仅展示迁移后的历史分群、成员与群发快照；源 ID 不等同于当前 V2 Campaign。原状态、时间或 sent_at 不代表新的审批、启动、重发、Provider 调用、外部发送或送达。</p></header>${body}</div>`;
}

async function mountOverview(stage: HTMLElement): Promise<void> {
  stage.innerHTML = historyShell('<section id="campaign-history-segments" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section><section id="campaign-history-plans" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section>');
  await Promise.all([
    mountPage(stage.querySelector<HTMLElement>('#campaign-history-segments')!, '历史分群', ['查看 / 来源', '源父关系', '名称 / 原优先级', '创建时间'], (offset) => readCampaignHistorySegments(offset, pageSize), segmentRow),
    mountPage(stage.querySelector<HTMLElement>('#campaign-history-plans')!, '历史群发计划', ['查看 / 来源', '源关联 / 策略', '原状态 / 计数', '源时间'], (offset) => readCampaignHistoryPlans(offset, pageSize), planRow),
  ]);
}

async function mountSegment(stage: HTMLElement, id: number): Promise<void> {
  stage.innerHTML = historyShell('<section id="campaign-history-detail" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section><section id="campaign-history-members" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section>');
  const detail = stage.querySelector<HTMLElement>('#campaign-history-detail')!;
  detail.innerHTML = '<p role="status">正在读取历史分群…</p>';
  try {
    const item = await getCampaignHistorySegmentItem(id);
    detail.innerHTML = `<a href="${url()}">返回历史列表</a><h2 style="font-size:16px">历史分群 #${item.id}</h2><p>源 Campaign #${item.campaign_source_id} · 源 Segment #${item.segment_source_id} · ${item.source_parent_state === 'missing_campaign' ? '<strong style="color:#8F5A16">missing_campaign</strong>' : 'observed'} · ${text(item.label)}</p>`;
  } catch (error) {
    detail.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史分群读取失败')}；未显示旧数据。</p><a href="${url()}">返回历史列表</a>`;
    stage.querySelector<HTMLElement>('#campaign-history-members')!.remove();
    return;
  }
  await mountPage(stage.querySelector<HTMLElement>('#campaign-history-members')!, `历史成员 · 分群 #${id}`, ['成员 / Customer360', '历史与源关联', '原状态', '源时间'], (offset) => readCampaignHistoryMembers(id, offset, pageSize), memberRow);
}

async function mountPlan(stage: HTMLElement, planID: number, recipientID?: number): Promise<void> {
  stage.innerHTML = historyShell('<section id="campaign-history-detail" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section><section id="campaign-history-recipients" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section><section id="campaign-history-messages" style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"></section>');
  const detail = stage.querySelector<HTMLElement>('#campaign-history-detail')!;
  detail.innerHTML = '<p role="status">正在读取历史群发计划…</p>';
  try {
    const item = await getCampaignHistoryPlanItem(planID);
    detail.innerHTML = `<a href="${url()}">返回历史列表</a><h2 style="font-size:16px">历史群发计划 #${item.id}</h2><p>源计划 #${item.source_id} · ${text(item.display_name)} · 原状态 ${text(item.original_status)}。仅历史快照，不提供审批、启动或重发。</p>`;
  } catch (error) {
    detail.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史计划读取失败')}；未显示旧数据。</p><a href="${url()}">返回历史列表</a>`;
    stage.querySelector<HTMLElement>('#campaign-history-recipients')!.remove();
    stage.querySelector<HTMLElement>('#campaign-history-messages')!.remove();
    return;
  }
  const recipients = stage.querySelector<HTMLElement>('#campaign-history-recipients')!;
  await mountPage(recipients, `历史收件人 · 计划 #${planID}`, ['查看 / 客户', '计划 / 显示名', '原状态', '源时间'], (offset) => readCampaignHistoryRecipients(planID, offset, pageSize), (item) => recipientRow(item, planID));
  const messages = stage.querySelector<HTMLElement>('#campaign-history-messages')!;
  if (recipientID === undefined) {
    messages.remove();
    return;
  }
  await mountPage(messages, `历史消息 · 收件人 #${recipientID}`, ['消息 / 客户', '历史关联', '原状态与时间', '历史文本'], async (offset) => {
    const page = await readCampaignHistoryMessages(recipientID, offset, pageSize);
    if (page.items.some((item) => item.plan_history_id !== planID)) throw new Error('历史消息与当前历史计划不一致，未显示数据');
    return page;
  }, messageRow);
}

export async function mountCampaignHistory(stage: HTMLElement): Promise<void> {
  const params = parseParams();
  if (params.segment !== undefined) return mountSegment(stage, params.segment);
  if (params.plan !== undefined) return mountPlan(stage, params.plan, params.recipient);
  return mountOverview(stage);
}
