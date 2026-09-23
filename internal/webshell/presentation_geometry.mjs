// Extra presentation assertions in the existing real-Host Chromium journey.
// All data and browser sessions belong to the isolated PostgreSQL fixture.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';

export async function verifyPresentation({ cdp, evaluate, waitFor, capture, baseURL, productID, screenshotDirectory }) {
  const metrics = [];
  const resize = (width, height = 900) => cdp.call('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
  // Hold only the presentation entry download; no business write is issued.
  let pausedScript;
  cdp.on('Fetch.requestPaused', event => { pausedScript = event.requestId; });
  await cdp.call('Network.setCacheDisabled', { cacheDisabled: true });
  await cdp.call('Fetch.enable', { patterns: [{ urlPattern: '*productHost*', resourceType: 'Script', requestStage: 'Request' }] });
  await cdp.call('Page.navigate', { url: baseURL + '/admin/productForm.html?id=' + productID });
  await waitFor(cdp, 'Boolean(window.__AICRMSurfaceFeedback) && Boolean(document.querySelector("#stage .surface-feedback__spinner"))', 'static loading did not appear while the page script was held');
  assert.ok(pausedScript, 'product entry was not intercepted');
  // The initial spinner is server-rendered before the asynchronous feedback
  // Host. Its DOM can therefore exist before its stylesheet has taken effect
  // on a cold Linux runner; wait for stylesheet application, not centering.
  await waitFor(cdp, `(() => {
    const spinner=document.querySelector('#stage .surface-feedback__spinner');
    const busy=spinner?.closest('.surface-feedback__busy--initial');
    const feedbackStyles=Array.from(document.styleSheets).some(sheet => typeof sheet.href === 'string' && sheet.href.includes('surfaceFeedbackStyles'));
    if (!spinner || !busy || !feedbackStyles) return false;
    const spinnerStyle=getComputedStyle(spinner), busyStyle=getComputedStyle(busy);
    return spinnerStyle.width === '24px' && spinnerStyle.height === '24px' && busyStyle.display === 'flex' && busyStyle.justifyContent === 'center';
  })()`, 'static loading styles did not become ready before geometry measurement');
  await evaluate(cdp, 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  const loadingGeometry = await evaluate(cdp, `(() => {
    const box=node => { const rect=node?.getBoundingClientRect(); return rect && {top:rect.top,bottom:rect.bottom,width:rect.width,height:rect.height}; };
    const spinner=document.querySelector('#stage .surface-feedback__spinner');
    const busy=spinner?.closest('.surface-feedback__busy--initial');
    const stage=document.querySelector('#stage');
    const spinnerStyle=spinner && getComputedStyle(spinner), busyStyle=busy && getComputedStyle(busy);
    const center=spinner ? (spinner.getBoundingClientRect().top+spinner.getBoundingClientRect().height/2)/innerHeight : null;
    return {center,viewport:{width:innerWidth,height:innerHeight},spinner:box(spinner),busy:box(busy),stage:box(stage),styles:{spinnerWidth:spinnerStyle?.width,busyDisplay:busyStyle?.display,busyJustifyContent:busyStyle?.justifyContent,feedbackStyles:Array.from(document.styleSheets).some(sheet => typeof sheet.href === 'string' && sheet.href.includes('surfaceFeedbackStyles'))}};
  })()`);
  assert.ok(loadingGeometry.center > .3 && loadingGeometry.center < .7, 'page loading should be centered in the content area: ' + JSON.stringify(loadingGeometry));
  await capture('page-loading-slow-script');
  await cdp.call('Fetch.failRequest', { requestId: pausedScript, errorReason: 'Failed' });
  await waitFor(cdp, 'Boolean(document.querySelector("[data-surface-resource-error] button"))', 'failed page script has no reload action');
  assert.equal(await evaluate(cdp, 'Boolean(document.querySelector("#stage .surface-feedback__spinner"))'), false);
  await capture('page-loading-resource-failure');
  await cdp.call('Fetch.disable');
  await cdp.call('Network.setCacheDisabled', { cacheDisabled: false });
  await cdp.call('Page.navigate', { url: baseURL + '/admin/productForm.html?id=' + productID });
  await waitFor(cdp, 'Boolean(document.querySelector("#pfName")) && Boolean(window.__AICRMSurfaceFeedback)', 'product presentation did not mount');
  await evaluate(cdp, 'document.fonts.ready');
  await resize(1440);
  const value = await evaluate(cdp, `(() => { const input=document.querySelector('#pfName'); return {font:getComputedStyle(input).fontSize, surface:document.body.dataset.uiSurface, controlHeight:input.getBoundingClientRect().height}; })()`);
  assert.equal(value.surface, 'admin');
  assert.equal(value.font, '14px');
  assert.equal(value.controlHeight, 36);
  metrics.push({page:'product', ...value});
  await capture('product-after-1440');
  // The unchanged donor/Host DOM with only the new typography sheet disabled
  // is a repeatable before comparison, using exactly the same fixture data.
  await evaluate(cdp, `Array.from(document.querySelectorAll('link[rel="stylesheet"]')).filter(n=>n.href.includes('presentationStyles')).forEach(n=>n.disabled=true)`);
  await capture('product-before-1440');
  await evaluate(cdp, `Array.from(document.querySelectorAll('link[rel="stylesheet"]')).filter(n=>n.href.includes('presentationStyles')).forEach(n=>n.disabled=false)`);

  await cdp.call('Page.navigate', { url: baseURL + '/admin/channelForm.html' });
  await waitFor(cdp, 'Boolean(document.querySelector(".channel-form-v3 .panel-title-row h3"))', 'channel form did not mount');
  for (const width of [1024,1366,1440,1920]) {
    await resize(width);
    const geometry = await evaluate(cdp, `(() => {const root=document.querySelector('.channel-form-v3'); const h=root.querySelector('.panel-title-row h3'); return {width:innerWidth, font:getComputedStyle(h).fontSize, weight:getComputedStyle(h).fontWeight, overflow:document.documentElement.scrollWidth>innerWidth+1};})()`);
    assert.equal(geometry.font, '16px');
    assert.equal(geometry.weight, '600');
    assert.equal(geometry.overflow, false, 'channel document overflow at ' + width);
    metrics.push({page:'channel', ...geometry});
    await capture('channel-after-' + width);
  }
  // CSS viewport equivalent of 200% zoom on a 1440px display.
  await resize(720,450);
  assert.equal(await evaluate(cdp, 'document.documentElement.scrollWidth>innerWidth+1'), false, 'channel reflow at 200% viewport');
  assert.equal(await evaluate(cdp, `(() => {const side=document.querySelector('.admin-sidebar').getBoundingClientRect();const main=document.querySelector('.admin-main-wrap').getBoundingClientRect();return main.top>=side.bottom-1})()`), true, 'small-screen navigation overlaps the main content');
  await capture('channel-reflow-200pct');

  // Isolated mobile presentation fixtures use the actual packaged CSS. They
  // supplement (not replace) the real survey/Sidebar business journeys.
  const manifest = JSON.parse(await fs.readFile('web/dist/asset-manifest.json', 'utf8'));
  const styles = ['tokens','surfaceFeedbackStyles','actionFeedbackStyles','presentationStyles'].map(key => `<link rel="stylesheet" href="${baseURL}/${manifest.entries[key]}">`).join('');
  const sidebarStyles = `<link rel="stylesheet" href="${baseURL}/${manifest.entries.sidebarStandardStyles}">`;
  const fixtures = {
    h5: `<div class="h5-backdrop"><div class="phone"><div id="screen" class="phone-screen"><div style="padding:20px;overflow:auto"><h1 style="font-size:22px">本周体验反馈</h1><p style="font-size:13px">演示样例 · 用于移动端展示验收</p><label data-option-id="a" style="display:flex;padding:12px;border:1px solid #dee0e3;border-radius:8px"><span style="font-size:15px">我已完成本周计划</span></label><textarea placeholder="填写你的反馈" style="font-size:15px;width:100%;margin-top:16px;min-height:96px"></textarea><button style="margin-top:16px;width:100%">提交反馈</button></div></div></div></div>`,
    sidebar: `<div class="wrap" id="sidebar-workbench-root"><section class="profile-card"><div class="name">体验客户</div><p class="meta">演示样例 · 用于侧边栏展示验收</p></section><div class="tabs"><button class="tab active">客户资料</button><button class="tab">商品</button><button class="tab">素材</button></div><section class="panel"><div class="head"><h2>图片素材</h2></div><p class="meta">在这里查看已选素材</p><button class="btn">选择素材</button></section></div>`,
  };
  for (const [surface, body] of Object.entries(fixtures)) {
    const { frameTree } = await cdp.call('Page.getFrameTree');
    await cdp.call('Page.setDocumentContent', { frameId: frameTree.frame.id, html: `<!doctype html><html><head><meta charset="utf-8">${surface==='sidebar'?sidebarStyles:''}${styles}</head><body data-ui-surface="${surface}">${body}</body></html>` });
    const expectedButtonFont = surface === 'sidebar' ? '12px' : '16px';
    const expectedControlHeight = surface === 'sidebar' ? 32 : 44;
    const controlSelector = surface === 'sidebar' ? '.tab' : 'button';
    const expectedStylesheetCount = surface === 'sidebar' ? 5 : 4;
    await waitFor(cdp, `document.styleSheets.length === ${expectedStylesheetCount}`, surface + ' styles did not load');
    for (const width of [320,375,390,414]) {
      await resize(width,844);
      const mobile = await evaluate(cdp, `(() => {const b=document.querySelector('${controlSelector}');return {width:innerWidth, font:getComputedStyle(b).fontSize, height:b.getBoundingClientRect().height, overflow:document.documentElement.scrollWidth>innerWidth+1};})()`);
      assert.equal(mobile.font, expectedButtonFont); assert.ok(mobile.height>=expectedControlHeight); assert.equal(mobile.overflow,false);
      metrics.push({page:surface+'-fixture',...mobile});
      if (width===390) await capture(surface+'-after-390');
    }
    if (surface === 'sidebar') {
      const sidebarType = await evaluate(cdp, `(() => ({
        name:getComputedStyle(document.querySelector('.name')).fontSize,
        meta:getComputedStyle(document.querySelector('.meta')).fontSize,
        tab:getComputedStyle(document.querySelector('.tab')).fontSize,
        heading:getComputedStyle(document.querySelector('.head h2')).fontSize,
        button:getComputedStyle(document.querySelector('.btn')).fontSize
      }))()`);
      assert.deepEqual(sidebarType, { name:'16px', meta:'11px', tab:'12px', heading:'16px', button:'12px' });
      metrics.push({page:'sidebar-type-scale', ...sidebarType});
    }
  }
  await fs.writeFile(path.join(screenshotDirectory,'presentation-metrics.json'), JSON.stringify(metrics,null,2));
  await cdp.call('Emulation.clearDeviceMetricsOverride');
}
