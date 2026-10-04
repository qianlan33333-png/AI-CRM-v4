import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';

const env = process.env;
const required = [
  'AICRM_PHASE2_BASE_URL', 'AICRM_PHASE2_EXPECTED_ENGINE', 'AICRM_PHASE2_PLAYWRIGHT_ROOT',
  'AICRM_PHASE2_WEBKIT_DRIVER', 'AICRM_PHASE2_SCREENSHOT_DIR', 'AICRM_PHASE2_STANDARD_ID',
  'AICRM_PHASE2_SERVICE_ID', 'AICRM_PHASE2_STANDARD_CODE', 'AICRM_PHASE2_SERVICE_CODE',
  'AICRM_PHASE2_ADMIN_SESSION', 'AICRM_PHASE2_ADMIN_CSRF', 'AICRM_PHASE2_SESSION_COOKIE_NAME',
  'AICRM_PHASE2_CSRF_COOKIE_NAME', 'AICRM_PHASE2_PAYMENT_COOKIE_NAME', 'AICRM_PHASE2_PAYMENT_SESSION',
];
for (const name of required) if (!env[name]) throw new Error(name + ' is required');

const requireFromBrowserEnv = createRequire(path.join(env.AICRM_PHASE2_PLAYWRIGHT_ROOT, 'package.json'));
const { webkit, devices } = requireFromBrowserEnv('playwright');
const baseURL = env.AICRM_PHASE2_BASE_URL.replace(/\/$/, '');
const screenshotDir = env.AICRM_PHASE2_SCREENSHOT_DIR;
await fs.mkdir(screenshotDir, { recursive: true, mode: 0o700 });
const browser = await webkit.launch({ headless: true, executablePath: env.AICRM_PHASE2_WEBKIT_DRIVER, timeout: 30000 });
const runtime = { expected: env.AICRM_PHASE2_EXPECTED_ENGINE, actual: browser.version(), userAgent: '' };
let releaseFirstCheckout;
try {
  assert.equal(runtime.actual, runtime.expected, 'the launched WebKit engine must match the independent receipt');
  // The real public purchase route requires the WeChat browser environment.
  // Keep WebKit's iPhone descriptor and simulate only this entry condition.
  const browserOptions = { ...devices['iPhone 12'], userAgent: devices['iPhone 12'].userAgent + ' MicroMessenger/8.0.0', ignoreHTTPSErrors: true };
  const context = await browser.newContext(browserOptions);
  await blockExternalRequests(context);
  await context.addCookies([
    { name: env.AICRM_PHASE2_SESSION_COOKIE_NAME, value: env.AICRM_PHASE2_ADMIN_SESSION, url: baseURL, httpOnly: true },
    { name: env.AICRM_PHASE2_CSRF_COOKIE_NAME, value: env.AICRM_PHASE2_ADMIN_CSRF, url: baseURL },
    { name: env.AICRM_PHASE2_PAYMENT_COOKIE_NAME, value: env.AICRM_PHASE2_PAYMENT_SESSION, url: baseURL, httpOnly: true },
  ]);
  await context.addInitScript(() => {
    window.__phase2InputEvents = {};
    document.addEventListener('input', event => {
      const target = event.target;
      if (!target || !target.id) return;
      window.__phase2InputEvents[target.id] = (window.__phase2InputEvents[target.id] || 0) + 1;
    }, true);
  });
  const checkoutPage = await context.newPage();
  const pageErrors = [];
  checkoutPage.on('pageerror', error => pageErrors.push(String(error.message || error)));
  const checkoutPosts = [];
  let secondCheckoutResolved = false;
  const firstCheckoutGate = new Promise(resolve => { releaseFirstCheckout = resolve; });
  await checkoutPage.route('**/api/v1/wechat-pay/checkouts', async route => {
    const request = route.request();
    if (request.method() !== 'POST') return route.continue();
    const headers = request.headers();
    checkoutPosts.push({
      key: headers['idempotency-key'] || headers['Idempotency-Key'] || '',
      body: JSON.parse(request.postData() || '{}'),
    });
    if (checkoutPosts.length === 1) {
      await firstCheckoutGate;
      try { await route.abort(); } catch (_) {}
      return;
    }
    if (checkoutPosts.length === 2) {
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'fixture_response_lost' }) });
      secondCheckoutResolved = true;
      if (releaseFirstCheckout) releaseFirstCheckout();
      return;
    }
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'unexpected_extra_checkout' }) });
  });
  await checkoutPage.goto(baseURL + '/pay/' + env.AICRM_PHASE2_STANDARD_CODE, { waitUntil: 'domcontentloaded' });
  await checkoutPage.locator('#checkoutContent').waitFor({ state: 'visible', timeout: 30000 });
  runtime.userAgent = await checkoutPage.evaluate(() => navigator.userAgent);
  const checkoutInput = await restoreShippingInputs(checkoutPage);
  assert.equal(checkoutInput.inputEvents.mobile || 0, 0, 'restored mobile value must not require an input event');
  assert.equal(checkoutInput.inputEvents.recipientName || 0, 0, 'restored recipient value must not require an input event');
  assert.equal(checkoutInput.inputEvents.detailAddress || 0, 0, 'restored address value must not require an input event');
  await checkoutPage.screenshot({ path: path.join(screenshotDir, '01-public-checkout-restored-address.png'), fullPage: true });
  await checkoutPage.locator('#buy').tap();
  await waitFor(() => checkoutPosts.length === 1, 'first checkout POST was not observed', 30000);
  await waitFor(async () => checkoutPage.locator('#buy').textContent().then(text => text.trim() === '重试原请求'), 'checkout timeout did not restore the manual retry state', 25000);
  const firstRequestBody = checkoutPosts[0].body;
  assert.equal(firstRequestBody.product_kind, 'standard');
  assert.equal(firstRequestBody.contact_collection_level, 'shipping_address');
  assert.equal(firstRequestBody.mobile, '+8613800138000');
  assert.equal(firstRequestBody.recipient_name, '浏览器测试收件人');
  assert.equal(firstRequestBody.detail_address, '测试路 12 号');
  assert.equal(firstRequestBody.province_code, checkoutInput.province);
  assert.equal(firstRequestBody.city_code, checkoutInput.city);
  assert.equal(firstRequestBody.district_code, checkoutInput.district);
  assert.equal(firstRequestBody.province_name, checkoutInput.provinceName);
  assert.equal(firstRequestBody.city_name, checkoutInput.cityName);
  assert.equal(firstRequestBody.district_name, checkoutInput.districtName);
  await checkoutPage.screenshot({ path: path.join(screenshotDir, '02-checkout-timeout-original-request.png'), fullPage: true });
  await delay(500);
  assert.equal(checkoutPosts.length, 1, 'timeout must not automatically create a second checkout');
  assert.equal(await checkoutPage.locator('#buy').isEnabled(), true, 'manual recovery action must be enabled');
  await checkoutPage.locator('#buy').tap();
  await waitFor(() => secondCheckoutResolved && checkoutPosts.length === 2, 'manual retry POST was not observed', 30000);
  assert.equal(checkoutPosts[1].key, checkoutPosts[0].key, 'manual retry must reuse the original Idempotency-Key');
  assert.deepEqual(checkoutPosts[1].body, checkoutPosts[0].body, 'manual retry must preserve the original checkout payload');
  await delay(500);
  assert.equal(checkoutPosts.length, 2, 'the failed manual retry must not loop or auto-create another checkout');
  await checkoutPage.screenshot({ path: path.join(screenshotDir, '03-checkout-manual-retry-same-key.png'), fullPage: true });

  const servicePage = await context.newPage();
  const serviceResponses = [];
  servicePage.on('response', async response => {
    if (new URL(response.url()).pathname.includes('/api/h5/service-period-products/')) {
      try { serviceResponses.push(await response.json()); } catch (_) {}
    }
  });
  await servicePage.goto(baseURL + '/s/' + env.AICRM_PHASE2_SERVICE_CODE, { waitUntil: 'domcontentloaded' });
  await servicePage.locator('#servicePeriodPayButton').waitFor({ state: 'visible', timeout: 30000 });
  await waitFor(() => serviceResponses.length > 0, 'authenticated service-period state read was not observed', 30000);
  const trustedServiceState = serviceResponses[serviceResponses.length - 1];
  assert.equal(trustedServiceState.authenticated, true, 'trusted local session should read the actual service-period identity boundary');
  assert.equal(await servicePage.locator('#servicePeriodPayButton').textContent().then(text => text.trim()), '立即报名');
  await servicePage.screenshot({ path: path.join(screenshotDir, '04-service-period-trusted-identity.png'), fullPage: true });

  // Exercise the distinct service-period payment entry as well as its detail
  // identity page. This request is intercepted locally, as for ordinary pay.
  const periodCheckout = await context.newPage();
  periodCheckout.on('pageerror', error => pageErrors.push(String(error.message || error)));
  const periodPosts = [];
  await periodCheckout.route('**/api/v1/wechat-pay/checkouts', async route => {
    const request = route.request();
    if (request.method() !== 'POST') return route.continue();
    periodPosts.push({ key: request.headers()['idempotency-key'], body: JSON.parse(request.postData() || '{}') });
    await route.fulfill({ status: 503, contentType: 'application/json', body: '{"code":"fixture_response_lost"}' });
  });
  await periodCheckout.goto(baseURL + '/s/' + env.AICRM_PHASE2_SERVICE_CODE + '/pay', { waitUntil: 'domcontentloaded' });
  await periodCheckout.locator('#checkoutContent').waitFor({ state: 'visible', timeout: 30000 });
  await restoreShippingInputs(periodCheckout);
  await periodCheckout.locator('#buy').tap();
  await waitFor(() => periodPosts.length === 1, 'service-period checkout POST was not observed', 10000);
  await waitFor(async () => (await periodCheckout.locator('#buy').textContent()).trim() === '重试原请求', 'service-period original request recovery missing', 10000);
  assert.equal(periodPosts[0].body.product_kind, 'service_period');
  assert.equal(periodPosts[0].body.product_id, Number(env.AICRM_PHASE2_SERVICE_ID));
  assert.equal(periodPosts[0].body.mobile, '+8613800138000');
  assert.equal(periodPosts[0].body.detail_address, '测试路 12 号');
  assert.equal(periodPosts[0].body.beneficiary_selection, 'payer_self');
  await periodCheckout.locator('#buy').tap();
  await waitFor(() => periodPosts.length === 2, 'service-period manual retry was not observed', 10000);
  assert.equal(periodPosts[1].key, periodPosts[0].key);
  assert.deepEqual(periodPosts[1].body, periodPosts[0].body);
  await delay(500);
  assert.equal(periodPosts.length, 2, 'service-period failed retry must not loop');
  await periodCheckout.screenshot({ path: path.join(screenshotDir, '04a-service-period-payment-original-retry.png'), fullPage: true });

  const anonymousContext = await browser.newContext(browserOptions);
  await blockExternalRequests(anonymousContext);
  // Model the real OAuth refusal-return state: its automatic attempt remains
  // in tab storage until a trusted read succeeds. No Provider is contacted.
  await anonymousContext.addInitScript(() => {
    sessionStorage.setItem("aicrm.oauth.auto:" + window.location.pathname, "1");
  });
  const anonymousPage = await anonymousContext.newPage();
  const anonymousPosts = [];
  anonymousPage.on('request', request => {
    if (request.method() === 'POST') anonymousPosts.push(request.url());
  });
  const anonymousResponses = [];
  anonymousPage.on('response', async response => {
    if (new URL(response.url()).pathname.includes('/api/h5/service-period-products/')) {
      try { anonymousResponses.push(await response.json()); } catch (_) {}
    }
  });
  await anonymousPage.goto(baseURL + '/s/' + env.AICRM_PHASE2_SERVICE_CODE, { waitUntil: 'domcontentloaded' });
  await anonymousPage.locator('#servicePeriodPayButton').waitFor({ state: 'visible', timeout: 30000 });
  await waitFor(() => anonymousResponses.length > 0, 'anonymous service-period state read was not observed', 30000);
  assert.equal(anonymousResponses[anonymousResponses.length - 1].authenticated, false);
  assert.equal((await anonymousPage.locator('#servicePeriodPayButton').textContent()).trim(), '重新授权', 'unverified identity must offer authorization instead of purchase');
  assert.equal(await anonymousPage.locator('#servicePeriodPayButton').isEnabled(), true, 'OAuth refusal must retain the existing manual authorization recovery action');
  const anonymousText = await anonymousPage.locator('#servicePeriodStateCard').textContent();
  assert.doesNotMatch(anonymousText, /剩余有效期|剩余\s*\d+\s*天/);
  assert.equal(anonymousPosts.length, 0, 'anonymous identity read must not issue a purchase POST');
  await anonymousPage.screenshot({ path: path.join(screenshotDir, '05-service-period-anonymous-boundary.png'), fullPage: true });
  await anonymousContext.close();

  const adminPage = await context.newPage();
  adminPage.on('pageerror', error => pageErrors.push(String(error.message || error)));
  await editProduct(adminPage, {
    id: env.AICRM_PHASE2_STANDARD_ID, kind: 'standard', prefix: 'pf',
    name: 'edited-' + env.AICRM_PHASE2_EXPECTED_ENGINE + '-standard',
  }, screenshotDir);
  await editProduct(adminPage, {
    id: env.AICRM_PHASE2_SERVICE_ID, kind: 'service_period', prefix: 'spf',
    name: 'edited-' + env.AICRM_PHASE2_EXPECTED_ENGINE + '-service',
  }, screenshotDir);
  assert.deepEqual(pageErrors, [], 'real production pages should not report uncaught script errors');

  const receiptPayload = { ...firstRequestBody };
  delete receiptPayload.checkout_session_binding;
  const receipt = {
    engine: runtime.actual, userAgent: runtime.userAgent,
    checkout: {
      restoredTextInputsHadNoInputEvents: true,
      firstPostCount: 1, manualRetryCount: checkoutPosts.length,
      sameIdempotencyKey: checkoutPosts[0].key === checkoutPosts[1].key,
      addressPayload: receiptPayload,
    },
    servicePeriodCheckout: { postCount: periodPosts.length, productKind: periodPosts[0].body.product_kind, sameIdempotencyKey: periodPosts[0].key === periodPosts[1].key, samePayload: true, providerIntercepted: true },
    servicePeriod: {
      trustedAuthenticated: trustedServiceState.authenticated,
      anonymousAuthenticated: anonymousResponses[anonymousResponses.length - 1].authenticated,
      anonymousPurchasePosts: anonymousPosts.length,
    },
    pageErrors,
    limits: ['checkout creation POST was intercepted and did not reach the payment handler', 'all external browser requests were blocked', 'WeChat UA and OAuth refusal-return tab state are simulated; this is not a physical iPhone or Provider acceptance'],
  };
  await fs.writeFile(path.join(screenshotDir, 'receipt.json'), JSON.stringify(receipt, null, 2) + '\n', { mode: 0o600 });
  console.log(JSON.stringify({ event: 'commerce_product_webkit_phase2', status: 'PASS', ...receipt }));
} finally {
  if (releaseFirstCheckout) releaseFirstCheckout();
  await browser.close();
}

async function restoreShippingInputs(page) {
  return page.evaluate(() => {
    const get = id => document.getElementById(id);
    const province = get('province');
    const city = get('city');
    const district = get('district');
    if (!(province instanceof HTMLSelectElement) || province.options.length < 2) throw new Error('province options unavailable');
    province.value = province.options[1].value;
    province.dispatchEvent(new Event('change', { bubbles: true }));
    if (!(city instanceof HTMLSelectElement) || city.options.length < 2) throw new Error('city options unavailable');
    city.value = city.options[1].value;
    city.dispatchEvent(new Event('change', { bubbles: true }));
    if (!(district instanceof HTMLSelectElement) || district.options.length < 2) throw new Error('district options unavailable');
    district.value = district.options[1].value;
    get('mobile').value = '13800138000';
    get('recipientName').value = '浏览器测试收件人';
    get('detailAddress').value = '测试路 12 号';
    return {
      province: province.value, provinceName: province.options[province.selectedIndex].textContent,
      city: city.value, cityName: city.options[city.selectedIndex].textContent,
      district: district.value, districtName: district.options[district.selectedIndex].textContent,
      inputEvents: window.__phase2InputEvents,
    };
  });
}

async function blockExternalRequests(context) {
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    return url.origin === new URL(baseURL).origin ? route.continue() : route.abort();
  });
}

async function editProduct(page, product, screenshotDirectory) {
  const id = String(product.id);
  const prefix = product.prefix;
  const pathForPage = product.kind === 'standard' ? '/admin/productForm.html?id=' + id : '/admin/spProductForm.html?id=' + id;
  const apiPath = product.kind === 'standard' ? '/api/v1/products/' + id : '/api/admin/service-period-products/' + id;
  const priceSelector = '#' + prefix + 'Price';
  const nameSelector = '#' + prefix + 'Name';
  const mediaSelector = product.kind === 'standard' ? '#product-media' : '#sp-media';
  const tabSelector = product.kind === 'standard' ? 'a[href="#product-media"]' : 'a[href="#sp-media"]';
  const writes = [];
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.pathname === apiPath && request.method() === 'PUT') {
      writes.push({ body: JSON.parse(request.postData() || '{}'), key: request.headers()['idempotency-key'] || '' });
    }
  });
  await page.goto(new URL(pathForPage, baseURL).toString(), { waitUntil: 'domcontentloaded' });
  await page.locator(priceSelector).waitFor({ state: 'visible', timeout: 30000 });
  assert.equal((await page.locator(priceSelector).inputValue()).trim(), '12.34', product.kind + ' price_minor=1234 must render as 12.34 yuan');
  assert.equal(await page.evaluate(prefixValue => window.__phase2InputEvents[prefixValue + 'Price'] || 0, prefix), 0, 'hydrating an existing price must not require an input event');
  await page.screenshot({ path: path.join(screenshotDirectory, '06-' + product.kind + '-editor-price-restored.png'), fullPage: true });

  await page.locator(nameSelector).fill(product.name);
  assert.equal(await page.evaluate(prefixValue => window.__phase2InputEvents[prefixValue + 'Price'] || 0, prefix), 0, 'editing only the name must leave the restored price without input events');
  await saveCurrentDimension(page, apiPath, writes);
  assert.equal(writes[0].body.price_minor, 1234, product.kind + ' save after a non-price edit must preserve price_minor');
  let readback = await readProduct(page, apiPath);
  assert.equal(Number(readback.price_minor), 1234, product.kind + ' HTTP readback after name save');

  await page.locator(priceSelector).fill('12.34');
  await saveCurrentDimension(page, apiPath, writes);
  assert.equal(writes[1].body.price_minor, 1234, product.kind + ' decimal yuan edit must serialize exact integer cents');
  readback = await readProduct(page, apiPath);
  assert.equal(Number(readback.price_minor), 1234, product.kind + ' exact-cent HTTP readback');

  await page.locator(tabSelector).tap();
  await page.locator(mediaSelector + ' [data-v3-product-material-key]').first().waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await page.locator(mediaSelector + ' [data-v3-product-material-key]').count(), 1, product.kind + ' fixture must start with exactly one image');
  await page.screenshot({ path: path.join(screenshotDirectory, '07-' + product.kind + '-editor-last-image.png'), fullPage: true });
  await page.locator(mediaSelector + ' [data-v3-product-material-action="remove"]').tap();
  await waitFor(async () => (await page.locator(mediaSelector + ' [data-v3-product-material-key]').count()) === 0, product.kind + ' last image did not leave the owner draft', 10000);
  await page.screenshot({ path: path.join(screenshotDirectory, '08-' + product.kind + '-editor-empty-image-draft.png'), fullPage: true });
  await saveCurrentDimension(page, apiPath, writes);
  assert.deepEqual(writes[2].body.images, [], product.kind + ' actual browser PUT must send an explicit empty images array');
  readback = await readProduct(page, apiPath);
  assert.deepEqual(readback.images, [], product.kind + ' HTTP readback must remain an empty array');
}

async function saveCurrentDimension(page, apiPath, writes) {
  const before = writes.length;
  const response = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.pathname === apiPath && response.request().method() === 'PUT';
  }, { timeout: 30000 });
  const buttons = await page.$$('button');
  let tapped = false;
  for (const button of buttons) {
    if ((await button.textContent() || '').trim() !== '保存当前维度') continue;
    if (!(await button.isVisible()) || !(await button.isEnabled())) continue;
    await button.tap();
    tapped = true;
    break;
  }
  if (!tapped) throw new Error('visible Save current dimension button was not found');
  const saved = await response;
  assert.equal(saved.status(), 200, 'Product save must return HTTP 200');
  await waitFor(async () => writes.length === before + 1, 'browser PUT body was not recorded', 10000);
}

async function readProduct(page, apiPath) {
  const raw = await page.evaluate(async pathValue => {
    const response = await fetch(pathValue, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error('product readback HTTP ' + response.status);
    return response.json();
  }, apiPath);
  return raw && raw.product ? raw.product : raw;
}

async function waitFor(check, message, timeout = 15000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    try {
      if (await check()) return;
    } catch (_) {}
    await delay(100);
  }
  throw new Error(message);
}

function delay(milliseconds) {
  return new Promise(resolve => setTimeout(resolve, milliseconds));
}
