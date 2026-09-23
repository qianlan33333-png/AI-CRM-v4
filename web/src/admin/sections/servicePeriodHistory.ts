import {
  readServicePeriodHistoryDefinitions, readServicePeriodHistoryEntitlements, readServicePeriodHistoryEvents,
  type ServicePeriodHistoryDefinition, type ServicePeriodHistoryEntitlement, type ServicePeriodHistoryEvent, type ServicePeriodHistoryPage,
} from '../../api/servicePeriodHistory';
import { esc } from './util';

const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const text = (value: string | number | null): string => value === null ? '未记录' : value === '' ? '（空）' : esc(String(value));
const customer = (id: number | null): string => id === null ? '未关联客户' : `<a href="customerDetail.html?id=${id}">客户 #${id}</a>`;
const order = (id: number | null): string => id === null ? '未关联订单' : `V2 订单 #${id}`;
const row = (...cells: string[]): string => `<tr>${cells.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`;

function definitionRow(item: ServicePeriodHistoryDefinition): string {
  return row(`${text(item.product_name)}<br>${text(item.product_code)}<br>V2 商品 #${item.product_id}`,
    `${(item.price_minor / 100).toFixed(2)} ${text(item.currency)}`,
    `${text(item.membership_config_name)}<br>配置 ID：${text(item.membership_config_id)}<br>时长：${item.duration_days} 天`,
    `源 deleted：${item.deleted ? 'true' : 'false'}<br>${text(item.updated_at)}`,
    `<a data-history-definition="${item.id}" href="spProductData.html?id=${item.product_id}&history=${item.id}">查看 V1 历史（只读）</a>`);
}

function entitlementRow(item: ServicePeriodHistoryEntitlement): string {
  return row(`历史权益 #${item.id}<br>源权益 #${item.source_entitlement_id}<br>${customer(item.customer_id)}`,
    `V1 快照状态：${text(item.status)}<br>配置：${text(item.membership_config_id)}`,
    `开始：${text(item.start_at)}<br>结束：${text(item.end_at)}`,
    `续期计数：${item.renewal_count}<br>${order(item.last_order_id)}<br>源订单号：${text(item.last_out_trade_no)}`,
    text(item.updated_at));
}

function eventRow(item: ServicePeriodHistoryEvent): string {
  return row(`历史事件 #${item.id}<br>源事件 #${item.source_event_id}<br>${text(item.event_id)}<br>${text(item.created_at)}`,
    `${text(item.event_type)}<br>调整天数：${item.duration_days}`,
    `${item.entitlement_id === null ? '未关联权益' : `历史权益 #${item.entitlement_id}`}<br>${customer(item.customer_id)}<br>${order(item.order_id)}<br>源订单号：${text(item.out_trade_no)}`,
    `开始：${text(item.before_start_at)}<br>结束：${text(item.before_end_at)}`,
    `开始：${text(item.after_start_at)}<br>结束：${text(item.after_end_at)}`);
}

// Each section clears stale data while loading/failing; retries are GET only.
async function mountHistoryPage<T>(section: HTMLElement, title: string, columns: string[], read: (offset: number) => Promise<ServicePeriodHistoryPage<T>>, renderRow: (item: T) => string): Promise<void> {
  const load = async (offset: number): Promise<void> => {
    section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page = await read(offset);
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><div style="overflow:auto"><table style="width:100%;min-width:900px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085">${columns.map((column) => `<th style="${cell}">${column}</th>`).join('')}</tr></thead><tbody>${page.items.map(renderRow).join('') || `<tr><td colspan="${columns.length}" style="${cell}">暂无 V1 历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px;display:flex;align-items:center;justify-content:space-between;gap:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.offset + page.items.length}</span><div style="display:flex;gap:8px"><button data-history-previous style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      section.querySelector('[data-history-previous]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      section.querySelector('[data-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重新读取</button>`;
      section.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load(0);
}

export async function mountServicePeriodHistory(stage: HTMLElement, definitionID?: string): Promise<void> {
  const id = definitionID === undefined ? undefined : Number(definitionID);
  if (definitionID !== undefined && (!/^[1-9]\d*$/.test(definitionID) || !Number.isSafeInteger(id))) throw new Error('V1 历史定义 ID 无效');
  stage.innerHTML = `<div style="padding:20px;display:grid;gap:16px;align-content:start"><header><a href="spProducts.html${id === undefined ? '' : '?history=1'}">${id === undefined ? '返回当前周期商品' : '返回 V1 历史列表'}</a><h1 style="font-size:20px;margin:12px 0 8px">V1 历史（只读）${id === undefined ? '' : ` · 定义 #${id}`}</h1><p style="color:#8F5A16;margin:0;line-height:1.7">原始快照状态与日期，不代表当前会员权益；未触发支付、授权、续期、退款或 Provider 外部效果。</p></header>${id === undefined ? '<section id="history-definitions"></section>' : '<section id="history-entitlements"></section><section id="history-events"></section>'}</div>`;
  if (id === undefined) {
    await mountHistoryPage(stage.querySelector<HTMLElement>('#history-definitions')!, '历史周期定义', ['商品', '原商品价格', '历史配置', '源删除标记 / 更新时间', '只读入口'], readServicePeriodHistoryDefinitions, definitionRow);
    return;
  }
  await Promise.all([
    mountHistoryPage(stage.querySelector<HTMLElement>('#history-entitlements')!, '历史权益', ['历史引用 / 客户', '源状态 / 配置', '源有效期', '续期 / 订单', '源更新时间'], (offset) => readServicePeriodHistoryEntitlements(id, offset), entitlementRow),
    mountHistoryPage(stage.querySelector<HTMLElement>('#history-events')!, '历史事件（不执行）', ['事件', '源类型 / 调整', '关联', '调整前', '调整后'], (offset) => readServicePeriodHistoryEvents(id, offset), eventRow),
  ]);
}
