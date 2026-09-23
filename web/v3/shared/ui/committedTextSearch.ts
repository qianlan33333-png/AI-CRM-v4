// Shared, V3-owned search interaction contract for the byte-frozen page and
// picker surfaces. It deliberately intercepts only the registered search
// controls below: ordinary form fields and text composers keep their native
// input semantics.
//
// A committed query is a read concern. It does not resolve an identity,
// persist data, or submit an external effect.

type SearchTrigger = 'input' | 'keydown';

type RegisteredSearch = {
  selector: string;
  trigger: SearchTrigger;
};

const registeredSearches: RegisteredSearch[] = [
  // The byte-frozen Channels template uses an `onInput` handler that replaces
  // the list on every key. Its accessible name is the stable donor seam.
  { selector: 'input[aria-label="搜索渠道名称"]', trigger: 'input' },
  { selector: 'input[placeholder="搜索优惠券名称"]', trigger: 'input' },
  { selector: 'input[placeholder="按问卷名称或 ID 搜索"]', trigger: 'input' },
  { selector: 'input[placeholder="搜索标签组 / 标签 / tag_id"]', trigger: 'input' },
  { selector: '#list-search', trigger: 'input' },
  { selector: 'input[placeholder="搜索计划名称、发送人"]:not(:disabled)', trigger: 'input' },
  { selector: 'input[placeholder="按名称、链接、文件名搜索"]', trigger: 'input' },
  { selector: 'input[data-image-library-query]', trigger: 'input' },
  { selector: 'input[data-material-library-query="attachment"]', trigger: 'input' },
  { selector: 'input[data-material-library-query="miniprogram"]', trigger: 'keydown' },
  { selector: 'input[data-open-platform-doc-search]', trigger: 'input' },
  { selector: 'input[data-field-mapping-variable-search]', trigger: 'input' },
  // Distribution is a current-page-only client filter. It shares the same
  // explicit commit boundary so an IME draft never redraws the admin table.
  { selector: 'input[aria-label="仅筛选当前已加载页"]', trigger: 'input' },
  // V3-owned access and survey Hosts keep their own read/filter functions.
  // These precise seams prevent the shared policy from touching provisioning,
  // role, login, binding, or controlled external-push controls.
  { selector: '#admin-access-search', trigger: 'input' },
  { selector: '#admin-access-employee-search', trigger: 'input' },
  { selector: 'input[data-survey-log-search]', trigger: 'input' },
  { selector: '#group-ops-app input[name="keyword"][data-filter]', trigger: 'keydown' },
  { selector: '.aicrm-group-chat-picker-mask [data-group-picker-search]', trigger: 'input' },
  { selector: '.aicrm-tag-picker [data-role="search"]', trigger: 'input' },
  { selector: '[data-operation-member-picker] [data-operation-member-search]', trigger: 'keydown' },
  { selector: '.aicrm-material-picker-mask [data-picker-search]', trigger: 'keydown' },
];

type SearchState = {
  forwarded: WeakSet<Event>;
  composing: WeakSet<HTMLInputElement>;
  compositionJustEnded: WeakSet<HTMLInputElement>;
  committedQueries: WeakMap<HTMLInputElement, string>;
  installed: boolean;
};

const stateKey = Symbol.for('aicrm.v3.committed-text-search.state');

function state(): SearchState {
  const holder = document as Document & { [stateKey]?: SearchState };
  if (!holder[stateKey]) {
    holder[stateKey] = {
      forwarded: new WeakSet<Event>(),
      composing: new WeakSet<HTMLInputElement>(),
      compositionJustEnded: new WeakSet<HTMLInputElement>(),
      committedQueries: new WeakMap<HTMLInputElement, string>(),
      installed: false,
    };
  }
  return holder[stateKey];
}

function registered(input: HTMLInputElement): RegisteredSearch | undefined {
  return registeredSearches.find(({ selector }) => input.matches(selector));
}

function inputFrom(event: Event): HTMLInputElement | null {
  return event.target instanceof HTMLInputElement ? event.target : null;
}

function preserveSelection(input: HTMLInputElement, selector: string): () => void {
  const start = input.selectionStart;
  const end = input.selectionEnd;
  const ownerDocument = input.ownerDocument;
  const wasActive = ownerDocument.activeElement === input;
  return () => {
    const ownerWindow = ownerDocument.defaultView;
    // A legacy redraw may replace the input synchronously. A navigation or
    // dialog close invalidates its document; that owner must not regain focus.
    if (!ownerWindow || ownerWindow.document !== ownerDocument) return;
    const current = input.isConnected && input.ownerDocument === ownerDocument
      ? input
      : ownerDocument.querySelector<HTMLInputElement>(selector);
    if (!current?.isConnected) return;
    const active = ownerDocument.activeElement;
    // A user who moved to another live control after the handler ran keeps
    // that focus. Reclaim only the original search focus or a redraw's body.
    if (!wasActive || (active !== input && active !== ownerDocument.body && active !== ownerDocument.documentElement)) return;
    current.focus({ preventScroll: true });
    if (start !== null && end !== null) {
      const length = current.value.length;
      current.setSelectionRange(Math.min(start, length), Math.min(end, length));
    }
  };
}

function dispatchLegacySearch(input: HTMLInputElement, search: RegisteredSearch): void {
  const shared = state();
  const event = search.trigger === 'input'
    ? new Event('input', { bubbles: true })
    : new KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' });
  shared.forwarded.add(event);
  input.dispatchEvent(event);
}

function forward(input: HTMLInputElement, search: RegisteredSearch): void {
  const restoreSelection = preserveSelection(input, search.selector);
  state().committedQueries.set(input, input.value);
  dispatchLegacySearch(input, search);
  restoreSelection();
  queueMicrotask(restoreSelection);
  const ownerWindow = input.ownerDocument.defaultView;
  if (ownerWindow && ownerWindow.document === input.ownerDocument && typeof ownerWindow.requestAnimationFrame === 'function') {
    ownerWindow.requestAnimationFrame(restoreSelection);
  }
}

function deferCompositionEnd(input: HTMLInputElement): void {
  const shared = state();
  shared.compositionJustEnded.add(input);
  window.setTimeout(() => shared.compositionJustEnded.delete(input), 0);
}

/**
 * Installs the only text-search policy shared by V3 Hosts. The existing
 * frozen handlers remain the authoritative list/fetch implementation; this
 * adapter blocks their per-keystroke invocation and forwards one event only
 * after a deliberate Enter. It is safe to call before or after a donor opens
 * its dynamic picker.
 */
export function installCommittedTextSearch(): void {
  const shared = state();
  if (shared.installed) return;
  shared.installed = true;

  document.addEventListener('compositionstart', (event) => {
    const input = inputFrom(event);
    if (input && registered(input)) state().composing.add(input);
  }, true);

  document.addEventListener('compositionupdate', (event) => {
    const input = inputFrom(event);
    if (input && registered(input)) state().composing.add(input);
  }, true);

  document.addEventListener('compositionend', (event) => {
    const input = inputFrom(event);
    if (!input || !registered(input)) return;
    state().composing.delete(input);
    // Composition completion only settles the draft. A following Enter used
    // to choose an IME candidate must not submit the search as well.
    deferCompositionEnd(input);
  }, true);

  document.addEventListener('input', (event) => {
    if (state().forwarded.has(event)) {
      state().forwarded.delete(event);
      return;
    }
    const input = inputFrom(event);
    if (!input || !registered(input)) return;
    // Keep the browser-owned draft and selection. The frozen handler is
    // intentionally prevented from filtering or fetching on every key.
    event.stopImmediatePropagation();
  }, true);

  document.addEventListener('keydown', (event) => {
    if (state().forwarded.has(event)) {
      state().forwarded.delete(event);
      return;
    }
    const input = inputFrom(event);
    const search = input && registered(input);
    if (!input || !search || event.key !== 'Enter') return;

    if (event.isComposing || event.keyCode === 229 || state().composing.has(input) || state().compositionJustEnded.has(input)) {
      // Do not prevent the browser's IME commit. Stopping propagation alone
      // protects the donor handler from treating the candidate Enter as a
      // business search submission.
      event.stopImmediatePropagation();
      return;
    }

    event.preventDefault();
    event.stopImmediatePropagation();
    forward(input, search);
  }, true);
}

/**
 * Repeats the last explicit query after an unrelated refresh. The refresh
 * button must not accidentally submit a newer IME/text draft merely because
 * the frozen picker reads its input value when it starts a reload.
 */
export function replayCommittedTextSearch(input: HTMLInputElement): void {
  const search = registered(input);
  if (!search) return;
  const shared = state();
  const draft = input.value;
  const selectionStart = input.selectionStart;
  const selectionEnd = input.selectionEnd;
  input.value = shared.committedQueries.get(input) ?? '';
  dispatchLegacySearch(input, search);
  input.value = draft;
  if (selectionStart !== null && selectionEnd !== null) input.setSelectionRange(selectionStart, selectionEnd);
}

/**
 * Returns the query most recently sent through the explicit commit boundary.
 * Callers that redraw or refresh a list must not consult a live IME draft.
 */
export function committedTextSearchValue(input: HTMLInputElement): string {
  const search = registered(input);
  if (!search) return '';
  return state().committedQueries.get(input) ?? '';
}

/** Mark a programmatic clear/open value as the new committed query. */
export function resetCommittedTextSearch(input: HTMLInputElement): void {
  const search = registered(input);
  if (!search) return;
  state().committedQueries.set(input, input.value);
}
