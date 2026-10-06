(() => {
  const button = document.getElementById("claimButton");
  const message = document.getElementById("claimMessage");
  const page = document.querySelector("[data-public-slug]");
  if (!button || !message || !page || page.dataset.isWechat !== "true") return;
  const slug = page.dataset.publicSlug;
  const binding = page.dataset.sessionBinding;
  const storageKey = "aicrm.coupon.claim.tab.v1:" + slug;
  const originalDisabled = button.disabled;
  const unknownMessage = "领取结果尚未确认，原请求已保留。请点击确认原领取结果。";
  const identityMessage = "微信授权已变化，原领取结果尚未确认。请点击确认原领取结果；此操作不会新增领取。";
  const recoveryUnknownMessage = "原领取结果暂未确认，原请求已保留。请使用原身份重新授权后再次核对；此操作不会新增领取。";
  const storageMessage = "无法读取或保存原领取恢复信息，请保留此页面并联系管理员核对。";
  const publicErrorMessages = {
    unionid_oauth_required: "请先使用原身份完成微信授权，再确认原领取结果",
    oauth_required: "请先使用原身份完成微信授权，再确认原领取结果",
    wechat_browser_required: "请在微信中打开后再领取",
    idempotency_key_required: "领取请求已失效，请联系管理员核对原领取结果",
    coupon_not_found: "优惠券不存在或已下架",
    coupon_unavailable: "当前优惠券暂不可领取",
    "coupon is not claimable": "当前优惠券暂不可领取",
    "coupon claim has not started": "优惠券还未开始领取",
    "coupon claim has ended": "优惠券领取已结束",
    "coupon is sold out": "优惠券已领完",
    "per-user coupon claim limit reached": "你已达到该优惠券的领取上限",
  };
  const formatError = value => {
    if (value == null) return "";
    if (typeof value === "string") {
      const text = value.trim();
      if (publicErrorMessages[text]) return publicErrorMessages[text];
      if (/^[a-z][a-z0-9]*_[a-z0-9_]+$/i.test(text)) return "";
      if (!/[一-龥]/.test(text) && /[a-z]/i.test(text)) return "";
      return text;
    }
    if (Array.isArray(value)) return value.map(formatError).filter(Boolean).join("；");
    if (typeof value === "object") {
      for (const key of ["detail", "message", "error", "reason", "errors", "msg"]) {
        const text = formatError(value[key]);
        if (text) return text;
      }
    }
    return "";
  };
  const validBinding = value => typeof value === "string" && /^[A-Za-z0-9_-]{43}$/.test(value);
  const readPending = () => {
    const raw = sessionStorage.getItem(storageKey);
    if (raw === null) return null;
    const value = JSON.parse(raw);
    if (!value || typeof value.key !== "string" || !/^[A-Za-z0-9_-]{8,128}$/.test(value.key) || value.body !== "{}" || !validBinding(value.session_binding)) throw new Error(storageMessage);
    return value;
  };
  const sameIntent = (a, b) => a && b && a.key === b.key && a.body === b.body && a.session_binding === b.session_binding;
  let pending = null, active = null, generation = 0, blocked = false, recoveryOnly = false;
  const showError = (text, disable = false) => {
    blocked = disable;
    message.textContent = text;
    message.classList.add("error");
    button.disabled = disable;
    button.textContent = disable ? "请核对原领取结果" : "确认原领取结果";
  };
  const restore = () => {
    if (active) return;
    try {
      pending = readPending();
      if (pending && pending.session_binding !== binding) { recoveryOnly = true; showError(identityMessage); return; }
      if (pending) { showError(recoveryOnly ? identityMessage : unknownMessage); return; }
      if (!validBinding(binding)) { button.disabled = true; return; }
      button.disabled = originalDisabled;
    } catch (_) { showError(storageMessage, true); }
  };
  const current = attempt => active === attempt && generation === attempt.generation;
  const settleUnknown = (attempt, text = unknownMessage, disable = false) => {
    if (!current(attempt)) return;
    active = null;
    generation++;
    clearTimeout(attempt.timer);
    // Invalidate the old continuation before aborting; some engines ignore
    // cancellation, and a received response may still have a pending body.
    if (attempt.controller) attempt.controller.abort();
    showError(text, disable);
    attempt.reject(new Error(text));
  };
  button.addEventListener("click", async () => {
    if (active || blocked || button.disabled) return;
    try {
      const stored = readPending();
      if (pending && !sameIntent(pending, stored)) { showError(storageMessage, true); return; }
      pending = stored;
      if (!validBinding(binding)) { showError(identityMessage, true); return; }
      if (pending && pending.session_binding !== binding) recoveryOnly = true;
      if (!pending && recoveryOnly) { showError(storageMessage, true); return; }
      if (!pending) {
        pending = {
          key: globalThis.crypto && crypto.randomUUID ? crypto.randomUUID() : `claim-${Date.now()}-${Math.random().toString(16).slice(2)}`,
          body: "{}",
          session_binding: binding,
        };
        sessionStorage.setItem(storageKey, JSON.stringify(pending));
        if (!sameIntent(pending, readPending())) throw new Error(storageMessage);
      }
    } catch (_) { showError(storageMessage, true); return; }
    const checkpoint = pending;
    const confirmOnly = recoveryOnly;
    const attempt = {generation: ++generation, expiresAt: Date.now() + 15000, controller: typeof AbortController === "function" ? new AbortController() : null};
    active = attempt;
    button.disabled = true;
    button.textContent = "领取中…";
    message.classList.remove("error");
    message.textContent = "";
    const deadline = new Promise((_, reject) => {
      attempt.reject = reject;
      attempt.timer = setTimeout(() => settleUnknown(attempt), 15000);
    });
    try {
      const result = await Promise.race([
        (async () => {
          const response = await fetch(`/api/h5/coupons/${encodeURIComponent(slug)}/claim`, {
            method: "POST",
            headers: {"Idempotency-Key": checkpoint.key, "Content-Type": "application/json", "X-AICRM-Coupon-Session-Binding": checkpoint.session_binding, ...(confirmOnly ? {"X-AICRM-Coupon-Recovery-Only": "true"} : {})},
            body: "{}",
            credentials: "same-origin",
            ...(attempt.controller ? {signal: attempt.controller.signal} : {}),
          });
          return {response, payload: await response.json()};
        })(),
        deadline,
      ]);
      if (!current(attempt)) return;
      if (Date.now() >= attempt.expiresAt) { settleUnknown(attempt); return; }
      const {response, payload} = result;
      if (payload && payload.error === "coupon_session_binding_mismatch") { recoveryOnly = true; settleUnknown(attempt, identityMessage); return; }
      if (!response.ok || !payload || payload.ok !== true || !payload.claim || !Number.isSafeInteger(payload.claim.claim_id) || payload.claim.claim_id < 1) {
        settleUnknown(attempt, confirmOnly ? recoveryUnknownMessage : (formatError(payload) || unknownMessage));
        return;
      }
      try {
        if (!sameIntent(checkpoint, readPending())) { settleUnknown(attempt, storageMessage, true); return; }
        sessionStorage.removeItem(storageKey);
        if (readPending() !== null) { settleUnknown(attempt, storageMessage, true); return; }
      } catch (_) { settleUnknown(attempt, storageMessage, true); return; }
      active = null;
      generation++;
      clearTimeout(attempt.timer);
      pending = null;
      recoveryOnly = false;
      message.textContent = "领取成功，正在刷新可用商品…";
      window.location.reload();
    } catch (_) {
      // Keep the same persisted key/body/binding for every unknown result.
      settleUnknown(attempt);
    }
  });
  const checkDeadline = () => {
    if (active && Date.now() >= active.expiresAt) settleUnknown(active);
  };
  window.addEventListener("pagehide", () => { if (active) settleUnknown(active); });
  window.addEventListener("pageshow", () => { checkDeadline(); restore(); });
  document.addEventListener("visibilitychange", () => { if (!document.hidden) checkDeadline(); });
  restore();
})();
