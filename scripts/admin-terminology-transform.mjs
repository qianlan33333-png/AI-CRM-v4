import { JSDOM } from 'jsdom';

// This is deliberately a closed dictionary, rather than a global text
// replacement. It applies only to static operator-facing copy in generated
// admin documents; customer-domain contracts remain their original tokens.
const staticText = new Map([
  ['客户管理后台', '用户管理后台'],
  ['客户激活 / 客户列表', '用户激活 / 用户列表'],
  ['客户渠道标识', '用户渠道标识'],
  ['客户无需好友验证', '用户无需好友验证'],
  ['仅显示已归档的入渠与客服快照；不代表当前客户归属、员工权限或企微 Provider 效果。客服源时间不含时区。', '仅显示已归档的入渠与客服快照；不代表当前用户归属、员工权限或企微 Provider 效果。客服源时间不含时区。'],
  ['客户 ID', '用户 ID'],
  ['客户档案 · AI-CRM 管理后台', '用户档案 · AI-CRM 管理后台'],
  ['客户档案不存在', '用户档案不存在'],
  ['该客户可能已删除，或当前账号无权查看此 OneID。', '该用户可能已删除，或当前账号无权查看此 OneID。'],
  ['返回客户列表', '返回用户列表'],
  ['客户列表', '用户列表'],
  ['客户档案', '用户档案'],
  ['客户 OneID', '用户 OneID'],
  ['更新客户资料', '更新用户资料'],
  ['客户时间线', '用户时间线'],
  ['当前客户暂无已匹配的黄小璨账号。', '当前用户暂无已匹配的黄小璨账号。'],
  ['客户列表 · AI-CRM 管理后台', '用户列表 · AI-CRM 管理后台'],
  ['客户', '用户'],
  ['当前筛选暂无客户', '当前筛选暂无用户'],
  ['客户主动申请退款', '用户主动申请退款'],
  ['付款人 / 客户身份', '付款人 / 用户身份'],
  ['当前后端仅支持可审计的本地负责人迁移事务：CSV 或 XLSX 第一张工作表中逐行提供客户 ID、预期原负责人 ID、客户更新时间与目标负责人 ID。XLSX 会在浏览器严格校验固定四列后转为 CSV；此流程不会发起企微转接，也不接受欢迎语。', '当前后端仅支持可审计的本地负责人迁移事务：CSV 或 XLSX 第一张工作表中逐行提供用户 ID、预期原负责人 ID、用户更新时间与目标负责人 ID。XLSX 会在浏览器严格校验固定四列后转为 CSV；此流程不会发起企微转接，也不接受欢迎语。'],
  ['预期客户更新时间', '预期用户更新时间'],
  ['：公开页只显示成员显示名、本地状态、来源和服务时间，不展示手机号、客户编号或外部身份。', '：公开页只显示成员显示名、本地状态、来源和服务时间，不展示手机号、用户编号或外部身份。'],
  ['集中管理企业客户标签：同步、搜索、新增、编辑、删除和复制 tag_id。', '集中管理企业用户标签：同步、搜索、新增、编辑、删除和复制 tag_id。'],
]);

const staticAttributes = new Map([
  ['姓名、客户 ID', '姓名、用户 ID'],
  ['发给客户时卡片上的标题', '发给用户时卡片上的标题'],
  ['姓名或客户身份', '姓名或用户身份'],
]);

const safeAttributeNames = new Set(['aria-label', 'placeholder', 'title']);
const technicalTextContainers = 'script, style, pre, code, textarea';
const containsTemplateSyntax = (value) => value.includes('{{') || value.includes('{%');

// These release assets are byte-frozen donor inputs, but their exact UI
// literals are rendered by existing V3 Hosts. Keep the donors unchanged and
// adjust only the generated same-origin copies. Each entry is an exact source
// fragment, so template variables and API/property tokens never participate.
const frozenScriptCopy = new Map([
  ['material_picker.js', new Map([
    ['group_invite: "客户群"', 'group_invite: "用户群"'],
  ])],
  ['send_content_composer.js', new Map([
    ['group_invite: "客户群"', 'group_invite: "用户群"'],
    ['>插入客户名</button>', '>插入用户姓名</button>'],
    ['Agent 将为每个客户生成个性化话术', 'Agent 将为每个用户生成个性化话术'],
    ['· 客户群 ${state.value.group_invite_library_ids.length}', '· 用户群 ${state.value.group_invite_library_ids.length}'],
  ])],
  ['channel_admission_pages.js', new Map([
    ['<th>客户</th><th>进入次数</th>', '<th>用户</th><th>进入次数</th>'],
  ])],
]);

function exactStaticCopy(value, dictionary) {
  const match = /^(\s*)([\s\S]*?)(\s*)$/.exec(value);
  if (!match || containsTemplateSyntax(match[2])) return undefined;
  const target = dictionary.get(match[2]);
  return target === undefined ? undefined : `${match[1]}${target}${match[3]}`;
}

function transformRoot(root, document) {
  const nodeFilter = document.defaultView.NodeFilter;
  const walker = document.createTreeWalker(root, nodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const parent = node.parentElement;
    if (!parent || parent.closest(technicalTextContainers)) continue;
    const source = node.nodeValue || '';
    const target = exactStaticCopy(source, staticText);
    if (target === undefined) continue;
    // An option without a value submits its text. Keep the original value when
    // only its visible label changes, so this presentation transform cannot
    // alter a saved refund-reason or other select protocol.
    if (parent.tagName === 'OPTION' && !parent.hasAttribute('value')) parent.setAttribute('value', source.trim());
    node.nodeValue = target;
  }

  for (const element of root.querySelectorAll('*')) {
    if (element.closest(technicalTextContainers)) continue;
    for (const attribute of element.attributes) {
      if (!safeAttributeNames.has(attribute.name) || containsTemplateSyntax(attribute.value)) continue;
      const target = staticAttributes.get(attribute.value) || staticText.get(attribute.value);
      if (target) element.setAttribute(attribute.name, target);
    }
  }

  for (const template of root.querySelectorAll('template')) transformRoot(template.content, document);
}

function canonicalizeScriptAsyncAttribute(source) {
  const located = new JSDOM(source, { includeNodeLocations: true });
  const ranges = [];
  const collect = (root) => {
    for (const script of root.querySelectorAll('script')) {
      if (!script.hasAttribute('async')) continue;
      const location = located.nodeLocation(script)?.startTag;
      if (location) ranges.push(location);
    }
    for (const template of root.querySelectorAll('template')) collect(template.content);
  };
  collect(located.window.document);
  let output = source;
  for (const range of ranges.sort((left, right) => right.startOffset - left.startOffset)) {
    const tag = output.slice(range.startOffset, range.endOffset);
    output = output.slice(0, range.startOffset) + tag.replace(/\sasync=""(?=\s|>|\/>)/g, ' async') + output.slice(range.endOffset);
  }
  located.window.close();
  return output;
}

export function rewriteAdminTerminology(documentHTML) {
  const dom = new JSDOM(documentHTML);
  transformRoot(dom.window.document, dom.window.document);
  // JSDOM writes boolean attributes as `async=""`. The browser treats both
  // spellings identically, but these generated pages are staged through a
  // byte-checked shell contract which retains the donor's `async` spelling.
  // JSDOM source locations limit this to parsed script start tags, so code,
  // CSS, comments, and other data literals are never rewritten.
  const output = canonicalizeScriptAsyncAttribute(dom.serialize());
  dom.window.close();
  return output;
}

export function rewriteFrozenAdminComponentCopy(name, source) {
  const replacements = frozenScriptCopy.get(name);
  if (!replacements) return source;
  let output = source;
  for (const [from, to] of replacements) {
    if (!output.includes(from)) throw new Error(`${name} is missing reviewed terminology fragment: ${from}`);
    output = output.replaceAll(from, to);
  }
  return output;
}
