import { bindMaterialGroup, management } from './materialGroupManagement';
// The unified material workspace is presentation-only. It joins the three
// existing Media pages through one shell route without taking ownership of
// their HTTP reads, mutations, private URLs, or frozen donor callbacks.
import {
  mountPageHeaderActionElements,
  pageHeaderActionElementsHaveConnectedOrigins,
} from './shared/ui/pageHeaderActions';
import {
  committedTextSearchValue,
  installCommittedTextSearch,
  resetCommittedTextSearch,
} from './shared/ui/committedTextSearch';
import { mountMaterialGroups, mountMaterialGroupControl } from './materialGrouping';
import { formatShanghaiDateTime } from './adminDateTime';
import { listLegacyAttachments, listLegacyMiniPrograms } from '../src/api/generated/p4-media-compat/p4-media-compat';
import { ApiError, apiRequestOptions, unwrapGenerated } from '../src/api/transport';
import type { LegacyAttachmentItem, LegacyAttachmentListSuccess, LegacyMiniProgram, LegacyMiniProgramListResponse } from '../src/api/generated/health.schemas';

installCommittedTextSearch();

type MaterialPage = 'attach' | 'mpLib';

type NodePresentation = { style: string | null; role: string | null; children: Node[] };
type AttachmentRowSource = { cells: NodePresentation[] };
type MiniCardSource = { card: NodePresentation; cover: NodePresentation; name: NodePresentation; thumbnail: NodePresentation; enabled: NodePresentation; actions: NodePresentation; inner: HTMLElement; body: HTMLElement; coverNode: HTMLElement; nameNode: HTMLElement; thumbnailNode: HTMLElement; enabledNode: HTMLElement; actionsNode: HTMLElement };

function snapshotNode(node: HTMLElement): NodePresentation {
  return { style: node.getAttribute('style'), role: node.getAttribute('role'), children: Array.from(node.childNodes) };
}

function restoreNode(node: HTMLElement, snapshot: NodePresentation): void {
  node.replaceChildren(...snapshot.children);
  if (snapshot.style === null) node.removeAttribute('style'); else node.setAttribute('style', snapshot.style);
  if (snapshot.role === null) node.removeAttribute('role'); else node.setAttribute('role', snapshot.role);
}

type PageConfig = {
  tab: 'attachments' | 'miniprograms';
  label: string;
  actionLabel: string;
  querySelector: string;
  queryMode: 'attachment' | 'miniprogram';
};

const pages: Record<MaterialPage, PageConfig> = {
  attach: {
    tab: 'attachments', label: '附件', actionLabel: '上传附件',
    querySelector: 'input[placeholder="搜索附件名"]', queryMode: 'attachment',
  },
  mpLib: {
    tab: 'miniprograms', label: '小程序', actionLabel: '新建小程序卡片',
    querySelector: '#fMpQuery', queryMode: 'miniprogram',
  },
};

const tabItems = [
  { value: 'images', label: '图片' },
  { value: 'attachments', label: '附件' },
  { value: 'miniprograms', label: '小程序' },
] as const;

const mediaContentChangedEvent = 'aicrm:media-content-changed';

function isNonnegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function isCompleteAttachmentList(value: unknown): value is LegacyAttachmentListSuccess {
  const response = value as Partial<LegacyAttachmentListSuccess>;
  return Array.isArray(response.items) && isNonnegativeInteger(response.total) && isNonnegativeInteger(response.offset) && typeof response.limit === 'number' && Number.isSafeInteger(response.limit) && response.limit >= 1 &&
    response.items.every((item) => Number.isSafeInteger(item.id) && item.id > 0 && typeof item.name === 'string' && typeof item.file_size === 'number' && Number.isFinite(item.file_size) && typeof item.enabled === 'boolean' && Number.isSafeInteger(item.version) && item.version > 0 && typeof item.created_at === 'string');
}

function isCompleteMiniProgramList(value: unknown, page: { offset: number; limit: number }): value is LegacyMiniProgramListResponse {
  const response = value as Partial<LegacyMiniProgramListResponse>;
  return response.ok === true && Array.isArray(response.items) && Array.isArray(response.miniprograms) && isNonnegativeInteger(response.total) && response.offset === page.offset && response.limit === page.limit && response.local_only === true && response.provider_call_executed === false && response.real_external_call_executed === false &&
    response.items.every((item) => Number.isSafeInteger(item.id) && item.id > 0 && typeof item.name === 'string' && typeof item.appid === 'string' && typeof item.pagepath === 'string' && typeof item.page_path === 'string' && typeof item.title === 'string' && typeof item.enabled === 'boolean' && Number.isSafeInteger(item.version) && item.version > 0 && typeof item.updated_at === 'string');
}

function apiStatus(error: unknown): number | undefined {
  if (error instanceof ApiError) return error.status;
  const status = error && typeof error === 'object' ? (error as { status?: unknown }).status : undefined;
  return typeof status === 'number' && Number.isSafeInteger(status) ? status : undefined;
}

function donorHeader(control: HTMLElement): HTMLElement | undefined {
  for (let current: HTMLElement | null = control.parentElement; current; current = current.parentElement) {
    if (current.tagName === 'DIV' && current.style.height === '52px') return current;
  }
  return undefined;
}

function hideDonorHeader(header: HTMLElement): void {
  if (header.dataset.materialLibraryHeaderHidden === 'true') return;
  header.dataset.materialLibraryHeaderHidden = 'true';
  // The frozen header has an inline display declaration. `hidden` alone does
  // not defeat that authored display in every browser, so keep it scoped to
  // this shell-only duplicate title.
  header.setAttribute('hidden', '');
  header.style.setProperty('display', 'none', 'important');
}

export function mountMaterialLibraryTabs(stage: HTMLElement, active: string): HTMLElement {
  const existing = stage.querySelector<HTMLElement>(':scope > [data-material-library-tabs]');
  if (existing) return existing;
  const nav = document.createElement('nav');
  nav.dataset.materialLibraryTabs = 'true';
  nav.setAttribute('aria-label', '素材类型');
  nav.style.cssText = 'display:flex;align-items:center;gap:4px;padding:10px 20px 0;background:#fff;border-bottom:1px solid #EFF0F1';
  for (const item of tabItems) {
    const link = document.createElement('a');
    link.href = `/admin/materials?tab=${item.value}`;
    link.textContent = item.label;
    link.dataset.materialLibraryTab = item.value;
    const selected = item.value === active;
    if (selected) link.setAttribute('aria-current', 'page');
    link.style.cssText = `height:30px;padding:0 12px;display:inline-flex;align-items:center;border-radius:6px 6px 0 0;text-decoration:none;font-size:13px;font-weight:${selected ? '600' : '400'};color:${selected ? '#245BDB' : '#646A73'};background:${selected ? '#EFF4FF' : 'transparent'}`;
    nav.append(link);
  }
  stage.prepend(nav);
  return nav;
}

class FrozenMaterialPresentation {
  private readonly page: MaterialPage;
  private readonly config: PageConfig;
  private readonly stage: HTMLElement;
  private action?: HTMLElement;
  private releaseAction?: () => void;
  private observer?: MutationObserver;
  private syncQueued = false;
  private attachmentQuery?: HTMLInputElement;
  private readAbort?: AbortController;
  private readGeneration = 0;
  private metadataQuery = '';
  private attachments: LegacyAttachmentItem[] = [];
  private miniPrograms: LegacyMiniProgram[] = [];
  private listSignature = '';
  private metadataReadFailed = false;
  private metadataLoaded = false;
  private authorizationLost = false;
  // Only an explicit Enter, 查询, 重置 or 重试 may update this value. The
  // visible input can contain an IME draft while another owner redraws.
  private committedMiniProgramQuery = '';
  private miniProgramQueryInitialized = false;
  private readonly attachmentRows = new WeakMap<HTMLTableRowElement, AttachmentRowSource>();
  private attachmentHeader?: NodePresentation[];
  private readonly miniCards = new WeakMap<HTMLElement, MiniCardSource>();
  private readonly authorizationDisabled = new Map<HTMLButtonElement, { disabled: boolean; ariaDisabled: string | null }>();

  constructor(page: MaterialPage, stage: HTMLElement) {
    this.page = page;
    this.config = pages[page];
    this.stage = stage;
  }

  start(): void {
    this.sync();
    this.observer = new MutationObserver(() => this.queueSync());
    this.observer.observe(this.stage, { childList: true, subtree: true });
    window.addEventListener(mediaContentChangedEvent, () => this.invalidateCurrentMetadata());
    document.addEventListener('click', (event) => this.blockUnauthorizedMutation(event), true);
  }

  private queueSync(): void {
    // A donor redraw can add several descendants in one turn. Coalesce that
    // work, but keep the next turn available for a genuine owner redraw.
    // Rendering below is idempotent, so a presentation-owned DOM update cannot
    // sustain a microtask loop.
    if (this.syncQueued) return;
    this.syncQueued = true;
    queueMicrotask(() => {
      this.syncQueued = false;
      this.sync();
    });
  }

  private sync(): void {
    this.ensureTabs();
    this.installCommittedSearch();
    this.readVisibleMiniProgramPage();
    this.applyCachedMetadata();
    this.reapplyAuthorizationReadOnly();
    const action = Array.from(this.stage.querySelectorAll<HTMLButtonElement>('button'))
      .find((candidate) => candidate.textContent?.trim() === this.config.actionLabel);
    if (!action) {
      // The action is intentionally no longer in `stage` after it is moved to
      // the topbar. Only a disconnected source marker means the donor redrew
      // without a replacement; do not bounce a live control back and forth on
      // every observer notification.
      if (this.action && !pageHeaderActionElementsHaveConnectedOrigins(`material-library-${this.page}`, [this.action])) {
        this.releaseAction?.();
        this.releaseAction = undefined;
        this.action = undefined;
      }
      this.reapplyAuthorizationReadOnly();
      return;
    }
    if (action === this.action && pageHeaderActionElementsHaveConnectedOrigins(`material-library-${this.page}`, [action])) {
      this.reapplyAuthorizationReadOnly();
      return;
    }
    this.releaseAction?.();
    this.action = action;
    const header = donorHeader(action);
    if (header) hideDonorHeader(header);
    this.releaseAction = mountPageHeaderActionElements(`material-library-${this.page}`, [action]);
    this.reapplyAuthorizationReadOnly();
  }

  private ensureTabs(): void {
    mountMaterialLibraryTabs(this.stage, this.config.tab);
    mountMaterialGroups(this.stage, this.page);
  }

  private installCommittedSearch(): void {
    const input = this.stage.querySelector<HTMLInputElement>(this.config.querySelector);
    if (!input) return;
    if (this.config.queryMode === 'miniprogram' && input.dataset.materialLibraryQuery !== this.config.queryMode) {
      input.dataset.materialLibraryQuery = this.config.queryMode;
      if (!this.miniProgramQueryInitialized) {
        // Server-rendered list state is already committed. Later donor
        // redraws must keep our last explicit user commit instead.
        this.committedMiniProgramQuery = input.value.trim();
        this.miniProgramQueryInitialized = true;
      }
      input.addEventListener('compositionend', () => {
        input.dataset.materialLibraryCompositionSettling = 'true';
        window.setTimeout(() => delete input.dataset.materialLibraryCompositionSettling, 0);
      });
      // The shared capture listener has already copied the committed value
      // before this forwarded keydown reaches the donor and this listener.
      input.addEventListener('keydown', (event) => {
        if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229 || input.dataset.materialLibraryCompositionSettling === 'true') return;
        // A normal browser path arrives here as a shared forwarded event. The
        // fallback to input.value is still an explicit non-composing Enter,
        // which keeps synthetic/native owner test events representative.
        this.commitMiniProgramQuery(committedTextSearchValue(input) || input.value);
        this.stage.querySelector<HTMLButtonElement>('#mpSearch')?.click();
      });
    }
    if (this.config.queryMode === 'miniprogram') {
      this.installMiniProgramControls(input);
      return;
    }
    if (input.dataset.materialLibraryQuery === this.config.queryMode) return;
    input.dataset.materialLibraryQuery = this.config.queryMode;
    this.attachmentQuery = input;
    input.addEventListener('input', () => {
      this.filterVisibleAttachments();
      void this.readMetadata(input.value);
    });
    void this.readMetadata(input.value);
  }

  private installMiniProgramControls(input: HTMLInputElement): void {
    const install = (selector: string, key: string, callback: () => void) => {
      const control = this.stage.querySelector<HTMLButtonElement>(selector);
      if (!control || control.dataset[key] === 'true') return;
      control.dataset[key] = 'true';
      control.addEventListener('click', callback, true);
    };
    install('#mpSearch', 'materialLibrarySearch', () => {
      // A button click is an explicit commit. The donor still owns the
      // actual query/re-render callback; this read follows that same value.
      this.commitMiniProgramQuery(input.value);
      resetCommittedTextSearch(input);
    });
    install('#mpReset', 'materialLibraryReset', () => {
      // Clear is explicit even when the donor redraw replaces the input.
      this.commitMiniProgramQuery('');
      queueMicrotask(() => {
        const current = this.stage.querySelector<HTMLInputElement>(this.config.querySelector);
        if (current) resetCommittedTextSearch(current);
      });
    });
    install('#mpRetry', 'materialLibraryRetry', () => {
      // Retry belongs to the frozen owner and must retain its page offset.
      // Do not replay Enter here: doing so would turn a retry into a new
      // search (offset zero) and invoke both owner callbacks.
      this.listSignature = ''; management()?.clear();
      this.metadataQuery = '';
      this.metadataLoaded = false;
      queueMicrotask(() => this.readVisibleMiniProgramPage());
    });
    for (const control of this.stage.querySelectorAll<HTMLButtonElement>('#mpPrevious, #mpNext')) {
      if (control.dataset.materialLibraryPagination === 'true') continue;
      control.dataset.materialLibraryPagination = 'true';
      control.addEventListener('click', () => { this.listSignature = ''; management()?.clear(); });
    }
  }

  private commitMiniProgramQuery(value: string): void {
    this.committedMiniProgramQuery = value.trim();
    this.listSignature = ''; management()?.clear();
    this.metadataQuery = '';
    this.metadataLoaded = false;
    // Let the donor query handler redraw first. Its subsequent DOM mutation
    // calls sync, which reads the selected page using this committed value.
    queueMicrotask(() => this.readVisibleMiniProgramPage());
  }

  private visibleMiniProgramPage(): { offset: number; limit: number } {
    // The frozen controller's bounded Media list uses a fixed 50-item page.
    // Its visible `start–end / total` range is the current page context; read
    // the same offset instead of assuming the first page after navigation.
    const range = this.stage.querySelector('#mpPrevious, #mpNext')?.parentElement?.querySelector('span')?.textContent || '';
    const match = range.match(/(\d+)\s*[-–—]\s*(\d+)/);
    const start = match ? Number(match[1]) : 1;
    return { offset: Number.isSafeInteger(start) && start > 0 ? start - 1 : 0, limit: 50 };
  }

  private readVisibleMiniProgramPage(): void {
    if (this.page !== 'mpLib') return;
    const page = this.visibleMiniProgramPage();
    const signature = `${this.committedMiniProgramQuery}\u0000${page.offset}:${page.limit}`;
    if (signature === this.listSignature) return;
    this.listSignature = signature;
    void this.readMetadata(this.committedMiniProgramQuery, page);
  }

  private filterVisibleAttachments(): void {
    const query = this.attachmentQuery?.value.trim().toLocaleLowerCase() || '';
    // Metadata upgrades the frozen heading from "附件名" to "名称". Keep using
    // the marked owner table after that upgrade so committed searches continue
    // to filter only actual attachment rows, never the refresh diagnostics.
    const table = this.attachmentTable();
    if (!table) return;
    for (const row of table.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
      const name = row.cells[0]?.querySelector('span:last-child')?.textContent?.trim().toLocaleLowerCase() || '';
      const match = !query || name.includes(query);
      row.hidden = !match;
      if (match) row.style.removeProperty('display');
      else row.style.setProperty('display', 'none', 'important');
    }
  }

  private async readMetadata(value: string, miniPage = this.visibleMiniProgramPage()): Promise<void> {
    const query = value.trim();
    const requestSignature = this.page === 'mpLib' ? `${query}\u0000${miniPage.offset}:${miniPage.limit}` : query;
    if (requestSignature === this.metadataQuery && this.hasUsableMetadata()) return;
    this.metadataQuery = requestSignature;
    this.readAbort?.abort();
    const abort = new AbortController();
    this.readAbort = abort;
    const generation = ++this.readGeneration;
    try {
      if (this.page === 'attach') {
        const response = unwrapGenerated(await listLegacyAttachments({
          limit: '100', offset: '0', enabled_only: 'false', ...(query ? { q: query } : {}),
        }, apiRequestOptions({ signal: abort.signal })));
        if (generation !== this.readGeneration) return;
        if (!isCompleteAttachmentList(response)) throw new Error('附件素材列表响应不完整');
        this.attachments = response.items;
      } else {
        const response = unwrapGenerated(await listLegacyMiniPrograms({
          limit: miniPage.limit, offset: miniPage.offset, enabled_only: false, ...(query ? { q: query } : {}),
        }, apiRequestOptions({ signal: abort.signal })));
        if (generation !== this.readGeneration) return;
        if (!isCompleteMiniProgramList(response, miniPage)) throw new Error('小程序素材列表响应不完整');
        this.miniPrograms = response.items;
      }
      this.metadataReadFailed = false;
      this.metadataLoaded = true;
      this.authorizationLost = false;
      this.setMutationReadOnly(false);
      this.stage.querySelector('[data-material-library-read-error]')?.remove();
      this.applyCachedMetadata();
    } catch (error) {
      if (generation !== this.readGeneration || (error instanceof DOMException && error.name === 'AbortError')) return;
      this.metadataReadFailed = true;
      const status = apiStatus(error);
      if (status === 401 || status === 403) {
        this.attachments = [];
        this.miniPrograms = [];
        this.metadataQuery = '';
        this.metadataLoaded = false;
        this.authorizationLost = true;
        this.clearEnrichedMetadata();
        this.setMutationReadOnly(true);
      }
      this.renderMetadataError(error);
    }
  }

  private hasUsableMetadata(): boolean {
    return !this.metadataReadFailed && this.metadataLoaded;
  }

  private reapplyAuthorizationReadOnly(): void {
    if (this.authorizationLost) this.setMutationReadOnly(true);
  }

  private invalidateCurrentMetadata(): void {
    // A source-owned save announces only after its own readback. The compact
    // directory must therefore re-read the visible owner page, but a redraw
    // caused by this presentation itself must not repeatedly invalidate it.
    this.metadataQuery = '';
    this.metadataLoaded = false;
    this.metadataReadFailed = false;
    this.listSignature = ''; management()?.clear();
    if (this.page === 'mpLib') {
      this.readVisibleMiniProgramPage();
      return;
    }
    const input = this.stage.querySelector<HTMLInputElement>(this.config.querySelector);
    if (input) void this.readMetadata(committedTextSearchValue(input));
  }

  private isMutationLabel(label: string): boolean {
    return new Set([
      '编辑', '删除', '创建', '保存', '上传', '启用', '停用',
      '刷新缩略图缓存', '＋ 上传缩略图（将缓存到企微）',
    ]).has(label);
  }

  private isModalMutationButton(control: HTMLButtonElement): boolean {
    if (!this.stage.contains(control)) return false;
    const modal = control.closest<HTMLElement>('div[style*="position:fixed"]');
    if (!modal) return false;
    return Boolean(modal.querySelector('#fMpName, #fAttName, #fAttUpName')) && this.isMutationLabel(control.textContent?.trim() || '');
  }

  private isMiniCover(control: Element): boolean {
    return Boolean(control.closest('[data-material-library-mini-cover]')) ||
      Boolean(control.closest('[data-material-library-mini-directory] div[style*="height:112px"][style*="cursor:pointer"]'));
  }

  private markMutationControls(): void {
    if (this.action) this.action.dataset.materialLibraryMutationControl = 'true';
    for (const control of this.stage.querySelectorAll<HTMLButtonElement>('button')) {
      const label = control.textContent?.trim() || '';
      if (this.isMutationLabel(label) || this.isModalMutationButton(control)) control.dataset.materialLibraryMutationControl = 'true';
    }
    for (const cover of this.stage.querySelectorAll<HTMLElement>('[data-material-library-mini-directory] div[style*="height:112px"][style*="cursor:pointer"]')) {
      cover.dataset.materialLibraryMiniCover = 'true';
      cover.dataset.materialLibraryMutationControl = 'true';
    }
  }

  private blockUnauthorizedMutation(event: MouseEvent): void {
    if (!this.authorizationLost || !(event.target instanceof Element)) return;
    const control = event.target.closest<HTMLElement>('[data-material-library-mutation-control="true"]');
    const button = event.target.closest<HTMLButtonElement>('button');
    if (!control && !this.isMiniCover(event.target) && !(button && this.isModalMutationButton(button))) return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }

  private setMutationReadOnly(readonly: boolean): void {
    if (!readonly) {
      for (const [control, prior] of this.authorizationDisabled) {
        control.disabled = prior.disabled;
        if (prior.ariaDisabled === null) control.removeAttribute('aria-disabled'); else control.setAttribute('aria-disabled', prior.ariaDisabled);
      }
      this.authorizationDisabled.clear();
      this.stage.removeAttribute('data-material-library-readonly');
      return;
    }
    this.stage.dataset.materialLibraryReadonly = 'true';
    this.markMutationControls();
    const controls = new Set<HTMLButtonElement>();
    if (this.action instanceof HTMLButtonElement) controls.add(this.action);
    for (const control of this.stage.querySelectorAll<HTMLButtonElement>('button')) {
      if (control.dataset.materialLibraryMutationControl === 'true') controls.add(control);
    }
    for (const control of controls) {
      if (!this.authorizationDisabled.has(control)) this.authorizationDisabled.set(control, { disabled: control.disabled, ariaDisabled: control.getAttribute('aria-disabled') });
      control.disabled = true;
      control.setAttribute('aria-disabled', 'true');
    }
  }

  private clearEnrichedMetadata(): void {
    const table = this.attachmentTable();
    const attachmentMetadataApplied = Boolean(table && (table.dataset.materialLibraryAttachmentTable === 'true' || table.querySelector('tbody tr[data-material-library-metadata-version]')));
    if (table && this.attachmentHeader && attachmentMetadataApplied) {
      const header = table.querySelector<HTMLTableRowElement>('thead tr');
      if (header) {
        while (header.cells.length > this.attachmentHeader.length) header.deleteCell(5);
        this.attachmentHeader.forEach((snapshot, index) => restoreNode(header.cells[index]!, snapshot));
      }
      for (const row of table.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
        const source = this.attachmentRows.get(row);
        if (!source) continue;
        while (row.cells.length > source.cells.length) row.deleteCell(5);
        source.cells.forEach((snapshot, index) => restoreNode(row.cells[index]!, snapshot));
        delete row.dataset.materialLibraryMetadataVersion;
      }
      if (table.hasAttribute('data-material-library-attachment-table')) table.removeAttribute('data-material-library-attachment-table');
    }
    for (const card of this.stage.querySelectorAll<HTMLElement>('[data-material-library-mini-directory] > [data-material-library-metadata-version]')) {
      // An unresolved row already renders safe placeholders. Rebuilding its
      // source tree would only emit another presentation-owned child mutation.
      if (card.dataset.materialLibraryMetadataVersion?.startsWith('unresolved:')) continue;
      const source = this.miniCards.get(card);
      if (!source) continue;
      restoreNode(source.coverNode, source.cover);
      restoreNode(source.nameNode, source.name);
      restoreNode(source.thumbnailNode, source.thumbnail);
      restoreNode(source.enabledNode, source.enabled);
      restoreNode(source.actionsNode, source.actions);
      source.body.replaceChildren(source.nameNode, source.thumbnailNode, source.enabledNode, source.actionsNode);
      source.inner.replaceChildren(source.coverNode, source.body);
      restoreNode(card, source.card);
      card.replaceChildren(source.inner);
      delete card.dataset.materialLibraryMetadataVersion;
    }
  }

  private applyCachedMetadata(): void {
    if (this.page === 'attach') this.annotateAttachments();
    else this.annotateMiniPrograms();
  }

  private attachmentTable(): HTMLTableElement | undefined {
    return Array.from(this.stage.querySelectorAll<HTMLTableElement>('table'))
      .find((candidate) => candidate.querySelector('thead th')?.textContent?.trim() === '附件名' || candidate.dataset.materialLibraryAttachmentTable === 'true');
  }

  private annotateAttachments(): void {
    const table = this.attachmentTable();
    if (!table) return;
    const rows = Array.from(table.querySelectorAll<HTMLTableRowElement>('tbody tr'));
    const byID = new Map(this.attachments.map((item) => [String(item.id), item]));
    const matched = rows.map((row) => byID.get(row.dataset.materialLibraryId || ''));
    // The V3 render seam carries the controller's resourceId into each donor
    // row. If that shape changes, preserve the owner row rather than guessing
    // from a display name, list position, search result, or page offset.
    if (!rows.length) return;
    if (matched.some((item) => !item)) {
      // Authorization loss has its own recovery message. Do not mask it with
      // a generic loading notice after the typed fields have been cleared.
      if (this.authorizationLost) {
        table.parentElement?.querySelector('[data-material-library-identity-notice="attachment"]')?.remove();
        return;
      }
      // A complete current read that no longer contains this visible ID must
      // never leave fields from an earlier typed record on the donor row.
      this.clearEnrichedMetadata();
      if (table.dataset.materialLibraryAttachmentTable !== 'unresolved') table.dataset.materialLibraryAttachmentTable = 'unresolved';
      let notice = table.parentElement?.querySelector<HTMLElement>('[data-material-library-identity-notice="attachment"]');
      if (!notice) {
        notice = document.createElement('p');
        notice.dataset.materialLibraryIdentityNotice = 'attachment';
        notice.style.cssText = 'margin:8px 12px;color:#646A73;font-size:12px;line-height:18px';
        table.before(notice);
      }
      const message = this.metadataLoaded || this.metadataReadFailed
        ? '部分附件信息暂不可用，已保留原有记录。'
        : '正在读取附件信息…';
      if (notice.textContent !== message) notice.textContent = message;
      return;
    }
    table.parentElement?.querySelector('[data-material-library-identity-notice="attachment"]')?.remove();
    if (table.dataset.materialLibraryAttachmentTable !== 'true') table.dataset.materialLibraryAttachmentTable = 'true';
    const header = table.querySelector<HTMLTableRowElement>('thead tr');
    if (header && !this.attachmentHeader) this.attachmentHeader = Array.from(header.cells).map((cell) => snapshotNode(cell));
    if (header && header.cells.length === 6) {
      header.cells[0].textContent = '名称';
      header.cells[4].textContent = '创建时间';
      const status = document.createElement('th'); status.textContent = '启用状态';
      const version = document.createElement('th'); version.textContent = '所属分组';
      for (const cell of [status, version]) cell.style.cssText = header.cells[4].style.cssText;
      header.insertBefore(status, header.cells[5]);
      header.insertBefore(version, header.cells[6]);
    }
    rows.forEach((row, index) => {
      const item = matched[index]!;
      if (!this.attachmentRows.has(row)) this.attachmentRows.set(row, { cells: Array.from(row.cells).map((cell) => snapshotNode(cell)) });
      const metadataVersion = `${item.id}:${item.version}`;
      if (row.dataset.materialLibraryMetadataVersion === metadataVersion) return;
      row.cells[3].textContent = formatSize(item.file_size);
      row.cells[4].textContent = formatTime(item.created_at);
      if (row.cells.length === 6) {
        const status = document.createElement('td');
        const version = document.createElement('td');
        for (const cell of [status, version]) cell.style.cssText = row.cells[4].style.cssText;
        row.insertBefore(status, row.cells[5]);
        row.insertBefore(version, row.cells[6]);
      }
      row.cells[5].textContent = item.enabled ? '启用' : '已停用';
      row.cells[6].replaceChildren();
      mountMaterialGroupControl(row.cells[6],this.page,item);
      // The MIME type already has its own column. Leave the first cell for a
      // scannable display name instead of spending half the row on a second
      // long `application/pdf` badge.
      const duplicateType = row.cells[0]?.querySelector<HTMLElement>('span:first-child');
      if (duplicateType) duplicateType.style.display = 'none';
      const operations = row.cells[7]?.querySelector<HTMLElement>('div');
      if (operations) operations.style.cssText = 'display:flex;flex-wrap:nowrap;gap:2px;justify-content:flex-end;white-space:nowrap';
      row.dataset.materialLibraryMetadataVersion = metadataVersion;
    });
  }

  private annotateMiniPrograms(): void {
    const grid = Array.from(this.stage.querySelectorAll<HTMLElement>('div'))
      .find((candidate) => candidate.dataset.materialLibraryMiniDirectory === 'true' || (candidate.style.display === 'grid' && candidate.style.gridTemplateColumns.includes('repeat(4')));
    if (!grid) return;
    const byID = new Map(this.miniPrograms.map((item) => [String(item.id), item]));
    grid.dataset.materialLibraryMiniDirectory = 'true';
    grid.setAttribute('role', 'table');
    grid.setAttribute('aria-label', '小程序素材目录');
    grid.style.cssText = 'display:grid;grid-template-columns:minmax(0,1fr);gap:0;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden;background:#fff';
    let header = grid.querySelector<HTMLElement>(':scope > [data-material-library-mini-header]');
    if (!header) {
      header = document.createElement('div');
      header.dataset.materialLibraryMiniHeader = 'true';
      header.setAttribute('role', 'row');
      header.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) minmax(150px,1fr) 88px;gap:10px;align-items:center;padding:9px 12px;background:#FAFAFB;border-bottom:1px solid #DEE0E3;color:#8F959E;font-size:12px;font-weight:500';
      ['封面', '名称 / 标题', 'AppID', '页面', '状态 / 封面', '更新时间', '所属分组', '操作'].forEach((label) => {
        const cell = document.createElement('span'); cell.setAttribute('role', 'columnheader'); cell.textContent = label; header!.append(cell);
      });
      grid.prepend(header);
    }
    const cards = Array.from(grid.children).filter((node): node is HTMLElement => node instanceof HTMLElement && node.dataset.materialLibraryMiniHeader !== 'true');
    cards.forEach((card, index) => {
      // Names are display values, never an identity. `m.resourceId` is added
      // by the V3 render seam and remains correct across duplicate names,
      // pagination and committed searches.
      const item = byID.get(card.dataset.materialLibraryId || '');
      const metadataVersion = item ? `${item.id}:${item.version}` : `unresolved:${card.dataset.materialLibraryId || index + 1}`;
      if (card.dataset.materialLibraryMetadataVersion === metadataVersion) return;
      let source = this.miniCards.get(card);
      if (!source) {
        const inner = card.firstElementChild as HTMLElement | null;
        const cover = inner?.firstElementChild as HTMLElement | null;
        const body = inner?.lastElementChild as HTMLElement | null;
        if (!inner || !cover || !body) return;
        const [nameNode, thumbnailStatus, enabledNode, actions] = Array.from(body.children) as HTMLElement[];
        if (!nameNode || !thumbnailStatus || !enabledNode || !actions) return;
        source = {
          card: snapshotNode(card), cover: snapshotNode(cover), name: snapshotNode(nameNode), thumbnail: snapshotNode(thumbnailStatus), enabled: snapshotNode(enabledNode), actions: snapshotNode(actions),
          inner, body, coverNode: cover, nameNode, thumbnailNode: thumbnailStatus, enabledNode, actionsNode: actions,
        };
        this.miniCards.set(card, source);
      } else {
        // A prior compact directory render moved these nodes directly under
        // `card`. Restore the captured donor structure before recomputing so
        // version changes update the same physical callback nodes instead of
        // reading a previous visual column as the donor body.
        restoreNode(source.coverNode, source.cover);
        restoreNode(source.nameNode, source.name);
        restoreNode(source.thumbnailNode, source.thumbnail);
        restoreNode(source.enabledNode, source.enabled);
        restoreNode(source.actionsNode, source.actions);
        source.body.replaceChildren(source.nameNode, source.thumbnailNode, source.enabledNode, source.actionsNode);
        source.inner.replaceChildren(source.coverNode, source.body);
      }
      const { coverNode: cover, nameNode, thumbnailNode: thumbnailStatus, enabledNode, actionsNode: actions } = source;
      card.setAttribute('role', 'row');
      card.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) minmax(150px,1fr) 88px;gap:10px;align-items:center;min-width:0;padding:9px 12px;border:0;border-bottom:1px solid #F2F3F5;border-radius:0;overflow:visible;background:#fff';
      cover.setAttribute('role', 'cell');
      // The frozen cover itself opens edit. Keep that identity explicit so a
      // permission loss blocks the direct div handler as well as row buttons.
      cover.dataset.materialLibraryMiniCover = 'true';
      cover.dataset.materialLibraryMutationControl = 'true';
      cover.style.cssText = 'height:56px;border-radius:5px;grid-column:1;cursor:pointer;background:#EFF4FF';
      nameNode.setAttribute('role', 'cell');
      nameNode.style.cssText = 'grid-column:2;min-width:0;font-size:13px;font-weight:500;color:#1F2329;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      // The donor source snapshot preserves the node identity. Its text is a
      // display projection, so replace only that text when a current typed
      // read changes the material name on the same physical card.
      const displayName = item?.name || '信息待确认';
      nameNode.replaceChildren(document.createTextNode(displayName));
      const title = document.createElement('small'); title.textContent = item?.title || '信息待确认'; title.style.cssText = 'display:block;margin-top:3px;color:#8F959E;font-size:12px;font-weight:400;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      nameNode.append(title);
      const appid = document.createElement('span'); appid.setAttribute('role', 'cell'); appid.textContent = item?.appid || '—'; appid.style.cssText = 'grid-column:3;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const page = document.createElement('span'); page.setAttribute('role', 'cell'); page.textContent = item?.page_path || item?.pagepath || '—'; page.style.cssText = 'grid-column:4;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const sourceThumbnailStatus = thumbnailStatus.textContent?.replace(/^\s*[●•]\s*/, '').trim() || '';
      const thumbnailText = sourceThumbnailStatus === 'ready' || sourceThumbnailStatus === '可用' ? '封面可用' : sourceThumbnailStatus === 'not_available' ? '封面暂不可用' : sourceThumbnailStatus || '封面状态未知';
      const enabledText = item ? (item.enabled ? '已启用' : '已停用') : (enabledNode.textContent?.trim() || '状态待确认');
      thumbnailStatus.textContent = `${enabledText} · ${thumbnailText}`;
      thumbnailStatus.setAttribute('role', 'cell');
      thumbnailStatus.style.cssText = `grid-column:5;min-width:0;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:${thumbnailText === '封面可用' ? '#237804' : '#B54708'}`;
      enabledNode.style.display = 'none';
      const updated = document.createElement('span'); updated.setAttribute('role', 'cell'); updated.textContent = item ? formatTime(item.updated_at) : '信息待确认'; updated.style.cssText = 'grid-column:6;min-width:0;color:#646A73;font-size:12px;line-height:18px';
      const groupCell=document.createElement('span');groupCell.setAttribute('role','cell');groupCell.style.cssText='grid-column:7;display:flex;gap:6px;align-items:center;flex-wrap:wrap';
      if(item) bindMaterialGroup(card,item.id,groupCell);
      actions.setAttribute('role', 'cell');
      actions.style.cssText = 'grid-column:8;display:flex;align-items:center;justify-content:flex-end;gap:2px;white-space:nowrap';
      // Keep the original elements in the row so the donor's direct event
      // handlers continue to target their own entity, including duplicates.
      card.replaceChildren(cover, nameNode, appid, page, thumbnailStatus, updated, groupCell, actions);
      card.dataset.materialLibraryMetadataVersion = metadataVersion;
    });
  }

  private renderMetadataError(error: unknown): void {
    const existing = this.stage.querySelector<HTMLElement>('[data-material-library-read-error]');
    const status = apiStatus(error);
    const message = status === 401
      ? '登录状态已失效，请重新登录后查看素材。'
      : status === 403
        ? '当前账号无权查看该类素材。'
        : '素材列表暂不可读取，当前内容已保留。';
    if (existing) {
      if (existing.textContent !== message) existing.textContent = message;
      return;
    }
    const node = document.createElement('p');
    node.dataset.materialLibraryReadError = 'true'; node.setAttribute('role', 'alert'); node.textContent = message;
    node.style.cssText = 'margin:0;color:#B42318;font-size:12px;line-height:20px';
    const input = this.stage.querySelector<HTMLElement>(this.config.querySelector);
    const section = input?.closest('div')?.parentElement || input?.parentElement;
    if (section) section.after(node);
    else this.stage.prepend(node);
  }
}

function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTime(value: string): string {
  if (!value) return '—';
  try { return formatShanghaiDateTime(value); } catch { return '—'; }
}

function boot(): void {
  if (!document.querySelector('main#stage[data-material-library-workspace="true"]')) return;
  const page = document.body?.dataset.page;
  if (page !== 'attach' && page !== 'mpLib') return;
  const stage = document.getElementById('stage');
  if (!stage || stage.dataset.materialLibraryPresentationMounted === 'true') return;
  stage.dataset.materialLibraryPresentationMounted = 'true';
  new FrozenMaterialPresentation(page, stage).start();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot, { once: true });
else boot();
