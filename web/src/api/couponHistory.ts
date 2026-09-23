import {
  listCouponHistoryDefinitions,
  listCouponHistoryClaims,
  listCouponHistoryRedemptions,
} from "./generated/p4-coupon-history/p4-coupon-history";
import {
  type CouponHistoryDefinition,
  type CouponHistoryClaim,
  type CouponHistoryRedemption,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type { CouponHistoryDefinition, CouponHistoryClaim, CouponHistoryRedemption };
export type CouponHistoryPage<T> = { items: T[]; total: number; limit: number; offset: number };
type Row = Record<string, unknown>;
const invalid = (): never => { throw new Error('V1 优惠券历史响应不完整，未显示历史数据'); };
const object = (value: unknown): Row => value && typeof value === 'object' && !Array.isArray(value) ? value as Row : invalid();
const integer = (value: unknown, minimum = 0): boolean => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
const date = (value: unknown): boolean => typeof value === 'string' && Number.isFinite(Date.parse(value));

function fields(row: Row, texts: string[], amounts: string[], ids: string[], nullableIDs: string[], dates: string[], nullableDates: string[]): void {
  if (texts.some((key) => typeof row[key] !== 'string') || amounts.some((key) => !integer(row[key])) || ids.some((key) => !integer(row[key], 1)) ||
    nullableIDs.some((key) => row[key] !== null && !integer(row[key], 1)) || dates.some((key) => !date(row[key])) ||
    nullableDates.some((key) => row[key] !== null && !date(row[key])) || row.currency !== 'CNY') invalid();
}

function definition(value: unknown): CouponHistoryDefinition {
  const row = object(value);
  fields(row, ['name', 'original_status', 'validity_mode', 'instructions'], ['discount_amount_total', 'total_issue_limit', 'per_user_issue_limit', 'issued_count'],
    ['id', 'source_coupon_id', 'created_by', 'updated_by', 'version'], [], ['claim_starts_at', 'claim_ends_at', 'created_at', 'updated_at'], ['use_starts_at', 'use_ends_at', 'first_claim_at']);
  if (row.status !== 'archived' || row.availability_status !== 'archived' || row.history_only !== true ||
    (row.relative_validity_days !== null && !integer(row.relative_validity_days, 1)) || !Array.isArray(row.target_refs) ||
    !row.target_refs.length || row.target_refs.some((ref) => typeof ref !== 'string' || !/^standard_product:[1-9]\d*$/.test(ref))) invalid();
  return row as unknown as CouponHistoryDefinition;
}

function claim(value: unknown): CouponHistoryClaim {
  const row = object(value);
  fields(row, ['claim_no', 'status'], ['discount_amount_total'], ['id', 'source_claim_id', 'source_coupon_id', 'coupon_id'], ['customer_id'],
    ['valid_from', 'valid_until', 'claimed_at', 'created_at', 'updated_at'], ['reserved_at', 'consumed_at', 'expired_at']);
  return row as unknown as CouponHistoryClaim;
}

function redemption(value: unknown): CouponHistoryRedemption {
  const row = object(value);
  fields(row, ['out_trade_no', 'status', 'release_reason'], ['original_amount_total', 'discount_amount_total', 'payable_amount_total'],
    ['id', 'source_redemption_id', 'source_claim_id', 'source_order_id', 'claim_history_id'], ['order_id'], ['reserved_until', 'reserved_at', 'created_at', 'updated_at'], ['consumed_at', 'released_at']);
  return row as unknown as CouponHistoryRedemption;
}

function params(offset: number, limit: number, couponID?: number): { limit: number; offset: number } {
  if (!integer(offset) || offset > 2147483647 || !integer(limit, 1) || limit > 100 || (couponID !== undefined && !integer(couponID, 1))) {
    throw new Error('V1 优惠券历史分页或优惠券 ID 无效');
  }
  return { limit, offset };
}

function page<T extends { id: number; coupon_id?: number }>(value: unknown, offset: number, limit: number, convert: (value: unknown) => T, couponID?: number): CouponHistoryPage<T> {
  const body = object(value);
  if (body.source !== 'v1_history' || body.read_only !== true || body.real_external_call_executed !== false || !Array.isArray(body.items) ||
    !integer(body.total) || body.limit !== limit || body.offset !== offset || (couponID !== undefined && body.coupon_id !== couponID)) invalid();
  const items = (body.items as unknown[]).map(convert);
  const total = body.total as number;
  if (items.length !== Math.min(limit, Math.max(0, total - offset)) || new Set(items.map((item) => item.id)).size !== items.length ||
    (couponID !== undefined && items.some((item) => item.coupon_id !== undefined && item.coupon_id !== couponID))) invalid();
  return { items, total, limit, offset };
}

export async function readCouponHistoryDefinitions(offset = 0, limit = 20): Promise<CouponHistoryPage<CouponHistoryDefinition>> {
  const response = await listCouponHistoryDefinitions(params(offset, limit), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, definition);
}
export async function readCouponHistoryClaims(couponID: number, offset = 0, limit = 20): Promise<CouponHistoryPage<CouponHistoryClaim>> {
  const response = await listCouponHistoryClaims(couponID, params(offset, limit, couponID), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, claim, couponID);
}
export async function readCouponHistoryRedemptions(couponID: number, offset = 0, limit = 20): Promise<CouponHistoryPage<CouponHistoryRedemption>> {
  const response = await listCouponHistoryRedemptions(couponID, params(offset, limit, couponID), apiRequestOptions());
  return page(unwrapGenerated(response), offset, limit, redemption, couponID);
}
