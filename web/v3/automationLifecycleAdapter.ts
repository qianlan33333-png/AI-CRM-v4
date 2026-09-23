import { installAutomationLifecycle } from './automationLifecycle';
void (async () => {
  // @ts-ignore Frozen donor module materialized during build.
  const { AdminController } = await import('../src/admin/controller');
  const template = document.querySelector<HTMLTemplateElement>('#tpl');
  if (!template) throw new Error('自动化模板不可用');
  installAutomationLifecycle(AdminController.prototype, template);
  // @ts-ignore Frozen donor starts only after the V3 lifecycle binding.
  await import('../src/admin/main');
})();
