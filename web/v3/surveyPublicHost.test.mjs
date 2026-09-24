import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const page = fs.readFileSync(path.join(root, "web/dist/h5/all.html"), "utf8");
const authHost = await buildTestBrowserBundle(
  path.join(root, "web/v3/h5AuthAdapter.ts"),
);
const publicHost = await buildTestBrowserBundle(
  path.join(root, "web/v3/surveyPublicHost.ts"),
);
const runtime = await buildTestBrowserBundle(
  path.join(root, "web/src/h5/main.ts"),
);
const dom = new JSDOM(page, {
  url: "https://test.invalid/h5/all.html?slug=survey",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});

dom.window.eval(authHost);
dom.window.eval(publicHost);
const document = dom.window.document;
const screen = document.getElementById("screen");
const template = document.getElementById("tpl");
assert.ok(screen && template, "release H5 screen and template must remain");
assert.equal(
  document.body.dataset.v3PublicSurvey,
  "all",
  "public Survey Host scopes itself to the actual answer page",
);
const inNestedTemplate = (root, selector) => {
  const found = [...root.querySelectorAll(selector)];
  for (const child of root.querySelectorAll("template"))
    found.push(...inNestedTemplate(child.content, selector));
  return found;
};
assert.ok(
  inNestedTemplate(template.content, "[data-question-id]")[0]?.hasAttribute(
    "data-v3-survey-question",
  ),
  "question cards receive a V3 presentation hook before the frozen runtime mounts",
);
assert.ok(
  inNestedTemplate(template.content, "[data-h5-submit]")[0]?.hasAttribute(
    "data-v3-survey-action",
  ),
  "existing submit action receives the V3 presentation hook",
);
assert.equal(
  inNestedTemplate(template.content, "[data-h5-error]")[0]?.getAttribute(
    "aria-live",
  ),
  "assertive",
  "existing validation and submission failures remain announced",
);
assert.ok(
  inNestedTemplate(template.content, "[data-v3-survey-submitting]")[0],
  "both frozen answer templates receive a stable submitting marker bound to the Owner state",
);

screen.innerHTML =
  '<button data-h5-submit>提交</button><p data-v3-survey-submitting role="status">正在提交，请勿重复操作…</p>';
await new Promise((resolve) => setTimeout(resolve, 0));
const submit = screen.querySelector("[data-h5-submit]");
assert.ok(
  submit instanceof dom.window.HTMLButtonElement,
  "rendered submit control must exist",
);
assert.equal(
  submit.disabled,
  true,
  "rendered submitting state disables the existing submit control",
);
assert.equal(
  submit.getAttribute("aria-busy"),
  "true",
  "rendered submitting state exposes an accessible busy state",
);
assert.equal(
  screen.dataset.v3SurveySubmitting,
  "true",
  "screen records only the existing submitting state",
);

screen.innerHTML =
  "<button data-h5-submit>提交</button><div data-h5-error>提交失败；未修改答案时可安全重试</div>";
await new Promise((resolve) => setTimeout(resolve, 0));
const recovered = screen.querySelector("[data-h5-submit]");
assert.ok(
  recovered instanceof dom.window.HTMLButtonElement,
  "retry submit control must exist",
);
assert.equal(
  recovered.disabled,
  false,
  "error recovery restores the existing retry action without clearing rendered inputs",
);
assert.equal(
  screen.querySelector("[data-h5-error]")?.getAttribute("aria-live"),
  "assertive",
  "failure recovery keeps the existing error announcement",
);

screen.innerHTML =
  '<button data-h5-submit>提交</button><div data-h5-error>请求失败（HTTP 503）</div>';
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(
  screen.querySelector("[data-v3-survey-recovery]")?.textContent,
  "暂时无法完成操作，请保留当前页面并稍后重试。",
  "an exact transport status receives a recoverable explanation without inferring the failed operation",
);
assert.equal(
  screen.querySelector("[data-v3-survey-error-detail]")?.textContent,
  "问题详情：HTTP 503",
  "the technical transport status remains available as secondary detail",
);
assert.ok(
  screen.querySelector("[data-h5-submit]"),
  "the existing retry action remains available beside the V3 error explanation",
);

for (const [raw, expected, detail] of [
  ["登录状态已失效，请重新登录", "登录或授权已失效，当前答案尚未提交。请重新授权后填写问卷。", "问题详情：HTTP 401"],
  ["当前账号无权执行此操作", "当前无权限访问此问卷，请联系管理员确认访问权限。", "问题详情：HTTP 403"],
]) {
  screen.innerHTML = `<button data-h5-submit>提交</button><div data-h5-error>${raw}</div>`;
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(screen.querySelector("[data-v3-survey-recovery]")?.textContent, expected, `${detail} keeps its authorization meaning`);
  assert.equal(screen.querySelector("[data-v3-survey-error-detail]")?.textContent, detail, `${detail} remains a secondary diagnostic`);
  assert.ok(screen.querySelector("[data-h5-submit]"), `${detail} does not clear the Owner retry control or draft`);
  if (detail.includes('401')) {
    assert.equal(screen.querySelector('[data-v3-survey-reauthorize]')?.getAttribute('href'), '/h5/auth.html?slug=survey', 'expired session has one explicit login action');
    assert.equal(screen.querySelector('[data-h5-submit]')?.disabled, true, 'expired session cannot resubmit until login');
  }
}

screen.innerHTML = "<button data-h5-submit>提交</button><div data-h5-error>问卷已停止填写</div>";
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(screen.querySelector("[data-v3-survey-recovery]"), null, "Owner domain errors retain their own wording rather than being presented as transport recovery");
assert.match(screen.querySelector("[data-h5-error]")?.textContent || "", /问卷已停止填写/, "Owner domain error remains visible");

screen.innerHTML = "<button data-h5-submit disabled>提交</button>";
await new Promise((resolve) => setTimeout(resolve, 0));
const ownerDisabled = screen.querySelector("[data-h5-submit]");
assert.ok(
  ownerDisabled instanceof dom.window.HTMLButtonElement,
  "Owner-disabled submit control must exist",
);
assert.equal(
  ownerDisabled.disabled,
  true,
  "presentation never releases an Owner-disabled submit control",
);
assert.equal(
  ownerDisabled.getAttribute("aria-busy"),
  null,
  "no pending state is inferred from button copy or disabled state",
);

screen.innerHTML = "<button data-h5-submit>提交</button>";
await new Promise((resolve) => setTimeout(resolve, 0));
const retry = screen.querySelector("[data-h5-submit]");
assert.ok(
  retry instanceof dom.window.HTMLButtonElement,
  "fresh retry control must exist",
);
retry.dispatchEvent(
  new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }),
);
assert.equal(
  retry.disabled,
  true,
  "first user click receives an immediate visual duplicate-click lock",
);
assert.equal(
  retry.getAttribute("aria-disabled"),
  "true",
  "duplicate-click lock is visible to assistive technology",
);
dom.window.close();

const resultPage = fs.readFileSync(path.join(root, "web/dist/h5/result.html"), "utf8");
const resultDOM = new JSDOM(resultPage, {
  url: "https://test.invalid/h5/result.html",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
resultDOM.window.eval(authHost);
resultDOM.window.eval(publicHost);
const resultTemplate = resultDOM.window.document.getElementById("tpl");
assert.ok(resultTemplate, "release result template must remain available");
assert.equal(
  inNestedTemplate(resultTemplate.content, "[data-v3-survey-internal-receipt-detail]").length,
  2,
  "only the fixed internal processing-scope receipt pair receives the V3 presentation marker",
);
const resultScreen = resultDOM.window.document.getElementById("screen");
resultScreen.innerHTML = '<div data-h5-result>提交已确认</div><div><div><span data-v3-survey-internal-receipt-detail>处理范围</span><strong data-v3-survey-internal-receipt-detail>仅本地处理，未执行外部效果</strong><span>提交时间</span><strong>2026-09-15 12:00</strong><span>问卷版本</span><strong>v2</strong></div><div>提交编号 42</div></div>';
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(
  resultScreen.textContent.includes("处理范围"),
  false,
  "rendered result omits only the diagnostic processing-scope line",
);
assert.ok(
  resultScreen.textContent.includes("提交时间") && resultScreen.textContent.includes("问卷版本") && resultScreen.textContent.includes("提交编号 42"),
  "result receipt preserves the traceable Owner facts",
);
resultDOM.window.close();

const donePage = fs.readFileSync(path.join(root, "web/dist/h5/done.html"), "utf8");
const doneDOM = new JSDOM(donePage, {
  url: "https://test.invalid/h5/done.html?slug=survey",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
doneDOM.window.eval(authHost);
doneDOM.window.eval(publicHost);
const doneScreen = doneDOM.window.document.getElementById("screen");
assert.equal(doneDOM.window.document.body.dataset.v3PublicSurvey, "done", "completion page is included in the public Survey Host");
doneScreen.innerHTML = '<section data-h5-done role="status" aria-live="polite" tabindex="-1"><h1>收到你的问卷</h1></section>';
await new Promise((resolve) => setTimeout(resolve, 0));
const doneCard = doneScreen.querySelector("[data-h5-done]");
assert.equal(doneCard?.getAttribute("data-v3-survey-done"), "", "rendered completion confirmation receives the V3 presentation hook");
assert.equal(doneDOM.window.document.activeElement, doneCard, "rendered completion confirmation receives focus after a confirmed submission");
doneDOM.window.close();

const failurePage = fs.readFileSync(path.join(root, "web/dist/h5/error.html"), "utf8");
const renderFailure = async (url) => {
  const failureDOM = new JSDOM(failurePage, {
    url,
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  failureDOM.window.eval(authHost);
  failureDOM.window.eval(publicHost);
  failureDOM.window.eval(runtime);
  await new Promise((resolve) => setTimeout(resolve, 0));
  return failureDOM;
};

for (const [code, title, message] of [
  ["survey_oauth_unavailable", "暂时无法继续", "当前问卷暂时不能完成微信授权"],
  ["survey_oauth_failed", "授权失败", "当前链接无法继续"],
  ["survey_identity_conflict", "无法继续", "当前微信身份与问卷状态不一致"],
]) {
  const failureDOM = await renderFailure(
    `https://test.invalid/h5/error.html?code=${code}`,
  );
  const failureScreen = failureDOM.window.document.getElementById("screen");
  assert.equal(failureDOM.window.document.body.dataset.v3PublicSurvey, "error", `${code}: failure route is owned by the existing public Survey Host`);
  assert.match(failureScreen?.textContent || "", new RegExp(title), `${code}: Owner error fact receives its explicit stop title`);
  assert.match(failureScreen?.textContent || "", new RegExp(message), `${code}: Owner error fact receives a non-progress explanation`);
  assert.equal(failureScreen?.textContent?.includes("增长诊断测评"), false, `${code}: frozen demo title must be absent`);
  assert.equal(failureScreen?.textContent?.includes("后端能力未就绪"), false, `${code}: engineering placeholder must be absent`);
  assert.equal(failureScreen?.textContent?.includes("演示题"), false, `${code}: demo copy must be absent`);
  assert.equal(failureDOM.window.document.querySelector('a[href="index.html"]'), null, `${code}: demo screen navigation must be absent`);
  assert.equal(failureScreen?.querySelector("button"), null, `${code}: terminal Owner error must not offer an unsupported retry`);
  failureDOM.window.close();
}

const unsafeCode = '<img src=x onerror="window.__escaped=1">';
const unknownDOM = await renderFailure(
  `https://test.invalid/h5/error.html?code=${encodeURIComponent(unsafeCode)}`,
);
const unknownScreen = unknownDOM.window.document.getElementById("screen");
assert.match(unknownScreen?.textContent || "", /链接无效/, "unknown error code has the generic invalid-link stop state");
assert.equal(unknownDOM.window.document.documentElement.outerHTML.includes(unsafeCode), false, "untrusted error code must not be copied into markup");
assert.equal(unknownScreen?.querySelector("a"), null, "unknown error code cannot create a navigation target");
unknownDOM.window.close();

// The required login gate is covered by h5AuthAdapter.test.mjs. The frozen
// answer, result and terminal Owner states remain covered above.

console.log("public Survey H5 presentation Host: PASS");
