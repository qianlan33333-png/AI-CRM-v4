(function (global) {
  "use strict";

  var detailPath = /^\/api\/admin\/automation-conversion\/group-ops\/history\/plans\/[1-9][0-9]{0,18}\/nodes$/;
  var captured = null;
  var scheduled = false;
  var generation = 0;

  function text(value) { return typeof value === "string" ? value : ""; }
  function attachments(value) {
    if (!Array.isArray(value)) return [];
    return value.map(function (item, index) {
      var row = item && typeof item === "object" && !Array.isArray(item) ? item : {};
      var kind = text(row.kind) || "附件";
      var id = row.id === undefined || row.id === null || row.id === "" ? "#" + (index + 1) : "#" + String(row.id);
      return { type_label: "历史素材 · " + kind, name: kind + " " + id, description: "原始附件引用（仅展示）" };
    });
  }

  function requestContext(input, init) {
    var url;
    try { url = new URL(typeof input === "string" ? input : input.url, global.location.href); } catch (_error) { return null; }
    var method = (init && init.method) || (typeof input !== "string" && input.method) || "GET";
    if (url.origin !== global.location.origin || method.toUpperCase() !== "GET" || !detailPath.test(url.pathname)) return null;
    var match = url.pathname.match(/\/plans\/([1-9][0-9]{0,18})\/nodes$/);
    if (!match) return null;
    var limit = Number(url.searchParams.get("limit"));
    var offset = Number(url.searchParams.get("offset"));
    if (!Number.isSafeInteger(limit) || limit < 1 || !Number.isSafeInteger(offset) || offset < 0) return null;
    return { planID: match[1], limit: limit, offset: offset };
  }

  function pageIsCurrent(host, page) {
    var heading = host.querySelector("h2");
    var controls = host.querySelector("[data-refresh]");
    if (!heading || heading.textContent !== "历史节点 · plan_id=" + page.planID || !controls || !controls.parentElement) return false;
    var summary = controls.parentElement.textContent || "";
    var match = summary.match(/(?:^|·\s*)offset=(\d+)\s*·\s*每页\s+(\d+)\s+条/);
    return !!match && Number(match[1]) === page.offset && Number(match[2]) === page.limit;
  }

  function articleForItem(rows, item) {
    var expected = String(item.id) + " / " + String(item.source_node_id);
    for (var index = 0; index < rows.length; index += 1) {
      var firstField = rows[index].firstElementChild;
      if (!firstField || firstField.tagName !== "DIV") continue;
      var spans = firstField.querySelectorAll("span");
      if (spans.length === 2 && (spans[0].textContent || "").trim() === "V2 ID / 源节点 ID：" && (spans[1].textContent || "").trim() === expected) return rows[index];
    }
    return null;
  }

  function render() {
    scheduled = false;
    if (!captured || !Array.isArray(captured.items)) return;
    var host = document.querySelector("#group-history-secondary [data-history-rows]")?.parentElement;
    var renderer = global.AICRMSendContentReadonlyDetail;
    if (!host || !pageIsCurrent(host, captured) || !renderer || typeof renderer.renderFull !== "function" || typeof renderer.escapeHtml !== "function") return;
    var rows = host.querySelectorAll("[data-history-rows] article");
    captured.items.forEach(function (item) {
      if (String(item.plan_id) !== captured.planID) return;
      var article = articleForItem(rows, item);
      var raw = article && article.querySelector("details");
      if (!raw || raw.getAttribute("data-groupops-history-content") === "rendered") return;
      var content = document.createElement("section");
      content.setAttribute("data-groupops-history-content", "rendered");
      content.style.display = "grid";
      content.style.gap = "8px";
      content.innerHTML = '<div><span style="color:#667085">原操作标题：</span><span style="white-space:pre-wrap">' + renderer.escapeHtml(text(item.action_title)) + '</span></div>' + renderer.renderFull({
        content_text: text(item.text_content),
        content_basis_label: "历史正文（仅展示，不执行）",
        attachment_basis_label: "历史附件引用（仅展示，不读取素材）",
        attachments: attachments(item.attachments)
      });
      raw.replaceWith(content);
    });
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    global.setTimeout(render, 0);
  }

  function capture(response, context, requestGeneration) {
    if (!response || typeof response.clone !== "function") return;
    Promise.resolve(response.clone().json()).then(function (page) {
      if (requestGeneration !== generation) return;
      if (page && page.source === "v1_history" && page.read_only === true && page.real_external_call_executed === false && Array.isArray(page.items) && Number(page.offset) === context.offset && Number(page.limit) === context.limit) {
        captured = { planID: context.planID, limit: context.limit, offset: context.offset, items: page.items };
        schedule();
      }
    }).catch(function () {});
  }

  var previousFetch = global.fetch;
  if (typeof previousFetch === "function") {
    global.fetch = function (input, init) {
      var context = requestContext(input, init);
      var requestGeneration = context ? (++generation) : 0;
      if (context) captured = null;
      return Promise.resolve(previousFetch.call(this, input, init)).then(function (response) {
        if (context) capture(response, context, requestGeneration);
        return response;
      });
    };
  }
  function observe() {
    var root = document.documentElement;
    if (!root) {
      global.setTimeout(observe, 0);
      return;
    }
    new MutationObserver(schedule).observe(root, { childList: true, subtree: true });
  }
  observe();
})(window);
