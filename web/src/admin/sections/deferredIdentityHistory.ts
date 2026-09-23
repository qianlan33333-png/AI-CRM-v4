import {
  getDeferredIdentityConflictItem,
  getDeferredPersonItem,
  getMissingRootIdentityItem,
  readDeferredIdentityConflicts,
  readDeferredPeople,
  readMissingRootIdentities,
  type DeferredIdentityHistoryConflict,
  type DeferredIdentityHistoryMissingRoot,
  type DeferredIdentityHistoryPage,
  type DeferredIdentityHistoryPerson,
} from '../../api/deferredIdentityHistory';
import { esc } from './util';

type Kind = 'people' | 'conflicts' | 'missing-roots';
type Item = DeferredIdentityHistoryPerson | DeferredIdentityHistoryConflict | DeferredIdentityHistoryMissingRoot;
type Options = { kind?: string; historyID?: string };
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';
const shown = (value: string | number | null): string => value === null ? 'NULL' : value === '' ? '（空字符串）' : esc(String(value));

function input(options: Options): { kind: Kind; id?: number } {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['deferred_identity_history', 'history_kind', 'history_id']);
  for (const key of query.keys()) if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('未归属身份历史参数无效');
  if (query.get('deferred_identity_history') !== '1') throw new Error('未归属身份历史入口无效');
  const rawKind = options.kind ?? query.get('history_kind') ?? 'people';
  const kind: Kind = rawKind === 'people' || rawKind === 'conflicts' || rawKind === 'missing-roots' ? rawKind : (() => { throw new Error('未归属身份历史类型无效'); })();
  const rawID = options.historyID ?? query.get('history_id') ?? undefined;
  if (rawID === undefined) return { kind };
  if (!/^[1-9]\d*$/.test(rawID) || !Number.isSafeInteger(Number(rawID))) throw new Error('未归属身份历史 ID 无效');
  return { kind, id: Number(rawID) };
}

function url(kind: Kind, id?: number): string {
  const query = new URLSearchParams({ deferred_identity_history: '1', history_kind: kind });
  if (id !== undefined) query.set('history_id', String(id));
  return `config.html?${query.toString()}`;
}

function nav(kind: Kind): string {
  const link = (value: Kind, label: string): string => `<a style="${button};text-decoration:none;${kind === value ? 'background:#EEF4FF;border-color:#84ADFF' : ''}" href="${url(value)}">${label}</a>`;
  return `<div style="display:flex;gap:8px;flex-wrap:wrap">${link('people', 'V1 人员证据')}${link('conflicts', 'V1 身份冲突')}${link('missing-roots', '缺失客户根证据')}</div>`;
}

function rows(values: string[]): string { return `<tr>${values.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`; }

function summary(kind: Kind, item: Item): string[] {
  if (kind === 'people') {
    const value = item as DeferredIdentityHistoryPerson;
    return [`历史证据 #${value.id}<br>源 ID：${value.source_id}`, `创建：${shown(value.created_at)}<br>更新：${shown(value.updated_at)}`, `<a data-deferred-identity-history-id="${value.id}" href="${url(kind, value.id)}">查看只读详情</a>`];
  }
  if (kind === 'conflicts') {
    const value = item as DeferredIdentityHistoryConflict;
    return [`历史冲突 #${value.id}<br>源 ID：${value.source_id}`, `类型：${shown(value.conflict_type)}<br>来源：${shown(value.source_type)}<br>状态：${shown(value.status)} / ${shown(value.resolution_status)}`, `创建：${shown(value.created_at)}<br>更新：${shown(value.updated_at)}<br>解决：${shown(value.resolved_at)}<br><a data-deferred-identity-history-id="${value.id}" href="${url(kind, value.id)}">查看只读详情</a>`];
  }
  const value = item as DeferredIdentityHistoryMissingRoot;
  return [`缺失根证据 #${value.id}<br>源 ID：${value.source_id}`, `原因：${shown(value.quarantine_reason)}<br>类型原值：${shown(value.type)}<br>状态：${shown(value.status)}`, `首次：${shown(value.first_seen_at)}<br>最后：${shown(value.last_seen_at)}<br><a data-deferred-identity-history-id="${value.id}" href="${url(kind, value.id)}">查看只读详情</a>`];
}

function detail(kind: Kind, item: Item): string {
  if (kind === 'people') {
    const value = item as DeferredIdentityHistoryPerson;
    return rows(['历史 ID / 源 ID', `${value.id} / ${value.source_id}`, '创建 / 更新', `${shown(value.created_at)} / ${shown(value.updated_at)}`]);
  }
  if (kind === 'conflicts') {
    const value = item as DeferredIdentityHistoryConflict;
    return rows(['历史 ID / 源 ID', `${value.id} / ${value.source_id}`, '冲突类型 / 来源', `${shown(value.conflict_type)} / ${shown(value.source_type)}`]) +
      rows(['状态 / 解决状态', `${shown(value.status)} / ${shown(value.resolution_status)}`, '解决时间', shown(value.resolved_at)]) +
      rows(['创建 / 更新', `${shown(value.created_at)} / ${shown(value.updated_at)}`, '', '']);
  }
  const value = item as DeferredIdentityHistoryMissingRoot;
  return rows(['历史 ID / 源 ID', `${value.id} / ${value.source_id}`, '固定原因', shown(value.quarantine_reason)]) +
    rows(['类型原值 / 状态', `${shown(value.type)} / ${shown(value.status)}`, '首次 / 最后观察', `${shown(value.first_seen_at)} / ${shown(value.last_seen_at)}`]) +
    rows(['创建 / 更新', `${shown(value.created_at)} / ${shown(value.updated_at)}`, '', '']);
}

function title(kind: Kind): string {
  return kind === 'people' ? 'V1 未归属人员证据' : kind === 'conflicts' ? 'V1 未归属身份冲突' : 'V1 缺失客户根证据';
}

async function read(kind: Kind, offset: number): Promise<DeferredIdentityHistoryPage<Item>> {
  if (kind === 'people') return readDeferredPeople(offset, 50);
  if (kind === 'conflicts') return readDeferredIdentityConflicts(offset, 50);
  return readMissingRootIdentities(offset, 50);
}

async function get(kind: Kind, id: number): Promise<Item> {
  if (kind === 'people') return getDeferredPersonItem(id);
  if (kind === 'conflicts') return getDeferredIdentityConflictItem(id);
  return getMissingRootIdentityItem(id);
}

export async function mountDeferredIdentityHistory(stage: HTMLElement, options: Options = {}): Promise<void> {
  let current: { kind: Kind; id?: number };
  try { current = input(options); } catch (error) {
    stage.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '未归属身份历史入口无效')}；未读取数据。</p>`;
    return;
  }
  stage.innerHTML = `<main data-deferred-identity-history style="padding:20px;display:grid;gap:16px"><a href="config.html">返回配置中心</a><header><h1 style="font-size:20px;margin:0 0 8px">V1 未归属身份历史证据（只读）</h1><p style="margin:0;color:#8F5A16;line-height:1.7">仅展示已封存的未归属 V1 历史证据；不创建客户、不绑定身份、不可执行，也不提高身份可信度。页面只显示公开元数据，绝不显示身份摘要、redaction roots 或原始 PII。</p><div style="margin-top:12px">${nav(current.kind)}</div></header><section data-deferred-identity-history-content></section></main>`;
  const content = stage.querySelector<HTMLElement>('[data-deferred-identity-history-content]')!;
  const failure = (retry: () => void): void => {
    content.innerHTML = `<p role="alert" style="color:#D83931">未归属身份历史读取失败；未显示旧数据，也未回退 Mock。</p><button data-deferred-identity-history-retry style="${button}">重新读取</button>`;
    content.querySelector('[data-deferred-identity-history-retry]')?.addEventListener('click', retry);
  };
  if (current.id !== undefined) {
    const load = async (): Promise<void> => {
      content.innerHTML = '<p role="status">正在读取 V1 历史证据…</p>';
      try {
        const item = await get(current.kind, current.id!);
        content.innerHTML = `<a href="${url(current.kind)}">返回历史列表</a><h2 style="font-size:16px">${title(current.kind)}详情</h2><div style="overflow:auto"><table style="width:100%;max-width:900px;border-collapse:collapse;font-size:13px"><tbody>${detail(current.kind, item)}</tbody></table></div>`;
      } catch {
        failure(() => { void load(); });
      }
    };
    await load();
    return;
  }
  const load = async (offset: number): Promise<void> => {
    content.innerHTML = '<p role="status">正在读取 V1 历史证据…</p>';
    try {
      const page = await read(current.kind, offset);
      const range = page.items.length ? `${page.offset + 1}–${page.offset + page.items.length}` : '0';
      content.innerHTML = `<h2 style="font-size:16px">${title(current.kind)}</h2><div style="overflow:auto"><table style="width:100%;min-width:760px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085"><th style="${cell}">历史引用</th><th style="${cell}">公开历史字段</th><th style="${cell}">只读时间 / 详情</th></tr></thead><tbody>${page.items.map((item) => rows(summary(current.kind, item))).join('') || `<tr><td colspan="3" style="${cell}">暂无 V1 历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px;display:flex;justify-content:space-between;gap:12px"><span>共 ${page.total} 条 · 当前 ${range}</span><div><button data-deferred-identity-history-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button> <button data-deferred-identity-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      content.querySelector('[data-deferred-identity-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      content.querySelector('[data-deferred-identity-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch {
      failure(() => { void load(offset); });
    }
  };
  await load(0);
}
