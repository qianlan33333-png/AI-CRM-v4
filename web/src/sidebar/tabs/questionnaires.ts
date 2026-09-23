import { listSidebarQuestionnaires } from "../../api/generated/p4-sidebar-questionnaires/p4-sidebar-questionnaires";
import type { ListSidebarQuestionnairesParams } from "../../api/generated/health.schemas";
import { unwrapGenerated } from "../../api/transport";
import { sidebarScopedOptions } from "./runtime";

export async function loadQuestionnaires(
  contextToken: string,
  params?: ListSidebarQuestionnairesParams,
  signal?: AbortSignal,
) {
  return unwrapGenerated(
    await listSidebarQuestionnaires(
      params,
      sidebarScopedOptions(contextToken, { signal }),
    ),
  );
}
