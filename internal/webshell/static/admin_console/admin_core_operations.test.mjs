import { JSDOM } from "jsdom";
import fs from "node:fs";
import assert from "node:assert/strict";
const script = fs.readFileSync(
  new URL("./admin_core_operations.js", import.meta.url),
  "utf8",
);
const calls = [];
let confirmations = 0;
const dom = new JSDOM(
  '<nav><a href="?tab=products" data-audience-workspace-tab="products">核心产品配置</a><a href="?tab=packages" data-audience-workspace-tab="packages">人群包管理</a></nav><section id="coreProductPanel"><div id="coreOperationsRoot"></div></section><section id="audiencePackagePanel">原有人群包</section>',
  {
    runScripts: "outside-only",
    url: "https://crm.test/admin/automation-conversion",
  },
);
dom.window.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
dom.window.HTMLDialogElement.prototype.close = function () {
  this.open = false;
  this.dispatchEvent(new dom.window.Event("close"));
};
dom.window.AICRMConfirmation = {
  confirm: async () => {
    confirmations++;
    return true;
  },
};
dom.window.AudienceOperationsHTTP = {
  errorState: () => ({ message: "读取失败" }),
  request: async (path, options = {}) => {
    calls.push({ path, options });
    if (path.includes("core/product-options")) return { data: { items: [{ code: "course-88", name: "销售课程", product_type: "standard" }], total: 1 } };
    if (path.endsWith("core/products"))
      return {
        data: options.body
          ? { ...options.body.product, version: 1 }
          : [
              {
                id: 1,
                name: "创业课",
                package_id: 8,
                description: "创业入门",
                enabled: true,
                version: 1,
              },
            ],
      };
    if (path.endsWith("core/prompt/history"))
      return { data: [{ id: 1, body: "历史判断规则" }] };
    if (path.endsWith("core/prompt"))
      return {
        data: {
          draft: "当前草稿",
          version: 2,
          published_id: 1,
          published_body: "历史判断规则",
          ...(options.body ? { version: 3 } : {}),
        },
      };
    if (path.includes("packages?"))
      return {
        items: [
          { id: 8, name: "现有人群包" },
          { id: 9, name: "新客人群包" },
        ],
      };
    if (path.startsWith("/api/admin/customers?keyword="))
      return { items: [{ customer_id: 42, customer_number: "7" }], total: 1 };
    if (path.endsWith("core/recommendations"))
      return {
        data: { items: [{ id: 11, customer_id: 7, state: "accepted" }] },
      };
    if (path.endsWith("core/recommendations/11"))
      return {
        data: {
          id: 11,
          customer_id: 7,
          state: "previewed",
          chosen_product_id: 1,
          reason: "符合入门阶段",
          evidence: "问卷答案",
        },
      };
    throw new Error("unexpected " + path);
  },
};
dom.window.eval(fs.readFileSync(new URL("./admin_search_select.js", import.meta.url), "utf8"));
dom.window.eval(script);
const settle = () => new Promise((r) => setTimeout(r, 10));
await settle();
const document = dom.window.document;
const button = (text) =>
  [...document.querySelectorAll("button")].find((b) => b.textContent === text);
assert.equal(document.querySelector("#coreProductPanel").hidden, true);
assert.equal(document.querySelector("#audiencePackagePanel").hidden, false);
assert.equal(document.querySelectorAll("#coreOperationsRoot form").length, 0);
assert.equal(document.querySelectorAll(".core-steps button").length, 3);
button("2 编写分配规则").click();
await settle();
const editor = document.querySelector("#corePromptEditor");
editor.value = "尚未保存的规则";
document.querySelector('[data-audience-workspace-tab="packages"]').click();
assert.equal(document.querySelector("#coreProductPanel").hidden, true);
assert.equal(document.querySelector("#audiencePackagePanel").hidden, false);
assert.equal(
  new URL(dom.window.location.href).searchParams.get("tab"),
  "packages",
);
document.querySelector('[data-audience-workspace-tab="products"]').click();
assert.equal(document.querySelector("#audiencePackagePanel").hidden, true);
assert.equal(editor.value, "尚未保存的规则");
dom.window.history.replaceState(null, "", "?tab=packages");
dom.window.dispatchEvent(new dom.window.PopStateEvent("popstate"));
assert.equal(document.querySelector("#coreProductPanel").hidden, true);
document.querySelector('[data-audience-workspace-tab="products"]').click();

button("1 配置产品").click();
await settle();
button("新增产品").click();
await settle();
const form = document.querySelector("dialog form");
form.querySelector("input").value = "成长课";
form.querySelector("textarea").value = "需要成长的客户";
form.querySelector("select").value = "9";
assert.equal(form.querySelector('option[value="8"]').disabled, true);
const sales = form.querySelector('[aria-label="关联销售商品"]');
assert.match(sales.textContent, /销售课程 · course-88/);
sales.value = "course-88"; sales.dispatchEvent(new dom.window.Event("change"));
button("保存产品").click();
await settle();
assert.equal(document.querySelector("dialog"), null);
assert.equal(calls.find(c => c.path.endsWith("core/products") && c.options.body).options.body.product.product_reference, "course-88");
assert.equal(editor.value, "尚未保存的规则");
assert.equal(
  document.querySelectorAll(".core-product-table tbody tr").length,
  2,
);
document.querySelector('[aria-label="编辑 创业课"]').click();
await settle();
assert.equal(document.querySelector("dialog select").disabled, true);
button("取消").click();
button("2 编写分配规则").click();
await settle();
const history = document.querySelector("#core-step-2 select");
history.value = "1";
history.dispatchEvent(new dom.window.Event("change"));
assert.equal(editor.value, "历史判断规则");
button("3 验证并使用").click();
await settle();
const customers = document.querySelector("#coreCustomerIDs");
customers.value = "7，7";
button("试运行（不入包）").click();
await settle();
const mutations = calls.filter(
  (c) => c.options.method === "POST" && !c.path.endsWith("core/products"),
);
assert.equal(mutations.length, 2);
assert.equal(mutations[0].options.body.publish, false);
assert.equal(mutations[0].options.body.body, "历史判断规则");
assert.equal(mutations[1].options.body.preview, true);
assert.deepEqual(Array.from(mutations[1].options.body.customer_ids), [42]);
assert.match(document.querySelector(".core-results").textContent, /等待处理/);
button("刷新结果").click();
await settle();
assert.match(
  document.querySelector(".core-results").textContent,
  /创业课.*仅验证/s,
);
assert.match(
  document.querySelector(".core-results").textContent,
  /判断依据：问卷答案/,
);
assert.ok(
  !document.querySelector(".core-results").textContent.includes("previewed"),
);
button("正式分配客户").click();
await settle();
assert.equal(confirmations, 1);
assert.equal(calls.at(-1).options.body.preview, false);
assert.ok(!calls.some((c) => c.path.endsWith("core/assignments")));
dom.window.close();
console.log(
  "core operations: Chinese guided configuration, draft preservation, immutable binding, preview and confirmed assignment passed",
);

// A failed directory read must not silently remove the stored association.
const pickerDOM = new JSDOM('<body></body>', { runScripts: 'outside-only' });
pickerDOM.window.eval(fs.readFileSync(new URL('./admin_search_select.js', import.meta.url), 'utf8'));
let failDirectory = true;
const picker = pickerDOM.window.AICRMSearchSelect({ value: 'saved-course', label: '关联销售商品', loadPage: async () => {
  if (failDirectory) throw new Error('unavailable');
  return { total: 1, items: [{ value: 'saved-course', label: '已售课程 · saved-course' }] };
}});
pickerDOM.window.document.body.append(picker.element);
await settle();
assert.equal(picker.value, 'saved-course');
assert.match(picker.element.textContent, /读取失败/);
failDirectory = false;
[...picker.element.querySelectorAll('button')].find(b => b.textContent === '重试').click();
await settle();
assert.match(picker.element.textContent, /已售课程/);
[...picker.element.querySelectorAll('button')].find(b => b.textContent === '清空选择').click();
assert.equal(picker.value, '');
pickerDOM.window.close();
