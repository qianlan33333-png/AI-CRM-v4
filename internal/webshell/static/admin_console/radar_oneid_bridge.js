(function () {
  'use strict';
  const nativeFetch = window.fetch.bind(window);
  const radarMutation = /^\/api\/admin\/radar-links(?:\/\d+)?(?:\/(?:enable|disable))?$/;
  const radarList = /^\/api\/admin\/radar-links$/;
  const listSummaries = new Map();
  let listSummaryReady = Promise.resolve();
  let listSummaryRevision = 0;
  let listGeneration = 0;
  const hydratedRows = new WeakMap();

  function listSummaryStatus(value) {
    return value && value.statistics_status === 'ready' ? 'ready' : 'unavailable';
  }
  function rememberListSummaries(response, generation) {
    const read = response.clone().json().then(function (payload) {
      if (generation !== listGeneration) return;
      listSummaries.clear();
      listSummaryRevision += 1;
      const items = payload && Array.isArray(payload.items) ? payload.items : [];
      items.forEach(function (item) {
        const id = Number(item && item.link_id);
        if (Number.isSafeInteger(id) && id > 0) listSummaries.set(id, item);
      });
    }).catch(function () {
      if (generation !== listGeneration) return;
      listSummaries.clear();
      listSummaryRevision += 1;
    });
    if (generation === listGeneration) listSummaryReady = read;
    return response;
  }

  window.addEventListener('aicrm:radar-list-generation', function (event) {
    const generation = event && event.detail && event.detail.generation;
    if (!Number.isSafeInteger(generation) || generation <= listGeneration) return;
    listGeneration = generation;
    listSummaries.clear();
    listSummaryRevision += 1;
    listSummaryReady = Promise.resolve();
  });

  window.fetch = function (input, init) {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.pathname + input.search : input.url;
    const url = new URL(raw, location.origin);
    const method = String((init && init.method) || (input instanceof Request && input.method) || 'GET').toUpperCase();
    if (url.origin === location.origin && radarMutation.test(url.pathname) && ['POST', 'PUT', 'PATCH'].includes(method)) {
      const next = Object.assign({}, init || {});
      const headers = new Headers(next.headers || (input instanceof Request ? input.headers : undefined));
      if (!headers.has('Idempotency-Key')) headers.set('Idempotency-Key', 'radar-ui-' + crypto.randomUUID());
      next.headers = Object.fromEntries(headers.entries());
      const lifecycle = !url.pathname.endsWith('/enable') && !url.pathname.endsWith('/disable');
      const enabledToggle = lifecycle && document.querySelector('#swEnabled');
      const wantsEnabled = enabledToggle ? enabledToggle.classList.contains('on') : null;
      if (typeof next.body === 'string' && lifecycle) {
        try {
          const payload = JSON.parse(next.body);
          const toggle = document.querySelector('#swAuth');
          payload.auth_policy = toggle && !toggle.classList.contains('on') ? 'anonymous' : 'unionid_required';
          next.body = JSON.stringify(payload);
        } catch (_) { /* generated client owns body validation */ }
      }
      return nativeFetch(input, next).then(async function (response) {
        if (!response.ok || !lifecycle || wantsEnabled === null) return response;
        let value;
        try { value = await response.clone().json(); } catch (_) { return response; }
        const link = value && value.link;
        if (!link || !link.link_id) return response;
        const target = wantsEnabled ? 'enable' : 'disable';
        if ((wantsEnabled && link.status === 'enabled') || (!wantsEnabled && link.status !== 'enabled')) return response;
        const lifecycleHeaders = new Headers(headers);
        lifecycleHeaders.set('Content-Type', 'application/json');
        lifecycleHeaders.set('Idempotency-Key', 'radar-ui-' + crypto.randomUUID());
        return nativeFetch('/api/admin/radar-links/' + link.link_id + '/' + target, {
          method: 'POST', credentials: 'include', headers: Object.fromEntries(lifecycleHeaders.entries()),
          body: JSON.stringify({ expected_version: link.version })
        });
      });
    }
    if (url.origin === location.origin && radarList.test(url.pathname) && method === 'GET') {
      const generation = listGeneration;
      return nativeFetch(input, init).then(function (response) { return rememberListSummaries(response, generation); });
    }
    return nativeFetch(input, init);
  };

  const hydrated = new Set();
  async function stats(id) {
    const response = await nativeFetch('/api/admin/radar-links/' + id + '/stats', { credentials: 'include' });
    if (!response.ok) throw new Error('stats unavailable');
    return response.json();
  }
  function integerText(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value.toLocaleString() : null;
  }
  function renderUnavailable(row) {
    row.querySelectorAll('td.num').forEach(function (node) { node.textContent = '不可用'; });
    const cells = row.querySelectorAll('td');
    if (cells[6]) cells[6].textContent = '不可用';
  }
  function renderListSummary(row, value) {
    if (listSummaryStatus(value) !== 'ready') {
      renderUnavailable(row);
      return;
    }
    const landings = integerText(value.total_landings);
    const users = integerText(value.authorized_users);
    const views = integerText(value.view_count);
    if (landings === null || users === null || views === null) {
      renderUnavailable(row);
      return;
    }
    const nums = row.querySelectorAll('td.num');
    if (nums[0]) nums[0].textContent = landings;
    if (nums[1]) nums[1].textContent = users;
    if (nums[2]) nums[2].textContent = views;
    const cells = row.querySelectorAll('td');
    if (!cells[6]) return;
    if (value.last_viewed_at === null) {
      cells[6].textContent = '—';
    } else if (typeof value.last_viewed_at === 'string' && value.last_viewed_at) {
      const formatted = formatShanghaiTime(value.last_viewed_at);
      cells[6].textContent = formatted === null ? '不可用' : formatted;
    } else {
      cells[6].textContent = '不可用';
    }
  }
  function formatShanghaiTime(value) {
    const timestamp = new Date(value);
    if (Number.isNaN(timestamp.getTime())) return null;
    const parts = new Intl.DateTimeFormat('zh-CN-u-ca-gregory', {
      timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
    }).formatToParts(timestamp);
    const fields = Object.fromEntries(parts.map(function (part) { return [part.type, part.value]; }));
    if (!fields.month || !fields.day || !fields.hour || !fields.minute) return null;
    return fields.month + '-' + fields.day + ' ' + fields.hour + ':' + fields.minute;
  }
  async function hydrateList() {
    await listSummaryReady;
    const rows = document.querySelectorAll('#listRows tr');
    for (const row of rows) {
      const rowGeneration = row.getAttribute('data-v3-radar-list-generation');
      if (rowGeneration !== null && Number(rowGeneration) !== listGeneration) continue;
      const action = row.querySelector('[data-detail]');
      const id = action && Number(action.getAttribute('data-detail'));
      if (!id || hydratedRows.get(row) === listSummaryRevision) continue;
      renderListSummary(row, listSummaries.get(id));
      hydratedRows.set(row, listSummaryRevision);
    }
  }
  async function hydrateDetail() {
    if (document.body.dataset.page !== 'radarDetail' || hydrated.has('detail')) return;
    const id = Number(new URLSearchParams(location.search).get('id'));
    const nodes = document.querySelectorAll('.stat-row .stat-v');
    if (!id || nodes.length < 4) return;
    hydrated.add('detail');
    try {
      const value = await stats(id);
      const landings = Number(value.total_landings || 0), users = Number(value.authorized_users || 0);
      nodes[0].textContent = landings.toLocaleString(); nodes[1].textContent = users.toLocaleString();
      nodes[2].textContent = Number(value.view_opens || 0).toLocaleString(); nodes[3].textContent = landings ? Math.round(users / landings * 100) + '%' : '0%';
    } catch (_) {
      nodes[0].textContent = '不可用'; nodes[1].textContent = '不可用';
      nodes[2].textContent = '不可用'; nodes[3].textContent = '不可用';
    }
  }
  async function hydrateForm() {
    if (document.body.dataset.page !== 'radarForm' || hydrated.has('form')) return;
    const id = Number(new URLSearchParams(location.search).get('id'));
    const toggle = document.querySelector('#swAuth'); if (!id || !toggle) return;
    hydrated.add('form');
    try {
      const response = await nativeFetch('/api/admin/radar-links/' + id, { credentials: 'include' });
      const value = await response.json();
      if (response.ok && value.link && value.link.auth_policy === 'anonymous') toggle.classList.remove('on');
    } catch (_) { hydrated.delete('form'); }
  }
  function hydrate() {
    if (!document || !document.body) return;
    void hydrateList(); void hydrateDetail(); void hydrateForm();
  }
  new MutationObserver(hydrate).observe(document.documentElement, { childList: true, subtree: true });
  document.addEventListener('DOMContentLoaded', hydrate);
})();
