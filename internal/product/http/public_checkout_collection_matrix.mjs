import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import {JSDOM, VirtualConsole} from 'jsdom';

const dir = process.argv[2];
const settle = async () => {for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve));};

for (const kind of ['standard', 'service_period']) {
  for (const level of ['none', 'mobile', 'shipping_address']) {
    for (const provider of ['wechat', 'alipay']) {
      const html = await readFile(path.join(dir, `${kind}-${level}.html`), 'utf8');
      const calls = [], errors = [];
      const checkoutRoute = provider === 'alipay' ? '/api/v1/alipay/checkouts' : '/api/v1/wechat-pay/checkouts';
      const console = new VirtualConsole();
      console.on('jsdomError', error => errors.push(error.message));
      const page = new JSDOM(html, {
        url: 'https://example.test/pay/fixture', runScripts: 'dangerously', virtualConsole: console,
        beforeParse(window) {
          Object.defineProperty(window.navigator, 'userAgent', {value: provider === 'wechat' ? 'MicroMessenger' : 'Mozilla/5.0'});
          Object.defineProperty(window.crypto, 'randomUUID', {value: () => `matrix-${kind}-${level}-${provider}`});
          window.fetch = async (url, options = {}) => {
            const route = String(url), method = options.method || 'GET';
            calls.push({route, method, body: options.body});
            const body = route === '/api/v1/wechat-pay/checkout-session'
              ? {checkout_session_binding: 'a'.repeat(43), can_create_checkout: true}
              : route.startsWith('/api/v1/wechat-pay/purchase-status')
                ? {purchase_state: 'available', can_purchase: true}
                : route.startsWith('/api/h5/coupons/available')
                  ? {items: []}
                  : route === checkoutRoute && method === 'POST'
                    ? {code: 'unavailable'}
                    : assert.fail(`unexpected matrix request ${method} ${route}`);
            const rejected = route === checkoutRoute && method === 'POST';
            return {ok: !rejected, status: rejected ? 503 : 200, async json() {return body;}};
          };
        },
      });
      const {document, Event} = page.window;
      await settle();
      assert.equal(errors.length, 0, `${kind}/${level}/${provider}: ${errors.join('; ')}`);
      assert.equal(document.getElementById('identityGate').hidden, true);
      assert.equal(document.querySelector('#checkoutContent > .product img'), null, 'checkout product has no image');
      assert.equal(document.getElementById('couponPanel').hidden, false);
      assert.equal(document.querySelector('[id*=Note], [id*=Remark]'), null, 'checkout has no notes');
      if (provider === 'alipay') document.querySelector('input[name=paymentMethod][value=alipay]').click();

      document.getElementById('buy').click();
      await settle();
      const posts = () => calls.filter(call => call.route === checkoutRoute && call.method === 'POST');
      if (level !== 'none') {
        assert.equal(posts().length, 0, `${kind}/${level}/${provider}: invalid details must block order creation`);
        assert.equal(document.getElementById('mobileError').textContent, '请填写手机号');
        assert.match(document.getElementById('paymentErrors').textContent, /手机号/);
        document.getElementById('mobile').value = '13800138000';
      }
      if (level === 'shipping_address') {
        assert.equal(document.getElementById('provinceError').textContent, '请选择省');
        assert.equal(document.querySelector('.shipping-purpose').textContent, '收实物商品使用');
        const province = document.getElementById('province'), city = document.getElementById('city'), district = document.getElementById('district');
        province.value = '11'; province.dispatchEvent(new Event('change'));
        city.value = '1101'; city.dispatchEvent(new Event('change'));
        district.value = '110101'; district.dispatchEvent(new Event('change'));
        document.getElementById('recipientName').value = '测试收件人';
        document.getElementById('detailAddress').value = '测试路 1 号';
      }
      if (level !== 'none') {document.getElementById('buy').click(); await settle();}
      assert.equal(posts().length, 1, `${kind}/${level}/${provider}: valid details create one original order`);
      const payload = JSON.parse(posts()[0].body);
      assert.equal(payload.product_kind, kind);
      assert.equal(payload.contact_collection_level, level);
      assert.equal(payload.coupon_claim_id, 0);
      if (provider === 'alipay') assert.equal(payload.provider, 'alipay');
      else assert.equal(payload.provider, undefined);
      if (level === 'none') assert.equal(payload.mobile, undefined);
      else assert.equal(payload.mobile, '+8613800138000');
      if (level === 'shipping_address') {
        assert.equal(payload.recipient_name, '测试收件人');
        assert.equal(payload.detail_address, '测试路 1 号');
        assert.equal(payload.district_code, '110101');
      } else assert.equal(payload.recipient_name, undefined);
      page.window.close();
    }
  }
}
