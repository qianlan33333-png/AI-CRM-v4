import { listSidebarOrders } from "../../api/generated/p4-sidebar-orders/p4-sidebar-orders";
import type { ListSidebarOrdersParams } from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadOrders(
  contextToken: string,
  params?: ListSidebarOrdersParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarOrders(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}
