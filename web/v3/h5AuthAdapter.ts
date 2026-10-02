import { surveyRead } from './surveyPublicApi';

// Product Design selected the required-login gate. Survey's Owner still
// validates the slug/session, owns OAuth state and controls submission.
if (document.body.dataset.page === 'auth') {
  const screen = document.getElementById('screen');
  if (!screen) throw new Error('微信授权页面缺少运行容器');
  document.body.dataset.v3PublicSurvey = 'auth';
  screen.dataset.v3PublicSurveyScreen = 'auth';
  const query = new URLSearchParams(location.search);
  const slug = query.get('slug') || '';
  const validSlug = /^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$/.test(slug);
  const inWechat = /MicroMessenger/i.test(navigator.userAgent || '');
  const card = document.createElement('section');
  card.className = 'required-auth-card';
  card.setAttribute('aria-labelledby', 'surveyAuthTitle');
  card.innerHTML = '<span class="auth-badge">微信身份验证</span><div class="auth-illustration" role="img" aria-label="微信登录验证"></div><h1 id="surveyAuthTitle">正在核验微信身份</h1><p class="auth-message">正在核验当前微信登录状态…</p><p class="auth-feedback" role="status" hidden></p><a class="auth-button" hidden>授权并继续</a><p class="auth-note">如不继续，可关闭当前微信页面</p>';
  screen.replaceChildren(card);
  const title = card.querySelector('h1')!;
  const message = card.querySelector<HTMLElement>('.auth-message')!;
  const feedback = card.querySelector<HTMLElement>('.auth-feedback')!;
  const action = card.querySelector<HTMLAnchorElement>('.auth-button')!;
  const showRequired = (reason = '') => {
    title.textContent = '登录才能填写问卷';
    message.textContent = '请先完成微信登录验证，验证成功后返回问卷继续填写。不会自动提交。';
    feedback.hidden = !reason;
    feedback.textContent = reason;
    action.hidden = !inWechat;
    if (inWechat) action.href = `/api/h5/surveys/oauth/start?slug=${encodeURIComponent(slug)}`;
    if (!inWechat) {
      title.textContent = '请在微信中打开';
      message.textContent = '请复制当前链接到微信中打开，登录后才能填写问卷。';
    }
  };
  const showBlocked = (reason: string, retry = false) => {
    title.textContent = '暂时无法继续填写';
    message.textContent = reason;
    feedback.hidden = true;
    action.hidden = !retry;
    if (retry) { action.textContent = '重试'; action.href = location.href; }
  };
  if (!validSlug) showBlocked('问卷链接无效，请从原问卷入口重新打开。');
  else if (query.has('oauth_error')) showRequired('微信授权未完成，请点击按钮重新授权。');
  else if (!inWechat) showRequired();
  else {
    void surveyRead(async (signal) => {
      const response = await fetch(`/api/h5/surveys/session?slug=${encodeURIComponent(slug)}`, {
        signal, credentials: 'same-origin', headers: { Accept: 'application/json' },
      });
      if (response.ok) {
        const body = await response.json() as { authorized?: unknown; identity_state?: unknown; display?: unknown; submitted?: unknown };
        if (body?.authorized !== true || body.identity_state !== 'resolved' ||
            !['all', 'one'].includes(String(body.display)) || typeof body.submitted !== 'boolean') {
          throw new Error('Incomplete session response');
        }
      }
      return response;
    }).then((response) => {
      if (response.ok) {
        location.replace(`/q/${encodeURIComponent(slug)}`);
      } else if (response.status === 401) {
        showRequired();
      } else if (response.status === 409) {
        showBlocked('当前微信身份存在冲突，请联系管理员处理后再填写。');
      } else if (response.status === 503) {
        showBlocked('微信登录暂不可用，请稍后重试。', true);
      } else {
        showBlocked('微信登录状态暂不可用，请稍后重试。', true);
      }
    }).catch(() => showBlocked('网络连接失败或读取超时，请检查网络后重试。', true));
  }
}
