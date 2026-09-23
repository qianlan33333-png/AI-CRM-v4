import {
  listSidebarPeriodicOrders,
  updateSidebarPeriodicRemark,
} from "../../api/generated/p4-sidebar-orders/p4-sidebar-orders";
import type {
  ListSidebarPeriodicOrdersParams,
  UpdateSidebarPeriodicRemarkBody,
} from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadPeriodicOrders(
  contextToken: string,
  params?: ListSidebarPeriodicOrdersParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarPeriodicOrders(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}

export async function savePeriodicRemark(
  contextToken: string,
  serviceProductId: number,
  memberRef: string,
  body: UpdateSidebarPeriodicRemarkBody,
  idempotencyKey: string,
) {
  return unwrapGenerated(
    await updateSidebarPeriodicRemark(
      serviceProductId,
      memberRef,
      body,
      sidebarScopedOptions(contextToken, {
        headers: { "Idempotency-Key": idempotencyKey },
      }),
    ),
  );
}
