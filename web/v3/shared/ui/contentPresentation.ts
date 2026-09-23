// Shared V3 representation of a persisted content package. It owns browser
// draft normalisation and safe rendering only: callers retain their authorised
// catalogue reads and every save/send/provider command.
import { renderMaterialThumbnail } from './materialThumbnailPresentation';

export type ContentMaterialKind = 'image' | 'miniprogram' | 'attachment' | 'group_invite';
export type ContentMaterialSource = 'media-library';
export type ContentMaterialOrder = 'canonical_by_kind' | 'caller_persisted';

export type ContentPackage = {
  content_text: string;
  image_library_ids: number[];
  miniprogram_library_ids: number[];
  attachment_library_ids: number[];
  group_invite_library_ids: number[];
};

/** A directory record for the Media-owned ID namespace used by ContentPackage. */
export type ContentMaterialRecord = {
  source: ContentMaterialSource;
  kind: ContentMaterialKind;
  id: number;
  label: string;
  subtitle?: string;
  /** A caller-authorised display URL. It is never used for upload or Provider preview. */
  thumbnailURL?: string;
  disabledReason?: string;
};

export type ContentVariable = {
  /** The exact token as persisted in text, in the caller's real syntax. */
  token: string;
  label: string;
  /** A historical token may remain visible but cannot be inserted again. */
  disabledReason?: string;
};

export type ContentVariablePolicy = {
  /** The caller owns the syntax: the shared component never assumes {{...}} means sendable. */
  scan(text: string): readonly string[];
  variables?: readonly ContentVariable[];
  unknownTokenReason?: string;
  /** Some callers preserve historical tokens as notices; others reject a save. */
  invalidBlocksConfirm?: boolean;
};

export type ContentVariableIssue = { token: string; reason: string; blocking: boolean };

/**
 * A caller-owned block that belongs beside a persisted content package but is
 * not a Media-library selection.  It is deliberately display-only: the
 * caller supplies the already-authorised title, path and preview URL, while
 * this shared renderer never reads, saves or interprets the Owner contract.
 */
export type ContentPresentationSupplement = {
  key: string;
  kind: 'excel_card';
  title: string;
  description?: string;
  thumbnailURL?: string;
  unavailableReason?: string;
};

export type ContentPresentationOptions = {
  mode: 'preview' | 'readonly';
  package: unknown;
  selectedRecords?: readonly ContentMaterialRecord[];
  /** Caller-persisted order is accepted only where the domain supplied it. */
  materialOrder?: ContentMaterialOrder;
  variablePolicy?: ContentVariablePolicy;
  /** Caller-owned persisted blocks, rendered after text and Media records. */
  supplements?: readonly ContentPresentationSupplement[];
  /** The caller's storage/normalisation rule for visible text. */
  normalizeText?: (value: string) => string;
  title?: string;
  /** A caller-specific persistence explanation for a readonly snapshot. */
  readonlyNote?: string;
};

const kindFields: ReadonlyArray<readonly [ContentMaterialKind, keyof Pick<ContentPackage, 'image_library_ids' | 'miniprogram_library_ids' | 'attachment_library_ids' | 'group_invite_library_ids'>]> = [
  ['image', 'image_library_ids'],
  ['miniprogram', 'miniprogram_library_ids'],
  ['attachment', 'attachment_library_ids'],
  ['group_invite', 'group_invite_library_ids'],
];

const kindLabels: Record<ContentMaterialKind, string> = {
  image: '图片', miniprogram: '小程序', attachment: '附件', group_invite: '群邀请',
};

function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function id(value: unknown): number | undefined {
  const parsed = typeof value === 'number' ? value : Number(String(value ?? '').trim());
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function ids(value: unknown): number[] {
  const values = Array.isArray(value) ? value : String(value ?? '').split(',');
  const output: number[] = [];
  for (const value of values) {
    const parsed = id(value);
    if (parsed !== undefined && !output.includes(parsed)) output.push(parsed);
  }
  return output;
}

function kind(value: unknown): ContentMaterialKind | undefined {
  const candidate = String(value ?? '').trim();
  return candidate === 'image' || candidate === 'miniprogram' || candidate === 'attachment' || candidate === 'group_invite'
    ? candidate : undefined;
}

/** A stable key cannot collide across Media material types. */
export function contentMaterialKey(source: ContentMaterialSource, materialKind: ContentMaterialKind, materialID: number): string {
  return `${source}:${materialKind}:${materialID}`;
}

function recordKey(record: ContentMaterialRecord): string | undefined {
  const materialKind = kind(record.kind);
  const materialID = id(record.id);
  if (record.source !== 'media-library' || !materialKind || materialID === undefined) return undefined;
  return contentMaterialKey(record.source, materialKind, materialID);
}

function recordCopy(record: ContentMaterialRecord): ContentMaterialRecord | undefined {
  const key = recordKey(record);
  if (!key) return undefined;
  const materialKind = kind(record.kind)!;
  const materialID = id(record.id)!;
  return {
    source: 'media-library', kind: materialKind, id: materialID,
    label: String(record.label || `${kindLabels[materialKind]}素材 #${materialID}`),
    subtitle: record.subtitle ? String(record.subtitle) : undefined,
    thumbnailURL: record.thumbnailURL ? String(record.thumbnailURL) : undefined,
    disabledReason: record.disabledReason ? String(record.disabledReason) : undefined,
  };
}

/**
 * Existing content-package consumers historically trim text. A new editor can
 * supply its owner's explicit normalizer so preview and confirm share one
 * contract instead of silently changing text at save time.
 */
export function normalizeContentPackage(value: unknown, normalizeText?: (value: string) => string): ContentPackage {
  const source = object(value);
  return {
    content_text: normalizeText ? normalizeText(String(source.content_text ?? '')) : String(source.content_text ?? '').trim(),
    image_library_ids: ids(source.image_library_ids),
    miniprogram_library_ids: ids(source.miniprogram_library_ids),
    attachment_library_ids: ids(source.attachment_library_ids),
    group_invite_library_ids: ids(source.group_invite_library_ids),
  };
}

/**
 * Converts caller-owned Media records to the established package fields. The
 * package intentionally carries no UI-only order field; field order survives
 * only inside each kind unless a domain adapter maps the supplied record order
 * to its actual persisted ordered contract.
 */
export function contentPackageFromRecords(text: unknown, records: readonly ContentMaterialRecord[], normalizeText?: (value: string) => string): ContentPackage {
  const result: ContentPackage = {
    content_text: normalizeText ? normalizeText(String(text ?? '')) : String(text ?? '').trim(), image_library_ids: [], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [],
  };
  const seen = new Set<string>();
  for (const record of records) {
    const normalized = recordCopy(record);
    if (!normalized) continue;
    const key = contentMaterialKey(normalized.source, normalized.kind, normalized.id);
    if (seen.has(key)) continue;
    seen.add(key);
    const field = kindFields.find(([candidate]) => candidate === normalized.kind)?.[1];
    if (field) result[field].push(normalized.id);
  }
  return result;
}

function wantedKeys(value: ContentPackage): Set<string> {
  const output = new Set<string>();
  for (const [materialKind, field] of kindFields) {
    for (const materialID of value[field]) output.add(contentMaterialKey('media-library', materialKind, materialID));
  }
  return output;
}

function unresolved(materialKind: ContentMaterialKind, materialID: number): ContentMaterialRecord {
  return {
    source: 'media-library', kind: materialKind, id: materialID,
    label: `${kindLabels[materialKind]}素材 #${materialID}`,
    disabledReason: '素材状态待目录确认',
  };
}

/**
 * Returns selected records in a caller-proved persisted sequence, or else in
 * the package's stable per-kind order. A record from another source is never
 * permitted to decorate a same-numbered Media reference.
 */
export function recordsForContentPackage(value: unknown, selectedRecords: readonly ContentMaterialRecord[] = [], materialOrder: ContentMaterialOrder = 'canonical_by_kind'): ContentMaterialRecord[] {
  const normalized = normalizeContentPackage(value);
  const wanted = wantedKeys(normalized);
  const supplied = new Map<string, ContentMaterialRecord>();
  for (const source of selectedRecords) {
    const record = recordCopy(source);
    if (record) supplied.set(contentMaterialKey(record.source, record.kind, record.id), record);
  }
  const output: ContentMaterialRecord[] = [];
  const emitted = new Set<string>();
  if (materialOrder === 'caller_persisted') {
    for (const source of selectedRecords) {
      const record = recordCopy(source);
      if (!record) continue;
      const key = contentMaterialKey(record.source, record.kind, record.id);
      if (wanted.has(key) && !emitted.has(key)) {
        output.push(record);
        emitted.add(key);
      }
    }
  }
  for (const [materialKind, field] of kindFields) for (const materialID of normalized[field]) {
    const key = contentMaterialKey('media-library', materialKind, materialID);
    if (emitted.has(key)) continue;
    output.push(supplied.get(key) || unresolved(materialKind, materialID));
    emitted.add(key);
  }
  return output;
}

/** Variables are caller-authorised syntax and tokens; this layer never resolves identities or values. */
export function contentVariableIssues(text: unknown, policy: ContentVariablePolicy | undefined): ContentVariableIssue[] {
  if (!policy) return [];
  const authorised = new Map((policy.variables || []).map((variable) => [variable.token, variable]));
  let scanned: readonly string[];
  try {
    scanned = policy.scan(String(text ?? ''));
  } catch {
    return [{ token: '变量规则', reason: '当前场景的变量校验不可用', blocking: Boolean(policy.invalidBlocksConfirm) }];
  }
  const output: ContentVariableIssue[] = [];
  for (const candidate of scanned) {
    const token = String(candidate || '');
    if (!token || output.some((item) => item.token === token)) continue;
    const variable = authorised.get(token);
    const reason = variable?.disabledReason?.trim() || (!variable ? policy.unknownTokenReason?.trim() || '该变量当前不可用' : '');
    if (reason) output.push({ token, reason, blocking: Boolean(policy.invalidBlocksConfirm) });
  }
  return output;
}

export function contentSummary(value: unknown): { text: string; counts: Record<ContentMaterialKind, number> } {
  const normalized = normalizeContentPackage(value);
  return {
    text: normalized.content_text ? (normalized.content_text.length > 60 ? `${normalized.content_text.slice(0, 60)}…` : normalized.content_text) : '未填写话术',
    counts: {
      image: normalized.image_library_ids.length,
      miniprogram: normalized.miniprogram_library_ids.length,
      attachment: normalized.attachment_library_ids.length,
      group_invite: normalized.group_invite_library_ids.length,
    },
  };
}

function controlledThumbnail(value: string | undefined): string | undefined {
  if (!value) return undefined;
  try {
    const url = new URL(value, window.location.origin);
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.href : undefined;
  } catch {
    return undefined;
  }
}

function supplements(value: readonly ContentPresentationSupplement[] | undefined): ContentPresentationSupplement[] {
  const output: ContentPresentationSupplement[] = [];
  const seen = new Set<string>();
  for (const candidate of value || []) {
    const key = String(candidate?.key || '').trim();
    if (!key || seen.has(key) || candidate?.kind !== 'excel_card') continue;
    seen.add(key);
    output.push({
      key,
      kind: 'excel_card',
      title: String(candidate.title || '标题未记录'),
      description: candidate.description ? String(candidate.description) : undefined,
      thumbnailURL: candidate.thumbnailURL ? String(candidate.thumbnailURL) : undefined,
      unavailableReason: candidate.unavailableReason ? String(candidate.unavailableReason) : undefined,
    });
  }
  return output;
}

/**
 * The sole browser renderer for draft preview and persisted readonly content.
 * It renders safe text/controlled thumbnails but never calls save, upload,
 * send, validation, or Provider preview APIs.
 */
export function renderContentPresentation(target: HTMLElement, options: ContentPresentationOptions): void {
  const normalized = normalizeContentPackage(options.package, options.normalizeText);
  const records = recordsForContentPackage(normalized, options.selectedRecords, options.materialOrder);
  const issues = contentVariableIssues(normalized.content_text, options.variablePolicy);
  const section = document.createElement('section');
  section.className = `aicrm-content-presentation aicrm-content-presentation--${options.mode}`;
  section.dataset.contentPresentation = options.mode;
  const heading = document.createElement('h4');
  heading.textContent = options.title || (options.mode === 'preview' ? '内容预览' : '已保存内容');
  const note = document.createElement('p');
  note.className = 'aicrm-content-presentation__note';
  const readonlyNote = String(options.readonlyNote || '').trim();
  note.textContent = options.mode === 'preview'
    ? '预览不会发送内容。'
    : readonlyNote || '此内容以最近一次保存结果为准。';
  const text = document.createElement('div');
  text.className = 'aicrm-content-presentation__text';
  text.textContent = normalized.content_text || '未填写话术';
  section.append(heading, note, text);
  if (issues.length) {
    const alert = document.createElement('p');
    alert.className = 'aicrm-content-presentation__notice';
    alert.setAttribute('role', 'status');
    alert.textContent = issues.map((issue) => `${issue.token}：${issue.reason}`).join('；');
    section.append(alert);
  }
  const materials = document.createElement('ol');
  materials.className = 'aicrm-content-presentation__materials';
  for (const record of records) {
    const item = document.createElement('li');
    item.className = 'aicrm-content-presentation__material';
    item.dataset.contentMaterialKey = contentMaterialKey(record.source, record.kind, record.id);
    const visual = document.createElement('div');
    visual.className = 'aicrm-content-presentation__visual';
    const thumbnailURL = controlledThumbnail(record.thumbnailURL);
    if (!thumbnailURL) item.classList.add('aicrm-content-presentation__material--without-thumbnail');
    if (thumbnailURL) renderMaterialThumbnail(visual, {
      url: thumbnailURL,
      imageDisplay: 'block',
      loadingDisplay: 'grid',
      fallbackDisplay: 'grid',
      unavailableLabel: '缩略图暂不可用',
      imageClassName: 'aicrm-content-presentation__thumbnail',
      fallbackClassName: 'aicrm-content-presentation__thumbnail-fallback',
    });
    const details = document.createElement('div');
    details.className = 'aicrm-content-presentation__material-details';
    const label = document.createElement('strong');
    label.textContent = `${kindLabels[record.kind]}：${record.label}`;
    details.append(label);
    if (record.subtitle) {
      const subtitle = document.createElement('span');
      subtitle.textContent = record.subtitle;
      details.append(subtitle);
    }
    if (record.disabledReason) {
      const unavailable = document.createElement('span');
      unavailable.className = 'aicrm-content-presentation__notice';
      unavailable.textContent = record.disabledReason;
      details.append(unavailable);
    }
    if (thumbnailURL) item.append(visual);
    item.append(details);
    materials.append(item);
  }
  if (records.length) section.append(materials);
  const supplementalBlocks = supplements(options.supplements);
  if (supplementalBlocks.length) {
    const list = document.createElement('ol');
    list.className = 'aicrm-content-presentation__supplements';
    for (const block of supplementalBlocks) {
      const item = document.createElement('li');
      item.className = 'aicrm-content-presentation__supplement';
      item.dataset.contentPresentationSupplement = block.key;
      const thumbnailURL = controlledThumbnail(block.thumbnailURL);
      if (!thumbnailURL) item.classList.add('aicrm-content-presentation__supplement--without-thumbnail');
      if (thumbnailURL) {
        const visual = document.createElement('div');
        visual.className = 'aicrm-content-presentation__visual';
        renderMaterialThumbnail(visual, {
          url: thumbnailURL,
          imageDisplay: 'block',
          loadingDisplay: 'grid',
          fallbackDisplay: 'grid',
          unavailableLabel: '封面暂不可用',
          imageClassName: 'aicrm-content-presentation__thumbnail',
          fallbackClassName: 'aicrm-content-presentation__thumbnail-fallback',
        });
        item.append(visual);
      }
      const details = document.createElement('div');
      details.className = 'aicrm-content-presentation__material-details';
      const title = document.createElement('strong');
      title.textContent = `小程序卡片：${block.title}`;
      details.append(title);
      if (block.description) {
        const description = document.createElement('span');
        description.textContent = block.description;
        details.append(description);
      }
      if (block.unavailableReason) {
        const unavailable = document.createElement('span');
        unavailable.className = 'aicrm-content-presentation__notice';
        unavailable.textContent = block.unavailableReason;
        details.append(unavailable);
      }
      item.append(details);
      list.append(item);
    }
    section.append(list);
  }
  target.replaceChildren(section);
}
