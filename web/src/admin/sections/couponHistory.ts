import {
  readCouponHistoryDefinitions, readCouponHistoryClaims, readCouponHistoryRedemptions,
  type CouponHistoryDefinition, type CouponHistoryClaim, type CouponHistoryRedemption, type CouponHistoryPage,
} from '../../api/couponHistory';
import { esc } from './util';

const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const text = (value: string | number | null): string => value === null ? '未记录' : value === '' ? '（空）' : esc(value);
const money = (minor: number): string => `¥${(minor / 100).toFixed(2)}（${minor} 分）`;
const row = (...cells: string[]): string => `<tr>${cells.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`;

function definitionRow(item: CouponHistoryDefinition): string {
  const validity = item.validity_mode === 'relative_days' ? `领取后 ${text(item.relative_validity_days)} 天`
    : `${text(item.use_starts_at)} → ${text(item.use_ends_at)}`;
  return row(`${text(item.name)}<br>V2 券 #${item.id} / 源券 #${item.source_coupon_id}<br>原状态：${text(item.original_status)}<br>V2：archived`,
    `${money(item.discount_amount_total)} CNY<br>总量 ${item.total_issue_limit} / 已发 ${item.issued_count}<br>单用户限领 ${item.per_user_issue_limit}`,
    `领取：${text(item.claim_starts_at)} → ${text(item.claim_ends_at)}<br>有效期：${validity}<br>首次领取：${text(item.first_claim_at)}`,
    `${item.target_refs.map(text).join('<br>')}<br>说明：${text(item.instructions)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`,
    `<a data-history-coupon="${item.id}" href="couponData.html?history=1&id=${item.id}">查看历史领取 / 核销</a>`);
}

function claimRow(item: CouponHistoryClaim): string {
  return row(`历史领取 #${item.id} / 源领取 #${item.source_claim_id}<br>编号：${text(item.claim_no)}<br>${item.customer_id === null ? '未关联客户' : `V2 客户 #${item.customer_id}`}`,
    `原状态：${text(item.status)}<br>${money(item.discount_amount_total)} CNY`,
    `${text(item.valid_from)} → ${text(item.valid_until)}<br>领取：${text(item.claimed_at)}`,
    `预占：${text(item.reserved_at)}<br>使用：${text(item.consumed_at)}<br>过期：${text(item.expired_at)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`);
}

function redemptionRow(item: CouponHistoryRedemption): string {
  return row(`历史核销 #${item.id} / 源核销 #${item.source_redemption_id}<br>历史领取 #${item.claim_history_id} / 源领取 #${item.source_claim_id}<br>${item.order_id === null ? '未关联订单' : `V2 订单 #${item.order_id}`}<br>源订单 #${item.source_order_id}<br>原订单号：${text(item.out_trade_no)}`,
    `原状态：${text(item.status)}<br>原价：${money(item.original_amount_total)}<br>减免：${money(item.discount_amount_total)}<br>应付：${money(item.payable_amount_total)} CNY`,
    `预占：${text(item.reserved_at)}<br>预占截止：${text(item.reserved_until)}<br>使用：${text(item.consumed_at)}<br>释放：${text(item.released_at)}`,
    `原释放原因：${text(item.release_reason)}<br>创建：${text(item.created_at)}<br>更新：${text(item.updated_at)}`);
}

async function mountPage<T>(section: HTMLElement, title: string, columns: string[], read: (offset: number) => Promise<CouponHistoryPage<T>>, renderRow: (item: T) => string): Promise<void> {
  const load = async (offset: number): Promise<void> => {
    section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page = await read(offset);
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><div style="overflow:auto"><table style="width:100%;min-width:900px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085">${columns.map((column) => `<th style="${cell}">${column}</th>`).join('')}</tr></thead><tbody>${page.items.map(renderRow).join('') || `<tr><td colspan="${columns.length}" style="${cell}">暂无 V1 历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px;display:flex;align-items:center;justify-content:space-between;gap:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span><div style="display:flex;gap:8px"><button data-history-previous style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      section.querySelector('[data-history-previous]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      section.querySelector('[data-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重新读取</button>`;
      section.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load(0);
}

export async function mountCouponHistory(stage: HTMLElement, rawID?: string): Promise<void> {
  const id = rawID === undefined ? undefined : Number(rawID);
  if (rawID !== undefined && (!/^[1-9]\d*$/.test(rawID) || !Number.isSafeInteger(id))) throw new Error('V1 历史优惠券 ID 无效');
  stage.innerHTML = `<div style="flex:1;min-height:0;overflow:auto;padding:20px;display:grid;grid-template-columns:minmax(0,1fr);gap:16px;align-content:start"><header><a href="coupons.html${id === undefined ? '' : '?history=1'}">${id === undefined ? '返回优惠券管理' : '返回 V1 历史列表'}</a><h1 style="font-size:20px;margin:12px 0 8px">V1 优惠券历史（只读）${id === undefined ? '' : ` · V2 券 #${id}`}</h1><p style="color:#8F5A16;margin:0;line-height:1.7">仅展示原始快照状态、金额和日期，不代表当前可用权益；未触发发券、领取、核销、支付、退款或 Provider 外部效果。日期按服务端原样展示（含时区），不重算历史状态。</p></header>${id === undefined ? '<section id="coupon-history-definitions"></section>' : '<section id="coupon-history-claims"></section><section id="coupon-history-redemptions"></section>'}</div>`;
  if (id === undefined) {
    await mountPage(stage.querySelector<HTMLElement>('#coupon-history-definitions')!, '历史券定义', ['定义 / 原状态', '原金额 / 发放数量', '源日期 / 有效期', '商品关联 / 原说明', '只读入口'], readCouponHistoryDefinitions, definitionRow);
    return;
  }
  await Promise.all([
    mountPage(stage.querySelector<HTMLElement>('#coupon-history-claims')!, '历史领取', ['历史引用 / 客户', '原状态 / 金额', '源有效期 / 领取时间', '源生命周期日期'], (offset) => readCouponHistoryClaims(id, offset), claimRow),
    mountPage(stage.querySelector<HTMLElement>('#coupon-history-redemptions')!, '历史核销（不执行）', ['历史引用 / 订单', '原状态 / 原金额', '源生命周期日期', '原原因 / 日期'], (offset) => readCouponHistoryRedemptions(id, offset), redemptionRow),
  ]);
}
