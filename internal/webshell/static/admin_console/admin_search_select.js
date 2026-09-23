// Shared admin selection control. The caller owns the authorized directory and value.
(() => {
  "use strict";
  window.AICRMSearchSelect = function ({ value = "", label, loadPage, emptyLabel = "不关联销售商品", initialLabel, initialQuery, onChange = () => {} }) {
    const root = document.createElement("div");
    root.className = "admin-search-select";
    const search = document.createElement("input");
    search.type = "search"; search.className = "ai-input";
    search.placeholder = "搜索名称或编号"; search.setAttribute("aria-label", `搜索${label}`);
    const select = document.createElement("select");
    select.className = "ai-select"; select.setAttribute("aria-label", label);
    const status = document.createElement("p"); status.className = "core-muted"; status.setAttribute("role", "status");
    const controls = document.createElement("div"); controls.className = "aud-actions";
    const button = (text, fn) => { const b = document.createElement("button"); b.type = "button"; b.className = "aud-btn"; b.textContent = text; b.onclick = fn; return b; };
    let selected = value, rows = [], offset = 0, query = "", total = 0, busy = false, generation = 0;
    const selectedLabels = new Map();
    if (value && initialLabel) selectedLabels.set(value, initialLabel);
    function render() {
      select.replaceChildren(new Option(emptyLabel, ""));
      if (selected && !rows.some(r => r.value === selected)) select.append(new Option(selectedLabels.get(selected) || `已关联：${selected}（待核实名称）`, selected));
      for (const row of rows) { selectedLabels.set(row.value, row.label); select.append(new Option(row.label, row.value)); }
      select.value = selected;
      more.hidden = rows.length >= total;
    }
    async function load(reset = true, initialQuery) {
      if (busy) return;
      const attempt = ++generation;
      const nextQuery = initialQuery ?? (reset ? search.value.trim() : query);
      const nextOffset = reset ? 0 : offset;
      busy = true; find.disabled = more.disabled = retry.disabled = true;
      status.textContent = "正在读取商品…";
      try {
        const page = await loadPage(nextQuery, nextOffset);
        if (attempt !== generation) return;
        rows = reset ? page.items : [...rows, ...page.items];
        query = nextQuery; offset = nextOffset + page.items.length; total = page.total;
        render(); status.textContent = rows.length ? `已显示 ${rows.length} / ${total} 个商品` : "没有匹配商品"; retry.hidden = true;
      } catch (_) { status.textContent = "商品读取失败，已保留当前选择，请重试。"; retry.hidden = false; }
      finally { busy = false; find.disabled = more.disabled = retry.disabled = false; }
    }
    const find = button("搜索", () => load());
    const more = button("加载更多", () => load(false)); more.hidden = true;
    const retry = button("重试", () => load()); retry.hidden = true;
    const clear = button("清空选择", () => { selected = ""; render(); onChange(selected); });
    search.addEventListener("keydown", event => { if (event.key === "Enter") { event.preventDefault(); if (!event.isComposing && event.keyCode !== 229) void load(); } });
    select.onchange = () => { selected = select.value; onChange(selected); };
    controls.append(find, more, retry, clear); root.append(search, select, controls, status);
    render(); void load(true, initialQuery ?? (value || ""));
    return { element: root, get value() { return selected; } };
  };
})();
