import {
  approveAIAssistantPlan,
  getAIAssistantPlan,
  getAIAssistantRecipient,
  listAIAssistantPlans,
  listAIAssistantRecipients,
  previewAIAssistantPlanApproval,
  rejectAIAssistantPlan,
  reviewAIAssistantRecipient,
  updateAIAssistantRecipientContent,
  type JsonRecord,
} from './generated/aiAssistantClient';

const root = document.querySelector<HTMLElement>('[data-cloud-plan-root]');
const cursors = new Map<string, Map<number, string>>();

function cookie(name: string): string {
  for (const item of document.cookie.split(';')) {
    const [key, ...parts] = item.trim().split('=');
    if (key === name) return decodeURIComponent(parts.join('='));
  }
  return '';
}

function key(): string {
  return `ai-${Date.now()}-${crypto.randomUUID()}`;
}

async function request(url: string, init: Omit<RequestInit, 'body'> & { body?: any } = {}): Promise<JsonRecord> {
  const headers = new Headers(init.headers || {});
  headers.set('Accept', 'application/json');
  if (init.body && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
    init.body = JSON.stringify(init.body);
  }
  if (init.method && init.method !== 'GET') {
    headers.set('Idempotency-Key', key());
    const csrf = cookie('aicrm_csrf') || cookie('aicrm_admin_csrf');
    if (csrf) headers.set('X-CSRF-Token', csrf);
  }
  const response = await fetch(url, { ...init, headers, credentials: 'same-origin' });
  const text = await response.text();
  let payload: JsonRecord = {};
  try { payload = text ? JSON.parse(text) : {}; } catch { /* fail below */ }
  if (!response.ok || payload.ok === false) throw new Error(errorText(payload.error || response.status));
  return payload;
}

function errorText(code: string | number): string {
  const messages: Record<string, string> = {
    version_or_idempotency_conflict: '页面数据已变化，请刷新后重试',
    material_drift: '素材已删除或版本发生变化，请重新选择',
    ai_assistant_unavailable: '发送能力尚未就绪，请联系管理员',
    permission_denied: '没有执行此操作的权限',
  };
  return messages[String(code)] || '请求失败，请刷新页面后重试';
}

function plan(value: JsonRecord): JsonRecord {
  const running = ['dispatching', 'needs_attention'].includes(value.state);
  const execution = planExecutionLabel(String(value.state || ''));
  return { plan_id: value.id, display_name: value.name, owner_userid: `管理员 #${value.created_by}${execution ? ` · ${execution}` : ''}`,
    updated_at: value.updated_at, target_count: value.target_count,
    review_status: value.state === 'rejected' ? 'rejected' : running || value.state.startsWith('completed') ? 'approved' : value.state,
    run_status: running ? 'running' : value.state.startsWith('completed') ? 'completed' : 'pending', version: value.version };
}

function sendState(value: string): string {
  if (value === 'delivery_proven') return 'sent';
  if (value === 'queued' || value === 'accepted') return 'queued';
  if (value === 'attempted' || value === 'provider_accepted' || value === 'reconciled') return 'sending';
  if (value === 'final_failed' || value === 'retryable_failed' || value === 'outcome_unknown') return 'failed';
  return 'pending';
}

function recipient(value: JsonRecord, count = 1): JsonRecord {
  const execution = executionLabel(String(value.execution_state || ''));
  return { recipient_id: value.id, display_name: value.customer_name || `用户 #${value.customer_id}`,
    external_userid: `${value.oneid_label || `OneID #${value.customer_id}`} · ${execution}`, owner_userid: value.staff_display_name || `员工 #${value.staff_id}`,
    updated_at: value.updated_at, planned_message_count: count, approval_status: value.review_state === 'pending_review' ? 'pending' : value.review_state,
    send_status: sendState(value.execution_state), supports_recipient_approval: true, version: value.version };
}

function executionLabel(state: string): string {
  return ({ not_accepted: '待整单确认', accepted: '效果已接受', queued: '已持久排队', attempted: 'Provider 调用已尝试', provider_accepted: 'Provider 已接受（未证明送达）', retryable_failed: '调用前失败，可按原键重试', outcome_unknown: '结果未知，需人工对账', reconciled: '已人工对账（非送达证明）', final_failed: '最终失败', delivery_proven: '已有可信送达证据' } as Record<string, string>)[state] || '状态未知';
}

function planExecutionLabel(state: string): string {
  return ({ dispatching: '执行中（未证明送达）', needs_attention: '执行需人工处理', completed_with_failures: '执行结束，存在失败', completed: '执行已结束（不等于全部送达）' } as Record<string, string>)[state] || '';
}

function contentPackage(blocks: JsonRecord[]): JsonRecord {
  return { content_text: blocks.filter((b) => b.kind === 'text').map((b) => b.text).join('\n'),
    image_library_ids: blocks.filter((b) => b.kind === 'image').map((b) => b.material_id),
    miniprogram_library_ids: blocks.filter((b) => b.kind === 'mini_program').map((b) => b.material_id),
    attachment_library_ids: blocks.filter((b) => b.kind === 'attachment').map((b) => b.material_id), group_invite_library_ids: blocks.filter((b) => b.kind === 'link').map((b) => b.material_id),
  };
}

function blocks(input: JsonRecord): JsonRecord[] {
  const pkg = input.content_payload?.content_package || input.content_package || input;
  const result: JsonRecord[] = [];
  const text = String(input.content_text || pkg.content_text || '').trim();
  if (text) result.push({ kind: 'text', text });
  for (const id of pkg.image_library_ids || []) result.push({ kind: 'image', material_kind: 'image', material_id: Number(id) });
  for (const id of pkg.miniprogram_library_ids || []) result.push({ kind: 'mini_program', material_kind: 'miniprogram', material_id: Number(id) });
  for (const id of pkg.attachment_library_ids || []) result.push({ kind: 'attachment', material_kind: 'attachment', material_id: Number(id) });
  for (const id of pkg.group_invite_library_ids || []) result.push({ kind: 'link', material_kind: 'group_invite', material_id: Number(id) });
  return result;
}

async function plansForDonorFilter(keyword: string, status: string): Promise<JsonRecord[]> {
  const states = status === 'pending_review'
    ? ['pending_review', 'partially_approved']
    : status === 'approved'
      ? ['approved', 'dispatching', 'needs_attention', 'completed_with_failures', 'completed']
      : status === 'active'
        ? ['dispatching', 'needs_attention']
        : status ? [status] : [''];
  const pages = await Promise.all(states.map((state) => listAIAssistantPlans(request, { limit: 50, keyword, status: state })));
  return pages.flatMap((page) => page.items || [])
    .sort((left, right) => String(right.updated_at || '').localeCompare(String(left.updated_at || '')) || Number(right.id || 0) - Number(left.id || 0))
    .slice(0, 50);
}

async function translated(url: string, options: any = {}): Promise<JsonRecord> {
  const method = options.method || 'GET';
  let match: RegExpMatchArray | null;
  if (method === 'GET' && url.startsWith('/api/admin/cloud-orchestrator/plans?')) {
    const query = new URL(url, location.origin).searchParams;
    const items = await plansForDonorFilter(query.get('keyword') || '', query.get('status') || '');
    return { ok: true, plans: items.map(plan) };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)$/)) && method === 'GET') {
    const payload = await getAIAssistantPlan(request, match[1]); return { ok: true, plan: plan(payload.plan) };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)\/recipients\?(.*)$/)) && method === 'GET') {
    const q = new URLSearchParams(match[2]); const offset = Number(q.get('offset') || 0); const cacheKey = match[1];
    if (!cursors.has(cacheKey)) cursors.set(cacheKey, new Map([[0, '']]));
    const cursor = cursors.get(cacheKey)!.get(offset) || '';
    const [p, page] = await Promise.all([getAIAssistantPlan(request, match[1]), listAIAssistantRecipients(request, match[1], { limit: 50, cursor })]);
    const rows = (page.items || []).map((item: JsonRecord) => recipient(item));
    if (page.next_cursor) cursors.get(cacheKey)!.set(offset + rows.length, page.next_cursor);
    return { ok: true, plan: plan(p.plan), rows, total: p.plan.target_count };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)\/recipients\/(\d+)$/)) && method === 'GET') {
    const payload = await getAIAssistantRecipient(request, match[1], match[2]); const pkg = contentPackage(payload.content.blocks || []);
    return { ok: true, recipient: recipient(payload.recipient), messages: [{ message_id: payload.content.id, sequence_index: 1, day_offset: 0, send_time: '确认后发送', status: sendState(payload.recipient.execution_state), content_text: pkg.content_text, content_payload: { content_package: pkg } }] };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)\/recipients\/(\d+)\/(approve|reject)$/)) && method === 'POST') {
    const current = await getAIAssistantRecipient(request, match[1], match[2]);
    const changed = await reviewAIAssistantRecipient(request, match[1], match[2], { expected_version: current.recipient.version, decision: match[3] === 'approve' ? 'approved' : 'rejected', reason: options.body?.reason || '' });
    return { ok: true, recipient: recipient(changed.recipient), status: 'approved' };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)\/(approve|reject)$/)) && method === 'POST') {
    const current = await getAIAssistantPlan(request, match[1]);
    if (match[2] === 'reject') { const changed = await rejectAIAssistantPlan(request, match[1], { expected_version: current.plan.version, reason: options.body?.reason || 'admin_ui_reject' }); return { ok: true, plan: plan(changed.plan) }; }
    const preview = await previewAIAssistantPlanApproval(request, match[1], { expected_version: current.plan.version });
    const changed = await approveAIAssistantPlan(request, match[1], { expected_version: current.plan.version, preview_digest: preview.preview_digest });
    return { ok: true, plan: plan(changed.plan) };
  }
  if ((match = url.match(/^\/api\/admin\/cloud-orchestrator\/plans\/(\d+)\/recipients\/(\d+)\/messages\/(\d+)$/)) && method === 'PATCH') {
    const current = await getAIAssistantRecipient(request, match[1], match[2]);
    const changed = await updateAIAssistantRecipientContent(request, match[1], match[2], { expected_version: current.recipient.version, blocks: blocks(options.body || {}) });
    const latest = await getAIAssistantRecipient(request, match[1], match[2]); const pkg = contentPackage(changed.blocks || []);
    return { ok: true, recipient: recipient(latest.recipient), message: { message_id: changed.id, sequence_index: 1, day_offset: 0, send_time: '确认后发送', status: 'pending', content_text: pkg.content_text, content_payload: { content_package: pkg } } };
  }
  if (url === '/api/admin/send-content/preview') return { ok: true, preview: { materials: [] } };
  return request(url, options);
}

(window as any).AdminApi = { ...(window as any).AdminApi, requestJson: translated,
  escapeHtml: (value: any) => String(value ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!)) };

if (root) root.querySelectorAll('.cloud-plan-field-label').forEach((node) => { if (node.textContent?.trim() === 'external_userid') node.textContent = 'OneID'; });

async function bootDonor(): Promise<void> {
  for (const source of ['/ai-assistant-assets/assets/standard-components/group_chat_picker.js','/ai-assistant-assets/assets/standard-components/material_picker.js','/ai-assistant-assets/assets/standard-components/send_content_composer.js','/ai-assistant-assets/aiassistant/send_content_readonly_detail.js','/ai-assistant-assets/aiassistant/cloud_plan_review.js']) {
    await new Promise<void>((resolve, reject) => { const script = document.createElement('script'); script.src = source; script.onload = () => resolve(); script.onerror = () => reject(new Error('AI 助手组件加载失败')); document.body.append(script); });
  }
}
void bootDonor();
