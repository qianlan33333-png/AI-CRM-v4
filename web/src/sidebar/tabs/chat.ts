import {
  listSidebarChatActivity,
  listSidebarOtherStaffChats,
} from "../../api/generated/p4-sidebar-activity/p4-sidebar-activity";
import type { ListSidebarChatActivityParams } from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadChatActivity(
  contextToken: string,
  params?: ListSidebarChatActivityParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarChatActivity(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}

export async function loadOtherStaffChats(
  contextToken: string,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarOtherStaffChats(
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}
