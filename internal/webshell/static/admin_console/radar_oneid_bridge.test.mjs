import assert from 'node:assert/strict';
import fs from 'node:fs';
import { JSDOM } from 'jsdom';

const bridge = fs.readFileSync(new URL('./radar_oneid_bridge.js', import.meta.url), 'utf8');
const wait = () => new Promise((resolve) => setTimeout(resolve, 20));

function row(id) {
  return `<tr><td>标题</td><td>链接</td><td>启用</td><td class="num">0</td><td class="num">0</td><td class="num">0</td><td>2026-09-01</td><td><button data-detail="${id}">详情</button></td></tr>`;
}

function setup(response) {
  const calls = [];
  const dom = new JSDOM('<!doctype html><body><table><tbody id="listRows"></tbody></table></body>', {
    url: 'https://crm.example/admin/radar.html', runScripts: 'dangerously', pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.Request = Request;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        calls.push({ path: url.pathname, method: init.method || 'GET' });
        if (url.pathname === '/api/admin/radar-links') return new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } });
        return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      };
    },
  });
  dom.window.eval(bridge);
  return { dom, calls };
}

{
  const { dom, calls } = setup({ items: [{ link_id: 7, statistics_status: 'ready', total_landings: 12, authorized_users: 3, view_count: 4, last_viewed_at: '2026-09-12T09:30:00Z' }] });
  await dom.window.fetch('/api/admin/radar-links');
  await wait();
  dom.window.document.querySelector('#listRows').innerHTML = row(7);
  await wait();
  const cells = dom.window.document.querySelectorAll('#listRows td');
  assert.equal(cells[3].textContent, '12', 'list uses the measured landing count');
  assert.equal(cells[4].textContent, '3', 'list uses the measured resolved-customer UV');
  assert.equal(cells[5].textContent, '4', 'list uses the measured view-stage count');
  assert.equal(cells[6].textContent, '09-12 17:30', 'list renders the measured last view in Asia/Shanghai rather than updated_at');
  assert.equal(calls.filter((call) => /\/stats$/.test(call.path)).length, 0, 'list does not issue one stats request per row');
  dom.window.document.querySelector('#listRows').innerHTML = '';
  await wait();
  dom.window.document.querySelector('#listRows').innerHTML = row(7);
  await wait();
  const repaintedCells = dom.window.document.querySelectorAll('#listRows td');
  assert.equal(repaintedCells[3].textContent, '12', 'a locally rebuilt row receives its summary again');
  assert.equal(repaintedCells[6].textContent, '09-12 17:30', 'a locally rebuilt row keeps the Shanghai last-view time');
  assert.equal(calls.filter((call) => /\/stats$/.test(call.path)).length, 0, 'locally rebuilding a row does not issue stats requests');
  dom.window.close();
}

{
  const { dom, calls } = setup({ items: [{ link_id: 8, statistics_status: 'unavailable', total_landings: null, authorized_users: null, view_count: null, last_viewed_at: null }] });
  await dom.window.fetch('/api/admin/radar-links');
  await wait();
  dom.window.document.querySelector('#listRows').innerHTML = row(8);
  await wait();
  const cells = dom.window.document.querySelectorAll('#listRows td');
  assert.equal(cells[3].textContent, '不可用');
  assert.equal(cells[4].textContent, '不可用');
  assert.equal(cells[5].textContent, '不可用');
  assert.equal(cells[6].textContent, '不可用', 'failed statistics never fall back to updated_at');
  assert.equal(calls.filter((call) => /\/stats$/.test(call.path)).length, 0, 'unavailable list statistics do not trigger detail-stat retries');
  dom.window.close();
}

{
  const { dom } = setup({ items: [{ link_id: 9, statistics_status: 'ready', total_landings: 0, authorized_users: 0, view_count: 0, last_viewed_at: null }] });
  await dom.window.fetch('/api/admin/radar-links');
  await wait();
  dom.window.document.querySelector('#listRows').innerHTML = row(9);
  await wait();
  const cells = dom.window.document.querySelectorAll('#listRows td');
  assert.equal(cells[3].textContent, '0');
  assert.equal(cells[4].textContent, '0');
  assert.equal(cells[5].textContent, '0');
  assert.equal(cells[6].textContent, '—', 'a ready link with no view has no last-view timestamp');
  dom.window.close();
}

{
  const { dom } = setup({ items: [{ link_id: 10, statistics_status: 'ready', total_landings: 1, authorized_users: 1, view_count: 1, last_viewed_at: 'not-a-timestamp' }] });
  await dom.window.fetch('/api/admin/radar-links');
  await wait();
  dom.window.document.querySelector('#listRows').innerHTML = row(10);
  await wait();
  assert.equal(dom.window.document.querySelectorAll('#listRows td')[6].textContent, '不可用', 'an invalid last-view timestamp is unavailable');
  dom.window.close();
}

console.log('radar list statistics bridge: ok');
