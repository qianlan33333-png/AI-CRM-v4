import { mountPageHeaderActionElements, pageHeaderActionElementsHaveConnectedOrigins } from './shared/ui/pageHeaderActions';

function byText<T extends HTMLElement>(root: ParentNode, selector: string, label: string): T | undefined {
  return Array.from(root.querySelectorAll<T>(selector)).find((element) => element.textContent?.trim() === label);
}

type Mounted = { owner: string; elements: readonly HTMLElement[]; cleanup: () => void };

function sameElements(previous: Mounted | undefined, owner: string, next: readonly HTMLElement[]): boolean {
  return previous?.owner === owner && previous.elements.length === next.length && previous.elements.every((element, index) => element === next[index]);
}

function activeOrClean(previous: Mounted | undefined, owner: string): Mounted | undefined {
  if (previous?.owner !== owner) return previous;
  if (pageHeaderActionElementsHaveConnectedOrigins(owner, previous.elements)) return previous;
  previous.cleanup();
  return undefined;
}

function mountTagActions(previous: Mounted | undefined): Mounted | undefined {
  const stage = document.querySelector<HTMLElement>('#stage');
  if (!stage || !document.body.matches('[data-page="tags"]')) return undefined;
  const sync = byText<HTMLButtonElement>(stage, 'button', '同步企微标签');
  const createGroup = byText<HTMLButtonElement>(stage, 'button', '新增标签组');
  const createTag = byText<HTMLButtonElement>(stage, 'button', '新增标签');
  if (!sync || !createGroup || !createTag) return activeOrClean(previous, 'wecom-tags');
  const elements = [sync, createGroup, createTag];
  if (sameElements(previous, 'wecom-tags', elements)) return previous;
  const cleanup = mountPageHeaderActionElements('wecom-tags', elements);
  // The frozen document owns this former local title row. The V3 shell now
  // supplies the one visible page title, so hide only the 52px donor title
  // row—not a wrapping workspace that happens to contain the same text.
  const localTitle = Array.from(stage.querySelectorAll<HTMLElement>('div')).find((element) =>
    // The shared runtime adds a display:contents container before rendering
    // the frozen fragment. Unit fixtures may mount the fragment directly, so
    // accept either one or two levels below #stage, but never inspect a
    // deeper card heading with the same text.
    (element.parentElement === stage || element.parentElement?.parentElement === stage)
      && element.style.height === '52px'
      && element.textContent?.includes('企微标签管理'),
  );
  if (localTitle instanceof HTMLElement) {
    localTitle.dataset.pageHeaderDonorTitle = 'tags';
    localTitle.hidden = true;
  }
  return { owner: 'wecom-tags', elements, cleanup };
}

function mountPlanDetailActions(previous: Mounted | undefined): Mounted | undefined {
  if (!document.body.matches('[data-page="ai-assistant"]')) return undefined;
  const root = document.querySelector<HTMLElement>('[data-cloud-plan-root][data-page-mode="detail"]');
  const approve = root?.querySelector<HTMLButtonElement>('[data-plan-approve]');
  const reject = root?.querySelector<HTMLButtonElement>('[data-plan-reject]');
  const back = root?.querySelector<HTMLAnchorElement>('a[href="/admin/cloud-orchestrator/plans"]');
  if (!root || !approve || !reject || !back) return activeOrClean(previous, 'ai-plan-detail');
  const source = approve.closest<HTMLElement>('.cloud-plan-actions');
  const elements = [back, reject, approve];
  if (sameElements(previous, 'ai-plan-detail', elements)) return previous;
  const cleanup = mountPageHeaderActionElements('ai-plan-detail', elements);
  if (source) source.hidden = true;
  return { owner: 'ai-plan-detail', elements, cleanup };
}

function mountWhenReady(): void {
  let mounted: Mounted | undefined;
  let scheduled = false;
  let observer: MutationObserver | undefined;
  const mount = () => {
    scheduled = false;
    mounted = mountTagActions(mounted) || mountPlanDetailActions(mounted);
  };
  const queueMount = () => {
    if (typeof document === 'undefined' || !document.documentElement) {
      observer?.disconnect();
      return;
    }
    if (scheduled) return;
    scheduled = true;
    queueMicrotask(mount);
  };
  observer = new MutationObserver(queueMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  mount();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mountWhenReady, { once: true });
else mountWhenReady();
