import { GroupManagement, bindMaterialGroup } from './materialGroupManagement';
import { request } from '../src/api/transport';
import { MaterialGroupSidebar, materialGroupLayout } from './materialGroupSidebar';

type Page = 'attach' | 'mpLib';
const paths: Record<Page, string> = {
  attach: '/api/admin/attachment-library',
  mpLib: '/api/admin/miniprogram-library',
};

// Extend the existing Media transport seam only for the current library list.
// Picker reads, mutations and other pages keep their original request.
export function materialGroupRequest(
  input: RequestInfo | URL,
  method: string,
): RequestInfo | URL {
  const page = document.body?.dataset.page as Page;
  if (!paths[page] || method !== 'GET') return input;
  const current = new URL(location.href);
  if (!current.searchParams.has('material_group')) return input;
  const url = new URL(
    input instanceof Request ? input.url : String(input),
    location.origin,
  );
  if (url.origin !== location.origin || url.pathname !== paths[page])
    return input;
  url.searchParams.set(
    'category',
    current.searchParams.get('material_group') || '',
  );
  return input instanceof Request ? new Request(url, input) : url;
}
export function mountMaterialGroups(stage: HTMLElement, page: Page): void {
  if (stage.querySelector('[data-material-groups]')) return;
  const sidebar = new MaterialGroupSidebar((value) => {
    const url = new URL(location.href);
    url.pathname = '/admin/materials';
    url.searchParams.set('tab', page === 'attach' ? 'attachments' : 'miniprograms');
    if (value === 'all') url.searchParams.delete('material_group');
    else url.searchParams.set('material_group', value.slice(6));
    url.searchParams.delete('offset');
    location.assign(url.href);
  });
  const current = new URL(location.href);
  const selected = current.searchParams.has('material_group') ? 'group:' + current.searchParams.get('material_group') : 'all';
  const base = [{ value: 'all', label: '全部分组' }, { value: 'group:', label: '未分组' }];
  sidebar.render(base, selected);
  const layout = materialGroupLayout();
  const content = document.createElement('div'); content.className = 'material-group-content';
  for (const child of Array.from(stage.childNodes)) {
    if (!(child instanceof HTMLElement && child.hasAttribute('data-material-library-tabs'))) content.append(child);
  }
  layout.append(sidebar.element, content); stage.append(layout);
 const manager=new GroupManagement(page==='attach'?'attachment':'miniprogram',content);
  const refresh = document.createElement('button'); refresh.type = 'button';
  refresh.textContent = '刷新'; refresh.className = 'admin-button';
  refresh.addEventListener('click', () => location.reload());
  sidebar.element.append(refresh);
  sidebar.message('加载分组…');
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 10000);
  void manager.load()
    .then(async (items) => {
      const data = {items};
      if (!Array.isArray(data.items)) throw new Error('invalid groups');
      const entries = data.items as { name: string; count: number }[];
      const options = [
        { value: 'all', label: '全部分组', count: entries.reduce((sum, x) => sum + x.count, 0) },
        { value: 'group:', label: '未分组', count: entries.find(x => x.name === '')?.count || 0 },
        ...entries.filter(x => x.name).map(x => ({ value: 'group:' + x.name, label: x.name, count: x.count })),
      ];
      // Keep a bookmarked group visible even when it currently has no items.
      if (!options.some(x => x.value === selected)) options.push({ value: selected, label: selected.slice(6), count: 0 });
      sidebar.render(options, selected); sidebar.message('');
    })
    .catch(() => sidebar.message('分组加载失败，请刷新重试。'))
    .finally(() => clearTimeout(timer));
}
export function mountMaterialGroupControl(
  container: HTMLElement,
  page: Page,
  item: { id: number; version: number; category?: unknown },
): void {
  const node=container.closest<HTMLElement>('tr,[data-material-library-id]')||container;
  bindMaterialGroup(node,item.id,container);
}
