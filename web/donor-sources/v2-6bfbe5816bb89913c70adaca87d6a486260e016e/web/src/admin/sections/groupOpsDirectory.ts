import { readGroupOpsDirectory, readGroupOpsOwners, refreshGroupOpsDirectory } from '../../api/groupOpsDirectory';
import type { GroupOpsDirectoryPage } from "../../api/generated/health.schemas";
import { confirmBox } from '../../shared/ui/feedback';
import { esc } from './util';

/** Selection only edits the form; the existing Save path owns CAS binding/removal. */
export function openGroupOpsDirectory(options: { selected?: string[] } = {}): Promise<string[] | null> {
  return new Promise((resolve) => {
    const selected = new Set(options.selected || []);
    const labels = new Map<string, string>();
    const mask = document.createElement('div');
    mask.id = 'group-directory';
    mask.style.cssText = 'position:fixed;inset:0;background:#0005;z-index:9800;display:flex;align-items:center;justify-content:center;padding:24px';
    const editable = options.selected !== undefined;
    let owners: Array<{ staffId: number; name: string }> = [];
    let owner = 0, offset = 0, generation = 0;
    let loading = true, closed = false, error = '', notice = '', refreshKey = '';
    let current: GroupOpsDirectoryPage | null = null;
    const finish = (value: string[] | null): void => { closed = true; generation++; mask.remove(); resolve(value); };
    const button = (action: string, label: string, disabled = false): string => `<button data-gd="${action}" ${disabled ? 'disabled' : ''}>${label}</button>`;
    function render(): void {
      if (closed) return;
      mask.innerHTML = `<section role="dialog" aria-modal="true" aria-label="可管理群目录" style="background:#fff;border-radius:8px;padding:20px;width:min(860px,100%);max-height:85vh;overflow:auto;display:grid;gap:12px">
        <header style="display:flex;justify-content:space-between"><strong>可管理群目录</strong>${button('close', '关闭')}</header>
        <p style="margin:0;color:#8F5A16;font-size:12px">当前列表是本地目录快照，不证明当前企微权限或群消息送达。刷新可能读取企微名下群并更新本地目录；不触发群发。</p>
        <label>运营成员 <select data-gd="owner" ${loading ? 'disabled' : ''}><option value="">请选择负责人</option>${owners.map((item) => `<option value="${item.staffId}" ${item.staffId === owner ? 'selected' : ''}>${esc(item.name)} · staff_id=${item.staffId}</option>`).join('')}</select></label>
        <div>${button('read', '重读本地目录', loading || !owner)} ${button('refresh', '刷新此成员名下群（企微读取）', loading || !owner)}</div>
        <div role="status">${esc(loading ? '正在读取…' : error || notice || (!owners.length ? '暂无可信运营成员，无法读取或刷新目录' : !owner ? '请选择负责人后读取本地目录' : ''))}</div>
        ${current ? `<table style="width:100%;text-align:left"><thead><tr><th>选择</th><th>群名称 / 引用</th><th>人数</th><th>本地刷新时间</th></tr></thead><tbody>${current.items.map((item) => `<tr><td>${editable ? `<input type="checkbox" aria-label="选择 ${esc(item.display_name)}" data-gd-ref="${esc(item.chat_reference)}" ${selected.has(item.chat_reference) ? 'checked' : ''}>` : '只读'}</td><td>${esc(item.display_name)}<small style="display:block">${esc(item.chat_reference)}</small></td><td>${item.member_count}</td><td>${esc(item.refreshed_at)}</td></tr>`).join('')}</tbody></table>${!current.items.length ? '<p>该成员本地目录为空；不等于企微没有群</p>' : ''}<div>共 ${current.total} 条 · offset=${offset} ${button('prev', '上一页', loading || offset === 0)} ${button('next', '下一页', loading || !current.has_more)}</div>` : ''}
        ${editable ? `<div style="border-top:1px solid #DEE0E3;padding-top:12px"><strong>待保存群选择（${selected.size}）</strong><p style="font-size:12px">切换负责人和翻页保留选择。未在已加载目录确认的原引用保留，只有显式移除才取消绑定。</p>${[...selected].map((ref) => `<div style="margin:5px 0">${esc(labels.get(ref) || '未在已加载目录确认')} · ${esc(ref)} <button data-gd-remove="${esc(ref)}">移除</button></div>`).join('')}${button('apply', '使用此选择（仍需保存计划）')}</div>` : ''}
      </section>`;
      mask.querySelectorAll<HTMLElement>('button,input,select').forEach((element) => { (element as HTMLElement & { __dcBound?: boolean }).__dcBound = true; });
      mask.querySelector<HTMLSelectElement>('[data-gd="owner"]')!.onchange = (event) => {
        owner = Number((event.target as HTMLSelectElement).value); offset = 0; refreshKey = ''; current = null; notice = ''; error = '';
        if (owner) void read(); else render();
      };
      mask.querySelectorAll<HTMLButtonElement>('[data-gd]').forEach((element) => {
        element.onclick = () => {
          switch (element.dataset.gd) {
            case 'close': finish(null); break;
            case 'apply': finish([...selected]); break;
            case 'read': void read(); break;
            case 'prev': offset -= 50; void read(); break;
            case 'next': offset += 50; void read(); break;
            case 'refresh': {
              const chosenOwner = owner;
              confirmBox('刷新企微群目录', '此操作可能实际读取所选成员名下的企微群并更新本地快照；不发送群消息。仅在已获本次目录读取授权时继续。当前响应不提供 Provider 读取回执。', '确认读取目录', false, () => { if (!closed && owner === chosenOwner && !loading) void read(true); });
              break;
            }
          }
        };
      });
      mask.querySelectorAll<HTMLInputElement>('[data-gd-ref]').forEach((element) => { element.onchange = () => { const ref = element.dataset.gdRef!; if (element.checked) selected.add(ref); else selected.delete(ref); render(); }; });
      mask.querySelectorAll<HTMLButtonElement>('[data-gd-remove]').forEach((element) => { element.onclick = () => { selected.delete(element.dataset.gdRemove!); render(); }; });
    }
    async function read(refresh = false): Promise<void> {
      const request = ++generation;
      loading = true; error = ''; notice = ''; current = null; render();
      try {
        if (refresh && !refreshKey) refreshKey = crypto.randomUUID();
        const result = refresh ? await refreshGroupOpsDirectory(owner, refreshKey) : await readGroupOpsDirectory(owner, 50, offset);
        if (closed || request !== generation) return;
        current = result; offset = result.offset;
        result.items.forEach((item) => labels.set(item.chat_reference, item.display_name));
        if (refresh) { refreshKey = ''; notice = '服务器返回目录快照；不代表 Provider 接受或消息送达，当前响应无 Provider 读取回执。'; }
      } catch { if (!closed && request === generation) error = '目录读取失败，未使用 Mock 或旧页数据。可重读本地目录；刷新重试须再次确认。'; }
      finally { if (!closed && request === generation) { loading = false; render(); } }
    }
    document.body.appendChild(mask); render();
    void readGroupOpsOwners().then((value) => { owners = value; }).catch(() => { error = '运营成员读取失败，未使用 Mock；请关闭后重试'; }).finally(() => { loading = false; render(); });
  });
}
