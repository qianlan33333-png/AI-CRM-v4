import {
  listAdminAccessMembers,
  saveAdminAccessMembers,
} from "../../api/generated/p4-setup-wizard/p4-setup-wizard";
import {
  type AdminAccessReadResponse,
  type AdminAccessSaveResponse,
} from "../../api/generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from '../../api/transport';
import { esc } from './util';

function assertLocalBoundary(value: AdminAccessReadResponse | AdminAccessSaveResponse): void {
  if (!value.ok || value.local_only !== true || value.external !== false) {
    throw new Error('后台访问接口未声明为仅本地，已停止展示结果');
  }
}

function render(root: HTMLElement, data: AdminAccessReadResponse, message = ''): void {
  const rows = data.members.map((member) => {
    const name = member.staff_name || member.display_name;
    const identity = member.staff_wecom_userid ? ` · ${esc(member.staff_wecom_userid)}` : '';
    const inactive = !member.is_active;
    return `<label style="display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 0;border-bottom:1px solid #F2F3F5;${inactive ? 'opacity:.55' : ''}">
      <span style="min-width:0"><strong style="font-size:13px">${esc(name)}</strong><span style="font-size:12px;color:#8F959E"> · ${esc(member.role)}${identity}${inactive ? ' · 账号已停用' : ''}</span></span>
      <input type="checkbox" data-admin-access-id="${member.admin_user_id}" ${member.login_enabled ? 'checked' : ''} ${inactive ? 'disabled' : ''} aria-label="${esc(name)} 后台登录权限">
    </label>`;
  }).join('') || '<div style="padding:12px 0;color:#8F959E;font-size:12px">暂无可配置的既有管理员账号</div>';
  root.innerHTML = `<section style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px">
    <div style="display:flex;justify-content:space-between;gap:12px;align-items:start">
      <div><h2 style="margin:0 0 4px;font-size:14px">后台访问成员</h2><p style="margin:0;color:#8F959E;font-size:12px">仅影响已预置账号的本地后台登录开关；不创建账号、角色或企微身份，不同步企微。</p></div>
      <span style="font-size:12px;color:#646A73">本地登录</span>
    </div>
    <form id="admin-access-form" style="margin-top:10px">
      <div>${rows}</div>
      <div id="admin-access-result" role="status" style="min-height:18px;margin-top:10px;color:${message === 'save_success' ? '#2E7D32' : '#D83931'};font-size:12px">${esc(message)}</div>
      <button type="submit" style="height:32px;padding:0 14px;border:0;border-radius:6px;background:#3370FF;color:#fff;cursor:pointer">保存登录权限</button>
    </form>
  </section>`;
}

function bindForm(root: HTMLElement, initial: AdminAccessReadResponse): void {
  root.querySelector<HTMLFormElement>('#admin-access-form')?.addEventListener('submit', (event) => {
    event.preventDefault();
    const result = root.querySelector<HTMLElement>('#admin-access-result');
    const members = initial.members.filter((member) => member.is_active).map((member) => ({
      admin_user_id: member.admin_user_id,
      login_enabled: root.querySelector<HTMLInputElement>(`[data-admin-access-id="${member.admin_user_id}"]`)?.checked === true,
    }));
    if (members.length === 0) {
      if (result) { result.textContent = 'validation_error'; result.style.color = '#D83931'; }
      return;
    }
    void (async () => {
      const saved = unwrapGenerated(await saveAdminAccessMembers({ members }, apiRequestOptions({
        headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` },
      }))) as AdminAccessSaveResponse;
      assertLocalBoundary(saved);
      if (!saved.idempotency_key) throw new Error('保存回执不完整');
      const current = unwrapGenerated(await listAdminAccessMembers(apiRequestOptions())) as AdminAccessReadResponse;
      assertLocalBoundary(current);
      render(root, current, 'save_success');
      bindForm(root, current);
    })().catch((error) => {
      if (result) { result.textContent = error instanceof Error ? error.message : '保存失败'; result.style.color = '#D83931'; }
    });
  });
}

export async function mountAdminAccess(root: HTMLElement): Promise<void> {
  const current = unwrapGenerated(await listAdminAccessMembers(apiRequestOptions())) as AdminAccessReadResponse;
  assertLocalBoundary(current);
  render(root, current);
  bindForm(root, current);
}
