import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { build } from "esbuild";
import { JSDOM } from "jsdom";
const bundle = (
  await build({
    stdin: {
      contents: `export {H5Controller} from './web/src/h5/controller';export {mount} from './web/src/shared/ui/runtime';`,
      resolveDir: process.cwd(),
    },
    bundle: true,
    write: false,
    format: "iife",
    globalName: "ContactTest",
  })
).outputFiles[0].text;
const shipping = {
  mobile_e164: "+8613800000000",
  recipient_name: "历史收件人",
  province_code: "11",
  province_name: "北京市",
  city_code: "1101",
  city_name: "北京市",
  district_code: "110101",
  district_name: "东城区",
  detail_address: "历史地址",
};
const regions = [
  {
    c: "11",
    n: "北京市",
    ch: [{ c: "1101", n: "北京市", ch: [{ c: "110101", n: "东城区" }] }],
  },
];
const tick = () => new Promise((r) => setTimeout(r, 5));
for (const page of ["all", "one"])
  for (const kind of ["mobile", "shipping_address"]) {
    const dom = new JSDOM('<body><main id="screen"></main></body>', {
      url: `https://crm.test/h5/${page}.html?slug=contacts`,
      runScripts: "outside-only",
      pretendToBeVisual: true,
    });
    const w = dom.window;
    w.Headers = Headers;
    let resolvePrefill;
    const pending = new Promise((resolve) => (resolvePrefill = resolve));
    const posts = [];
    w.fetch = async (url, options = {}) => {
      if (String(url).includes("/session?")) return pending;
      if (options.method === "POST") {
        posts.push(JSON.parse(options.body));
        return Response.json({ code: "unavailable" }, { status: 503 });
      }
      return Response.json(
        {
          id: 1,
          slug: "contacts",
          version: 1,
          title: "收货信息",
          description: "",
          mode: "survey",
          assessment_config: {},
          region_options: regions,
          answer_display_mode: page === "one" ? "one_by_one" : "all_in_one",
          questions: [
            {
              id: 1,
              type: kind,
              title: kind === "mobile" ? "手机号" : "收货信息",
              required: true,
              sort_order: 0,
              options: [],
              contact_field:
                kind === "mobile" ? "contact_phone" : "shipping_contact",
            },
          ],
        },
        { headers: { "X-Aicrm-Survey-Authorization-Context": "a".repeat(64) } },
      );
    };
    w.eval(bundle + ";globalThis.ContactTest=ContactTest;");
    const c = new w.ContactTest.H5Controller(page);
    w.ContactTest.mount(
      w.document.querySelector("#screen"),
      new JSDOM(
        fs.readFileSync(path.join(process.env.AICRM_INSTALLED_PRODUCT_ROOT || process.cwd(), `web/dist/h5/${page}.html`), "utf8"),
      ).window.document.querySelector("#tpl").innerHTML,
      c,
    );
    await c.init();
    if (kind === "shipping_address") {
      const recipient = w.document.querySelector(
        '[data-contact-field="recipient_name"]',
      );
      assert.ok(recipient, "actual template must mount structured shipping");
      recipient.value = "刚填写";
      recipient.dispatchEvent(new w.Event("input", { bubbles: true }));
      recipient.value = "";
      recipient.dispatchEvent(new w.Event("input", { bubbles: true }));
      resolvePrefill(
        Response.json({
          contact_prefill: {
            1: { question_id: 1, shipping_address: shipping },
          },
        }),
      );
      await tick();
      assert.equal(
        recipient.value,
        "",
        "late response must not overwrite explicit clearing",
      );
      assert.equal(
        w.document.querySelector('[data-contact-field="mobile_e164"]').value,
        "13800000000",
        "untouched fields still prefill",
      );
      assert.equal(
        w.document.querySelector('[data-contact-field="district_code"]').value,
        "110101",
      );
      recipient.value = "当前收件人";
      recipient.dispatchEvent(new w.Event("input", { bubbles: true }));
      await c.submit();
      assert.equal(posts.length, 1);
      assert.equal(
        posts[0].answers[0].shipping_address.recipient_name,
        "当前收件人",
      );
      assert.equal(
        posts[0].answers[0].shipping_address.detail_address,
        "历史地址",
      );
      assert.equal(posts[0].answers[0].text_value, undefined);
    } else {
      const input = w.document.querySelector('[data-text-question-id="1"]');
      assert.ok(input);
      input.value = "13900000000";
      input.dispatchEvent(new w.Event("input", { bubbles: true }));
      input.value = "";
      input.dispatchEvent(new w.Event("input", { bubbles: true }));
      resolvePrefill(
        Response.json({
          contact_prefill: { 1: { question_id: 1, text_value: "13800000000" } },
        }),
      );
      await tick();
      assert.equal(input.value, "");
      assert.equal(c.textAnswers.get(1), "");
      input.value = "13900000000";
      input.dispatchEvent(new w.Event("input", { bubbles: true }));
      await c.submit();
      assert.equal(posts[0].answers[0].text_value, "13900000000");
    }
    dom.window.close();
  }
const sidebarCode = (
  await build({
    entryPoints: ["web/v3/sidebar/contactProfile.ts"],
    bundle: true,
    write: false,
    format: "iife",
    globalName: "SidebarContact",
  })
).outputFiles[0].text;
for (const role of ["viewer", "admin"]) {
  const dom = new JSDOM(
    '<main id="sidebar-workbench-root"><section class="profile-card"></section></main>',
    { url: "https://crm.test/sidebar", runScripts: "outside-only" },
  );
  const w = dom.window;
  let reveals = 0;
  w.fetch = async (url) => {
    if (String(url).includes("cli/session"))
      return Response.json({ roles: [role] });
    reveals++;
    return Response.json({
      mobile_e164: shipping.mobile_e164,
      shipping_address: shipping,
    });
  };
  w.eval(sidebarCode + ";globalThis.SidebarContact=SidebarContact;");
  w.SidebarContact.mountSidebarContacts(
    {
      contact_phone_masked: "138****0000",
      contact_shipping_summary: "历* 北京市东城区 详细地址已保护",
    },
    "42",
    () => {},
  );
  await tick();
  assert.equal(w.document.body.textContent.includes("历史地址"), false);
  const button = w.document.querySelector(".contact-reveal");
  if (role === "viewer") assert.equal(button, null);
  else {
    button.click();
    await tick();
    assert.equal(reveals, 1);
    assert.ok(w.document.body.textContent.includes("历史地址"));
    button.click();
    assert.equal(w.document.body.textContent.includes("历史地址"), false);
  }
  dom.window.close();
}
console.log(
  "contact prefill: both questionnaire modes, phone/shipping, late clear, partial fill, current submitted values, sidebar reveal scope passed",
);
