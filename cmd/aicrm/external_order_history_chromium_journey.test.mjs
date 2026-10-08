import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import test from 'node:test';

const source = await fs.readFile(new URL('./external_order_history_chromium_journey.mjs', import.meta.url), 'utf8');
const helpers = source.slice(source.indexOf('let journeyPhase'), source.indexOf('\nasync function waitForBrowserExit'));
const profileURL = vm.runInNewContext(helpers + '\ncustomerProfileURL', { URL });

function fixture(body, afterDelay = () => {}) {
  const document = { body, readyState: 'loading', querySelector: () => null, querySelectorAll: () => [] };
  const page = { document, location: { pathname: '/admin/orderDetail.html' } };
  let evaluations = 0;
  const cdp = { call: async (method, { expression }) => {
    assert.equal(method, 'Runtime.evaluate');
    evaluations++;
    try {
      return { result: { value: vm.runInNewContext(expression, page) } };
    } catch (error) {
      return { exceptionDetails: { text: 'Uncaught', exception: { description: error.toString() } } };
    }
  } };
  const waitFor = vm.runInNewContext(helpers + '\njourneyPhase="history-detail"; waitFor', {
    delay: async () => afterDelay(document),
  });
  return { cdp, waitFor, evaluations: () => evaluations };
}

test('navigation with no body waits, then evaluates the original predicate', async () => {
  const f = fixture(null, document => { document.body = { textContent: '原支付时间 external-browser-known' }; });
  await f.waitFor(f.cdp, "document.body.textContent.includes('原支付时间') && document.body.textContent.includes('external-browser-known')", 'history detail missing');
  assert.equal(f.evaluations(), 2);
});

test('body presence does not satisfy a false business predicate', async () => {
  let delays = 0;
  const f = fixture({ textContent: 'not ready' }, document => {
    if (++delays === 2) document.body.textContent = 'expected-order';
  });
  await f.waitFor(f.cdp, "document.body.textContent.includes('expected-order')", 'order missing');
  assert.equal(delays, 2);
  assert.equal(f.evaluations(), 3);
});

test('other page exceptions still fail with the exact phase and description', async () => {
  const f = fixture({ textContent: 'ready' });
  await assert.rejects(f.waitFor(f.cdp, "(()=>{throw new Error('business predicate failure')})()", 'unused timeout'), error =>
    error.message.includes('phase=history-detail') && error.message.includes('Error: business predicate failure')
  );
});

test('actual page collector excludes placeholders and preserves relative profile hrefs', () => {
  const expressionLiteral = source.match(/const page=await evaluate\(cdp,(.*)\);/)[1];
  const expression = vm.runInNewContext(expressionLiteral);
  const anchors = [
    { hasHref: false, getAttribute: () => null },
    { hasHref: true, getAttribute: () => '' },
    { hasHref: true, getAttribute: () => '   ' },
    { hasHref: true, getAttribute: () => '/admin/customers/42' },
  ];
  const page = vm.runInNewContext(expression, { document: {
    body: { textContent: 'history' },
    querySelectorAll: selector => {
      if (selector === 'button') return [];
      assert.equal(selector, 'a[href]');
      return anchors.filter(a => a.hasHref);
    },
  } });
  assert.deepEqual(Array.from(page.links), ['/admin/customers/42']);
  assert.equal(profileURL(page.links, 'https://fixture.test'), 'https://fixture.test/admin/customers/42');
});

test('empty and non-string hrefs cannot hide a real canonical profile link', () => {
  const links = [null, '', ' ', { baseVal: '#' }, '#', '/admin/customerDetail.html?id=42', '/admin/customers/42'];
  assert.equal(profileURL(links, 'https://fixture.test'), 'https://fixture.test/admin/customers/42');
  assert.equal(profileURL(['', '/admin/customerDetail.html?id=42'], 'https://fixture.test'), undefined);
  assert.throws(() => profileURL(['http://['], 'https://fixture.test'), /Invalid URL/);
});
