import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { build } from "esbuild";
const compiled = await build({
  entryPoints: ["web/v3/fieldMappingEditor.ts"],
  bundle: true,
  write: false,
  format: "iife",
  globalName: "MappingUI",
});
const dom = new JSDOM('<div id="editor"></div>', {
  runScripts: "outside-only",
});
dom.window.eval(compiled.outputFiles[0].text + ";window.MappingUI=MappingUI;");
const calls = [];
let late;
const editor = dom.window.MappingUI.createFieldMappingEditor(
  dom.window.document.querySelector("#editor"),
  {
    preview: async (mapping) => {
      calls.push(mapping);
      return {
        payload_json: '{"preview":true}',
        synthetic: true,
        real_external_call_executed: false,
      };
    },
  },
);
editor.setMapping({
  version: 1,
  fields: [
    {
      key: "phone",
      source: "variable",
      value_type: "string",
      variable: "order.mobile",
    },
    { key: "active", source: "fixed", value_type: "boolean", value: false },
    { key: "count", source: "fixed", value_type: "number", value: 2 },
    { key: "empty", source: "fixed", value_type: "null", value: null },
  ],
});
assert.equal(editor.getMapping().fields[1].value, false);
assert.equal(editor.getMapping().fields[2].value, 2);
assert.equal(editor.getMapping().fields[3].value, null);
assert.equal(editor.getMapping().fields[0].variable, "order.mobile");
const d = dom.window.document;
d.querySelector(".fm-variable").click();
const search = d.querySelector('[aria-label="搜索变量"]');
search.value = "金额";
search.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
assert.equal(d.querySelectorAll(".fm-choice").length, 3, "typing a variable query keeps the candidate list as a draft");
search.dispatchEvent(new dom.window.CompositionEvent("compositionstart", { bubbles: true }));
search.dispatchEvent(new dom.window.CompositionEvent("compositionend", { bubbles: true }));
const candidateEnter = new dom.window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter" });
Object.defineProperty(candidateEnter, "keyCode", { value: 229 });
search.dispatchEvent(candidateEnter);
assert.equal(candidateEnter.defaultPrevented, false, "IME candidate Enter does not redraw variable choices");
await new Promise((resolve) => setTimeout(resolve, 0));
search.dispatchEvent(new dom.window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter" }));
assert.equal(d.querySelectorAll(".fm-choice").length, 1);
d.querySelector(".fm-choice").click();
assert.equal(editor.getMapping().fields[0].variable, "order.paid_amount_minor");
assert.equal(editor.getMapping().fields[0].value_type, "number");
const keys = d.querySelectorAll('[aria-label="字段名"]');
keys[1].value = "phone";
assert.throws(() => editor.getMapping(), /标红/);
assert.match(d.querySelectorAll(".fm-error")[1].textContent, /重复/);
keys[1].value = "__proto__";
assert.throws(() => editor.getMapping(), /标红/);
keys[1].value = "active";
const values = d.querySelectorAll('[aria-label="固定值"]');
values[2].value = "9007199254740993";
assert.throws(() => editor.getMapping(), /标红/);
values[2].value = "2";
editor.refresh();
await new Promise((resolve) => setTimeout(resolve, 250));
assert.equal(calls.length, 1);
assert.match(d.querySelector("[data-fm-preview]").textContent, /preview/);
editor.destroy();
dom.window.close();
console.log(
  "field mapping editor: typed values, variables, validation, synthetic preview PASS",
);
