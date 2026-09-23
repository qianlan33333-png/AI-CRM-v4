import {
  getInvalidSourceHistoryItem,
  readInvalidSourceHistory,
  requireInvalidSourceKind,
  type InvalidSourceItem,
  type InvalidSourceKind,
} from '../../api/invalidSourceHistory';
import { esc } from './util';

type Input = { kind: InvalidSourceKind; historyID?: number };

const kinds: InvalidSourceKind[] = ['tags', 'channels', 'assets', 'links'];
const labels: Record<InvalidSourceKind, string> = {
  tags: '未绑定联系标签',
  channels: '异常渠道定义',
  assets: '异常媒体定义',
  links: '异常雷达链接定义',
};
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;cursor:pointer';
const cell = 'padding:9px 11px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';

function parse(): Input {
  const query = new URLSearchParams(location.search);
  const allowed = new Set(['invalid_source_history', 'history_kind', 'history_id']);
  for (const key of query.keys()) {
    if (!allowed.has(key) || query.getAll(key).length !== 1) throw new Error('异常源历史参数无效');
  }
  if (query.getAll('invalid_source_history').length !== 1 || query.get('invalid_source_history') !== '1') throw new Error('异常源历史入口无效');
  if (query.getAll('history_kind').length !== 1) throw new Error('异常源历史类型无效');
  const kind = requireInvalidSourceKind(query.get('history_kind')!);
  const rawID = query.get('history_id');
  if (rawID === null) return { kind };
  if (!/^[1-9]\d*$/.test(rawID) || !Number.isSafeInteger(Number(rawID))) throw new Error('异常源历史 ID 无效');
  return { kind, historyID: Number(rawID) };
}

function url(kind: InvalidSourceKind, historyID?: number): string {
  const query = new URLSearchParams({ invalid_source_history: '1', history_kind: kind });
  if (historyID !== undefined) query.set('history_id', String(historyID));
  return `config.html?${query.toString()}`;
}

function text(value: unknown): string {
  if (value === null) return 'NULL';
  if (value === '') return '（空字符串）';
  return esc(String(value));
}

function summary(kind: InvalidSourceKind, item: InvalidSourceItem): string {
  if (kind === 'tags') {
    const value = item as Extract<InvalidSourceItem, { tag_source_id: string }>;
    return `历史 #${text(value.id)} · 源标签 #${text(value.tag_source_id)}<br>来源创建时间：${text(value.created_at)}<br>封存原因：${text(value.quarantine_reason)}`;
  }
  if (kind === 'channels') {
    const value = item as Extract<InvalidSourceItem, { channel_type: string }>;
    return `历史 #${text(value.id)} · 源渠道 #${text(value.source_id)}<br>代码/名称：${text(value.code)} / ${text(value.name)}<br>类型/载体：${text(value.channel_type)} / ${text(value.carrier_type)}<br>来源创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
  }
  if (kind === 'assets') {
    const value = item as Extract<InvalidSourceItem, { file_name: string }>;
    return `历史 #${text(value.id)} · 源媒体 #${text(value.source_id)}<br>种类/名称：${text(value.kind)} / ${text(value.name)}<br>文件名/MIME/记录大小：${text(value.file_name)} / ${text(value.mime_type)} / ${text(value.file_size)}<br>原启用：${text(value.original_enabled)} · 来源创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
  }
  const value = item as Extract<InvalidSourceItem, { title: string }>;
  return `历史 #${text(value.id)} · 源链接 #${text(value.source_id)}<br>代码/标题：${text(value.code)} / ${text(value.title)}<br>来源创建/更新：${text(value.created_at)} / ${text(value.updated_at)}`;
}

function fields(kind: InvalidSourceKind, item: InvalidSourceItem): [string, unknown][] {
  if (kind === 'tags') {
    const value = item as Extract<InvalidSourceItem, { tag_source_id: string }>;
    return [['历史 ID', value.id], ['源标签 ID', value.tag_source_id], ['来源创建时间', value.created_at], ['封存原因', value.quarantine_reason]];
  }
  if (kind === 'channels') {
    const value = item as Extract<InvalidSourceItem, { channel_type: string }>;
    return [['历史 ID', value.id], ['源渠道 ID', value.source_id], ['代码', value.code], ['名称', value.name], ['渠道类型', value.channel_type], ['载体类型', value.carrier_type], ['来源创建时间', value.created_at], ['来源更新时间', value.updated_at], ['封存原因', value.quarantine_reason]];
  }
  if (kind === 'assets') {
    const value = item as Extract<InvalidSourceItem, { file_name: string }>;
    return [['历史 ID', value.id], ['源媒体 ID', value.source_id], ['媒体种类', value.kind], ['名称', value.name], ['文件名', value.file_name], ['MIME 类型', value.mime_type], ['来源记录大小', value.file_size], ['原启用', value.original_enabled], ['来源创建时间', value.created_at], ['来源更新时间', value.updated_at], ['封存原因', value.quarantine_reason]];
  }
  const value = item as Extract<InvalidSourceItem, { title: string }>;
  return [['历史 ID', value.id], ['源链接 ID', value.source_id], ['代码', value.code], ['标题', value.title], ['来源创建时间', value.created_at], ['来源更新时间', value.updated_at], ['封存原因', value.quarantine_reason]];
}

function detail(kind: InvalidSourceKind, item: InvalidSourceItem): string {
  return `<dl>${fields(kind, item).map(([name, value]) => `<dt>${esc(name)}</dt><dd style="white-space:pre-wrap;overflow-wrap:anywhere">${text(value)}</dd>`).join('')}</dl>`;
}

export async function mountInvalidSourceHistory(stage: HTMLElement): Promise<void> {
  stage.innerHTML = `<main data-invalid-source-history style="padding:20px;display:grid;gap:14px"><a href="config.html">返回当前配置</a><nav aria-label="异常源历史类别" style="display:flex;gap:8px;flex-wrap:wrap">${kinds.map((kind) => `<a data-invalid-source-history-kind="${kind}" href="${url(kind)}">${esc(labels[kind])}</a>`).join('')}</nav><h1 style="margin:0;font-size:20px">V1 异常源历史（只读）</h1><p style="color:#8F5A16">仅观察已封存的异常源事实。不会下载媒体、访问来源 URL、修复数据、恢复渠道或标签，也不会触发 Provider 或其他外部效果。</p><section data-invalid-source-history-results></section></main>`;
  const results = stage.querySelector<HTMLElement>('[data-invalid-source-history-results]')!;
  let input: Input;
  try {
    input = parse();
  } catch (error) {
    results.innerHTML = `<p role="alert">${esc(error instanceof Error ? error.message : '异常源历史参数无效')}；未读取数据。</p>`;
    return;
  }

  const load = async (offset = 0): Promise<void> => {
    results.innerHTML = '<p role="status">正在读取异常源历史…</p>';
    try {
      if (input.historyID !== undefined) {
        const item = await getInvalidSourceHistoryItem(input.kind, input.historyID);
        results.innerHTML = `<p><a href="${url(input.kind)}">返回${esc(labels[input.kind])}列表</a></p><article data-invalid-source-history-id="${item.id}" style="border:1px solid #DEE0E3;border-radius:8px;padding:14px">${summary(input.kind, item)}${detail(input.kind, item)}</article>`;
        return;
      }
      const page = await readInvalidSourceHistory(input.kind, offset, 20);
      results.innerHTML = `<div style="overflow:auto"><table style="width:100%;border-collapse:collapse"><thead><tr><th style="${cell}">${esc(labels[input.kind])}</th></tr></thead><tbody>${page.items.map((item) => `<tr><td style="${cell}"><a href="${url(input.kind, item.id)}" style="color:#245BDB">查看只读详情</a><br>${summary(input.kind, item)}</td></tr>`).join('') || `<tr><td style="${cell}">暂无历史记录</td></tr>`}</tbody></table></div><div style="margin-top:12px"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span> <button data-invalid-source-history-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button> <button data-invalid-source-history-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div>`;
      results.querySelector<HTMLButtonElement>('[data-invalid-source-history-prev]')?.addEventListener('click', () => { void load(Math.max(0, page.offset - page.limit)); });
      results.querySelector<HTMLButtonElement>('[data-invalid-source-history-next]')?.addEventListener('click', () => { void load(page.offset + page.limit); });
    } catch (error) {
      results.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '异常源历史读取失败')}；未显示历史数据，也未回退 Mock。</p>`;
    }
  };
  await load();
}
