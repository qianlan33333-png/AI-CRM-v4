(function () {
  'use strict';
  const platformFetch = typeof window.fetch === 'function' ? window.fetch.bind(window) : function () { return Promise.reject(new Error('fetch unavailable')); };
  function text(value) { return value == null ? '' : String(value); }
  function csrfToken() { const match = document.cookie.match(/(?:^|;\s*)(?:aicrm_admin_csrf|aicrm_csrf)=([^;]+)/); return match ? decodeURIComponent(match[1]) : ''; }
  function requestKey() { return 'survey-host-' + (crypto.randomUUID ? crypto.randomUUID() : Date.now().toString(36)); }
  async function adminRequest(path, options) {
    const headers = new Headers((options && options.headers) || {}); headers.set('X-CSRF-Token', csrfToken()); headers.set('Idempotency-Key', requestKey());
    const response = await platformFetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers: headers }));
    if (!response.ok) { const error = new Error(response.status === 401 ? '登录状态已失效，请重新登录后继续。' : response.status === 403 ? '没有此操作权限。' : response.status === 409 ? '问卷配置已变化，请重新读取后重试。' : response.status >= 500 ? '问卷服务暂不可用，请稍后重试。' : '问卷请求失败，请稍后重试。'); error.status = response.status; throw error; }
    return response.json();
  }
  function appendCell(row, value) { const cell = document.createElement('td'); cell.textContent = text(value || '—'); cell.style.cssText = 'padding:8px;border-bottom:1px solid #f2f3f5;vertical-align:top'; row.appendChild(cell); }
  function logTime(value) { if (!value) return '—'; const formatted = window.AdminFmt && typeof window.AdminFmt.localTime === 'function' ? window.AdminFmt.localTime(value) : ''; return formatted || '时间暂时无法显示'; }
  function statusLabel(item) {
    if (item.status === 'queued') return '等待处理'; if (item.status === 'executed') return item.provider_result_received === true ? '已收到处理结果' : '已完成处理';
    if (item.status === 'outcome_unknown') return '处理结果待确认（不会自动重复发送）'; if (item.status === 'disabled') return '当时未启用外推配置';
    if (item.status === 'legacy_success') return '历史记录：已完成'; if (item.status === 'legacy_failed' || item.status === 'final_failed') return '未完成'; if (item.status === 'attempted') return '正在等待结果'; return '历史记录：状态待确认';
  }
  function failureCategoryLabel(value) {
    return ({ provider_disabled: '外推服务未启用', provider_execution_unproven: '未能确认外部服务实际执行', provider_outcome_unknown: '外部处理结果待核对', retryable_provider_failure: '外部服务处理失败，可重试', provider_final_failure: '外部服务处理失败' })[text(value)] || (value ? '处理原因待确认' : '—');
  }
  function attemptLabel(item) { const attempt = Number.isInteger(item.provider_attempt_number) ? item.provider_attempt_number : item.attempt_count; return Number.isInteger(attempt) ? '尝试 ' + attempt + ' 次' : '尝试次数未记录'; }
  function findExternalPushCard() {
    const heading = Array.from(document.querySelectorAll('h3')).find(function (node) { return node.textContent.trim() === '外部推送绑定'; });
    return heading && heading.parentElement && heading.parentElement.parentElement && heading.parentElement.parentElement.parentElement ? heading.parentElement.parentElement.parentElement : null;
  }
  function findLogCard() {
    const heading = Array.from(document.querySelectorAll('h3')).find(function (node) { return /问卷.*(?:外推|推送).*记录/.test(node.textContent); });
    return heading ? heading.parentElement : null;
  }
  function installTargetSelector(references) {
    const values = Array.from(new Set((Array.isArray(references) ? references : []).filter(function (value) { return typeof value === 'string' && /^[A-Za-z0-9._:-]{1,128}$/.test(value); }))).sort();
    const catalog = values.join('\n');
    const sync = function () { if (!globalThis.document || !globalThis.document.documentElement) { observer.disconnect(); return; } const field = document.getElementById('opsConfigurationReference'); if (!field) return; const isSelect = field.tagName === 'SELECT'; if (isSelect && field.dataset.surveyHostTargetCatalog === catalog) return; const selected = field.value.trim(); const select = isSelect ? field : document.createElement('select'); select.replaceChildren(); select.id = field.id; select.name = field.name; select.style.cssText = field.style.cssText; select.setAttribute('aria-label', '推送配置引用'); select.dataset.surveyHostTargetCatalog = catalog; const empty = document.createElement('option'); empty.value = ''; empty.disabled = true; empty.textContent = values.length ? '请选择已部署的推送目标' : '当前没有可用的推送目标'; select.appendChild(empty); values.forEach(function (reference) { const item = document.createElement('option'); item.value = reference; item.textContent = reference; select.appendChild(item); }); if (selected && !values.includes(selected)) { const unavailable = document.createElement('option'); unavailable.value = selected; unavailable.disabled = true; unavailable.textContent = '当前绑定的目标已不可用，请重新选择'; select.appendChild(unavailable); } select.value = selected; select.disabled = !values.length; if (!isSelect) field.replaceWith(select); };
    const observer = new MutationObserver(sync); observer.observe(document, { childList: true, subtree: true }); window.addEventListener('pagehide', function () { observer.disconnect(); }, { once:true }); sync();
  }
  function installFrozenPublishBridge() {
    if (!document.body || document.body.dataset.page !== 'questionnaireDetail') return;
    let pendingButton = null;
    let publishing = false;
    function currentButton(fallback) { return document.getElementById('v2-publish-save') || fallback || null; }
    function showPublishState(button, questionnaire, failed) {
      const target = currentButton(button);
      let status = document.querySelector('[data-survey-host-publish-status]');
      if (!status) {
        status = document.createElement('span');
        status.dataset.surveyHostPublishStatus = 'true';
        status.style.cssText = 'margin-left:10px;font-size:13px';
        if (target && target.parentElement) target.parentElement.appendChild(status);
      }
      if (failed) {
        status.textContent = '发布失败，请重试';
        status.style.color = '#d93026';
        return;
      }
      const publicPath = text(questionnaire && questionnaire.public_path);
      status.replaceChildren();
      status.style.color = '#1f7a1f';
      status.dataset.surveyHostPublishedVersion = text(questionnaire && questionnaire.version);
      status.appendChild(document.createTextNode('已发布 · '));
      const link = document.createElement('a');
      link.href = publicPath;
      link.textContent = '打开公开问卷';
      link.dataset.surveyHostPublishedPath = publicPath;
      status.appendChild(link);
    }
    function responseQuestionnaire(value) {
      if (!value || typeof value !== 'object') return null;
      if (value.questionnaire && typeof value.questionnaire === 'object') return value.questionnaire;
      if (value.data && value.data.questionnaire && typeof value.data.questionnaire === 'object') return value.data.questionnaire;
      return value;
    }
    async function publishSavedQuestionnaire(saved, button) {
      const questionnaire = responseQuestionnaire(saved);
      const id = Number(questionnaire && questionnaire.id);
      if (!Number.isSafeInteger(id) || id < 1) throw new Error('saved questionnaire id is missing');
      await adminRequest('/api/admin/questionnaires/' + id + '/public-publish', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ expected_questionnaire_version: Number(questionnaire.version) || 0 }) });
      const detail = responseQuestionnaire(await adminRequest('/api/admin/questionnaires/' + id, { method: 'GET', headers: { Accept: 'application/json' } }));
      if (!detail || detail.status !== 'active' || detail.enabled !== true || !text(detail.public_path)) throw new Error('published questionnaire was not confirmed');
      showPublishState(button, detail, false);
    }
    window.addEventListener('aicrm:survey-editor-save', function (event) {
      const detail = event && event.detail;
      const button = pendingButton;
      if (!button || publishing || !detail || !detail.promise || typeof detail.promise.then !== 'function') return;
      pendingButton = null;
      publishing = true;
      // questionnaireEditorV3 dispatches this event synchronously with the
      // promise for the save it has just started. Do not infer a publish from
      // fetch timing: an unrelated regular save must never consume this click.
      Promise.resolve(detail.promise).then(function (saved) {
        return publishSavedQuestionnaire(saved, button);
      }).catch(function () {
        showPublishState(button, null, true);
      }).finally(function () {
        publishing = false;
      });
    });
    document.addEventListener('click', function (event) {
      // A previous validation failure cannot survive into another control's
      // click. The current publish click is installed below and is cleared in
      // its bubble phase if the Adapter does not synchronously consume it.
      if (pendingButton && !publishing) pendingButton = null;
      const button = event.target && event.target.closest && event.target.closest('#v2-publish-save');
      if (!button) return;
      if (pendingButton || publishing) {
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
      }
      pendingButton = button;
    }, true);
    document.addEventListener('click', function () {
      // The frozen button's target listener runs before this bubble listener.
      // A valid save has already handed its exact promise to the Host; a
      // validation failure has not, so discard only the unconsumed gesture.
      if (pendingButton && !publishing) pendingButton = null;
    });
  }
  function installFrozenEnableBridge() {
    if (!document.body || !['questionnaires', 'questionnaireDetail'].includes(document.body.dataset.page || '')) return;
    const originalFetch = window.fetch;
    if (typeof originalFetch !== 'function') return;
    window.fetch = function (input, options) {
      const method = text(options && options.method || input && input.method || 'GET').toUpperCase();
      const rawURL = typeof input === 'string' ? input : input && input.url || '';
      let pathname = rawURL;
      try { pathname = new URL(rawURL, window.location.href).pathname; } catch (_error) {}
      const match = /^\/api\/admin\/questionnaires\/([1-9][0-9]*)\/enable$/.exec(pathname);
      const response = originalFetch(input, options);
      if (method !== 'POST' || !match) return response;
      // A frozen editor calls /enable for a just-saved draft. The Owner correctly
      // rejects that because a draft has no immutable definition. Keep normal
      // re-enables on /enable; only its concrete conflict is retried through the
      // Owner's definition-freezing public-publish operation.
      return Promise.resolve(response).then(function (result) {
        if (!result || result.status !== 409) return result;
        // Do not publish whatever happens to be current after the /enable
        // conflict. Read the draft once and submit that exact version so a
        // concurrent edit remains a visible 409 instead of a silent publish.
        return platformFetch('/api/admin/questionnaires/' + match[1], { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } }).then(function (detailResponse) {
          if (!detailResponse || !detailResponse.ok) return result;
          return detailResponse.json();
        }).then(function (detail) {
          const questionnaire = detail && (detail.questionnaire || detail.data && detail.data.questionnaire || detail);
          if (!questionnaire || !Number.isSafeInteger(Number(questionnaire.version)) || Number(questionnaire.version) < 1) return result;
          const headers = new Headers((options && options.headers) || {});
          headers.set('Content-Type', 'application/json');
          headers.set('Accept', 'application/json');
          headers.set('X-CSRF-Token', csrfToken());
          headers.set('Idempotency-Key', requestKey());
          return platformFetch('/api/admin/questionnaires/' + match[1] + '/public-publish', {
            method: 'POST', credentials: 'same-origin', headers: headers,
            body: JSON.stringify({ expected_questionnaire_version: Number(questionnaire.version) }),
          });
        }).catch(function () { return result; });
      });
    };
  }
  function installFrozenShareStateGuard() {
    const page = document.body && document.body.dataset.page || '';
    if (!['questionnaires', 'questionnaireDetail'].includes(page)) return;
    function ready(questionnaire) {
      return !!(questionnaire && questionnaire.status === 'active' && questionnaire.enabled === true && text(questionnaire.public_path));
    }
    function notice(button) {
      let status = document.querySelector('[data-survey-host-share-status]');
      if (!status) {
        status = document.createElement('span'); status.dataset.surveyHostShareStatus = 'true'; status.setAttribute('role', 'alert');
        status.style.cssText = 'margin-left:10px;font-size:13px;color:#d93026';
        (button && button.parentElement || document.body).appendChild(status);
      }
      status.textContent = '问卷尚未发布或已停用，不能分享公开链接。请先保存并发布。';
    }
    function setButtonState(questionnaire) {
      const published = ready(questionnaire);
      document.querySelectorAll('[data-action="share"],#editor-share-btn').forEach(function (button) {
        button.dataset.surveyHostShareReady = published ? 'true' : 'false';
        button.disabled = !published; button.setAttribute('aria-disabled', published ? 'false' : 'true');
        button.title = published ? '' : '请先保存并发布问卷';
      });
    }
    function setListState() {
      const activeDocument = globalThis.document;
      if (!activeDocument || !activeDocument.body) return;
      activeDocument.querySelectorAll('tbody tr').forEach(function (row) {
        // The frozen V3 list binds r.shareIt directly to an ordinary anchor;
        // it has neither legacy classes nor data-action attributes. Its own
        // controller has already derived these canonical labels from status,
        // is_disabled and public_path, so use that rendered state rather than
        // guessing from a draft URL.
        const share = Array.from(row.querySelectorAll('a')).find(function (anchor) { return text(anchor.textContent).trim() === '分享'; });
        if (!share) return;
        const published = !/(未发布|已停用|草稿|draft)/i.test(text(row.textContent));
        share.dataset.surveyHostShareReady = published ? 'true' : 'false';
        share.setAttribute('aria-disabled', published ? 'false' : 'true');
        share.title = published ? '' : '请先保存并发布问卷';
      });
    }
    const questionnaireID = new URLSearchParams(location.search).get('id') || '';
    if (page === 'questionnaireDetail' && /^[1-9][0-9]*$/.test(questionnaireID)) {
      adminRequest('/api/admin/questionnaires/' + questionnaireID, { method: 'GET', headers: { Accept: 'application/json' } }).then(function (payload) {
        const questionnaire = payload && (payload.questionnaire || payload.data && payload.data.questionnaire || payload); setButtonState(questionnaire);
      }).catch(function () { setButtonState(null); });
      window.addEventListener('aicrm:survey-editor-save', function (event) {
        const detail = event && event.detail;
        if (!detail || !detail.promise || typeof detail.promise.then !== 'function') return;
        Promise.resolve(detail.promise).then(function (payload) { setButtonState(payload && (payload.questionnaire || payload.data && payload.data.questionnaire || payload)); }).catch(function () { setButtonState(null); });
      });
    } else {
      setListState(); new MutationObserver(setListState).observe(document.body, { childList: true, subtree: true });
    }
    document.addEventListener('click', function (event) {
      const candidate = event.target && event.target.closest && event.target.closest('[data-action="share"],#editor-share-btn,a');
      const button = candidate && (candidate.id === 'editor-share-btn' || candidate.dataset.action === 'share' || (page === 'questionnaires' && text(candidate.textContent).trim() === '分享')) ? candidate : null;
      if (!button || button.dataset.surveyHostShareReady === 'true') return;
      event.preventDefault(); event.stopImmediatePropagation(); notice(button);
    }, true);
  }
  function installQrFallback() {
    const page = document.body.dataset.page || ''; if (!['questionnaires', 'questionnaireDetail', 'questionnaireOps'].includes(page)) return;
    const pending = new WeakSet();
    function scan() { if (!document || !document.body) return; const box = document.getElementById('shareQrBox'); if (!box || pending.has(box)) return; pending.add(box); setTimeout(function () { if (!box.isConnected || box.childElementCount || text(box.textContent).trim()) return; const alert = document.createElement('div'); alert.setAttribute('role', 'alert'); alert.dataset.surveyQrFallback = 'true'; alert.style.cssText = 'padding:16px;color:#d93026;text-align:center;line-height:1.6'; alert.textContent = '二维码加载失败，请使用上方“复制”按钮复制链接。'; box.replaceChildren(alert); }, 1200); }
    scan(); new MutationObserver(scan).observe(document.body, { childList: true, subtree: true });
  }
  function makeMetadataForm(payload, operationsPath) {
    const push = payload.external_push || {}, metadata = push.metadata && typeof push.metadata === 'object' ? push.metadata : {};
    const reserved = new Set(['user_id', 'questionnaire_title', 'submitted_at', 'answers', 'phone_number', 'type', 'expires_at_ts', 'day', 'frequency', 'remark', 'assessment_result_snapshot', 'is_test', 'test_run_id', '__proto__', 'constructor', 'prototype']);
    if (!document.getElementById('survey-push-editor-style')) {
      const style = document.createElement('style'); style.id = 'survey-push-editor-style'; style.textContent = `
      .survey-push-editor{min-width:0;margin-top:20px;color:#1f2937;font-size:14px}.survey-push-editor *{box-sizing:border-box}
      .survey-push-layout>section,.survey-push-layout>aside{min-width:0}.survey-push-layout{display:grid;grid-template-columns:minmax(0,1.5fr) minmax(260px,1fr);gap:24px;align-items:start}
      .survey-push-editor h4{margin:0 0 8px;font-size:16px}.survey-push-help{color:#86909c;font-size:13px;line-height:1.6;margin:0 0 16px}
      .survey-push-row,.survey-push-columns{display:grid;grid-template-columns:minmax(105px,1fr) 70px minmax(120px,1.2fr) 42px;gap:10px;align-items:center;margin-bottom:10px}
      .survey-push-columns{color:#86909c;font-size:12px;padding:10px 0;border-bottom:1px solid #edf0f5}.survey-push-editor input{width:100%;min-width:0;padding:10px 12px;border:1px solid #dce1e8;border-radius:8px;background:white;font-size:14px;color:#1f2937}
      .survey-push-editor input:focus{outline:2px solid #b6caff;border-color:#3370ff}.survey-push-editor input[readonly]{background:#f7f8fa;color:#646a73}
      .survey-push-editor button{font:inherit;cursor:pointer;border:1px solid #dce1e8;border-radius:8px;background:white;color:#3370ff;padding:9px 12px}.survey-push-editor button:disabled{opacity:.55;cursor:default}
      .survey-push-fixed{font-size:12px;color:#646a73;background:#f2f4f7;border-radius:5px;padding:5px;text-align:center}.survey-push-remove{padding:8px 0!important;font-size:12px!important;color:#86909c!important}
      .survey-push-preview{border:1px solid #e6eaf0;border-radius:12px;background:#f8fafc;padding:18px;overflow:hidden}.survey-push-preview pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.7 ui-monospace,monospace;color:#344054;max-height:420px;overflow:auto}
      .survey-push-preserved{border:1px solid #dce8ff;background:#eff5ff;border-radius:8px;padding:12px;color:#476282;font-size:13px;line-height:1.7;margin-bottom:16px}
      .survey-push-actions{display:flex;justify-content:flex-end;align-items:center;gap:12px;border-top:1px solid #edf0f5;margin-top:22px;padding-top:18px}.survey-push-save{background:#3370ff!important;color:white!important;border-color:#3370ff!important}.survey-push-error{color:#d93026;font-size:13px;min-height:20px}.survey-push-editor [aria-invalid=true]{border-color:#e5484d}
      @media(max-width:1000px){.survey-push-layout{grid-template-columns:1fr}.survey-push-row,.survey-push-columns{grid-template-columns:minmax(80px,1fr) 54px minmax(90px,1fr) 36px;gap:6px}}
      @media(max-width:600px){.survey-push-row,.survey-push-columns{grid-template-columns:minmax(0,1fr) 44px minmax(0,1fr) 32px;gap:4px}.survey-push-editor input{padding:8px 6px}.survey-push-fixed{padding:4px 2px}.survey-push-preview{padding:12px}.survey-push-editor button{max-width:100%;overflow-wrap:anywhere}}
      `; document.head.appendChild(style);
    }
    function element(tag, label, cls) { const node = document.createElement(tag); if (label) node.textContent = label; if (cls) node.className = cls; return node; }
    const form = element('form', '', 'survey-push-editor'); form.dataset.surveyPushMetadata = 'true';
    const layout = element('div', '', 'survey-push-layout'), fields = element('section'), preview = element('aside', '', 'survey-push-preview');
    fields.append(element('h4', '附加固定参数'), element('p', '填写接收方要求的参数名和固定值。问卷答案无需配置，提交时会自动附带。', 'survey-push-help'));
    const columns = element('div', '', 'survey-push-columns'); ['参数名 key', '值来源', '参数值', ''].forEach(function (label) { columns.append(element('span', label)); }); fields.append(columns);
    const builtins = [['type', '推送类型'], ['expires_at_ts', '有效期秒时间戳'], ['day', '第几天'], ['frequency', '频次'], ['remark', '备注']];
    builtins.forEach(function (item) {
      const row = element('div', '', 'survey-push-row'), key = element('input'), input = element('input'); key.value = item[0]; key.readOnly = true; key.setAttribute('aria-label', item[1] + '参数名');
      input.name = item[0]; input.placeholder = item[1] + '（选填）'; input.setAttribute('aria-label', item[1]); input.value = metadata[item[0]] == null ? '' : String(metadata[item[0]]);
      row.append(key, element('span', '固定值', 'survey-push-fixed'), input, element('span')); fields.append(row);
    });
    const params = element('div'); params.dataset.surveyPushParams = 'true'; fields.append(params);
    function paramRow(name, value) {
      const row = element('div', '', 'survey-push-row'), key = element('input'), val = element('input'), remove = element('button', '删除', 'survey-push-remove');
      key.dataset.paramName = 'true'; key.placeholder = '参数名'; key.setAttribute('aria-label', '自定义参数名'); key.value = name || '';
      val.dataset.paramValue = 'true'; val.placeholder = '输入固定文本'; val.setAttribute('aria-label', '自定义参数值'); val.value = value == null ? '' : String(value);
      remove.type = 'button'; remove.onclick = function () { row.remove(); updatePreview(); }; row.append(key, element('span', '固定值', 'survey-push-fixed'), val, remove); params.append(row);
    }
    Object.entries(metadata.custom_params || {}).forEach(function (item) { paramRow(item[0], item[1]); });
    const add = element('button', '＋ 添加字段'); add.type = 'button'; add.onclick = function () { paramRow('', ''); params.lastElementChild.querySelector('input').focus(); }; fields.append(add);
    preview.append(element('h4', 'JSON 预览 · 固定参数'), element('div', '原有问卷 JSON 保持不变：答案、提交信息和测评结果将在提交时原样附带。这里仅预览附加参数，不读取真实答卷。', 'survey-push-preserved'));
    const code = element('pre'); code.dataset.surveyPushPreview = 'true'; const copy = element('button', '复制参数 JSON'); copy.type = 'button'; preview.append(code, copy);
    const errorBox = element('p', '', 'survey-push-error'); errorBox.setAttribute('role', 'alert');
    const saveStatus = element('p', '', 'survey-push-help'); saveStatus.dataset.surveyPushSaveStatus = 'true'; saveStatus.setAttribute('role', 'status'); saveStatus.setAttribute('aria-live', 'polite');
    const actions = element('div', '', 'survey-push-actions'), save = element('button', '保存当前维度', 'survey-push-save'); save.type = 'submit'; actions.append(save); layout.append(fields, preview); form.append(layout, errorBox, saveStatus, actions);
    let validJSON = null, saving = false;
    function readMetadata() {
      const next = { custom_params: Object.create(null) }; form.querySelectorAll('[aria-invalid]').forEach(function (input) { input.removeAttribute('aria-invalid'); });
      builtins.forEach(function (item) { const key = item[0], input = form.elements[key], value = input.value.trim(); if (!value) return;
        const numeric = /^(day|frequency|expires_at_ts)$/.test(key), parsed = numeric ? Number(value) : value;
        if (numeric && (!Number.isSafeInteger(parsed) || parsed < 0)) { input.setAttribute('aria-invalid', 'true'); throw new Error(item[1] + '必须是非负整数'); } next[key] = parsed;
      });
      params.querySelectorAll('.survey-push-row').forEach(function (row) {
        const key = row.querySelector('[data-param-name]'), val = row.querySelector('[data-param-value]'), name = key.value;
        if (!name || name !== name.trim() || new TextEncoder().encode(name).length > 128 || reserved.has(name) || Object.hasOwn(next.custom_params, name)) {
          key.setAttribute('aria-invalid', 'true'); throw new Error(!name ? '请填写参数名' : reserved.has(name) ? '参数名“' + name + '”属于原有问卷字段，不能覆盖' : '参数名重复、过长或包含首尾空格');
        }
        next.custom_params[name] = val.value;
      }); return next;
    }
    function updatePreview() {
      try { const next = readMetadata(), flat = Object.assign(Object.create(null), next); delete flat.custom_params; Object.assign(flat, next.custom_params); validJSON = JSON.stringify(flat, null, 2); code.textContent = validJSON; errorBox.textContent = ''; copy.disabled = false; return next; }
      catch (error) { validJSON = null; code.textContent = '请先修正左侧字段'; errorBox.textContent = error.message; copy.disabled = true; return null; }
    }
    copy.onclick = async function () { if (!validJSON) return; try { await navigator.clipboard.writeText(validJSON); copy.textContent = '已复制'; } catch (_) { errorBox.textContent = '复制失败，请手动选择右侧 JSON 复制'; } };
    form.addEventListener('input', function () { saveStatus.textContent = ''; updatePreview(); }); updatePreview();
    form.addEventListener('submit', async function (event) {
      event.preventDefault(); if (saving) return; const next = updatePreview(); if (!next) return; saving = true; save.disabled = true; saveStatus.textContent = '正在保存…';
      try {
        const reference = document.getElementById('opsConfigurationReference');
        const saved = await adminRequest(operationsPath + '/external-push', { method: 'PUT', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ enabled: form.dataset.enabled === undefined ? push.enabled === true : form.dataset.enabled === 'true', configuration_reference: reference ? reference.value.trim() : (push.configuration_reference || ''), metadata: next, configuration_version: payload.configuration_version }) });
        payload.configuration_version = saved.configuration_version; push.enabled = saved.external_push.enabled; push.configuration_reference = saved.external_push.configuration_reference; push.metadata = saved.external_push.metadata; save.textContent = '已保存'; saveStatus.textContent = '已保存当前维度';
      } catch (error) { save.textContent = error && (error.status === 409 || /\b409\b/.test(error.message || '')) ? '配置已更新，请重新打开后再保存' : '保存失败'; errorBox.textContent = save.textContent; saveStatus.textContent = ''; }
      finally { saving = false; save.disabled = false; }
    });
    return form;
  }
  // The shared committedTextSearch adapter forwards this input only after a
  // normal Enter. This Host owns the local query state so scope changes and
  // log refreshes retain a submitted query without treating a newer draft as
  // a search.
  function renderLogs(card, current, global, scope, committedKeyword, draftKeyword, setScope, setCommittedKeyword) {
    card.replaceChildren(); card.dataset.surveyHostLogs = 'true';
    const header = document.createElement('div'); header.style.cssText = 'display:flex;justify-content:space-between;gap:8px;align-items:center'; const heading = document.createElement('h3'); heading.textContent = scope === 'global' ? '全部问卷外推记录' : '当前问卷外推记录'; heading.style.cssText = 'margin:0;font-size:15px'; const count = document.createElement('span'); count.style.cssText = 'font-size:12px;color:#8F959E'; header.append(heading, count); card.appendChild(header);
    const controls = document.createElement('div'); controls.style.cssText = 'display:flex;gap:8px;flex-wrap:wrap;margin-top:12px';
    const filter = document.createElement('input'); filter.type = 'search'; filter.placeholder = '测试记录 ID / 问卷 ID'; filter.setAttribute('aria-label', '搜索问卷外推记录'); filter.dataset.surveyLogSearch = 'true'; filter.value = draftKeyword; filter.style.cssText = 'height:28px;min-width:180px;border:1px solid #DEE0E3;border-radius:6px;padding:0 8px;font-size:12px';
    [['当前问卷', 'current'], ['全部问卷', 'global']].forEach(function (item) { const button = document.createElement('button'); button.type = 'button'; button.textContent = item[0]; button.dataset.surveyLogScope = item[1]; button.style.cssText = 'height:28px;padding:0 10px;border:1px solid #DEE0E3;border-radius:6px;background:' + (scope === item[1] ? '#EFF4FF' : '#fff') + ';font-size:12px'; button.onclick = function () { setScope(item[1], filter.value); }; controls.appendChild(button); });
    controls.appendChild(filter); card.appendChild(controls);
    const query = String(committedKeyword || '').trim().toLowerCase(); const source = scope === 'global' ? global : current; const rows = source.filter(function (item) { return !query || JSON.stringify(item).toLowerCase().includes(query); }); count.textContent = rows.length + ' 条' + (filter.value !== committedKeyword ? ' · 输入后按 Enter 搜索' : '');
    filter.oninput = function () { setCommittedKeyword(filter.value); };
    if (!rows.length) { const empty = document.createElement('p'); empty.textContent = query ? '没有匹配的测试记录。' : '暂无测试记录。'; card.appendChild(empty); return; }
    const table = document.createElement('table'); table.style.cssText = 'width:100%;border-collapse:collapse;margin-top:10px;font-size:12px'; const head = document.createElement('thead'), headRow = document.createElement('tr'); ['时间', '外推记录', '处理状态', '尝试情况', '备注'].forEach(function (label) { const cell = document.createElement('th'); cell.textContent = label; cell.style.cssText = 'text-align:left;padding:8px;border-bottom:1px solid #DEE0E3'; headRow.appendChild(cell); }); head.appendChild(headRow); const body = document.createElement('tbody'); rows.forEach(function (item) { const row = document.createElement('tr'); row.dataset.surveySearch = JSON.stringify(item).toLowerCase(); appendCell(row, logTime(item.occurred_at || item.updated_at || item.created_at)); appendCell(row, item.source_pk || item.test_run_id || item.id); appendCell(row, statusLabel(item)); appendCell(row, attemptLabel(item)); appendCell(row, item.failure_category ? failureCategoryLabel(item.failure_category) : (item.read_only_legacy ? '历史只读记录' : '—')); body.appendChild(row); }); table.append(head, body); card.appendChild(table);
  }
  function confirmControlledPush(button) {
    if (document.querySelector('[data-survey-host-test-confirmation]')) return;
    const dialog = document.createElement('div'); dialog.dataset.surveyHostTestConfirmation = 'true'; dialog.setAttribute('role', 'dialog'); dialog.setAttribute('aria-modal', 'true'); dialog.style.cssText = 'margin-top:10px;padding:12px;border:1px solid #DEE0E3;border-radius:6px;background:#FAFBFC;font-size:13px;line-height:1.6';
    const title = document.createElement('strong'); title.textContent = '创建受控外推测试';
    const detail = document.createElement('p'); detail.style.cssText = 'margin:6px 0 10px;color:#646A73'; detail.textContent = '将为当前问卷创建外推测试并等待处理回执。创建受理不代表接收方业务已生效。确认继续？';
    const actions = document.createElement('div'); actions.style.cssText = 'display:flex;gap:8px;justify-content:flex-end';
    const cancel = document.createElement('button'); cancel.type = 'button'; cancel.textContent = '取消'; cancel.onclick = function () { dialog.remove(); };
    const confirm = document.createElement('button'); confirm.type = 'button'; confirm.dataset.surveyHostTestConfirm = 'true'; confirm.textContent = '确认创建受控测试'; confirm.onclick = function () { dialog.remove(); if (typeof window.__surveyHostTestPush === 'function') void window.__surveyHostTestPush(button); };
    actions.append(cancel, confirm); dialog.append(title, detail, actions); button.insertAdjacentElement('afterend', dialog);
  }
  async function mount() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    const questionnaireID = new URLSearchParams(location.search).get('id') || ''; if (!/^[1-9][0-9]*$/.test(questionnaireID)) return;
    const externalCard = findExternalPushCard(), logCard = findLogCard(); if (!externalCard || !logCard || externalCard.querySelector('[data-survey-push-metadata]')) return;
    const operationsPath = '/api/admin/questionnaires/' + questionnaireID + '/operations';
    try {
      const [payload, page] = await Promise.all([adminRequest(operationsPath, { method: 'GET', headers: { Accept: 'application/json' } }), adminRequest('/admin/questionnaires/external-push-logs?limit=100&offset=0', { method: 'GET', headers: { Accept: 'application/json' } })]);
      if (!payload || !Array.isArray(payload.items)) throw new Error('invalid operations');
      const metadataForm = makeMetadataForm(payload, operationsPath);
      const heading = externalCard.querySelector('h3'), headingRow = heading && heading.parentElement && heading.parentElement.parentElement;
      const oldToggle = headingRow && Array.from(headingRow.children).find(function (node) { return node.tagName === 'SPAN'; });
      if (oldToggle) {
        const toggleLabel = document.createElement('label'); toggleLabel.style.cssText = 'display:flex;gap:8px;align-items:center;font-size:14px;color:#646a73';
        const toggle = document.createElement('input'); toggle.type = 'checkbox'; toggle.setAttribute('role', 'switch'); toggle.setAttribute('aria-label', '启用问卷外推'); toggle.dataset.surveyPushEnabled = 'true'; toggle.checked = payload.external_push && payload.external_push.enabled === true;
        toggleLabel.append(toggle, document.createTextNode('启用')); oldToggle.replaceWith(toggleLabel);
        // Tie the host switch to the one form without invoking the frozen
        // controller, which would rerender and discard unsaved metadata.
        metadataForm.id = 'survey-push-configuration-form'; toggle.setAttribute('form', metadataForm.id);
        toggle.addEventListener('change', function () { metadataForm.dataset.enabled = toggle.checked ? 'true' : 'false'; });
        metadataForm.dataset.enabled = toggle.checked ? 'true' : 'false';
      }
      const help = heading && heading.parentElement.querySelector('p'); if (help) help.textContent = '问卷提交后，按已配置的目标推送原有问卷 JSON 和附加固定参数。';
      if (!document.getElementById('opsConfigurationReference')) {
        const label = document.createElement('label'); label.textContent = '推送目标'; label.style.cssText = 'display:block;margin:16px 0;color:#646a73;font-size:13px';
        const target = document.createElement('select'); target.id = 'opsConfigurationReference'; target.style.cssText = 'display:block;width:100%;padding:10px;border:1px solid #dce1e8;border-radius:8px;margin-top:8px';
        const selected = document.createElement('option'); selected.value = payload.external_push && payload.external_push.configuration_reference || ''; selected.textContent = selected.value; target.append(selected); label.append(target); externalCard.append(label);
      }
      externalCard.querySelectorAll('button').forEach(function (button) { if (button.textContent === '保存外部推送') button.hidden = true; });
      externalCard.appendChild(metadataForm);
      const headerSave = Array.from(document.querySelectorAll('button')).find(function (button) { return !externalCard.contains(button) && button.textContent.trim() === '保存当前维度'; });
      if (headerSave) {
        metadataForm.querySelector('button[type="submit"]').hidden = true;
        headerSave.addEventListener('click', function (event) {
          let panel = externalCard; while (panel && panel !== document.body) { if (panel.hidden || panel.style.display === 'none') return; panel = panel.parentElement; }
          event.preventDefault(); event.stopImmediatePropagation(); metadataForm.requestSubmit();
        }, true);
      }
      installTargetSelector(payload.target_catalog_available === true ? payload.available_configuration_references : []);
      const legacyLogBoundary = Array.from((logCard.parentElement || logCard).querySelectorAll('p')).find(function (node) { return node.textContent.includes('只显示本地 queued 测试记录') || node.textContent.includes('没有 Provider 调用'); });
      if (legacyLogBoundary) { legacyLogBoundary.dataset.surveyHostLogBoundary = 'true'; legacyLogBoundary.textContent = '受控外推记录展示创建、尝试和处理回执；HTTP 受理不代表接收方业务已生效。'; }
      const logState = { current: payload.items, global: page && Array.isArray(page.items) ? page.items : [], scope: 'current', committedQuery: '', draftQuery: '' };
      const redraw = function (nextScope, draftQuery) {
        logState.scope = nextScope || logState.scope;
        const live = logCard.querySelector('input[data-survey-log-search]');
        if (draftQuery !== undefined) logState.draftQuery = String(draftQuery);
        else if (live) logState.draftQuery = live.value;
        renderLogs(logCard, logState.current, logState.global, logState.scope, logState.committedQuery, logState.draftQuery,
          function (scope, draft) { redraw(scope, draft); },
          function (query) { logState.committedQuery = String(query).trim(); logState.draftQuery = String(query); redraw(logState.scope, logState.draftQuery); });
      };
      redraw(logState.scope);
      const originalTest = Array.from(externalCard.querySelectorAll('button')).find(function (button) { return button.textContent.includes('测试推送（仅本地记录）') || button.dataset.surveyHostTestPush === 'true'; }); if (originalTest) { originalTest.dataset.surveyHostTestPush = 'true'; originalTest.textContent = '创建受控外推测试'; }
      window.__surveyHostTestPush = async function (button) { button.disabled = true; button.textContent = '正在创建受控测试…'; try { const receipt = await adminRequest(operationsPath + '/external-push/test', { method: 'POST', headers: { Accept: 'application/json' } }); button.dataset.surveyHostTestReceipt = text(receipt && receipt.status); button.textContent = receipt && receipt.status === 'queued' ? '受控外推测试已创建，等待处理结果' : '受控外推测试已创建'; const refreshed = await adminRequest(operationsPath, { method: 'GET', headers: { Accept: 'application/json' } }); const refreshedPage = await adminRequest('/admin/questionnaires/external-push-logs?limit=100&offset=0', { method: 'GET', headers: { Accept: 'application/json' } }); logState.current = refreshed.items || []; logState.global = refreshedPage.items || []; redraw(logState.scope); } catch (error) { button.textContent = error && error.status === 403 ? '创建受控测试失败：无操作权限' : '创建受控测试失败'; } finally { button.disabled = false; } };
    } catch (_error) { const note = document.createElement('p'); note.setAttribute('role', 'alert'); note.textContent = '外推设置读取失败，请稍后重试。'; externalCard.appendChild(note); }
  }
  function installLegacyQuestionnaireOpsGuard() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    window.fetch = function (input, options) { const method = (options && options.method || (input && input.method) || 'GET').toUpperCase(); const url = typeof input === 'string' ? input : input && input.url || ''; if (method === 'GET' && /^\/admin\/questionnaires\/[1-9][0-9]*\/external-push-logs(?:\?|$)/.test(url)) { const body = JSON.stringify({items:[],total:0,limit:100,offset:0,has_more:false,local_only:true}); return Promise.resolve({ok:true,status:200,headers:new Headers({'Content-Type':'application/json'}),json:function () { return Promise.resolve(JSON.parse(body)); },text:function () { return Promise.resolve(body); },clone:function () { return this; }}); } return platformFetch(input, options); };
    document.addEventListener('click', function (event) { const button = event.target && event.target.closest && event.target.closest('button'); if (button && button.dataset.surveyHostTestPush === 'true') { event.preventDefault(); event.stopImmediatePropagation(); confirmControlledPush(button); } }, true);
  }
  function start() {
    installQrFallback(); if (document.body.dataset.page !== 'questionnaireOps') return; const stage = document.getElementById('stage'); if (!stage) return;
    let scheduled = false; const ensureHost = function () { if (scheduled || (findExternalPushCard() && findExternalPushCard().querySelector('[data-survey-push-metadata]'))) return; scheduled = true; setTimeout(function () { scheduled = false; void mount(); }, 0); };
    new MutationObserver(ensureHost).observe(stage, { childList: true, subtree: true }); ensureHost(); setTimeout(ensureHost, 50);
  }
  installFrozenPublishBridge();
  installFrozenEnableBridge();
  installFrozenShareStateGuard();
  installLegacyQuestionnaireOpsGuard();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true }); else start();
}());
