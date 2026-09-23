/**
 * 后台兼容页面加载器：仅在对应页面被动态加载。
 *  - 富交互页（雷达 / AI 助手 / 漏斗）→ sections/* 独立模块（真实 DOM + AdminApi）
 *  - 其余 28 屏 → mini-runtime 模板 + AdminController
 * 每页 HTML 由 scripts/build.mjs 生成（静态导航 shell）。
 */
import { mount } from "../shared/ui/runtime";
import { initFeedback } from "../shared/ui/feedback";
import { api } from "../shared/api/client";
import { AdminController } from "./controller";
import { surveyUnresolvedHistoryHttp } from "../api/surveyUnresolvedHistoryHttp";

type DeferredFunction = (...args: never[]) => unknown;

function deferredModule<T extends DeferredFunction>(
  loader: () => Promise<Record<string, unknown>>,
  exportName: string,
): T {
  return ((...args: Parameters<T>) =>
    loader().then((module) => (module[exportName] as T)(...args))) as T;
}

const mountRadar = deferredModule<
  (typeof import("./sections/radar"))["mountRadar"]
>(() => import("./sections/radar"), "mountRadar");
const mountAiAssistant = deferredModule<
  (typeof import("./sections/aiAssistant"))["mountAiAssistant"]
>(() => import("./sections/aiAssistant"), "mountAiAssistant");
const mountFunnelGrid = deferredModule<
  (typeof import("./sections/funnelGrid"))["mountFunnelGrid"]
>(() => import("./sections/funnelGrid"), "mountFunnelGrid");
const mountHXCHistory = deferredModule<
  (typeof import("./sections/hxcHistory"))["mountHXCHistory"]
>(() => import("./sections/hxcHistory"), "mountHXCHistory");
const mountCampaignWorkspace = deferredModule<
  (typeof import("./sections/campaigns"))["mountCampaignWorkspace"]
>(() => import("./sections/campaigns"), "mountCampaignWorkspace");
const mountCampaignHistory = deferredModule<
  (typeof import("./sections/campaignHistory"))["mountCampaignHistory"]
>(() => import("./sections/campaignHistory"), "mountCampaignHistory");
const mountAdminAccess = deferredModule<
  (typeof import("./sections/adminAccess"))["mountAdminAccess"]
>(() => import("./sections/adminAccess"), "mountAdminAccess");
const mountSetupWizard = deferredModule<
  (typeof import("./sections/setupWizard"))["mountSetupWizard"]
>(() => import("./sections/setupWizard"), "mountSetupWizard");
const mountGroupOpsHistory = deferredModule<
  (typeof import("./sections/groupOpsHistory"))["mountGroupOpsHistory"]
>(() => import("./sections/groupOpsHistory"), "mountGroupOpsHistory");
const mountServicePeriodHistory = deferredModule<
  (typeof import("./sections/servicePeriodHistory"))["mountServicePeriodHistory"]
>(() => import("./sections/servicePeriodHistory"), "mountServicePeriodHistory");
const mountCouponHistory = deferredModule<
  (typeof import("./sections/couponHistory"))["mountCouponHistory"]
>(() => import("./sections/couponHistory"), "mountCouponHistory");
const mountMessageHistory = deferredModule<
  (typeof import("./sections/messageHistory"))["mountMessageHistory"]
>(() => import("./sections/messageHistory"), "mountMessageHistory");
const mountAudienceHistory = deferredModule<
  (typeof import("./sections/audienceHistory"))["mountAudienceHistory"]
>(() => import("./sections/audienceHistory"), "mountAudienceHistory");
const renderLegacyMarketingHistory = deferredModule<
  (typeof import("./sections/legacyMarketingHistory"))["renderLegacyMarketingHistory"]
>(
  () => import("./sections/legacyMarketingHistory"),
  "renderLegacyMarketingHistory",
);
const mountProfileCatalogHistory = deferredModule<
  (typeof import("./sections/profileCatalogHistory"))["mountProfileCatalogHistory"]
>(
  () => import("./sections/profileCatalogHistory"),
  "mountProfileCatalogHistory",
);
const mountAutomationHistory = deferredModule<
  (typeof import("./sections/automationHistory"))["mountAutomationHistory"]
>(() => import("./sections/automationHistory"), "mountAutomationHistory");
const mountRadarMarketingHistory = deferredModule<
  (typeof import("./sections/radarMarketingHistory"))["mountRadarMarketingHistory"]
>(
  () => import("./sections/radarMarketingHistory"),
  "mountRadarMarketingHistory",
);
const mountMemberGridHistory = deferredModule<
  (typeof import("./sections/memberGridHistory"))["mountMemberGridHistory"]
>(() => import("./sections/memberGridHistory"), "mountMemberGridHistory");
const mountContactHistory = deferredModule<
  (typeof import("./sections/contactHistory"))["mountContactHistory"]
>(() => import("./sections/contactHistory"), "mountContactHistory");
const mountSurveyUnresolvedHistory = deferredModule<
  (typeof import("./sections/surveyUnresolvedHistory"))["mountSurveyUnresolvedHistory"]
>(
  () => import("./sections/surveyUnresolvedHistory"),
  "mountSurveyUnresolvedHistory",
);
const mountStaticHistory = deferredModule<
  (typeof import("./sections/staticHistory"))["mountStaticHistory"]
>(() => import("./sections/staticHistory"), "mountStaticHistory");
const mountCustomerStateHistory = deferredModule<
  (typeof import("./sections/customerStateHistory"))["mountCustomerStateHistory"]
>(() => import("./sections/customerStateHistory"), "mountCustomerStateHistory");
const mountMarketingStateHistory = deferredModule<
  (typeof import("./sections/marketingStateHistory"))["mountMarketingStateHistory"]
>(
  () => import("./sections/marketingStateHistory"),
  "mountMarketingStateHistory",
);
const mountBroadcastJobHistory = deferredModule<
  (typeof import("./sections/broadcastJobHistory"))["mountBroadcastJobHistory"]
>(() => import("./sections/broadcastJobHistory"), "mountBroadcastJobHistory");
const mountOutboundTaskHistory = deferredModule<
  (typeof import("./sections/outboundTaskHistory"))["mountOutboundTaskHistory"]
>(() => import("./sections/outboundTaskHistory"), "mountOutboundTaskHistory");
const mountWeComContactHistory = deferredModule<
  (typeof import("./sections/wecomContactHistory"))["mountWeComContactHistory"]
>(() => import("./sections/wecomContactHistory"), "mountWeComContactHistory");
const mountInvalidSourceHistory = deferredModule<
  (typeof import("./sections/invalidSourceHistory"))["mountInvalidSourceHistory"]
>(() => import("./sections/invalidSourceHistory"), "mountInvalidSourceHistory");
const mountDeferredIdentityHistory = deferredModule<
  (typeof import("./sections/deferredIdentityHistory"))["mountDeferredIdentityHistory"]
>(
  () => import("./sections/deferredIdentityHistory"),
  "mountDeferredIdentityHistory",
);


function showLoadError(stage: HTMLElement, error: unknown): void {
  stage.innerHTML = `<div style="margin:32px;padding:24px;border:1px solid #F2B8B5;border-radius:8px;color:#D83931;background:#FFF1F0">${error instanceof Error ? error.message : "页面数据读取失败"}</div>`;
}

function boot(): void {
  const page = document.body.getAttribute("data-page") || "customers";
  const stage = document.getElementById("stage");
  if (!stage) return;

  const historyQuery = new URLSearchParams(location.search);
  if (page === "config" && historyQuery.has("invalid_source_history")) {
    void mountInvalidSourceHistory(stage).catch(() => {
      stage.innerHTML =
        '<p role="alert">异常源历史读取失败；未修改当前业务。</p>';
    });
    return;
  }
  if (
    page === "config" &&
    historyQuery.get("deferred_identity_history") === "1"
  ) {
    void mountDeferredIdentityHistory(stage, {
      kind: historyQuery.get("history_kind") ?? undefined,
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }
  if (
    page === "automation" &&
    historyQuery.get("outbound_task_history") === "1"
  ) {
    void mountOutboundTaskHistory(stage, {
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch(() => {
      stage.innerHTML =
        '<p role="alert">外发任务历史读取失败；未创建或发送任务。</p>';
    });
    return;
  }
  if (
    page === "questionnaires" &&
    historyQuery.get("unresolved_history") === "1"
  ) {
    void mountSurveyUnresolvedHistory(stage, surveyUnresolvedHistoryHttp, {
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }
  if (
    page === "automation" &&
    historyQuery.get("legacy_marketing_history") === "1"
  ) {
    void renderLegacyMarketingHistory(stage).catch(() => {
      stage.innerHTML =
        '<section data-legacy-marketing-history><h1>V1 旧版营销历史（只读）</h1><p role="alert">历史数据读取失败；未更改当前分层。</p></section>';
    });
    return;
  }
  if (
    page === "automation" &&
    historyQuery.get("broadcast_job_history") === "1"
  ) {
    void mountBroadcastJobHistory(stage, {
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch(() => {
      stage.innerHTML =
        '<p role="alert">群发任务历史读取失败；未创建或发送任务。</p>';
    });
    return;
  }
  if (
    page === "config" &&
    historyQuery.get("marketing_state_history") === "1"
  ) {
    void mountMarketingStateHistory(stage).catch(() => {
      stage.innerHTML =
        '<p role="alert">营销状态历史读取失败；未进入当前配置。</p>';
    });
    return;
  }
  if (page === "config" && historyQuery.get("customer_state_history") === "1") {
    void mountCustomerStateHistory(stage).catch(() => {
      stage.innerHTML =
        '<p role="alert">客户状态历史读取失败；未进入当前配置。</p>';
    });
    return;
  }
  if (page === "config" && historyQuery.get("static_history") === "1") {
    void mountStaticHistory(stage).catch(() => {
      stage.innerHTML =
        '<p role="alert">静态历史读取失败；未进入当前配置。</p>';
    });
    return;
  }
  if (page === "funnel" && historyQuery.get("hxc_history") === "1") {
    void mountHXCHistory(stage, {
      kind: historyQuery.get("history_kind") ?? undefined,
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }
  if (
    (page === "radar" && historyQuery.get("click_history") === "1") ||
    (page === "ai" && historyQuery.get("marketing_config_history") === "1")
  ) {
    void mountRadarMarketingHistory(stage, {
      kind:
        page === "radar"
          ? "radar_click"
          : (historyQuery.get("history_kind") ?? "marketing_config"),
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch(() => {
      stage.innerHTML =
        '<p role="alert">历史参数或读取失败；未进入当前业务。</p>';
    });
    return;
  }
  if (page === "config" && historyQuery.get("automation_history") === "1") {
    void mountAutomationHistory(stage, {
      kind: historyQuery.get("history_kind") ?? undefined,
      historyID: historyQuery.get("history_id") ?? undefined,
    }).catch(() => {
      stage.innerHTML =
        '<section data-automation-history><h1>V1 自动化历史（只读）</h1><p role="alert">历史参数或读取失败；未进入当前配置。</p></section>';
    });
    return;
  }

  const rawId = Number(new URLSearchParams(location.search).get("id") || "");
  const id = rawId || undefined;

  const historyParams = new URLSearchParams(location.search);
  if (page === "config" && historyParams.get("wecom_contact_history") === "1") {
    void mountWeComContactHistory(stage, {
      kind: historyParams.get("history_kind") ?? undefined,
      historyID: historyParams.get("history_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }
  if (page === "ownerMig" && historyParams.get("contact_history") === "1") {
    void mountContactHistory(stage, {
      kind: historyParams.get("history_kind") ?? undefined,
      historyID: historyParams.get("history_id") ?? undefined,
      customerID: historyParams.get("customer_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }

  if (
    page === "spProductData" &&
    historyParams.get("member_grid_history") === "1"
  ) {
    void mountMemberGridHistory(stage, {
      kind: historyParams.get("history_kind") ?? undefined,
      historyID: historyParams.get("history_id") ?? undefined,
      productID: historyParams.get("product_id") ?? undefined,
      customerID: historyParams.get("customer_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }

  const qs = new URLSearchParams(location.search);
  if (page === "customers" && qs.get("message_history") === "1") {
    void mountMessageHistory(stage, {
      historyID: qs.get("history_message_id") ?? undefined,
      customerID: qs.get("customer_id") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }

  if (page === "config" && qs.get("profile_catalog_history") === "1") {
    void mountProfileCatalogHistory(stage, {
      templateID: qs.get("history_template_id") ?? undefined,
      categoryID: qs.get("history_category_id") ?? undefined,
      view: qs.get("history_view") ?? undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }

  if (
    page === "campaigns" &&
    new URLSearchParams(location.search).get("history") === "1"
  ) {
    void mountCampaignHistory(stage).catch((error) =>
      showLoadError(stage, error),
    );
    return;
  }

  if (
    ["coupons", "couponData"].includes(page) &&
    new URLSearchParams(location.search).get("history") === "1"
  ) {
    const historyID =
      page === "couponData"
        ? new URLSearchParams(location.search).get("id") || ""
        : undefined;
    void mountCouponHistory(stage, historyID).catch((error) =>
      showLoadError(stage, error),
    );
    return;
  }
  if (
    (page === "groupops" || page === "groupopsDetail") &&
    new URLSearchParams(location.search).get("history") === "1"
  ) {
    void mountGroupOpsHistory(stage, {
      view: page === "groupops" ? "list" : "detail",
      planID: new URLSearchParams(location.search).get("id") || undefined,
    }).catch((error) => showLoadError(stage, error));
    return;
  }

  /* ---- 富交互页：模块自管理反馈（toast/confirmBox 均引自 feedback.ts） ---- */
  switch (page) {
    case "spProducts":
      if (new URLSearchParams(location.search).get("history") === "1") {
        void mountServicePeriodHistory(stage).catch((error) =>
          showLoadError(stage, error),
        );
        return;
      }
      break;
    case "automation": {
      const query = new URLSearchParams(location.search);
      if (query.get("history") === "1") {
        void mountAudienceHistory(stage, {
          packageID: query.get("history_package_id") ?? undefined,
          definitionID: query.get("history_definition_id") ?? undefined,
          ruleID: query.get("history_rule_id") ?? undefined,
        }).catch(() => {
          stage.innerHTML =
            '<section data-audience-history><h1>V1 历史（只读）</h1><p role="alert">历史数据读取失败；未进入当前人群管理。</p></section>';
        });
        return;
      }
      break;
    }
    case "radar":
      void mountRadar(stage, api, { view: "list" }).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "radarDetail":
      void mountRadar(stage, api, { view: "detail", id }).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "radarForm":
      void mountRadar(stage, api, { view: "form", id }).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "ai":
      void mountAiAssistant(stage, api, { view: "list" }).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "aiDetail":
      void mountAiAssistant(stage, api, { view: "detail", id }).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "funnel":
      void mountFunnelGrid(stage, api).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "campaigns":
      void mountCampaignWorkspace(stage).catch((error) =>
        showLoadError(stage, error),
      );
      return;
    case "spProductData": {
      const historyID = new URLSearchParams(location.search).get("history");
      if (historyID !== null) {
        void mountServicePeriodHistory(stage, historyID).catch((error) =>
          showLoadError(stage, error),
        );
        return;
      }
      break;
    }
  }

  /* ---- 模板页：mini-runtime + 全局反馈委托 ---- */
  const tpl = document.getElementById("tpl") as HTMLTemplateElement | null;
  if (!tpl) return;
  const controller = new AdminController(api, page);
  initFeedback();
  stage.textContent = "正在读取页面数据…";
  void controller
    .init()
    .then(async () => {
      mount(stage, tpl.innerHTML, controller);
      if (page === "config") {
        const dialog = stage.querySelector<HTMLElement>(
          "#config-extension-dialog",
        );
        const host = stage.querySelector<HTMLElement>("#config-extension-host");
        const title = stage.querySelector<HTMLElement>(
          "#config-extension-title",
        );
        const close = (): void => {
          if (dialog) dialog.style.display = "none";
          if (host) host.textContent = "";
        };
        stage
          .querySelector<HTMLButtonElement>("#close-config-extension")
          ?.addEventListener("click", close);
        const open = (
          label: string,
          render: (root: HTMLElement) => Promise<void>,
        ): void => {
          if (!dialog || !host || !title) return;
          title.textContent = label;
          host.textContent = "正在读取配置…";
          dialog.style.display = "flex";
          void render(host).catch((error) => showLoadError(host, error));
        };
        stage
          .querySelector<HTMLButtonElement>("#open-setup-wizard")
          ?.addEventListener("click", () =>
            open("企微接入基础配置", mountSetupWizard),
          );
        stage
          .querySelector<HTMLButtonElement>("#open-admin-access")
          ?.addEventListener("click", () =>
            open("后台访问成员", mountAdminAccess),
          );
      }
    })
    .catch((error) => showLoadError(stage, error));
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot);
} else {
  boot();
}
