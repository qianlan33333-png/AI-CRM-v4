// @ts-nocheck
import type {
  ListSidebarMaterialsParams,
  SidebarMaterialItem,
  SidebarMaterialResponse,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import { boundedLimitValue, LOCAL_READ_SAFETY } from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 mediaport.ImageListItem 投影（字段与 UI 契约同名）。 */
interface BackendImageListItem {
  id: number;
  name: string;
  file_name: string;
  mime_type: string;
  file_size: number;
  description: string;
  tags: string[];
  category: string;
  width: number;
  height: number;
  updated_at: string;
}

function mapMaterial(item: BackendImageListItem): SidebarMaterialItem {
  return {
    id: item.id,
    name: item.name ?? "",
    file_name: item.file_name ?? "",
    mime_type: item.mime_type ?? "",
    file_size: item.file_size,
    description: item.description ?? "",
    tags: Array.isArray(item.tags) ? item.tags : [],
    category: item.category ?? "",
    width: item.width,
    height: item.height,
    updated_at: item.updated_at,
    // 缩略图经 /api/sidebar/v2/materials/{id}/variants/thumb_320 异步加载，
    // 初始状态恒为 pending。
    thumbnail_status: "pending",
  };
}

/** 素材列表：直连后端 GET /api/sidebar/v2/materials（limit/offset/q/category/tags）。 */
export async function loadMaterials(
  contextToken: string,
  params?: ListSidebarMaterialsParams,
  signal?: AbortSignal,
): Promise<SidebarMaterialResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 100);
  const offset = Math.max(0, Math.trunc(params?.offset ?? 0) || 0);
  const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
  if (params?.q) query.set("q", params.q);
  if (params?.category) query.set("category", params.category);
  if (params?.tags) query.set("tags", params.tags);
  const response = await request(
    `/api/sidebar/v2/materials?${query.toString()}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as {
    items?: BackendImageListItem[];
    total?: number;
  };
  return {
    items: (Array.isArray(data.items) ? data.items : []).map(mapMaterial),
    total: Number(data.total) || 0,
    limit,
    offset,
    // 快捷关键词需要额外聚合接口，当前后端未提供，保持空列表。
    quick_keywords: [],
    safety: { ...LOCAL_READ_SAFETY },
  };
}

/**
 * 缩略图预览：直连侧边栏专属的只读变体路由（后台 /api/admin/image-library
 * 变体路由需要管理员会话，侧边栏观看者不可用）。
 */
export async function loadThumbnailPreview(
  contextToken: string,
  imageId: number,
  signal?: AbortSignal,
): Promise<Blob> {
  const response = await request(
    `/api/sidebar/v2/materials/${imageId}/variants/thumb_320`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  return response.blob();
}
