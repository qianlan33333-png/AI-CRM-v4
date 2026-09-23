/*
 * V3 host binding for the frozen Agent editor. The donor template renders
 * readonly="false" for a new code, which HTML correctly treats as readonly.
 * Supplying a valid code before the unchanged donor save handler runs retains
 * the donor DOM, controller, API URL, DTO, and interaction sequence verbatim.
 */
(() => {
  const doc = document;
  const pageWindow = window;
  const body = () => doc.body;
  const code = () => body()?.dataset.automationCreateCode || "";
  const legal = (value) => /^agent_[a-f0-9]{32}$/.test(value);
  const existingRecord = () => new URLSearchParams(pageWindow.location.search).has("id");
  const synchronize = () => {
    try {
      if (!body()) return false;
      const createCode = code();
      const input = doc.getElementById("agentCode");
      if (existingRecord() || !legal(createCode)) return true;
      if (!(input instanceof HTMLInputElement)) return false;
      // Never replace a value that the user or another trusted browser action
      // has already supplied. The frozen input is unintentionally readonly on
      // new pages, so the normal path begins blank and receives this host code.
      if (!input.value.trim()) {
        input.value = createCode;
      }
      input.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    } catch (_) {
      // A document can be torn down while an observer is queued; it is no
      // longer a page where a create binding can be applied.
      return true;
    }
  };

  // This listener is intentionally registered before the donor module. It
  // replays an input event at click-capture time, after the frozen controller
  // has mounted its own input listener but before its unchanged Save handler
  // reads the form. That closes the parser/module ordering gap without
  // replacing a donor listener, DTO, request URL, or visible interaction.
  doc.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target?.closest("[data-agent-save]")) return;
    synchronize();
  }, true);

  if (synchronize()) return;
  const observer = new MutationObserver(() => {
    if (synchronize()) observer.disconnect();
  });
  observer.observe(doc.documentElement, { childList: true, subtree: true });
  pageWindow.setTimeout(() => observer.disconnect(), 10_000);
})();
