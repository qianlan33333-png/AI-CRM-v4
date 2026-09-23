import { installGroupSelects, withGroupSelection } from './materialGroupManagement';
// V3 feedback overlay for byte-frozen media forms.  The adapter does not own
// media writes; it makes the existing request/result boundary observable and
// prevents a second click while that write or its mandatory readback is live.

export {};
import { materialGroupRequest } from './materialGrouping';
import { clearActionBusy, setActionBusy } from './actionFeedback';

type SaveState = {
  button: HTMLButtonElement;
  label: string;
  mutationStarted: boolean;
  mutationAccepted: boolean;
  idempotencyKey?: string;
  mutationFingerprint?: string;
  mutationPath?: string;
  mutationMethod?: string;
  pendingPreflight: number;
  readbackStarted: boolean;
  pendingReadbacks: number;
};

let activeSave: SaveState | undefined;
let suppressNextMaterialRejection = false;
let pendingUnknownMutation: { page: string; path: string; method: string; fingerprint: string; key: string; saved: boolean } | undefined;
const donorFetch = globalThis.fetch.bind(globalThis);

// Keep file selection feedback next to the control: transient global toasts
// disappear before users finish entering the material name and tags.
function installFileFeedback(): void {
  if (typeof document === 'undefined' || !document.body) return;
  if (!['images', 'attach'].includes(document.body.dataset.page || '')) return;
  for (const input of document.querySelectorAll<HTMLInputElement>('#fImgUpFile, #fAttUpFile')) {
    if (input.dataset.materialFileFeedback) continue;
    input.dataset.materialFileFeedback = 'true';
    const image = input.id === 'fImgUpFile';
    input.accept = image ? 'image/png,image/jpeg,image/gif' : 'application/pdf';
    input.setAttribute('aria-label', image ? '选择图片文件' : '选择 PDF 文件');
    const status = document.createElement('p');
    status.id = `${input.id}-status`;
    status.setAttribute('role', 'status');
    status.style.cssText = 'margin:8px 0 0;color:#646A73;font-size:12px;line-height:1.5';
    input.setAttribute('aria-describedby', status.id);
    input.after(status);
    const describe = (cancelled = false) => {
      const file = input.files?.[0];
      status.textContent = file
        ? `已选择 ${file.name}（${Math.max(1, Math.ceil(file.size / 1024))} KB），点击上传后保存。`
        : `${cancelled ? '已取消选择。' : ''}${image ? '请选择 PNG、JPEG 或 GIF 图片' : '请选择 PDF 附件'}，最大 10 MB。`;
    };
    input.addEventListener('change', () => describe());
    input.addEventListener('cancel', () => describe(true));
    describe();
  }
}

type ThumbnailControl = {
  originalLabel: string;
  unavailableLabel: string;
  explanation: string;
  kind: 'upload' | 'refresh';
  modalAction: '创建' | '保存';
};

const thumbnailControls: ThumbnailControl[] = [
  {
    originalLabel: '＋ 上传缩略图（将缓存到企微）',
    unavailableLabel: '上传缩略图（暂不支持）',
    explanation: '当前不支持企微缩略图上传；不会上传至企微。',
    kind: 'upload',
    modalAction: '创建',
  },
  {
    originalLabel: '刷新缩略图缓存',
    unavailableLabel: '刷新缩略图缓存（暂不支持）',
    explanation: '当前不支持企微缩略图刷新；不会发起企微调用。',
    kind: 'refresh',
    modalAction: '保存',
  },
];

function isMiniProgramModal(button: HTMLButtonElement, modalAction: ThumbnailControl['modalAction']): boolean {
  const modal = button.closest<HTMLElement>('div[style*="position:fixed"]');
  if (!modal) return false;
  return Boolean(
    modal.querySelector('#fMpName') &&
    modal.querySelector('#fMpAppid') &&
    modal.querySelector('#fMpPath') &&
    modal.querySelector('#fMpTitle') &&
    [...modal.querySelectorAll('button')].some((candidate) => candidate.textContent?.trim() === modalAction),
  );
}

function installMiniProgramThumbnailFeedback(): void {
  if (typeof document === 'undefined' || !document.body || document.body.dataset.page !== 'mpLib') return;
  for (const control of thumbnailControls) {
    for (const button of document.querySelectorAll<HTMLButtonElement>('button')) {
      if (button.dataset.materialMiniThumbnailUnavailable || button.textContent?.trim() !== control.originalLabel) continue;
      if (!isMiniProgramModal(button, control.modalAction)) continue;
      const help = document.createElement('p');
      help.id = `material-mp-thumbnail-${control.kind}-unavailable`;
      help.dataset.materialMiniThumbnailUnavailableHelp = 'true';
      help.setAttribute('role', 'status');
      help.textContent = control.explanation;
      help.style.cssText = 'margin:6px 0 0;color:#646A73;font-size:12px;line-height:1.5';
      button.dataset.materialMiniThumbnailUnavailable = control.kind;
      button.disabled = true;
      button.setAttribute('aria-disabled', 'true');
      button.setAttribute('aria-describedby', help.id);
      button.style.cursor = 'not-allowed';
      button.style.opacity = '0.58';
      button.textContent = control.unavailableLabel;
      button.after(help);
    }
  }
}

function installMaterialFeedback(): void {
  installGroupSelects();
  installFileFeedback();
  installMiniProgramThumbnailFeedback();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', installMaterialFeedback);
else installMaterialFeedback();
new MutationObserver(installMaterialFeedback).observe(document.documentElement, { childList: true, subtree: true });

function isMediaMutation(url: URL, method: string): boolean {
  if (method === 'GET' || method === 'HEAD') return false;
  return url.pathname.startsWith('/api/admin/image-library') ||
    url.pathname.startsWith('/api/admin/miniprogram-library') ||
    url.pathname.startsWith('/api/admin/attachment-library');
}

function isMediaReadback(url: URL, method: string): boolean {
  if (method !== 'GET') return false;
  return url.pathname === '/api/admin/image-library' ||
    url.pathname === '/api/admin/miniprogram-library' ||
    url.pathname === '/api/admin/attachment-library';
}

function message(text: string): void {
  document.getElementById('material-v3-save-message')?.remove();
  const node = document.createElement('div');
  node.id = 'material-v3-save-message';
  node.setAttribute('role', 'alert');
  node.textContent = text;
  node.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  document.body.appendChild(node);
  window.setTimeout(() => node.remove(), 5000);
}

function release(state: SaveState): void {
  if (activeSave !== state) return;
  clearActionBusy(state.button);
  activeSave = undefined;
}

function failMutation(state: SaveState): void {
  if (activeSave !== state) return;
  // The byte-frozen controller currently leaves these promise rejections
  // uncaught. It may gain local handling later, so reset at the actual HTTP
  // failure instead of waiting for a global unhandled-rejection event.
  suppressNextMaterialRejection = true;
  release(state);
  message('素材保存失败；编辑内容已保留，可修正后重试。');
}

function failReadback(state: SaveState): void {
  if (activeSave !== state) return;
  if (state.idempotencyKey && state.mutationFingerprint != null && state.mutationPath && state.mutationMethod) {
    pendingUnknownMutation = { page: document.body.dataset.page || '', path: state.mutationPath, method: state.mutationMethod, fingerprint: state.mutationFingerprint, key: state.idempotencyKey, saved: true };
  }
  release(state);
  message('素材已保存，但回读失败；编辑内容已保留，可刷新后核对。');
}

function finishReadback(state: SaveState): void {
  queueMicrotask(() => {
    if (activeSave === state && state.readbackStarted && state.pendingReadbacks === 0) {
      if (pendingUnknownMutation?.key === state.idempotencyKey) pendingUnknownMutation = undefined;
      release(state);
    }
  });
}

function mutationFingerprint(input: RequestInfo | URL, init: RequestInit | undefined): string {
  const request = input instanceof Request ? input : undefined;
  const body = init?.body ?? request?.body;
  if (typeof body === 'string') return body;
  if (body instanceof URLSearchParams) return body.toString();
  if (body instanceof FormData) return [...body.entries()].map(([key, value]) => `${key}=${typeof value === 'string' ? value : `${value.name}:${value.size}:${value.type}:${value.lastModified}`}`).join('&');
  return body == null ? '' : String(body);
}

function idempotencyKey(state: SaveState, url: URL, method: string, input: RequestInfo | URL, init?: RequestInit): string {
  const fingerprint = mutationFingerprint(input, init);
  const retry = pendingUnknownMutation;
  if (retry && retry.page === document.body.dataset.page && retry.path === url.pathname && retry.method === method && retry.fingerprint === fingerprint) {
    state.mutationFingerprint = fingerprint;
    state.idempotencyKey = retry.key;
    return retry.key;
  }
  const key = `material-save-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
  state.mutationFingerprint = fingerprint;
  state.idempotencyKey = key;
  return key;
}

function unknownMutation(state: SaveState, url: URL, method: string): void {
  if (activeSave !== state || !state.idempotencyKey || state.mutationFingerprint == null) return;
  pendingUnknownMutation = { page: document.body.dataset.page || '', path: url.pathname, method, fingerprint: state.mutationFingerprint, key: state.idempotencyKey, saved: false };
  suppressNextMaterialRejection = true;
  release(state);
  message('素材保存结果未知；请在当前页面保持内容不变后重试，或先查看列表核对。');
}

type MiniProgramForm = {
  name: HTMLInputElement;
  appid: HTMLInputElement;
  pagePath: HTMLInputElement;
  title: HTMLInputElement;
  fields: HTMLElement;
};

function miniProgramForm(): MiniProgramForm | undefined {
  if (document.body.dataset.page !== 'mpLib') return undefined;
  const name = document.getElementById('fMpName');
  const appid = document.getElementById('fMpAppid');
  const pagePath = document.getElementById('fMpPath');
  const title = document.getElementById('fMpTitle');
  if (!(name instanceof HTMLInputElement) || !(appid instanceof HTMLInputElement) ||
      !(pagePath instanceof HTMLInputElement) || !(title instanceof HTMLInputElement)) return undefined;
  const fields = name.parentElement?.parentElement;
  if (!(fields instanceof HTMLElement)) return undefined;
  return { name, appid, pagePath, title, fields };
}

function clearMiniProgramValidation(form: MiniProgramForm): void {
  form.fields.querySelector('[data-material-mp-validation]')?.remove();
  for (const input of [form.name, form.appid, form.pagePath, form.title]) {
    input.removeAttribute('aria-invalid');
    const describedBy = input.dataset.materialMpDescribedBy;
    if (describedBy !== undefined) {
      if (describedBy) input.setAttribute('aria-describedby', describedBy);
      else input.removeAttribute('aria-describedby');
      delete input.dataset.materialMpDescribedBy;
    }
  }
}

function showMiniProgramValidation(
  form: MiniProgramForm,
  text: string,
  invalid: HTMLInputElement[] = [],
): void {
  clearMiniProgramValidation(form);
  const status = document.createElement('p');
  status.id = 'material-v3-mp-validation';
  status.dataset.materialMpValidation = 'true';
  status.setAttribute('role', invalid.length ? 'alert' : 'status');
  status.textContent = text;
  status.style.cssText = `margin:0;padding:9px 10px;border-radius:6px;font-size:13px;line-height:1.5;${invalid.length ? 'border:1px solid #FBC4C2;background:#FFF5F5;color:#B42318' : 'border:1px solid #D6E4FF;background:#F0F5FF;color:#245BDB'}`;
  form.fields.prepend(status);
  for (const input of invalid) {
    input.dataset.materialMpDescribedBy = input.getAttribute('aria-describedby') || '';
    const existing = input.dataset.materialMpDescribedBy.trim();
    input.setAttribute('aria-describedby', [existing, status.id].filter(Boolean).join(' '));
    input.setAttribute('aria-invalid', 'true');
  }
  invalid[0]?.focus();
}

function miniProgramPreflight(button: HTMLButtonElement): boolean {
  const label = button.textContent?.trim();
  if (label !== '创建' && label !== '保存') return false;
  const form = miniProgramForm();
  if (!form) return false;
  const editing = label === '保存';
  const required = editing
    ? [
      [form.name, '素材名称'],
      [form.appid, '小程序 AppID'],
      [form.pagePath, '页面路径'],
      [form.title, '卡片标题'],
    ] as const
    : [
      [form.name, '素材名称'],
      [form.appid, '小程序 AppID'],
      [form.pagePath, '页面路径'],
    ] as const;
  const missing = required.filter(([input]) => !input.value.trim());
  if (missing.length) {
    showMiniProgramValidation(
      form,
      `请填写${missing.map(([, label]) => label).join('、')}。`,
      missing.map(([input]) => input),
    );
    return true;
  }
  if (!editing && !form.title.value.trim())
    showMiniProgramValidation(form, '卡片标题为空，将使用素材名称。');
  else clearMiniProgramValidation(form);
  return false;
}

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  const url = new URL(request?.url || String(input), location.origin);
  init=withGroupSelection(url,method,init);
  const state = activeSave;
  const mutation = Boolean(state && isMediaMutation(url, method));
  const readback = Boolean(state && state.mutationAccepted && isMediaReadback(url, method));
  const preflight = Boolean(state && !state.mutationStarted && !state.mutationAccepted && isMediaReadback(url, method));
  if (mutation && state) state.mutationStarted = true;
  if (mutation && state) { state.mutationPath = url.pathname; state.mutationMethod = method; }
  if (readback && state) {
    state.readbackStarted = true;
    state.pendingReadbacks += 1;
  }
  if (preflight && state) state.pendingPreflight += 1;
  let nextInit = init;
  if (mutation && state) {
    const fingerprint = mutationFingerprint(input, init);
    const previous = pendingUnknownMutation;
    if (previous && previous.page === (document.body.dataset.page || '') && previous.path === url.pathname && previous.method === method && previous.fingerprint !== fingerprint) {
      suppressNextMaterialRejection = true;
      release(state);
      message(previous.saved ? '素材已保存但列表回读失败；请先刷新或查看列表核对，不能直接修改后再次创建。' : '素材保存结果未知；请保持内容不变后重试，或先查看列表核对。');
      return new Response(JSON.stringify({ code: 'material_save_recovery_required' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
    }
    const headers = new Headers(request?.headers);
    new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
    headers.set('Idempotency-Key', idempotencyKey(state, url, method, input, init));
    nextInit = { ...init, headers };
  }
  try {
    const response = await donorFetch(materialGroupRequest(input, method), nextInit);
    if (mutation && state) {
      if (response.ok) {
        state.mutationAccepted = true;
        pendingUnknownMutation = undefined;
      }
      else failMutation(state);
    }
    if (readback && state) {
      if (!response.ok) failReadback(state);
      else {
        state.pendingReadbacks -= 1;
        finishReadback(state);
      }
    }
    if (preflight && state) {
      if (!response.ok) failMutation(state);
      else state.pendingPreflight -= 1;
    }
    return response;
  } catch (error) {
    if (mutation && state) unknownMutation(state, url, method);
    if (preflight && state) failMutation(state);
    if (readback && state) failReadback(state);
    throw error;
  }
};

document.addEventListener('click', (event) => {
  const page = document.body.dataset.page;
  if (page !== 'images' && page !== 'mpLib' && page !== 'attach') return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  // Native disabled buttons do not dispatch a click, but this capture guard
  // also covers programmatic/synthetic clicks before the frozen controller's
  // inline handler can show its inaccurate generic capability toast.
  if (page === 'mpLib' && button?.dataset.materialMiniThumbnailUnavailable) {
    event.preventDefault();
    event.stopImmediatePropagation();
    return;
  }
  if (!button || !['保存', '创建', '上传'].includes(button.textContent?.trim() || '')) return;
  const groupControl=document.querySelector<HTMLSelectElement>('[data-material-group-select]');
  if(groupControl?.disabled){event.preventDefault();event.stopImmediatePropagation();message('分组信息尚未加载或当前账号没有编辑权限，请刷新核对。');return;}

  if (miniProgramPreflight(button)) {
    event.preventDefault();
    event.stopImmediatePropagation();
    return;
  }
  if (activeSave) {
    event.preventDefault();
    event.stopImmediatePropagation();
    return;
  }
  const state: SaveState = { button, label: button.textContent.trim(), mutationStarted: false, mutationAccepted: false, pendingPreflight: 0, readbackStarted: false, pendingReadbacks: 0 };
  activeSave = state;
  setActionBusy(button, '保存中…');
  // Validation may reject before it sends a request. That is not a save
  // failure and must leave the editor usable with its values intact. A
  // microtask runs after the frozen click handler, without creating a
  // time-based completion boundary for a real write. A resource-ID lookup
  // belongs to a real edit chain and remains locked until its PUT follows.
  queueMicrotask(() => { if (!state.mutationStarted && state.pendingPreflight === 0) release(state); });
}, true);

window.addEventListener('unhandledrejection', (event) => {
  const state = activeSave;
  if (!state) {
    if (suppressNextMaterialRejection) {
      suppressNextMaterialRejection = false;
      event.preventDefault();
    }
    return;
  }
  event.preventDefault();
  if (state.mutationAccepted) failReadback(state);
  else failMutation(state);
});

// Credential refresh remains owned by the existing Media background job.
// The operator library no longer mounts preparation diagnostics or polls them.
