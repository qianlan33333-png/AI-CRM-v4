// @ts-nocheck
import type {
  ListSidebarOrdersParams,
  SidebarOrderItem,
  SidebarOrderResponse,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import {
  boundedLimitValue,
  LOCAL_READ_SAFETY,
  orderProviderLabel,
  orderStatusLabel,
} from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 orderport.Page + domain.Snapshot 投影。 */
interface BackendOrderSnapshot {
  created_at: string;
  merchant_order_no: string;
  amount: { amount_minor: number; currency: string };
  status: string;
  provider: string;
  items?: Array<{ product_code: string; product_name: string }>;
}

function mapOrder(snapshot: BackendOrderSnapshot): SidebarOrderItem {
  const line = Array.isArray(snapshot.items) ? snapshot.items[0] : undefined;
  const amountMinor = Number(snapshot.amount?.amount_minor) || 0;
  return {
    created_at: snapshot.created_at,
    merchant_order_no: snapshot.merchant_order_no ?? "",
    product_code: line?.product_code ?? "",
    product_name: line?.product_name ?? "",
    amount_yuan: (amountMinor / 100).toFixed(2),
    currency: snapshot.amount?.currency ?? "",
    status: snapshot.status ?? "",
    status_label: orderStatusLabel(snapshot.status ?? ""),
    provider: snapshot.provider ?? "",
    provider_label: orderProviderLabel(snapshot.provider ?? ""),
  };
}

/** 订单：直连后端 GET /api/sidebar/v2/orders（limit ≤ 50，支持 offset）。 */
export async function loadOrders(
  contextToken: string,
  params?: ListSidebarOrdersParams,
  signal?: AbortSignal,
): Promise<SidebarOrderResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 50);
  const offset = Math.max(0, Math.trunc(params?.offset ?? 0) || 0);
  const response = await request(
    `/api/sidebar/v2/orders?limit=${limit}&offset=${offset}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as {
    items?: BackendOrderSnapshot[];
    total?: number;
  };
  const items = (Array.isArray(data.items) ? data.items : []).map(mapOrder);
  const total = Number(data.total) || 0;
  return {
    items,
    total,
    limit,
    has_more: offset + items.length < total,
    safety: { ...LOCAL_READ_SAFETY },
  };
}
