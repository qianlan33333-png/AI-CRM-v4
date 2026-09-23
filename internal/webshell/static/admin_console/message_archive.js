(function () {
  "use strict";

  const root = document.querySelector("[data-message-archive-root]");
  if (!root) return;
  const match = /^\/admin\/message-archive\/customers\/(\d+)$/.exec(location.pathname);
  if (!match) return;

  const form = document.getElementById("archive-search-form");
  const state = document.getElementById("archive-state");
  const list = document.getElementById("archive-message-list");
  const more = document.getElementById("archive-more");
  const staff = document.getElementById("archive-staff");
  let cursor = null;
  let watermark = "";
  let objectURLs = [];

  function setState(title, detail, error) {
    state.hidden = false;
    state.className = "admin-state admin-state--inline" + (error ? " admin-state--error" : "");
    state.replaceChildren();
    const heading = document.createElement("strong");
    const copy = document.createElement("span");
    heading.textContent = title;
    copy.textContent = detail;
    state.append(heading, copy);
  }

  function format(value) {
    if (!value) return "—";
    const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === "function" ? window.AdminFmt.localTime(value) : "";
    return formatted || "时间暂时无法显示";
  }

  function resetURLs() {
    for (const value of objectURLs) URL.revokeObjectURL(value);
    objectURLs = [];
  }

  function imageButton(mediaID) {
    const control = document.createElement("button");
    control.type = "button";
    control.className = "archive-media";
    control.textContent = "读取私有图片";
    control.addEventListener("click", async function () {
      control.disabled = true;
      try {
        const response = await fetch(root.dataset.apiBase + match[1] + "/media/" + encodeURIComponent(mediaID), { credentials: "same-origin", cache: "no-store" });
        if (!response.ok) throw new Error("media_unavailable");
        const blob = await response.blob();
        if (!/^image\/(jpeg|png|gif|webp)$/.test(blob.type)) throw new Error("media_unsupported");
        const objectURL = URL.createObjectURL(blob);
        objectURLs.push(objectURL);
        const image = document.createElement("img");
        image.className = "archive-media__image";
        image.alt = "已归档图片";
        image.src = objectURL;
        control.replaceWith(image);
      } catch (_) {
        control.disabled = false;
        control.textContent = "图片暂不可读取";
      }
    });
    return control;
  }

  function item(message) {
    const node = document.createElement("article");
    node.className = "admin-profile-message" + (message.direction === "staff_to_customer" ? " is-staff" : "");
    const meta = document.createElement("div");
    meta.className = "admin-profile-message-meta";
    const sent = document.createElement("span");
    const speaker = document.createElement("span");
    const direction = message.direction === "customer_to_staff" ? "用户发出" : message.direction === "staff_to_customer" ? "员工发出" : "方向待确认";
    const staffNames = (message.staff_names || []).join("、");
    sent.textContent = format(message.occurred_at);
    speaker.textContent = direction + (staffNames ? " · " + staffNames : "") + " · " + (message.chat_type === "group" ? "群聊" : "单聊");
    meta.append(sent, speaker);
    const body = document.createElement("div");
    body.className = "admin-profile-message-content";
    if (message.render_type === "supported") body.textContent = message.content_text || (message.message_type === "image" ? "[图片：私有媒体]" : "[消息]");
    else body.textContent = "[此消息类型已保留，暂不可显示]";
    node.append(meta, body);
    for (const mediaID of message.media_ids || []) node.append(imageButton(mediaID));
    return node;
  }

  function filters() {
    const data = new FormData(form);
    const params = new URLSearchParams();
    for (const name of ["q", "chat_type", "message_type", "direction", "staff_user_id"]) {
      const value = String(data.get(name) || "").trim();
      if (value) params.set(name, value);
    }
    const dateTime = window.AdminDateTime;
    if (!dateTime || typeof dateTime.shanghaiDateTimeLocalToRFC3339 !== "function") {
      setState("时间筛选暂不可用", "请刷新重试。", true);
      return null;
    }
    for (const name of ["start_at", "end_at"]) {
      const value = String(data.get(name) || "").trim();
      if (!value) continue;
      const converted = dateTime.shanghaiDateTimeLocalToRFC3339(value);
      if (!converted) {
        setState("筛选时间格式无效", "请填写有效时间后查询。", true);
        return null;
      }
      params.set(name, converted);
    }
    return params;
  }

  async function loadStaff() {
    if (!staff) return;
    try {
      const response = await fetch(root.dataset.apiBase + match[1] + "/staff", { credentials: "same-origin", headers: { Accept: "application/json" }, cache: "no-store" });
      const payload = await response.json();
      if (!response.ok) return;
      for (const person of payload.items || []) {
        if (!Number.isInteger(person.id) || typeof person.display_name !== "string") continue;
        const option = document.createElement("option");
        option.value = String(person.id);
        option.textContent = person.display_name;
        staff.append(option);
      }
    } catch (_) {}
  }

  async function load(reset) {
    const params = filters();
    if (!params) return;
    if (reset) {
      cursor = null;
      watermark = "";
      resetURLs();
      list.replaceChildren();
    }
    setState("正在读取会话存档", "查询本地受保护归档。", false);
    if (cursor) {
      params.set("watermark", watermark);
      params.set("after_at", cursor.after_at);
      params.set("after_id", cursor.after_id);
    }
    try {
      const response = await fetch(root.dataset.apiBase + match[1] + "?" + params, { credentials: "same-origin", headers: { Accept: "application/json" }, cache: "no-store" });
      const payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "request_failed");
      for (const message of payload.items || []) list.append(item(message));
      list.hidden = list.children.length === 0;
      cursor = payload.next || null;
      watermark = cursor ? cursor.watermark : "";
      more.hidden = !cursor;
      state.hidden = list.children.length > 0;
      if (!list.children.length) setState("暂无已归档消息", "等待企业微信会话存档通知后才会拉取。", false);
    } catch (error) {
      setState("会话存档暂时不可用", error.message === "unauthenticated" ? "登录已失效，请重新登录。" : "请稍后重试。", true);
      more.hidden = true;
    }
  }

  form.addEventListener("submit", function (event) { event.preventDefault(); void load(true); });
  more.addEventListener("click", function () { void load(false); });
  window.addEventListener("pagehide", resetURLs);
  const start = function () { void loadStaff(); void load(true); };
  if (window.AdminFmt && typeof window.AdminFmt.whenAdminDateTimeReady === "function") {
    window.AdminFmt.whenAdminDateTimeReady(start, function () {
      setState("时间暂时无法显示", "请刷新重试。", true);
    });
  } else {
    setState("时间暂时无法显示", "请刷新重试。", true);
  }
}());
