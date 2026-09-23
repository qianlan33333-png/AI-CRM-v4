import {
  archiveLegacyAutomationAgent,
  copyLegacyAutomationAgent,
  createLegacyAutomationAgent,
  getLegacyAutomationAgent,
  listLegacyAutomationAgents,
  precheckLegacyAutomationAgent,
  publishLegacyAutomationAgent,
  updateLegacyAutomationAgent,
} from '../../api/generated/p4-automation-agents/p4-automation-agents';
import {
  type LegacyAutomationAgentDetail,
  type LegacyAutomationAgentListResponse,
} from '../../api/generated/health.schemas';
import { apiRequestOptions, unwrapGenerated } from '../../api/transport';
import { esc } from './util';

function key(): string {
  return globalThis.crypto?.randomUUID?.() || `automation-agent-${Date.now()}-${Math.random()}`;
}

function writeOptions(): RequestInit {
  return apiRequestOptions({ headers: { 'Idempotency-Key': key() } });
}

function showResult(root: HTMLElement, message: string, failed = false): void {
  const result = root.querySelector<HTMLElement>('[data-agent-result]');
  if (!result) return;
  result.textContent = message;
  result.style.color = failed ? '#D83931' : '#2E7D32';
}

async function loadList(root: HTMLElement): Promise<void> {
  const data = unwrapGenerated(await listLegacyAutomationAgents(apiRequestOptions())) as LegacyAutomationAgentListResponse;
  if (!data.ok) throw new Error('自动化配置读取失败');
  const rows = data.items.map((item) => `<tr>
    <td style="padding:10px 16px;border-bottom:1px solid #F2F3F5"><div style="font-size:13px;font-weight:600">${esc(item.agent_name)}</div><div style="margin-top:2px;font:12px ui-monospace,Menlo,monospace;color:#A6AAB0">${esc(item.agent_code)}</div></td>
    <td style="padding:10px 12px;border-bottom:1px solid #F2F3F5"><span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:4px;background:#F2F3F5;color:#646A73;font-size:12px">${esc(item.automation_type === 'fixed_script' ? '固定话术' : 'Agent 机器人')}</span></td>
    <td style="padding:10px 12px;border-bottom:1px solid #F2F3F5"><span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:4px;background:#F2F3F5;color:#646A73;font-size:12px">${esc(`${item.fixed_material_summary.image_count} 图片 / ${item.fixed_material_summary.miniprogram_count} 小程序 / ${item.fixed_material_summary.attachment_count} PDF / ${item.fixed_material_summary.group_invite_count} 群邀请`)}</span></td>
    <td style="padding:10px 12px;border-bottom:1px solid #F2F3F5"><span style="display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:4px;background:#FFF7E8;color:#8A4B08;font-size:12px">${esc(item.status === 'paused' ? '已暂停（执行关闭）' : item.status)}</span></td>
    <td style="padding:10px 12px;border-bottom:1px solid #F2F3F5;font-size:12px;color:#646A73">${esc(item.updated_at)}</td>
    <td style="padding:10px 16px;border-bottom:1px solid #F2F3F5;text-align:right"><div style="display:inline-flex;gap:8px;flex-wrap:wrap;justify-content:flex-end">
      <a href="agentEdit.html?id=${item.id}" style="height:28px;padding:0 12px;display:inline-flex;align-items:center;border:1px solid #DEE0E3;border-radius:6px;background:#fff;font-size:13px;color:#1F2329;text-decoration:none">编辑</a>
      <button type="button" data-agent-action="precheck" data-agent-id="${item.id}" style="height:28px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;font-size:13px;color:#1F2329;cursor:pointer">启用前检查</button>
      <button type="button" data-agent-action="copy" data-agent-id="${item.id}" style="height:28px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;font-size:13px;color:#1F2329;cursor:pointer">复制</button>
      <button type="button" data-agent-action="archive" data-agent-id="${item.id}" style="height:28px;padding:0 12px;border:1px solid #FBC4C2;border-radius:6px;background:#FFF5F5;font-size:13px;color:#D83931;cursor:pointer">归档</button>
    </div>
    </td>
  </tr>`).join('') || '<tr><td colspan="6" style="padding:32px;text-align:center;color:#8F959E">暂无当前自动化配置</td></tr>';
  root.innerHTML = `<div style="display:flex;align-items:center;justify-content:space-between;gap:16px;height:52px;padding:0 20px;background:#fff;border-bottom:1px solid #DEE0E3"><div><div style="font-size:12px;color:#8F959E">客户管理后台 / 配置及后台</div><div style="font-size:16px;font-weight:600">自动化话术</div></div><div style="display:flex;gap:8px"><a href="agentEdit.html" style="height:30px;padding:0 12px;display:inline-flex;align-items:center;border:0;border-radius:6px;background:#3370ff;color:#fff;font-size:13px;text-decoration:none">新增 Agent</a><a href="agentEdit.html?type=fixed_script" style="height:30px;padding:0 12px;display:inline-flex;align-items:center;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#344054;font-size:13px;text-decoration:none">新增固定话术</a></div></div>
    <div style="padding:16px 20px;display:grid;gap:12px"><div style="padding:12px 14px;border:1px solid #D6E4FF;background:#F5F8FF;border-radius:8px;color:#245BDB;font-size:13px">当前配置可真实编辑；迁移项默认暂停。本页不运行 Agent、不发送消息，不调用 Provider。</div><div data-agent-result role="status" style="min-height:20px;font-size:13px"></div><div style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden"><div style="padding:12px 16px;border-bottom:1px solid #EFF0F1"><h2 style="margin:0;font-size:14px">自动化列表</h2></div><table style="width:100%;border-collapse:collapse"><thead><tr style="background:#FAFAFB;color:#8F959E;text-align:left"><th style="padding:9px 16px">自动化名称</th><th style="padding:9px 12px">自动化类型</th><th style="padding:9px 12px">固定素材</th><th style="padding:9px 12px">状态</th><th style="padding:9px 12px">更新时间</th><th style="padding:9px 16px;text-align:right">操作</th></tr></thead><tbody>${rows}</tbody></table></div></div>`;
  root.querySelectorAll<HTMLButtonElement>('[data-agent-action]').forEach((button) => button.addEventListener('click', () => {
    const agentID = Number(button.dataset.agentId);
    const action = button.dataset.agentAction;
    if (!Number.isSafeInteger(agentID) || agentID < 1) return;
    button.disabled = true;
    void (async () => {
      if (action === 'precheck') {
        const check = unwrapGenerated(await precheckLegacyAutomationAgent(agentID, apiRequestOptions())) as { configuration_ready: boolean; reasons: string[] };
        const ready = check.configuration_ready && (check as { can_activate?: boolean }).can_activate !== false && check.reasons.length === 0;
        if (ready) showResult(root, '配置检查通过：满足启用前置条件；启用仍由后端开关控制，本页不执行启用');
        else showResult(root, `当前不可启用：${check.reasons.join('、') || '配置未就绪'}`, true);
        button.disabled = false;
        return;
      } else if (action === 'copy') unwrapGenerated(await copyLegacyAutomationAgent(agentID, writeOptions()));
      else if (action === 'archive') unwrapGenerated(await archiveLegacyAutomationAgent(agentID, writeOptions()));
      else throw new Error('未知操作');
      await loadList(root);
      showResult(root, '保存成功；未触发任何外部效果');
    })().catch((error) => {
      button.disabled = false;
      showResult(root, error instanceof Error ? error.message : '操作失败', true);
    });
  }));
}

function materialSummary(content?: LegacyAutomationAgentDetail['fixed_content_package']): string {
  if (!content) return '<p style="font-size:12px;color:#8F959E">无既有素材</p>';
  const ids = (label: string, arr?: number[]) => {
    if (!Array.isArray(arr) || arr.length === 0) return '';
    return `<div style="display:flex;align-items:center;gap:8px;font-size:12px;color:#646A73"><span style="min-width:72px;color:#8F959E">${esc(label)}</span><span style="font:12px ui-monospace,Menlo,monospace;color:#4E5969">${esc(arr.join(', '))}</span><span style="color:#8F959E">（共 ${arr.length}）</span></div>`;
  };
  const parts = [
    ids('图片', content.image_library_ids),
    ids('小程序', content.miniprogram_library_ids),
    ids('PDF', content.attachment_library_ids),
    ids('群邀请', content.group_invite_library_ids),
  ].filter(Boolean);
  return parts.length ? parts.join('') : '<p style="font-size:12px;color:#8F959E">无既有素材 ID</p>';
}

async function loadEditor(root: HTMLElement, agentID?: number): Promise<void> {
  const current = agentID ? (unwrapGenerated(await getLegacyAutomationAgent(agentID, apiRequestOptions())) as { ok: boolean; agent: LegacyAutomationAgentDetail }).agent : undefined;
  const content = current?.fixed_content_package;
  const initialType = current?.automation_type || (new URLSearchParams(location.search).get('type') === 'fixed_script' ? 'fixed_script' : 'agent');
  const control = 'width:100%;box-sizing:border-box;min-height:36px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;padding:8px 10px;font-size:13px';
  root.innerHTML = `<div style="display:flex;align-items:center;justify-content:space-between;gap:16px;height:52px;padding:0 20px;background:#fff;border-bottom:1px solid #DEE0E3"><div style="display:flex;align-items:center;gap:10px"><a href="agents.html" style="width:28px;height:28px;border:1px solid #DEE0E3;border-radius:6px;display:flex;align-items:center;justify-content:center;text-decoration:none;color:#646A73">←</a><div><div style="font-size:12px;color:#8F959E">客户管理后台 / 配置及后台</div><div style="font-size:16px;font-weight:600">${current ? '编辑' : '新增'}自动化配置</div></div></div><a href="agents.html" style="height:30px;padding:0 12px;display:inline-flex;align-items:center;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#344054;font-size:13px;text-decoration:none">返回自动化列表</a></div>
    <form data-agent-form style="padding:16px 20px;display:grid;gap:12px"><div style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px"><div style="display:flex;align-items:center;justify-content:space-between;gap:12px"><div><h1 style="margin:0;font-size:16px">${current ? esc(current.agent_name) : '新自动化'}</h1><p style="margin:5px 0 0;font-size:12px;color:#8F959E">保存只更新 V2 当前配置；不运行 Agent、不发送消息、不调用 Provider。</p></div><div style="display:flex;gap:8px"><button type="submit" style="height:30px;padding:0 14px;border:0;border-radius:6px;background:#3370ff;color:#fff;cursor:pointer">保存</button>${current ? '<button type="button" data-agent-publish style="height:30px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;cursor:pointer">发布当前草稿</button>' : ''}</div></div><div data-agent-result role="status" style="min-height:20px;margin-top:8px;font-size:13px"></div></div>
    <div style="display:grid;grid-template-columns:226px minmax(0,1fr);gap:12px;align-items:start"><nav style="display:grid;gap:2px;padding:8px;border:1px solid #DEE0E3;border-radius:8px;background:#fff;position:sticky;top:0"><a href="#agent-basic" style="min-height:44px;display:grid;grid-template-columns:22px 1fr;gap:10px;align-items:center;padding:0 10px;border-radius:6px;background:#EFF4FF;color:#3370ff;text-decoration:none;font-size:13px;font-weight:500"><span style="width:22px;height:22px;display:grid;place-items:center;border-radius:50%;background:#3370ff;color:#fff">1</span>基本信息</a><a href="#agent-prompts" style="min-height:44px;display:grid;grid-template-columns:22px 1fr;gap:10px;align-items:center;padding:0 10px;color:#4E5969;text-decoration:none;font-size:13px"><span style="width:22px;height:22px;display:grid;place-items:center;border-radius:50%;background:#EEF2F7">2</span>Prompt 配置</a><a href="#agent-content" style="min-height:44px;display:grid;grid-template-columns:22px 1fr;gap:10px;align-items:center;padding:0 10px;color:#4E5969;text-decoration:none;font-size:13px"><span style="width:22px;height:22px;display:grid;place-items:center;border-radius:50%;background:#EEF2F7">3</span>固定素材</a></nav>
    <div style="display:grid;gap:12px"><section id="agent-basic" style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:18px"><h2 style="margin:0 0 16px;font-size:15px">基本信息</h2><div style="display:grid;grid-template-columns:1fr 1fr;gap:16px"><label style="display:grid;gap:6px;font-size:12px;color:#646A73">名称<input name="agent_name" required maxlength="120" value="${esc(current?.agent_name || '')}" style="${control}"></label><label style="display:grid;gap:6px;font-size:12px;color:#646A73">编码<input name="agent_code" required maxlength="120" pattern="[a-z0-9_-]+" ${current ? 'readonly' : ''} value="${esc(current?.agent_code || '')}" style="${control}"></label><label style="display:grid;gap:6px;font-size:12px;color:#646A73">类型<select name="automation_type" style="${control}"><option value="agent" ${initialType === 'agent' ? 'selected' : ''}>Agent 机器人</option><option value="fixed_script" ${initialType === 'fixed_script' ? 'selected' : ''}>固定话术</option></select></label><label style="display:grid;gap:6px;font-size:12px;color:#646A73">状态<input name="status" readonly value="paused（执行关闭）" style="${control};background:#F7F8FA"></label></div></section>
    <section id="agent-prompts" style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:18px"><h2 style="margin:0 0 16px;font-size:15px">Prompt 配置</h2><label style="display:grid;gap:6px;font-size:12px;color:#646A73">角色提示词<textarea name="role_prompt" maxlength="20000" rows="8" style="${control};line-height:20px;resize:vertical">${esc(current?.draft_role_prompt || '')}</textarea></label><label style="display:grid;gap:6px;margin-top:16px;font-size:12px;color:#646A73">任务提示词<textarea name="task_prompt" maxlength="20000" rows="10" style="${control};line-height:20px;resize:vertical">${esc(current?.draft_task_prompt || '')}</textarea></label></section>
    <section id="agent-content" style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:18px"><h2 style="margin:0 0 16px;font-size:15px">固定素材</h2><div style="margin-bottom:12px;padding:10px 12px;border:1px solid #F0D59A;background:#FFF7E8;border-radius:6px;font-size:12px;color:#8A4B08">后端契约不支持固定素材写入：OpenAPI 对四个素材 ID 数组 maxItems=0，保存时不会发送 fixed_content_package。以下为既有素材只读展示。</div><label style="display:grid;gap:6px;font-size:12px;color:#646A73">固定文本<textarea name="content_text" maxlength="4000" rows="5" disabled style="${control};line-height:20px;resize:vertical;background:#F7F8FA">${esc(content?.content_text || '')}</textarea></label><div style="display:grid;gap:8px;margin-top:12px">${materialSummary(content)}</div><details style="margin-top:16px;border-top:1px solid #EFF0F1;padding-top:14px"><summary style="cursor:pointer;font-size:13px;color:#646A73">V1 必要业务配置 JSON（高级）</summary><label style="display:grid;gap:6px;margin-top:12px;font-size:12px;color:#646A73">JSON<textarea name="legacy_configuration" rows="14" style="${control};font-family:ui-monospace,Menlo,monospace;line-height:20px;resize:vertical">${esc(JSON.stringify(current?.legacy_configuration || {}, null, 2))}</textarea></label></details></section></div></div></form>`;
  const form = root.querySelector<HTMLFormElement>('[data-agent-form]');
  form?.addEventListener('submit', (event) => {
    event.preventDefault();
    const values = new FormData(form);
    void (async () => {
      const legacy = JSON.parse(String(values.get('legacy_configuration') || '{}')) as Record<string, unknown>;
      if (legacy === null || Array.isArray(legacy) || typeof legacy !== 'object') throw new Error('V1 必要业务配置必须是 JSON 对象');
      const payload = {
        agent_name: String(values.get('agent_name') || '').trim(),
        automation_type: String(values.get('automation_type')) as 'agent' | 'fixed_script',
        status: 'paused' as const,
        role_prompt: String(values.get('role_prompt') || ''),
        task_prompt: String(values.get('task_prompt') || ''),
        legacy_configuration: legacy,
      };
      const saved = current
        ? unwrapGenerated(await updateLegacyAutomationAgent(current.id, payload, writeOptions()))
        : unwrapGenerated(await createLegacyAutomationAgent({ ...payload, agent_code: String(values.get('agent_code') || '').trim() }, writeOptions()));
      const nextID = (saved as { agent: LegacyAutomationAgentDetail }).agent.id;
      location.href = `agentEdit.html?id=${nextID}&saved=1`;
    })().catch((error) => showResult(root, error instanceof Error ? error.message : '保存失败', true));
  });
  root.querySelector<HTMLButtonElement>('[data-agent-publish]')?.addEventListener('click', (event) => {
    const button = event.currentTarget as HTMLButtonElement;
    if (!current) return;
    button.disabled = true;
    void publishLegacyAutomationAgent(current.id, writeOptions()).then(unwrapGenerated).then(() => loadEditor(root, current.id)).catch((error) => {
      button.disabled = false;
      showResult(root, error instanceof Error ? error.message : '发布失败', true);
    });
  });
  if (new URLSearchParams(location.search).get('saved') === '1') showResult(root, '保存成功；已从服务端重新读取');
}

export async function mountAutomationAgents(root: HTMLElement, page: 'list' | 'edit'): Promise<void> {
  if (page === 'list') return loadList(root);
  const rawID = new URLSearchParams(location.search).get('id');
  const agentID = rawID ? Number(rawID) : undefined;
  if (rawID && (!Number.isSafeInteger(agentID) || (agentID || 0) < 1)) throw new Error('Agent ID 无效');
  return loadEditor(root, agentID);
}
