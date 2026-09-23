function bootLegacyFrames() {
  document.querySelectorAll("[data-legacy-shell]").forEach((shell) => {
    const frame = shell.querySelector("[data-legacy-frame]");
    const state = shell.querySelector("[data-legacy-state]");
    if (!frame || !state) {
      return;
    }

    let loaded = false;
    const ready = () => {
      if (loaded) {
        return;
      }
      loaded = true;
      shell.querySelector(".admin-legacy-frame-wrap")?.classList.add("is-ready");
    };

    frame.addEventListener("load", ready, { once: true });
    window.setTimeout(() => {
    if (loaded) {
      return;
    }
    state.classList.remove("admin-state--loading");
    state.classList.add("admin-state--error");
    state.innerHTML = [
        "<strong>页面加载超时</strong>",
        "<span>当前页面没有按预期完成加载，请稍后重试。</span>",
    ].join("");
  }, 15000);
  });
}

function bootOutputModal() {
  const backdrop = document.querySelector("[data-output-modal-backdrop]");
  if (!backdrop) {
    return;
  }

  const closeUrl = backdrop.getAttribute("data-close-url") || "";
  const closeModal = () => {
    if (!closeUrl) {
      return;
    }
    window.location.href = closeUrl;
  };

  backdrop.addEventListener("click", (event) => {
    if (event.target === backdrop) {
      closeModal();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeModal();
    }
  });

  backdrop.querySelectorAll("[data-output-modal-close]").forEach((node) => {
    node.addEventListener("click", (event) => {
      if (!closeUrl) {
        return;
      }
      event.preventDefault();
      closeModal();
    });
  });
}

function bootCopyButtons() {
  document.querySelectorAll("[data-copy-text]").forEach((button) => {
    button.addEventListener("click", async () => {
      const text = button.getAttribute("data-copy-text") || "";
      const defaultLabel = button.getAttribute("data-copy-label-default") || button.textContent || "复制";
      const successLabel = button.getAttribute("data-copy-label-success") || "已复制";
      const errorLabel = button.getAttribute("data-copy-label-error") || "复制失败";
      try {
        await navigator.clipboard.writeText(text);
        button.textContent = successLabel;
      } catch (error) {
        button.textContent = errorLabel;
      }
      window.setTimeout(() => {
        button.textContent = defaultLabel;
      }, 1500);
    });
  });
}

function bootOutputReviewForms() {
  document.querySelectorAll("[data-output-review-reject-form]").forEach((form) => {
    const button = form.querySelector("[data-output-review-reject]");
    const noteInput = form.querySelector('input[name="review_note"]');
    if (!button || !noteInput) {
      return;
    }
    button.addEventListener("click", () => {
      const defaultValue = noteInput.value || "";
      const note = window.prompt("可选：填写这条话术未被采用的原因或修改建议", defaultValue);
      if (note === null) {
        return;
      }
      noteInput.value = note.trim();
      form.submit();
    });
  });
}

function formatShanghaiTime(input) {
  if (input === null || input === undefined || input === "") {
    return "";
  }
  let raw = input;
  if (input instanceof Date) {
    if (Number.isNaN(input.getTime())) return "时间暂时无法显示";
    raw = input.toISOString();
  }
  const formatter = window.AdminDateTime;
  if (!formatter || typeof formatter.formatShanghaiDateTime !== "function") return "时间暂时无法显示";
  const value = formatter.formatShanghaiDateTime(raw);
  return value === "未提供" ? "时间暂时无法显示" : value;
}

function whenAdminDateTimeReady(onReady, onUnavailable) {
  const current = () => window.AdminDateTime && typeof window.AdminDateTime.formatShanghaiDateTime === "function" ? window.AdminDateTime : null;
  let readyDispatched = false;
  let unavailableShown = false;
  const notifyReady = () => {
    const bridge = current();
    if (!bridge || readyDispatched) return false;
    readyDispatched = true;
    onReady(bridge);
    return true;
  };
  if (notifyReady()) return;
  window.addEventListener("aicrm:admin-date-time-ready", notifyReady);
  window.setTimeout(() => {
    if (notifyReady() || unavailableShown) return;
    unavailableShown = true;
    if (typeof onUnavailable === "function") onUnavailable();
  }, 3000);
}

window.AdminFmt = Object.assign(window.AdminFmt || {}, {
  relativeTime: formatShanghaiTime,
  localTime: formatShanghaiTime,
  whenAdminDateTimeReady,
});

document.addEventListener("DOMContentLoaded", () => {
  bootLegacyFrames();
  bootOutputModal();
  bootCopyButtons();
  bootOutputReviewForms();
});
