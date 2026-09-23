// @ts-nocheck
import type {
  ListSidebarTimelineParams,
  SidebarTimelineResponse,
} from "../../../src/api/generated/health.schemas";
import { request } from "../../../src/api/transport";
import { boundedLimitValue, LOCAL_READ_SAFETY } from "./local-contract";
import { sidebarScopedOptions } from "./runtime";

/** 后端 customerport.TimelineItem 投影。 */
interface BackendTimelineItem {
  id: number;
  event_type: string;
  occurred_at: string;
}

/**
 * 时间线：直连后端 GET /api/sidebar/v2/timeline。后端 TimelinePage 没有
 * 游标分页，只读第一页；next_cursor 保持缺省，UI 因此不显示「加载更多」。
 */
export async function loadTimeline(
  contextToken: string,
  params?: ListSidebarTimelineParams,
  signal?: AbortSignal,
): Promise<SidebarTimelineResponse> {
  const limit = boundedLimitValue(params?.limit, 20, 50);
  const response = await request(
    `/api/sidebar/v2/timeline?limit=${limit}`,
    sidebarScopedOptions(contextToken, { signal }),
  );
  const data = (await response.json()) as { items?: BackendTimelineItem[] };
  return {
    items: (Array.isArray(data.items) ? data.items : []).map((item) => ({
      id: item.id,
      event_type: item.event_type,
      occurred_at: item.occurred_at,
    })),
    safety: { ...LOCAL_READ_SAFETY },
  };
}
