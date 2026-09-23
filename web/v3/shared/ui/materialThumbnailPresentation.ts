// Shared, V3-owned rendering for caller-authorised material thumbnails.
//
// This module deliberately does not validate, transform, fetch, upload, or
// revoke URLs.  The owning Media/adapter caller supplies an already-authorised
// display URL (including a private blob URL when applicable) and owns its
// abort/revocation lifecycle.  The component only represents browser load
// state so list, picker, and readonly content do not invent parallel visual
// error handling.

export type MaterialThumbnailState = 'loading' | 'loaded' | 'error' | 'no_url';

export type MaterialThumbnailOptions = {
  /** Caller-authorised URL. An absent URL produces the no_url state. */
  url?: string;
  alt?: string;
  /** Visible fallback for an image that the browser could not render. */
  unavailableLabel?: string;
  /** Short status while the browser is still loading a caller-authorised URL. */
  loadingLabel?: string;
  /** Compact label used when a caller elects to keep a no-url visual cell. */
  noURLLabel?: string;
  imageClassName?: string;
  /** The caller's visible image display rule after a successful browser load. */
  imageDisplay?: string;
  /** Optional caller-owned visible display rules for status layers. */
  loadingDisplay?: string;
  fallbackDisplay?: string;
  fallbackClassName?: string;
  imageDataAttribute?: string;
  fallbackDataAttribute?: string;
};

export type MaterialThumbnailResult = {
  /** The current browser presentation state, updated by load/error events. */
  readonly state: MaterialThumbnailState;
  image?: HTMLImageElement;
  loading: HTMLElement;
  fallback: HTMLElement;
};

const rendererCleanup = new WeakMap<HTMLElement, () => void>();

function dataAttribute(target: HTMLElement, attribute: string | undefined): void {
  if (!attribute) return;
  target.setAttribute(attribute, 'true');
}

/**
 * All nodes are created by this renderer, so it can reliably make a hidden
 * state win over caller CSS that intentionally gives status nodes `display`.
 * This does not alter caller-owned URL, image, or loader lifecycle.
 */
function setHidden(node: HTMLElement, hidden: boolean, visibleDisplay?: string): void {
  node.hidden = hidden;
  if (hidden) node.style.setProperty('display', 'none', 'important');
  else if (visibleDisplay) node.style.setProperty('display', visibleDisplay);
  else node.style.removeProperty('display');
}

/**
 * Renders an inert thumbnail state into a caller-owned element. The returned
 * nodes make tests and caller-specific accessibility hooks possible without
 * granting this shared renderer any URL-trust or network authority.
 */
export function renderMaterialThumbnail(target: HTMLElement, options: MaterialThumbnailOptions): MaterialThumbnailResult {
  rendererCleanup.get(target)?.();
  target.replaceChildren();
  let active = true;
  let state: MaterialThumbnailState = 'loading';
  const setState = (next: MaterialThumbnailState): void => {
    if (!active) return;
    state = next;
    target.dataset.materialThumbnailState = next;
  };
  const result = (image: HTMLImageElement | undefined, loading: HTMLElement, fallback: HTMLElement): MaterialThumbnailResult => ({
    get state() { return state; }, image, loading, fallback,
  });
  const url = String(options.url || '').trim();
  const loading = document.createElement('span');
  loading.className = 'aicrm-material-thumbnail__loading';
  loading.textContent = options.loadingLabel || '加载预览…';
  const fallback = document.createElement('span');
  fallback.className = options.fallbackClassName || 'aicrm-material-thumbnail__fallback';
  dataAttribute(fallback, options.fallbackDataAttribute);
  setHidden(loading, false, options.loadingDisplay);

  if (!url) {
    setState('no_url');
    fallback.textContent = options.noURLLabel || '暂无预览';
    setHidden(fallback, false, options.fallbackDisplay);
    setHidden(loading, true);
    target.append(loading, fallback);
    rendererCleanup.set(target, () => { active = false; });
    return result(undefined, loading, fallback);
  }

  setState('loading');
  fallback.textContent = options.unavailableLabel || '预览暂不可用';
  setHidden(fallback, true);
  const image = document.createElement('img');
  image.className = options.imageClassName || 'aicrm-material-thumbnail__image';
  dataAttribute(image, options.imageDataAttribute);
  image.src = url;
  image.alt = options.alt || '';
  setHidden(image, true);
  const onLoad = () => {
    setState('loaded');
    setHidden(loading, true);
    setHidden(fallback, true);
    setHidden(image, false, options.imageDisplay);
  };
  const onError = () => {
    setState('error');
    setHidden(loading, true);
    setHidden(image, true);
    setHidden(fallback, false, options.fallbackDisplay);
  };
  image.addEventListener('load', onLoad);
  image.addEventListener('error', onError);
  rendererCleanup.set(target, () => {
    active = false;
    image.removeEventListener('load', onLoad);
    image.removeEventListener('error', onError);
  });
  target.append(loading, image, fallback);
  return result(image, loading, fallback);
}
