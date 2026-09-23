import { listSidebarTimeline } from "../../api/generated/p4-sidebar-activity/p4-sidebar-activity";
import type { ListSidebarTimelineParams } from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadTimeline(
  contextToken: string,
  params?: ListSidebarTimelineParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarTimeline(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}
