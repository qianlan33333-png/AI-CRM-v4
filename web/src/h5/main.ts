/**
 * 用户端 H5 页面入口。
 * 每页 HTML 由 scripts/build.mjs 生成（手机壳 + <template id="tpl">）。
 */
import { mount } from '../shared/ui/runtime';
import { initFeedback } from '../shared/ui/feedback';
import { H5Controller } from './controller';

function boot(): void {
  const page = document.body.getAttribute('data-page') || 'auth';
  const tpl = document.getElementById('tpl') as HTMLTemplateElement | null;
  const screen = document.getElementById('screen');
  if (!tpl || !screen) return;

  const controller = new H5Controller(page);
  mount(screen, tpl.innerHTML, controller);
  initFeedback();
  void controller.init();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
