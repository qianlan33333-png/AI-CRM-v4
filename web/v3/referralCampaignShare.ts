import { openDetailDrawer } from "./shared/ui/detailDrawer";
import { renderQr } from "../src/admin/sections/qr";
import { copyPlainText } from "./shared/ui/plainTextClipboard";
import { downloadQrPng } from "./shared/ui/qrPng";

type CampaignShareOptions = {
  campaignID: number;
  campaignName: string;
  read: (signal: AbortSignal) => Promise<unknown>;
};

// The admin host supplies its authorized GET. This view only presents one
// current campaign and never issues invitation credentials or domain writes.
export function openCampaignShareDialog(
  options: CampaignShareOptions,
): HTMLDialogElement {
  const trigger =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  document
    .querySelector<HTMLDialogElement>("dialog[data-referral-campaign-share]")
    ?.close();
  trigger?.focus();
  const body = document.createElement("section");
  body.className = "shared-qr-dialog referral-campaign-share";
  const title = `分享活动 · ${options.campaignName}`;
  const dialog = openDetailDrawer(title, body, {
    placement: "center",
    closeOnBackdrop: true,
  });
  dialog.dataset.referralCampaignShare = String(options.campaignID);
  let controller: AbortController | undefined;
  let attempt = 0;
  dialog.addEventListener(
    "close",
    () => {
      attempt++;
      controller?.abort();
    },
    { once: true },
  );
  const node = (tag: "p" | "div" | "footer", text = "") => {
    const element = document.createElement(tag);
    element.textContent = text;
    return element;
  };
  const action = (label: string, run: () => void | Promise<void>) => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    (button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
    button.dataset.capabilityState = "real";
    button.addEventListener("click", () => {
      if (button.disabled) return;
      button.disabled = true;
      void Promise.resolve()
        .then(run)
        .catch(() => {
          if (!dialog.open) return;
          const warning = node("p", "操作失败，请重试。");
          warning.setAttribute("role", "alert");
          body.append(warning);
        })
        .finally(() => {
          if (button.isConnected) button.disabled = false;
        });
    });
    return button;
  };
  async function load(): Promise<void> {
    controller?.abort();
    controller = new AbortController();
    const request = controller;
    const current = ++attempt;
    body.setAttribute("aria-busy", "true");
    const loading = node("p", "正在读取活动分享入口…");
    loading.setAttribute("role", "status");
    body.replaceChildren(loading);
    try {
      const raw = await options.read(request.signal);
      if (!dialog.open || current !== attempt || request.signal.aborted) return;
      const campaign = raw as Record<string, unknown> | null;
      if (
        !campaign ||
        Number(campaign.id) !== options.campaignID ||
        typeof campaign.name !== "string"
      )
        throw new Error("活动入口读取失败，请重试。");
      const state = String(campaign.effective_state || campaign.state);
      const labels: Record<string, string> = {
        draft: "草稿",
        scheduled: "未开始",
        active: "进行中",
        ended: "已结束",
        disabled: "已停用",
      };
      if (!labels[state]) throw new Error("活动状态读取失败，请重试。");
      const latestTitle = `分享活动 · ${campaign.name}`;
      dialog.setAttribute("aria-label", latestTitle);
      dialog.querySelector("h2")!.textContent = latestTitle;
      body.replaceChildren(node("p", `活动状态：${labels[state]}`));
      if (state === "draft") {
        body.append(node("p", "活动尚未启用，请进入活动详情启用后再分享。"));
        return;
      }
      if (typeof campaign.activity_url !== "string")
        throw new Error("活动公共链接暂不可用，请重试。");
      const url = new URL(campaign.activity_url);
      if (
        !["https:", "http:"].includes(url.protocol) ||
        url.username ||
        url.password ||
        url.hash ||
        url.pathname !== "/referral" ||
        url.search !== `?campaign=${options.campaignID}`
      )
        throw new Error("活动公共链接暂不可用，请重试。");
      if (state === "scheduled") {
        const start = new Date(String(campaign.starts_at));
        if (Number.isNaN(start.valueOf()))
          throw new Error("活动开始时间暂不可用，请重试。");
        body.append(
          node(
            "p",
            `活动将于 ${new Intl.DateTimeFormat("zh-CN", { timeZone: "Asia/Shanghai", dateStyle: "medium", timeStyle: "short" }).format(start)} 开始，当前不能参加。`,
          ),
        );
      } else if (state === "ended" || state === "disabled") {
        body.append(
          node(
            "p",
            `活动${state === "ended" ? "已结束" : "已停用"}，用户当前不能参加。`,
          ),
        );
      }
      body.append(node("p", "将链接或二维码发到群里，用户即可进入本活动。"));
      const input = document.createElement("input");
      input.className = "shared-qr-dialog__url";
      input.readOnly = true;
      input.value = url.href;
      input.setAttribute("aria-label", "活动公共链接");
      const code = node("div");
      code.className = "shared-qr-dialog__code";
      renderQr(code, url.href, "活动公共入口");
      const feedback = node("p");
      feedback.className = "referral-campaign-share-feedback";
      feedback.setAttribute("role", "status");
      const report = (text: string, failed = false) => {
        if (!dialog.open) return;
        feedback.textContent = text;
        feedback.setAttribute("role", failed ? "alert" : "status");
      };
      const controls = node("footer");
      controls.className = "shared-qr-dialog__actions";
      controls.append(
        action("复制链接", async () => {
          const copied = await copyPlainText(url.href);
          report(
            copied ? "活动链接已复制。" : "复制失败，请选中上方链接手动复制。",
            !copied,
          );
          if (!copied && dialog.open) {
            input.focus();
            input.select();
          }
        }),
        action("下载二维码", async () => {
          try {
            await downloadQrPng(
              url.href,
              `裂变活动-${options.campaignID}-二维码.png`,
            );
            report("已开始下载活动二维码。");
          } catch (error) {
            report(
              error instanceof Error
                ? error.message
                : "二维码导出失败，请重试。",
              true,
            );
          }
        }),
      );
      const open = document.createElement("a");
      open.className = "referral-admin-button";
      open.textContent = "打开活动";
      open.href = url.href;
      open.target = "_blank";
      open.rel = "noopener noreferrer";
      controls.append(open);
      body.append(input, code, controls, feedback);
    } catch (error) {
      if (!dialog.open || current !== attempt || request.signal.aborted) return;
      const warning = node(
        "p",
        error instanceof Error ? error.message : "活动入口读取失败，请重试。",
      );
      warning.setAttribute("role", "alert");
      const controls = node("footer");
      controls.className = "shared-qr-dialog__actions";
      controls.append(action("重试", load));
      body.replaceChildren(warning, controls);
    } finally {
      if (current === attempt) body.setAttribute("aria-busy", "false");
    }
  }
  void load();
  return dialog;
}
