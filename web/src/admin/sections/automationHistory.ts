import {
  readAutomationHistoryAgent, readAutomationHistoryAgents, readAutomationHistoryConfig, readAutomationHistoryConfigs,
  readAutomationHistoryPrompt, readAutomationHistoryPrompts, readAutomationHistorySOP, readAutomationHistorySOPs,
  type AutomationHistoryAgent, type AutomationHistoryConfig, type AutomationHistoryPage, type AutomationHistoryPrompt, type AutomationHistorySOP,
} from '../../api/automationHistory';
import { esc } from './util';

type Kind = 'sop' | 'config' | 'prompt' | 'agent';
type Options = { kind?: string; historyID?: string };
type Item = AutomationHistorySOP | AutomationHistoryConfig | AutomationHistoryPrompt | AutomationHistoryAgent;
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';

function historyID(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error('V1 自动化历史 ID 无效');
  return Number(value);
}

function text(value: string | number | boolean): string { return esc(String(value)); }
function digest(value: number[]): string { return `${value.map((part) => part.toString(16).padStart(2, '0')).join('').slice(0, 16)}…`; }
function row(label: string, value: string): string { return `<tr><th style="${cell};width:180px;color:#667085">${esc(label)}</th><td style="${cell}">${value}</td></tr>`; }

function links(selected: Kind): string {
  const link = (kind: Kind, label: string): string => `<a href="config.html?${new URLSearchParams({ automation_history: '1', history_kind: kind }).toString()}" style="${button};text-decoration:none;${kind === selected ? 'background:#EEF4FF;border-color:#84ADFF' : ''}">${label}</a>`;
  return `<div style="display:flex;gap:8px;flex-wrap:wrap">${link('sop', 'SOP 历史')}${link('config', '配置历史')}${link('prompt', 'Prompt 历史')}${link('agent', 'Agent 历史')}</div>`;
}

function label(kind: Kind): string {
  return ({ sop: 'SOP', config: '自动化配置', prompt: 'Prompt', agent: 'Agent' })[kind];
}

function fields(kind: Kind, item: Item): string {
  const common = row('V2 历史 ID', text(item.id)) + row('V1 source ID', text(item.source_id)) + row('来源 key 摘要', text(digest(item.source_key_digest))) + row('来源 payload 摘要', text(digest(item.source_payload_digest)));
  switch (kind) {
    case 'sop': {
      const value = item as AutomationHistorySOP;
      return common + row('原 pool_key', text(value.pool_key)) + row('原 day_index', text(value.day_index)) + row('原内容（已脱敏）', text(value.content_masked)) + row('原图片摘要', text(digest(value.images_digest))) + row('原 enabled（不代表当前启用）', text(value.original_enabled)) + row('原创建时间', text(value.created_at)) + row('原更新时间', text(value.updated_at));
    }
    case 'config': {
      const value = item as AutomationHistoryConfig;
      return common + row('原 agent_code', text(value.agent_code)) + row('原显示名称', text(value.display_name)) + row('原场景代码', text(value.scenario_code)) + row('原 enabled（不代表当前启用）', text(value.original_enabled)) + row('原 draft / published 版本', text(`${value.draft_version} / ${value.published_version}`)) + row('原发布时间', text(value.published_at)) + row('原最后修改时间 / 来源', text(`${value.last_modified_at} / ${value.last_modified_source}`)) + row('原提交发布标记 / 时间', text(`${value.submitted_for_publish} / ${value.submitted_at}`)) + row('原创建 / 更新时间', text(`${value.created_at} / ${value.updated_at}`)) + row('原 actor 摘要', text(digest(value.actors_digest))) + row('原配置摘要', text(digest(value.config_digest)));
    }
    case 'prompt': {
      const value = item as AutomationHistoryPrompt;
      return common + row('原 agent_code', text(value.agent_code)) + row('原显示名称', text(value.display_name)) + row('原 enabled（不代表当前启用）', text(value.original_enabled)) + row('原版本', text(value.version)) + row('原创建 / 更新时间', text(`${value.created_at} / ${value.updated_at}`)) + row('Prompt 摘要（原文不显示）', text(digest(value.prompt_digest)));
    }
    case 'agent': {
      const value = item as AutomationHistoryAgent;
      return common + row('原 program / workflow / node / task source ID', text(`${value.program_source_id} / ${value.workflow_source_id} / ${value.node_source_id} / ${value.task_source_id}`)) + row('原 agent_code / 名称', text(`${value.agent_code} / ${value.agent_name}`)) + row('原类型 / 状态', text(`${value.original_type} / ${value.original_status}`)) + row('原 sort_order', text(value.sort_order)) + row('原 enabled（不代表当前启用）', text(value.original_enabled)) + row('原创建 / 更新 / 归档时间', text(`${value.created_at} / ${value.updated_at} / ${value.archived_at}`)) + row('原 actor 摘要', text(digest(value.actors_digest))) + row('原配置摘要', text(digest(value.configuration_digest)));
    }
  }
}

function itemLink(kind: Kind, item: Item): string {
  return `<a data-automation-history-id="${item.id}" href="config.html?${new URLSearchParams({ automation_history: '1', history_kind: kind, history_id: String(item.id) }).toString()}" style="color:#245BDB">查看详情 #${item.id}</a>`;
}

async function mountList<T extends Item>(host: HTMLElement, kind: Kind, read: (limit: number, offset: number) => Promise<AutomationHistoryPage<T>>): Promise<void> {
  const load = async (offset: number): Promise<void> => {
    host.innerHTML = `<h2 style="font-size:16px">V1 ${label(kind)} 历史</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page = await read(20, offset);
      host.innerHTML = `<h2 style="font-size:16px">V1 ${label(kind)} 历史</h2><div style="overflow:auto"><table style="width:100%;min-width:880px;border-collapse:collapse;font-size:13px"><thead><tr><th style="${cell}">历史记录</th><th style="${cell}">来源状态 / 原时间</th><th style="${cell}">只读详情</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}">V2 #${item.id}<br>V1 source #${item.source_id}</td><td style="${cell}">${summary(kind, item)}</td><td style="${cell}">${itemLink(kind, item)}</td></tr>`).join('') || `<tr><td colspan="3" style="${cell}">暂无 V1 历史记录</td></tr>`}</tbody></table></div><div style="display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span><div style="display:flex;gap:8px"><button data-history-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      host.querySelector('[data-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      host.querySelector('[data-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      host.innerHTML = `<h2 style="font-size:16px">V1 ${label(kind)} 历史</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重试本页</button>`;
      host.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load(0);
}

function summary(kind: Kind, item: Item): string {
  switch (kind) {
    case 'sop': { const value = item as AutomationHistorySOP; return `pool=${text(value.pool_key)} · day=${text(value.day_index)} · enabled=${text(value.original_enabled)}<br>${text(value.created_at)} / ${text(value.updated_at)}`; }
    case 'config': { const value = item as AutomationHistoryConfig; return `${text(value.agent_code)} · ${text(value.scenario_code)} · enabled=${text(value.original_enabled)}<br>${text(value.created_at)} / ${text(value.updated_at)}`; }
    case 'prompt': { const value = item as AutomationHistoryPrompt; return `${text(value.agent_code)} · version=${text(value.version)} · enabled=${text(value.original_enabled)}<br>${text(value.created_at)} / ${text(value.updated_at)}`; }
    case 'agent': { const value = item as AutomationHistoryAgent; return `${text(value.original_type)} / ${text(value.original_status)} · enabled=${text(value.original_enabled)}<br>${text(value.created_at)} / ${text(value.updated_at)}`; }
  }
}

export async function mountAutomationHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  const kind: Kind = options.kind === undefined || options.kind === 'sop' ? 'sop' : options.kind === 'config' || options.kind === 'prompt' || options.kind === 'agent' ? options.kind : (() => { throw new Error('V1 自动化历史类型无效'); })();
  const id = historyID(options.historyID);
  stage.innerHTML = `<div data-automation-history style="flex:1;min-height:0;overflow:auto;padding:20px;display:grid;gap:16px;align-content:start"><header><a href="config.html">返回当前自动化配置</a><h1 style="font-size:20px;margin:12px 0 8px">V1 自动化历史（只读）</h1><p style="color:#8F5A16;margin:0;line-height:1.7">仅展示 V1 封存事实。原 enabled、状态、时间、0 与负数均保留来源含义，不代表当前启用、保存、发布、激活或执行；Prompt、actor 与 JSON 原文仅以摘要保留，页面不会调用 Provider。</p><div style="margin-top:12px">${links(kind)}</div></header><section data-automation-history-content></section></div>`;
  const content = stage.querySelector<HTMLElement>('[data-automation-history-content]')!;
  if (id !== undefined) {
    const load = async (): Promise<void> => {
      content.innerHTML = '<p role="status">正在读取 V1 历史详情…</p>';
      try {
        const value = kind === 'sop' ? await readAutomationHistorySOP(id) : kind === 'config' ? await readAutomationHistoryConfig(id) : kind === 'prompt' ? await readAutomationHistoryPrompt(id) : await readAutomationHistoryAgent(id);
        const back = `config.html?${new URLSearchParams({ automation_history: '1', history_kind: kind }).toString()}`;
        content.innerHTML = `<a href="${back}">返回 V1 ${label(kind)} 历史列表</a><h2 style="font-size:16px">V1 ${label(kind)} 历史详情 #${value.id}</h2><table style="width:100%;max-width:960px;border-collapse:collapse;font-size:13px"><tbody>${fields(kind, value)}</tbody></table>`;
      } catch (error) {
        content.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重试详情</button>`;
        content.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(); });
      }
    };
    await load();
    return;
  }
  switch (kind) {
    case 'sop': await mountList(content, kind, readAutomationHistorySOPs); return;
    case 'config': await mountList(content, kind, readAutomationHistoryConfigs); return;
    case 'prompt': await mountList(content, kind, readAutomationHistoryPrompts); return;
    case 'agent': await mountList(content, kind, readAutomationHistoryAgents);
  }
}
