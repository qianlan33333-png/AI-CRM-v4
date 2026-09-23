import { getHxcHistory, readHxcHistory, type HxcHistoryItem, type HxcHistoryKind } from '../../api/hxcHistory';
import { esc } from './util';

const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const kinds: HxcHistoryKind[] = ['meta', 'snapshot', 'activation', 'lead', 'batch', 'sender_config', 'send_record', 'member_usage', 'chat_job'];
type Options = { kind?: string; historyID?: string; generation?: string };
const value = (input: string | number | null): string => input === null ? 'NULL' : input === '' ? '（空字符串）' : esc(input);
const source = (item: HxcHistoryItem): string => 'generation' in item ? `历史 #${item.id} · 分代 #${item.generation}` : `历史 #${item.id} · 源行 #${item.source_id}`;
function date(value: unknown): string { return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value) ? esc(value) : value === null ? 'NULL' : '无效日期'; }
function url(kind: HxcHistoryKind, id?: number, generation?: number): string { return `funnel.html?hxc_history=1&history_kind=${kind}${id === undefined ? '' : `&history_id=${id}`}${generation === undefined ? '' : `&generation=${generation}`}`; }
function parse(options: Options): { kind: HxcHistoryKind; id?: number; generation?: number } {
  const query = new URLSearchParams(location.search); const allowed = new Set(['hxc_history', 'history_kind', 'history_id', 'generation']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('HXC 历史参数无效');
  const kind = options.kind ?? query.get('history_kind') ?? 'snapshot'; const id = options.historyID ?? query.get('history_id') ?? undefined; const generation = options.generation ?? query.get('generation') ?? undefined;
  if (query.get('hxc_history') !== '1' || !kinds.includes(kind as HxcHistoryKind)) throw new Error('HXC 历史入口无效');
  if (id !== undefined && (!/^[1-9]\d*$/.test(id) || !Number.isSafeInteger(Number(id)))) throw new Error('HXC 历史 ID 无效');
  if (generation !== undefined && (kind !== 'member_usage' || !/^(?:0|-?[1-9]\d*)$/.test(generation) || !Number.isSafeInteger(Number(generation)))) throw new Error('HXC 历史 generation 无效');
  return { kind: kind as HxcHistoryKind, id: id === undefined ? undefined : Number(id), generation: generation === undefined ? undefined : Number(generation) };
}
function summary(kind: HxcHistoryKind, item: HxcHistoryItem): string {
  if (kind === 'member_usage') { const x = item as Extract<HxcHistoryItem, { generation: number }>; return `${source(x)}<br>历史成员/注册/真实使用：${x.is_member ? 'true' : 'false'} / ${x.is_registered ? 'true' : 'false'} / ${x.has_real_usage ? 'true' : 'false'}<br>会员层级/状态：${value(x.membership_tier)} / ${value(x.membership_status)}<br>来源：会员 ${value(x.membership_source)} · 注册 ${value(x.registration_source)} · 使用 ${value(x.usage_source)}<br>时间：注册 ${value(x.registered_at)} · 首次/最近使用 ${value(x.first_used_at)} / ${value(x.last_used_at)} · 投影 ${value(x.projected_at)}<br>历史分代观察不代表当前会员资格、客户归属或员工归属。`; }
  if (kind === 'meta') { const x = item as Extract<HxcHistoryItem, { trigger_source: string }>; return `${source(x)}<br>状态：${value(x.status)} · 触发：${value(x.trigger_source)}<br>开始：${value(x.started_at)} · 完成：${value(x.finished_at)}<br>行/成员/用户/仅成员：${x.row_count}/${x.member_hit}/${x.user_hit}/${x.only_member}`; }
  if (kind === 'snapshot') { const x = item as Extract<HxcHistoryItem, { observation: string }>; const customer = x.customer_id === null ? 'Customer：未解析（不猜测）' : `<a href="customerDetail.html?id=${x.customer_id}" style="color:#245BDB">Customer360 #${x.customer_id}</a>`; return `${source(x)}<br>${customer} · 观测：${value(x.observed_at)}<br>漏斗：${value(x.funnel_state)} · CRM/HXC：${value(x.crm_hxc_state)}/${value(x.hxc_member_status)}<br>日期：CRM ${date(x.crm_created_at)} · 问卷 ${date(x.last_questionnaire_at)} · 订阅期 ${date(x.subscription_period_start)}<br>批量/会话：${x.conversation_chat}/${x.conversation_consult}/${x.conversation_lesson} · 消息：${x.messages_user}/${x.messages_ai}`; }
  if (kind === 'activation') { const x = item as Extract<HxcHistoryItem, { source_table: string }>; return `${source(x)}<br>源表：${value(x.source_table)}<br>原状态：${value(x.original_state)} · is_active=${x.is_active ? 'true' : 'false'}<br>批次来源：${value(x.legacy_import_batch_ref)}<br>创建/更新：${value(x.created_at)} / ${value(x.updated_at)}`; }
  if (kind === 'lead') { const x = item as Extract<HxcHistoryItem, { original_type: string }>; return `${source(x)}<br>类型：${value(x.original_type)} · is_active=${x.is_active ? 'true' : 'false'}<br>批次来源：${value(x.legacy_import_batch_ref)}<br>创建/更新：${value(x.created_at)} / ${value(x.updated_at)}`; }
  if (kind === 'batch') { const x = item as Extract<HxcHistoryItem, { import_type: string }>; return `${source(x)}<br>导入类型：${value(x.import_type)}<br>总计/成功/失败：${x.total_rows}/${x.success_rows}/${x.failed_rows}<br>创建：${value(x.created_at)}`; }
  if (kind === 'sender_config') { const x = item as Extract<HxcHistoryItem, { priority: number }>; return `${source(x)}<br>优先级：${x.priority} · V1启用标记=${x.original_is_active ? 'true' : 'false'}<br>创建/更新：${value(x.created_at)} / ${value(x.updated_at)}<br>该标记不授予当前发送权限。`; }
  if (kind === 'chat_job') { const x = item as Extract<HxcHistoryItem, { queue_source_id: number | null }>; return `${source(x)}<br>历史队列/成员/发送记录源引用：${value(x.queue_source_id)} / ${value(x.member_source_id)} / ${value(x.send_record_source_id)}<br>原状态/发送渠道：${value(x.original_status)} / ${value(x.send_channel)}<br>创建/更新：${value(x.created_at)} / ${value(x.updated_at)}<br>完成字段原文：${value(x.finished_at_source)}<br>该历史对话任务不可执行，不表示当前完成或 Provider 成功。`; }
  const x = item as Extract<HxcHistoryItem, { task_type: string }>; return `${source(x)}<br>任务类型：${value(x.task_type)} · 原状态：${value(x.original_status)}<br>选择/合格/已发送/失败：${x.selected_count}/${x.eligible_count}/${x.sent_count}/${x.failed_count}<br>目标来源：${value(x.target_source)} #${value(x.target_source_id)}<br>创建/状态同步/刷新：${value(x.created_at)} / ${value(x.last_status_sync_at)} / ${value(x.last_refreshed_at)}<br>历史计数不表示本次任务或外部发送成功。`;
}
export async function mountHXCHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  stage.innerHTML = `<main data-hxc-history style="padding:20px;display:grid;gap:14px"><a href="funnel.html">返回漏斗</a><nav aria-label="HXC 历史类别" style="display:flex;gap:8px;flex-wrap:wrap">${kinds.map((kind) => `<a data-hxc-kind="${kind}" href="${url(kind)}">${kind}</a>`).join('')}</nav><h1 style="margin:0;font-size:20px">V1 HXC 历史观察（只读）</h1><p style="color:#8F5A16">只展示已封存历史观察；不会刷新当前漏斗、创建群发、写权益或调用 Provider。发送配置、发送记录和历史对话任务均不代表当前权限、完成状态或外部成功；会员使用是历史分代观察，不代表当前会员资格。</p><section data-hxc-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-hxc-results]')!;
  let input: { kind: HxcHistoryKind; id?: number; generation?: number };
  try { input = parse(options); } catch (error) { results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '历史参数无效')}；未读取数据。</p>`; return; }
  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取 HXC 历史…</p>';
    try {
      if (input.id !== undefined) {
        const item = await getHxcHistory(input.kind, input.id);
        results.innerHTML = `<p><a href="${url(input.kind, undefined, input.generation)}">返回历史列表</a></p><article data-hxc-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(input.kind, item)}</article>`;
        return;
      }
      const page = await readHxcHistory(input.kind, offset, 20, undefined, undefined, input.generation);
      const filter = input.kind === 'member_usage' ? `<form data-hxc-generation-filter style="margin-bottom:10px"><label>历史 generation <input name="generation" inputmode="numeric" value="${input.generation ?? ''}"></label> <button style="${button}">筛选</button></form>` : '';
      results.innerHTML = `${filter}<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">HXC 历史</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}"><a href="${url(input.kind, item.id, input.generation)}" style="color:#245BDB">查看详情</a><br>${summary(input.kind, item)}</td></tr>`).join('') || `<tr><td style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · offset=${page.offset}</span> <button data-hxc-prev style="${button}" ${offset === 0 ? 'disabled' : ''}>上一页</button> <button data-hxc-next style="${button}" ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button> <button data-hxc-retry style="${button}">重试本页</button></div>`;
      results.querySelector<HTMLFormElement>('[data-hxc-generation-filter]')?.addEventListener('submit', (event) => { event.preventDefault(); const generation = String(new FormData(event.currentTarget as HTMLFormElement).get('generation') ?? ''); if (generation !== '' && (!/^(?:0|-?[1-9]\d*)$/.test(generation) || !Number.isSafeInteger(Number(generation)))) { results.innerHTML = '<p role="alert">HXC 历史 generation 无效；未读取数据。</p>'; return; } window.location.assign(url('member_usage', undefined, generation === '' ? undefined : Number(generation))); });
      results.querySelector<HTMLButtonElement>('[data-hxc-prev]')?.addEventListener('click', () => { void load(Math.max(0, offset - 20)); });
      results.querySelector<HTMLButtonElement>('[data-hxc-next]')?.addEventListener('click', () => { void load(offset + 20); });
      results.querySelector<HTMLButtonElement>('[data-hxc-retry]')?.addEventListener('click', () => { void load(offset); });
    } catch (error) { results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : 'HXC 历史读取失败')}；未显示旧数据或 Mock 数据。</p><button data-hxc-retry style="${button}">重试本页</button>`; results.querySelector<HTMLButtonElement>('[data-hxc-retry]')?.addEventListener('click', () => { void load(offset); }); }
  };
  await load();
}
