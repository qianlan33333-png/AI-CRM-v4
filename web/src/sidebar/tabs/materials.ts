import {
  getGetSidebarMaterialThumbnailPreviewUrl,
  getSidebarMaterialThumbnailStatus,
  listSidebarMaterials,
} from "../../api/generated/p4-sidebar-materials/p4-sidebar-materials";
import type { ListSidebarMaterialsParams } from "../../api/generated/health.schemas";
import { request, unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadMaterials(
  contextToken: string,
  params?: ListSidebarMaterialsParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarMaterials(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}

export async function loadThumbnailStatus(
  contextToken: string,
  imageId: number,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await getSidebarMaterialThumbnailStatus(
      imageId,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}

export async function loadThumbnailPreview(
  contextToken: string,
  imageId: number,
  signal?: AbortSignal,
) {
  const response = await request(
    getGetSidebarMaterialThumbnailPreviewUrl(imageId),
    sidebarScopedOptions(contextToken, { signal }),
  );
  return response.blob();
}
