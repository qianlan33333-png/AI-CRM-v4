import {
  legacyMarketingHistoryApi,
  type LegacyMarketingHistoryPage, type LegacyMarketingHistoryReadAdapter, type LegacyMarketingHistoryState, type LegacyMarketingHistoryValue,
} from '../../api/legacyMarketingHistory';
import { esc } from './util';

type Kind = 'state' | 'value';
type HistoryItem = LegacyMarketingHistoryState | LegacyMarketingHistoryValue;
const button = 'padding:7px 12px;border:1px solid #D0D5DD;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
const cell = 'padding:10px 12px;border-bottom:1px solid #EEF0F3;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';
const text = (value: string | number | null): string => value === null ? 'NULL（源未记录）' : value === '' ? '（空字符串）' : esc(String(value));
const row = (values: string[]): string => `<tr>${values.map((value) => `<td style="${cell}">${value}</td>`).join('')}</tr>`;

function label(kind: Kind): string { return kind === 'state' ? '营销状态快照' : '价值分层快照'; }
function rows(kind: Kind, items: HistoryItem[]): string {
  if (!items.length) return '<tr><td colspan="4" style="padding:22px;text-align:center;color:#8F959E">暂无 V1 历史记录</td></tr>';
  return items.map((item) => {
    if (kind === 'state') {
      const value = item as LegacyMarketingHistoryState;
      return row([`历史 #${value.id}<br>V1 source #${text(value.source_id)}`, `${text(value.scenario_key)}<br>阶段：${text(value.marketing_phase)} · ${text(value.phase_label)}`, `生命周期：${text(value.lifecycle_status)}<br>原批次状态：${text(value.last_batch_status)}`, `<button data-legacy-marketing-detail="${value.id}" style="${button}">查看只读详情</button>`]);
    }
    const value = item as LegacyMarketingHistoryValue;
    return row([`历史 #${value.id}<br>V1 source #${text(value.source_id)}`, text(value.scenario_key), `${text(value.value_segment)}<br>${text(value.segment_label)}`, `score=${text(value.score)}<br><button data-legacy-marketing-detail="${value.id}" style="${button};margin-top:6px">查看只读详情</button>`]);
  }).join('');
}

function detailRows(kind: Kind, item: HistoryItem): string {
  const fields: [string, string | number | null][] = kind === 'state'
    ? (() => { const value = item as LegacyMarketingHistoryState; return [
      ['V2 历史 ID', value.id], ['V1 source ID', value.source_id], ['场景', value.scenario_key], ['原营销阶段 / 标签', `${value.marketing_phase} / ${value.phase_label}`], ['原阶段原因', value.phase_reason], ['原生命周期状态', value.lifecycle_status], ['原批次状态', value.last_batch_status], ['原批次窗口开始 / 结束', `${value.last_batch_window_start} / ${value.last_batch_window_end}`], ['原最近触发消息时间', value.last_trigger_message_at], ['原进入 / 退出时间', `${text(value.entered_at)} / ${text(value.exited_at)}`], ['原退出原因', value.exit_reason], ['原创建 / 更新时间', `${value.created_at} / ${value.updated_at}`],
    ]; })()
    : (() => { const value = item as LegacyMarketingHistoryValue; return [
      ['V2 历史 ID', value.id], ['V1 source ID', value.source_id], ['场景', value.scenario_key], ['原价值分层 / 标签', `${value.value_segment} / ${value.segment_label}`], ['原 score', value.score], ['原创建 / 更新时间', `${value.created_at} / ${value.updated_at}`],
    ]; })();
  return fields.map(([name, value]) => `<tr><th style="${cell};width:200px;color:#667085">${esc(name)}</th><td style="${cell}">${typeof value === 'string' && value.includes(' / ') ? value.split(' / ').map((part) => text(part)).join(' / ') : text(value)}</td></tr>`).join('');
}

export async function renderLegacyMarketingHistory(stage: HTMLElement, api: LegacyMarketingHistoryReadAdapter = legacyMarketingHistoryApi): Promise<void> {
  stage.innerHTML = `<div data-legacy-marketing-history style="flex:1;min-height:0;overflow:auto;padding:20px;display:grid;gap:16px;align-content:start"><header><a href="automation.html">返回当前运营管理</a><h1 style="font-size:20px;margin:12px 0 8px">V1 营销历史（只读）</h1><p style="margin:0;color:#8F5A16;line-height:1.7">只观察 V1 封存的营销状态与价值分层快照。历史记录不会改变当前分层，不会恢复旧批次，也不会触发执行、发送或 Provider 外部效果。</p></header><nav data-legacy-marketing-controls style="display:flex;gap:8px;flex-wrap:wrap"></nav><section data-legacy-marketing-content style="display:grid;gap:12px"></section></div>`;
  const controls = stage.querySelector<HTMLElement>('[data-legacy-marketing-controls]')!;
  const content = stage.querySelector<HTMLElement>('[data-legacy-marketing-content]')!;
  let selected: Kind | undefined;

  const renderControls = (): void => {
    controls.innerHTML = (['state', 'value'] as Kind[]).map((kind) => `<button data-legacy-marketing-load="${kind}" style="${button}${selected === kind ? ';background:#EEF4FF;border-color:#84ADFF;color:#1849A9' : ''}">加载${label(kind)}</button>`).join('');
    controls.querySelectorAll<HTMLButtonElement>('[data-legacy-marketing-load]').forEach((element) => element.onclick = () => { void loadList(element.dataset.legacyMarketingLoad as Kind, 0); });
  };

  const loadDetail = async (kind: Kind, historyID: number): Promise<void> => {
    content.innerHTML = `<p role="status">正在读取${label(kind)}详情…</p>`;
    try {
      const item = kind === 'state' ? await api.getState(historyID) : await api.getValue(historyID);
      content.innerHTML = `<div style="display:flex;justify-content:space-between;align-items:center;gap:12px"><h2 style="font-size:16px;margin:0">${label(kind)}详情 #${item.id}</h2><button data-legacy-marketing-back style="${button}">返回列表</button></div><section style="padding:12px;border:1px solid #D6E4FF;border-radius:8px;background:#F5F8FF;color:#1849A9;font-size:13px;line-height:20px">这是历史观察记录，不代表当前营销分层或执行资格。</section><div style="overflow:auto"><table style="width:100%;max-width:960px;border-collapse:collapse;font-size:13px"><tbody>${detailRows(kind, item)}</tbody></table></div>`;
      content.querySelector<HTMLButtonElement>('[data-legacy-marketing-back]')!.onclick = () => { void loadList(kind, 0); };
    } catch (error) {
      content.innerHTML = `<p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据，也未回退 Mock。</p><button data-legacy-marketing-retry style="${button}">重试详情</button>`;
      content.querySelector<HTMLButtonElement>('[data-legacy-marketing-retry]')!.onclick = () => { void loadDetail(kind, historyID); };
    }
  };

  const loadList = async (kind: Kind, offset: number): Promise<void> => {
    selected = kind;
    renderControls();
    content.innerHTML = `<h2 style="font-size:16px;margin:0">${label(kind)}</h2><p role="status">正在读取 V1 历史…</p>`;
    try {
      const page: LegacyMarketingHistoryPage<HistoryItem> = kind === 'state' ? await api.listStates(20, offset) : await api.listValues(20, offset);
      const headings = kind === 'state' ? ['历史记录', '原场景 / 阶段', '原生命周期 / 批次', '只读详情'] : ['历史记录', '原场景', '原价值分层', '原 score / 详情'];
      content.innerHTML = `<h2 style="font-size:16px;margin:0">${label(kind)}</h2><div style="overflow:auto"><table style="width:100%;min-width:820px;border-collapse:collapse;font-size:13px"><thead><tr style="background:#FAFAFB;color:#667085">${headings.map((heading) => `<th style="${cell}">${heading}</th>`).join('')}</tr></thead><tbody>${rows(kind, page.items)}</tbody></table></div><div style="display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap"><span>共 ${page.total} 条 · 当前 ${page.items.length ? page.offset + 1 : 0}–${page.items.length ? page.offset + page.items.length : 0}</span><div style="display:flex;gap:8px"><button data-legacy-marketing-prev style="${button}" ${page.offset === 0 ? 'disabled' : ''}>上一页</button><button data-legacy-marketing-next style="${button}" ${page.offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div></div>`;
      content.querySelectorAll<HTMLButtonElement>('[data-legacy-marketing-detail]').forEach((element) => element.onclick = () => { void loadDetail(kind, Number(element.dataset.legacyMarketingDetail)); });
      content.querySelector<HTMLButtonElement>('[data-legacy-marketing-prev]')?.addEventListener('click', () => { void loadList(kind, Math.max(0, page.offset - page.limit)); });
      content.querySelector<HTMLButtonElement>('[data-legacy-marketing-next]')?.addEventListener('click', () => { void loadList(kind, page.offset + page.limit); });
    } catch (error) {
      content.innerHTML = `<h2 style="font-size:16px;margin:0">${label(kind)}</h2><p role="alert" style="color:#D83931">${esc(error instanceof Error ? error.message : '历史读取失败')}；未显示历史数据，也未回退 Mock。</p><button data-legacy-marketing-retry style="${button}">重试本页</button>`;
      content.querySelector<HTMLButtonElement>('[data-legacy-marketing-retry]')!.onclick = () => { void loadList(kind, offset); };
    }
  };

  renderControls();
  content.innerHTML = '<p>请选择一种 V1 历史快照后再读取。页面不会自动加载，也没有写入或执行操作。</p>';
}
