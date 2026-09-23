import { readGroupOpsOwners, readGroupOpsDirectory, refreshGroupOpsDirectory } from './groupOpsDirectory';
import { saveGroupOpsPlanDto, transitionGroupOpsPlanDto, deleteGroupOpsPlanDto } from './admin';
import { ApiError } from './transport';

const assert = (condition: unknown, message: string): void => { if (!condition) throw new Error(message); };
export async function runGroupOpsDirectoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch;
  const oldDocument = Object.getOwnPropertyDescriptor(globalThis, 'document');
  Object.defineProperty(globalThis, 'document', { configurable: true, value: { cookie: 'aicrm_csrf=directory-csrf' } });
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const safety = { provider_execution_eligible: false, real_external_call_executed: false, provider_accepted: false, delivery_proven: false };
  const item = { chat_reference: 'group-new', owner_staff_id: 7, display_name: '群 <原名>', member_count: 0, refreshed_at: '2026-08-28T00:00:00Z' };
  const envelope = { ...safety, items: [item], total: 51, limit: 50, offset: 50, has_more: false };
  let body: unknown = { ...safety, scope: 'group_ops', page_size: 100, items: [{ staff_id: 7, sender_userid: 'staff-seven', display_name: '成员七' }] };
  let status = 200;
  globalThis.fetch = async (input, init = {}) => { calls.push({ url: String(input), init }); return new Response(JSON.stringify(body), { status }); };
  const rejects = async (fn: () => Promise<unknown>, statusCode?: number): Promise<void> => {
    try { await fn(); } catch (error) { assert(statusCode === undefined || error instanceof ApiError && error.status === statusCode, 'wrong error status'); return; }
    throw new Error('invalid response or request accepted');
  };
  try {
    assert((await readGroupOpsOwners())[0].staffId === 7, 'trusted staff ID');
    body = envelope;
    assert((await readGroupOpsDirectory(7, 50, 50)).items[0].member_count === 0, 'page preserves zero and real name');
    body = { ...envelope, offset: 0 };
    await refreshGroupOpsDirectory(7, 'directory-retry-stable-key');
    assert(calls[0].url === '/api/admin/common/operation-members?scope=group_ops&page_size=100', 'member scope');
    assert(calls[1].url === '/api/admin/automation-conversion/group-ops/groups?owner_userid=7&limit=50&offset=50', 'GET legacy owner_userid carries trusted numeric staff');
    assert(calls[2].url.endsWith('/groups/sync') && calls[2].init.method === 'POST' && calls[2].init.body === '{"owner_staff_id":7,"limit":50}', 'sync closed payload');
    assert(new Headers(calls[2].init.headers).get('Idempotency-Key') === 'directory-retry-stable-key' && new Headers(calls[2].init.headers).get('X-CSRF-Token') === 'directory-csrf' && calls.every((call) => call.init.credentials === 'include'), 'same-origin auth CSRF and stable key');
    body = { ...envelope, items: [{ ...item, owner_staff_id: 8 }] };
    await rejects(() => readGroupOpsDirectory(7, 50, 50));
    body = { ...envelope, offset: 0 };
    await rejects(() => readGroupOpsDirectory(7, 50, 50));
    const before = calls.length;
    await rejects(() => readGroupOpsDirectory(0));
    await rejects(() => readGroupOpsDirectory(7, 50, -1));
    assert(calls.length === before, 'invalid query never sent');
    status = 503; await rejects(() => readGroupOpsDirectory(7), 503);
    status = 403; await rejects(() => refreshGroupOpsDirectory(7, 'directory-retry-stable-key'), 403);
    status = 200;

    const detail = { plan: { plan_id: '10', name: '计划', revision: 1, status: 'draft' }, members: [{ staff_id: 7 }], nodes: [], group_assets: [{ group_asset_id: '1', asset_reference: 'group-old' }, { group_asset_id: '2', asset_reference: 'unknown-old' }], webhook_descriptor: {}, provider_execution_eligible: false, real_external_call_executed: false };
    calls.length = 0;
    globalThis.fetch = async (input, init = {}) => {
      const url = String(input); calls.push({ url, init });
      const payload = init.body ? JSON.parse(String(init.body)) : {};
      if (init.method === 'DELETE' || init.method === 'POST' && url.endsWith('/group-assets')) {
        assert(payload.expected_revision === detail.plan.revision, 'CAS always uses latest reread revision');
        if (init.method === 'DELETE') detail.group_assets = detail.group_assets.filter((asset) => asset.group_asset_id !== '1');
        else detail.group_assets.push({ group_asset_id: '3', asset_reference: payload.asset_reference });
        detail.plan.revision++;
      }
      return new Response(JSON.stringify(url.endsWith('/content/preview') ? { preview_lines: [], issue_codes: [] } : detail), { status: 200 });
    };
    const result = await saveGroupOpsPlanDto({ id: '10', name: '计划', staffIds: [7], assetReferences: ['unknown-old', 'group-new'], nodes: [] });
    assert(result.plan.revision === 3 && result.assets.map((asset) => asset.reference).join(',') === 'unknown-old,group-new', 'unknown prior reference preserved through real CAS adapter');
    const writes = calls.filter((call) => call.init.method !== 'GET');
    assert(writes.length === 3 && writes[0].init.method === 'DELETE' && writes[0].url.endsWith('/group-assets/group-old') && writes[1].url.endsWith('/group-assets') && writes[2].url.endsWith('/content/preview'), 'existing save performs reference removal, addition and local preview only');
    const keys = writes.slice(0, 2).map((call) => new Headers(call.init.headers).get('Idempotency-Key') || '');
    assert(keys.every((key) => key.length >= 16) && new Set(keys).size === keys.length, 'each distinct mutation carries its own required idempotency key');
    assert(calls.filter((call) => call.init.method === 'GET').length === 4 && !calls.some((call) => /sync|broadcast|run-due/.test(call.url)), 'rereads after CAS and no Provider/run-due request');
    for (const appliedBeforeDisconnect of [false, true]) {
      const retryInput = { id: '10', name: '计划', staffIds: [7], assetReferences: ['unknown-old', 'group-new', 'retry-group'], nodes: [] };
      detail.group_assets = detail.group_assets.filter((asset) => asset.asset_reference !== 'retry-group');
      calls.length = 0;
      let disconnect = true;
      globalThis.fetch = async (input, init = {}) => {
        const url = String(input); calls.push({ url, init });
        if (init.method === 'POST' && url.endsWith('/group-assets')) {
          const payload = JSON.parse(String(init.body));
          assert(payload.expected_revision === detail.plan.revision, 'retry reads current revision first');
          if (!disconnect || appliedBeforeDisconnect) {
            assert(!detail.group_assets.some((asset) => asset.asset_reference === payload.asset_reference), 'unknown-result retry must not duplicate an applied binding');
            detail.group_assets.push({ group_asset_id: '4', asset_reference: payload.asset_reference }); detail.plan.revision++;
          }
          if (disconnect) { disconnect = false; throw new Error('test network disconnect'); }
        }
        return new Response(JSON.stringify(url.endsWith('/content/preview') ? { preview_lines: [], issue_codes: [] } : detail), { status: 200 });
      };
      await rejects(() => saveGroupOpsPlanDto(retryInput));
      const retryStart = calls.length;
      await saveGroupOpsPlanDto(retryInput);
      assert(calls[retryStart].init.method === 'GET', 'save retry always starts from current server detail');
      const attempts = calls.filter((call) => call.init.method === 'POST' && call.url.endsWith('/group-assets'));
      assert(attempts.length === (appliedBeforeDisconnect ? 1 : 2), 'known applied result skips the second mutation');
      assert(new Set(attempts.map((call) => new Headers(call.init.headers).get('Idempotency-Key'))).size === 1, 'same unapplied logical write retains key across retry');
    }
    for (const action of ['activate', 'pause', 'archive', 'delete'] as const) {
      calls.length = 0;
      let failMutation = true;
      globalThis.fetch = async (input, init = {}) => {
        calls.push({ url: String(input), init });
        if (init.method !== 'GET') {
          const headers = new Headers(init.headers);
          assert((headers.get('Idempotency-Key') || '').length >= 16 && headers.get('X-CSRF-Token') === 'directory-csrf', 'lifecycle requires backend idempotency and CSRF headers');
          assert(JSON.parse(String(init.body)).expected_revision === detail.plan.revision, 'lifecycle uses current CAS revision');
          if (failMutation) return new Response('{"code":"unavailable"}', { status: 503 });
        }
        return new Response(JSON.stringify(detail), { status: 200 });
      };
      const run = () => action === 'delete' ? deleteGroupOpsPlanDto('10') : transitionGroupOpsPlanDto('10', action);
      await rejects(run, 503); failMutation = false; await run();
      const attempts = calls.filter((call) => call.init.method !== 'GET');
      assert(calls.length === 3 && calls[0].init.method === 'GET' && calls[2].init.method !== 'GET', 'unconfirmed lifecycle retry preserves original intent without a new detail read');
      assert(attempts.length === 2 && new Headers(attempts[0].init.headers).get('Idempotency-Key') === new Headers(attempts[1].init.headers).get('Idempotency-Key'), 'same lifecycle command retries with same key');
      assert(action === 'delete' ? attempts[0].init.method === 'DELETE' && attempts[0].url.endsWith('/plans/10') : attempts[0].url.endsWith('/' + action), 'exact existing lifecycle route');
    }
    for (const action of ['activate', 'pause', 'archive', 'delete'] as const) {
      calls.length = 0;
      const revision = detail.plan.revision;
      let applied = false;
      globalThis.fetch = async (input, init = {}) => {
        calls.push({ url: String(input), init });
        if (init.method === 'GET') return applied && action === 'delete' ? new Response('{"code":"not_found"}', { status: 404 }) : new Response(JSON.stringify(detail), { status: 200 });
        assert(JSON.parse(String(init.body)).expected_revision === revision, 'committed lifecycle retry preserves original body');
        if (!applied) { applied = true; detail.plan.revision++; throw new Error('response lost after commit'); }
        return new Response(JSON.stringify(detail), { status: 200 });
      };
      const run = () => action === 'delete' ? deleteGroupOpsPlanDto('10') : transitionGroupOpsPlanDto('10', action);
      await rejects(run); await run();
      assert(calls.length === 3 && calls[1].init.body === calls[2].init.body && new Headers(calls[1].init.headers).get('Idempotency-Key') === new Headers(calls[2].init.headers).get('Idempotency-Key'), 'committed lifecycle/delete is replayed exactly once without new revision or 404 lookup');
    }
    for (const action of ['activate', 'delete'] as const) {
      calls.length = 0; let conflict = true;
      globalThis.fetch = async (input, init = {}) => {
        calls.push({ url: String(input), init });
        if (init.method !== 'GET' && conflict) { conflict = false; detail.plan.revision++; return new Response('{"code":"conflict"}', { status: 409 }); }
        return new Response(JSON.stringify(detail), { status: 200 });
      };
      const run = () => action === 'delete' ? deleteGroupOpsPlanDto('10') : transitionGroupOpsPlanDto('10', action);
      await rejects(run, 409); await run();
      assert(calls.length === 4 && calls[2].init.method === 'GET' && calls[1].init.body !== calls[3].init.body && new Headers(calls[1].init.headers).get('Idempotency-Key') !== new Headers(calls[3].init.headers).get('Idempotency-Key'), 'definitive CAS 409 clears stale intent; next explicit retry reads new revision and key');
    }
    const nodeDetail = { plan: { plan_id: 'node-10', name: '节点计划', revision: 1, status: 'draft' }, members: [], group_assets: [], webhook_descriptor: {}, nodes: [{ node_id: '51', position: 1, kind: 'message', message_text: 'before', material_plan: { references: [{ kind: 'image', id: 7 }] } }], provider_execution_eligible: false, real_external_call_executed: false };
    const nodeInput: Parameters<typeof saveGroupOpsPlanDto>[0] = { id: 'node-10', name: '节点计划', staffIds: [], assetReferences: [], nodes: [{ id: '51', position: 1, kind: 'message', messageText: '\u0085 after \n', materialPlan: { references: [{ kind: 'image', id: 8 }] } }] };
    calls.length = 0; let loseNodeResponse = true;
    globalThis.fetch = async (input, init = {}) => {
      const url = String(input); calls.push({ url, init });
      if (init.method === 'PATCH') {
        const payload = JSON.parse(String(init.body));
        assert(payload.expected_revision === nodeDetail.plan.revision, 'node update current revision');
        Object.assign(nodeDetail.nodes[0], payload, { message_text: String(payload.message_text || '').replace(/^[\u0085 \n]+|[\u0085 \n]+$/g, '') }); nodeDetail.plan.revision++;
        if (loseNodeResponse) { loseNodeResponse = false; throw new Error('committed node response lost'); }
      }
      return new Response(JSON.stringify(url.endsWith('/content/preview') ? { preview_lines: [], issue_codes: [] } : nodeDetail), { status: 200 });
    };
    await rejects(() => saveGroupOpsPlanDto(nodeInput)); await saveGroupOpsPlanDto(nodeInput);
    assert(calls.filter((call) => call.init.method === 'PATCH').length === 1 && nodeDetail.plan.revision === 2, 'committed typed node is not updated or evented again after response loss');
    nodeInput.nodes[0].messageText = '\uFEFFintentional change';
    await saveGroupOpsPlanDto(nodeInput);
    assert(calls.filter((call) => call.init.method === 'PATCH').length === 2, 'intentional node field change still updates');
    assert(nodeDetail.nodes[0].message_text.charCodeAt(0) === 0xFEFF, 'BOM is retained like Go TrimSpace, not removed by JavaScript trim');
    const addInput: Parameters<typeof saveGroupOpsPlanDto>[0] = { ...nodeInput, nodes: [...nodeInput.nodes, { position: 2, kind: 'message', messageText: 'new node', materialPlan: { references: [{ kind: 'attachment', id: 9 }] } }] };
    calls.length = 0; let loseAddResponse = true;
    globalThis.fetch = async (input, init = {}) => {
      const url = String(input); calls.push({ url, init });
      if (init.method === 'POST' && url.endsWith('/nodes')) {
        const payload = JSON.parse(String(init.body));
        assert(payload.expected_revision === nodeDetail.plan.revision, 'new node current revision');
        nodeDetail.nodes.push({ node_id: '52', ...payload }); nodeDetail.plan.revision++;
        if (loseAddResponse) { loseAddResponse = false; throw new Error('committed new node response lost'); }
      }
      if (init.method === 'DELETE') {
        assert(JSON.parse(String(init.body)).expected_revision === nodeDetail.plan.revision, 'node removal current revision');
        nodeDetail.nodes = nodeDetail.nodes.filter((node) => node.node_id !== url.split('/').pop()); nodeDetail.plan.revision++;
      }
      return new Response(JSON.stringify(url.endsWith('/content/preview') ? { preview_lines: [], issue_codes: [] } : nodeDetail), { status: 200 });
    };
    await rejects(() => saveGroupOpsPlanDto(addInput));
    const committedRevision = nodeDetail.plan.revision;
    await saveGroupOpsPlanDto(addInput);
    assert(nodeDetail.plan.revision === committedRevision && calls.filter((call) => call.url.endsWith('/nodes') && call.init.method === 'POST').length === 1 && !calls.some((call) => call.init.method === 'DELETE' || call.init.method === 'PATCH'), 'unique-position complete typed match retains real new node ID after lost response, without remove/add/event');
    calls.length = 0;
    addInput.nodes[1].materialPlan = { references: [{ kind: 'attachment', id: 10 }] };
    await saveGroupOpsPlanDto(addInput);
    assert(calls.some((call) => call.init.method === 'DELETE') && calls.some((call) => call.init.method === 'POST' && call.url.endsWith('/nodes')) && nodeDetail.nodes[1].material_plan.references[0].id === 10, 'different typed material is not falsely matched by position or message text');
    calls.length = 0;
    await saveGroupOpsPlanDto(nodeInput);
    assert(nodeDetail.nodes.length === 1 && nodeDetail.nodes[0].node_id === '51' && calls.filter((call) => call.init.method === 'DELETE').length === 1, 'explicit retained node survives intentional deletion of the other node');

    const created = { plan: { plan_id: 'created-10', name: '新建回放测试', revision: 1, status: 'draft' }, members: [], nodes: [], group_assets: [] as Array<{ group_asset_id: string; asset_reference: string }>, webhook_descriptor: {}, provider_execution_eligible: false, real_external_call_executed: false };
    const creationSnapshot = JSON.stringify(created);
    const createInput = { name: '新建回放测试', staffIds: [], assetReferences: ['group-a', 'group-b'], nodes: [] };
    calls.length = 0; let createKey = '', lostSecondAsset = false;
    globalThis.fetch = async (input, init = {}) => {
      const url = String(input); calls.push({ url, init });
      if (init.method === 'POST' && url.endsWith('/plans')) {
        const key = new Headers(init.headers).get('Idempotency-Key')!;
        assert(!createKey || key === createKey, 'creation replay keeps original key');
        createKey = key; return new Response(creationSnapshot, { status: 201 });
      }
      if (init.method === 'POST' && url.endsWith('/group-assets')) {
        const payload = JSON.parse(String(init.body));
        assert(payload.expected_revision === created.plan.revision && !created.group_assets.some((asset) => asset.asset_reference === payload.asset_reference), 'creation replay must reread actual target and not duplicate existing assets');
        created.group_assets.push({ group_asset_id: String(created.group_assets.length + 1), asset_reference: payload.asset_reference }); created.plan.revision++;
        if (payload.asset_reference === 'group-b' && !lostSecondAsset) { lostSecondAsset = true; throw new Error('second asset response lost after commit'); }
      }
      return new Response(JSON.stringify(url.endsWith('/content/preview') ? { preview_lines: [], issue_codes: [] } : created), { status: 200 });
    };
    await rejects(() => saveGroupOpsPlanDto(createInput)); await saveGroupOpsPlanDto(createInput);
    assert(calls.filter((call) => call.url.endsWith('/group-assets')).length === 2 && created.plan.revision === 3, 'new plan partial retry creates no duplicate assets/events');
    assert(calls.filter((call) => call.url.endsWith('/plans')).length === 2 && calls.every((call, index) => !call.url.endsWith('/plans') || calls[index + 1]?.url.endsWith('/plans/created-10') && calls[index + 1]?.init.method === 'GET'), 'both create and create replay are followed by current detail GET');
    globalThis.fetch = async () => new Response('{"code":"not_found"}', { status: 404 });
    await rejects(() => deleteGroupOpsPlanDto('10'), 404);
  } finally {
    globalThis.fetch = previous;
    if (oldDocument) Object.defineProperty(globalThis, 'document', oldDocument); else Reflect.deleteProperty(globalThis, 'document');
  }
}
