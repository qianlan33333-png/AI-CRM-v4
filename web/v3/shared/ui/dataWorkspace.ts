import { TabulatorFull, type ColumnDefinition } from "tabulator-tables";
import { init, use, type EChartsType } from "echarts/core";
import { BarChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import { workspaceStyle } from "./dataWorkspaceStyle";
use([BarChart, GridComponent, TooltipComponent, CanvasRenderer]);
export interface WorkspaceMetric {
  key: string;
  label: string;
  value: number | null;
  percentage?: number | null;
  distribution?: { label: string; value: number }[];
}
export interface WorkspacePresentation {
  columns?: string[];
  widths?: Record<string, number>;
  metrics?: string[];
  order?: string[];
}
type Page = "overview" | "details";
export function workspaceButton(
  label: string,
  action: () => void,
): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  b.onclick = action;
  return b;
}
function icon(
  kind: "overview" | "details" | "filter" | "settings",
): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 20 20");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.4");
  svg.setAttribute("class", "dw-icon");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(svg.namespaceURI, "path");
  path.setAttribute(
    "d",
    {
      overview: "M3 16V9h3v7zm6 0V4h3v12zm6 0V7h3v9z",
      details: "M3 3h14v14H3zM3 8h14M3 12h14M8 3v14",
      filter: "M2 4h16l-6 7v5l-4 2v-7z",
      settings: "M3 5h14M3 10h14M3 15h14M7 3v4M13 8v4M8 13v4",
    }[kind],
  );
  svg.append(path);
  return svg;
}
// Persist only the selected view reference, never query values or row data.
export function storedWorkspaceView(value?: string): string | null {
  const key =
    "crm-workspace-view:" +
    location.pathname +
    ":" +
    (new URLSearchParams(location.search).get("id") || "");
  try {
    if (value !== undefined) localStorage.setItem(key, value);
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
// The component owns presentation and navigation only. Its caller owns scope,
// authorization, full-result metrics/group counts and cursor pagination.
export class DataWorkspace {
  private grid?: TabulatorFull;
  private ready?: Promise<void>;
  private chart?: EChartsType;
  private tierChart?: EChartsType;
  private metrics: WorkspaceMetric[] = [];
  private rows: Record<string, unknown>[] = [];
  private groupFields: string[] = [];
  private presentation: WorkspacePresentation = {};
  private applying = false;
  private destroyed = false;
  private revision = 0;
  private allowedDetails = true;
  private page: Page = "overview";
  private nav = document.createElement("nav");
  private bar = document.createElement("div");
  private tools = document.createElement("div");
  private scope = document.createElement("div");
  private meta = document.createElement("div");
  private overview = document.createElement("section");
  private details = document.createElement("section");
  private table = document.createElement("div");
  private cards = document.createElement("div");
  private chartGrid = document.createElement("div");
  private plot = document.createElement("div");
  private tierPlot = document.createElement("div");
  private mainCard = document.createElement("div");
  private tierCard = document.createElement("div");
  private drawer = document.createElement("dialog");
  private drawerBody = document.createElement("div");
  private drawerTitle = document.createElement("h2");
  private configButton: HTMLButtonElement;
  private dirtyLabel = document.createElement("span");
  private detailTools: HTMLElement[] = [];
  private defaultColumns: string[];
  private dirty = false;
  private drill?: (key: string) => void;
  private drillKeys?: string[];
  private observer: ResizeObserver;
  private pop = () =>
    this.setPage(
      new URLSearchParams(location.search).get("tab") === "details"
        ? "details"
        : "overview",
      false,
    );
  constructor(
    readonly root: HTMLElement,
    private columns: ColumnDefinition[],
    private changed?: (value: WorkspacePresentation) => void,
    options: { allowDetails?: boolean } = {},
  ) {
    if (!document.getElementById("data-workspace-style")) {
      const s = document.createElement("style");
      s.id = "data-workspace-style";
      s.textContent = workspaceStyle;
      document.head.append(s);
    }
    this.allowedDetails = options.allowDetails !== false;
    this.defaultColumns = columns
      .filter((c) => c.visible !== false)
      .map((c) => c.field!)
      .filter(Boolean);
    root.classList.add("data-workspace");
    this.nav.className = "dw-nav";
    this.nav.setAttribute("aria-label", "数据页面");
    for (const [value, label] of [
      ["overview", "数据总览"],
      ["details", "明细视图"],
    ] as const) {
      const a = document.createElement("a");
      const url = new URL(location.href);
      url.searchParams.set("tab", value);
      a.href = url.pathname + url.search + url.hash;
      a.dataset.workspaceTab = value;
      a.append(icon(value), document.createTextNode(label));
      a.onclick = (e) => {
        if (!e.ctrlKey && !e.metaKey && !e.shiftKey && e.button === 0) {
          e.preventDefault();
          this.setPage(value);
        }
      };
      this.nav.append(a);
    }
    this.bar.className = "dw-bar";
    this.tools.className = "dw-tools";
    this.scope.className = "dw-scope";
    this.meta.className = "dw-meta";
    this.overview.className = "dw-overview";
    this.overview.setAttribute("aria-label", "数据总览");
    this.details.className = "dw-details";
    this.details.setAttribute("aria-label", "明细视图");
    this.cards.className = "dw-cards";
    this.chartGrid.className = "dw-chart-grid";
    const chartCard = (
      card: HTMLElement,
      plot: HTMLElement,
      title: string,
      subtitle: string,
    ) => {
      card.className = "dw-chart-card";
      const h = document.createElement("h3"),
        p = document.createElement("p");
      h.textContent = title;
      p.textContent = subtitle;
      plot.className = "dw-plot";
      card.append(h, p, plot);
    };
    chartCard(this.mainCard, this.plot, "会员状态分布", "当前筛选范围内的人数");
    chartCard(
      this.tierCard,
      this.tierPlot,
      "会员等级分布",
      "当前筛选范围内的等级构成",
    );
    this.chartGrid.append(this.mainCard, this.tierCard);
    this.overview.append(this.cards, this.chartGrid);
    this.details.append(this.table);
    this.dirtyLabel.className = "dw-dirty";
    this.dirtyLabel.hidden = true;
    this.dirtyLabel.textContent = "未保存";
    this.configButton = workspaceButton("配置看板", () => this.openSettings());
    this.configButton.className = "dw-config-button";
    this.bar.append(this.tools, this.dirtyLabel, this.configButton);
    this.drawer.className = "dw-drawer";
    const header = document.createElement("header");
    const close = workspaceButton("×", () => this.drawer.close());
    close.setAttribute("aria-label", "关闭设置");
    header.append(this.drawerTitle, close);
    this.drawerBody.className = "dw-drawer-body";
    this.drawer.append(header, this.drawerBody);
    root.append(
      this.nav,
      this.bar,
      this.scope,
      this.meta,
      this.overview,
      this.details,
      this.drawer,
    );
    this.observer = new ResizeObserver(() => {
      if (this.page === "overview") {
        this.chart?.resize();
        this.tierChart?.resize();
      }
    });
    this.observer.observe(this.overview);
    window.addEventListener("popstate", this.pop);
    this.pop();
  }
  setPage(page: Page, push = true) {
    if (this.destroyed) return;
    this.page =
      page === "details" && this.allowedDetails ? "details" : "overview";
    this.root.dataset.workspacePage = this.page;
    for (const a of this.nav.querySelectorAll<HTMLAnchorElement>("a")) {
      a.hidden = a.dataset.workspaceTab === "details" && !this.allowedDetails;
      if (a.dataset.workspaceTab === this.page)
        a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
      const u = new URL(location.href);
      u.searchParams.set("tab", a.dataset.workspaceTab!);
      a.href = u.pathname + u.search + u.hash;
    }
    if (push) {
      const u = new URL(location.href);
      u.searchParams.set("tab", this.page);
      window.history.pushState(null, "", u.pathname + u.search + u.hash);
    }
    this.overview.hidden = this.page !== "overview";
    this.details.hidden = this.page !== "details";
    this.detailTools.forEach((el) => {
      el.hidden = this.page !== "details";
    });
    this.configButton.replaceChildren(
      icon("settings"),
      document.createTextNode(this.page === "overview" ? "配置看板" : "列设置"),
    );
    for (const p of this.tools.querySelectorAll("details")) p.open = false;
    if (this.drawer.open) this.drawer.close();
    if (this.page === "details") void this.renderTable();
    else this.renderMetrics();
  }
  closeTools() {
    for (const el of this.tools.querySelectorAll("details")) el.open = false;
  }
  refreshLinks() {
    for (const a of this.nav.querySelectorAll<HTMLAnchorElement>("a")) {
      const u = new URL(location.href);
      u.searchParams.set("tab", a.dataset.workspaceTab!);
      a.href = u.pathname + u.search + u.hash;
    }
  }
  setViewPicker(el: HTMLElement) {
    this.bar.prepend(el);
  }
  setMeta(el: HTMLElement) {
    this.meta.append(el);
  }
  setFooter(el: HTMLElement) {
    el.classList.add("dw-footer");
    this.details.append(el);
  }
  setScope(items: { label: string; remove?: () => void }[]) {
    this.scope.replaceChildren();
    const label = document.createElement("span");
    label.textContent = items.length ? "当前范围" : "当前范围：全部授权数据";
    this.scope.append(label);
    for (const item of items) {
      const b = workspaceButton(
        item.label + (item.remove ? " ×" : ""),
        item.remove || (() => {}),
      );
      b.setAttribute(
        "aria-label",
        (item.remove ? "移除条件：" : "条件：") + item.label,
      );
      if (!item.remove) b.disabled = true;
      this.scope.append(b);
    }
  }
  addTool(label: string, content: HTMLElement, detailsOnly = false) {
    const d = document.createElement("details"),
      s = document.createElement("summary"),
      p = document.createElement("div"),
      h = document.createElement("h3");
    d.className = "dw-tool";
    s.append(
      icon(label === "筛选" ? "filter" : "settings"),
      document.createTextNode(label),
    );
    p.className = "dw-popover";
    h.textContent = label;
    p.append(h, content);
    d.append(s, p);
    this.tools.append(d);
    if (detailsOnly) {
      this.detailTools.push(d);
      d.hidden = this.page !== "details";
    }
    d.addEventListener("toggle", () => {
      if (d.open)
        for (const other of this.tools.querySelectorAll("details"))
          if (other !== d) other.open = false;
    });
    return d;
  }
  placeToolbar(toolbar: HTMLElement) {
    return this.addTool("筛选", toolbar);
  }
  markDirty() {
    this.dirty = true;
    this.dirtyLabel.hidden = false;
  }
  markSaved() {
    this.dirty = false;
    this.dirtyLabel.hidden = true;
  }
  async confirmViewChange(save: () => Promise<boolean>): Promise<boolean> {
    if (!this.dirty) return true;
    const dialog = document.createElement("dialog");
    dialog.className = "dw-dialog";
    const h = document.createElement("h3"),
      p = document.createElement("p"),
      footer = document.createElement("footer");
    h.textContent = "保存当前视图的修改？";
    p.textContent = "当前筛选和展示设置尚未保存。";
    dialog.append(h, p, footer);
    this.root.append(dialog);
    return new Promise((resolve) => {
      let done = false;
      const finish = (v: boolean) => {
        if (done) return;
        done = true;
        dialog.close();
        dialog.remove();
        resolve(v);
      };
      footer.append(
        workspaceButton("取消", () => finish(false)),
        workspaceButton("放弃修改", () => finish(true)),
        workspaceButton("保存并切换", () => {
          void save()
            .then(finish)
            .catch(() => finish(false));
        }),
      );
      dialog.addEventListener("cancel", () => finish(false));
      dialog.showModal();
    });
  }
  setDrilldown(fn: (key: string) => void, keys?: string[]) {
    this.drill = fn;
    this.drillKeys = keys;
    this.renderMetrics();
  }
  private notify() {
    if (this.applying || this.destroyed) return;
    this.markDirty();
    this.changed?.(structuredClone(this.presentation));
  }
  private async ensureGrid() {
    if (this.grid) return this.ready;
    this.grid = new TabulatorFull(this.table, {
      height: 540,
      layout: "fitData",
      placeholder: "没有符合条件的数据",
      columns: this.columns.map((c) => ({
        ...c,
        minWidth: c.minWidth || 140,
        headerSort: false,
        formatter: c.formatter || "plaintext",
      })),
      data: [],
      movableColumns: true,
      groupToggleElement: "header",
    });
    this.ready = new Promise<void>((resolve) =>
      this.grid!.on("tableBuilt", resolve),
    );
    this.grid.on("columnResized", () => {
      if (this.applying) return;
      this.presentation.widths = Object.fromEntries(
        this.grid!.getColumns().map((c) => [c.getField(), c.getWidth()]),
      );
      this.notify();
    });
    this.grid.on("columnMoved", () => {
      this.presentation.order = this.grid!.getColumns().map((c) =>
        c.getField(),
      );
      this.notify();
    });
    await this.ready;
    if (!this.destroyed) this.applyColumns();
  }
  private applyColumns() {
    if (!this.grid) return;
    this.applying = true;
    for (const field of [
      ...(this.presentation.order ||
        this.columns.map((c) => c.field!).filter(Boolean)),
    ].reverse()) {
      const first = this.grid.getColumns()[0]?.getField();
      if (first && field !== first && this.grid.getColumn(field))
        this.grid.moveColumn(field, first, false);
    }
    for (const c of this.grid.getColumns()) {
      const f = c.getField();
      if ((this.presentation.columns || this.defaultColumns).includes(f))
        c.show();
      else c.hide();
      const originalWidth = this.columns.find(
        (column) => column.field === f,
      )?.width;
      c.setWidth(
        this.presentation.widths?.[f] ||
          (typeof originalWidth === "number" ? originalWidth : true),
      );
    }
    this.applying = false;
  }
  async render(
    rows: Record<string, unknown>[],
    metrics: WorkspaceMetric[],
    groups: string[] = [],
  ) {
    this.rows = rows;
    this.metrics = metrics;
    this.groupFields = groups;
    this.revision++;
    if (this.page === "details") await this.renderTable();
    else this.renderMetrics();
  }
  private async renderTable() {
    const version = this.revision;
    await this.ensureGrid();
    if (this.destroyed || version !== this.revision) return;
    const keyed = this.rows.map((row) => {
      const result = { ...row };
      const values = row.__groupValues as Record<string, unknown> | undefined;
      for (const f of this.groupFields)
        result["__groupKey_" + f] = JSON.stringify(
          values && f in values ? values[f] : row[f],
        );
      return result;
    });
    this.grid!.setGroupBy(this.groupFields.map((f) => "__groupKey_" + f));
    this.grid!.setGroupHeader((_value, _count, data, group) => {
      const f = group.getField().replace(/^__groupKey_/, "");
      const row = data[0] as Record<string, unknown> | undefined;
      const counts = row?.__groupCounts as Record<string, number> | undefined;
      const span = document.createElement("span");
      span.textContent = `${row?.[f] || "未填写"} · ${counts?.[f] ?? "未知"} 人`;
      return span.outerHTML;
    });
    await this.grid!.replaceData(keyed);
    if (this.page === "details") this.grid!.redraw(true);
  }
  private renderMetrics() {
    if (this.destroyed || this.page !== "overview") return;
    const selected =
      this.presentation.metrics || this.metrics.map((m) => m.key);
    this.cards.replaceChildren();
    for (const key of selected) {
      const m = this.metrics.find((m) => m.key === key);
      if (!m || m.distribution) continue;
      const card = document.createElement("div"),
        label = document.createElement("span"),
        value = document.createElement("strong");
      card.className = "dw-card";
      card.dataset.metric = key;
      label.textContent = m.label;
      value.textContent =
        m.value === null ? "未知" : m.value.toLocaleString("zh-CN");
      card.append(label, value);
      const note = document.createElement("small");
      note.textContent =
        m.percentage === undefined
          ? "当前筛选范围"
          : m.percentage === null
            ? "占比未知"
            : `占比 ${m.percentage.toFixed(1)}%`;
      card.append(note);
      if (
        this.drill &&
        (!this.drillKeys || this.drillKeys.includes(key)) &&
        m.value !== null &&
        m.value > 0 &&
        !m.distribution
      ) {
        const b = workspaceButton("查看明细 →", () => this.drill?.(key));
        b.className = "dw-drill";
        b.dataset.drillMetric = key;
        card.append(b);
      }
      this.cards.append(card);
    }
    if (!selected.length) {
      const p = document.createElement("p");
      p.className = "dw-empty";
      p.textContent = "暂未选择指标，可在“配置看板”中添加";
      this.cards.append(p);
    }
    const series = this.metrics.filter(
      (m) =>
        m.key !== "total" &&
        m.key !== "expiring_7d" &&
        !m.distribution &&
        selected.includes(m.key),
    );
    const distribution = this.metrics.find(
      (m) => m.distribution && selected.includes(m.key),
    );
    this.mainCard.hidden = !series.length;
    this.tierCard.hidden = !distribution;
    const option = (
      labels: string[],
      values: (number | null)[],
      color: string,
    ) => ({
      animation: false,
      tooltip: { trigger: "axis", renderMode: "richText" },
      grid: { left: 42, right: 20, top: 35, bottom: 55 },
      xAxis: {
        type: "category",
        data: labels,
        axisTick: { show: false },
        axisLine: { lineStyle: { color: "#dee0e3" } },
        axisLabel: {
          color: "#646a73",
          interval: 0,
          overflow: "break",
          width: 100,
          fontSize: 11,
        },
      },
      yAxis: {
        type: "value",
        minInterval: 1,
        axisLabel: { color: "#8f959e", fontSize: 11 },
        splitLine: { lineStyle: { color: "#eff0f1", type: "dashed" } },
      },
      series: [
        {
          type: "bar",
          data: values,
          barMaxWidth: 40,
          itemStyle: { color, borderRadius: [3, 3, 0, 0] },
        },
      ],
    });
    if (series.length) {
      this.chart ||= init(this.plot);
      this.chart.resize();
      this.chart.setOption(
        option(
          series.map((m) => m.label),
          series.map((m) => m.value),
          "#4e6ef2",
        ),
        true,
      );
    }
    if (distribution) {
      this.tierCard.querySelector("h3")!.textContent = distribution.label;
      this.tierChart ||= init(this.tierPlot);
      this.tierChart.resize();
      this.tierChart.setOption(
        option(
          distribution.distribution!.map((m) => m.label),
          distribution.distribution!.map((m) => m.value),
          "#7b89f5",
        ),
        true,
      );
    }
  }
  openSettings() {
    this.drawerBody.replaceChildren();
    this.drawerTitle.textContent =
      this.page === "overview" ? "配置看板" : "列设置";
    const p = document.createElement("p");
    p.textContent =
      this.page === "overview"
        ? "选择需要展示的预设指标，调整顺序后保存视图。"
        : "设置明细字段的显示状态。列宽和顺序也会随视图保存。";
    this.drawerBody.append(p);
    const metricPage = this.page === "overview";
    const items = metricPage
      ? this.metrics.map((m) => ({ key: m.key, label: m.label }))
      : this.columns
          .filter((c) => c.field)
          .map((c) => ({ key: c.field!, label: String(c.title) }));
    const selected = metricPage
      ? this.presentation.metrics || this.metrics.map((m) => m.key)
      : this.presentation.columns || this.defaultColumns;
    const order = metricPage
      ? selected
      : this.presentation.order || items.map((i) => i.key);
    items.sort(
      (a, b) =>
        (order.includes(a.key) ? order.indexOf(a.key) : 999) -
        (order.includes(b.key) ? order.indexOf(b.key) : 999),
    );
    for (const item of items) {
      const row = document.createElement("label"),
        checkbox = document.createElement("input"),
        name = document.createElement("span");
      row.className = "dw-setting-row";
      checkbox.type = "checkbox";
      checkbox.checked = selected.includes(item.key);
      checkbox.dataset[metricPage ? "metric" : "column"] = item.key;
      name.textContent = item.label;
      row.append(checkbox, name);
      checkbox.onchange = () => {
        const chosen = metricPage
          ? this.presentation.metrics || this.metrics.map((m) => m.key)
          : this.presentation.columns || this.defaultColumns;
        const next = checkbox.checked
          ? [...chosen, item.key]
          : chosen.filter((k) => k !== item.key);
        if (metricPage) this.presentation.metrics = next;
        else this.presentation.columns = next;
        this.applyColumns();
        this.renderMetrics();
        this.notify();
      };
      const up = workspaceButton("↑", () => {
        const next = [
          ...(metricPage
            ? this.presentation.metrics || this.metrics.map((m) => m.key)
            : this.presentation.order || items.map((i) => i.key)),
        ];
        const i = next.indexOf(item.key);
        if (i > 0) {
          [next[i - 1], next[i]] = [next[i], next[i - 1]];
          if (metricPage) this.presentation.metrics = next;
          else this.presentation.order = next;
          this.applyColumns();
          this.renderMetrics();
          this.notify();
          this.openSettings();
        }
      });
      up.setAttribute("aria-label", item.label + "前移");
      row.append(up);
      this.drawerBody.append(row);
    }
    if (!this.drawer.open) this.drawer.showModal();
  }
  async configure(value: WorkspacePresentation) {
    this.presentation = structuredClone(value);
    if (this.ready) await this.ready;
    if (this.destroyed) return;
    this.applyColumns();
    this.renderMetrics();
  }
  showDetails(show: boolean) {
    this.allowedDetails = show;
    this.setPage(this.page, false);
  }
  destroy() {
    this.destroyed = true;
    window.removeEventListener("popstate", this.pop);
    this.observer.disconnect();
    this.chart?.dispose();
    this.tierChart?.dispose();
    this.grid?.destroy();
    this.drawer.remove();
    this.root.replaceChildren();
  }
}
