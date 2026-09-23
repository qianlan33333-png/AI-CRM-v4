import {
  readOwnerMigrationResultHistory, readOwnerMigrationResultHistoryDetail, readSidebarProfileHistory, readSidebarProfileHistoryDetail,
  type ContactHistoryPage, type OwnerMigrationResultHistoryItem, type SidebarProfileHistoryItem,
} from '../../api/contactHistory';
import { esc } from './util';

type Kind = 'sidebar' | 'owner';
type Options = { kind?: string; historyID?: string; customerID?: string };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap';
const text = (value: string | number | null): string => value === null ? '未关联客户' : value === '' ? '（空）' : esc(String(value));
const row = (...cells: string[]): string => `<tr>${cells.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`;

function id(value: string | undefined, label: string): number | undefined {
  if (value === undefined) return undefined;
  const parsed = Number(value);
  if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(parsed)) throw new Error(`V1 ${label} ID 无效`);
  return parsed;
}

function links(kind: Kind, customerID?: number): string {
  const query = (next: Kind): string => {
    const params = new URLSearchParams({ contact_history: '1', history_kind: next });
    if (next === 'sidebar' && customerID !== undefined) params.set('customer_id', String(customerID));
    return `ownerMig.html?${params.toString()}`;
  };
  return `<div style="display:flex;gap:8px;flex-wrap:wrap"><a href="${query('owner')}" style="${button};text-decoration:none;${kind === 'owner' ? 'background:#EEF4FF;border-color:#84ADFF' : ''}">负责人迁移旧结果</a><a href="${query('sidebar')}" style="${button};text-decoration:none;${kind === 'sidebar' ? 'background:#EEF4FF;border-color:#84ADFF' : ''}">Sidebar 旧资料</a></div>`;
}

function sidebarFilter(customerID?: number): string {
  return `<form id="contact-history-customer-filter" style="margin-top:12px;display:flex;align-items:end;gap:8px;flex-wrap:wrap"><label style="display:grid;gap:4px;font-size:12px;color:#667085">按 V2 客户 ID 筛选<input name="customer_id" type="number" min="1" step="1" value="${customerID === undefined ? '' : customerID}" style="width:150px;height:30px;box-sizing:border-box;border:1px solid #D0D5DD;border-radius:6px;padding:0 8px"></label><button type="submit" style="${button}">筛选</button><button type="button" data-contact-history-clear style="${button}">清除</button><span id="contact-history-customer-error" role="alert" style="color:#D83931;font-size:12px"></span></form>`;
}

function sidebarRow(item: SidebarProfileHistoryItem): string {
  return row(`历史 Sidebar #${item.id}<br>${item.customer_id === null ? '未关联客户' : `V2 客户 #${item.customer_id}`}`,
    `来源：${text(item.source)}<br>行业：${text(item.industry)}<br>行业说明：${text(item.industry_description)}<br>需求/阻碍/跟进：${text(item.needs_blockers_followup)}`,
    `原更新时间：${text(item.updated_at)}`,
    `<a data-contact-history-id="${item.id}" href="ownerMig.html?contact_history=1&history_kind=sidebar&history_id=${item.id}${item.customer_id === null ? '' : `&customer_id=${item.customer_id}`}">查看原始快照</a>`);
}

function ownerRow(item: OwnerMigrationResultHistoryItem): string {
  return row(`历史结果 #${item.id}<br>范围：${text(item.scope_type)}<br>创建：${text(item.created_at)}<br>执行时间：${text(item.executed_at)}`,
    `总行 ${item.total_rows} · 可迁 ${item.eligible_count}<br>V1 企微成功记录 ${item.wecom_success} · 失败记录 ${item.wecom_failed}<br>V1 CRM 更新记录 ${item.crm_updated}`,
    `原文件 hash：${text(item.file_hash)}<br>原预览 hash：${text(item.preview_hash)}<br>会话关系：${text(item.session_relation)}<br>预览关系：${text(item.preview_relation)}`,
    `<a data-contact-history-id="${item.id}" href="ownerMig.html?contact_history=1&history_kind=owner&history_id=${item.id}">查看原始结果</a>`);
}

function detailRows(item: SidebarProfileHistoryItem | OwnerMigrationResultHistoryItem): string {
  if ('customer_id' in item) {
    return row('V2 客户', text(item.customer_id), '来源', text(item.source)) +
      row('行业', text(item.industry), '行业说明', text(item.industry_description)) +
      row('需求/阻碍/跟进', text(item.needs_blockers_followup), '原更新时间', text(item.updated_at));
  }
  return row('范围', text(item.scope_type), '原文件 hash', text(item.file_hash)) +
    row('原预览 hash', text(item.preview_hash), '原欢迎语', text(item.transfer_welcome_message)) +
    row('总行 / 可迁', `${item.total_rows} / ${item.eligible_count}`, 'V1 企微成功 / 失败', `${item.wecom_success} / ${item.wecom_failed}`) +
    row('V1 CRM 更新', text(item.crm_updated), '原企微转接选项', item.include_wecom_transfer ? 'true' : 'false') +
    row('会话 / 预览关系', `${text(item.session_relation)} / ${text(item.preview_relation)}`, '创建 / 执行时间', `${text(item.created_at)} / ${text(item.executed_at)}`);
}

async function mountList<T extends { id: number }>(section: HTMLElement, title: string, columns: string[], read: (offset: number) => Promise<ContactHistoryPage<T>>, render: (item: T) => string): Promise<void> {
  const load = async (offset: number): Promise<void> => {
    section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page = await read(offset);
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><div style="overflow:auto"><table style="width:100%;min-width:900px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085">${columns.map((column) => `<th style="${cell}">${column}</th>`).join('')}</tr></thead><tbody>${page.items.map(render).join('') || `<tr><td colspan="${columns.length}" style="${cell}">暂无 V1 历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px;display:flex;align-items:center;justify-content:space-between;gap:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span><div style="display:flex;gap:8px"><button data-history-previous style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      section.querySelector('[data-history-previous]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      section.querySelector('[data-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      section.innerHTML = `<h2 style="font-size:16px">${title}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重新读取</button>`;
      section.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(offset); });
    }
  };
  await load(0);
}

export async function mountContactHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  const kind: Kind = options.kind === undefined || options.kind === 'owner' ? 'owner' : options.kind === 'sidebar' ? 'sidebar' : (() => { throw new Error('V1 联系人历史类型无效'); })();
  const historyID = id(options.historyID, kind === 'owner' ? '负责人迁移结果历史' : 'Sidebar 历史');
  const customerID = id(options.customerID, '客户');
  if (kind === 'owner' && customerID !== undefined) throw new Error('V1 负责人迁移结果历史不能按客户筛选');
  stage.innerHTML = `<div style="flex:1;min-height:0;overflow:auto;padding:20px;display:grid;grid-template-columns:minmax(0,1fr);gap:16px;align-content:start"><header><a href="ownerMig.html">返回负责人迁移</a><h1 style="font-size:20px;margin:12px 0 8px">V1 联系人历史（只读）</h1><p style="color:#8F5A16;margin:0;line-height:1.7">只展示 V1 原始封存资料，不修改当前 Sidebar、客户或负责人。负责人结果中的企微成功、失败和 CRM 更新均为 V1 旧结果记录，不是 V2 迁移执行，也不是 Provider 成功证据；原时间、空文本与 NULL 均按服务端原样显示。</p><div style="margin-top:12px">${links(kind, customerID)}</div>${kind === 'sidebar' && historyID === undefined ? sidebarFilter(customerID) : ''}</header><section id="contact-history-content"></section></div>`;
  const section = stage.querySelector<HTMLElement>('#contact-history-content')!;
  const filter = stage.querySelector<HTMLFormElement>('#contact-history-customer-filter');
  if (filter) {
    const showFilterError = (error: unknown): void => { stage.querySelector<HTMLElement>('#contact-history-customer-error')!.textContent = error instanceof Error ? error.message : '筛选无效'; };
    filter.addEventListener('submit', (event) => {
      event.preventDefault();
      const value = new FormData(filter).get('customer_id');
      void mountContactHistory(stage, { kind: 'sidebar', customerID: typeof value === 'string' && value !== '' ? value : undefined }).catch(showFilterError);
    });
    filter.querySelector('[data-contact-history-clear]')?.addEventListener('click', () => { void mountContactHistory(stage, { kind: 'sidebar' }); });
  }
  if (historyID !== undefined) {
    const back = `ownerMig.html?${new URLSearchParams({ contact_history: '1', history_kind: kind, ...(kind === 'sidebar' && customerID !== undefined ? { customer_id: String(customerID) } : {}) }).toString()}`;
    const load = async (): Promise<void> => {
      section.innerHTML = '<p role="status">正在读取 V1 历史…</p>';
      try {
        const value = kind === 'sidebar' ? await readSidebarProfileHistoryDetail(historyID, customerID) : await readOwnerMigrationResultHistoryDetail(historyID);
        section.innerHTML = `<a href="${back}">返回 V1 历史列表</a><h2 style="font-size:16px">${kind === 'sidebar' ? 'Sidebar 原始快照' : '负责人迁移原始结果'}</h2><div style="overflow:auto"><table style="width:100%;max-width:900px;border-collapse:collapse;font-size:13px"><tbody>${detailRows(value)}</tbody></table></div>`;
      } catch (error) {
        section.innerHTML = `<a href="${back}">返回 V1 历史列表</a><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据。</p><button data-history-retry style="${button}">重新读取</button>`;
        section.querySelector('[data-history-retry]')?.addEventListener('click', () => { void load(); });
      }
    };
    await load();
    return;
  }
  if (kind === 'sidebar') {
    await mountList(section, 'Sidebar 原始快照', ['历史引用 / 客户', '原业务文本', '原时间', '只读入口'], (offset) => readSidebarProfileHistory(offset, 20, customerID), sidebarRow);
    return;
  }
  await mountList(section, '负责人迁移旧结果', ['历史引用 / 原时间', 'V1 原计数（不代表执行）', '原关联快照', '只读入口'], readOwnerMigrationResultHistory, ownerRow);
}
