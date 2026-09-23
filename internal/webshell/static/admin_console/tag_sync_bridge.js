(() => {
  "use strict";

  const statusURL = "/api/admin/wecom/tags/sync-status";
  const catalogURL = "/api/admin/wecom/tags";
  const successKey = "aicrm.tag-sync.completed";
  let active = false;
  let state = "idle";
  let trackedReceipt = 0;
  let acceptanceGraceUntil = 0;
  let timer = 0;
  let archiveTimer = 0;
  let archiveRefreshing = false;
  let catalogTags = [];
  let catalogGroups = [];
  let catalogNeedsRefresh = false;
  let recoverySignature = "";
  const retryKeys = new Map();
  const tagSyncStateLabel = (value) => ({
    idle: "未开始", queued: "排队中", attempted: "正在执行", executed: "已完成",
    outcome_unknown: "结果待核对", retryable_failed: "可重试失败", final_failed: "执行失败",
    cancelled: "已取消", reconciled: "已完成核对",
  })[String(value || "")] || "状态待确认";

  const syncButton = () => {
    if (typeof document === "undefined" || !document?.querySelectorAll)
      return null;
    return Array.from(document.querySelectorAll("button")).find(
      (button) =>
        button.textContent.trim() === "同步企微标签" ||
        button.dataset.tagSyncButton === "1",
    );
  };

  // The donor uses local IDs for its commands. Preserve that key separately
  // before showing the authoritative read-only Provider ID in its detail view.
  const paintDetailProviderID = () => {
    if (typeof document === "undefined" || !document?.querySelectorAll) return;
    for (const code of document.querySelectorAll("#stage code")) {
      const field = code.parentElement;
      if (
        field?.previousElementSibling?.textContent.trim() !== "tag_id" ||
        !Array.from(field.querySelectorAll("button")).some(
          (button) => button.textContent.trim() === "复制",
        )
      )
        continue;
      if (!code.dataset.localTagId) {
        const localID = code.textContent.trim();
        if (!/^[1-9][0-9]*$/.test(localID)) continue;
        code.dataset.localTagId = localID;
      }
      const matches = catalogTags.filter(
        (tag) => String(tag.id || tag.tag_id) === code.dataset.localTagId,
      );
      const providerID =
        matches.length === 1 ? String(matches[0].provider_tag_id || "") : "";
      const label = providerID || "未同步（尚未取得企微 tag_id）";
      if (code.textContent !== label) code.textContent = label;
    }
  };

  const paint = () => {
    paintDetailProviderID();
    const button = syncButton();
    if (!button) return;
    button.dataset.tagSyncButton = "1";
    button.disabled = active;
    const label = !active
      ? "同步企微标签"
      : state === "outcome_unknown"
        ? "同步结果待对账"
        : state === "retryable_failed"
          ? "同步待重试"
          : "同步中…";
    if (button.textContent !== label) button.textContent = label;
    button.style.cursor = active ? "not-allowed" : "pointer";
    button.style.opacity = active ? "0.65" : "1";
    button.setAttribute("aria-busy", active ? "true" : "false");
  };

  const notice = (message, error = false) => {
    const node = document.createElement("div");
    node.setAttribute("role", error ? "alert" : "status");
    node.textContent = message;
    Object.assign(node.style, {
      position: "fixed",
      right: "24px",
      bottom: "24px",
      zIndex: "99999",
      maxWidth: "460px",
      padding: "12px 18px",
      borderRadius: "8px",
      color: "#fff",
      background: error ? "#d83931" : "#1f2329",
      boxShadow: "0 8px 28px rgba(0,0,0,.18)",
      fontSize: "14px",
    });
    document.body.appendChild(node);
    window.setTimeout(() => node.remove(), 5000);
  };

  const schedule = (delay = 800) => {
    window.clearTimeout(timer);
    timer = window.setTimeout(poll, delay);
  };

  const paintRecoveries = (items) => {
    const rows = Array.isArray(items)
      ? items.filter(
          (item) =>
            Number(item.id) > 0 &&
            ["final_failed", "retryable_failed"].includes(item.state),
        )
      : [];
    const signature = JSON.stringify(rows);
    const existing = document.querySelector("[data-tag-mutation-recovery]");
    if (signature === recoverySignature && existing) return;
    recoverySignature = signature;
    existing?.remove();
    if (!rows.length) return;
    const panel = document.createElement("section");
    panel.dataset.tagMutationRecovery = "1";
    Object.assign(panel.style, {
      margin: "12px 0",
      padding: "12px 14px",
      border: "1px solid #f2c94c",
      borderRadius: "8px",
      background: "#fffbe6",
      color: "#614700",
      fontSize: "13px",
      lineHeight: "1.8",
    });
    const title = document.createElement("strong");
    title.textContent = "企微写入恢复";
    panel.appendChild(title);
    const labels = {
      group_create: "创建标签组",
      group_update: "修改标签组",
      group_archive: "删除标签组",
      tag_create: "创建标签",
      tag_update: "修改标签",
      tag_archive: "删除标签",
    };
    for (const item of rows) {
      const row = document.createElement("div");
      row.textContent = `${labels[item.operation] || "标签操作"}「${item.name || "未命名"}」 `;
      const button = document.createElement("button");
      button.textContent = "重试企微同步";
      button.__dcBound = true;
      button.type = "button";
      button.addEventListener("click", async () => {
        button.disabled = true;
        const retryScope = `${item.id}:${item.generation}`;
        if (!retryKeys.has(retryScope))
          retryKeys.set(retryScope, "tag-retry-" + crypto.randomUUID());
        const cookie = document.cookie
          .split(";")
          .map((part) => part.trim())
          .find(
            (part) =>
              part.startsWith("aicrm_admin_csrf=") ||
              part.startsWith("aicrm_csrf="),
          );
        const csrf = cookie
          ? decodeURIComponent(cookie.slice(cookie.indexOf("=") + 1))
          : "";
        try {
          const response = await fetch(
            `${catalogURL}/mutations/${item.id}/retry`,
            {
              method: "POST",
              credentials: "same-origin",
              headers: {
                "X-CSRF-Token": csrf,
                "Idempotency-Key": retryKeys.get(retryScope),
              },
            },
          );
          if (!response.ok) {
            notice(
              response.status === 409
                ? "当前结果不能安全重试，请先核对企微"
                : response.status === 503
                  ? "企微标签写入尚不可用，请联系管理员"
                  : "重试未受理，请刷新后再试",
              true,
            );
            return;
          }
          const result = await response.json();
          const states = {
            accepted: "原企微任务等待执行",
            queued: "原企微任务等待执行",
            attempted: "原企微任务正在执行，结果待确认",
            executed: "原企微任务已完成",
            outcome_unknown: "原企微任务结果待核对，请勿重复创建",
            final_failed: "原企微任务尚未完成，请查看当前失败状态",
            retryable_failed: "原企微任务尚未完成，请查看当前失败状态",
            cancelled: "原企微任务已取消",
            reconciled: "原企微任务已完成对账，请查看核对结果",
          };
          notice(
            states[result.effect_state] || "原企微任务状态待确认",
            [
              "outcome_unknown",
              "final_failed",
              "retryable_failed",
              "cancelled",
            ].includes(result.effect_state),
          );
          scheduleArchiveRefresh(100);
        } catch (_) {
          notice("请求结果待确认，再次点击将查询同一次受理", true);
        } finally {
          button.disabled = false;
        }
      });
      row.appendChild(button);
      panel.appendChild(row);
    }
    document.getElementById("stage")?.prepend(panel);
  };

  const refreshArchiveOperations = async () => {
    if (archiveRefreshing) return;
    archiveRefreshing = true;
    try {
      const response = await fetch(catalogURL, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "application/json" },
      });
      if (!response.ok) return;
      const payload = await response.json();
      catalogTags = Array.isArray(payload.tags) ? payload.tags : [];
      catalogGroups = Array.isArray(payload.groups) ? payload.groups : [];
      paintDetailProviderID();
      catalogNeedsRefresh = [
        ...catalogTags,
        ...catalogGroups,
        ...(Array.isArray(payload.archive_operations)
          ? payload.archive_operations
          : []),
      ].some((item) =>
        ["queued", "attempted"].includes(
          item.provider_write_state || item.state,
        ),
      );
      paintRecoveries(payload.mutation_recoveries);
    } catch (_error) {
      // The frozen catalog keeps rendering. A transient read failure must not
      // replace its data with a fabricated archive outcome.
    } finally {
      archiveRefreshing = false;
      if (catalogNeedsRefresh) scheduleArchiveRefresh(1500);
    }
  };

  const scheduleArchiveRefresh = (delay = 300) => {
    window.clearTimeout(archiveTimer);
    archiveTimer = window.setTimeout(
      () => void refreshArchiveOperations(),
      delay,
    );
  };

  const poll = async () => {
    try {
      const response = await fetch(statusURL, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error(`status ${response.status}`);
      const payload = await response.json();
      const sync = payload && payload.sync;
      if (!sync || typeof sync.state !== "string")
        throw new Error("invalid sync status");
      const receipt = Number(sync.receipt_id || 0);
      const isActive = Boolean(sync.active);
      if (isActive) {
        active = true;
        state = sync.state;
        trackedReceipt = receipt;
        paint();
        schedule();
        return;
      }
      if (
        Date.now() < acceptanceGraceUntil &&
        (trackedReceipt === 0 || !receipt || receipt === trackedReceipt)
      ) {
        active = true;
        paint();
        schedule(250);
        return;
      }
      const completedTracked = trackedReceipt > 0 && receipt === trackedReceipt;
      active = false;
      state = sync.state;
      paint();
      if (completedTracked && sync.state === "executed") {
        try {
          sessionStorage.setItem(
            successKey,
            JSON.stringify({
              groups: sync.group_count || 0,
              tags: sync.tag_count || 0,
            }),
          );
        } catch (_error) {}
        location.reload();
        return;
      }
      if (completedTracked && ["final_failed", "cancelled"].includes(sync.state)) {
        notice(`标签同步未完成（${tagSyncStateLabel(sync.state)}），已允许重新发起`, true);
      }
      if (completedTracked && sync.state === "reconciled") notice("已完成核对，请查看结果。", true);
    } catch (_error) {
      if (active) schedule(1500);
    }
  };

  document.addEventListener(
    "click",
    (event) => {
      const button =
        event.target instanceof Element ? event.target.closest("button") : null;
      if (
        !button ||
        (button.textContent.trim() !== "同步企微标签" &&
          button.dataset.tagSyncButton !== "1")
      )
        return;
      if (active) {
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
      }
      active = true;
      state = "queued";
      trackedReceipt = 0;
      acceptanceGraceUntil = Date.now() + 3000;
      paint();
      schedule(250);
    },
    true,
  );

  document.addEventListener(
    "click",
    (event) => {
      const button =
        event.target instanceof Element ? event.target.closest("button") : null;
      if (button?.textContent.trim() === "删除") {
        // The frozen controller archives a row asynchronously. Refresh twice
        // after the command settles so recovery actions use the latest
        // durable receipt even after the archived row leaves the table.
        scheduleArchiveRefresh(300);
        window.setTimeout(() => scheduleArchiveRefresh(300), 1200);
      }
    },
    true,
  );

  // Preserve the frozen controller's local command IDs. Only clipboard reads
  // use the authoritative Tag-owned Provider binding; never copy a local ID.
  document.addEventListener(
    "click",
    (event) => {
      const button =
        event.target instanceof Element ? event.target.closest("button") : null;
      if (!button) return;
      const label = button.textContent.trim();
      const row = button.closest("tr");
      const detailCode =
        label === "复制" ? button.parentElement?.querySelector("code") : null;
      if (label !== "复制 tag_id" && !detailCode) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      let matches = [];
      if (detailCode) {
        const localID = Number(
          detailCode.dataset.localTagId || detailCode.textContent,
        );
        matches = catalogTags.filter(
          (tag) => Number(tag.id || tag.tag_id) === localID,
        );
      } else if (row) {
        const name = row.cells[0]?.textContent.trim();
        const groupName = document
          .querySelector('[data-tag-group-card][aria-pressed="true"] span')
          ?.textContent.trim();
        const groups = catalogGroups.filter(
          (group) => String(group.group_name || group.name) === groupName,
        );
        if (groups.length === 1) {
          matches = catalogTags.filter(
            (tag) =>
              Number(tag.group_id) === Number(groups[0].group_id) &&
              String(tag.tag_name || tag.name) === name,
          );
        }
      }
      const providerID =
        matches.length === 1 ? String(matches[0].provider_tag_id || "") : "";
      if (!providerID) {
        notice("尚未取得企微 tag_id，请先确认企微同步完成", true);
        return;
      }
      if (!navigator.clipboard?.writeText) {
        notice("浏览器暂不支持复制，请在安全连接中重试", true);
        return;
      }
      void navigator.clipboard.writeText(providerID).then(
        () => notice("企微 tag_id 已复制"),
        () => notice("复制失败，请重试", true),
      );
    },
    true,
  );

  new MutationObserver(() => {
    paint();
    scheduleArchiveRefresh(500);
  }).observe(document.documentElement, { childList: true, subtree: true });
  document.addEventListener("DOMContentLoaded", () => {
    let raw = null;
    try {
      raw = sessionStorage.getItem(successKey);
    } catch (_error) {}
    if (raw) {
      try {
        sessionStorage.removeItem(successKey);
      } catch (_error) {}
      try {
        const result = JSON.parse(raw);
        notice(
          `标签同步完成：${result.groups} 个标签组，${result.tags} 个标签`,
        );
      } catch (_error) {
        notice("标签同步完成");
      }
    }
    paint();
    void poll();
    void refreshArchiveOperations();
  });
})();
