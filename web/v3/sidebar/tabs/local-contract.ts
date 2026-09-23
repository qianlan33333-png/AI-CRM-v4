// @ts-nocheck
/**
 * 侧边栏工作台与本地后端（internal/sidebar）之间的真实契约适配。
 *
 * 生成客户端冻结的是 v2 契约；本地后端的投影字段更窄。本模块是唯一
 * 允许把后端响应整形为 UI 契约的位置：只映射后端真实存在的字段，
 * 无数据源的能力保持缺省或诚实报错，绝不伪造。
 */
import type { SidebarSafety } from "../../../src/api/generated/health.schemas";

/** 本地只读投影的固定安全包络：读路径不发生任何 Provider 外呼。 */
export const LOCAL_READ_SAFETY: SidebarSafety = {
  local_only: true,
  provider_execution_eligible: false,
  real_external_call_executed: false,
};

/** 后端列表端点的 limit 上限（internal/sidebar boundedLimit）。 */
export function boundedLimitValue(value: number | undefined, fallback: number, max: number): number {
  const raw = value === undefined ? fallback : Math.trunc(value);
  if (!Number.isFinite(raw) || raw < 1) return fallback;
  return Math.min(raw, max);
}

/** 周期订单成员引用：前端内部的确定性编码，承载后端 entitlement id。 */
export function encodeMemberRef(entitlementID: number): string {
  return `spm_${String(entitlementID).padStart(22, "0")}`;
}

export function decodeMemberRef(memberRef: string): number {
  const id = Number(memberRef.slice(4));
  return Number.isInteger(id) && id >= 1 ? id : 0;
}

const ORDER_STATUS_LABELS: Record<string, string> = {
  pending_payment: "待支付",
  paid: "已支付",
  partially_refunded: "部分退款",
  refunded: "已退款",
  cancelled: "已取消",
  payment_failed: "支付失败",
  closed: "已关闭",
};

const ORDER_PROVIDER_LABELS: Record<string, string> = {
  wechat_pay: "微信支付",
  wechat_shop: "微信小店",
  alipay: "支付宝",
};

export function orderStatusLabel(status: string): string {
  return ORDER_STATUS_LABELS[status] ?? status;
}

export function orderProviderLabel(provider: string): string {
  return ORDER_PROVIDER_LABELS[provider] ?? provider;
}
