export {};

/*
 * Public Survey presentation seam.
 *
 * The H5 Controller remains the authority for the Survey Owner's question
 * validation, submission key, retry and result-token flows.  This Host is
 * loaded before that frozen runtime and only adds stable presentation and
 * accessibility hooks to its release template and rendered screen.
 */

const supportedPages = new Set(["auth", "all", "one", "result", "error", "done"]);
const page = document.body.dataset.page || "";

type FailurePresentation = {
  title: string;
  message: string;
  returnSlug?: string;
};

const validPublicSlug = (value: string): boolean =>
  /^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$/.test(value);

const failurePresentation = (): FailurePresentation | null => {
  const query = new URLSearchParams(location.search);
  if (page === "auth" && query.has("oauth_error")) {
    const slug = query.get("slug") || "";
    return {
      title: "授权失败",
      message: "未能完成微信授权，当前授权流程无法继续。",
      ...(validPublicSlug(slug) ? { returnSlug: slug } : {}),
    };
  }
  if (page !== "error") return null;

  // The Handler owns these exact redirect codes. Query text is never echoed or
  // used as a target, so an arbitrary error URL cannot become page content or
  // navigation.
  switch (query.get("code")) {
    case "survey_oauth_unavailable":
      return {
        title: "暂时无法继续",
        message: "当前问卷暂时不能完成微信授权，请稍后从原问卷入口重新打开。",
      };
    case "survey_oauth_failed":
      return {
        title: "授权失败",
        message: "未能完成微信授权，当前链接无法继续。",
      };
    case "survey_identity_conflict":
      return {
        title: "无法继续",
        message: "当前微信身份与问卷状态不一致，请联系管理员处理后从原问卷入口重新打开。",
      };
    default:
      return {
        title: "链接无效",
        message: "当前链接无法继续，请从原问卷入口重新打开。",
      };
  }
};

if (supportedPages.has(page) && page !== 'auth') {
  document.body.dataset.v3PublicSurvey = page;

  const template = document.getElementById("tpl") as HTMLTemplateElement | null;
  if (!template) throw new Error("公开问卷页面缺少冻结运行模板");

  const failure = failurePresentation();
  if (failure) {
    const stop = document.createElement("section");
    stop.dataset.v3SurveyStop = "";
    stop.setAttribute("role", "status");
    stop.setAttribute("aria-live", "assertive");
    const card = document.createElement("div");
    card.dataset.v3SurveyStopCard = "";
    const label = document.createElement("span");
    label.dataset.v3SurveyStopLabel = "";
    label.textContent = "问卷访问";
    const title = document.createElement("h1");
    title.textContent = failure.title;
    const message = document.createElement("p");
    message.textContent = failure.message;
    card.append(label, title, message);
    if (failure.returnSlug) {
      const returnLink = document.createElement("a");
      returnLink.dataset.v3SurveySafeReturn = "";
      returnLink.href = `/q/${encodeURIComponent(failure.returnSlug)}`;
      returnLink.textContent = "重新打开问卷";
      card.append(returnLink);
    }
    stop.append(card);
    template.content.replaceChildren(stop);
  }

  const templateDescendants = <T extends Element>(
    root: ParentNode,
    selector: string,
  ): T[] => {
    const found = new Set<T>(Array.from(root.querySelectorAll<T>(selector)));
    for (const nested of Array.from(
      root.querySelectorAll<HTMLTemplateElement>("template"),
    )) {
      for (const element of templateDescendants<T>(nested.content, selector))
        found.add(element);
    }
    return [...found];
  };

  const markTemplate = (): void => {
    const content = template.content;
    for (const card of templateDescendants<HTMLElement>(
      content,
      "[data-question-id]",
    )) {
      card.dataset.v3SurveyQuestion = "";
    }
    for (const option of templateDescendants<HTMLElement>(
      content,
      "label[data-option-id]",
    )) {
      option.dataset.v3SurveyOption = "";
    }
    for (const control of templateDescendants<HTMLElement>(
      content,
      "[data-h5-submit], [data-h5-next], [data-h5-previous]",
    )) {
      control.dataset.v3SurveyAction = "";
    }
    // The frozen controller already exposes its stable `submitting` render
    // value. Reuse all.html's existing status node, then add the same
    // structural marker to one.html before it mounts. This avoids inspecting
    // user-facing copy and avoids duplicating all.html's screen-reader update.
    let hasSubmittingStatus = false;
    for (const condition of templateDescendants<HTMLTemplateElement>(
      content,
      "template",
    )) {
      if (condition.dataset.scIf !== "{{ submitting }}") continue;
      const feedback =
        condition.content.querySelector<HTMLElement>('[role="status"]');
      if (!feedback) continue;
      feedback.dataset.v3SurveySubmitting = "";
      feedback.setAttribute("aria-live", "polite");
      hasSubmittingStatus = true;
    }
    if (!hasSubmittingStatus) {
      const templateForContent = (
        fragment: Node,
      ): HTMLTemplateElement | null => {
        const visit = (root: ParentNode): HTMLTemplateElement | null => {
          for (const nested of Array.from(
            root.querySelectorAll<HTMLTemplateElement>("template"),
          )) {
            if (nested.content === fragment) return nested;
            const owner = visit(nested.content);
            if (owner) return owner;
          }
          return null;
        };
        return visit(content);
      };
      const control = templateDescendants<HTMLElement>(
        content,
        "[data-h5-submit]",
      )[0];
      const controlTemplate =
        control && templateForContent(control.parentNode || content);
      // `canSubmit` is nested in one.html's action footer. Its feedback must
      // be attached to that footer rather than the conditional template,
      // which disappears at the exact time we need to report submission.
      const target: ParentNode | null = controlTemplate?.dataset.scIf?.includes(
        "canSubmit",
      )
        ? controlTemplate.parentElement
        : control?.parentNode || null;
      if (target) {
        const condition = document.createElement("template");
        condition.dataset.scIf = "{{ submitting }}";
        const feedback = document.createElement("div");
        feedback.dataset.v3SurveySubmitting = "";
        feedback.setAttribute("role", "status");
        feedback.setAttribute("aria-live", "polite");
        feedback.textContent = "正在提交，请勿重复操作…";
        condition.content.append(feedback);
        target.append(condition);
      }
    }
    for (const error of templateDescendants<HTMLElement>(
      content,
      "[data-h5-error]",
    )) {
      error.setAttribute("aria-live", "assertive");
      error.dataset.v3SurveyError = "";
    }
    for (const receipt of templateDescendants<HTMLElement>(
      content,
      "[data-h5-receipt], [data-h5-result]",
    )) {
      receipt.dataset.v3SurveyReceipt = "";
    }
    // result.html's receipt contains a fixed diagnostic-only processing row.
    // This is a template-carrier marker, never a deduction about the current
    // submission state: the Owner still validates `local_only` and
    // `external_executed` before it renders the result at all.
    if (page === "result") {
      for (const label of templateDescendants<HTMLElement>(content, "span")) {
        if (label.textContent?.trim() !== "处理范围") continue;
        label.dataset.v3SurveyInternalReceiptDetail = "";
        const value = label.nextElementSibling;
        if (value instanceof HTMLElement)
          value.dataset.v3SurveyInternalReceiptDetail = "";
      }
    }
  };

  const rendered = (): HTMLElement | null => document.getElementById("screen");

  const improveTransportError = (error: HTMLElement): void => {
    if (error.querySelector("[data-v3-survey-recovery]")) return;
    const rawNodes = Array.from(error.childNodes).filter(
      (node): node is Text =>
        node.nodeType === Node.TEXT_NODE && Boolean(node.textContent?.trim()),
    );
    const raw = rawNodes.map((node) => node.textContent?.trim() || "").join(" ");
    const status = /(?:^|[（(\s])HTTP ([45][0-9]{2})(?:$|[）)\s])/.exec(raw)?.[1];
    // `ApiError` deliberately makes 401/403 actionable but omits the HTTP
    // number in its public copy. Classify only those two stable messages and
    // the known unavailable transport response. Owner validation and domain
    // errors retain their original wording and retry semantics.
    const unauthorized = status === "401" || raw.includes("登录状态已失效");
    const forbidden = status === "403" || raw.includes("当前账号无权执行此操作");
    const unavailable = status === "503";
    if (!unauthorized && !forbidden && !unavailable) return;
    for (const node of rawNodes) node.remove();
    const recovery = document.createElement("p");
    recovery.dataset.v3SurveyRecovery = "";
    recovery.textContent = unauthorized
      ? "登录或授权已失效，当前答案尚未提交。请重新授权后填写问卷。"
      : forbidden
        ? "当前无权限访问此问卷，请联系管理员确认访问权限。"
        : page === "result"
          ? "暂时无法查询提交结果，请稍后重试。"
          : "暂时无法完成操作，请保留当前页面并稍后重试。";
    const detail = document.createElement("small");
    detail.dataset.v3SurveyErrorDetail = "";
    detail.textContent = `问题详情：HTTP ${unauthorized ? "401" : forbidden ? "403" : status}`;
    error.prepend(recovery);
    error.append(detail);
    if (unauthorized && (page === 'all' || page === 'one')) {
      const slug = new URLSearchParams(location.search).get('slug') || '';
      if (validPublicSlug(slug)) {
        const action = document.createElement('a');
        action.dataset.v3SurveyReauthorize = '';
        action.className = 'auth-button';
        action.href = `/h5/auth.html?slug=${encodeURIComponent(slug)}`;
        action.textContent = '授权并继续';
        error.append(action);
      }
    }
  };

  const decorate = (): void => {
    const screen = rendered();
    if (!screen) return;
    screen.dataset.v3PublicSurveyScreen = page;
    const isSubmitting = !!screen.querySelector("[data-v3-survey-submitting]");
    if (isSubmitting) screen.dataset.v3SurveySubmitting = "true";
    else delete screen.dataset.v3SurveySubmitting;
    for (const button of Array.from(
      screen.querySelectorAll<HTMLButtonElement>("[data-h5-submit]"),
    )) {
      // Keep any Owner-controlled disabled state intact.  The all-in-one
      // template retains its submit control while `submitting` is true, so
      // only that explicit, stable state adds a busy lock here.
      if (isSubmitting) {
        button.disabled = true;
        button.setAttribute("aria-disabled", "true");
        button.setAttribute("aria-busy", "true");
      }
      button.dataset.v3SurveyAction = "";
    }
    for (const card of Array.from(
      screen.querySelectorAll<HTMLElement>("[data-question-id]"),
    ))
      card.dataset.v3SurveyQuestion = "";
    for (const option of Array.from(
      screen.querySelectorAll<HTMLElement>("label[data-option-id]"),
    ))
      option.dataset.v3SurveyOption = "";
    for (const error of Array.from(
      screen.querySelectorAll<HTMLElement>("[data-h5-error]"),
    )) {
      error.setAttribute("aria-live", "assertive");
      error.dataset.v3SurveyError = "";
      improveTransportError(error);
    }
    if (screen.querySelector('[data-v3-survey-reauthorize]')) {
      for (const button of Array.from(screen.querySelectorAll<HTMLButtonElement>('[data-h5-submit]'))) {
        button.disabled = true;
        button.setAttribute('aria-disabled', 'true');
      }
    }
    for (const receipt of Array.from(
      screen.querySelectorAll<HTMLElement>(
        "[data-h5-receipt], [data-h5-result]",
      ),
    ))
      receipt.dataset.v3SurveyReceipt = "";
    for (const detail of Array.from(
      screen.querySelectorAll<HTMLElement>(
        "[data-v3-survey-internal-receipt-detail]",
      ),
    ))
      detail.remove();

    const done = screen.querySelector<HTMLElement>("[data-h5-done]");
    if (done) {
      done.dataset.v3SurveyDone = "";
      done.setAttribute("role", "status");
      done.setAttribute("aria-live", "polite");
      done.tabIndex = -1;
      if (document.activeElement !== done) done.focus({ preventScroll: true });
    }
  };

  markTemplate();
  const screen = rendered();
  if (screen) {
    const observer = new MutationObserver(decorate);
    observer.observe(screen, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    screen.addEventListener(
      "click",
      (event) => {
        const target =
          event.target instanceof Element
            ? event.target.closest<HTMLButtonElement>("[data-h5-submit]")
            : null;
        if (!target || target.disabled) return;
        // Do not prevent the frozen controller's handler.  The immediate visual
        // lock closes the second-click window while its existing submitting
        // guard and stable submission key retain their authoritative behavior.
        target.disabled = true;
        target.setAttribute("aria-disabled", "true");
        target.setAttribute("aria-busy", "true");
        screen.dataset.v3SurveySubmitting = "true";
      },
      true,
    );
    decorate();
  }
}
