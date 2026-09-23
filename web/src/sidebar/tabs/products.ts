import {
  listSidebarShareableProducts,
  prepareSidebarImageTemporaryMedia,
} from "../../api/generated/p4-sidebar-send/p4-sidebar-send";
import type { ListSidebarShareableProductsParams } from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadProducts(
  contextToken: string,
  params?: ListSidebarShareableProductsParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarShareableProducts(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}

export async function prepareTemporaryImage(
  contextToken: string,
  imageId: number,
  idempotencyKey: string,
) {
  return unwrapGenerated(
    await prepareSidebarImageTemporaryMedia(
      imageId,
      sidebarScopedOptions(contextToken, {
        headers: { "Idempotency-Key": idempotencyKey },
      }),
    ),
  );
}
