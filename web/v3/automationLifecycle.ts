import { request } from '../src/api/transport';
import { confirmBox, toast } from '../src/shared/ui/feedback';

type Row = { id: number; status: string; pause?: () => void; lifecycleLabel?: string };
type Values = { rows?: { agents?: Row[] } };
type Controller = { page: string; renderVals(): Values; init(): Promise<void> };
const pending = new Set<number>();
const keys = new Map<string, string>();
const reasons: Record<string, string> = {
  prompt_unconfigured: '请先编辑并完善角色和任务提示词',
  material_unconfigured: '请先编辑并配置话术内容或素材',
  unpublished_changes: '草稿尚未发布', already_active: '当前已启用',
};
const endpoint = (id: number) => `/api/admin/automation-agents/${id}`;
async function read(id: number) {
  const data = await (await request(endpoint(id))).json();
  const a = data.agent;
  if (a?.id !== id || !['paused', 'active', 'archived'].includes(a.status) || typeof a.execution_enabled !== 'boolean') throw new Error('自动化状态读取无效，请刷新后重试');
  return a;
}
async function mutate(id: number, action: string) {
  const scope = `${id}:${action}`;
  const key = keys.get(scope) || crypto.randomUUID();
  keys.set(scope, key);
  await request(`${endpoint(id)}/${action}`, { method: 'POST', headers: { 'Idempotency-Key': key } });
  keys.delete(scope);
}
async function change(controller: Controller, id: number, target: 'active' | 'paused') {
  if (pending.has(id)) return;
  pending.add(id);
  try {
    const current = await read(id);
    if (current.status === 'archived') throw new Error('该话术已归档，请刷新列表');
    if (current.status === target && current.execution_enabled === (target === 'active')) {
      await controller.init();
      return;
    }
    if (target === 'active') {
      let check = await (await request(`${endpoint(id)}/precheck`)).json();
      if (!Array.isArray(check.reasons)) throw new Error('启用检查结果无效');
      if (!check.can_activate) {
        if (check.reasons.length === 1 && check.reasons[0] === 'unpublished_changes') {
          // Release before the dialog so Cancel does not retain a lock. The
          // confirmed continuation rechecks state and serializes the writes.
          pending.delete(id);
          confirmBox('发布并启用', '该话术有未发布修改。确认发布当前草稿并启用自动化？', '发布并启用', false, () => {
            void publishAndActivate(controller, id);
          });
          return;
        }
        throw new Error(check.reasons.map((r: string) => reasons[r] || '配置暂不满足启用条件').join('；') || '配置暂不满足启用条件');
      }
    }
    await mutate(id, target === 'active' ? 'activate' : 'pause');
    const actual = await read(id);
    if (actual.status !== target || actual.execution_enabled !== (target === 'active')) throw new Error('状态未切换，请刷新后检查配置');
    await controller.init();
    toast(target === 'active' ? '自动化话术已启用' : '自动化话术已暂停');
  } catch (error) {
    toast(error instanceof Error ? error.message : '操作结果未确认，请刷新后重试', true);
  } finally { pending.delete(id); }
}
async function publishAndActivate(controller: Controller, id: number) {
  if (pending.has(id)) return;
  pending.add(id);
  try {
    const current = await read(id);
    if (current.status !== 'paused') throw new Error('状态已变化，请刷新后重试');
    await mutate(id, 'publish');
  } catch (error) {
    toast(error instanceof Error ? error.message : '发布失败，请重试', true);
    return;
  } finally { pending.delete(id); }
  await change(controller, id, 'active');
}
export function installAutomationLifecycle(controller: Controller, template: HTMLTemplateElement) {
  const findAction = (root: DocumentFragment): Element | null => {
    const direct = root.querySelector('[data-agent-action="pause"]');
    if (direct) return direct;
    for (const nested of root.querySelectorAll<HTMLTemplateElement>('template')) {
      const found = findAction(nested.content);
      if (found) return found;
    }
    return null;
  };
  const action = findAction(template.content);
  if (!action) throw new Error('自动化启停模板入口缺失');
  action.textContent = '{{ r.lifecycleLabel }}';
  const render = controller.renderVals;
  controller.renderVals = function () {
    const values = render.call(this);
    if (this.page !== 'agents') return values;
    for (const row of values.rows?.agents || []) {
      const target = row.status === '已暂停' ? 'active' : row.status === '启用中' ? 'paused' : undefined;
      row.lifecycleLabel = target === 'active' ? '启用' : target === 'paused' ? '暂停' : '状态待确认';
      row.pause = () => {
        if (!target || !Number.isSafeInteger(row.id) || row.id < 1) { toast('请刷新列表确认话术状态', true); return; }
        confirmBox(target === 'active' ? '启用自动化话术' : '暂停自动化话术', target === 'active' ? '启用后，关联自动化可按现有规则执行。确认启用？' : '确认暂停该自动化话术？', target === 'active' ? '启用' : '暂停', false, () => { void change(this, row.id, target); });
      };
    }
    return values;
  };
}
