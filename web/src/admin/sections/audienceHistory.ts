import {
  readAudienceHistoryGroups, readAudienceHistoryPackages, readAudienceHistoryVersions, readAudienceHistorySenders,
  readAudienceHistoryRules, readAudienceHistoryRuleVersions, readAudienceHistoryDefinitions, readAudienceHistoryMembers,
  readAudienceHistoryActivityRuns, readAudienceHistoryActivityMemberEvents,
  readAudienceHistoryPackage, readAudienceHistoryDefinition, requireAudienceHistoryID, audienceHistoryDigestHex,
} from '../../api/audienceHistory';
import { esc } from './util';

type Field = [string, string, ('relation' | 'digest')?];
const identity: Field[] = [['V2 历史 ID', 'id'], ['源记录 ID', 'source_id']];
const timestamps: Field[] = [['创建时间', 'created_at'], ['更新时间', 'updated_at']];
const status: Field = ['原始状态', 'original_status'];
const cardStyle = 'background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px';

function cell(value: unknown): string {
  if (value === undefined) throw new Error('历史字段缺失');
  return esc(value === null ? 'NULL（源未记录）' : value === '' ? '""（源空字符串）' : value);
}

function fields(row: object, columns: Field[]): string {
  const values = row as Record<string, unknown>;
  return columns.map(([label, key, kind]) => {
    const value = values[key];
    let text = cell(value);
    if (kind === 'relation' && value === null) text = 'NULL（历史关联未确认）';
    if (kind === 'relation' && ['customer_id', 'staff_id', 'owner_staff_id'].includes(key)) text = value === null ? 'NULL（未解析）' : `${text}（DM01 历史映射）`;
    if (kind === 'digest') {
      const hex = audienceHistoryDigestHex(value as number[]);
      text = `<span title="${hex}">${hex.slice(0, 16)}…</span>`;
    }
    return `<div><span style="color:#667085">${esc(label)}：</span><span style="white-space:pre-wrap;overflow-wrap:anywhere">${text}</span></div>`;
  }).join('');
}

const article = (body: string): string => `<article style="border-top:1px solid #EEF0F3;padding:12px 0;display:grid;gap:5px">${body}</article>`;
const historyLink = (key: string, id: unknown, label: unknown): string => `<a style="color:#245BDB" href="automation.html?history=1&${key}=${requireAudienceHistoryID(id as number)}">${cell(label)}</a>`;

const packageFields: Field[] = [
  ...identity, ['名称', 'name'], ['源 package_key', 'package_key'], status,
  ['历史分组关联', 'group_history_id', 'relation'], ['源 current_version_source_id', 'current_version_source_id', 'relation'],
  ['源自然语言定义', 'natural_language_definition'], ['源 query_mode', 'query_mode'], ['源 identity_policy', 'identity_policy'],
  ['源增量开关（不启用）', 'incremental_enabled'], ['源日开关（不启用）', 'daily_enabled'],
  ['源增量间隔秒', 'incremental_interval_seconds'], ['源日时钟', 'daily_refresh_time'], ['源时区文本', 'timezone'], ['源回看秒', 'lookback_seconds'],
  ['源上次增量时间', 'last_incremental_at'], ['源上次日执行时间', 'last_daily_refreshed_at'],
  ['源下次增量时间（不调度）', 'next_incremental_at'], ['源下次日执行时间（不调度）', 'next_daily_at'],
  ['源暂停原因', 'paused_reason'], ...timestamps, ['运行定义摘要', 'runtime_digest', 'digest'],
];
const definitionFields: Field[] = [
  ...identity, ['源 code', 'code'], ['名称', 'display_name'], ['说明', 'description'], ['源类型', 'source_type'],
  ['源 SQL dialect（不执行）', 'sql_dialect'], status, ['源版本', 'version'], ['源缓存人数', 'cached_headcount'],
  ['源最后记录时间', 'last_refreshed_at'], ['源使用次数', 'usage_count'], ...timestamps, ['定义摘要', 'definition_digest', 'digest'],
];
const activityIdentity: Field[] = [['V2 历史 ID', 'id']];

function rows(kind: string, items: object[]): string {
  return items.map((row) => {
    const r = row as Record<string, unknown>;
    switch (kind) {
      case 'groups': return article(fields(row, [...identity, ['分组名', 'name'], ...timestamps]));
      case 'packages': return article(historyLink('history_package_id', r.id, r.name) + fields(row, [...identity, ['源 package_key', 'package_key'], status, ['历史分组关联', 'group_history_id', 'relation'], ...timestamps]));
      case 'versions': return article(fields(row, [...identity, ['历史包 ID', 'package_history_id'], ['源版本', 'version_number'], status, ['源模板键', 'template_key'], ['源模板版本', 'template_version'], ['源模板指纹', 'template_fingerprint'], ['自然语言说明', 'natural_language_explanation'], ['创建时间', 'created_at'], ['原发布时间', 'published_at'], ['定义摘要', 'definition_digest', 'digest']]));
      case 'senders': return article(fields(row, [...identity, ['历史包 ID', 'package_history_id'], ['历史 staff_id', 'staff_id', 'relation'], ['历史显示名称', 'display_name'], ['源优先级', 'priority'], status, ...timestamps]));
      case 'rules': return article(historyLink('history_rule_id', r.id, r.display_name) + fields(row, [...identity, ['源 rule_key', 'rule_key'], ['说明', 'description'], ['源规则类型', 'rule_type'], ['历史负责人 staff_id', 'owner_staff_id', 'relation'], status, ...timestamps]));
      case 'rule_versions': return article(fields(row, [...identity, ['历史规则 ID', 'rule_history_id'], ['源版本', 'version'], ['源执行器类型（不执行）', 'executor_type'], status, ['原发布时间', 'published_at'], ['创建时间', 'created_at'], ['定义摘要', 'definition_digest', 'digest']]));
      case 'definitions': return article(historyLink('history_definition_id', r.id, r.display_name) + fields(row, [...identity, ['源 code', 'code'], status, ['源版本', 'version'], ['源缓存人数', 'cached_headcount'], ['源使用次数', 'usage_count'], ...timestamps]));
      case 'members': return article(fields(row, [...identity, ['历史包 ID', 'package_history_id'], ['历史 CustomerID', 'customer_id', 'relation'], ['原身份类型（非外部身份）', 'identity_kind'], status, ['原首次进入时间', 'first_entered_at'], ['原最后出现时间', 'last_seen_at'], ['原最后变更时间', 'last_updated_at'], ['原退出时间', 'exited_at'], ...timestamps, ['原载荷摘要', 'payload_digest', 'digest']]));
      case 'activity_runs': return article(fields(row, [...activityIdentity, ['历史包 ID', 'package_history_id'], ['历史版本 ID', 'version_history_id', 'relation'], ['原运行类型', 'run_type'], status, ['原开始时间', 'refresh_started_at'], ['原结束时间', 'refresh_finished_at'], ['原上次水位', 'last_watermark_at'], ['原下次水位', 'next_watermark_at'], ['原返回人数', 'returned_count'], ['原进入人数', 'entered_count'], ['原更新人数', 'updated_count'], ['原退出人数', 'exited_count'], ['原成员事件数', 'member_event_count'], ['原耗时毫秒', 'duration_ms'], ['创建时间', 'created_at']]));
      case 'activity_member_events': return article(fields(row, [...activityIdentity, ['历史包 ID', 'package_history_id'], ['历史运行 ID', 'run_history_id', 'relation'], ['历史成员 ID', 'member_history_id', 'relation'], ['原事件类型', 'event_type'], ['原发生时间', 'occurred_at'], ['创建时间', 'created_at']]));
      default: throw new Error('未知历史分区');
    }
  }).join('');
}

type Page<T> = { items: T[]; total: number; limit: number; offset: number };

async function section<T extends object>(host: HTMLElement, title: string, kind: string, read: (limit: number, offset: number) => Promise<Page<T>>): Promise<void> {
  const limit = 20;
  async function load(offset: number): Promise<void> {
    host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="status">正在读取历史记录…</p>`;
    try {
      const page = await read(limit, offset);
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><div data-history-rows>${page.items.length ? rows(kind, page.items) : '<p>暂无历史记录</p>'}</div><div style="display:flex;gap:10px;align-items:center"><button data-prev ${offset === 0 ? 'disabled' : ''}>上一页</button><span>共 ${page.total} 条 · offset=${page.offset} · 每页 ${page.limit} 条</span><button data-next ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div>`;
      host.querySelector('[data-prev]')?.addEventListener('click', () => void load(Math.max(0, offset - limit)));
      host.querySelector('[data-next]')?.addEventListener('click', () => void load(offset + limit));
    } catch {
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="alert">历史数据读取失败；未使用演示数据。</p><button data-retry>重试本页（offset=${offset}）</button>`;
      host.querySelector('[data-retry]')?.addEventListener('click', () => void load(offset));
    }
  }
  await load(0);
}

export async function mountAudienceHistory(stage: HTMLElement, options: { packageID?: string; definitionID?: string; ruleID?: string } = {}): Promise<void> {
  stage.innerHTML = `<div data-audience-history style="overflow:auto;padding:20px;display:grid;gap:16px"><header><h1 style="font-size:20px">Audience · V1 历史（只读）</h1><a href="automation.html">返回当前运营管理</a> · <a href="automation.html?history=1">历史列表</a><p style="padding:12px;background:#FFF9F0;border:1px solid #F5D6A7;border-radius:8px">仅展示迁移后的历史事实，不代表当前人群命中或授权。原状态、时间、负数、0 与 NULL 保留原义；历史开关仅作记录，不启用、不调度、不发送、不调用 Provider。源 SQL、代码、外部身份不在此页公开。</p></header><div data-history-sections style="display:grid;gap:16px"></div></div>`;
  const body = stage.querySelector<HTMLElement>('[data-history-sections]')!;
  function host(kind: string): HTMLElement {
    const node = document.createElement('section');
    node.dataset.historyKind = kind;
    node.setAttribute('style', cardStyle);
    body.append(node);
    return node;
  }
  const selected = [options.packageID, options.definitionID, options.ruleID].filter((v) => v !== undefined);
  if (selected.length > 1) { body.innerHTML = '<p role="alert">历史入口参数冲突；未读取记录。</p>'; return; }
  if (selected.length) {
    let id: number;
    try { id = requireAudienceHistoryID(selected[0]!); }
    catch { body.innerHTML = '<p role="alert">历史 ID 无效；未读取任何记录。</p>'; return; }
    if (options.ruleID !== undefined) {
      await section(host('rule_versions'), `历史规则版本 · V2 ID=${id}`, 'rule_versions', (limit, offset) => readAudienceHistoryRuleVersions(id, limit, offset));
      return;
    }
    const detail = host('detail');
    try {
      if (options.packageID !== undefined) {
        const value = await readAudienceHistoryPackage(id);
        detail.innerHTML = `<h2 style="font-size:16px">历史人群包详情</h2>${fields(value.item, packageFields)}`;
        await Promise.all([
          section(host('versions'), '历史版本', 'versions', (limit, offset) => readAudienceHistoryVersions(id, limit, offset)),
          section(host('senders'), '历史发送人（不用于当前发送）', 'senders', (limit, offset) => readAudienceHistorySenders(id, limit, offset)),
          section(host('members'), '历史成员（不是当前命中成员）', 'members', (limit, offset) => readAudienceHistoryMembers(id, limit, offset)),
        ]);
      } else {
        const value = await readAudienceHistoryDefinition(id);
        detail.innerHTML = `<h2 style="font-size:16px">历史 Segment 定义</h2>${fields(value.item, definitionFields)}`;
      }
    } catch {
      detail.innerHTML = '<p role="alert">历史详情读取失败；未使用演示数据。</p><button data-retry>重试读取详情</button>';
      detail.querySelector('[data-retry]')?.addEventListener('click', () => void mountAudienceHistory(stage, options));
    }
    return;
  }
  await Promise.all([
    section(host('packages'), '历史人群包', 'packages', readAudienceHistoryPackages),
    section(host('groups'), '历史分组', 'groups', readAudienceHistoryGroups),
    section(host('rules'), '历史规则', 'rules', readAudienceHistoryRules),
    section(host('definitions'), '历史 Segment 定义', 'definitions', readAudienceHistoryDefinitions),
    section(host('activity_runs'), '历史活动运行（不刷新当前人群）', 'activity_runs', readAudienceHistoryActivityRuns),
    section(host('activity_member_events'), '历史成员事件（不是当前成员状态）', 'activity_member_events', readAudienceHistoryActivityMemberEvents),
  ]);
}
