// @ts-nocheck
import type {
  ListSidebarPeriodicOrdersParams,
  SidebarPeriodicOrderResponse,
  SidebarPeriodicRemarkResponse,
  SidebarServicePeriodMember,
  UpdateSidebarPeriodicRemarkBody,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import {
  boundedLimitValue,
  decodeMemberRef,
  encodeMemberRef,
  LOCAL_READ_SAFETY,
} from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 orderport.Entitlement 投影。 */
interface BackendEntitlement {
  id: number;
  customer_id?: number;
  service_product_id: number;
  title?: string;
  last_order_id?: number;
  status: string;
  start_at: string;
  end_at: string;
  remark?: string;
  alliance?: string | null;
  version: number;
  updated_at: string;
}

/**
 * 周期订单成员投影。后端 Entitlement 没有 created_at 与 manual/paid_order
 * 枚举：created_at 以 updated_at 兜底（投影限制），source 由是否存在
 * 来源订单（last_order_id）推导。member_ref 是前端内部的确定性编码。
 */
function mapMember(row: BackendEntitlement): SidebarServicePeriodMember {
  return {
    member_ref: encodeMemberRef(row.id),
    service_product_id: row.service_product_id,
    customer_id: row.customer_id ?? 0,
    state: (row.status || "expired") as SidebarServicePeriodMember["state"],
    source: row.last_order_id ? "paid_order" : "manual",
    starts_at: row.start_at,
    expires_at: row.end_at || undefined,
    remark: row.remark || undefined,
    alliance: row.alliance ?? undefined,
    version: row.version,
    created_at: row.updated_at,
    updated_at: row.updated_at,
  };
}

/**
 * 周期订单：直连后端 GET /api/sidebar/v2/periodic-orders。后端
 * ListCustomerEntitlements 没有 offset 分页，一页最多 50 条；
 * has_more 恒为 false，超出部分由工作台计数提示。
 */
export async function loadPeriodicOrders(
  contextToken: string,
  params?: ListSidebarPeriodicOrdersParams,
  signal?: AbortSignal,
): Promise<SidebarPeriodicOrderResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 50);
  const response = await request(
    `/api/sidebar/v2/periodic-orders?limit=${limit}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as { items?: BackendEntitlement[] };
  const items = (Array.isArray(data.items) ? data.items : []).map(mapMember);
  return {
    items,
    limit,
    offset: 0,
    has_more: false,
    safety: { ...LOCAL_READ_SAFETY },
  };
}

/** 备注保存：member_ref 解码为 entitlement id 后直连后端 remark 端点。 */
export async function savePeriodicRemark(
  contextToken: string,
  _serviceProductId: number,
  memberRef: string,
  body: UpdateSidebarPeriodicRemarkBody,
  idempotencyKey: string,
): Promise<SidebarPeriodicRemarkResponse> {
  const entitlementID = decodeMemberRef(memberRef);
  if (!entitlementID) throw new Error("周期订单成员引用无效。");
  const response = await request(
    `/api/sidebar/v2/periodic-orders/${entitlementID}/remark`,
    sidebarScopedOptions(contextToken, {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": idempotencyKey,
      },
      body: JSON.stringify({
        remark: body.remark,
        expected_version: body.expected_version,
      }),
    }),
  );
  const member = mapMember((await response.json()) as BackendEntitlement);
  return { member, safety: { ...LOCAL_READ_SAFETY } };
}
