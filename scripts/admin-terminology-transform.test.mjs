import assert from 'node:assert/strict';
import { rewriteAdminTerminology, rewriteFrozenAdminComponentCopy } from './admin-terminology-transform.mjs';

const source = `<!doctype html><html><head><title>客户列表 · AI-CRM 管理后台</title><!-- <script async=""> --><style>.example::before { content: '<script async="">'; }</style><script type="module" async src="/assets/surfaceFeedbackHost.js"></script><script nonce="immutable-nonce">const protocol = { customer_id: '客户', message: '{{客户名}}', literal: '<script async="">' };</script></head><body><aside aria-label="客户管理后台">客户管理后台</aside><template id="tpl"><h1>客户档案</h1><label>付款人 / 客户身份</label><input placeholder="姓名、客户 ID" value="{{客户名}}"><input data-customer-id="42" title="Customer ID"><table><thead><tr><th>客户备注名</th></tr></thead></table><select><option value="客户主动申请退款">客户主动申请退款</option><option>客户主动申请退款</option></select><p>\n  客户无需好友验证\n</p><pre>客户示例</pre><code>customer_id: 客户</code><textarea>客户备注名</textarea><p>{{ customer.display_name }} 的客户资料</p></template></body></html>`;
const output = rewriteAdminTerminology(source);

assert.match(output, /<title>用户列表 · AI-CRM 管理后台<\/title>/);
assert.match(output, /<script type="module" async src="\/assets\/surfaceFeedbackHost\.js"><\/script>/);
assert.doesNotMatch(output, /<script type="module" async="" src="\/assets\/surfaceFeedbackHost\.js"><\/script>/);
assert.match(output, /literal: '<script async="">'/);
assert.match(output, /<!-- <script async=""> -->/);
assert.match(output, /content: '<script async="">';/);
assert.match(output, /aria-label="用户管理后台"/);
assert.match(output, /<h1>用户档案<\/h1>/);
assert.match(output, /付款人 \/ 用户身份/);
assert.match(output, /placeholder="姓名、用户 ID"/);
assert.match(output, /<option value="客户主动申请退款">用户主动申请退款<\/option>/);
assert.equal([...output.matchAll(/<option value="客户主动申请退款">用户主动申请退款<\/option>/g)].length, 2);
assert.match(output, /<th>客户备注名<\/th>/);
assert.match(output, /\n  用户无需好友验证\n/);
assert.match(output, /<pre>客户示例<\/pre>/);
assert.match(output, /<code>customer_id: 客户<\/code>/);
assert.match(output, /<textarea>客户备注名<\/textarea>/);
assert.match(output, /value="\{\{客户名\}\}"/);
assert.match(output, /\{\{ customer\.display_name \}\} 的客户资料/);
assert.match(output, /data-customer-id="42"/);
assert.match(output, /title="Customer ID"/);
assert.match(output, /<script nonce="immutable-nonce">const protocol = \{ customer_id: '客户', message: '\{\{客户名\}\}', literal: '<script async="">' \};<\/script>/);

const component = rewriteFrozenAdminComponentCopy('send_content_composer.js', 'group_invite: "客户群"; >插入客户名</button>; Agent 将为每个客户生成个性化话术; · 客户群 ${state.value.group_invite_library_ids.length}; insert("{{客户名}}");');
assert.match(component, /group_invite: "用户群"/);
assert.match(component, /插入用户姓名/);
assert.match(component, /每个用户/);
assert.match(component, /· 用户群/);
assert.match(component, /\{\{客户名\}\}/);
assert.equal(rewriteFrozenAdminComponentCopy('unrelated.js', 'customer_id = "客户"'), 'customer_id = "客户"');

console.log('admin-terminology-transform: PASS');
