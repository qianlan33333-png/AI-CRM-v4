/**
 * AI 助手 · 运营计划审阅 —— 富交互模块（方向稿 ai-assistant.html 的 TypeScript 移植）
 * 视图：
 *  - ai.html        计划列表（搜索 / 状态筛选 + 4 统计卡）
 *  - aiDetail.html  计划详情（信息 KV + 人员表分页加载 + 整单审批 + 人员抽屉三级浮窗）
 * 状态机与生产一致：pending_review → approved / rejected；人员 pending 级联。
 */
import type { AdminApi } from '../../shared/api/client';
import type { AiPlan, AiPlanStatus, AiRecipient, AiRecipientStatus, Tone } from '../../shared/api/types';
import { toast, confirmBox } from '../../shared/ui/feedback';
import { esc } from './util';

export interface AiMountOpts {
  view: 'list' | 'detail';
  id?: number;
}

const STATUS: Record<AiPlanStatus, [string, Tone]> = {
  pending_review: ['待审批', 'warn'],
  approved: ['已批准', 'ok'],
  rejected: ['已拒绝', 'red'],
  active: ['执行中', 'blue'],
};
const RC_STATUS: Record<AiRecipientStatus, [string, Tone]> = {
  pending: ['待审阅', 'warn'],
  approved: ['已批准', 'ok'],
  rejected: ['已拒绝', 'red'],
  sent: ['已发送', 'blue'],
  failed: ['发送失败', 'red'],
};

export async function mountAiAssistant(root: HTMLElement, api: AdminApi, opts: AiMountOpts): Promise<void> {
  root.className = 'labs sec-ai';
  if (api.mode === 'http') {
    renderBlocked(root, opts);
    return;
  }
  const db = await api.loadDb({ page: opts.view === 'list' ? 'ai' : 'aiDetail', id: opts.id == null ? undefined : String(opts.id) });
  if (opts.view === 'list') renderList(root, db.aiPlans);
  else void renderDetail(root, api, db.aiPlans, opts.id);
}

function renderBlocked(root: HTMLElement, opts: AiMountOpts): void {
  const detail = opts.view === 'detail';
  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <b>AI 助手${detail ? ' / 计划详情' : ''}</b></div>
    <div class="page-head"><div><div class="page-title">AI 助手 · 运营计划审阅</div><div class="page-desc">保留原审阅工作区；仅在 DTO 语义可证明等价后开放操作</div></div></div>
    <div class="card" data-backend-blocked style="margin-bottom:14px;padding:14px 16px;border-color:#F5D6A7;background:#FFF9F0;color:#8A4B08;font-size:13px;line-height:20px">
      <strong style="display:block;color:#1F2329;margin-bottom:4px">后端能力未就绪</strong>
      当前 AI 审阅壳的 ai-assist DTO 与 Cloud Orchestrator campaigns / touch-plans / review 契约不等价。页面未读取演示计划、未发送请求，也未开放审批。
    </div>
    ${detail ? `
      <div class="card" style="padding:18px"><div class="muted">计划详情</div><h2 style="margin:8px 0 4px;font-size:18px">无法读取该计划</h2><p class="muted" style="margin:0">缺少可验证的计划与收件人映射。</p><a href="ai.html" class="btn" style="display:inline-flex;margin-top:14px;text-decoration:none">返回计划列表</a></div>
    ` : `
      <div class="card toolbar"><input class="input" disabled placeholder="搜索计划名称、发送人" style="width:280px"><select class="select" disabled><option>全部状态</option></select><button class="btn" disabled>刷新</button></div>
      <div class="stat-row"><div class="card stat"><div class="stat-l">待审批计划</div><div class="stat-v">—</div></div><div class="card stat"><div class="stat-l">预计触达</div><div class="stat-v">—</div></div><div class="card stat"><div class="stat-l">执行中计划</div><div class="stat-v">—</div></div><div class="card stat"><div class="stat-l">人员</div><div class="stat-v">—</div></div></div>
      <div class="plan-head-row"><div>计划名称</div><div>创建人 / 发送人</div><div>更新时间</div><div>目标人数</div><div>状态</div><div>查看详情</div></div><div class="card" style="padding:40px;text-align:center;color:#8F959E">没有可安全展示的计划</div>
    `}`;
}

/* ================= 计划列表 ================= */
function renderList(root: HTMLElement, plans: AiPlan[]): void {
  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <b>AI 助手</b></div>
    <div class="page-head">
      <div><div class="page-title">AI 助手 · 运营计划审阅</div><div class="page-desc">Agent 生成的群发计划在此审阅，确认后按发送人派发执行 · 逻辑与生产环境一致</div></div>
    </div>
    <div class="card toolbar">
      <input class="input" id="fKeyword" placeholder="搜索计划名称、发送人" style="width:280px">
      <select class="select" id="fStatus"><option value="">全部状态</option><option value="pending_review">待审批</option><option value="approved">已批准</option><option value="rejected">已拒绝</option><option value="active">执行中</option></select>
      <button class="btn" id="fRefresh">刷新</button>
    </div>
    <div class="stat-row">
      <div class="card stat"><div class="stat-l">待审批计划</div><div class="stat-v" id="stPending">0</div></div>
      <div class="card stat"><div class="stat-l">今日预计触达</div><div class="stat-v" id="stTouch">0</div></div>
      <div class="card stat"><div class="stat-l">执行中计划</div><div class="stat-v" id="stActive">0</div></div>
      <div class="card stat"><div class="stat-l">一级页加载人员</div><div class="stat-v">0 <span style="font-size:13px;font-weight:400;color:#8F959E">人</span></div></div>
    </div>
    <div class="plan-head-row"><div>计划名称</div><div>创建人 / 发送人</div><div>更新时间</div><div>目标人数</div><div>状态</div><div>查看详情</div></div>
    <div id="planList"></div>`;

  const $ = <T extends HTMLElement>(s: string): T => root.querySelector(s) as T;

  function paint(): void {
    const kw = ($('#fKeyword') as HTMLInputElement).value.trim().toLowerCase();
    const st = ($('#fStatus') as HTMLSelectElement).value;
    const list = plans.filter(
      (p) => (!st || p.status === st) && (!kw || p.name.toLowerCase().includes(kw) || p.owner.toLowerCase().includes(kw)),
    );
    $('#planList').innerHTML = list.length
      ? list
          .map(
            (p) => `
      <div class="plan-row" data-open="${p.id}">
        <div><div class="plan-name">${esc(p.name)}</div><div class="plan-code">${esc(p.code)}</div></div>
        <div>${esc(p.creator)}</div>
        <div class="muted">${esc(p.updated)}</div>
        <div class="num">${p.target.toLocaleString()}</div>
        <div><span class="chip ${STATUS[p.status][1]}">${STATUS[p.status][0]}</span></div>
        <div><button class="btn sm" data-open="${p.id}">查看详情</button></div>
      </div>`,
          )
          .join('')
      : `<div class="card" style="padding:40px;text-align:center;color:#8F959E">没有匹配的计划</div>`;
    $('#stPending').textContent = String(plans.filter((p) => p.status === 'pending_review').length);
    $('#stTouch').textContent = plans
      .filter((p) => p.status === 'pending_review' || p.status === 'active')
      .reduce((a, p) => a + p.target, 0)
      .toLocaleString();
    $('#stActive').textContent = String(plans.filter((p) => p.status === 'active').length);
  }

  ['#fKeyword', '#fStatus'].forEach((s) => $(s).addEventListener('input', paint));
  $('#fRefresh').addEventListener('click', () => {
    paint();
    toast('已刷新');
  });
  root.addEventListener('click', (e) => {
    const op = (e.target as HTMLElement).closest('[data-open]') as HTMLElement | null;
    if (op) location.href = 'aiDetail.html?id=' + op.dataset.open;
  });

  paint();
}

/* ================= 计划详情 ================= */
async function renderDetail(root: HTMLElement, api: AdminApi, plans: AiPlan[], id?: number): Promise<void> {
  const plan = plans.find((x) => x.id === id) || plans[0];
  if (!plan) {
    root.innerHTML = '<div class="card" style="padding:40px;text-align:center;color:#8F959E">计划不存在</div>';
    return;
  }
  const recipients = await api.listAiRecipients(plan.id);
  let shown = 50;
  let drawerRc: AiRecipient | null = null;

  root.innerHTML = `
    <div class="crumb">客户管理后台 / 运营 / <a href="ai.html">AI 助手</a> / <b>${esc(plan.name)}</b></div>
    <div class="page-head"><div class="page-title">计划详情</div></div>

    <div class="card detail-head">
      <div style="display:flex;align-items:center;gap:10px"><span class="muted">当前状态</span><span class="chip warn" id="dStatus">待审批</span><span class="muted" id="dHint" style="font-size:12px"></span></div>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="btn" id="dBack">返回一级页</button>
        <button class="btn danger" id="dReject">拒绝计划</button>
        <button class="btn primary" id="dApprove">确认并发送</button>
      </div>
    </div>

    <div class="detail-layout">
      <aside class="card panel">
        <h2>计划信息</h2>
        <div class="kv"><div class="kv-l">计划名称</div><div class="kv-v">${esc(plan.name)}</div></div>
        <div class="kv"><div class="kv-l">计划编号</div><div class="kv-v mono">${esc(plan.code)}</div></div>
        <div class="kv"><div class="kv-l">发送人</div><div class="kv-v">${esc(plan.owner)}</div></div>
        <div class="kv"><div class="kv-l">更新时间</div><div class="kv-v">${esc(plan.updated)}</div></div>
        <div class="kv"><div class="kv-l">目标人数</div><div class="kv-v">${plan.target.toLocaleString()}</div></div>
        <div class="kv"><div class="kv-l">状态</div><div class="kv-v" id="kvStatus">--</div></div>
      </aside>

      <div class="card">
        <div class="panel" style="padding-bottom:0"><h2>目标人员</h2></div>
        <div style="overflow-x:auto">
        <table class="tbl" style="min-width:720px">
          <thead><tr><th>目标人员</th><th>发送人</th><th>更新时间</th><th>话术次数</th><th>发送状态</th><th>操作</th></tr></thead>
          <tbody id="rcRows"></tbody>
        </table>
        </div>
        <div class="loadbar">
          <span class="muted" id="rcLoaded">已加载 0 / 0 人</span>
          <div class="progress"><span id="rcBar" style="width:0%"></span></div>
          <button class="btn" id="rcMore">继续加载 50 人</button>
        </div>
      </div>
    </div>

    <div class="drawer-mask" id="drawerMask"></div>
    <aside class="drawer" id="drawer">
      <div class="drawer-head">
        <div><div class="drawer-title" id="dwName">人员详情</div><div class="muted" id="dwSub" style="margin-top:4px;font-size:12px">—</div></div>
        <button class="btn sm" id="dwClose">关闭</button>
      </div>
      <div class="drawer-body">
        <div style="display:flex;gap:8px">
          <button class="btn primary" id="dwApprove">批准这个人发送</button>
          <button class="btn danger" id="dwReject">拒绝这个人</button>
        </div>
        <div class="card panel" style="margin-top:14px">
          <div class="kv"><div class="kv-l">目标人员名称</div><div class="kv-v" id="dwTarget">--</div></div>
          <div class="kv"><div class="kv-l">external_userid</div><div class="kv-v mono" id="dwExt">--</div></div>
          <div class="kv"><div class="kv-l">发送人</div><div class="kv-v" id="dwOwner">--</div></div>
          <div class="kv"><div class="kv-l">共几次话术任务</div><div class="kv-v" id="dwCount">--</div></div>
        </div>
        <div id="dwTasks"></div>
      </div>
    </aside>`;

  const $ = <T extends HTMLElement>(s: string): T => root.querySelector(s) as T;

  function syncStatus(): void {
    const [label, tone] = STATUS[plan.status];
    const chip = $('#dStatus');
    chip.textContent = label;
    chip.className = 'chip ' + tone;
    $('#kvStatus').textContent = label;
    const lock = plan.status === 'approved' || plan.status === 'rejected';
    const ap = $('#dApprove') as HTMLButtonElement;
    const rj = $('#dReject') as HTMLButtonElement;
    ap.disabled = lock;
    rj.disabled = lock;
    ap.style.opacity = lock ? '.55' : '1';
    rj.style.opacity = lock ? '.55' : '1';
    $('#dHint').textContent =
      plan.status === 'approved'
        ? '· 已批准，等待派发执行'
        : plan.status === 'rejected'
          ? '· 已拒绝，不会发送'
          : plan.status === 'active'
            ? '· 正在按发送人派发执行'
            : '· 审阅通过后将按计划派发';
  }

  function paintRows(): void {
    const all = recipients;
    const list = all.slice(0, shown);
    $('#rcRows').innerHTML = list
      .map(
        (r) => `
      <tr data-rc="${r.id}">
        <td><b style="font-weight:600">${esc(r.name)}</b><div class="mono" style="margin-top:2px">${esc(r.external_userid)}</div></td>
        <td>${esc(r.owner)}</td>
        <td class="muted">${esc(r.updated)}</td>
        <td class="num">${r.taskCount}</td>
        <td><span class="chip ${RC_STATUS[r.status][1]}">${RC_STATUS[r.status][0]}</span></td>
        <td><button class="link-btn btn sm" data-rc="${r.id}">详情</button></td>
      </tr>`,
      )
      .join('');
    $('#rcLoaded').textContent = `已加载 ${list.length} / ${all.length} 人`;
    ($('#rcBar') as HTMLElement).style.width = (list.length / Math.max(1, all.length)) * 100 + '%';
    const more = $('#rcMore') as HTMLButtonElement;
    more.disabled = list.length >= all.length;
    more.textContent = list.length >= all.length ? '已全部加载' : '继续加载 50 人';
  }

  /* ---------- 抽屉 ---------- */
  function openDrawer(rcId: number): void {
    const r = recipients.find((x) => x.id === rcId);
    if (!r) return;
    drawerRc = r;
    $('#dwName').textContent = r.name;
    $('#dwSub').textContent = '目标人员 · ' + RC_STATUS[r.status][0];
    $('#dwTarget').textContent = r.name;
    $('#dwExt').textContent = r.external_userid;
    $('#dwOwner').textContent = r.owner;
    $('#dwCount').textContent = r.taskCount + ' 次';
    $('#dwTasks').innerHTML = r.tasks
      .map(
        (t, i) => `
      <div class="task">
        <div class="task-head"><b style="font-size:13px">话术任务 ${t.no} · ${esc(t.kind)}</b><span class="chip ${RC_STATUS[r.status][1]}">${RC_STATUS[r.status][0]}</span></div>
        <div class="task-text">${esc(t.text)}</div>
        <div class="task-media">${t.media.map((m) => `<div>· ${esc(m)}</div>`).join('')}</div>
        <div class="task-note"><textarea placeholder="编辑备注（仅审阅可见，不影响话术内容）" data-note="${r.id}_${i}">${esc(t.note)}</textarea></div>
      </div>`,
      )
      .join('');
    const lock = r.status !== 'pending';
    const ap = $('#dwApprove') as HTMLButtonElement;
    const rj = $('#dwReject') as HTMLButtonElement;
    ap.disabled = lock;
    rj.disabled = lock;
    ap.style.opacity = lock ? '.55' : '1';
    rj.style.opacity = lock ? '.55' : '1';
    $('#drawer').classList.add('open');
    $('#drawerMask').classList.add('open');
  }
  function closeDrawer(): void {
    $('#drawer').classList.remove('open');
    $('#drawerMask').classList.remove('open');
  }

  /* ---------- 事件 ---------- */
  $('#rcMore').addEventListener('click', () => {
    shown += 50;
    paintRows();
  });
  $('#dBack').addEventListener('click', () => {
    location.href = 'ai.html';
  });
  $('#dApprove').addEventListener('click', () => {
    if (plan.status === 'approved' || plan.status === 'rejected') return;
    confirmBox(
      '确认并发送',
      `计划「${plan.name}」将派发给 ${plan.target.toLocaleString()} 名目标人员。\n发送人：${plan.owner}\n确认后立即生效。`,
      '确认并发送',
      false,
      () => {
        void api.approveAiPlan(plan.id).then(() => {
          plan.status = 'approved';
          recipients.forEach((r) => {
            if (r.status === 'pending') r.status = 'approved';
          });
          syncStatus();
          paintRows();
          toast('计划已批准，等待派发');
        });
      },
    );
  });
  $('#dReject').addEventListener('click', () => {
    if (plan.status === 'approved' || plan.status === 'rejected') return;
    confirmBox('拒绝计划', `计划「${plan.name}」将被拒绝，不会发送任何消息。`, '确认拒绝', true, () => {
      void api.rejectAiPlan(plan.id).then(() => {
        plan.status = 'rejected';
        recipients.forEach((r) => {
          if (r.status === 'pending') r.status = 'rejected';
        });
        syncStatus();
        paintRows();
        toast('计划已拒绝');
      });
    });
  });
  $('#dwClose').addEventListener('click', closeDrawer);
  $('#drawerMask').addEventListener('click', closeDrawer);
  $('#dwApprove').addEventListener('click', () => {
    const r = drawerRc;
    if (!r || r.status !== 'pending') return;
    void api.approveAiRecipient(plan.id, r.id).then(() => {
      r.status = 'approved';
      paintRows();
      openDrawer(r.id);
      toast('已批准这个人发送');
    });
  });
  $('#dwReject').addEventListener('click', () => {
    const r = drawerRc;
    if (!r || r.status !== 'pending') return;
    void api.rejectAiRecipient(plan.id, r.id).then(() => {
      r.status = 'rejected';
      paintRows();
      openDrawer(r.id);
      toast('已拒绝这个人');
    });
  });

  // 备注实时写回
  root.addEventListener('input', (e) => {
    const ta = (e.target as HTMLElement).closest('[data-note]') as HTMLTextAreaElement | null;
    if (!ta) return;
    const [rc, i] = ta.dataset.note!.split('_');
    void api.updateRecipientNote(plan.id, Number(rc), Number(i), ta.value);
    const r = recipients.find((x) => x.id === Number(rc));
    if (r) r.tasks[Number(i)].note = ta.value;
  });
  root.addEventListener('click', (e) => {
    const rc = (e.target as HTMLElement).closest('[data-rc]') as HTMLElement | null;
    if (rc) openDrawer(Number(rc.dataset.rc));
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeDrawer();
  });

  syncStatus();
  paintRows();
}
