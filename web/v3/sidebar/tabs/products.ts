// @ts-nocheck
import type {
  ListSidebarShareableProductsParams,
  SidebarShareableProduct,
  SidebarShareableProductResponse,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import { boundedLimitValue, LOCAL_READ_SAFETY } from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 productport.ProductOption 投影。 */
interface BackendProductOption {
  id: number;
  code: string;
  product_type: string;
  name: string;
  price_minor: number;
  currency: string;
}

/**
 * 可分享商品：直连后端 GET /api/sidebar/v2/products。本地商品选项投影
 * 没有描述与库存：description 置空、stock_quantity 缺省（UI 显示
 * 「库存未同步」）。public_path 指向仍然在线的公开购买页。
 */
export async function loadProducts(
  contextToken: string,
  params?: ListSidebarShareableProductsParams,
  signal?: AbortSignal,
): Promise<SidebarShareableProductResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 50);
  const response = await request(
    `/api/sidebar/v2/products?limit=${limit}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as { items?: BackendProductOption[] };
  const items = (Array.isArray(data.items) ? data.items : [])
    .filter((item) => typeof item.code === "string" && item.code !== "")
    .map((item): SidebarShareableProduct => {
      const kind =
        item.product_type === "service_period" ? "service_period" : "ordinary";
      return {
        kind,
        product_id: item.id,
        product_code: item.code,
        name: item.name,
        description: "",
        price_minor: item.price_minor,
        currency: item.currency,
        public_path: `/p/${kind}/${item.id}`,
      };
    });
  return { items, safety: { ...LOCAL_READ_SAFETY } };
}
