import { formatShanghaiDateTime, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime';
import { installCommittedTextSearch } from './shared/ui/committedTextSearch';

installCommittedTextSearch();

type ClientSummary = {
  client_id: string;
  display_name: string;
  purpose: string;
  credential_hint?: string;
  audiences: string[];
  scopes: string[];
  capabilities: string[];
  allowed_cidrs: string[] | null;
  owner_scope?: Record<string, string[]>;
  token_ttl_seconds: number;
  expires_at?: string;
  enabled: boolean;
  reissue_required: boolean;
  auth_version: number;
  last_used_at?: string;
  created_at: string;
};

type AuditEntry = {
  actor_admin_user_id?: number;
  action: string;
  outcome: string;
  details: unknown;
  created_at: string;
};

type OperationDescriptor = {
  operation_id: string;
  rest_method: string;
  rest_path: string;
  mcp_tool: string;
  capability: string;
  required_scope: string;
  schema_version: string;
};

type IssuedSecret = { clientID: string; secret: string };

class RequestError extends Error {
  constructor(readonly status: number, readonly code: string) {
    super(code);
  }
}

const V1_PURPOSE = 'external_agent';
const V1_AUDIENCE = 'external_integration';
const KNOWN_SCOPES = ['read', 'write'];
const title = '开放平台调用方';

function auditActionLabel(value: string): string {
  return ({
    machine_client_created: '创建调用方', machine_client_rotated: '轮换密钥', machine_client_enabled: '更新启用状态',
    machine_client_updated: '更新调用方', machine_client_grants_updated: '更新授权范围', machine_client_activated: '确认启用调用方',
    machine_token_issued: '签发访问令牌', direct_key_authenticated: '直接密钥认证', machine_client_imported: '导入历史调用方',
  } as Record<string, string>)[value] || '审计操作待确认';
}

function auditOutcomeLabel(value: string): string {
  return ({
    succeeded: '已完成', enabled: '已启用', disabled: '已停用', revoked_prior_bearers: '已撤销旧访问凭据',
    unchanged: '没有变更', imported: '已导入', replayed: '已按原记录核对',
  } as Record<string, string>)[value] || '审计结果待确认';
}

function cookie(name: string): string {
  const prefix = `${name}=`;
  for (const entry of document.cookie.split(';')) {
    const item = entry.trim();
    if (item.startsWith(prefix)) {
      try {
        return decodeURIComponent(item.slice(prefix.length));
      } catch {
        return '';
      }
    }
  }
  return '';
}

function csrf(): string {
  return cookie('aicrm_admin_csrf') || cookie('aicrm_csrf');
}

function element<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(label: string, onClick: () => void | Promise<void>, kind = 'secondary'): HTMLButtonElement {
  const node = element('button', label);
  node.type = 'button';
  node.dataset.openPlatformAction = label;
  node.className = `open-platform-button open-platform-button-${kind}`;
  node.addEventListener('click', () => { void onClick(); });
  return node;
}

function field(label: string, input: HTMLElement, note?: string): HTMLDivElement {
  const wrapper = element('div');
  wrapper.className = 'open-platform-field';
  const heading = element('span', label);
  heading.className = 'open-platform-field-label';
  wrapper.append(heading, input);
  if (note) {
    const help = element('small', note);
    help.className = 'open-platform-field-note';
    wrapper.append(help);
  }
  return wrapper;
}

function textInput(value = '', type = 'text'): HTMLInputElement {
  const node = document.createElement('input');
  node.type = type;
  node.value = value;
  node.className = 'open-platform-input';
  return node;
}

function textArea(value = ''): HTMLTextAreaElement {
  const node = document.createElement('textarea');
  node.value = value;
  node.className = 'open-platform-textarea';
  node.rows = 3;
  return node;
}

function checkList(values: string[], selected: string[], name: string): HTMLDivElement {
  const result = element('div');
  result.className = 'open-platform-checklist';
  for (const value of values) {
    const row = element('label');
    row.className = 'open-platform-check';
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.name = name;
    input.value = value;
    input.checked = selected.includes(value);
    row.append(input, document.createTextNode(value));
    result.append(row);
  }
  return result;
}

function checkedValues(container: ParentNode, name: string): string[] {
  return [...container.querySelectorAll<HTMLInputElement>(`input[name="${name}"]:checked`)].map((entry) => entry.value).sort();
}

function cidrs(value: string): string[] {
  return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean);
}

function nullableStrings(value: string[] | null | undefined): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

function validTTL(value: string): number | undefined {
  const ttl = Number(value.trim());
  return Number.isInteger(ttl) && ttl >= 60 && ttl <= 3600 ? ttl : undefined;
}

function safeOwnerScope(value: string): Record<string, string[]> | null {
  const source = value.trim();
  if (!source) return null;
  const parsed: unknown = JSON.parse(source);
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) throw new Error('invalid_owner_scope');
  return parsed as Record<string, string[]>;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  const method = (init.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    headers.set('Content-Type', 'application/json');
    const token = csrf();
    if (token) headers.set('X-CSRF-Token', token);
  }
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' });
  const body: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const code = body && typeof body === 'object' && 'error' in body && typeof body.error === 'string' ? body.error : 'request_failed';
    throw new RequestError(response.status, code);
  }
  return body as T;
}

function dateTimeLocalValue(value?: string): string {
  const formatted = formatShanghaiDateTime(value);
  return /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(formatted) ? formatted.replace(' ', 'T') : '';
}

function comparableShanghaiDateTimeLocal(value: string): string {
  const converted = shanghaiDateTimeLocalToRFC3339(value);
  return converted ? dateTimeLocalValue(converted) : '';
}

function unchangedDateTimeLocal(value: string, initial: string): boolean {
  const current = comparableShanghaiDateTimeLocal(value);
  const original = comparableShanghaiDateTimeLocal(initial);
  return Boolean(current && original && current === original);
}

function formatTime(value?: string): string {
  return value ? formatShanghaiDateTime(value) : '未设置';
}

function exactV1Capabilities(items: OperationDescriptor[]): string[] {
  return [...new Set(items.map((item) => item.capability).filter(Boolean))].sort();
}

function styles(): HTMLStyleElement {
  const style = element('style');
  style.textContent = `
    body[data-page="apidocs"] #stage.stage.rich{padding:0;overflow:auto}
    .open-platform-root{box-sizing:border-box;min-height:100%;background:#f6f7f9;color:#1f2329;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    .open-platform-header,.open-platform-card{background:#fff;border:1px solid #dee0e3;border-radius:8px}
    .open-platform-header{display:flex;align-items:center;justify-content:space-between;gap:16px;min-height:52px;padding:7px 20px;margin:0;border:0;border-bottom:1px solid #dee0e3;border-radius:0}.open-platform-header h1{font-size:16px;line-height:22px;margin:0}.open-platform-muted{color:#646a73;font-size:13px}
    .open-platform-layout{display:grid;grid-template-columns:minmax(280px,34%) minmax(0,1fr);gap:16px;padding:16px 20px 36px}.open-platform-card{padding:16px;min-width:0}.open-platform-card h2{font-size:15px;margin:0 0 12px}.open-platform-list{display:grid;gap:8px}.open-platform-client{width:100%;text-align:left;border:1px solid #dee0e3;border-radius:6px;background:#fff;padding:10px;cursor:pointer}.open-platform-client[aria-current="true"]{border-color:#3370ff;background:#eff4ff}.open-platform-client strong,.open-platform-client small{display:block}.open-platform-client small{color:#646a73;margin-top:2px}
    .open-platform-form{display:grid;gap:12px}.open-platform-field{display:grid;gap:5px}.open-platform-field-label{font-weight:600}.open-platform-field-note{color:#646a73}.open-platform-input,.open-platform-textarea{box-sizing:border-box;width:100%;border:1px solid #bcc0c6;border-radius:5px;padding:7px;font:inherit;background:#fff}.open-platform-textarea{resize:vertical}.open-platform-checklist{display:flex;flex-wrap:wrap;gap:8px}.open-platform-check{border:1px solid #dee0e3;border-radius:4px;padding:4px 7px;display:inline-flex;gap:5px;align-items:center;font-size:12px}.open-platform-actions{display:flex;gap:8px;flex-wrap:wrap}.open-platform-button{border:1px solid #bcc0c6;border-radius:5px;background:#fff;padding:7px 10px;cursor:pointer;font:inherit}.open-platform-button-primary{background:#3370ff;border-color:#3370ff;color:#fff}.open-platform-button-danger{color:#c9352b;border-color:#d83931}.open-platform-status{min-height:20px;color:#646a73}.open-platform-status[data-error="true"]{color:#d83931}.open-platform-table{border-collapse:collapse;width:100%;font-size:12px}.open-platform-table th,.open-platform-table td{padding:8px;border-bottom:1px solid #eff0f1;text-align:left;vertical-align:top;word-break:break-word}.open-platform-table th{color:#646a73;font-weight:600}.open-platform-secret{white-space:pre-wrap;word-break:break-all;padding:10px;border-radius:6px;background:#f5f6f7;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.open-platform-dialog{border:0;border-radius:8px;box-shadow:0 12px 48px #0004;max-width:560px}.open-platform-dialog::backdrop{background:#0006}.open-platform-dialog-body{display:grid;gap:12px;min-width:min(460px,80vw)}.open-platform-catalog{margin-top:16px}.open-platform-code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.open-platform-empty{padding:16px;color:#646a73}
    .open-platform-docs{max-width:1180px;margin:0 auto;padding:28px 28px 56px}.open-platform-docs-header{display:flex;align-items:flex-start;justify-content:space-between;gap:20px;margin-bottom:24px}.open-platform-docs-title{font-size:26px;line-height:1.25;margin:0 0 8px}.open-platform-docs-subtitle{margin:0;color:#646a73;max-width:760px}.open-platform-tabs{display:flex;gap:4px;border-bottom:1px solid #dee0e3;margin-bottom:22px}.open-platform-tab{appearance:none;border:0;border-bottom:2px solid transparent;background:transparent;padding:10px 12px;color:#646a73;font:inherit;cursor:pointer}.open-platform-tab[aria-current="page"]{color:#3370ff;border-bottom-color:#3370ff;font-weight:600}.open-platform-docs-grid{display:grid;grid-template-columns:210px minmax(0,1fr);gap:28px}.open-platform-docs-toc{position:sticky;top:12px;align-self:start;display:grid;gap:3px}.open-platform-docs-toc a{color:#646a73;text-decoration:none;padding:5px 8px;border-radius:4px}.open-platform-docs-toc a:hover{background:#f2f3f5;color:#1f2329}.open-platform-docs-main{min-width:0}.open-platform-search{box-sizing:border-box;width:100%;border:1px solid #bcc0c6;border-radius:6px;padding:9px 11px;font:inherit;margin:0 0 14px}.open-platform-doc-section{padding:22px 0;border-bottom:1px solid #eff0f1;scroll-margin-top:12px}.open-platform-doc-section h2{font-size:18px;margin:0 0 10px}.open-platform-doc-section h3{font-size:15px;margin:18px 0 8px}.open-platform-doc-section p,.open-platform-doc-section li{color:#3f444d}.open-platform-doc-section ul{padding-left:20px}.open-platform-doc-card{border:1px solid #dee0e3;border-radius:8px;background:#fff;padding:14px;margin:12px 0}.open-platform-doc-note{border-left:3px solid #3370ff;background:#eff4ff;padding:10px 12px;color:#3f444d}.open-platform-doc-warning{border-left-color:#d83931;background:#fff1f0}.open-platform-doc-code{position:relative;margin:10px 0;overflow:auto;border-radius:7px;background:#1f2329;color:#eff4ff;padding:14px 44px 14px 14px;font:12px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre}.open-platform-copy{position:absolute;right:8px;top:8px;border:1px solid #6b7480;border-radius:4px;background:#303640;color:#fff;padding:4px 7px;font:12px inherit;cursor:pointer}.open-platform-doc-table{border-collapse:collapse;width:100%;font-size:13px}.open-platform-doc-table th,.open-platform-doc-table td{padding:9px;border-bottom:1px solid #eff0f1;text-align:left;vertical-align:top}.open-platform-doc-table th{font-weight:600;color:#646a73;background:#fafafa}.open-platform-method{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#3370ff;font-size:12px}.open-platform-management-banner{margin:16px 20px 0;border:1px solid #ffe0b2;background:#fff7e6;color:#8a5a00;border-radius:6px;padding:10px 12px}.open-platform-management-banner a{color:inherit;font-weight:600}.open-platform-retry{margin-left:8px}.open-platform-disabled{opacity:.65;pointer-events:none}
    @media (max-width:800px){.open-platform-layout{grid-template-columns:1fr;padding:12px}.open-platform-docs{padding:20px 16px 40px}.open-platform-docs-header{display:block}.open-platform-docs-grid{grid-template-columns:1fr}.open-platform-docs-toc{position:static;grid-template-columns:repeat(2,minmax(0,1fr));margin-bottom:6px}}
  `;
  return style;
}

function setStatus(node: HTMLElement, message = '', error = false): void {
  node.textContent = message;
  node.dataset.error = String(error);
}

function secretDialog(issued: IssuedSecret, onActivate: () => Promise<void>, onRefresh: () => void | Promise<void>, onClose: () => void | Promise<void>): HTMLDialogElement {
  const dialog = document.createElement('dialog');
  dialog.className = 'open-platform-dialog';
  dialog.dataset.openPlatformSecret = issued.clientID;
  const body = element('div');
  body.className = 'open-platform-dialog-body';
  body.append(element('h2', '复制调用方密钥'));
  body.append(element('p', '此密钥只在本次创建或轮换后展示。复制并确认后才能启用调用方。'));
  const secret = element('code', issued.secret);
  secret.className = 'open-platform-secret';
  body.append(secret);
  const message = element('p');
  message.className = 'open-platform-status';
  body.append(message);
  const actions = element('div');
  actions.className = 'open-platform-actions';
  let activationUnconfirmed = false;
  const activate = async (): Promise<void> => {
    if (activationUnconfirmed) return;
    try {
      await onActivate();
      dialog.close();
    } catch {
      // A transport failure can arrive after the server committed activation.
      // Refresh the owner projection, keep this one-time secret unavailable for
      // retries, and never describe an unknown result as disabled.
      activationUnconfirmed = true;
      setStatus(message, '未确认启用结果，请刷新核对状态。', true);
      void Promise.resolve(onRefresh());
    }
  };
  actions.append(button('复制并确认启用', async () => {
    if (!navigator.clipboard || typeof navigator.clipboard.writeText !== 'function') {
      setStatus(message, '当前浏览器无法安全复制；请手动复制后再确认启用。', true);
      return;
    }
    try {
      await navigator.clipboard.writeText(issued.secret);
    } catch {
      setStatus(message, '复制失败；调用方仍保持停用。', true);
      return;
    }
    await activate();
  }, 'primary'));
  actions.append(button('我已手动复制并确认启用', activate));
  actions.append(button('关闭并清除密钥', () => dialog.close()));
  body.append(actions);
  dialog.append(body);
  dialog.addEventListener('close', () => {
    secret.textContent = '';
    void Promise.resolve(onClose());
    dialog.remove();
  }, { once: true });
  document.body.append(dialog);
  dialog.showModal();
  return dialog;
}

function codeExample(value: string): HTMLElement {
  const block = element('pre', value);
  block.className = 'open-platform-doc-code';
  const copy = button('复制', async () => {
    if (!navigator.clipboard?.writeText) {
      copy.textContent = '复制不可用';
      window.setTimeout(() => { copy.textContent = '复制'; }, 1800);
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      copy.textContent = '已复制';
    } catch {
      copy.textContent = '复制失败';
    }
    window.setTimeout(() => { copy.textContent = '复制'; }, 1200);
  });
  copy.className = 'open-platform-copy';
  block.append(copy);
  return block;
}

function docSection(id: string, title: string): HTMLElement {
  const section = element('section');
  section.id = id;
  section.className = 'open-platform-doc-section';
  section.dataset.apiDocSection = id;
  section.append(element('h2', title));
  return section;
}

function docTable(headers: string[], rows: string[][]): HTMLTableElement {
  const table = element('table');
  table.className = 'open-platform-doc-table';
  const head = element('thead');
  const header = element('tr');
  for (const value of headers) header.append(element('th', value));
  head.append(header);
  const body = element('tbody');
  for (const values of rows) {
    const row = element('tr');
    for (const value of values) row.append(element('td', value));
    body.append(row);
  }
  table.append(head, body);
  return table;
}

function staticDocumentation(onClients: () => void): HTMLElement {
  const root = element('section');
  root.className = 'open-platform-root open-platform-docs';
  root.dataset.openPlatformDocs = 'v1';
  const header = element('header');
  header.className = 'open-platform-docs-header';
  const lead = element('div');
  lead.append(element('h1', 'AI-CRM 外部只读 API v1'));
  lead.querySelector('h1')!.className = 'open-platform-docs-title';
  const subtitle = element('p', '面向已授权外部工作台的稳定只读合同。按快速接入取得短期 Token，再按 capability 目录开始只读查询。');
  subtitle.className = 'open-platform-docs-subtitle';
  lead.append(subtitle);
  header.append(lead, button('密钥管理', onClients, 'primary'));
  root.append(header);

  const tabs = element('nav');
  tabs.className = 'open-platform-tabs';
  const docsTab = button('接口文档', () => {});
  docsTab.className = 'open-platform-tab'; docsTab.setAttribute('aria-current', 'page');
  const clientsTab = button('密钥管理', onClients);
  clientsTab.className = 'open-platform-tab';
  tabs.append(docsTab, clientsTab); root.append(tabs);

  const search = textInput('', 'search');
  search.placeholder = '搜索接口、字段或错误码';
  search.className = 'open-platform-search';
  search.setAttribute('aria-label', '搜索 API 文档');
  root.append(search);
  const searchEmpty = element('p', '没有匹配的接口、字段或错误码。');
  searchEmpty.className = 'open-platform-empty';
  searchEmpty.hidden = true;
  root.append(searchEmpty);

  const layout = element('div'); layout.className = 'open-platform-docs-grid';
  const toc = element('nav'); toc.className = 'open-platform-docs-toc'; toc.setAttribute('aria-label', 'API 文档目录');
  const main = element('main'); main.className = 'open-platform-docs-main';
  const links: Array<[string, string]> = [['quickstart', '快速接入'], ['operations', '只读接口'], ['audience', '人群包接口'], ['oneid', 'OneID'], ['pagination', '分页'], ['orders', '订单与退款'], ['errors', '错误码'], ['testing', '调用前验证']];
  for (const [id, label] of links) {
    const anchor = element('a', label); anchor.href = `#${id}`; toc.append(anchor);
  }

  const quickstart = docSection('quickstart', '快速 OAuth 接入');
  quickstart.append(element('p', '使用一个已激活、只含 read scope 的专用调用方换取短期 Bearer Token。不要把 client secret、access token、用户 ID 或真实响应写入命令历史、静态文档或测试输出。'));
  quickstart.append(codeExample(`export AICRM_BASE_URL='https://<deployed-aicrm-host>'\nexport AICRM_CLIENT_ID='<dedicated-client-id>'\nexport AICRM_CLIENT_SECRET='<secret-from-secret-store>'\n\nTOKEN_RESPONSE="$(curl --fail-with-body --silent --show-error \\\n  --user \"$AICRM_CLIENT_ID:$AICRM_CLIENT_SECRET\" \\\n  -H 'Content-Type: application/x-www-form-urlencoded' \\\n  --data-urlencode 'grant_type=client_credentials' \\\n  --data-urlencode 'audience=external_integration' \\\n  --data-urlencode 'scope=read' \\\n  \"$AICRM_BASE_URL/oauth/token\")"\nexport AICRM_ACCESS_TOKEN="$(jq -er '.access_token' <<<\"$TOKEN_RESPONSE\")"`));
  const openapi = element('a', '下载已认证 OpenAPI YAML'); openapi.href = '/api/admin/config/openapi.yaml'; openapi.className = 'open-platform-button'; openapi.download = 'aicrm-openapi.yaml'; quickstart.append(openapi);
  const quickNote = element('p', '在“密钥管理”中创建调用方、确认一次性密钥、激活、轮换、停用和查看审计。创建或轮换后密钥只展示一次；复制确认前调用方保持停用。'); quickNote.className = 'open-platform-doc-note'; quickstart.append(quickNote);

  const operations = docSection('operations', '11 个专用只读接口');
  operations.append(element('p', '下表是当前 v1 只读合同：10 个 capability 对应 11 个 operation。用户活动摘要、AI 写工作流和操作状态不属于本专用只读 profile。'));
  const operationTable = docTable(['Operation', 'REST', 'Capability'], [
    ['platform.capabilities.list', 'GET /open/v1/capabilities', 'platform.capabilities.read'],
    ['customer.resolve', 'POST /open/v1/customers:resolve', 'customer.resolve'],
    ['customer.context.get', 'GET /open/v1/customers/{customer_id}', 'customer.read'],
    ['order.list', 'GET /open/v1/orders', 'order.read'],
    ['order.get', 'GET /open/v1/orders/{order_id}', 'order.read'],
    ['identity.get', 'GET /open/v1/customers/{customer_id}/identities', 'identity.read'],
    ['questionnaire.submissions.list', 'GET /open/v1/questionnaire-submissions', 'questionnaire.read'],
    ['customer.detail.get', 'GET /open/v1/customers/{customer_id}/detail', 'customer.detail.read'],
    ['chat.records.list', 'GET /open/v1/chat-records', 'chat.read'],
    ['radar.clicks.list', 'GET /open/v1/radar/clicks', 'radar.click.read'],
    ['radar.links.list', 'GET /open/v1/radar/links', 'radar.link.read'],
  ]);
  operations.append(operationTable);
  const operationExamples: Array<[string, string, string]> = [
    ['platform.capabilities.list', '无 path、query 或 body。', '{"data":{"operations":[{"operation_id":"order.list","capability":"order.read"}]},"error":null,"request_id":"<request-id>"}'],
    ['customer.resolve', 'body.references（必填数组，1–8 项）；每项都要有非空 kind、scope、value。', '{"data":{"customer_id":"<canonical-customer-id>","identity_id":"<identity-id>","status":"found"},"error":null,"request_id":"<request-id>"}'],
    ['customer.context.get', 'path.customer_id（必填正整数）；不接受 query 或 body。', '{"data":{"customer_id":"<canonical-customer-id>","display_name":"<authorized-display-name>","status":"active"},"error":null,"request_id":"<request-id>"}'],
    ['order.list', '所有筛选均可选：provider=wechat_pay|wechat_shop|alipay；product_code、merchant_order_no、provider_transaction_no 为精确字符串；source_system 与 source_record_id 必须成对；customer_id 为正整数；created_from/to、paid_from/to 为秒级 Unix 时间（范围含边界）；is_paid、is_refunded 为 Boolean；limit 1–100（默认 100）；cursor 为最多 4096 字节 opaque 字符串。', '{"data":{"items":[{"order_id":"<order-id>","customer_id":"<canonical-customer-id>","amount_minor":19900,"amount_yuan":"199.00","is_refunded":false}],"next_cursor":"<opaque-cursor>"},"error":null,"request_id":"<request-id>"}'],
    ['order.get', 'path.order_id（必填正整数）；不接受 query 或 body。', '{"data":{"order_id":"<order-id>","items":[{"line_no":1,"product_code":"<product-code>","line_amount_minor":19900}],"refund_records":[{"refund_id":"<refund-id>","status":"completed","amount_minor":19900}],"timeline":[{"status":"paid","occurred_at":"<rfc3339-time>"}]},"error":null,"request_id":"<request-id>"}'],
    ['identity.get', 'path.customer_id（必填正整数）；unionid_scope 可重复，分别限定可读取的 UnionID 开放平台 scope。', '{"data":{"customer_id":"<canonical-customer-id>","identities":[{"kind":"phone","scope":"phone:cn11","value":"<authorized-identity-value>","assurance":"declared","status":"active"}]},"error":null,"request_id":"<request-id>"}'],
    ['questionnaire.submissions.list', 'customer_id（必填正整数）；questionnaire_id 为可选正整数（省略不按问卷筛选）；source_system 与 source_record_id 必须成对；submitted_from/to 为非负 Unix 秒，to 为排他上界；limit 1–100（默认 100）；cursor 最多 4096 字节。', '{"data":{"customer_id":"<canonical-customer-id>","items":[{"submission_id":"<submission-id>","questionnaire_id":"<questionnaire-id>","submitted_at":"<rfc3339-time>","identity_status":"resolved"}],"next_cursor":"<opaque-cursor>"},"error":null,"request_id":"<request-id>"}'],
    ['customer.detail.get', 'path.customer_id（必填正整数）；不接受 query 或 body。', '{"data":{"customer_id":"<canonical-customer-id>","business_detail_availability":{"status":"available"},"owner":{"user_id":"<staff-id>"},"follow_users":[{"user_id":"<staff-id>"}]},"error":null,"request_id":"<request-id>"}'],
    ['chat.records.list', 'customer_id（必填正整数）；chat_type=private|group（默认 private）；private 时 staff_user_id 或 staff_wecom_userid；occurred_from/to 为 Unix 秒；source_system=message_archive 与 source_record_id 必须成对；message_id 为精确字符串；limit 固定 20；cursor 最多 4096 字节。', '{"data":{"customer_id":"<canonical-customer-id>","items":[{"message_id":"<message-id>","chat_type":"private","occurred_at":"<rfc3339-time>","media_availability":"api_unavailable","staff":[{"staff_id":"<staff-id>"}]}],"next_cursor":"<opaque-cursor>"},"error":null,"request_id":"<request-id>"}'],
    ['radar.clicks.list', '所有筛选均可选：customer_id、radar_id、radar_code、session_id 为精确筛选；clicked_from/to 为 Unix 秒；limit 1–100（默认 100）；cursor 为 opaque 字符串。', '{"data":{"items":[{"click_id":"<click-id>","session_id":"<session-id>","radar_id":"<radar-id>","clicked_at":"<rfc3339-time>","identity_status":"resolved"}],"next_cursor":"<opaque-cursor>"},"error":null,"request_id":"<request-id>"}'],
    ['radar.links.list', '所有筛选均可选：radar_id、radar_code 为链接精确筛选；limit 1–100（默认 100）；cursor 为 opaque 字符串；不接受 customer_id。', '{"data":{"items":[{"link_id":"<link-id>","radar_id":"<radar-id>","radar_code":"<radar-code>","title":"<authorized-title>","is_enabled":true}],"next_cursor":"<opaque-cursor>"},"error":null,"request_id":"<request-id>"}'],
  ];
  for (const [operation, parameters, response] of operationExamples) {
    const card = element('article'); card.className = 'open-platform-doc-card';
    card.dataset.apiDocOperation = operation;
    card.append(element('h3', operation), element('p', `参数：${parameters}`), element('p', '响应字段节选（完整响应仍为 data、error、request_id envelope）：'), codeExample(JSON.stringify(JSON.parse(response), null, 2)));
    operations.append(card);
  }
  operations.append(element('p', '每次先调用 capabilities，服务端按当前 Token 的 scope、capability、CIDR 与 owner scope 返回实际可用项。不要把管理员目录 /api/admin/open-platform/routes 当作机器调用方的有效授权。'));

  const oneid = docSection('oneid', 'OneID 与安全查询');
  oneid.append(element('p', '先用 references 数组中的明确 kind、scope、value 调用 customer.resolve，再以返回的 canonical customer_id 查询订单、问卷、聊天或 Radar 点击。解析只读，不会建客、绑定或合并身份。OpenID 必须带 App scope；UnionID 必须带开放平台 scope。'));
  oneid.append(codeExample(`curl --fail-with-body --silent --show-error \\\n  -X POST \"$AICRM_BASE_URL/open/v1/customers:resolve\" \\\n  -H \"Authorization: Bearer $AICRM_ACCESS_TOKEN\" \\\n  -H 'Content-Type: application/json' \\\n  --data '{"references":[{"kind":"unionid","scope":"<wechat-open-platform-scope>","value":"<authorized-identity-value>"}]}'`));
  const oneidNote = element('p', '没有唯一可信证据时，按 pending、conflict 或 unresolved 处理；不得猜测用户归属或把空响应当成身份已打通。'); oneidNote.className = 'open-platform-doc-note'; oneid.append(oneidNote);

  const pagination = docSection('pagination', '分页与游标');
  pagination.append(element('p', '订单、问卷、Chat 与 Radar 列表使用签名 opaque cursor。首请求不带 cursor；只在响应存在 next_cursor 时原样回传。该合同没有 has_more 字段。游标与 operation、筛选、effective grant、auth version 和必要水位绑定，筛选或授权变化后应从第一页重新同步。'));
  pagination.append(codeExample(`curl --fail-with-body --silent --show-error \\\n  -H \"Authorization: Bearer $AICRM_ACCESS_TOKEN\" \\\n  \"$AICRM_BASE_URL/open/v1/orders?limit=100&cursor=<next_cursor-from-prior-response>\"`));

  const orders = docSection('orders', '订单、退款与业务数据');
  orders.append(element('p', '订单金额使用整数分 amount_minor 与两位小数字符串 amount_yuan；不要用浮点金额。payer 与 beneficiary 是不同事实，payer 优先形成 customer_id，beneficiary 不能被冒充为 payer。退款完成金额才决定 is_refunded；申请中、outcome_unknown 和 final_failed 均单独表达。'));
  const refundStatusNote = element('p', '退款金额状态合同：refund_amount_status 为必填字段。Payment 的本地退款摘要存在时返回 known；没有摘要时返回 unavailable。调用方应按完整 OpenAPI schema 校验该字段。is_refunded 仍只表示已完成退款金额；申请中、outcome_unknown 与 final_failed 的金额分别读取对应分项字段。'); refundStatusNote.className = 'open-platform-doc-note'; orders.append(refundStatusNote);
  orders.append(codeExample(`curl --fail-with-body --silent --show-error \\\n  -H \"Authorization: Bearer $AICRM_ACCESS_TOKEN\" \\\n  \"$AICRM_BASE_URL/open/v1/orders?customer_id=<canonical-customer-id>&limit=100\"`));

  const errors = docSection('errors', '错误码与处理');
  errors.append(docTable(['HTTP', 'error.code', '调用方动作'], [
    ['400', 'validation', '修正 path、query、时间范围或 cursor；重新从第一页开始。'],
    ['401', 'authentication', '重新获取 Token，并核实 Client 是否停用或刚轮换。'],
    ['403', 'permission', '核实 scope、capability、CIDR 与 owner scope；不要尝试绕过。'],
    ['404', 'not_found', '确认资源存在且在授权范围内。'],
    ['409', 'identity_pending / identity_conflict / conflict / outcome_unknown', '保留不确定状态，不要当作空数组、自动绑定或完成结果。'],
    ['429', 'rate_limited', '遵守限流并退避。'],
    ['503', 'dependency_unavailable', '稍后重试并保留 request_id 供排查。'],
  ]));
  errors.append(element('p', '成功 envelope 为 { data, error: null, request_id }；失败 envelope 为 { data: null, error: { code }, request_id }，同时读取 X-Request-ID。'));

  const testing = docSection('testing', '调用前验证');
  testing.append(element('p', '在测试环境依次确认当前 Token 的 capabilities、OneID 解析、已授权的业务查询和 cursor 续页行为。下载完整 API 合同后，以返回的 request_id 关联问题排查；不要记录 Token、身份值或原始用户响应。'));
  const contractDownload = element('a', '下载完整 API 合同'); contractDownload.href = '/api/admin/config/openapi.yaml'; contractDownload.className = 'open-platform-button'; contractDownload.download = 'aicrm-openapi.yaml'; testing.append(contractDownload);
  const testingNote = element('p', '此页面提供调用合同与占位示例，不提供在线试调，也不回传真实用户、订单、身份或凭据。'); testingNote.className = 'open-platform-doc-note'; testing.append(testingNote);
  const audience = docSection('audience', '人群包与推送记录接口');
  audience.append(element('p', '以下能力需在密钥管理中单独授权。前四项使用 read scope；推送上报使用 write scope 和 Idempotency-Key，仅记录监督节点报告，不执行发送。所有响应使用 data / error / request_id 标准结构。'));
  audience.append(docTable(['REST', 'Capability'], [
    ['GET /open/v1/audience/core-products', 'audience.product.read'],
    ['GET /open/v1/audience/packages/{package_id}/members', 'audience.member.read'],
    ['GET /open/v1/audience/packages/{package_id}/members/{customer_id}/operations', 'audience.member.operations.read'],
    ['GET /open/v1/audience/packages/{package_id}/members/{customer_id}/history', 'audience.member.history.read'],
    ['POST /open/v1/audience/push-records', 'audience.push.write'],
  ]));
  audience.append(element('p', '沿用 CRM 客户编号，读取及上报均校验调用方的数据范围。成员、明细和历史支持 limit 与 cursor；过滤后某页可能为空，仍应继续使用 next_cursor。相同业务推送不重复计数，状态更新提高 status_version 并使用新的请求幂等键；网络重试保持原键和原请求。'));
  main.append(quickstart, operations, audience, oneid, pagination, orders, errors, testing);
  const operationRows = [...operationTable.querySelectorAll<HTMLTableRowElement>('tbody tr')];
  operationRows.forEach((row, index) => { row.dataset.apiDocOperation = operationExamples[index][0]; });
  const applySearch = (): void => {
    const keyword = search.value.trim().toLocaleLowerCase();
    let operationMatches = 0;
    for (const item of main.querySelectorAll<HTMLElement>('[data-api-doc-operation]')) {
      const matches = !keyword || item.textContent!.toLocaleLowerCase().includes(keyword);
      item.hidden = !matches;
      if (matches) operationMatches += 1;
    }
    let visibleSections = 0;
    for (const section of main.querySelectorAll<HTMLElement>('[data-api-doc-section]')) {
      const matches = section.id === 'operations' ? operationMatches > 0 : !keyword || section.textContent!.toLocaleLowerCase().includes(keyword);
      section.hidden = !matches;
      if (matches) visibleSections += 1;
    }
    searchEmpty.hidden = !keyword || visibleSections > 0;
  };
  search.dataset.openPlatformDocSearch = '';
  search.addEventListener('input', applySearch);
  for (const anchor of toc.querySelectorAll<HTMLAnchorElement>('a')) {
    anchor.addEventListener('click', () => {
      if (!search.value) return;
      search.value = '';
      applySearch();
    });
  }
  layout.append(toc, main); root.append(layout);
  return root;
}

async function boot(): Promise<void> {
  if (document.body?.dataset.page !== 'apidocs') return;
  const stage = document.querySelector<HTMLElement>('#stage');
  if (!stage) return;
  const query = new URLSearchParams(window.location.search);
  const requestedTab = query.get('tab');
  let activeTab: 'docs' | 'clients' = requestedTab === 'clients' || (!requestedTab && query.has('client')) ? 'clients' : 'docs';
  let selectedID = query.get('client') || '';
  let selectedClient: ClientSummary | undefined;
  // A selected caller has a second, authoritative detail request. Keep the
  // create controls out of the DOM until it settles so a refresh cannot erase
  // values an administrator has already entered.
  let selectedClientLoading = false;
  let selectedLoadEpoch = 0;
  let managementLoadEpoch = 0;
  let clients: ClientSummary[] = [];
  let catalog: OperationDescriptor[] = [];
  let clientFailure: RequestError | null = null;
  let catalogFailure: RequestError | null = null;
  let issued: IssuedSecret | null = null;

  document.head.append(styles());
  stage.replaceChildren();
  const root = element('section');
  root.className = 'open-platform-root';
  root.dataset.openPlatformHost = 'v1';
  stage.append(root);

  const loadSelected = async (): Promise<void> => {
    const clientID = selectedID;
    const loadEpoch = ++selectedLoadEpoch;
    const managementEpoch = managementLoadEpoch;
    selectedClient = undefined;
    if (!clientID) {
      selectedClientLoading = false;
      renderManagement();
      return;
    }
    selectedClientLoading = true;
    renderManagement();
    try {
      const detail = await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(clientID)}`);
      if (activeTab !== 'clients' || managementEpoch !== managementLoadEpoch || loadEpoch !== selectedLoadEpoch || selectedID !== clientID) return;
      selectedClient = detail.client;
    } catch {
      if (activeTab !== 'clients' || managementEpoch !== managementLoadEpoch || loadEpoch !== selectedLoadEpoch || selectedID !== clientID) return;
      selectedClient = undefined;
    }
    if (activeTab !== 'clients' || managementEpoch !== managementLoadEpoch || loadEpoch !== selectedLoadEpoch || selectedID !== clientID) return;
    selectedClientLoading = false;
    renderManagement();
  };

  const failureMessage = (failure: RequestError | null, resource: 'clients' | 'catalog'): string => {
    if (!failure) return '';
    if (failure.status === 401) return '请先登录后管理调用方。';
    if (failure.status === 403 && resource === 'clients') return '仅超级管理员可管理调用方。';
    if (failure.status >= 500) return resource === 'clients' ? '调用方管理服务暂不可用，请重试。' : '能力目录暂不可用，请重试。';
    return resource === 'clients' ? '调用方管理暂不可用，请重试。' : '能力目录暂不可用，请重试。';
  };

  const asRequestError = (reason: unknown): RequestError => reason instanceof RequestError ? reason : new RequestError(0, 'request_failed');

  const refresh = async (): Promise<void> => {
    if (activeTab !== 'clients') return;
    const managementEpoch = ++managementLoadEpoch;
    root.replaceChildren();
    const loading = element('p', '正在加载开放平台调用方…');
    loading.className = 'open-platform-empty';
    root.append(loading);
    const [clientResult, catalogResult] = await Promise.allSettled([
      request<{ items: ClientSummary[] }>('/api/admin/open-platform/clients'),
      request<{ items: OperationDescriptor[] }>('/api/admin/open-platform/routes'),
    ]);
    if (activeTab !== 'clients' || managementEpoch !== managementLoadEpoch) return;
    clientFailure = clientResult.status === 'rejected' ? asRequestError(clientResult.reason) : null;
    catalogFailure = catalogResult.status === 'rejected' ? asRequestError(catalogResult.reason) : null;
    if (clientResult.status === 'fulfilled') {
      clients = clientResult.value.items || [];
      if (!selectedID && clients.length) selectedID = clients[0].client_id;
    } else {
      clients = [];
    }
    if (catalogResult.status === 'fulfilled') {
      catalog = catalogResult.value.items || [];
    } else {
      catalog = [];
    }
    if (!clientFailure) {
      await loadSelected();
    } else {
      selectedClient = undefined;
      selectedClientLoading = false;
      renderManagement();
    }
  };

  const choose = (id: string): void => {
    selectedID = id;
    const next = new URL(window.location.href);
    next.searchParams.set('tab', 'clients');
    next.searchParams.set('client', id);
    window.history.replaceState({}, '', next);
    void loadSelected();
  };

  const showIssuedSecret = (result: { client: ClientSummary; secret: string }): void => {
    issued = { clientID: result.client.client_id, secret: result.secret };
    secretDialog(issued, async () => {
      if (!issued) throw new Error('missing_secret');
      await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(issued.clientID)}/activate`, {
        method: 'POST', body: JSON.stringify({ client_secret: issued.secret, copied_confirmed: true }),
      });
      issued = null;
      await refresh();
    }, refresh, async () => { issued = null; await refresh(); });
  };

  const renderCreate = (): HTMLElement => {
    const card = element('section');
    card.className = 'open-platform-card';
    card.append(element('h2', '新建 V1 调用方'));
    const form = element('form');
    form.className = 'open-platform-form';
    const clientID = textInput(); clientID.autocomplete = 'off'; clientID.dataset.openPlatformCreate = 'client_id';
    const displayName = textInput(); displayName.dataset.openPlatformCreate = 'display_name';
    const ttl = textInput('1800', 'number'); ttl.min = '60'; ttl.max = '3600'; ttl.dataset.openPlatformCreate = 'token_ttl_seconds';
    const ips = textArea(); ips.dataset.openPlatformCreate = 'allowed_cidrs';
    const capabilities = exactV1Capabilities(catalog);
    form.append(
      field('调用方 ID', clientID, '固定 V1 OAuth 调用方标识。'),
      field('显示名称', displayName),
      field('Token TTL（秒）', ttl),
      field('来源 CIDR（可留空）', ips, '以逗号或换行分隔。'),
      field('Scope', checkList(KNOWN_SCOPES, ['read'], 'create-scope')),
      field('V1 能力', checkList(capabilities, capabilities.includes('platform.capabilities.read') ? ['platform.capabilities.read'] : [], 'create-capability')),
    );
    const message = element('p'); message.className = 'open-platform-status';
    form.append(message, button('创建并显示一次密钥', async () => {
      const scopes = checkedValues(form, 'create-scope');
      const granted = checkedValues(form, 'create-capability');
      const ttlSeconds = validTTL(ttl.value);
      if (!clientID.value.trim() || !displayName.value.trim() || ttlSeconds === undefined || scopes.length === 0 || granted.length === 0) {
        setStatus(message, '请填写调用方、TTL，并至少选择一项 scope 与能力。', true);
        return;
      }
      try {
        const result = await request<{ client: ClientSummary; secret: string }>('/api/admin/open-platform/clients', {
          method: 'POST',
          body: JSON.stringify({
            client_id: clientID.value.trim(), display_name: displayName.value.trim(), purpose: V1_PURPOSE,
            audiences: [V1_AUDIENCE], scopes, capabilities: granted, allowed_cidrs: cidrs(ips.value), token_ttl_seconds: ttlSeconds,
          }),
        });
        selectedID = result.client.client_id;
        showIssuedSecret(result);
      } catch {
        setStatus(message, '创建未完成。请检查 V1 授权字段并重试。', true);
      }
    }, 'primary'));
    card.append(form);
    return card;
  };

  const renderDetail = (client: ClientSummary | undefined): HTMLElement => {
    const card = element('section');
    card.className = 'open-platform-card';
    if (!client) {
      card.append(element('h2', '调用方详情'), element('p', '选择已有调用方，或创建新的 V1 调用方。'));
      return card;
    }
    card.dataset.openPlatformClient = client.client_id;
    card.append(element('h2', client.display_name));
    const summary = element('p', `${client.client_id} · ${client.enabled ? '已启用' : '待启用或已停用'} · OAuth 版本 ${client.auth_version}`);
    summary.className = 'open-platform-muted';
    card.append(summary);
    const form = element('form'); form.className = 'open-platform-form';
    const displayName = textInput(client.display_name); displayName.dataset.openPlatformEdit = 'display_name';
    const ttl = textInput(String(client.token_ttl_seconds), 'number'); ttl.min = '60'; ttl.max = '3600'; ttl.dataset.openPlatformEdit = 'token_ttl_seconds';
    const ips = textArea(nullableStrings(client.allowed_cidrs).join('\n')); ips.dataset.openPlatformEdit = 'allowed_cidrs';
    const capabilityValues = exactV1Capabilities(catalog);
    const initialExpiresAt = client.expires_at;
    const initialExpiresLocal = dateTimeLocalValue(initialExpiresAt);
    const expires = textInput(initialExpiresLocal, 'datetime-local'); expires.step = '1'; expires.dataset.openPlatformEdit = 'expires_at';
    const ownerScope = textArea(client.owner_scope && Object.keys(client.owner_scope).length ? JSON.stringify(client.owner_scope, null, 2) : ''); ownerScope.dataset.openPlatformEdit = 'owner_scope';
    form.append(
      field('显示名称', displayName), field('Token TTL（秒）', ttl), field('来源 CIDR（可留空）', ips),
      field('Scope', checkList(KNOWN_SCOPES, client.scopes, 'edit-scope')),
      field('V1 能力', checkList(capabilityValues, client.capabilities, 'edit-capability')),
      field('Owner scope（可留空以清除）', ownerScope, '仅受服务端验证的资源范围。'),
      field('到期时间（可留空以清除）', expires),
    );
    const message = element('p'); message.className = 'open-platform-status';
    const actions = element('div'); actions.className = 'open-platform-actions';
    actions.append(button('保存授权', async () => {
      const scopes = checkedValues(form, 'edit-scope');
      const granted = checkedValues(form, 'edit-capability');
      const ttlSeconds = validTTL(ttl.value);
      if (!displayName.value.trim() || ttlSeconds === undefined || scopes.length === 0 || granted.length === 0) {
        setStatus(message, '请保留显示名称、TTL、scope 与能力。', true);
        return;
      }
      try {
        const scope = safeOwnerScope(ownerScope.value);
        const convertedExpiry = expires.value ? shanghaiDateTimeLocalToRFC3339(expires.value) : null;
        if (expires.value && !convertedExpiry) {
          setStatus(message, '到期时间格式无效，请填写有效时间后保存。', true);
          return;
        }
        await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}`, {
          method: 'PATCH',
          body: JSON.stringify({ display_name: displayName.value.trim(), audiences: [V1_AUDIENCE], scopes, capabilities: granted, allowed_cidrs: cidrs(ips.value), token_ttl_seconds: ttlSeconds, owner_scope: scope, expires_at: unchangedDateTimeLocal(expires.value, initialExpiresLocal) ? initialExpiresAt ?? null : convertedExpiry }),
        });
        await refresh();
      } catch {
        setStatus(message, '保存未完成；现有授权没有在页面上更新。', true);
      }
    }, 'primary'));
    actions.append(button('轮换密钥', async () => {
      try {
        const result = await request<{ client: ClientSummary; secret: string }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}/rotate`, { method: 'POST', body: '{}' });
        showIssuedSecret(result);
      } catch { setStatus(message, '轮换未完成。', true); }
    }));
    if (client.enabled) {
      actions.append(button('停用调用方', async () => {
        try { await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}/disable`, { method: 'POST', body: '{}' }); await refresh(); }
        catch { setStatus(message, '停用未完成。', true); }
      }, 'danger'));
    }
    form.append(message, actions);
    card.append(form);
    card.append(renderAudit(client.client_id));
    return card;
  };

  const renderAudit = (clientID: string): HTMLElement => {
    const section = element('section'); section.className = 'open-platform-catalog';
    section.append(element('h2', '最近审计'));
    const body = element('div', '正在读取审计…'); body.className = 'open-platform-muted'; section.append(body);
    void request<{ items: AuditEntry[] }>(`/api/admin/open-platform/clients/${encodeURIComponent(clientID)}/audit?limit=50`).then((result) => {
      const table = element('table'); table.className = 'open-platform-table';
      const head = element('thead'); const header = element('tr');
      for (const label of ['时间', '操作', '结果', '详情']) header.append(element('th', label));
      head.append(header); const rows = element('tbody');
      for (const entry of result.items || []) {
        const row = element('tr');
        const details = typeof entry.details === 'string' ? entry.details : JSON.stringify(entry.details ?? {});
        for (const value of [formatTime(entry.created_at), auditActionLabel(entry.action), auditOutcomeLabel(entry.outcome), details]) row.append(element('td', value));
        rows.append(row);
      }
      if (!rows.children.length) { const row = element('tr'); const cell = element('td', '暂无审计记录'); cell.colSpan = 4; row.append(cell); rows.append(row); }
      table.append(head, rows); body.replaceWith(table);
    }).catch(() => { body.textContent = '审计暂不可读取。'; });
    return section;
  };

  const renderCatalog = (): HTMLElement => {
    const card = element('section'); card.className = 'open-platform-card open-platform-catalog';
    card.append(element('h2', 'V1 能力目录'));
    const table = element('table'); table.className = 'open-platform-table';
    const head = element('thead'); const header = element('tr');
    for (const label of ['Operation', 'REST', 'MCP', 'Capability', 'Scope']) header.append(element('th', label));
    head.append(header); const rows = element('tbody');
    for (const item of catalog) {
      const row = element('tr');
      for (const value of [item.operation_id, `${item.rest_method} ${item.rest_path}`, item.mcp_tool, item.capability, item.required_scope]) {
        const cell = element('td', value); if (value.includes('.') || value.startsWith('/')) cell.className = 'open-platform-code'; row.append(cell);
      }
      rows.append(row);
    }
    table.append(head, rows); card.append(table);
    return card;
  };

  const renderManagement = (): void => {
    root.replaceChildren();
    const header = element('header'); header.className = 'open-platform-header';
    const lead = element('div'); lead.append(element('h1', title), element('p', 'V1 OAuth 调用方、最小授权与审计。密钥不会在离开本页后保留。'));
    const actions = element('div'); actions.className = 'open-platform-actions';
    actions.append(button('返回接口文档', () => showTab('docs')), button('刷新', refresh));
    header.append(lead, actions); root.append(header);
    if (clientFailure) {
      const banner = element('div', failureMessage(clientFailure, 'clients'));
      banner.className = 'open-platform-management-banner';
      if (clientFailure.status === 401) {
        const login = element('a', '前往登录'); login.href = `/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`;
        banner.append(document.createTextNode(' '), login);
      }
      const retry = button('重试', refresh); retry.className = 'open-platform-button open-platform-retry'; banner.append(retry);
      root.append(banner);
      return;
    }
    if (catalogFailure) {
      const banner = element('div', failureMessage(catalogFailure, 'catalog'));
      banner.className = 'open-platform-management-banner';
      const retry = button('重试', refresh); retry.className = 'open-platform-button open-platform-retry'; banner.append(retry); root.append(banner);
    }
    const layout = element('div'); layout.className = 'open-platform-layout';
    const list = element('section'); list.className = 'open-platform-card'; list.append(element('h2', '调用方'));
    const clientList = element('div'); clientList.className = 'open-platform-list';
    for (const client of clients) {
      const row = element('button'); row.type = 'button'; row.className = 'open-platform-client'; row.setAttribute('aria-current', String(client.client_id === selectedID));
      row.append(element('strong', client.display_name), element('small', `${client.client_id} · ${client.enabled ? '已启用' : '已停用'} · ${client.credential_hint || '未签发'}`));
      row.addEventListener('click', () => choose(client.client_id)); clientList.append(row);
    }
    if (!clients.length) clientList.append(element('p', '尚无 V1 调用方。'));
    list.append(clientList);
    if (selectedClientLoading) {
      const loading = element('p', '正在加载调用方详情…');
      loading.className = 'open-platform-empty';
      list.append(loading);
    } else if (!catalogFailure) {
      list.append(renderCreate());
    } else {
      list.append(element('p', '能力目录不可用时不能修改或创建调用方。'));
    }
    const detailColumn = element('div'); detailColumn.className = 'open-platform-list';
    if (!catalogFailure) detailColumn.append(renderDetail(selectedClient), renderCatalog());
    else detailColumn.append(renderDetail(undefined));
    layout.append(list, detailColumn); root.append(layout);
  };

  const renderDocumentation = (): void => {
    root.replaceChildren(staticDocumentation(() => showTab('clients')));
    const fragment = window.location.hash.slice(1);
    if (!fragment) return;
    Promise.resolve().then(() => {
      let targetID = fragment;
      try { targetID = decodeURIComponent(fragment); } catch { /* malformed fragments do not block the document */ }
      const target = document.getElementById(targetID);
      if (target && typeof target.scrollIntoView === 'function') target.scrollIntoView({ block: 'start' });
    });
  };

  const showTab = (tab: 'docs' | 'clients', push = true): void => {
    activeTab = tab;
    const next = new URL(window.location.href);
    if (tab === 'docs') {
      managementLoadEpoch += 1;
      selectedLoadEpoch += 1;
      next.searchParams.set('tab', 'docs');
      next.searchParams.delete('client');
      if (push) window.history.pushState({}, '', next);
      renderDocumentation();
      return;
    }
    next.searchParams.set('tab', 'clients');
    if (push) window.history.pushState({}, '', next);
    void refresh();
  };

  window.addEventListener('popstate', () => {
    const next = new URLSearchParams(window.location.search);
    const explicit = next.get('tab');
    selectedID = next.get('client') || '';
    activeTab = explicit === 'clients' || (!explicit && Boolean(selectedID)) ? 'clients' : 'docs';
    if (activeTab === 'clients') void refresh();
    else {
      managementLoadEpoch += 1;
      selectedLoadEpoch += 1;
      renderDocumentation();
    }
  });

  if (activeTab === 'clients') await refresh();
  else renderDocumentation();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => { void boot(); }, { once: true });
} else {
  void boot();
}
