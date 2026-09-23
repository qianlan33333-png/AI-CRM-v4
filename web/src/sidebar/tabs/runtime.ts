import { apiRequestOptions } from "../../api/transport";

export function sidebarScopedOptions(
  contextToken: string,
  init: RequestInit = {},
): RequestInit {
  return apiRequestOptions({
    ...init,
    headers: { ...init.headers, "X-Sidebar-Context-Token": contextToken },
  });
}
