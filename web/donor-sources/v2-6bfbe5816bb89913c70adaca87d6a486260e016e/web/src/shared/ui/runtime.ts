/**
 * AI-CRM 前端迷你模板运行时（TypeScript 版）
 * 语义与设计原型 support.js 完全一致：
 *  - {{ path.to.value }} 插值（文本节点 / 属性）
 *  - style 属性绑定对象（camelCase → kebab-case）
 *  - onClick="{{ handler }}" 事件绑定（绑定后打 __dcBound 标记，反馈层据此跳过）
 *  - <template data-sc-for="{{ list }}" data-as="item"> 列表渲染
 *  - <template data-sc-if="{{ cond }}"> 条件渲染
 *  - state / setState 触发整体重渲染
 *
 * 构建期（scripts/build.mjs）负责把模板里的 <sc-for>/<sc-if> 转成
 * <template data-sc-*>，运行时只认 <template> 形态。
 */

export type StyleObj = Record<string, string | number | null | undefined>;
export type Vals = Record<string, unknown>;
export type Scope = Record<string, unknown> & object;

/** 带渲染回调的页面控制器基类 */
export abstract class PageBase {
  props: Record<string, unknown> = {};
  state: Record<string, unknown> = {};
  /** 由 mount() 注入的重渲染回调 */
  __render?: () => void;

  setState(patch: Record<string, unknown>): void {
    Object.assign(this.state, patch);
    if (this.__render) this.__render();
  }

  /** 子类实现：返回本帧模板可用的全部绑定值 */
  abstract renderVals(): Vals;
}

export function camelToKebab(k: string): string {
  return k.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());
}

export function styleObjToCss(obj: StyleObj): string {
  return Object.entries(obj)
    .filter(([, v]) => v !== null && v !== undefined)
    .map(([k, v]) => camelToKebab(k) + ':' + String(v))
    .join(';');
}

/** 解析 "a.b.c" 路径表达式；支持 true/false/null/数字字面量 */
export function resolveExpr(expr: string, scope: Scope): unknown {
  expr = expr.trim();
  if (expr === 'true') return true;
  if (expr === 'false') return false;
  if (expr === 'null') return null;
  if (/^-?\d+(\.\d+)?$/.test(expr)) return Number(expr);
  const parts = expr.split('.');
  let val: unknown = scope;
  for (const p of parts) {
    if (val === null || val === undefined) return undefined;
    val = (val as Record<string, unknown>)[p];
  }
  return val;
}

const TOKEN_RE = /\{\{([^}]*)\}\}/g;

export function interpolate(str: string, scope: Scope): string {
  return str.replace(TOKEN_RE, (_m, expr: string) => {
    const v = resolveExpr(expr, scope);
    if (v === null || v === undefined) return '';
    if (typeof v === 'object') return styleObjToCss(v as StyleObj);
    return String(v);
  });
}

/** 元素上由本运行时绑定过事件的标记（feedback 委托层跳过这些节点） */
export const BOUND_MARK = '__dcBound';

interface BoundElement extends HTMLElement {
  __dcBound?: boolean;
}

function walk(node: Node, scope: Scope): void {
  // <template data-sc-for / data-sc-if>
  if (node.nodeType === 1 && (node as HTMLElement).tagName === 'TEMPLATE') {
    const tpl = node as HTMLTemplateElement;
    const forList = tpl.getAttribute('data-sc-for');
    const ifVal = tpl.getAttribute('data-sc-if');
    if (forList !== null) {
      const as = tpl.getAttribute('data-as') || 'item';
      const list = (resolveExpr(forList.replace(/^\{\{|\}\}$/g, '').trim(), scope) as unknown[]) || [];
      const frag = document.createDocumentFragment();
      for (const item of list) {
        const childScope: Scope = Object.create(scope);
        childScope[as] = item;
        const clone = tpl.content.cloneNode(true) as DocumentFragment;
        walkChildren(clone, childScope);
        frag.appendChild(clone);
      }
      tpl.replaceWith(frag);
      return;
    }
    if (ifVal !== null) {
      const v = resolveExpr(ifVal.replace(/^\{\{|\}\}$/g, '').trim(), scope);
      if (!v) {
        tpl.remove();
        return;
      }
      const clone = tpl.content.cloneNode(true) as DocumentFragment;
      walkChildren(clone, scope);
      tpl.replaceWith(clone);
      return;
    }
  }

  if (node.nodeType === 3) {
    const text = node as Text;
    if (text.nodeValue && text.nodeValue.includes('{{')) {
      text.nodeValue = interpolate(text.nodeValue, scope);
    }
    return;
  }

  if (node.nodeType === 1) {
    const el = node as BoundElement;
    for (const attr of Array.from(el.attributes)) {
      if (!attr.value.includes('{{')) {
        if (attr.name.startsWith('hint-')) el.removeAttribute(attr.name);
        continue;
      }
      const whole = attr.value.match(/^\{\{([^}]*)\}\}$/);
      if (/^on[a-z]+$/i.test(attr.name)) {
        const fn = whole ? resolveExpr(whole[1], scope) : null;
        el.removeAttribute(attr.name);
        if (typeof fn === 'function') {
          el.__dcBound = true;
          el.addEventListener(attr.name.slice(2).toLowerCase(), (ev) => {
            (fn as (e: Event) => void)(ev);
          });
        }
      } else if (whole) {
        const v = resolveExpr(whole[1], scope);
        if (v !== null && typeof v === 'object') {
          el.setAttribute(attr.name, styleObjToCss(v as StyleObj));
        } else {
          el.setAttribute(attr.name, v === null || v === undefined ? '' : String(v));
        }
      } else {
        el.setAttribute(attr.name, interpolate(attr.value, scope));
      }
    }
    walkChildren(el, scope);
  }
}

export function walkChildren(node: Node, scope: Scope): void {
  for (const child of Array.from(node.childNodes)) {
    walk(child, scope);
  }
}

/**
 * 把屏幕模板挂载到 host 元素：
 * 每次 controller.setState() 触发整体重渲染（clone template → walk → replaceChildren）。
 * 返回渲染函数（一般不需要手动调用）。
 */
export function mount(host: HTMLElement, templateHtml: string, controller: PageBase): () => void {
  const tpl = document.createElement('template');
  tpl.innerHTML = templateHtml;

  const container = document.createElement('div');
  container.style.display = 'contents';
  host.replaceChildren(container);

  const render = (): void => {
    const vals = controller.renderVals();
    const scope: Scope = Object.create(null);
    Object.assign(scope, vals);
    const frag = tpl.content.cloneNode(true) as DocumentFragment;
    walkChildren(frag, scope);
    container.replaceChildren(frag);
  };
  controller.__render = render;
  render();
  return render;
}
