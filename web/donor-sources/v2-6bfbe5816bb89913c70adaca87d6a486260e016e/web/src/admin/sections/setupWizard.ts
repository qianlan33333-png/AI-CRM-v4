import {
  getSetupWizard,
  saveSetupWizard,
} from "../../api/generated/p4-setup-wizard/p4-setup-wizard";
import {
  type SetupWizardReadResponse,
  type SetupWizardSaveResponse,
} from "../../api/generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from '../../api/transport';

function assertLocalBoundary(value: SetupWizardReadResponse | SetupWizardSaveResponse): void {
  if (!value.ok || value.local_only !== true || value.external !== false || value.runtime_applied !== false) {
    throw new Error('配置接口未声明为仅本地保存，已停止展示结果');
  }
}

function escapeHtml(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[char] || char);
}

function render(root: HTMLElement, data: SetupWizardReadResponse, message = ''): void {
  const masked = data.masked;
  const status = (configured: boolean): string => configured ? '已配置（值不展示）' : '未配置';
  root.innerHTML = `
    <section style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px">
      <div style="display:flex;justify-content:space-between;gap:12px;align-items:start">
        <div><h2 style="margin:0 0 4px;font-size:14px">企微接入基础配置</h2><p style="margin:0;color:#8F959E;font-size:12px">仅保存本地 Corp ID 与 Agent ID；不写入密钥，不代表配置已应用到运行时或企微。</p></div>
        <span style="font-size:12px;color:#646A73">本地保存</span>
      </div>
      <form id="setup-wizard-form" style="display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:14px">
        <label style="display:grid;gap:5px;font-size:12px">Corp ID<input id="setup-corp-id" value="${escapeHtml(data.editable['wecom.corp_id'])}" style="height:34px;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px"></label>
        <label style="display:grid;gap:5px;font-size:12px">Agent ID<input id="setup-agent-id" type="number" min="1" value="${data.editable['wecom.agent_id'] || ''}" style="height:34px;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px"></label>
        <div style="grid-column:1/-1;font-size:12px;color:#646A73">Secret：${status(masked['wecom.secret'].configured)}　Callback Token：${status(masked['wecom.callback_token'].configured)}　Callback AES Key：${status(masked['wecom.callback_aes_key'].configured)}　AI Key：此接口无权配置</div>
        <div id="setup-wizard-result" role="status" style="grid-column:1/-1;min-height:18px;color:${message === 'save_success' ? '#2E7D32' : '#D83931'};font-size:12px">${escapeHtml(message)}</div>
        <div style="grid-column:1/-1"><button type="submit" style="height:32px;padding:0 14px;border:0;border-radius:6px;background:#3370FF;color:#fff;cursor:pointer">保存本地配置</button></div>
      </form>
    </section>`;
}

function bindForm(root: HTMLElement, initial: SetupWizardReadResponse): void {
  let current = initial;
  root.querySelector<HTMLFormElement>('#setup-wizard-form')?.addEventListener('submit', (event) => {
    event.preventDefault();
    const corpId = root.querySelector<HTMLInputElement>('#setup-corp-id')?.value.trim() || '';
    const agentId = Number(root.querySelector<HTMLInputElement>('#setup-agent-id')?.value || 0);
    const result = root.querySelector<HTMLElement>('#setup-wizard-result');
    if (!corpId || !Number.isInteger(agentId) || agentId < 1) {
      if (result) { result.textContent = 'validation_error'; result.style.color = '#D83931'; }
      return;
    }

    void (async () => {
      const saved = unwrapGenerated(await saveSetupWizard({
        'wecom.corp_id': corpId,
        'wecom.agent_id': agentId,
        'wecom.secret': '',
        'wecom.callback_token': '',
        'wecom.callback_aes_key': '',
        'ai.api_key': '',
        expected_digest: current.expected_digest,
        admin_action_token: current.admin_action_token,
      }, apiRequestOptions({ headers: { 'Idempotency-Key': globalThis.crypto?.randomUUID?.() || `web-${Date.now()}` } }))) as SetupWizardSaveResponse;
      assertLocalBoundary(saved);
      if (saved.receipt.audits.length !== 2 || saved.receipt.events.length !== 2) throw new Error('保存回执不完整');
      current = unwrapGenerated(await getSetupWizard(apiRequestOptions())) as SetupWizardReadResponse;
      assertLocalBoundary(current);
      render(root, current, 'save_success');
      bindForm(root, current);
    })().catch((error) => {
      if (result) { result.textContent = error instanceof Error ? error.message : '保存失败'; result.style.color = '#D83931'; }
    });
  });
}

export async function mountSetupWizard(root: HTMLElement): Promise<void> {
  const current = unwrapGenerated(await getSetupWizard(apiRequestOptions())) as SetupWizardReadResponse;
  assertLocalBoundary(current);
  render(root, current);
  bindForm(root, current);
}
