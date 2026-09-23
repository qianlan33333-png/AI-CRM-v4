import { request } from '../src/api/transport';

type AnyMap = Record<string, any>;
const body = document.body;
const page = body.dataset.page || '';
const qid = Number(new URLSearchParams(location.search).get('id') || 0);
const text = (value: unknown): string => String(value ?? '').trim();
const object = (value: unknown): AnyMap => value && typeof value === 'object' ? value as AnyMap : {};
const list = (value: unknown): AnyMap[] => Array.isArray(value) ? value.map(object) : [];
const key = (scope: string): string => `${scope}-${globalThis.crypto?.randomUUID?.() || Date.now()}`;

async function write(path: string, method: string, payload?: unknown): Promise<AnyMap> {
  return readResponse(await request(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key('survey-ops') },
    ...(payload === undefined ? {} : { body: JSON.stringify(payload) }),
  }));
}

async function readResponse(value: unknown): Promise<AnyMap> {
  const candidate = value as { json?: () => Promise<unknown> };
  return object(typeof candidate?.json === 'function' ? await candidate.json() : value);
}

async function read(path: string): Promise<AnyMap> { return readResponse(await request(path)); }

function operationRoot(): HTMLElement {
  const root = document.createElement('div');
  root.className = 'qo-page';
  root.innerHTML = `
    <section class="qo-card qo-summary">
      <div class="qo-summary-head"><div class="qo-summary-copy"><a class="qo-back" href="/admin/questionnaires.html">返回问卷管理</a><h1 data-title>问卷运营配置</h1><div class="qo-code">问卷 ID · ${qid}</div></div>
      <div class="qo-actions"><button class="qo-button qo-button--secondary" data-copy>复制公开地址</button><a class="qo-button qo-button--secondary" data-open target="_blank" rel="noopener">打开公开页</a><button class="qo-button qo-button--primary" data-save-current>保存当前维度</button></div></div>
      <div class="qo-summary-grid"><div class="qo-metric"><span>问卷状态</span><strong data-status>—</strong></div><div class="qo-metric"><span>提交量</span><strong data-count>0</strong></div><div class="qo-metric"><span>提交后动作</span><strong data-completion-summary>未启用</strong></div><div class="qo-metric"><span>外部推送</span><strong data-push-summary>未启用</strong></div></div>
      <div class="qo-toast" data-toast role="status" aria-live="polite"></div>
    </section>
    <section class="qo-workspace">
      <nav class="qo-card qo-nav"><button class="is-active" data-tab="completion"><span class="qo-index">1</span><span>提交后动作</span></button><button data-tab="push"><span class="qo-index">2</span><span>外部推送</span></button></nav>
      <div class="qo-card qo-panels">
        <section class="qo-panel is-active" data-panel="completion">
          <div class="qo-panel-head"><div><h2>提交后动作</h2><p>仅在提交成功，或已通过身份机制识别为提交过时执行。</p></div><label class="qo-switch-line"><span data-completion-enabled-label>未启用</span><input type="checkbox" data-completion-enabled role="switch"></label></div>
          <div class="qo-panel-body" data-completion-body hidden>
            <div class="qo-option-grid"><button class="qo-option is-active" data-mode="lead_qr"><span class="qo-radio"></span><span><strong>展示渠道二维码</strong><small>提交完成后展示所绑定渠道的实时二维码。</small></span></button><button class="qo-option" data-mode="redirect"><span class="qo-radio"></span><span><strong>直接跳转</strong><small>支持 H5 或小程序动态 URL Link。</small></span></button></div>
            <div class="qo-config-card" data-qr-fields>
              <label class="qo-field"><span>绑定渠道码</span><select id="qo-lead-channel" data-channel><option value="">正在加载可用渠道…</option></select><small>仅可选择启用中、二维码载体且已有可用二维码的渠道。</small></label>
              <div class="qo-field-grid"><label class="qo-field"><span>二维码主标题</span><input data-qr-title maxlength="40" placeholder="留空沿用渠道名称或“扫码继续”"></label>
              <label class="qo-field"><span>二维码副标题</span><input data-qr-subtitle maxlength="100" placeholder="留空沿用现有长按识别提示"></label></div>
              <div class="qo-channel-preview" data-channel-preview hidden><img data-channel-preview-image alt="渠道二维码预览"><div><strong data-channel-preview-name></strong><small>展示时实时解析渠道当前二维码</small></div></div>
            </div>
            <div class="qo-config-card" data-redirect-fields hidden>
              <div class="qo-field-grid"><label class="qo-field"><span>跳转类型</span><select data-target-type><option value="h5">H5 跳转地址</option><option value="url_link">动态 URL Link 接口</option></select></label>
              <label class="qo-field qo-field--full" data-h5><span>H5 跳转地址</span><input data-h5-url placeholder="https://example.com/landing 或 /internal/path"></label>
              <label class="qo-field qo-field--full" data-url-link hidden><span>动态 URL Link 接口</span><input data-source-url placeholder="https://example.com/api/wxlink"><small>提交成功后请求该接口，并从响应中读取微信官方 URL Link。</small></label>
              <label class="qo-field" data-url-link hidden><span>响应字段</span><input data-response-key value="url_link" placeholder="url_link"></label></div>
            </div>
          </div>
          <div class="qo-panel-actions"><button class="qo-button qo-button--primary" data-save-completion>保存提交后动作</button></div>
        </section>
        <section class="qo-panel" data-panel="push" hidden>
          <div class="qo-panel-head"><div><h2>外部推送</h2><p>提交完成后仅入 External Effect 异步队列，不在提交请求中直连 Webhook。</p></div><label class="qo-switch-line"><span data-push-enabled-label>未启用</span><input type="checkbox" data-push-enabled role="switch"></label></div>
          <div class="qo-field-grid" data-push-fields>
            <label class="qo-field qo-field--full"><span>Webhook 地址</span><input id="qo-push-url" type="url" data-webhook placeholder="https://hooks.example.com/questionnaire"></label>
            <label class="qo-field"><span>订阅类型</span><select data-push-type><option value="">不传</option><option value="subscription">subscription</option><option value="premium">premium</option><option value="trial">trial</option></select></label>
            <label class="qo-field"><span>到期时间（秒级时间戳）</span><input type="number" min="0" step="1" data-expires></label>
            <label class="qo-field"><span>服务周期（天）</span><input type="number" min="0" step="1" data-day></label>
            <label class="qo-field"><span>频率</span><input type="number" min="0" step="1" data-frequency></label>
            <label class="qo-field qo-field--full"><span>备注</span><textarea rows="3" data-remark placeholder="推送用途说明"></textarea></label>
          </div>
          <div class="qo-subsection-head"><div><strong>自定义参数</strong><small>随测试与正式异步推送一并发送。</small></div><button class="qo-button qo-button--secondary" data-add-param>添加参数</button></div>
          <div class="qo-param-list" data-params></div>
          <div class="qo-capability" data-capability></div>
          <div class="qo-panel-actions qo-panel-actions--split"><a class="qo-button qo-button--secondary" data-logs>查看推送日志</a><div><button class="qo-button qo-button--secondary" data-test>测试推送</button><button class="qo-button qo-button--primary" data-save-push>保存外部推送</button></div></div>
        </section>
      </div>
    </section>`;
  return root;
}

function el<T extends Element>(root: ParentNode, selector: string): T { const found = root.querySelector<T>(selector); if (!found) throw new Error(`missing ${selector}`); return found; }
function input(root: ParentNode, selector: string): HTMLInputElement { return el(root, selector); }
function numberOrNull(value: string): number | null { return value.trim() === '' ? null : Number(value); }
function toast(root: HTMLElement, message: string, failed = false): void { const node = el<HTMLElement>(root, '[data-toast]'); node.textContent = message; node.classList.toggle('is-error', failed); }
function addParam(root: HTMLElement, name = '', value = ''): void { const row = document.createElement('div'); row.className = 'qo-param-row'; row.innerHTML = '<input data-param-name placeholder="参数名"><input data-param-value placeholder="参数值"><button class="qo-button qo-button--secondary" type="button">删除</button>'; input(row, '[data-param-name]').value = name; input(row, '[data-param-value]').value = value; el<HTMLButtonElement>(row, 'button').onclick = () => row.remove(); el(root, '[data-params]').append(row); }

async function mount(): Promise<void> {
  const stage = document.getElementById('stage'); if (!stage || !Number.isSafeInteger(qid) || qid < 1) return;
  const root = operationRoot(); stage.replaceChildren(root);
  const [detailRaw, opsRaw, channelsRaw] = await Promise.all([read(`/api/admin/questionnaires/${qid}`), read(`/api/admin/questionnaires/${qid}/operations`), read('/api/admin/channels?limit=100&status=active')]);
  const detailCarrier = object(detailRaw); const detail = object(detailCarrier.questionnaire || detailCarrier); let ops = object(opsRaw); const completion = object(ops.completion); const push = object(ops.external_push); const target = object(completion.completion_target); const metadata = object(push.metadata);
  const title = text(detail.title || detail.name) || `问卷 ${qid}`; el<HTMLElement>(root, '[data-title]').textContent = title; el<HTMLElement>(root, '[data-status]').textContent = detail.is_disabled ? '已停用' : '启用中'; el<HTMLElement>(root, '[data-count]').textContent = text(detail.submission_count || 0);
  const publicPath = text(detail.public_path) || `/q/${encodeURIComponent(text(detail.slug))}`; el<HTMLAnchorElement>(root, '[data-open]').href = publicPath; el<HTMLAnchorElement>(root, '[data-logs]').href = `/admin/questionnaires/${qid}/external-push-logs`;
  let mode = completion.mode === 'redirect' ? 'redirect' : 'lead_qr'; input(root, '[data-completion-enabled]').checked = completion.enabled === true; input(root, '[data-qr-title]').value = text(completion.lead_qr_title); input(root, '[data-qr-subtitle]').value = text(completion.lead_qr_subtitle); input(root, '[data-h5-url]').value = text(target.h5_url); input(root, '[data-source-url]').value = text(target.source_url); input(root, '[data-response-key]').value = text(target.response_url_key) || 'url_link'; el<HTMLSelectElement>(root, '[data-target-type]').value = target.target_type === 'url_link' ? 'url_link' : 'h5';
  const channel = el<HTMLSelectElement>(root, '[data-channel]');
  const channels = list(object(channelsRaw).channels || object(channelsRaw).items).map(item => { const id = Number(item.channel_id || item.id); const qr = text(item.active_qrcode_asset_url || item.qr_url); const carrier = text(item.carrier_type) || (item.channel_type === 'wecom_customer_acquisition' ? 'link' : 'qrcode'); const qrStatus = text(item.qrcode_status) || (qr ? 'legacy_untracked' : 'not_generated'); return { id, name: text(item.channel_name || item.name) || `渠道 ${id}`, qr, selectable: Number.isSafeInteger(id) && id > 0 && (text(item.status) || 'active') === 'active' && carrier === 'qrcode' && Number(item.qrcode_asset_id || item.active_qrcode_asset_id || 0) > 0 && ['active', 'generated', 'legacy_untracked'].includes(qrStatus) && qr.startsWith('https://') }; }).filter(item => Number.isSafeInteger(item.id) && item.id > 0);
  channel.replaceChildren(Object.assign(document.createElement('option'), { value: '', textContent: channels.length ? '请选择渠道码' : '暂无可用二维码渠道' })); for (const item of channels) { const option = document.createElement('option'); option.value = String(item.id); option.textContent = item.name + (item.selectable ? '' : ' · 暂不可用'); option.disabled = !item.selectable; channel.append(option); } channel.value = text(completion.lead_channel_id || completion.channel_id);
  input(root, '[data-push-enabled]').checked = push.enabled === true; input(root, '[data-webhook]').value = text(push.webhook_url); el<HTMLSelectElement>(root, '[data-push-type]').value = text(push.type || metadata.type); input(root, '[data-expires]').value = push.expires_at_ts ?? metadata.expires_at_ts ?? ''; input(root, '[data-day]').value = push.day ?? metadata.day ?? ''; input(root, '[data-frequency]').value = push.frequency ?? metadata.frequency ?? ''; el<HTMLTextAreaElement>(root, '[data-remark]').value = text(push.remark || metadata.remark);
  const params = object(push.custom_params || metadata.custom_params); Object.entries(params).forEach(([name, value]) => addParam(root, name, text(value))); if (!Object.keys(params).length) addParam(root);
  const sync = (): void => { const completionOn = input(root, '[data-completion-enabled]').checked; el<HTMLElement>(root, '[data-completion-body]').hidden = !completionOn; el<HTMLElement>(root, '[data-completion-enabled-label]').textContent = completionOn ? '已启用' : '未启用'; el<HTMLElement>(root, '[data-completion-summary]').textContent = !completionOn ? '未启用' : mode === 'redirect' ? '直接跳转' : '渠道二维码'; el<HTMLElement>(root, '[data-qr-fields]').hidden = !completionOn || mode !== 'lead_qr'; el<HTMLElement>(root, '[data-redirect-fields]').hidden = !completionOn || mode !== 'redirect'; root.querySelectorAll<HTMLElement>('[data-mode]').forEach(node => node.classList.toggle('is-active', node.dataset.mode === mode)); const link = el<HTMLSelectElement>(root, '[data-target-type]').value === 'url_link'; root.querySelectorAll<HTMLElement>('[data-url-link]').forEach(node => node.hidden = !link); el<HTMLElement>(root, '[data-h5]').hidden = link; const pushOn = input(root, '[data-push-enabled]').checked; el<HTMLElement>(root, '[data-push-enabled-label]').textContent = pushOn ? '已启用' : '未启用'; el<HTMLElement>(root, '[data-push-summary]').textContent = pushOn ? '已启用' : '未启用'; const capability = el<HTMLElement>(root, '[data-capability]'); capability.textContent = ops.provider_enabled ? '外推能力已启用；测试操作仅排队，不会在当前请求中外呼。' : '外推能力当前不可测试：能力未启用'; capability.classList.toggle('is-ok', ops.provider_enabled === true); el<HTMLButtonElement>(root, '[data-test]').disabled = ops.provider_enabled !== true; const selected = channels.find(item => item.id === Number(channel.value) && item.selectable); const preview = el<HTMLElement>(root, '[data-channel-preview]'); preview.hidden = !selected; if (selected) { el<HTMLImageElement>(root, '[data-channel-preview-image]').src = selected.qr; el<HTMLElement>(root, '[data-channel-preview-name]').textContent = selected.name; } };
  const saveCompletion = async (): Promise<void> => { const enabled = input(root, '[data-completion-enabled]').checked; const payload = !enabled ? { enabled: false } : mode === 'lead_qr' ? { enabled: true, action_type: 'lead_qr', lead_channel_id: Number(channel.value), lead_qr_title: input(root, '[data-qr-title]').value.trim(), lead_qr_subtitle: input(root, '[data-qr-subtitle]').value.trim() } : { enabled: true, action_type: 'redirect', completion_target: { enabled: true, target_type: el<HTMLSelectElement>(root, '[data-target-type]').value, h5_url: input(root, '[data-h5-url]').value.trim(), source_url: input(root, '[data-source-url]').value.trim(), response_url_key: input(root, '[data-response-key]').value.trim() || 'url_link' } }; if (enabled && mode === 'lead_qr' && !Number(channel.value)) throw new Error('请选择一个可用渠道码'); toast(root, '正在保存提交后动作…'); ops = await write(`/api/admin/questionnaires/${qid}/operations/completion`, 'PUT', payload); toast(root, '提交后动作已保存'); };
  const pushPayload = (): AnyMap => { const custom_params: AnyMap = {}; root.querySelectorAll<HTMLElement>('.qo-param-row').forEach(row => { const name = input(row, '[data-param-name]').value.trim(); if (name) custom_params[name] = input(row, '[data-param-value]').value; }); return { enabled: input(root, '[data-push-enabled]').checked, webhook_url: input(root, '[data-webhook]').value.trim(), type: el<HTMLSelectElement>(root, '[data-push-type]').value, expires_at_ts: numberOrNull(input(root, '[data-expires]').value), day: numberOrNull(input(root, '[data-day]').value), frequency: numberOrNull(input(root, '[data-frequency]').value), remark: el<HTMLTextAreaElement>(root, '[data-remark]').value.trim(), custom_params, configuration_version: Number(ops.configuration_version || 0) }; };
  const savePush = async (): Promise<void> => { const payload = pushPayload(); if (payload.enabled && !payload.webhook_url) throw new Error('启用外部推送时必须填写 Webhook 地址'); toast(root, '正在保存外部推送…'); ops = await write(`/api/admin/questionnaires/${qid}/operations/external-push`, 'PUT', payload); toast(root, '外部推送已保存'); };
  root.querySelectorAll<HTMLButtonElement>('[data-tab]').forEach(button => button.onclick = () => { root.querySelectorAll<HTMLButtonElement>('[data-tab]').forEach(node => node.classList.toggle('is-active', node === button)); root.querySelectorAll<HTMLElement>('[data-panel]').forEach(panel => { const active = panel.dataset.panel === button.dataset.tab; panel.hidden = !active; panel.classList.toggle('is-active', active); }); }); root.querySelectorAll<HTMLButtonElement>('[data-mode]').forEach(button => button.onclick = () => { mode = button.dataset.mode === 'redirect' ? 'redirect' : 'lead_qr'; sync(); }); input(root, '[data-completion-enabled]').onchange = sync; input(root, '[data-push-enabled]').onchange = sync; channel.onchange = sync; el<HTMLSelectElement>(root, '[data-target-type]').onchange = sync; el<HTMLButtonElement>(root, '[data-add-param]').onclick = () => addParam(root); el<HTMLButtonElement>(root, '[data-save-completion]').onclick = () => void saveCompletion().catch(error => toast(root, error.message || '保存失败', true)); el<HTMLButtonElement>(root, '[data-save-push]').onclick = () => void savePush().catch(error => toast(root, error.message || '保存失败', true)); el<HTMLButtonElement>(root, '[data-save-current]').onclick = () => void (el<HTMLButtonElement>(root, '[data-tab].is-active').dataset.tab === 'push' ? savePush() : saveCompletion()).catch(error => toast(root, error.message || '保存失败', true)); el<HTMLButtonElement>(root, '[data-test]').onclick = () => void savePush().then(() => write(`/api/admin/questionnaires/${qid}/operations/external-push/test`, 'POST')).then(receipt => toast(root, `测试推送已排队 · ${text(receipt.test_run_id)}`)).catch(error => toast(root, error.message || '测试失败', true)); el<HTMLButtonElement>(root, '[data-copy]').onclick = () => void navigator.clipboard.writeText(new URL(publicPath, location.origin).href).then(() => toast(root, '公开地址已复制')).catch(() => toast(root, '复制失败，请使用“打开公开页”', true)); sync();
}

if (page === 'questionnaireOps') {
  const start = (): void => { let attempts = 0; const timer = window.setInterval(() => { const stage = document.getElementById('stage'); if (stage && !stage.querySelector('[data-surface-placeholder]') || attempts++ > 100) { clearInterval(timer); void mount().catch(error => { const stage = document.getElementById('stage'); if (stage) stage.textContent = error instanceof Error ? error.message : '问卷运营配置加载失败'; }); } }, 25); };
  document.readyState === 'loading' ? document.addEventListener('DOMContentLoaded', start, { once: true }) : start();
}
