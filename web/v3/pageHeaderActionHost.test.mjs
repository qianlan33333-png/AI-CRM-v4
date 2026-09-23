import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const bundle = await build({
  entryPoints: ['web/v3/pageHeaderActionHost.ts'], bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, logLevel: 'warning',
});

async function settle() { await new Promise((resolve) => setTimeout(resolve, 0)); }
async function settleMutations() { await settle(); await settle(); }

const tags = new JSDOM(`<!doctype html><body data-page="tags"><header class="admin-topbar"><div class="admin-topbar-head"><h1>企微标签管理</h1></div></header><main id="stage"><div style="display:contents"><div style="height:52px">用户管理后台 / 运营 / 企微标签管理</div><section><div><button>同步企微标签</button><button>新增标签组</button><button>新增标签</button></div></section></div></main></body>`, { runScripts: 'outside-only' });
try {
  tags.window.eval(bundle.outputFiles[0].text);
  await settle();
  const topbar = tags.window.document.querySelector('.admin-topbar');
  assert.equal(topbar.querySelectorAll('h1').length, 1, 'tag page retains the one shell title');
  assert.deepEqual([...topbar.querySelectorAll('[data-page-header-actions="wecom-tags"] button')].map((node) => node.textContent), ['同步企微标签', '新增标签组', '新增标签'], 'tag actions move into the topbar');
  assert.equal(tags.window.document.querySelector('#stage > div > div').hidden, true, 'tag donor title row is visually hidden after shell title is enabled');
  assert.equal(tags.window.document.querySelector('#stage section button'), null, 'tag card no longer contains duplicated actions');
  const firstCreate = topbar.querySelector('[data-page-header-actions="wecom-tags"] button:last-child');
  tags.window.document.querySelector('#stage').innerHTML = '<div style="display:contents"><div style="height:52px">用户管理后台 / 运营 / 企微标签管理</div><section><div><button>同步企微标签</button><button>新增标签组</button><button>新增标签</button></div></section></div>';
  await settleMutations();
  const refreshedCreate = topbar.querySelector('[data-page-header-actions="wecom-tags"] button:last-child');
  assert.notEqual(refreshedCreate, firstCreate, 'a donor redraw replaces stale header controls with the current original nodes');
  assert.equal(tags.window.document.querySelector('#stage section button'), null, 'a donor redraw leaves no parallel tag action row');
  const beforeUnrelatedMutation = refreshedCreate;
  tags.window.document.querySelector('#stage').append(tags.window.document.createElement('aside'));
  await settleMutations();
  assert.equal(topbar.querySelector('[data-page-header-actions="wecom-tags"] button:last-child'), beforeUnrelatedMutation, 'unrelated mutations do not remount or replace a current header action');
  tags.window.document.querySelector('#stage').innerHTML = '<div>加载中</div>';
  await settleMutations();
  assert.equal(topbar.querySelector('[data-page-header-actions="wecom-tags"]'), null, 'a donor redraw without replacement actions removes stale controls from the header');
} finally { tags.window.close(); }

const plan = new JSDOM(`<!doctype html><body data-page="ai-assistant"><header class="admin-topbar"><div class="admin-topbar-head"><h1>AI 助手</h1></div></header><main id="stage"><section data-cloud-plan-root data-page-mode="detail"><div class="cloud-plan-detail-head"><span data-plan-detail-state>待审批</span><div class="cloud-plan-actions"><button data-plan-approve>确认并发送</button><button data-plan-reject>拒绝计划</button><a href="/admin/cloud-orchestrator/plans">返回一级页</a></div></div></section></main></body>`, { runScripts: 'outside-only' });
try {
  let approved = 0;
  plan.window.document.querySelector('[data-plan-approve]').addEventListener('click', () => { approved += 1; });
  plan.window.eval(bundle.outputFiles[0].text);
  await settle();
  const topbar = plan.window.document.querySelector('.admin-topbar');
  const actions = topbar.querySelector('[data-page-header-actions="ai-plan-detail"]');
  assert.deepEqual([...actions.children].map((node) => node.textContent), ['返回一级页', '拒绝计划', '确认并发送'], 'plan actions move into the topbar in navigation, reject, approve order');
  actions.querySelector('[data-plan-approve]').click();
  assert.equal(approved, 1, 'moved plan action retains the existing approval callback');
  assert.equal(plan.window.document.querySelector('.cloud-plan-actions').hidden, true, 'plan body action row is hidden once controls are relocated');
  plan.window.document.querySelector('[data-cloud-plan-root]').innerHTML = '<div>计划信息加载中</div>';
  await settleMutations();
  assert.equal(topbar.querySelector('[data-page-header-actions="ai-plan-detail"]'), null, 'a plan redraw without replacement actions removes stale send controls from the header');
} finally { plan.window.close(); }

console.log('page header action host: PASS');
