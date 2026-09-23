export {};

// Release-only loader for the byte-frozen dd8 selection components.  It does
// adapt selection data: each page Host remains responsible for scoped reads.
// It supplies the V3 envelope for the shared directory refresh command and
// guarantees dependency order and one evaluation per page.
import { installTagPickerAdapter } from './shared/ui/tagPickerAdapter';
import { installCommittedTextSearch, resetCommittedTextSearch } from './shared/ui/committedTextSearch';
import { installStaffPickerAdapter } from './shared/ui/staffPickerAdapter';
declare global {
  interface Window {
    AICRMStandardComponents?: {
      ready(): Promise<void>;
      readyFor(capabilities: readonly OnDemandStandardComponentCapability[]): Promise<void>;
    };
    AICRMWeComTagPicker?: unknown;
  }
}

type StandardComponentCapability = 'operationMembers' | 'groupChats' | 'materials' | 'sendContent' | 'tags';
type OnDemandStandardComponentCapability = 'tags';

type OperationMemberPickerOptions = Record<string, unknown>;
type OperationMemberPicker = { open(options: OperationMemberPickerOptions): unknown };
type OperationMemberPickerWindow = { OperationMemberPicker?: OperationMemberPicker };

function operationMemberPicker(): OperationMemberPicker | undefined {
  return (window as unknown as OperationMemberPickerWindow).OperationMemberPicker;
}

const operationMemberSearchLifecycleInstalled = new WeakSet<object>();

function installOperationMemberSearchLifecycle(): void {
  const picker = operationMemberPicker();
  if (!picker || operationMemberSearchLifecycleInstalled.has(picker)) return;
  operationMemberSearchLifecycleInstalled.add(picker);
  const open = picker.open.bind(picker);
  picker.open = (options: OperationMemberPickerOptions) => {
    const result = open(options);
    const input = document.querySelector<HTMLInputElement>('[data-operation-member-picker] [data-operation-member-search]');
    if (input) resetCommittedTextSearch(input);
    return result;
  };
}

// The frozen script assigns this global after the stable Host. Observe the
// assignment once so every actual picker starts from its own empty committed
// query. The V3 Host never changes its caller-owned read or refresh envelope.
function observeOperationMemberPicker(): void {
  const descriptor = Object.getOwnPropertyDescriptor(window, 'OperationMemberPicker');
  if (descriptor && !descriptor.configurable) {
    installOperationMemberSearchLifecycle();
    return;
  }
  let current = operationMemberPicker();
  Object.defineProperty(window, 'OperationMemberPicker', {
    configurable: true,
    get: () => current,
    set: (value) => {
      current = value;
      installOperationMemberSearchLifecycle();
    },
  });
  installOperationMemberSearchLifecycle();
}

const components: ReadonlyArray<{ capability: StandardComponentCapability; source: string; ready: () => boolean }> = [
  { capability: 'operationMembers', source: '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).OperationMemberPicker?.open === 'function' },
  { capability: 'groupChats', source: '/assets/standard-components/group_chat_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMGroupChatPicker?.open === 'function' },
  { capability: 'materials', source: '/assets/standard-components/material_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMMaterialPicker?.open === 'function' },
  { capability: 'sendContent', source: '/assets/standard-components/send_content_composer.js', ready: () => {
    const composer = (window as unknown as Record<string, { open?: unknown; mount?: unknown }>).AICRMSendContentComposer;
    return typeof composer?.open === 'function' && typeof composer.mount === 'function';
  } },
  { capability: 'tags', source: '/assets/standard-components/wecom_tag_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMWeComTagPicker?.open === 'function' },
];

const componentByCapability = new Map(components.map((component) => [component.capability, component]));
const componentLoads = new Map<StandardComponentCapability, Promise<void>>();
const readyComponents = new Set<StandardComponentCapability>();
let tagPickerLocked = false;

function lockOriginalTagPicker(): void {
  if (tagPickerLocked || !window.AICRMWeComTagPicker) return;
  tagPickerLocked = true;
  const original = window.AICRMWeComTagPicker;
  Object.defineProperty(window, 'AICRMWeComTagPicker', {
    configurable: true,
    get: () => original,
    // Frozen page bundles may still assign their historical reimplementation.
    // Preserve the loaded standard global without changing either donor file.
    set: () => undefined,
  });
}

function matchingScript(source: string): HTMLScriptElement | undefined {
  return [...document.querySelectorAll<HTMLScriptElement>('script[data-aicrm-standard-component]')]
    .find((script) => script.dataset.aicrmStandardComponent === source && script.src === new URL(source, document.baseURI).href);
}

function load(component: { capability: StandardComponentCapability; source: string; ready: () => boolean }): Promise<void> {
  if (readyComponents.has(component.capability)) return Promise.resolve();
  const pending = componentLoads.get(component.capability);
  if (pending) return pending;
  let startRequest: (() => void) | undefined;
  const request = new Promise<void>((resolve, reject) => {
    let script = matchingScript(component.source);
    let appendScript = false;
    let settled = false;
    if (!script) {
      script = document.createElement('script');
      script.defer = true;
      script.src = component.source;
      script.dataset.aicrmStandardComponent = component.source;
      script.dataset.aicrmStandardComponentProvenance = 'v3-standard-components-host';
      script.dataset.aicrmStandardComponentState = 'pending';
      appendScript = true;
    }
    const fail = () => {
      if (settled) return;
      settled = true;
      if (appendScript) script?.remove();
      reject(new Error('标准选择组件加载失败，请刷新页面后重试'));
    };
    const succeed = () => {
      if (settled) return;
      if (!component.ready()) { fail(); return; }
      settled = true;
      readyComponents.add(component.capability);
      if (component.capability === 'tags') lockOriginalTagPicker();
      if (component.capability === 'operationMembers') installOperationMemberSearchLifecycle();
      resolve();
    };
    script.addEventListener('load', () => { script!.dataset.aicrmStandardComponentState = 'loaded'; succeed(); }, { once: true });
    script.addEventListener('error', fail, { once: true });
    if (appendScript) {
      // Register the single-flight before appending. A cached load failure can
      // arrive synchronously, including in a real browser.
      startRequest = () => { if (!settled) document.head.append(script!); };
    }
    else if (script.dataset.aicrmStandardComponentState === 'loaded') queueMicrotask(succeed);
  });
  componentLoads.set(component.capability, request);
  void request.then(undefined, () => {
    // Do not let an older rejected request erase a later retry's single-flight.
    if (componentLoads.get(component.capability) === request) componentLoads.delete(component.capability);
  });
  startRequest?.();
  return request;
}

function readyFor(capabilities: readonly OnDemandStandardComponentCapability[]): Promise<void> {
  const selected = [...new Set(capabilities)].map((capability) => {
    const component = componentByCapability.get(capability);
    if (!component) throw new Error(`未知标准选择组件：${capability}`);
    return component;
  });
  return Promise.all(selected.map(load)).then(() => undefined);
}

installCommittedTextSearch();
observeOperationMemberPicker();

window.AICRMStandardComponents = {
  ready(): Promise<void> {
    return components.reduce(async (previous, component) => {
      await previous;
      await load(component);
    }, Promise.resolve());
  },
  readyFor,
};
// This is a separate V3 API. Keep the byte-frozen tag global available for
// pages that still need it while V3-owned callers adopt the shared session.
installTagPickerAdapter();
// A separate V3 API. It never changes the frozen OperationMemberPicker's
// request scope or refresh command: every migrated Host supplies its own
// authorised read and optional refresh contract.
installStaffPickerAdapter();
const autoStart = document.querySelector('[data-customer-directory-root]')
  ? window.AICRMStandardComponents.readyFor(['tags'])
  : window.AICRMStandardComponents.ready();
void autoStart.catch(() => undefined);
