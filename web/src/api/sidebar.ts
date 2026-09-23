import {
  bindSidebarPhone,
  bootstrapSidebar,
  getCompleteSidebarOAuthUrl,
  getSidebarAgentConfig,
  getSidebarWorkbench,
  getStartSidebarOAuthUrl,
  mintSidebarContext,
  updateSidebarProfile,
} from "./generated/p4-sidebar-core/p4-sidebar-core";
import type {
  BindSidebarPhoneBody,
  BootstrapSidebarBody,
  CompleteSidebarOAuthParams,
  ListSidebarChatActivityParams,
  ListSidebarMaterialsParams,
  ListSidebarOrdersParams,
  ListSidebarPeriodicOrdersParams,
  ListSidebarQuestionnairesParams,
  ListSidebarShareableProductsParams,
  ListSidebarTimelineParams,
  MintSidebarContextBody,
  SidebarAgentConfigSignature,
  SidebarBootstrapResponse,
  SidebarChatActivityResponse,
  SidebarContextResponse,
  SidebarMaterialResponse,
  SidebarOrderResponse,
  SidebarOtherStaffChatResponse,
  SidebarPeriodicOrderResponse,
  SidebarPeriodicRemarkResponse,
  SidebarPhoneBindingResponse,
  SidebarProfileUpdateResponse,
  SidebarQuestionnaireResponse,
  SidebarShareableProductResponse,
  SidebarTemporaryMediaResponse,
  SidebarThumbnailPendingResponse,
  SidebarTimelineResponse,
  SidebarWorkbenchResponse,
  StartSidebarOAuthParams,
  UpdateSidebarPeriodicRemarkBody,
  UpdateSidebarProfileBody,
} from "./generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from "./transport";

function scopedOptions(
  contextToken: string,
  init: RequestInit = {},
): RequestInit {
  return apiRequestOptions({
    ...init,
    headers: { ...init.headers, "X-Sidebar-Context-Token": contextToken },
  });
}

export function newSidebarIdempotencyKey(scope: string): string {
  const randomUUID = globalThis.crypto?.randomUUID?.();
  return randomUUID
    ? `${scope}-${randomUUID}`
    : `${scope}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export const sidebarApi = {
  mintContext: async (body: MintSidebarContextBody) =>
    unwrapGenerated(
      await mintSidebarContext(body, apiRequestOptions()),
    ) as SidebarContextResponse,
  bootstrap: async (body: BootstrapSidebarBody, signal?: AbortSignal) =>
    unwrapGenerated(
      await bootstrapSidebar(body, apiRequestOptions({ signal })),
    ) as SidebarBootstrapResponse,
  agentConfig: async (url: string) =>
    unwrapGenerated(
      await getSidebarAgentConfig({ url }, apiRequestOptions()),
    ) as SidebarAgentConfigSignature,
  oauthStartUrl: (params: StartSidebarOAuthParams) =>
    getStartSidebarOAuthUrl(params),
  oauthCallbackUrl: (params: CompleteSidebarOAuthParams) =>
    getCompleteSidebarOAuthUrl(params),
  workbench: async (contextToken: string) =>
    unwrapGenerated(
      await getSidebarWorkbench(scopedOptions(contextToken)),
    ) as SidebarWorkbenchResponse,
  profile: async (
    contextToken: string,
    body: UpdateSidebarProfileBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-profile"),
  ) =>
    unwrapGenerated(
      await updateSidebarProfile(
        body,
        scopedOptions(contextToken, {
          headers: { "Idempotency-Key": idempotencyKey },
        }),
      ),
    ) as SidebarProfileUpdateResponse,
  bindPhone: async (
    contextToken: string,
    body: BindSidebarPhoneBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-phone"),
  ) =>
    unwrapGenerated(
      await bindSidebarPhone(
        body,
        scopedOptions(contextToken, {
          headers: { "Idempotency-Key": idempotencyKey },
        }),
      ),
    ) as SidebarPhoneBindingResponse,

  timeline: async (
    contextToken: string,
    params?: ListSidebarTimelineParams,
    signal?: AbortSignal,
  ) => {
    const { loadTimeline } = await import("../sidebar/tabs/timeline");
    return (await loadTimeline(
      contextToken,
      params,
      signal,
    )) as SidebarTimelineResponse;
  },
  chatActivity: async (
    contextToken: string,
    params?: ListSidebarChatActivityParams,
    signal?: AbortSignal,
  ) => {
    const { loadChatActivity } = await import("../sidebar/tabs/chat");
    return (await loadChatActivity(
      contextToken,
      params,
      signal,
    )) as SidebarChatActivityResponse;
  },
  otherStaffChats: async (contextToken: string, signal?: AbortSignal) => {
    const { loadOtherStaffChats } = await import("../sidebar/tabs/chat");
    return (await loadOtherStaffChats(
      contextToken,
      signal,
    )) as SidebarOtherStaffChatResponse;
  },
  questionnaires: async (
    contextToken: string,
    params?: ListSidebarQuestionnairesParams,
    signal?: AbortSignal,
  ) => {
    const { loadQuestionnaires } =
      await import("../sidebar/tabs/questionnaires");
    return (await loadQuestionnaires(
      contextToken,
      params,
      signal,
    )) as SidebarQuestionnaireResponse;
  },
  orders: async (
    contextToken: string,
    params?: ListSidebarOrdersParams,
    signal?: AbortSignal,
  ) => {
    const { loadOrders } = await import("../sidebar/tabs/orders");
    return (await loadOrders(
      contextToken,
      params,
      signal,
    )) as SidebarOrderResponse;
  },
  periodicOrders: async (
    contextToken: string,
    params?: ListSidebarPeriodicOrdersParams,
    signal?: AbortSignal,
  ) => {
    const { loadPeriodicOrders } =
      await import("../sidebar/tabs/periodic-orders");
    return (await loadPeriodicOrders(
      contextToken,
      params,
      signal,
    )) as SidebarPeriodicOrderResponse;
  },
  updateRemark: async (
    contextToken: string,
    serviceProductId: number,
    memberRef: string,
    body: UpdateSidebarPeriodicRemarkBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-periodic-remark"),
  ) => {
    const { savePeriodicRemark } =
      await import("../sidebar/tabs/periodic-orders");
    return (await savePeriodicRemark(
      contextToken,
      serviceProductId,
      memberRef,
      body,
      idempotencyKey,
    )) as SidebarPeriodicRemarkResponse;
  },
  materials: async (
    contextToken: string,
    params?: ListSidebarMaterialsParams,
    signal?: AbortSignal,
  ) => {
    const { loadMaterials } = await import("../sidebar/tabs/materials");
    return (await loadMaterials(
      contextToken,
      params,
      signal,
    )) as SidebarMaterialResponse;
  },
  shareableProducts: async (
    contextToken: string,
    params?: ListSidebarShareableProductsParams,
    signal?: AbortSignal,
  ) => {
    const { loadProducts } = await import("../sidebar/tabs/products");
    return (await loadProducts(
      contextToken,
      params,
      signal,
    )) as SidebarShareableProductResponse;
  },
  prepareTemporaryImage: async (
    contextToken: string,
    imageId: number,
    idempotencyKey: string,
  ) => {
    const { prepareTemporaryImage } = await import("../sidebar/tabs/products");
    return (await prepareTemporaryImage(
      contextToken,
      imageId,
      idempotencyKey,
    )) as SidebarTemporaryMediaResponse;
  },
  thumbnail: async (
    contextToken: string,
    imageId: number,
    signal?: AbortSignal,
  ) => {
    const { loadThumbnailStatus } = await import("../sidebar/tabs/materials");
    return (await loadThumbnailStatus(
      contextToken,
      imageId,
      signal,
    )) as SidebarThumbnailPendingResponse;
  },
  thumbnailPreview: async (
    contextToken: string,
    imageId: number,
    signal?: AbortSignal,
  ) => {
    const { loadThumbnailPreview } = await import("../sidebar/tabs/materials");
    return loadThumbnailPreview(contextToken, imageId, signal);
  },
};
