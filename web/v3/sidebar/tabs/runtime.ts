// @ts-nocheck
import { apiRequestOptions } from "../../../src/api/transport";

export function sidebarScopedOptions(
  contextToken: string,
  init: RequestInit = {},
): RequestInit {
  return apiRequestOptions({
    ...init,
    headers: { ...init.headers, "X-Sidebar-Context-Token": contextToken },
  });
}
