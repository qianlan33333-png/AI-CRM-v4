export type ValueType = "string" | "number" | "boolean" | "null" | "json";
export type MappingField = {
  key: string;
  source: "fixed" | "variable";
  value_type: ValueType;
  value?: unknown;
  variable?: string;
};
export type FieldMapping = { version: 1; fields: MappingField[] };
export type MappingPreview = {
  payload_json: string;
  synthetic: true;
  real_external_call_executed: false;
};
const variables = [
  { key: "order.mobile", label: "订单手机号", type: "string" },
  { key: "payer.nickname", label: "付款人昵称", type: "string" },
  {
    key: "order.paid_amount_minor",
    label: "订单实付金额（分）",
    type: "number",
  },
] as const;
const styles = `.fm-layout{display:grid;grid-template-columns:minmax(0,3fr) minmax(250px,2fr);gap:20px;align-items:start}.fm-editor,.fm-preview{min-width:0;border:1px solid #e5e7eb;border-radius:12px;padding:18px;background:#fff}.fm-header{font-size:16px;font-weight:600;margin:0 0 14px}.fm-row{display:grid;grid-template-columns:minmax(90px,1fr) 100px minmax(130px,2fr) 36px;gap:8px;padding:12px 0;border-bottom:1px solid #edf0f3;align-items:start}.fm-control{box-sizing:border-box;width:100%;min-width:0;min-height:38px;border:1px solid #d9dee8;border-radius:8px;padding:8px;font:inherit;background:#fff;color:#202939}.fm-button{min-height:36px;padding:8px 12px;border:1px solid #d9dee8;border-radius:8px;background:#fff;color:#245bdb;cursor:pointer;font:inherit}.fm-value{display:grid;gap:6px;min-width:0}.fm-variable{background:#edf4ff;color:#245bdb;overflow-wrap:anywhere;text-align:left}.fm-popover{position:relative;border:1px solid #d9dee8;padding:10px;border-radius:10px;background:#fff;box-shadow:0 8px 24px #14213d16;display:grid;gap:6px}.fm-popover[hidden]{display:none}.fm-choice{display:block;width:100%;text-align:left}.fm-error{grid-column:1/-1;color:#b42318;font-size:12px;line-height:1.6}.fm-hint{font-size:12px;color:#667085;line-height:1.7;margin:8px 0}.fm-preview{background:#f7f9fc;position:sticky;top:16px}.fm-json{white-space:pre-wrap;overflow-wrap:anywhere;font:13px/1.7 ui-monospace,monospace;margin:0;max-height:540px;overflow:auto}.fm-preview-state{font-size:12px;color:#667085;margin-bottom:10px}.fm-labels{display:grid;grid-template-columns:minmax(90px,1fr) 100px minmax(130px,2fr) 36px;gap:8px;font-size:12px;color:#667085}.fm-actions{display:flex;gap:8px;margin-top:14px;flex-wrap:wrap}@media(max-width:850px){.fm-layout{grid-template-columns:1fr}.fm-preview{position:static}}@media(max-width:520px){.fm-row,.fm-labels{grid-template-columns:1fr 90px}.fm-value{grid-column:1/-1}.fm-editor{padding:12px}}`;
function safeNumbers(value: unknown): boolean {
  if (typeof value === "number")
    return (
      Number.isFinite(value) &&
      (!Number.isInteger(value) || Number.isSafeInteger(value))
    );
  if (Array.isArray(value)) return value.every(safeNumbers);
  return (
    !value ||
    typeof value !== "object" ||
    Object.values(value).every(safeNumbers)
  );
}
export function createFieldMappingEditor(
  container: HTMLElement,
  options: {
    preview(mapping: FieldMapping): Promise<MappingPreview>;
    onChange?(): void;
  },
) {
  installCommittedTextSearch();
  const doc = container.ownerDocument;
  if (!doc.getElementById("field-mapping-styles")) {
    const style = doc.createElement("style");
    style.id = "field-mapping-styles";
    style.textContent = styles;
    doc.head.append(style);
  }
  container.classList.add("fm-layout");
  container.innerHTML =
    '<section class="fm-editor"><h4 class="fm-header">推送字段</h4><div class="fm-labels"><span>字段名</span><span>来源</span><span>值</span></div><div data-fm-rows></div><div class="fm-actions"><button type="button" class="fm-button" data-fm-add>＋ 添加字段</button></div><p class="fm-hint">固定值按所选类型发送；变量缺失时发送 null。金额单位为分。</p></section><aside class="fm-preview"><h4 class="fm-header">JSON 预览</h4><div class="fm-preview-state" role="status">使用模拟数据，不发送请求到推送地址</div><pre class="fm-json" data-fm-preview></pre></aside>';
  const rows = container.querySelector<HTMLElement>("[data-fm-rows]")!;
  const preview = container.querySelector<HTMLElement>("[data-fm-preview]")!;
  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "fm-button";
  copy.textContent = "复制 JSON";
  copy.onclick = async () => {
    if (!preview.textContent) return;
    try {
      await navigator.clipboard.writeText(preview.textContent);
      copy.textContent = "已复制";
    } catch {
      copy.textContent = "复制失败，请选择下方 JSON 复制";
    }
  };
  preview.before(copy);
  const status = container.querySelector<HTMLElement>("[role=status]")!;
  let revision = 0,
    timer: ReturnType<typeof setTimeout> | undefined;
  const node = <K extends keyof HTMLElementTagNameMap>(
    tag: K,
    className: string,
  ) => {
    const el = doc.createElement(tag);
    el.className = className;
    return el;
  };
  const select = (values: [string, string][]) => {
    const el = node("select", "fm-control");
    for (const [value, label] of values) {
      const option = doc.createElement("option");
      option.value = value;
      option.textContent = label;
      el.append(option);
    }
    return el;
  };
  type Row = {
    element: HTMLElement;
    key: HTMLInputElement;
    source: HTMLSelectElement;
    type: HTMLSelectElement;
    value: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;
    variable: string;
    error: HTMLElement;
  };
  const items: Row[] = [];
  const getMapping = (): FieldMapping => {
    const used = new Set<string>();
    let invalid = false;
    const fields = items.map((row) => {
      row.error.textContent = "";
      const key = row.key.value;
      const field: MappingField = {
        key,
        source: row.source.value as MappingField["source"],
        value_type: row.type.value as ValueType,
      };
      try {
        if (!key) throw new Error("请填写字段名");
        if (
          key.trim() !== key ||
          /[\u0000-\u001f\u007f]/.test(key) ||
          encodeURIComponent(key).replace(/%[0-9A-F]{2}/gi, "x").length > 128 ||
          ["__proto__", "constructor", "prototype"].includes(key)
        )
          throw new Error("字段名格式无效或超出128字节");
        if (used.has(key)) throw new Error("字段名不能重复");
        used.add(key);
        if (field.source === "variable") {
          const variable = variables.find((v) => v.key === row.variable);
          if (!variable) throw new Error("请选择变量");
          field.variable = variable.key;
          field.value_type = variable.type;
        } else {
          const text = row.value.value;
          if (field.value_type === "string") field.value = text;
          else if (field.value_type === "null") field.value = null;
          else if (field.value_type === "boolean") {
            if (!["true", "false"].includes(text.trim()))
              throw new Error("布尔值请填写 true 或 false");
            field.value = text.trim() === "true";
          } else {
            try {
              field.value = JSON.parse(text);
            } catch {
              throw new Error(
                field.value_type === "number"
                  ? "请填写有效数字"
                  : "请填写有效 JSON",
              );
            }
            if (
              field.value_type === "number" &&
              typeof field.value !== "number"
            )
              throw new Error("请填写有效数字");
            if (
              field.value_type === "json" &&
              (!field.value || typeof field.value !== "object")
            )
              throw new Error("JSON 值必须是对象或数组");
            if (!safeNumbers(field.value))
              throw new Error("数字超出浏览器安全范围，请缩小数值");
          }
        }
      } catch (error) {
        row.error.textContent =
          error instanceof Error ? error.message : "字段无效";
        invalid = true;
      }
      return field;
    });
    if (invalid) throw new Error("请修正标红字段后保存");
    return { version: 1, fields };
  };
  const refresh = () => {
    const current = ++revision;
    if (timer) clearTimeout(timer);
    options.onChange?.();
    let mapping: FieldMapping;
    try {
      mapping = getMapping();
    } catch {
      preview.textContent = "";
      status.textContent = "请修正左侧字段";
      return;
    }
    status.textContent = "正在生成模拟预览…";
    timer = setTimeout(
      () =>
        void options
          .preview(mapping)
          .then((result) => {
            if (current !== revision) return;
            if (
              result.synthetic !== true ||
              result.real_external_call_executed !== false ||
              typeof result.payload_json !== "string"
            )
              throw new Error("预览响应无效");
            preview.textContent = result.payload_json;
            status.textContent = "模拟数据 · 缺失变量为 null · 金额单位为分";
          })
          .catch((error) => {
            if (current !== revision) return;
            preview.textContent = "";
            status.textContent =
              error instanceof Error
                ? error.message
                : "预览失败，请重新编辑后重试";
          }),
      200,
    );
  };
  const add = (field?: MappingField) => {
    const element = node("div", "fm-row");
    const key = node("input", "fm-control");
    key.placeholder = "例如 mobile";
    key.setAttribute("aria-label", "字段名");
    key.value = field?.key || "";
    const source = select([
      ["fixed", "固定值"],
      ["variable", "变量"],
    ]);
    source.value = field?.source || "fixed";
    source.setAttribute("aria-label", "值来源");
    const valueWrap = node("div", "fm-value");
    const type = select([
      ["string", "字符串"],
      ["number", "数字"],
      ["boolean", "布尔"],
      ["null", "空值 null"],
      ["json", "JSON"],
    ]);
    type.value = field?.value_type || "string";
    type.setAttribute("aria-label", "值类型");
    let value: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement =
      node("input", "fm-control");
    value.setAttribute("aria-label", "固定值");
    value.value =
      field?.value_type === "string"
        ? String(field.value ?? "")
        : field?.value === undefined
          ? ""
          : JSON.stringify(field.value);
    const variableButton = node("button", "fm-button fm-variable");
    variableButton.type = "button";
    const popover = node("div", "fm-popover");
    popover.hidden = true;
    const search = node("input", "fm-control");
    search.placeholder = "搜索变量";
    search.setAttribute("aria-label", "搜索变量");
    search.dataset.fieldMappingVariableSearch = "";
    const choices = node("div", "");
    popover.append(search, choices);
    const remove = node("button", "fm-button");
    remove.type = "button";
    remove.textContent = "×";
    remove.setAttribute("aria-label", "删除字段");
    const error = node("div", "fm-error");
    error.setAttribute("role", "alert");
    const row: Row = {
      element,
      key,
      source,
      type,
      value,
      variable: field?.variable || "",
      error,
    };
    items.push(row);
    const renderChoices = () => {
      choices.replaceChildren();
      for (const option of variables.filter((v) =>
        (v.key + " " + v.label)
          .toLowerCase()
          .includes(search.value.toLowerCase()),
      )) {
        const b = node("button", "fm-button fm-choice");
        b.type = "button";
        b.textContent = option.label + " · " + option.key;
        b.onclick = () => {
          row.variable = option.key;
          popover.hidden = true;
          update();
          refresh();
        };
        choices.append(b);
      }
    };
    const update = () => {
      const variable = source.value === "variable";
      const desired =
        type.value === "json"
          ? "TEXTAREA"
          : type.value === "boolean"
            ? "SELECT"
            : "INPUT";
      if (value.tagName !== desired) {
        const previous = value.value;
        const next =
          desired === "SELECT"
            ? select([
                ["true", "true"],
                ["false", "false"],
              ])
            : desired === "TEXTAREA"
              ? node("textarea", "fm-control")
              : node("input", "fm-control");
        next.setAttribute("aria-label", "固定值");
        next.value =
          desired === "SELECT"
            ? previous === "false"
              ? "false"
              : "true"
            : previous;
        next.addEventListener("input", refresh);
        next.addEventListener("change", refresh);
        value.replaceWith(next);
        value = next;
        row.value = next;
      }
      type.hidden = variable;
      value.hidden = variable || type.value === "null";
      variableButton.hidden = !variable;
      variableButton.textContent =
        variables.find((v) => v.key === row.variable)?.label || "选择变量";
      if (!variable) popover.hidden = true;
    };
    variableButton.onclick = () => {
      popover.hidden = !popover.hidden;
      if (!popover.hidden) {
        renderChoices();
        search.focus();
      }
    };
    search.oninput = renderChoices;
    search.onkeydown = (event) => {
      if (event.key === "Escape") {
        popover.hidden = true;
        variableButton.focus();
      }
    };
    for (const input of [key, value]) input.addEventListener("input", refresh);
    for (const control of [source, type])
      control.addEventListener("change", () => {
        update();
        refresh();
      });
    remove.onclick = () => {
      items.splice(items.indexOf(row), 1);
      element.remove();
      refresh();
    };
    valueWrap.append(type, value, variableButton, popover);
    element.append(key, source, valueWrap, remove, error);
    rows.append(element);
    update();
  };
  container.querySelector<HTMLButtonElement>("[data-fm-add]")!.onclick = () => {
    add();
    refresh();
  };
  return {
    getMapping,
    setMapping(mapping: FieldMapping) {
      if (
        mapping.version !== 1 ||
        !Array.isArray(mapping.fields) ||
        mapping.fields.some(
          (field) =>
            !field ||
            typeof field.key !== "string" ||
            !["fixed", "variable"].includes(field.source) ||
            !["string", "number", "boolean", "null", "json"].includes(
              field.value_type,
            ),
        )
      )
        throw new Error("不支持的字段映射版本或字段");
      rows.replaceChildren();
      items.splice(0);
      for (const field of mapping.fields) add(field);
      refresh();
    },
    refresh,
    destroy() {
      revision++;
      if (timer) clearTimeout(timer);
      container.replaceChildren();
    },
  };
}
import { installCommittedTextSearch } from "./shared/ui/committedTextSearch";
