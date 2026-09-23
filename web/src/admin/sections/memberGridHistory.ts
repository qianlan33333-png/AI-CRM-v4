import {
  readMemberUsageHistory, readMemberUsageHistoryDetail, readMemberViewHistory, readMemberViewHistoryDetail,
  type MemberGridHistoryKind, type MemberGridHistoryPage, type MemberUsageHistoryItem, type MemberViewHistoryItem,
} from '../../api/memberGridHistory';
import { esc } from './util';

const button = 'height:32px;padding:0 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const control = 'height:34px;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px;background:#fff;color:#344054;font-size:13px';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
type Props = { kind?: string; historyID?: string; productID?: string; customerID?: string };
type HistoryRow = MemberViewHistoryItem | MemberUsageHistoryItem;

const id = (value: string | undefined, label: string): number | undefined => {
  if (value === undefined || value === '') return undefined;
  if (!/^[1-9]\d*$/.test(value)) throw new Error(`${label} 必须是正整数`);
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${label} 必须是安全整数`);
  return parsed;
};
const text = (value: string | number | null): string => value === null ? '未记录' : value === '' ? '（空）' : esc(String(value));
const bool = (value: boolean): string => value ? 'true' : 'false';
const link = (kind: MemberGridHistoryKind, options: { historyID?: number; productID?: number; customerID?: number } = {}): string => {
  const query = new URLSearchParams({ member_grid_history: '1', history_kind: kind });
  if (options.historyID) query.set('history_id', String(options.historyID));
  if (options.productID) query.set('product_id', String(options.productID));
  if (options.customerID) query.set('customer_id', String(options.customerID));
  return `spProductData.html?${query}`;
};
const errorText = (error: unknown): string => error instanceof Error ? error.message : 'V1 Member Grid 历史读取失败';

function row(...values: string[]): string { return `<tr>${values.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`; }
function listRows(kind: MemberGridHistoryKind, items: HistoryRow[], filter?: number, failure = ''): string {
  if (failure) return '<tr><td colspan="5" style="padding:22px;text-align:center;color:#B42318">读取失败，未显示历史数据</td></tr>';
  if (!items.length) return '<tr><td colspan="5" style="padding:22px;text-align:center;color:#8F959E">当前筛选没有 V1 历史记录</td></tr>';
  return items.map((item) => {
    if (kind === 'view') { const view = item as MemberViewHistoryItem; return row(String(view.id), text(view.name), view.product_id === null ? '未关联 V2 商品' : String(view.product_id), `默认：${bool(view.is_default)} · 位置：${view.position}`, `<a href="${link('view', { historyID: view.id, productID: filter })}">查看详情</a>`); }
    const usage = item as MemberUsageHistoryItem;
    return row(String(usage.id), usage.customer_id === null ? '未关联客户' : String(usage.customer_id), `正式登录：${bool(usage.formally_logged_in)} · Token 使用：${bool(usage.has_token_usage)}`, text(usage.learning_plan_id), `<a href="${link('usage', { historyID: usage.id, customerID: filter })}">查看详情</a>`);
  }).join('');
}

function listHtml(kind: MemberGridHistoryKind, page: MemberGridHistoryPage<HistoryRow>, filter?: number, failure = ''): string {
  const isView = kind === 'view';
  const filterName = isView ? 'product' : 'customer';
  const filterLabel = isView ? 'Product ID' : 'Customer ID';
  const previous = page.offset > 0 ? `<button id="member-grid-history-prev" style="${button}">上一页</button>` : '';
  const next = page.offset + page.items.length < page.total ? `<button id="member-grid-history-next" style="${button}">下一页</button>` : '';
  const headers = isView ? '<th style="padding:9px 12px">历史 ID</th><th style="padding:9px 12px">名称</th><th style="padding:9px 12px">对应 Product</th><th style="padding:9px 12px">旧快照</th><th style="padding:9px 12px">操作</th>' : '<th style="padding:9px 12px">历史 ID</th><th style="padding:9px 12px">对应 Customer</th><th style="padding:9px 12px">旧状态</th><th style="padding:9px 12px">学习计划</th><th style="padding:9px 12px">操作</th>';
  const error = failure ? `<section style="padding:12px;border:1px solid #FDA29B;border-radius:8px;background:#FFFBFA;color:#B42318">${esc(failure)} <button id="member-grid-history-retry" style="${button};margin-left:8px">重试 GET</button></section>` : '';
  return `<div style="padding:20px;display:grid;gap:14px;align-content:start"><div style="display:flex;justify-content:space-between;gap:12px;align-items:center;flex-wrap:wrap"><div><div style="font-size:12px;color:#8F959E">交易 / Member Grid / V1 历史</div><h1 style="margin:4px 0 0;font-size:20px">Member Grid 历史（只读）</h1></div><a href="spProducts.html" style="${button};display:inline-flex;align-items:center;text-decoration:none">返回周期商品</a></div><section style="padding:14px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px;line-height:20px">这是 V1 归档快照，只能读取；不表示当前登录、权益或权限。旧保存视图不会在此执行，也不会产生外部调用。</section><nav style="display:flex;gap:8px;flex-wrap:wrap"><a href="${link('view')}" style="${button};display:inline-flex;align-items:center;text-decoration:none${isView ? ';border-color:#3370ff;color:#1849A9' : ''}">旧保存视图</a><a href="${link('usage')}" style="${button};display:inline-flex;align-items:center;text-decoration:none${isView ? '' : ';border-color:#3370ff;color:#1849A9'}">旧使用快照</a></nav>${error}<section style="border:1px solid #DEE0E3;border-radius:8px;background:#fff;overflow:hidden"><div style="padding:14px 16px;border-bottom:1px solid #EEF0F3;display:flex;justify-content:space-between;gap:12px;align-items:center;flex-wrap:wrap"><div><h2 style="margin:0;font-size:15px">${isView ? '旧保存视图' : '旧使用快照'}</h2><p style="margin:5px 0 0;color:#667085;font-size:12px">${failure ? '数量未获取' : `共 ${page.total} 条`}</p></div><form id="member-grid-history-filter" style="display:flex;gap:8px;align-items:center;flex-wrap:wrap"><label style="font-size:12px;color:#667085">${filterLabel}<input name="${filterName}_id" value="${filter || ''}" inputmode="numeric" style="${control};width:110px;margin-left:5px"></label><button style="${button}">筛选</button><button type="button" id="member-grid-history-clear" style="${button}">清除</button><span id="member-grid-history-filter-error" role="alert" style="color:#B42318;font-size:12px"></span></form></div><div style="overflow-x:auto"><table style="width:100%;min-width:760px;border-collapse:collapse"><thead><tr style="background:#FAFAFB;text-align:left;color:#667085">${headers}</tr></thead><tbody>${listRows(kind, page.items, filter, failure)}</tbody></table></div><div style="padding:12px 16px;display:flex;justify-content:flex-end;gap:8px">${previous}${next}</div></section></div>`;
}

function detailHtml(kind: MemberGridHistoryKind, item: HistoryRow | undefined, filter?: number, failure = ''): string {
  const back = link(kind, kind === 'view' ? { productID: filter } : { customerID: filter });
  const fields = !item ? [] : kind === 'view'
    ? (() => { const view = item as MemberViewHistoryItem; return [['历史 ID', view.id], ['旧视图 ID', view.source_view_id], ['旧周期商品 ID', view.source_service_product_id], ['对应 Product', view.product_id === null ? '未关联 V2 商品' : view.product_id], ['名称', view.name], ['位置', view.position], ['默认视图', bool(view.is_default)], ['Schema 版本', view.schema_version], ['版本', view.version], ['创建时间', view.created_at], ['更新时间', view.updated_at]]; })()
    : (() => { const usage = item as MemberUsageHistoryItem; return [['历史 ID', usage.id], ['对应 Customer', usage.customer_id === null ? '未关联客户' : usage.customer_id], ['正式登录', bool(usage.formally_logged_in)], ['Token 使用', bool(usage.has_token_usage)], ['学习计划 ID', usage.learning_plan_id], ['学习计划当前值', usage.learning_plan_current], ['学习计划总值', usage.learning_plan_total], ['近 7 日打开数', usage.open_count_7d], ['最后打开时间', usage.last_open_at], ['刷新时间', usage.refreshed_at]]; })();
  const error = failure ? `<section style="padding:12px;border:1px solid #FDA29B;border-radius:8px;background:#FFFBFA;color:#B42318">${esc(failure)} <button id="member-grid-history-detail-retry" style="${button};margin-left:8px">重试 GET</button></section>` : '';
  return `<div style="padding:20px;display:grid;gap:14px;align-content:start"><div style="display:flex;justify-content:space-between;gap:12px;align-items:center"><div><div style="font-size:12px;color:#8F959E">交易 / Member Grid / V1 历史</div><h1 style="margin:4px 0 0;font-size:20px">${kind === 'view' ? '旧保存视图' : '旧使用快照'}详情（只读）</h1></div><a href="${back}" style="${button};display:inline-flex;align-items:center;text-decoration:none">返回列表</a></div>${error}<section style="padding:14px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px;line-height:20px">仅展示归档业务字段；不显示旧 ConfigJSON、原始身份或摘要。此记录不能执行旧视图，也不表示当前登录、权益或权限。</section><section style="border:1px solid #DEE0E3;border-radius:8px;background:#fff;overflow:hidden"><table style="width:100%;border-collapse:collapse"><tbody>${fields.map(([label, value]) => row(`<strong>${esc(String(label))}</strong>`, text(value as string | number | null))).join('')}</tbody></table></section></div>`;
}

export async function mountMemberGridHistory(stage: HTMLElement, props: Props = {}): Promise<void> {
  const kind: MemberGridHistoryKind = props.kind === undefined || props.kind === '' || props.kind === 'view' ? 'view' : props.kind === 'usage' ? 'usage' : (() => { throw new Error('Member Grid 历史类型无效'); })();
  const productID = id(props.productID, 'Product ID');
  const customerID = id(props.customerID, 'Customer ID');
  const historyID = id(props.historyID, '历史 ID');
  if (kind === 'view' && customerID !== undefined) throw new Error('旧保存视图不接受 Customer ID 筛选');
  if (kind === 'usage' && productID !== undefined) throw new Error('旧使用快照不接受 Product ID 筛选');
  const filter = kind === 'view' ? productID : customerID;
  const list = (offset = 0): Promise<MemberGridHistoryPage<HistoryRow>> => kind === 'view' ? readMemberViewHistory(offset, 20, filter) : readMemberUsageHistory(offset, 20, filter);
  const detail = (): Promise<HistoryRow> => kind === 'view' ? readMemberViewHistoryDetail(historyID as number, filter) : readMemberUsageHistoryDetail(historyID as number, filter);
  const showList = async (offset = 0, failure = ''): Promise<void> => {
    try {
      const page = await list(offset);
      stage.innerHTML = listHtml(kind, page, filter, failure);
      stage.querySelector<HTMLFormElement>('#member-grid-history-filter')?.addEventListener('submit', (event) => {
        event.preventDefault(); const input = new FormData(event.currentTarget as HTMLFormElement).get(kind === 'view' ? 'product_id' : 'customer_id');
        const selected = typeof input === 'string' ? input.trim() : ''; const label = kind === 'view' ? 'Product ID' : 'Customer ID';
        try { id(selected, label); } catch (error) { const target = stage.querySelector<HTMLElement>('#member-grid-history-filter-error'); if (target) target.textContent = errorText(error); return; }
        void mountMemberGridHistory(stage, kind === 'view' ? { kind, productID: selected } : { kind, customerID: selected }).catch((error) => { const target = stage.querySelector<HTMLElement>('#member-grid-history-filter-error'); if (target) target.textContent = errorText(error); });
      });
      stage.querySelector<HTMLButtonElement>('#member-grid-history-clear')?.addEventListener('click', () => { void mountMemberGridHistory(stage, { kind }); });
      stage.querySelector<HTMLButtonElement>('#member-grid-history-prev')?.addEventListener('click', () => { void showList(Math.max(0, page.offset - page.limit)); });
      stage.querySelector<HTMLButtonElement>('#member-grid-history-next')?.addEventListener('click', () => { void showList(page.offset + page.limit); });
    } catch (error) {
      stage.innerHTML = listHtml(kind, { items: [], total: 0, limit: 20, offset }, filter, errorText(error));
      stage.querySelector<HTMLButtonElement>('#member-grid-history-retry')?.addEventListener('click', () => { void showList(offset); });
    }
  };
  const showDetail = async (failure = ''): Promise<void> => {
    try {
      const item = await detail();
      stage.innerHTML = detailHtml(kind, item, filter, failure);
    } catch (error) {
      stage.innerHTML = detailHtml(kind, undefined, filter, errorText(error));
      stage.querySelector<HTMLButtonElement>('#member-grid-history-detail-retry')?.addEventListener('click', () => { void showDetail(); });
    }
  };
  if (historyID !== undefined) await showDetail(); else await showList();
}
