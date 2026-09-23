import {
  acceptCampaignOutboundHandoffDto,
  decideCampaignTouchPlanReviewDto,
  decideCampaignTouchPlanRecipientReviewDto,
  deleteCampaignDto,
  dispatchCampaignOutboundRecipientDto,
  getCampaignDto,
  getCampaignOutboundHandoffReconciliationDto,
  getCampaignTouchPlanDto,
  getCampaignTouchPlanRecipientDto,
  getCampaignTouchPlanRecipientReviewDto,
  getCampaignTouchPlanReviewDto,
  listCampaignMembersDto,
  listCampaignPlanIndexDto,
  listCampaignsDto,
  listCampaignTouchPlanRecipientsDto,
  listCampaignTouchPlansDto,
  saveCampaignTouchPlanRecipientMessageDto,
  tryGetCampaignOutboundDispatchReconciliationDto,
  tryGetCampaignOutboundHandoffDto,
  dispatchCampaignOutboundHandoffDto,
  type CampaignDetail,
  type CampaignFilter,
  type CampaignMemberStatusPage,
  type CampaignOutboundDispatchReconciliation,
  type CampaignOutboundHandoff,
  type CampaignOutboundHandoffReconciliation,
  type CampaignTouchPlan,
  type CampaignTouchPlanDetail,
  type CampaignTouchPlanIndexItem,
  type CampaignTouchPlanIndexPage,
  type CampaignTouchPlanRecipient,
  type CampaignTouchPlanRecipientPage,
  type CampaignTouchPlanRecipientReview,
  type CampaignTouchPlanReview,
} from '../../api/admin';
import {
  cancelExternalEffectRuntimeDto,
  cancelPushCenterJobDto,
  getPushCenterJobDetailDto,
  readExternalEffectsWorkspaceDto,
  retryPushCenterJobDto,
  type ExternalEffectsWorkspace,
  type PushCenterJobDetail,
} from '../../api/external_effects';
import { confirmBox, toast } from '../../shared/ui/feedback';
import { mountPushObservability } from './observability';
import { mountCampaignDefinitionHistory } from './campaignDefinitionHistory';
import { esc } from './util';

type CampaignPage = {
  campaign: CampaignDetail;
  plans: CampaignTouchPlan[];
  plan?: CampaignTouchPlanDetail;
  review?: CampaignTouchPlanReview;
  recipientPage?: CampaignTouchPlanRecipientPage;
  recipient?: CampaignTouchPlanRecipient;
  recipientReview?: CampaignTouchPlanRecipientReview | null;
  handoff?: CampaignOutboundHandoff | null;
  handoffReconciliation?: CampaignOutboundHandoffReconciliation;
  dispatchReconciliation?: CampaignOutboundDispatchReconciliation | null;
};

const button = 'height:30px;padding:0 11px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const primaryButton = button + ';border-color:#3370ff;background:#3370ff;color:#fff';

const query = (): URLSearchParams => new URLSearchParams(location.search);
const goto = (campaignCode?: string, planID?: string, customerID?: number): void => {
  const params = new URLSearchParams();
  if (campaignCode) params.set('campaign', campaignCode);
  if (planID) params.set('plan', planID);
  if (customerID) params.set('recipient', String(customerID));
  location.href = `campaigns.html${params.size ? `?${params.toString()}` : ''}`;
};
const gotoExternalEffects = (jobID?: number): void => {
  const params = new URLSearchParams({ view: 'external-effects' });
  if (jobID) params.set('job', String(jobID));
  location.href = `campaigns.html?${params.toString()}`;
};
const gotoObservability = (): void => { location.href = 'campaigns.html?view=observability'; };
const status = (value: string): string => `<span style="display:inline-flex;padding:2px 8px;border-radius:999px;background:#F2F4F7;color:#475467;font-size:12px">${esc(value)}</span>`;
const safety = '<p style="margin:0;color:#8F5A16;font-size:12px">只读快照/本地审核不证明 Provider 调用、外部发送或送达。</p>';
const reviewAuditValue = (actorID: number | null, at: string | null): string => `${actorID == null ? '未记录操作人' : `actor #${actorID}`} · ${at == null ? '未记录时间' : esc(at)}`;
const planReviewAuditHtml = (review: CampaignTouchPlanReview): string => `<div data-campaign-review-audit style="padding:10px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:12px;line-height:18px"><strong>V2 产品内本地审核审计</strong><div>提交：${reviewAuditValue(review.submittedByActorID, review.submittedAt)}</div><div>审核：${reviewAuditValue(review.reviewedByActorID, review.reviewedAt)}</div><div style="color:#8F5A16">仅展示真实 review 返回的 actor/time；不代表 Provider 发送、回执或送达。可在“可观察性”中按 trace/session 查看本地 audit 事实。</div></div>`;

function shell(title: string, body: string): string {
  return `<div style="padding:20px;display:grid;gap:16px;align-content:start"><div><div style="font-size:12px;color:#8F959E">运营 / Cloud Campaign</div><h1 style="margin:4px 0 0;font-size:20px">${title}</h1><a href="campaigns.html?history=1">V1 Campaign 业务历史（只读）</a> · <a href="campaigns.html?definition_history=1">V1 Campaign 定义历史（只读）</a></div>${body}</div>`;
}

function listHtml(rows: CampaignDetail[], filter: CampaignFilter): string {
  const rowHtml = rows.map((item) => `<tr><td style="padding:10px 12px;border-bottom:1px solid #EEF0F3"><button data-campaign="${esc(item.code)}" style="border:0;background:transparent;color:#1849A9;cursor:pointer;font-weight:600">${esc(item.name)}</button><div style="margin-top:3px;color:#8F959E;font-family:ui-monospace,Menlo,monospace;font-size:12px">${esc(item.code)}</div></td><td style="padding:10px 12px;border-bottom:1px solid #EEF0F3">${status(item.approvalStatus)}</td><td style="padding:10px 12px;border-bottom:1px solid #EEF0F3">${status(item.runtimeStatus)}</td><td style="padding:10px 12px;border-bottom:1px solid #EEF0F3">v${item.version}</td><td style="padding:10px 12px;border-bottom:1px solid #EEF0F3">${esc(item.updatedAt)}</td></tr>`).join('') || '<tr><td colspan="5" style="padding:24px;text-align:center;color:#8F959E">当前筛选下没有 Campaign</td></tr>';
  return shell('Campaign 本地生命周期', `<div style="display:flex;gap:10px;flex-wrap:wrap"><label>审核状态 <select id="campaign-approval"><option value="">全部</option>${['draft', 'approved', 'rejected'].map((value) => `<option value="${value}"${filter.approvalStatus === value ? ' selected' : ''}>${value}</option>`).join('')}</select></label><label>运行状态 <select id="campaign-runtime"><option value="">全部</option>${['idle', 'planned', 'paused'].map((value) => `<option value="${value}"${filter.runtimeStatus === value ? ' selected' : ''}>${value}</option>`).join('')}</select></label><button id="campaign-refresh" style="${button}">刷新</button><button id="external-effects-open" style="${button}">外部效果与 Push Center</button><button id="observability-open" style="${button}">可观察性</button></div><div style="border:1px solid #DEE0E3;border-radius:8px;overflow:hidden"><table style="width:100%;border-collapse:collapse"><thead><tr style="background:#FAFAFB;color:#667085;text-align:left"><th style="padding:10px 12px">Campaign</th><th style="padding:10px 12px">审核</th><th style="padding:10px 12px">运行</th><th style="padding:10px 12px">版本</th><th style="padding:10px 12px">更新时间</th></tr></thead><tbody>${rowHtml}</tbody></table></div><div style="display:grid;gap:10px"><div><h2 style="margin:0 0 6px;font-size:15px">Campaign 命中成员</h2><p style="margin:0;color:#667085;font-size:13px">进入 Campaign 详情后，可按真实本地状态查看成员与总数。</p></div><div><h2 style="margin:0 0 6px;font-size:15px">可观察性与审计筛选</h2><p style="margin:0;color:#667085;font-size:13px">可按 trace_id 筛选 Push 本地聚合，并按 trace_id 或 session_id 读取 Cloud audit 本地事实；不代表 Provider 发送或送达。</p></div></div>`);
}

function planRows(plans: CampaignTouchPlan[]): string {
  return plans.map((plan) => `<tr><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3"><button data-plan="${esc(plan.id)}" style="border:0;background:transparent;color:#1849A9;font-family:ui-monospace,Menlo,monospace;cursor:pointer">${esc(plan.id)}</button></td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${esc(plan.sourceKind)}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${plan.targetCount}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${plan.contentStepCount}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${esc(plan.createdAt)}</td></tr>`).join('') || '<tr><td colspan="5" style="padding:20px;text-align:center;color:#8F959E">暂无已冻结的本地 touch plan</td></tr>';
}

function planIndexHtml(page: CampaignTouchPlanIndexPage, reviewStatus: CampaignTouchPlanIndexItem['reviewStatus'] | '' = ''): string {
  const rows = page.items.map((item) => `<tr><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3"><button data-campaign="${esc(item.campaignCode)}" data-plan="${esc(item.id)}" style="border:0;background:transparent;color:#1849A9;font-family:ui-monospace,Menlo,monospace;cursor:pointer">${esc(item.id)}</button><div style="color:#8F959E;font-size:12px">${esc(item.campaignCode)}</div></td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${status(item.reviewStatus)} · v${item.reviewVersion}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${esc(item.sourceKind)}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${item.targetCount}</td><td style="padding:9px 12px;border-bottom:1px solid #EEF0F3">${esc(item.createdAt)}</td></tr>`).join('') || '<tr><td colspan="5" style="padding:20px;text-align:center;color:#8F959E">当前筛选下没有本地 touch plan</td></tr>';
  const more = page.nextCursor ? `<button id="plan-index-more" style="${button}">加载更多</button>` : '';
  return shell('运营计划本地审核', `<div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap"><label>审核状态 <select id="plan-index-status"><option value="">全部</option>${['draft', 'pending_review', 'approved', 'rejected'].map((value) => `<option value="${value}"${reviewStatus === value ? ' selected' : ''}>${value}</option>`).join('')}</select></label><button id="plan-index-refresh" style="${button}">刷新</button></div><div style="padding:10px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px">这里只展示本地冻结计划与本地审核状态；不代表发送、Provider 执行或送达。</div><div style="border:1px solid #DEE0E3;border-radius:8px;overflow:hidden"><table style="width:100%;border-collapse:collapse"><thead><tr style="color:#667085;text-align:left"><th style="padding:9px 12px">计划 / Campaign</th><th style="padding:9px 12px">审核</th><th style="padding:9px 12px">来源</th><th style="padding:9px 12px">目标数</th><th style="padding:9px 12px">创建时间</th></tr></thead><tbody>${rows}</tbody></table></div>${more}${safety}`);
}

function campaignHtml(page: CampaignPage): string {
  const campaign = page.campaign;
  const steps = campaign.steps.map((step) => `<li style="margin:5px 0"><strong>第 ${step.index} 步 · 延迟 ${step.delayMinutes} 分钟</strong><div style="margin-top:3px;white-space:pre-wrap">${esc(step.content)}</div></li>`).join('') || '<li>无步骤</li>';
  return shell(esc(campaign.name), `<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="campaign-back" style="${button}">返回列表</button><span style="font-family:ui-monospace,Menlo,monospace;color:#667085">${esc(campaign.code)}</span>${status(campaign.approvalStatus)}${status(campaign.runtimeStatus)}<span>v${campaign.version}</span><button id="campaign-delete" style="${button};border-color:#F5B7B1;color:#B42318">删除本地 Campaign</button></div><div style="display:grid;grid-template-columns:minmax(280px,1fr) minmax(360px,1fr);gap:14px"><section style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"><h2 style="margin:0 0 8px;font-size:15px">Campaign 详情</h2><ol style="margin:0;padding-left:20px">${steps}</ol><button id="campaign-members-open" style="${button};margin-top:12px">查看成员状态</button>${safety}</section><section style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"><h2 style="margin:0 0 8px;font-size:15px">运营计划（已冻结 touch plan）</h2><table style="width:100%;border-collapse:collapse"><thead><tr style="color:#667085;text-align:left"><th>计划</th><th>来源</th><th>目标</th><th>步骤</th><th>创建时间</th></tr></thead><tbody>${planRows(page.plans)}</tbody></table>${safety}</section></div><aside id="campaign-members-drawer" hidden></aside>`);
}

function campaignMembersHtml(page: CampaignMemberStatusPage): string {
  const rows = page.items.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${item.customerID}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.status)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${esc(item.planID)}</td></tr>`).join('') || '<tr><td colspan="3" style="padding:16px;color:#8F959E">当前页没有 Campaign 成员</td></tr>';
  const previous = page.offset > 0 ? `<button id="campaign-members-prev" style="${button}">上一页</button>` : '';
  const next = page.offset + page.limit < page.total ? `<button id="campaign-members-next" style="${button}">下一页</button>` : '';
  const first = page.items.length === 0 ? 0 : page.offset + 1;
  return `<aside id="campaign-members-drawer" data-campaign-members="ready" style="padding:14px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;display:grid;gap:10px"><div style="display:flex;justify-content:space-between;gap:8px;align-items:center"><div><h2 style="margin:0;font-size:15px">Campaign 成员状态</h2><p style="margin:4px 0 0;color:#667085;font-size:12px">本地最新 touch-plan 状态 · 共 ${page.total} 人 · ${first}-${Math.min(page.offset + page.items.length, page.total)}</p></div><button id="campaign-members-close" style="${button}">关闭</button></div><table style="width:100%;border-collapse:collapse;background:#fff"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">Customer ID</th><th style="padding:8px">状态</th><th style="padding:8px">Touch plan</th></tr></thead><tbody>${rows}</tbody></table><div style="display:flex;justify-content:flex-end;gap:8px">${previous}${next}</div><p style="margin:0;color:#8F5A16;font-size:12px">状态仅来自 V2 本地成员投影，不代表 Provider 调用、发送或送达。</p></aside>`;
}

function renderCampaignMembers(stage: HTMLElement, campaignCode: string, page: CampaignMemberStatusPage): void {
  const drawer = stage.querySelector<HTMLElement>('#campaign-members-drawer');
  if (!drawer) return;
  drawer.outerHTML = campaignMembersHtml(page);
  stage.querySelector<HTMLButtonElement>('#campaign-members-close')?.addEventListener('click', () => {
    const current = stage.querySelector<HTMLElement>('#campaign-members-drawer');
    if (current) current.hidden = true;
  });
  stage.querySelector<HTMLButtonElement>('#campaign-members-prev')?.addEventListener('click', () => void loadCampaignMembers(stage, campaignCode, page.offset - page.limit));
  stage.querySelector<HTMLButtonElement>('#campaign-members-next')?.addEventListener('click', () => void loadCampaignMembers(stage, campaignCode, page.offset + page.limit));
}

async function loadCampaignMembers(stage: HTMLElement, campaignCode: string, offset: number): Promise<void> {
  const drawer = stage.querySelector<HTMLElement>('#campaign-members-drawer');
  if (!drawer) return;
  drawer.hidden = false;
  drawer.innerHTML = '<p data-campaign-members="loading" style="margin:0;color:#667085">正在读取真实 Campaign 成员状态…</p>';
  try {
    renderCampaignMembers(stage, campaignCode, await listCampaignMembersDto(campaignCode, { limit: 50, offset }));
  } catch (error) {
    const current = stage.querySelector<HTMLElement>('#campaign-members-drawer');
    if (!current) return;
    current.outerHTML = `<aside id="campaign-members-drawer" data-campaign-members="error" style="padding:14px;border:1px solid #F2B8B5;border-radius:8px;background:#FFF1F0;color:#B42318;display:grid;gap:8px"><strong>Campaign 成员状态读取失败</strong><span>${esc(error instanceof Error ? error.message : '请求失败')}；不会使用 Mock 或 Seed 补齐。</span><button id="campaign-members-close" style="${button};justify-self:start">关闭</button></aside>`;
    stage.querySelector<HTMLButtonElement>('#campaign-members-close')?.addEventListener('click', () => {
      const failed = stage.querySelector<HTMLElement>('#campaign-members-drawer');
      if (failed) failed.hidden = true;
    });
  }
}

const count = (label: string, value: number): string => `<div style="display:flex;justify-content:space-between;gap:12px"><span>${label}</span><strong>${value}</strong></div>`;
function handoffHtml(page: CampaignPage): string {
  const review = page.review!;
  if (review.status !== 'approved' || !review.handoffStatus) return `<section style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"><h2 style="margin:0 0 8px;font-size:15px">Outbound handoff（count-only）</h2><p style="margin:0;color:#667085;font-size:13px">须先完成计划本地审核，服务端生成可受理的 handoff 后才会开放下一步。本页不会创建模拟受理或发送结果。</p></section>`;
  if (!page.handoff) return `<section style="padding:14px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF"><h2 style="margin:0 0 8px;font-size:15px">Outbound handoff（待本地受理）</h2><p style="margin:0 0 10px;color:#1849A9;font-size:13px">计划审核已完成，但尚无受理后的 held 快照。受理只保存本地事实并创建内部事件工作，不会调用 Provider、发送或证明送达。</p><div style="display:flex;gap:8px;flex-wrap:wrap"><button id="handoff-accept" style="${primaryButton}">二次确认并受理 handoff</button><button id="handoff-refresh" style="${button}">刷新真实状态</button></div></section>`;
  const reconciliation = page.handoffReconciliation!;
  const dispatch = page.dispatchReconciliation;
  const handoffCounts = [count('held', reconciliation.heldCount), count('blocked', reconciliation.blockedCount), count('pending', reconciliation.pendingCount), count('not_evaluated', reconciliation.notEvaluatedCount), count('eligible', reconciliation.eligibleCount), count('inactive', reconciliation.inactiveCount), count('contact_policy', reconciliation.contactPolicyCount)].join('');
  const dispatchCounts = dispatch ? [count('accepted（本地受理）', dispatch.accepted), count('queued（本地 EER 排队）', dispatch.queued), count('attempted（本地记录）', dispatch.attempted), count('executed（本地记录）', dispatch.executed), count('reconciled（本地投影）', dispatch.reconciled), count('retryable_failed', dispatch.retryableFailed), count('final_failed', dispatch.finalFailed)].join('') : '<p style="margin:0;color:#667085;font-size:13px">尚未创建本地 EER 排队工作。</p>';
  const unknown = dispatch && dispatch.outcomeUnknown > 0 ? `<div style="padding:9px;border:1px solid #F5D6A7;border-radius:6px;background:#FFF9F0;color:#8F5A16;font-size:13px"><strong>outcome_unknown：${dispatch.outcomeUnknown}</strong>。结果未知，需人工复核；不得自动重试或宣称发送/送达。</div>` : '<p style="margin:0;color:#667085;font-size:13px">当前 count-only 投影没有 outcome_unknown。</p>';
  const dispatchFacts = dispatch ? `<div style="font-size:12px;color:#8F5A16">handoff 历史事实：business_call_dispatched=${dispatch.businessCallDispatched} · real_external_call_executed=${dispatch.realExternalCallExecuted} · delivery_proven=false</div>` : '';
  return `<section style="padding:14px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;display:grid;gap:10px"><div><h2 style="margin:0 0 5px;font-size:15px">Outbound handoff（count-only）</h2><div style="font-size:13px;color:#1849A9">状态 ${status(page.handoff.status)} · handoff #${page.handoff.id} · 审核版本 v${page.handoff.reviewVersion} · 目标 ${page.handoff.targetCount} · 步骤 ${page.handoff.stepCount}</div></div><div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:8px"><div style="padding:9px;border:1px solid #D6E4FF;border-radius:6px;background:#fff;display:grid;gap:5px"><strong style="font-size:13px">受理复核</strong>${handoffCounts}</div><div style="padding:9px;border:1px solid #D6E4FF;border-radius:6px;background:#fff;display:grid;gap:5px"><strong style="font-size:13px">EER 本地投影</strong>${dispatchCounts}</div></div>${dispatchFacts}${unknown}<div style="display:flex;gap:8px;flex-wrap:wrap"><button id="handoff-refresh" style="${button}">刷新 count-only 回执</button><button id="handoff-dispatch" style="${primaryButton}">再次确认并排入本地 EER</button></div><p style="margin:0;color:#8F5A16;font-size:12px">accepted/queued 仅表示本地受理或本地排队，不等于发送或送达。business/real 标记只是该 handoff 已有历史事实，仍不证明本次动作送达。</p></section>`;
}

function planHtml(page: CampaignPage): string {
  const plan = page.plan!;
  const review = page.review!;
  const recipients = page.recipientPage?.items || [];
  const steps = plan.steps.map((step) => `<li><strong>第 ${step.index} 步 · 延迟 ${step.delayMinutes} 分钟</strong><div style="white-space:pre-wrap">${esc(step.content)}</div></li>`).join('') || '<li>无步骤</li>';
  const recipientRows = recipients.map((recipient) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${recipient.customerID}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3"><button data-recipient="${recipient.customerID}" style="${button}">读取范围详情</button></td></tr>`).join('') || '<tr><td colspan="2" style="padding:16px;color:#8F959E">没有可展示的收件人</td></tr>';
  const recipientDispatch = page.recipient && page.recipientReview?.status === 'approved' && review.status === 'approved' && page.handoff?.status === 'held' ? `<div data-recipient-dispatch-ready style="padding:9px;border:1px solid #F5D6A7;border-radius:6px;background:#FFF9F0;color:#8F5A16;font-size:12px"><button id="recipient-dispatch" style="${primaryButton}">受控发送该客户</button><span style="margin-left:8px">动作后返回 handoff 本地汇总；accepted/queued 不等于 Provider 发送或送达。</span></div>` : '';
  const recipientDetail = page.recipient ? `<div style="display:grid;gap:10px;padding:10px;border:1px solid #D6E4FF;border-radius:6px;background:#F5F8FF"><div>已验证当前 plan 范围内 canonical_customer_id：<strong>${page.recipient.customerID}</strong>。当前契约不含昵称、成员状态或消息任务。<a href="customerDetail.html?id=${page.recipient.customerID}" style="margin-left:8px;color:#1849A9">在 Customer360 查看档案</a></div><label style="display:grid;gap:5px">单客户消息覆盖<textarea id="recipient-message" maxlength="4000" rows="4" style="padding:8px;border:1px solid #D0D5DD;border-radius:6px">${esc(page.recipientReview?.messageOverride || '')}</textarea></label><div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="recipient-message-save" style="${button}">保存本地消息</button>${page.review?.status === 'pending_review' && (!page.recipientReview || page.recipientReview.status === 'pending_review') ? `<button id="recipient-review-approve" style="${primaryButton}">批准该客户</button><button id="recipient-review-reject" style="${button};border-color:#F5B7B1;color:#B42318">拒绝该客户</button>` : ''}${page.recipientReview ? status(page.recipientReview.status) + `<span style="font-size:12px;color:#667085">v${page.recipientReview.version}</span>` : '<span style="font-size:12px;color:#667085">尚无本地单客户审核记录</span>'}</div>${page.recipientReview ? `<div data-campaign-recipient-review-audit style="font-size:12px;color:#1849A9">本地 review 更新：${reviewAuditValue(page.recipientReview.updatedByActorID, page.recipientReview.updatedAt)}</div>` : ''}${recipientDispatch}<p style="margin:0;color:#8F5A16;font-size:12px">保存、批准、拒绝均只写本地 review，不会创建发送任务或调用 Provider。</p></div>` : '';
  const actions = review.status === 'pending_review' ? `<button id="plan-approve" style="${primaryButton}">批准本地审核</button><button id="plan-reject" style="${button};border-color:#F5B7B1;color:#B42318">拒绝本地审核</button>` : `<span style="color:#8F959E;font-size:13px">当前状态不是 pending_review，不能提交批准/拒绝。</span>`;
  const more = page.recipientPage?.nextCursor ? `<button id="recipient-more" style="${button};margin-top:10px">加载更多目标人员</button>` : '';
  return shell('Touch plan 本地审核', `<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="plan-back" style="${button}">返回 Campaign</button><span style="font-family:ui-monospace,Menlo,monospace;color:#667085">${esc(plan.id)}</span>${status(review.status)}<span>审核版本 v${review.version}</span>${review.handoffStatus ? status(review.handoffStatus) : ''}</div><div style="padding:12px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px">批准只写入本地 touch-plan review；即使生成 handoff 也仍须二次确认受理，不等于发送或送达。</div>${planReviewAuditHtml(review)}${handoffHtml(page)}<div style="display:grid;grid-template-columns:minmax(280px,1fr) minmax(360px,1fr);gap:14px"><section style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"><h2 style="margin:0 0 8px;font-size:15px">计划详情</h2><p>来源：${esc(plan.sourceKind)} · 目标：${plan.targetCount} · Campaign 版本：v${plan.campaignVersion}</p><ol style="padding-left:20px">${steps}</ol><div style="display:flex;gap:8px;flex-wrap:wrap">${actions}</div></section><section style="padding:14px;border:1px solid #DEE0E3;border-radius:8px"><h2 style="margin:0 0 8px;font-size:15px">目标人员（canonical OneID）</h2><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">OneID</th><th style="padding:8px">范围详情</th></tr></thead><tbody>${recipientRows}</tbody></table>${more}${recipientDetail}</section></div>`);
}

const effectBoundary = '<div style="padding:10px;border:1px solid #F5D6A7;border-radius:8px;background:#FFF9F0;color:#8F5A16;font-size:13px;line-height:20px"><strong>本地事实边界：</strong>accepted、queued、attempted、executed、sent 仅为本地投影或任务状态，不证明 Provider 调用、外部发送或送达。outcome_unknown 必须人工确认，不得自动重试。</div>';
const effectBlocked = '<div style="padding:10px;border:1px solid #F5D6A7;border-radius:8px;background:#FFF9F0;color:#8F5A16;font-size:13px"><strong>backend_blocked</strong>：当前真实 OpenAPI 没有安全的 run-due / runtime retry / 人工证据对账输入契约；页面不会推测参数、不会发起请求。</div>';

function countCard(label: string, value: number): string {
  return `<div style="padding:10px;border:1px solid #DEE0E3;border-radius:8px;background:#fff"><div style="font-size:12px;color:#667085">${esc(label)}</div><strong style="font-size:19px">${value}</strong></div>`;
}

function externalEffectsHtml(page: ExternalEffectsWorkspace): string {
  const runtime = page.runtime.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${esc(item.id)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.owner)} / ${esc(item.kind)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.state)} · ${item.attemptCount} attempts</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.state === 'accepted' ? `<button data-effect-cancel="${esc(item.id)}" style="${button}">取消本地 effect</button>` : '<span style="color:#8F959E;font-size:12px">无安全本地操作</span>'}</td></tr>`).join('') || '<tr><td colspan="4" style="padding:16px;color:#8F959E">没有可读的本地 effect runtime</td></tr>';
  const jobs = page.jobs.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${esc(item.id)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.status)} · ${esc(item.classification)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.attemptCount}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.updatedAt)}</td></tr>`).join('') || '<tr><td colspan="4" style="padding:16px;color:#8F959E">没有可读的本地 job</td></tr>';
  const pushSections = page.push.sections.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.label)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3;font-family:ui-monospace,Menlo,monospace">${esc(item.key)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.count}</td></tr>`).join('') || '<tr><td colspan="3" style="padding:16px;color:#8F959E">当前没有可用 section 投影</td></tr>';
  const pushJobs = page.push.jobs.map((item) => {
    const control = item.status === 'pending' ? `<button data-push-cancel="${item.jobID}" style="${button}">取消本地任务</button>` : item.status === 'retryable_failed' ? `<button data-push-retry="${item.jobID}" style="${button}">创建本地重试</button>` : item.status === 'outcome_unknown' ? '<span style="color:#B54708;font-size:12px">结果未知：人工确认，禁止重试</span>' : '<span style="color:#8F959E;font-size:12px">无安全本地操作</span>';
    return `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3"><button data-push-detail="${item.jobID}" style="border:0;background:transparent;color:#1849A9;cursor:pointer;font-family:ui-monospace,Menlo,monospace">#${item.jobID}</button></td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.status)} · ${esc(item.failureClass)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.attemptCount}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${control}</td></tr>`;
  }).join('') || '<tr><td colspan="4" style="padding:16px;color:#8F959E">没有可读的 Push Center job</td></tr>';
  const diagnostics = [countCard('accepted（本地）', page.runtimeDiagnostics.accepted), countCard('queued（本地）', page.runtimeDiagnostics.queued), countCard('attempted（本地）', page.runtimeDiagnostics.attempted), countCard('outcome_unknown', page.runtimeDiagnostics.outcomeUnknown), countCard('retryable_failed', page.runtimeDiagnostics.retryableFailed)].join('');
  const pushStats = page.push.counts ? `<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(105px,1fr));gap:8px">${[countCard('总数（本地）', page.push.counts.total), countCard('pending', page.push.counts.pending), countCard('running', page.push.counts.running), countCard('sent（本地状态）', page.push.counts.sent), countCard('failed', page.push.counts.failed)].join('')}</div>` : `<div style="padding:10px;border:1px solid #F5D6A7;border-radius:8px;background:#FFF9F0;color:#8F5A16;font-size:13px"><strong>Push Center degraded：</strong>${esc(page.push.message || '读模型暂不可用')}；不会把空计数视为成功或零任务。</div>`;
  const unknown = page.jobRisk.outcomeUnknown > 0 || page.runtimeDiagnostics.outcomeUnknown > 0 || page.push.jobs.some((item) => item.status === 'outcome_unknown');
  return shell('外部效果与 Push Center', `<div style="display:flex;gap:8px;flex-wrap:wrap"><button id="effects-back" style="${button}">返回 Campaign</button><button id="effects-refresh" style="${button}">刷新真实本地投影</button></div>${effectBoundary}${unknown ? '<div style="padding:10px;border:1px solid #F5D6A7;border-radius:8px;background:#FFF9F0;color:#8F5A16;font-size:13px"><strong>outcome_unknown 存在：</strong>需人工确认或独立对账；页面已禁用自动重试。</div>' : ''}<section style="display:grid;gap:10px"><div><h2 style="margin:0;font-size:16px">External Effects runtime / diagnostics</h2><p style="margin:4px 0 0;color:#667085;font-size:13px">只读本地投影；取消仅在 accepted、worker 未开始时可用。</p></div><div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:8px">${diagnostics}</div><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">effect</th><th style="padding:8px">owner / kind</th><th style="padding:8px">本地状态</th><th style="padding:8px">操作</th></tr></thead><tbody>${runtime}</tbody></table><h3 style="margin:0;font-size:14px">本地 job（诊断）</h3><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">job</th><th style="padding:8px">本地状态</th><th style="padding:8px">attempts</th><th style="padding:8px">更新时间</th></tr></thead><tbody>${jobs}</tbody></table>${effectBlocked}</section><section style="display:grid;gap:10px"><div><h2 style="margin:0;font-size:16px">Push Center</h2><p style="margin:4px 0 0;color:#667085;font-size:13px">仅读取 section、stats、job 与本地 reconciliation；不展示收件人或 Provider 成功。</p></div>${pushStats}<div style="display:grid;grid-template-columns:minmax(260px,1fr) minmax(360px,2fr);gap:12px"><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">section</th><th style="padding:8px">key</th><th style="padding:8px">count</th></tr></thead><tbody>${pushSections}</tbody></table><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">job</th><th style="padding:8px">本地状态</th><th style="padding:8px">attempts</th><th style="padding:8px">安全操作</th></tr></thead><tbody>${pushJobs}</tbody></table></div></section>`);
}

function pushDetailHtml(detail: PushCenterJobDetail): string {
  const attempts = detail.attempts.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3">${item.attempt}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.state)} · ${esc(item.failureClass)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.updatedAt)}</td></tr>`).join('') || '<tr><td colspan="3" style="padding:14px;color:#8F959E">暂无本地 attempt 记录</td></tr>';
  const receipts = detail.receipts.map((item) => `<tr><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.operation)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${status(item.taskStatus)}</td><td style="padding:8px;border-bottom:1px solid #EEF0F3">${esc(item.completedAt)}</td></tr>`).join('') || '<tr><td colspan="3" style="padding:14px;color:#8F959E">暂无本地控制回执</td></tr>';
  const control = detail.job.status === 'pending' ? `<button id="push-detail-cancel" style="${button}">取消本地任务</button>` : detail.job.status === 'retryable_failed' ? `<button id="push-detail-retry" style="${button}">创建本地重试</button>` : detail.job.status === 'outcome_unknown' ? '<span style="color:#B54708">结果未知：需人工确认，禁止重试</span>' : '<span style="color:#8F959E">无安全本地操作</span>';
  return shell('Push Center job 本地对账', `<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="push-detail-back" style="${button}">返回 Push Center</button><span style="font-family:ui-monospace,Menlo,monospace">#${detail.job.jobID}</span>${status(detail.job.status)}<span>${detail.job.attemptCount} attempts</span>${control}</div>${effectBoundary}<div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><section><h2 style="margin:0 0 8px;font-size:15px">本地 attempt</h2><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">次数</th><th style="padding:8px">状态</th><th style="padding:8px">记录时间</th></tr></thead><tbody>${attempts}</tbody></table></section><section><h2 style="margin:0 0 8px;font-size:15px">本地控制回执</h2><table style="width:100%;border-collapse:collapse"><thead><tr style="text-align:left;color:#667085"><th style="padding:8px">操作</th><th style="padding:8px">本地状态</th><th style="padding:8px">完成时间</th></tr></thead><tbody>${receipts}</tbody></table></section></div>`);
}

async function loadExternalEffects(stage: HTMLElement): Promise<void> {
  const page = await readExternalEffectsWorkspaceDto();
  stage.innerHTML = externalEffectsHtml(page);
  stage.querySelector<HTMLButtonElement>('#effects-back')?.addEventListener('click', () => goto());
  stage.querySelector<HTMLButtonElement>('#effects-refresh')?.addEventListener('click', () => void loadExternalEffects(stage).catch((error) => showError(stage, error)));
  stage.querySelectorAll<HTMLButtonElement>('[data-push-detail]').forEach((element) => element.addEventListener('click', () => gotoExternalEffects(Number(element.dataset.pushDetail))));
  stage.querySelectorAll<HTMLButtonElement>('[data-effect-cancel]').forEach((element) => element.addEventListener('click', () => confirmBox('取消本地 external effect', '仅允许取消尚未尝试的 accepted 本地 effect；不会撤回任何外部结果。', '确认取消', true, () => {
    void cancelExternalEffectRuntimeDto(element.dataset.effectCancel || '').then(() => { toast('已取消本地 effect；不代表外部撤回或送达状态'); void loadExternalEffects(stage); }).catch((error) => toast(error instanceof Error ? error.message : '本地 effect 取消失败', true));
  })));
  stage.querySelectorAll<HTMLButtonElement>('[data-push-cancel]').forEach((element) => element.addEventListener('click', () => confirmBox('取消本地 Push Center job', '仅允许取消 pending 本地任务；不会调用 Provider，也不会撤回任何外部结果。', '确认取消', true, () => {
    void cancelPushCenterJobDto(Number(element.dataset.pushCancel)).then(() => { toast('已受理本地取消；不等于 Provider 调用、发送或送达'); void loadExternalEffects(stage); }).catch((error) => toast(error instanceof Error ? error.message : 'Push Center 取消失败', true));
  })));
  stage.querySelectorAll<HTMLButtonElement>('[data-push-retry]').forEach((element) => element.addEventListener('click', () => confirmBox('创建本地 Push Center 重试', '只会为 retryable_failed 创建下一代本地任务；不调用 Provider、不证明发送或送达。outcome_unknown 不允许重试。', '确认创建', false, () => {
    void retryPushCenterJobDto(Number(element.dataset.pushRetry)).then(() => { toast('已受理本地重试；不等于 Provider 调用、发送或送达'); void loadExternalEffects(stage); }).catch((error) => toast(error instanceof Error ? error.message : 'Push Center 重试失败', true));
  })));
}

async function loadPushCenterDetail(stage: HTMLElement, jobID: number): Promise<void> {
  const detail = await getPushCenterJobDetailDto(jobID);
  stage.innerHTML = pushDetailHtml(detail);
  stage.querySelector<HTMLButtonElement>('#push-detail-back')?.addEventListener('click', () => gotoExternalEffects());
  stage.querySelector<HTMLButtonElement>('#push-detail-cancel')?.addEventListener('click', () => confirmBox('取消本地 Push Center job', '仅允许取消 pending 本地任务；不会调用 Provider，也不会撤回任何外部结果。', '确认取消', true, () => {
    void cancelPushCenterJobDto(jobID).then(() => { toast('已受理本地取消；不等于 Provider 调用、发送或送达'); gotoExternalEffects(jobID); }).catch((error) => toast(error instanceof Error ? error.message : 'Push Center 取消失败', true));
  }));
  stage.querySelector<HTMLButtonElement>('#push-detail-retry')?.addEventListener('click', () => confirmBox('创建本地 Push Center 重试', '只会为 retryable_failed 创建下一代本地任务；outcome_unknown 不允许重试。', '确认创建', false, () => {
    void retryPushCenterJobDto(jobID).then(() => { toast('已受理本地重试；不等于 Provider 调用、发送或送达'); gotoExternalEffects(jobID); }).catch((error) => toast(error instanceof Error ? error.message : 'Push Center 重试失败', true));
  }));
}

async function loadList(stage: HTMLElement, filter: CampaignFilter = {}): Promise<void> {
  const rows = await listCampaignsDto(filter);
  stage.innerHTML = listHtml(rows.map((item) => ({ ...item, steps: [] })), filter);
  stage.querySelectorAll<HTMLButtonElement>('[data-campaign]').forEach((element) => element.addEventListener('click', () => goto(element.dataset.campaign)));
  stage.querySelector<HTMLButtonElement>('#external-effects-open')?.addEventListener('click', () => gotoExternalEffects());
  stage.querySelector<HTMLButtonElement>('#observability-open')?.addEventListener('click', gotoObservability);
  stage.querySelector<HTMLButtonElement>('#campaign-refresh')?.addEventListener('click', () => {
    const approval = (stage.querySelector<HTMLSelectElement>('#campaign-approval')?.value || '') as CampaignFilter['approvalStatus'] | '';
    const runtime = (stage.querySelector<HTMLSelectElement>('#campaign-runtime')?.value || '') as CampaignFilter['runtimeStatus'] | '';
    void loadList(stage, { approvalStatus: approval || undefined, runtimeStatus: runtime || undefined }).catch((error) => showError(stage, error));
  });
}

function renderPlanIndex(stage: HTMLElement, page: CampaignTouchPlanIndexPage, reviewStatus: CampaignTouchPlanIndexItem['reviewStatus'] | '' = ''): void {
  stage.innerHTML = planIndexHtml(page, reviewStatus);
  stage.querySelectorAll<HTMLButtonElement>('[data-plan][data-campaign]').forEach((element) => element.addEventListener('click', () => goto(element.dataset.campaign, element.dataset.plan)));
  stage.querySelector<HTMLButtonElement>('#plan-index-refresh')?.addEventListener('click', () => {
    const nextStatus = stage.querySelector<HTMLSelectElement>('#plan-index-status')?.value as CampaignTouchPlanIndexItem['reviewStatus'] | '';
    void loadPlanIndex(stage, nextStatus || undefined).catch((error) => showError(stage, error));
  });
  stage.querySelector<HTMLButtonElement>('#plan-index-more')?.addEventListener('click', () => {
    void listCampaignPlanIndexDto(reviewStatus || undefined, page.nextCursor || undefined)
      .then((more) => renderPlanIndex(stage, { items: [...page.items, ...more.items], nextCursor: more.nextCursor }, reviewStatus))
      .catch((error) => showError(stage, error));
  });
}

async function loadPlanIndex(stage: HTMLElement, reviewStatus?: CampaignTouchPlanIndexItem['reviewStatus']): Promise<void> {
  renderPlanIndex(stage, await listCampaignPlanIndexDto(reviewStatus), reviewStatus || '');
}

function showError(stage: HTMLElement, error: unknown): void {
  stage.innerHTML = shell('Campaign 本地工作区', `<div style="padding:14px;border:1px solid #F2B8B5;border-radius:8px;background:#FFF1F0;color:#B42318">${esc(error instanceof Error ? error.message : '读取失败')}</div><button id="campaign-retry" style="${button}">返回并重试</button>`);
  stage.querySelector<HTMLButtonElement>('#campaign-retry')?.addEventListener('click', () => goto());
}

async function loadCampaign(stage: HTMLElement, campaignCode: string, planID?: string, customerID?: number): Promise<void> {
  const [campaign, plans] = await Promise.all([getCampaignDto(campaignCode), listCampaignTouchPlansDto(campaignCode)]);
  if (!planID) {
    stage.innerHTML = campaignHtml({ campaign, plans });
    stage.querySelector<HTMLButtonElement>('#campaign-back')?.addEventListener('click', () => goto());
    stage.querySelectorAll<HTMLButtonElement>('[data-plan]').forEach((element) => element.addEventListener('click', () => goto(campaignCode, element.dataset.plan)));
    stage.querySelector<HTMLButtonElement>('#campaign-members-open')?.addEventListener('click', () => void loadCampaignMembers(stage, campaignCode, 0));
    stage.querySelector<HTMLButtonElement>('#campaign-delete')?.addEventListener('click', () => confirmBox('删除本地 Campaign', `确认删除「${campaign.name}」？仅当服务端允许草稿且空闲时才会删除；不会清除任何外部历史任务。`, '确认删除', true, () => {
      void deleteCampaignDto(campaignCode).then(() => { toast('本地 Campaign 已删除'); goto(); }).catch((error) => toast(error instanceof Error ? error.message : 'Campaign 删除失败', true));
    }));
    return;
  }
  const [plan, review, recipientPage] = await Promise.all([getCampaignTouchPlanDto(campaignCode, planID), getCampaignTouchPlanReviewDto(campaignCode, planID), listCampaignTouchPlanRecipientsDto(campaignCode, planID)]);
  const [recipient, recipientReview] = customerID == null ? [undefined, undefined] as const : await Promise.all([getCampaignTouchPlanRecipientDto(campaignCode, planID, customerID), getCampaignTouchPlanRecipientReviewDto(campaignCode, planID, customerID)] as const);
  const handoff = review.handoffStatus ? await tryGetCampaignOutboundHandoffDto(campaignCode, planID) : null;
  const [handoffReconciliation, dispatchReconciliation] = handoff ? await Promise.all([getCampaignOutboundHandoffReconciliationDto(campaignCode, planID), tryGetCampaignOutboundDispatchReconciliationDto(campaignCode, planID)]) : [undefined, null] as const;
  const renderPlan = (nextPage: CampaignTouchPlanRecipientPage): void => {
    stage.innerHTML = planHtml({ campaign, plans, plan, review, recipientPage: nextPage, recipient, recipientReview, handoff, handoffReconciliation, dispatchReconciliation });
    stage.querySelector<HTMLButtonElement>('#plan-back')?.addEventListener('click', () => goto(campaignCode));
    stage.querySelectorAll<HTMLButtonElement>('[data-recipient]').forEach((element) => element.addEventListener('click', () => goto(campaignCode, planID, Number(element.dataset.recipient))));
    stage.querySelector<HTMLButtonElement>('#recipient-more')?.addEventListener('click', () => {
      void listCampaignTouchPlanRecipientsDto(campaignCode, planID, nextPage.nextCursor || undefined).then((more) => renderPlan({ items: [...nextPage.items, ...more.items], nextCursor: more.nextCursor })).catch((error) => toast(error instanceof Error ? error.message : '目标人员读取失败', true));
    });
    (['approve', 'reject'] as const).forEach((operation) => stage.querySelector<HTMLButtonElement>(`#plan-${operation}`)?.addEventListener('click', () => confirmBox(`${operation === 'approve' ? '批准' : '拒绝'}本地审核`, '该操作仅写入本地 review，不会调用 Provider 或发送消息。', `确认${operation === 'approve' ? '批准' : '拒绝'}`, operation === 'reject', () => {
      void decideCampaignTouchPlanReviewDto(campaignCode, planID, operation).then(() => { toast('本地审核状态已更新'); goto(campaignCode, planID); }).catch((error) => toast(error instanceof Error ? error.message : '本地审核失败', true));
    })));
    stage.querySelector<HTMLButtonElement>('#recipient-message-save')?.addEventListener('click', () => {
      const message = stage.querySelector<HTMLTextAreaElement>('#recipient-message')?.value || '';
      void saveCampaignTouchPlanRecipientMessageDto(campaignCode, planID, customerID!, message).then(() => { toast('单客户本地消息已保存'); goto(campaignCode, planID, customerID); }).catch((error) => toast(error instanceof Error ? error.message : '单客户消息保存失败', true));
    });
    (['approve', 'reject'] as const).forEach((operation) => stage.querySelector<HTMLButtonElement>(`#recipient-review-${operation}`)?.addEventListener('click', () => confirmBox(`${operation === 'approve' ? '批准' : '拒绝'}该客户`, '该操作仅写入当前 touch plan 的本地单客户 review，不会发送消息。', `确认${operation === 'approve' ? '批准' : '拒绝'}`, operation === 'reject', () => {
      void decideCampaignTouchPlanRecipientReviewDto(campaignCode, planID, customerID!, operation).then(() => { toast('单客户本地审核已更新'); goto(campaignCode, planID, customerID); }).catch((error) => toast(error instanceof Error ? error.message : '单客户审核失败', true));
    })));
    stage.querySelector<HTMLButtonElement>('#recipient-dispatch')?.addEventListener('click', () => confirmBox('受控发送该客户', '只为该客户创建 gated 本地 EER 工作。accepted/queued 不等于 Provider 发送或送达；外部开关仍由服务端控制。', '确认受理', false, () => {
      void dispatchCampaignOutboundRecipientDto(campaignCode, planID, customerID!).then((result) => { toast(`该客户动作已受理；handoff 本地汇总 accepted ${result.accepted} / queued ${result.queued}，business=${result.businessCallDispatched} / real=${result.realExternalCallExecuted}；不等于送达`); goto(campaignCode, planID, customerID); }).catch((error) => toast(error instanceof Error ? error.message : '单客户受控发送失败', true));
    }));
    stage.querySelector<HTMLButtonElement>('#handoff-refresh')?.addEventListener('click', () => goto(campaignCode, planID, customerID));
    stage.querySelector<HTMLButtonElement>('#handoff-accept')?.addEventListener('click', () => confirmBox('二次确认受理 handoff', '确认后只保存 held 本地事实并创建内部事件工作；不会调用 Provider、发送消息或证明送达。', '确认受理', false, () => {
      void acceptCampaignOutboundHandoffDto(campaignCode, planID).then(() => { toast('Campaign handoff 已受理为本地 held 事实；不等于发送或送达'); goto(campaignCode, planID, customerID); }).catch((error) => toast(error instanceof Error ? error.message : 'Campaign handoff 受理失败', true));
    }));
    stage.querySelector<HTMLButtonElement>('#handoff-dispatch')?.addEventListener('click', () => confirmBox('确认排入本地 EER', '确认后仅排入 gated 本地 EER 工作。queued/accepted 不等于发送或送达；结果未知必须人工复核。', '确认排入', false, () => {
      void dispatchCampaignOutboundHandoffDto(campaignCode, planID).then(() => { toast('已排入本地 EER；不等于发送或送达'); goto(campaignCode, planID, customerID); }).catch((error) => toast(error instanceof Error ? error.message : 'Campaign handoff 排队失败', true));
    }));
  };
  renderPlan(recipientPage);
}

export async function mountCampaignWorkspace(stage: HTMLElement): Promise<void> {
  const params = query();
  if (params.get('definition_history') === '1') return mountCampaignDefinitionHistory(stage);
  if (params.get('legacy_admin_path') === '/admin/cloud-orchestrator/plans') return loadPlanIndex(stage);
  if (params.get('view') === 'observability') return mountPushObservability(stage);
  if (params.get('view') === 'external-effects') {
    const rawJobID = params.get('job');
    if (!rawJobID) return loadExternalEffects(stage);
    const jobID = Number(rawJobID);
    if (!Number.isSafeInteger(jobID) || jobID <= 0) throw new Error('Push Center job 范围无效，已拒绝读取');
    return loadPushCenterDetail(stage, jobID);
  }
  const campaignCode = params.get('campaign') || undefined;
  const planID = params.get('plan') || undefined;
  const rawCustomerID = Number(params.get('recipient') || '');
  const customerID = Number.isSafeInteger(rawCustomerID) && rawCustomerID > 0 ? rawCustomerID : undefined;
  if (planID && !campaignCode) throw new Error('缺少 Campaign code，拒绝读取未限定范围的 touch plan');
  if (campaignCode) return loadCampaign(stage, campaignCode, planID, customerID);
  return loadList(stage);
}
