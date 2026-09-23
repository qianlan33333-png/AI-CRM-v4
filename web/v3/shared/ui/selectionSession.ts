// V3-owned temporary selection state for scoped pickers. It deliberately owns
// no business command: callers provide authorised reads and apply commits.

export type SelectionKey = string;
export type SelectionMode = 'single' | 'multiple';

export type SelectionItem<T> = {
  kind: string;
  source: string;
  id: string | number;
  label: string;
  value: T;
  disabledReason?: string;
};

export type SelectionPage<T> = {
  items: SelectionItem<T>[];
  nextCursor?: string;
};

export type SelectionLoader<T> = (request: { query: string; cursor?: string; signal: AbortSignal }) => Promise<SelectionPage<T>>;

export type SelectionSnapshot<T> = {
  items: SelectionItem<T>[];
  draft: SelectionItem<T>[];
  committed: SelectionItem<T>[];
  query: { draft: string; submitted: string };
  cursor?: string;
  nextCursor?: string;
  loading: boolean;
  readonlyReason?: string;
  overLimit: boolean;
  notice?: string;
  error?: string;
};

export type SelectionLoadResult<T> =
  | { state: 'applied'; snapshot: SelectionSnapshot<T> }
  | { state: 'stale'; snapshot: SelectionSnapshot<T> }
  | { state: 'failed'; snapshot: SelectionSnapshot<T> };

export type SelectionCommit<T> = {
  selected: SelectionItem<T>[];
  added: SelectionItem<T>[];
  removed: SelectionItem<T>[];
};

export type SelectionSessionOptions = {
  mode?: SelectionMode;
  limit?: number;
  readonlyReason?: string;
  /**
   * Runs only for the newest live request. Adapters use this to classify an
   * access failure without letting an aborted/older request lock a new dialog.
   */
  onCurrentLoadFailure?: (failure: unknown) => void;
};

type ReadMode = 'replace' | 'append';

export function selectionKey(kind: string, source: string, id: string | number): SelectionKey {
  return [kind, source, String(id)].map((part) => encodeURIComponent(part)).join(':');
}

function keyOf<T>(item: SelectionItem<T>): SelectionKey {
  return selectionKey(item.kind, item.source, item.id);
}

function copyValue<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value;
  const clone = (globalThis as { structuredClone?: <V>(input: V) => V }).structuredClone;
  if (clone) {
    try { return clone(value); } catch { /* callers may use an opaque value */ }
  }
  return (Array.isArray(value) ? [...value] : { ...value }) as T;
}

function copy<T>(item: SelectionItem<T>): SelectionItem<T> {
  return { ...item, value: copyValue(item.value) };
}

function message(error: unknown): string {
  if (error instanceof Error && error.message.trim()) return error.message;
  return '目录暂时无法加载，请稍后重试。';
}

function selectionLimit(options: SelectionSessionOptions): number {
  if (options.mode === 'single') return 1;
  const candidate = Number(options.limit);
  return Number.isSafeInteger(candidate) && candidate > 0 ? candidate : Number.POSITIVE_INFINITY;
}

/**
 * One picker session owns temporary selection and its authorised read lifecycle.
 * `submitSearch` is the only method that adopts a newer text draft. Refresh and
 * pagination retain the last explicit query, so an unfinished IME/text draft
 * cannot accidentally change a directory reload.
 */
export class SelectionSession<T> {
  private readonly records = new Map<SelectionKey, SelectionItem<T>>();
  private readonly draft = new Set<SelectionKey>();
  private readonly committed = new Set<SelectionKey>();
  private readonly listeners = new Set<(snapshot: SelectionSnapshot<T>) => void>();
  private pageKeys: SelectionKey[] = [];
  private requestEpoch = 0;
  private activeRequest?: AbortController;
  private loading = false;
  private error?: string;
  private notice?: string;
  private readonlyReason?: string;
  private draftQuery = '';
  private submittedQuery = '';
  private cursor?: string;
  private nextCursor?: string;
  private readonly limit: number;
  private readonly mode: SelectionMode;
  private readonly onCurrentLoadFailure?: (failure: unknown) => void;

  constructor(initial: SelectionItem<T>[] = [], options: SelectionSessionOptions = {}) {
    this.mode = options.mode || 'multiple';
    this.limit = selectionLimit(options);
    this.upsert(initial);
    for (const item of initial) {
      const key = keyOf(item);
      this.draft.add(key);
      this.committed.add(key);
    }
    this.readonlyReason = options.readonlyReason?.trim() || undefined;
    this.onCurrentLoadFailure = options.onCurrentLoadFailure;
  }

  subscribe(listener: (snapshot: SelectionSnapshot<T>) => void): () => void {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => this.listeners.delete(listener);
  }

  setDraftQuery(query: string, options: { silent?: boolean } = {}): void {
    this.draftQuery = query;
    if (!options.silent) this.emit();
  }

  setReadonly(reason?: string): void {
    const next = reason?.trim() || undefined;
    if (next) this.restoreCommitted();
    this.readonlyReason = next;
    this.notice = undefined;
    this.emit();
  }

  /**
   * Locks interaction after a current authorised read loses access without
   * rewriting the operator's in-memory draft. Cancel still restores the
   * original committed value; confirmation remains blocked while locked.
   */
  lockReadonly(reason: string): void {
    const next = reason.trim();
    if (!next) return;
    this.readonlyReason = next;
    this.notice = undefined;
    this.emit();
  }

  setDisabled(key: SelectionKey, reason?: string): void {
    const item = this.records.get(key);
    if (!item) return;
    item.disabledReason = reason?.trim() || undefined;
    this.emit();
  }

  toggle(key: SelectionKey): boolean {
    if (this.readonlyReason) return false;
    const item = this.records.get(key);
    if (!item) return false;
    if (this.draft.has(key)) {
      // A selected item that later becomes unavailable remains inspectable
      // until the operator explicitly removes it.
      this.draft.delete(key);
      this.notice = undefined;
      this.emit();
      return true;
    }
    if (item.disabledReason) {
      this.notice = item.disabledReason;
      this.emit();
      return false;
    }
    if (this.mode === 'single') {
      // Replacing a single temporary choice is still only a draft change. An
      // unavailable prior choice remains restorable through cancel, but does
      // not force the operator through a separate remove action first.
      this.draft.clear();
      this.draft.add(key);
      this.notice = undefined;
      this.emit();
      return true;
    }
    if (this.draft.size >= this.limit) {
      this.notice = this.limit === 1 ? '请先移除当前选择，再选择其他内容。' : `最多选择 ${this.limit} 项。`;
      this.emit();
      return false;
    }
    this.draft.add(key);
    this.notice = undefined;
    this.emit();
    return true;
  }

  isDraftSelected(key: SelectionKey): boolean {
    return this.draft.has(key);
  }

  item(key: SelectionKey): SelectionItem<T> | undefined {
    const item = this.records.get(key);
    return item ? copy(item) : undefined;
  }

  /**
   * Refresh record presentation without changing the current page or either
   * selection set. A complete catalog can use this to resolve an initial
   * selection that falls outside the current search/page.
   */
  reconcile(items: SelectionItem<T>[]): void {
    this.upsert(items);
    this.emit();
  }

  /** Discard edits and invalidate an in-flight directory read. */
  cancel(): void {
    this.invalidateRead();
    this.restoreCommitted();
    this.notice = undefined;
    this.error = undefined;
    this.emit();
  }

  /** Invalidate reads when a picker is permanently closed. */
  dispose(): void {
    this.invalidateRead();
    this.listeners.clear();
  }

  isOverLimit(): boolean { return this.draft.size > this.limit; }

  /** Inspect a pending change before a caller accepts its business contract. */
  previewCommit(): SelectionCommit<T> {
    if (this.readonlyReason) {
      return { selected: this.itemsFor(this.committed), added: [], removed: [] };
    }
    const added = this.itemsFor(this.draft, this.committed, false);
    const removed = this.itemsFor(this.committed, this.draft, false);
    const selected = this.itemsFor(this.draft);
    return { selected, added, removed };
  }

  commit(): SelectionCommit<T> {
    if (this.isOverLimit()) throw new Error(this.limit === 1 ? '初始选择超过单选限制，请先保留一项。' : `初始选择超过 ${this.limit} 项，请先移除多余选择。`);
    if (this.readonlyReason) {
      this.restoreCommitted();
      const preview = this.previewCommit();
      this.emit();
      return preview;
    }
    const preview = this.previewCommit();
    this.committed.clear();
    for (const key of this.draft) this.committed.add(key);
    this.notice = undefined;
    this.emit();
    return preview;
  }

  /** Submit the current draft as a new query and restart at the first page. */
  submitSearch(loader: SelectionLoader<T>): Promise<SelectionLoadResult<T>> {
    this.submittedQuery = this.draftQuery;
    this.cursor = undefined;
    this.nextCursor = undefined;
    return this.read(loader, undefined, 'replace');
  }

  /** Reload the submitted query, retaining any unfinished text draft. */
  reload(loader: SelectionLoader<T>): Promise<SelectionLoadResult<T>> {
    // Refresh returns to page one for the existing committed query. Replacing
    // the current list with only page N after "load more" makes earlier
    // results disappear without an operator action. Draft selections remain
    // in their own set, including selections from a later loaded page.
    this.cursor = undefined;
    this.nextCursor = undefined;
    return this.read(loader, undefined, 'replace');
  }

  /** Load the next page for the submitted query without changing its text. */
  loadNextPage(loader: SelectionLoader<T>): Promise<SelectionLoadResult<T>> {
    if (!this.nextCursor) return Promise.resolve({ state: 'applied', snapshot: this.snapshot() });
    return this.read(loader, this.nextCursor, 'append');
  }

  snapshot(): SelectionSnapshot<T> {
    return {
      items: this.pageKeys.flatMap((key) => this.records.has(key) ? [copy(this.records.get(key)!)] : []),
      draft: this.itemsFor(this.draft),
      committed: this.itemsFor(this.committed),
      query: { draft: this.draftQuery, submitted: this.submittedQuery },
      cursor: this.cursor,
      nextCursor: this.nextCursor,
      loading: this.loading,
      readonlyReason: this.readonlyReason,
      overLimit: this.isOverLimit(),
      notice: this.notice,
      error: this.error,
    };
  }

  private async read(loader: SelectionLoader<T>, cursor: string | undefined, mode: ReadMode): Promise<SelectionLoadResult<T>> {
    const epoch = ++this.requestEpoch;
    this.activeRequest?.abort();
    const controller = new AbortController();
    this.activeRequest = controller;
    this.loading = true;
    this.error = undefined;
    this.notice = undefined;
    this.emit();
    try {
      const page = await loader({ query: this.submittedQuery, cursor, signal: controller.signal });
      if (epoch !== this.requestEpoch) return { state: 'stale', snapshot: this.snapshot() };
      this.upsert(page.items);
      const keys = page.items.map(keyOf);
      this.pageKeys = mode === 'append' ? [...new Set([...this.pageKeys, ...keys])] : keys;
      this.cursor = cursor;
      this.nextCursor = page.nextCursor;
      this.loading = false;
      this.activeRequest = undefined;
      this.emit();
      return { state: 'applied', snapshot: this.snapshot() };
    } catch (failure) {
      if (epoch !== this.requestEpoch) return { state: 'stale', snapshot: this.snapshot() };
      this.loading = false;
      this.activeRequest = undefined;
      // Permission changes are observable session state. Apply them only after
      // the epoch check above so an old rejected fetch cannot freeze a newer
      // search, a reopened picker, or a picker that has already been closed.
      try { this.onCurrentLoadFailure?.(failure); } catch { /* display the original directory failure */ }
      this.error = message(failure);
      this.emit();
      return { state: 'failed', snapshot: this.snapshot() };
    }
  }

  private invalidateRead(): void {
    this.requestEpoch += 1;
    this.activeRequest?.abort();
    this.activeRequest = undefined;
    this.loading = false;
  }

  private restoreCommitted(): void {
    this.draft.clear();
    for (const key of this.committed) this.draft.add(key);
  }

  private upsert(items: SelectionItem<T>[]): void {
    for (const item of items) this.records.set(keyOf(item), { ...item });
  }

  private itemsFor(primary: Set<SelectionKey>, exclude?: Set<SelectionKey>, includeShared = true): SelectionItem<T>[] {
    return [...primary].flatMap((key) => {
      if (!includeShared && exclude?.has(key)) return [];
      const item = this.records.get(key);
      // A snapshot is an immutable view. Host renderers must use session
      // methods to update a record rather than changing our internal map.
      return item ? [copy(item)] : [];
    });
  }

  private emit(): void {
    const snapshot = this.snapshot();
    for (const listener of this.listeners) listener(snapshot);
  }
}
