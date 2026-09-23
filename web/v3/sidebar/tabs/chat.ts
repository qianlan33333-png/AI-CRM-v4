// @ts-nocheck
import type {
  ListSidebarChatActivityParams,
  SidebarChatActivityResponse,
  SidebarOtherStaffChatResponse,
} from "../../../src/api/generated/health.schemas";

/**
 * 聊天动态与其他客服聊天：当前后端未启用消息归档（cmd/aicrm 组合根挂的是
 * disabledCustomerChatActivity，且无 other-staff-chats 路由）。这里直接
 * 抛出诚实错误，由面板错误态呈现，不伪造空数据也不发注定失败的请求。
 */
const CHAT_ARCHIVE_DISABLED =
  "当前部署未启用聊天消息归档，聊天动态与其他客服聊天不可用；其余工作台能力不受影响。";

export async function loadChatActivity(
  _contextToken: string,
  _params?: ListSidebarChatActivityParams,
  _signal?: AbortSignal,
): Promise<SidebarChatActivityResponse> {
  throw new Error(CHAT_ARCHIVE_DISABLED);
}

export async function loadOtherStaffChats(
  _contextToken: string,
  _signal?: AbortSignal,
): Promise<SidebarOtherStaffChatResponse> {
  throw new Error(CHAT_ARCHIVE_DISABLED);
}
