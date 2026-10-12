type Attribute = { key: string; value: string };
export type Variant = { id: number; version: number; name: string; description: string; attributes: Attribute[]; price_minor: number };
export type VariantConfiguration = { variants_enabled: boolean; variants: Variant[] };
let host: HTMLElement | undefined;
let enabled: HTMLInputElement;
let rows: HTMLElement;
let locked = false;
const fieldStyle = 'width:100%;min-height:36px;padding:8px 10px;border:1px solid #DEE0E3;border-radius:6px;font:inherit;box-sizing:border-box';
function button(text: string, action: () => void): HTMLButtonElement {
  const b = document.createElement('button'); b.type = 'button'; b.textContent = text;
  b.style.cssText = 'border:1px solid #DEE0E3;border-radius:6px;padding:7px 12px;background:white;color:#3370ff;cursor:pointer;font:inherit';
  b.addEventListener('click', action); return b;
}
function input(parent: HTMLElement, title: string, value: string, key: string, multiline = false): HTMLInputElement | HTMLTextAreaElement {
  const label = document.createElement('label'); label.style.cssText = 'display:grid;gap:6px;min-width:0'; label.textContent = title;
  const node = document.createElement(multiline ? 'textarea' : 'input'); node.style.cssText = fieldStyle; node.value = value; node.dataset.variantField = key;
  node.setAttribute('aria-label', title); label.append(node); parent.append(label); return node;
}
function addRow(v: Variant): void {
  const row = document.createElement('div'); row.dataset.variantRow = ''; row.dataset.id = String(v.id); row.dataset.version = String(v.version);
  row.style.cssText = 'border:1px solid #DEE0E3;border-radius:8px;padding:16px;display:grid;gap:12px;background:#fff';
  const heading = document.createElement('div'); heading.style.cssText = 'display:flex;justify-content:space-between;align-items:center';
  const title = document.createElement('strong'); title.textContent = '规格'; heading.append(title, button('移除', () => row.remove())); row.append(heading);
  const primary = document.createElement('div'); primary.style.cssText = 'display:grid;grid-template-columns:minmax(0,1fr) minmax(100px,180px);gap:16px';row.append(primary);
  input(primary, '规格名称', v.name, 'name'); const price = input(primary, '售价（元）', (v.price_minor / 100).toFixed(2), 'price') as HTMLInputElement; price.inputMode = 'decimal';
  input(row, '描述（选填）', v.description, 'description', true);
  const attrs = document.createElement('div'); attrs.dataset.variantAttributes = ''; attrs.style.cssText = 'display:grid;gap:8px';row.append(attrs);
  const addAttribute = (a: Attribute) => {
    const line = document.createElement('div');line.dataset.variantAttribute = '';line.style.cssText = 'display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr) auto;gap:8px;align-items:end';
    input(line, '属性名', a.key, 'key');input(line, '属性值', a.value, 'value');line.append(button('移除属性', () => line.remove()));attrs.append(line);
  };
  v.attributes.forEach(addAttribute); const add = button('添加属性', () => addAttribute({ key: '', value: '' }));add.style.justifySelf = 'start';row.append(add);rows.append(row);
}
function mode(): void {
  if (!host) return;
  const details = host.querySelector<HTMLElement>('[data-variant-details]');if (details) details.hidden = !enabled.checked;
  const price = document.getElementById('pfPrice');const label = price?.closest('label') || price?.parentElement;if (label instanceof HTMLElement) label.hidden = enabled.checked;
}
export function mountProductVariants(snapshot: Record<string, unknown> | undefined, ready: boolean): void {
  const sale = document.getElementById('product-sale');if (!sale || !ready || snapshot?.source_kind === 'wechat_shop') return;
  if (snapshot) {
    const prices = (Array.isArray(snapshot.variants) ? snapshot.variants as Variant[] : []).map(v => v.price_minor);
    const amount = (value: number) => (value / 100).toFixed(2);
    const text = snapshot.variants_enabled === true ? prices.length ? `${amount(Math.min(...prices))}～${amount(Math.max(...prices))}` : '暂无可选规格' : amount(Number(snapshot.price_minor));
    const label = Array.from(document.querySelectorAll('span')).find(node => node.textContent === '价格');
    const value = label?.parentElement?.querySelector('strong');
    if (value && value.textContent !== text) value.textContent = text;
  }
  if (host?.isConnected) return;
  host = document.createElement('section');host.dataset.productVariants = '';host.style.cssText = 'margin:20px 0;display:grid;gap:14px';
  const label = document.createElement('label');label.className = 'product-distribution-policy__toggle';enabled = document.createElement('input');enabled.type = 'checkbox';enabled.dataset.variantsEnabled = '';label.append(enabled, document.createTextNode('启用规格'));host.append(label);
  const details = document.createElement('div');details.dataset.variantDetails = '';const hint = document.createElement('p');hint.textContent = '为不同版本分别设置名称和售价。描述和属性可选。';hint.style.cssText = 'margin:0 0 12px;color:#646A73;font-size:13px';details.append(hint);
  rows = document.createElement('div');rows.style.cssText = 'display:grid;gap:12px';details.append(rows);
  const add = button('＋ 新增规格', () => addRow({ id: 0, version: 0, name: '', description: '', attributes: [], price_minor: 0 }));add.style.marginTop = '12px';details.append(add);host.append(details);
  const fields = Array.from(sale.children).find(node => node instanceof HTMLElement && node.style.display === 'grid' && node.style.gridTemplateColumns);
  sale.insertBefore(host, fields?.nextSibling || null);
  enabled.addEventListener('change', mode);syncProductVariants(snapshot);lockProductVariants(locked);
}
export function readProductVariants(): VariantConfiguration {
  if (!host?.isConnected) throw new Error('规格配置尚未加载，请稍后重试。');
  const variants = [...rows.querySelectorAll<HTMLElement>('[data-variant-row]')].map(row => {
    const value = (key: string) => (row.querySelector<HTMLInputElement>(`[data-variant-field="${key}"]`)?.value || '').trim();
    const name = value('name'), amount = value('price');const minor = Math.round(Number(amount) * 100);
    if (!name || !/^\d+(\.\d{1,2})?$/.test(amount) || !Number.isSafeInteger(minor)) throw new Error('请为每个规格填写名称和有效售价（最多两位小数）。');
    const attributes = [...row.querySelectorAll<HTMLElement>('[data-variant-attribute]')].map(line => ({ key: (line.querySelector<HTMLInputElement>('[data-variant-field="key"]')?.value || '').trim(), value: (line.querySelector<HTMLInputElement>('[data-variant-field="value"]')?.value || '').trim() }));
    if (attributes.some(a => !a.key || !a.value) || new Set(attributes.map(a => a.key)).size !== attributes.length) throw new Error('属性名和属性值需填写完整，同一规格的属性名不能重复。');
    return { id: Number(row.dataset.id), version: Number(row.dataset.version), name, description: value('description'), attributes, price_minor: minor };
  });
  return { variants_enabled: enabled.checked, variants };
}
export function syncProductVariants(snapshot: Record<string, unknown> | undefined): void {
  if (!host?.isConnected) return;enabled.checked = snapshot?.variants_enabled === true;rows.replaceChildren();
  const variants = Array.isArray(snapshot?.variants) ? snapshot.variants as Variant[] : [];
  variants.forEach(v => addRow({ ...v, attributes: v.attributes || [] }));mode();
}
export function lockProductVariants(value: boolean): void {
  locked = value;host?.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLTextAreaElement>('input,button,textarea').forEach(node => { node.disabled = value; });
}
