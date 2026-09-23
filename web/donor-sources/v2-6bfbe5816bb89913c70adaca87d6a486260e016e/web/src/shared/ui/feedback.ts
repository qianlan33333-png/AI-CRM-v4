/**
 * 全局交互反馈层（TypeScript 版）
 * 合并两处来源：
 *  - 线上 admin_feedback.js：toast / 确认浮窗 / 提交 busy / 文件选择与上传提示的视觉
 *  - 未被控制器接管的业务按钮不会补模拟成功；它们必须由对应 Adapter 绑定。
 *
 * 原则：只补反馈，不改变既有业务流程；__dcBound（运行时已绑定）按钮自动跳过。
 */

const BOUND_MARK = '__dcBound';

interface FbElement extends HTMLElement {
  __dcBound?: boolean;
  __fbBusy?: boolean;
}

let installed = false;
let fbTimer: ReturnType<typeof setTimeout> | null = null;

/* ---------- 样式与 DOM ---------- */
function ensureUI(): void {
  if (document.getElementById('fb-toast')) return;
  const mk = (html: string): Element => {
    const d = document.createElement('div');
    d.innerHTML = html;
    return d.firstElementChild as Element;
  };
  document.body.appendChild(mk('<div id="fb-toast" hidden></div>'));
  document.body.appendChild(
    mk(`<div id="fb-mask" hidden><div id="fb-panel">
      <div id="fb-head"></div><div id="fb-body"></div>
      <div id="fb-foot"><button class="fb-btn" id="fb-cancel">取消</button><button class="fb-btn primary" id="fb-ok">确认</button></div>
    </div></div>`),
  );
  document.body.appendChild(
    mk(`<div id="fb-prog-mask" hidden><div id="fb-prog">
      <div id="fb-prog-title">正在上传</div>
      <progress id="fb-prog-bar" max="100" value="0"></progress>
      <div id="fb-prog-pct">0%</div>
    </div></div>`),
  );
  document.getElementById('fb-mask')!.addEventListener('click', (e) => {
    if ((e.target as HTMLElement).id === 'fb-mask') hideConfirm();
  });
  document.getElementById('fb-cancel')!.addEventListener('click', () => hideConfirm());
}

/* ---------- Toast ---------- */
export function toast(msg: string, err = false): void {
  ensureUI();
  const t = document.getElementById('fb-toast')!;
  t.textContent = msg;
  t.className = err ? 'err' : '';
  t.hidden = false;
  if (fbTimer) clearTimeout(fbTimer);
  fbTimer = setTimeout(() => {
    t.hidden = true;
  }, 2400);
}

/* ---------- 确认浮窗（Promise 版） ---------- */
let onOkCb: (() => void) | null = null;

function hideConfirm(): void {
  document.getElementById('fb-mask')!.hidden = true;
  onOkCb = null;
}

export function confirmBox(
  title: string,
  body: string,
  okLabel = '确认',
  danger = false,
  onOk?: () => void,
): void {
  ensureUI();
  document.getElementById('fb-head')!.textContent = title;
  document.getElementById('fb-body')!.textContent = body;
  const ok = document.getElementById('fb-ok') as HTMLButtonElement;
  ok.textContent = okLabel;
  ok.className = 'fb-btn ' + (danger ? 'danger' : 'primary');
  onOkCb = onOk || null;
  ok.onclick = () => {
    const cb = onOkCb;
    hideConfirm();
    if (cb) cb();
  };
  document.getElementById('fb-mask')!.hidden = false;
}

/* ---------- 按钮 busy ---------- */
export function busy(btn: FbElement, ms: number, done?: () => void): void {
  if (btn.__fbBusy) return;
  btn.__fbBusy = true;
  const old = btn.textContent || '';
  btn.classList.add('fb-busy');
  btn.textContent = '⏳ ' + old;
  setTimeout(() => {
    btn.classList.remove('fb-busy');
    btn.textContent = old;
    btn.__fbBusy = false;
    if (done) done();
  }, ms);
}

/* ---------- 模拟上传进度浮窗（预览环境无真实后端） ---------- */
export function simulateUpload(label?: string, onDone?: () => void): void {
  ensureUI();
  const mask = document.getElementById('fb-prog-mask')!;
  const bar = document.getElementById('fb-prog-bar') as HTMLProgressElement;
  const pct = document.getElementById('fb-prog-pct')!;
  document.getElementById('fb-prog-title')!.textContent = '正在上传 · ' + (label || '文件');
  mask.hidden = false;
  let p = 0;
  const tick = setInterval(() => {
    p = Math.min(100, p + 9 + Math.random() * 12);
    bar.value = p;
    pct.textContent = Math.floor(p) + '%';
    if (p >= 100) {
      clearInterval(tick);
      setTimeout(() => {
        mask.hidden = true;
        bar.value = 0;
        toast('上传完成');
        if (onDone) onDone();
      }, 320);
    }
  }, 150);
}

const BUSINESS_ACTION_RE = /删除|下架|停用|禁用|拒绝|驳回|上传|选择文件|更换图片|更换文件|导出|下载|保存|提交|发布|上线|发送|群发|推送|创建|新建|刷新|重试|重新加载|启用|生成|复制|同步|归档|退款|审核|批准/;

function classifyUnboundActions(root: ParentNode): void {
  for (const element of root.querySelectorAll<HTMLElement>('button,a')) {
    const bound = (element as FbElement)[BOUND_MARK] === true;
    const navigates = element instanceof HTMLAnchorElement && element.hasAttribute('href');
    if (bound) element.dataset.capabilityState = 'real';
    else if (navigates || !BUSINESS_ACTION_RE.test((element.textContent || '').trim())) element.dataset.capabilityState = 'presentation_only';
    else {
      element.dataset.capabilityState = 'backend_blocked';
      element.setAttribute('aria-description', '后端能力未就绪，点击不会发送请求');
    }
  }
}

/* ---------- 委托：未接管业务动作统一明确阻断 ---------- */
function delegate(e: Event): void {
  const target = e.target as HTMLElement;
  if (target.closest('#fb-mask') || target.closest('#fb-prog-mask')) return;
  const btn = target.closest('button,a') as FbElement | null;
  if (!btn || btn[BOUND_MARK] || btn instanceof HTMLAnchorElement && btn.hasAttribute('href') || btn instanceof HTMLButtonElement && btn.disabled) return;
  const t = (btn.textContent || '').trim();
  if (BUSINESS_ACTION_RE.test(t)) {
    btn.dataset.capabilityState = 'backend_blocked';
    toast('后端能力未就绪：该操作不可执行', true);
  }
}

/* ---------- 文件选择提示 ---------- */
function onFileChange(e: Event): void {
  const input = e.target as HTMLInputElement;
  if (!(input instanceof HTMLInputElement) || input.type !== 'file') return;
  if (!input.files || !input.files.length) return;
  let total = 0;
  for (let i = 0; i < input.files.length; i++) total += input.files[i].size || 0;
  const mb = total / 1048576;
  const sizeText = mb >= 1 ? mb.toFixed(1) + ' MB' : Math.max(1, Math.round(total / 1024)) + ' KB';
  const name = input.files.length === 1 ? input.files[0].name : input.files.length + ' 个文件';
  toast('已选择：' + name + '（' + sizeText + '）');
}

/** 安装全局反馈层（每个页面入口调用一次） */
export function initFeedback(): void {
  if (installed) return;
  installed = true;
  ensureUI();
  classifyUnboundActions(document);
  new MutationObserver((records) => {
    for (const record of records) for (const node of record.addedNodes) if (node instanceof HTMLElement) classifyUnboundActions(node);
  }).observe(document.body, { childList: true, subtree: true });
  document.addEventListener('click', delegate, true);
  document.addEventListener('change', onFileChange, true);
}
