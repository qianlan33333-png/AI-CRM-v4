import { readMessageHistory, readMessageHistoryDetail, type MessageHistoryItem, type MessageHistoryQuery } from '../../api/messageHistory';
import { ApiError } from '../../api/transport';
import { esc } from './util';

const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const text = (value: string | number | null): string => value === null ? '未记录（NULL）' : value === '' ? '（空字符串）' : esc(value);
function historyURL(id?: number, customerID?: number): string {
  return 'customers.html?message_history=1' + (id === undefined ? '' : '&history_message_id=' + id) + (customerID === undefined ? '' : '&customer_id=' + customerID);
}
function message(item: MessageHistoryItem, detail: boolean, customerID?: number): string {
  const timestamp = item.send_time_basis === 'civil_unzoned'
    ? `${text(item.original_send_time)} · 未定时区（civil_unzoned）；未推定 UTC 时刻`
    : `原时刻：${text(item.original_send_time)} · 明确时区时刻：${text(item.sent_at)}`;
  return `<article data-message-id="${item.id}" style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px;display:grid;gap:10px">
    <header style="display:flex;justify-content:space-between;gap:12px"><strong>历史消息 #${item.id}</strong>${detail ? '' : `<a href="${historyURL(item.id, customerID)}">查看单条历史</a>`}</header>
    <div style="color:#667085;font-size:12px">源行 #${item.source_id} · sequence=${text(item.sequence)} · 会话：${text(item.chat_type)} · 消息类型：${text(item.message_type)}</div>
    <div>${item.customer_id === null ? '客户关联：未解析' : `DM01 历史映射 · customer_id=${item.customer_id}（非当前 Provider 验证）`}</div>
    <div data-message-body style="white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7;border-left:3px solid #DEE0E3;padding:8px 12px">${text(item.content_masked)}</div>
    <div data-message-time style="white-space:pre-wrap;font-size:12px;color:#8F5A16">${timestamp}</div>
    <div style="font-size:12px;color:#667085">记录创建时间：${text(item.created_at)} · 归档摘要 ${item.source_payload_digest.map((byte) => byte.toString(16).padStart(2, '0')).join('').slice(0, 16)}…</div>
  </article>`;
}
function positive(raw: string | undefined): number | undefined {
  if (raw === undefined) return undefined;
  const id = Number(raw);
  if (!/^[1-9]\d*$/.test(raw) || !Number.isSafeInteger(id)) throw new Error('聊天历史 ID 或 customer_id 必须是正整数');
  return id;
}
export async function mountMessageHistory(stage: HTMLElement, options: { historyID?: string; customerID?: string } = {}): Promise<void> {
  stage.innerHTML = `<div data-message-history style="flex:1;min-height:0;overflow:auto;padding:20px;display:grid;gap:16px;align-content:start"><header><a href="customers.html">返回客户管理</a><h1 style="font-size:20px;margin:12px 0 8px">V1 聊天历史（只读）</h1><p style="color:#8F5A16;line-height:1.7">仅展示服务端脱敏历史正文；不等于当前会话或当前客户关系。未触发同步、发送或 Provider 外部效果。未定时区的源时刻保留原文，不推定 UTC。</p></header><div data-message-controls></div><section data-message-results style="display:grid;gap:12px"></section></div>`;
  const results = stage.querySelector<HTMLElement>('[data-message-results]')!;
  let historyID: number | undefined, customerID: number | undefined;
  try { historyID = positive(options.historyID); customerID = positive(options.customerID); }
  catch { results.innerHTML = '<p role="alert">聊天历史 ID 或 customer_id 无效；未读取历史或当前客户数据。</p>'; return; }
  let query: MessageHistoryQuery = { customerID };
  let generation = 0;
  const bind = (selector: string, action: () => void): void => {
    const element = stage.querySelector<HTMLElement>(selector);
    if (element) { (element as HTMLElement & { __dcBound?: boolean }).__dcBound = true; element.onclick = action; }
  };
  const load = async (offset: number): Promise<void> => {
    const request = ++generation;
    results.innerHTML = '<p role="status">正在读取聊天历史…</p>';
    try {
      if (historyID !== undefined) {
        const item = await readMessageHistoryDetail(historyID);
        if (request !== generation) return;
        if (customerID !== undefined && item.customer_id !== customerID) throw new Error('历史客户筛选不匹配');
        results.innerHTML = `<a href="${historyURL(undefined, customerID)}">返回历史列表</a>${message(item, true)}`;
        return;
      }
      const page = await readMessageHistory({ ...query, limit: 20, offset });
      if (request !== generation) return;
      results.innerHTML = page.items.map((item) => message(item, false, query.customerID)).join('') || '<p>暂无符合筛选的聊天历史记录</p>';
      results.innerHTML += `<div style="display:flex;justify-content:space-between;align-items:center"><span>共 ${page.total} 条 · offset=${page.offset}</span><div><button data-message-prev style="${button}" ${offset === 0 ? 'disabled' : ''}>上一页</button> <button data-message-next style="${button}" ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      bind('[data-message-prev]', () => { void load(Math.max(0, offset - 20)); });
      bind('[data-message-next]', () => { void load(offset + 20); });
    } catch (error) {
      if (request !== generation) return;
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof ApiError ? error.message : '聊天历史读取失败')}；未显示旧数据，也未回退 Mock。</p><button data-message-retry style="${button}">重新读取</button>`;
      bind('[data-message-retry]', () => { void load(offset); });
    }
  };
  if (historyID === undefined) {
    stage.querySelector('[data-message-controls]')!.innerHTML = `<form data-message-filter style="display:flex;gap:12px;flex-wrap:wrap;align-items:end"><label>历史映射 customer_id <input data-message-customer inputmode="numeric" value="${customerID ?? ''}" placeholder="仅 canonical 客户 ID"></label><label>会话类型 <select data-message-chat><option value="">全部</option><option value="private">私聊</option><option value="group">群聊</option></select></label><button style="${button}" type="submit">筛选历史</button></form>`;
    const form = stage.querySelector<HTMLFormElement>('[data-message-filter]')!;
    form.querySelectorAll<HTMLElement>('button,input,select').forEach((element) => { (element as HTMLElement & { __dcBound?: boolean }).__dcBound = true; });
    form.onsubmit = (event) => {
      event.preventDefault();
      try {
        const raw = stage.querySelector<HTMLInputElement>('[data-message-customer]')!.value;
        query = { customerID: positive(raw === '' ? undefined : raw), chatType: stage.querySelector<HTMLSelectElement>('[data-message-chat]')!.value as MessageHistoryQuery['chatType'] || undefined };
        void load(0);
      } catch { generation++; results.innerHTML = '<p role="alert">customer_id 必须是 canonical 正整数；未读取数据。</p>'; }
    };
  }
  await load(0);
}
