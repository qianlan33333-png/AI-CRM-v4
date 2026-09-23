import {
  getProfileHistoryTemplate,
  listProfileHistoryCategories,
  listProfileHistoryOptionMappings,
  listProfileHistoryTemplates,
  listSignupTagHistoryRules,
} from "../../api/generated/p4-profile-catalog-history/p4-profile-catalog-history";
import {
  type ProfileCatalogHistoryCategory,
  type ProfileCatalogHistoryCategoryPage,
  type ProfileCatalogHistoryOptionMapping,
  type ProfileCatalogHistoryOptionMappingPage,
  type ProfileCatalogHistorySignupTagRule,
  type ProfileCatalogHistorySignupTagRulePage,
  type ProfileCatalogHistoryTemplate,
  type ProfileCatalogHistoryTemplateDetail,
  type ProfileCatalogHistoryTemplatePage,
} from "../../api/generated/health.schemas";
import { apiRequestOptions, unwrapGenerated } from '../../api/transport';
import { esc } from './util';

type HistoryPage = ProfileCatalogHistoryTemplatePage | ProfileCatalogHistoryCategoryPage | ProfileCatalogHistoryOptionMappingPage | ProfileCatalogHistorySignupTagRulePage;
type HistoryItem = ProfileCatalogHistoryTemplate | ProfileCatalogHistoryCategory | ProfileCatalogHistoryOptionMapping | ProfileCatalogHistorySignupTagRule;
type Field = [string, string, 'digest'?];
type Page<T> = { items: T[]; total: number; limit: number; offset: number };

const cardStyle = 'background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:16px';
const historyBase = 'config.html?profile_catalog_history=1';

export function requireProfileCatalogHistoryID(value: string | number): number {
  if ((typeof value !== 'string' && typeof value !== 'number') || (typeof value === 'string' && !/^[1-9][0-9]*$/.test(value)) || !Number.isSafeInteger(Number(value)) || Number(value) < 1) throw new Error('历史 ID 无效');
  return Number(value);
}

export function profileCatalogHistoryDigestHex(value: number[]): string {
  if (!Array.isArray(value) || value.length !== 32 || value.some((part) => !Number.isInteger(part) || part < 0 || part > 255) || value.every((part) => part === 0)) throw new Error('历史摘要格式无效');
  return value.map((part) => part.toString(16).padStart(2, '0')).join('');
}

function signedInteger(value: unknown): void {
  if (typeof value !== 'number' || !Number.isSafeInteger(value)) throw new Error('历史源 ID 无法精确展示');
}

function text(value: unknown): asserts value is string {
  if (typeof value !== 'string') throw new Error('历史文本字段无效');
}

function dateTime(value: unknown): void {
  text(value);
  if (Number.isNaN(Date.parse(value))) throw new Error('历史时间字段无效');
}

function nullableSigned(value: unknown): void {
  if (value !== null) signedInteger(value);
}

function pageArgs(limit: number, offset: number): { limit: number; offset: number } {
  if (!Number.isInteger(limit) || limit < 1 || limit > 100 || !Number.isInteger(offset) || offset < 0 || offset > 2147483647) throw new Error('历史分页参数无效');
  return { limit, offset };
}

function safety(value: { source: string; read_only: boolean; real_external_call_executed: boolean }): void {
  if (!value || value.source !== 'v1_history' || value.read_only !== true || value.real_external_call_executed !== false) throw new Error('档案历史响应不是只读历史事实');
}

function templateRow(value: unknown): asserts value is ProfileCatalogHistoryTemplate {
  const row = value as ProfileCatalogHistoryTemplate;
  requireProfileCatalogHistoryID(row?.id);
  signedInteger(row.source_id);
  profileCatalogHistoryDigestHex(row.source_key_digest);
  profileCatalogHistoryDigestHex(row.source_payload_digest);
  for (const key of ['template_code', 'template_name', 'description'] as const) text(row[key]);
  for (const key of ['created_at', 'updated_at'] as const) dateTime(row[key]);
  for (const key of ['questionnaire_source_id', 'segmentation_question_source_id', 'program_source_id'] as const) nullableSigned(row[key]);
  if (typeof row.original_enabled !== 'boolean') throw new Error('历史模板开关无效');
  signedInteger(row.version);
  profileCatalogHistoryDigestHex(row.created_by_digest);
  profileCatalogHistoryDigestHex(row.updated_by_digest);
}

function categoryRow(value: unknown): asserts value is ProfileCatalogHistoryCategory {
  const row = value as ProfileCatalogHistoryCategory;
  requireProfileCatalogHistoryID(row?.id);
  requireProfileCatalogHistoryID(row.template_history_id);
  for (const key of ['source_id', 'template_source_id', 'sort_order'] as const) signedInteger(row[key]);
  profileCatalogHistoryDigestHex(row.source_key_digest);
  profileCatalogHistoryDigestHex(row.source_payload_digest);
  for (const key of ['category_key', 'category_name', 'description'] as const) text(row[key]);
  for (const key of ['created_at', 'updated_at'] as const) dateTime(row[key]);
  if (typeof row.original_enabled !== 'boolean') throw new Error('历史类目开关无效');
}

function mappingRow(value: unknown): asserts value is ProfileCatalogHistoryOptionMapping {
  const row = value as ProfileCatalogHistoryOptionMapping;
  requireProfileCatalogHistoryID(row?.id);
  requireProfileCatalogHistoryID(row.template_history_id);
  requireProfileCatalogHistoryID(row.category_history_id);
  for (const key of ['source_id', 'template_source_id', 'category_source_id', 'question_source_id', 'option_source_id'] as const) signedInteger(row[key]);
  profileCatalogHistoryDigestHex(row.source_key_digest);
  profileCatalogHistoryDigestHex(row.source_payload_digest);
  dateTime(row.created_at);
}

function signupRuleRow(value: unknown): asserts value is ProfileCatalogHistorySignupTagRule {
  const row = value as ProfileCatalogHistorySignupTagRule;
  requireProfileCatalogHistoryID(row?.id);
  profileCatalogHistoryDigestHex(row.source_key_digest);
  profileCatalogHistoryDigestHex(row.source_payload_digest);
  for (const key of ['tag_source_id', 'tag_name', 'signup_status'] as const) text(row[key]);
  dateTime(row.updated_at);
  if (typeof row.original_active !== 'boolean') throw new Error('历史报名标签规则开关无效');
}

function checkRow(value: unknown, kind: 'template' | 'category' | 'mapping' | 'signup_rule'): asserts value is HistoryItem {
  if (kind === 'template') return templateRow(value);
  if (kind === 'category') return categoryRow(value);
  if (kind === 'mapping') return mappingRow(value);
  return signupRuleRow(value);
}

function checkedPage<T extends HistoryPage>(value: T, limit: number, offset: number, kind: 'template' | 'category' | 'mapping' | 'signup_rule', parent?: { templateID: number; categoryID?: number }): T {
  safety(value);
  if (!Array.isArray(value.items) || !Number.isSafeInteger(value.total) || value.total < 0 || value.limit !== limit || value.offset !== offset || value.items.length !== Math.min(limit, Math.max(0, value.total - offset))) throw new Error('档案历史分页响应无效');
  value.items.forEach((item) => checkRow(item, kind));
  if (parent && value.items.some((item) => {
    const row = item as ProfileCatalogHistoryCategory | ProfileCatalogHistoryOptionMapping;
    return row.template_history_id !== parent.templateID || (parent.categoryID !== undefined && (row as ProfileCatalogHistoryOptionMapping).category_history_id !== parent.categoryID);
  })) throw new Error('档案历史父关联不匹配');
  return value;
}

export async function readProfileHistoryTemplates(limit = 20, offset = 0): Promise<ProfileCatalogHistoryTemplatePage> {
  return checkedPage(unwrapGenerated(await listProfileHistoryTemplates(pageArgs(limit, offset), apiRequestOptions())) as ProfileCatalogHistoryTemplatePage, limit, offset, 'template');
}

export async function readProfileHistoryTemplate(idValue: string | number): Promise<ProfileCatalogHistoryTemplateDetail> {
  const id = requireProfileCatalogHistoryID(idValue);
  const value = unwrapGenerated(await getProfileHistoryTemplate(id, apiRequestOptions())) as ProfileCatalogHistoryTemplateDetail;
  safety(value);
  if (!value.item || value.item.id !== id) throw new Error('档案历史详情 ID 不匹配');
  templateRow(value.item);
  return value;
}

export async function readProfileHistoryCategories(templateID: string | number, limit = 20, offset = 0): Promise<ProfileCatalogHistoryCategoryPage> {
  const id = requireProfileCatalogHistoryID(templateID);
  return checkedPage(unwrapGenerated(await listProfileHistoryCategories(id, pageArgs(limit, offset), apiRequestOptions())) as ProfileCatalogHistoryCategoryPage, limit, offset, 'category', { templateID: id });
}

export async function readProfileHistoryOptionMappings(templateID: string | number, categoryID: string | number, limit = 20, offset = 0): Promise<ProfileCatalogHistoryOptionMappingPage> {
  const template = requireProfileCatalogHistoryID(templateID);
  const category = requireProfileCatalogHistoryID(categoryID);
  return checkedPage(unwrapGenerated(await listProfileHistoryOptionMappings(template, category, pageArgs(limit, offset), apiRequestOptions())) as ProfileCatalogHistoryOptionMappingPage, limit, offset, 'mapping', { templateID: template, categoryID: category });
}

export async function readSignupTagHistoryRules(limit = 20, offset = 0): Promise<ProfileCatalogHistorySignupTagRulePage> {
  return checkedPage(unwrapGenerated(await listSignupTagHistoryRules(pageArgs(limit, offset), apiRequestOptions())) as ProfileCatalogHistorySignupTagRulePage, limit, offset, 'signup_rule');
}

function cell(value: unknown): string {
  if (value === undefined) throw new Error('历史字段缺失');
  return esc(value === null ? 'NULL（源未记录）' : value === '' ? '""（源空字符串）' : value);
}

function fields(row: object, columns: Field[]): string {
  const values = row as Record<string, unknown>;
  return columns.map(([label, key, kind]) => {
    const value = values[key];
    const rendered = kind === 'digest' ? (() => { const hex = profileCatalogHistoryDigestHex(value as number[]); return `<span title="${hex}">${hex.slice(0, 16)}…</span>`; })() : cell(value);
    return `<div><span style="color:#667085">${esc(label)}：</span><span style="white-space:pre-wrap;overflow-wrap:anywhere">${rendered}</span></div>`;
  }).join('');
}

const article = (body: string): string => `<article style="border-top:1px solid #EEF0F3;padding:12px 0;display:grid;gap:5px">${body}</article>`;
const templateFields: Field[] = [
  ['V2 历史 ID', 'id'], ['源 V1 signed ID', 'source_id'], ['源模板代码', 'template_code'], ['源模板名称', 'template_name'],
  ['源问卷 ID', 'questionnaire_source_id'], ['源分群问题 ID', 'segmentation_question_source_id'], ['源项目 ID', 'program_source_id'],
  ['说明', 'description'], ['源 enabled（不生效）', 'original_enabled'], ['源版本', 'version'], ['创建时间', 'created_at'], ['更新时间', 'updated_at'], ['源键摘要', 'source_key_digest', 'digest'],
  ['创建 actor 摘要', 'created_by_digest', 'digest'], ['更新 actor 摘要', 'updated_by_digest', 'digest'], ['源载荷摘要', 'source_payload_digest', 'digest'],
];
const categoryFields: Field[] = [
  ['V2 历史 ID', 'id'], ['源 V1 signed ID', 'source_id'], ['源模板 signed ID', 'template_source_id'], ['历史模板 ID', 'template_history_id'],
  ['源类目键', 'category_key'], ['源类目名称', 'category_name'], ['说明', 'description'], ['源排序', 'sort_order'], ['源 enabled（不生效）', 'original_enabled'], ['创建时间', 'created_at'], ['更新时间', 'updated_at'], ['源键摘要', 'source_key_digest', 'digest'], ['源载荷摘要', 'source_payload_digest', 'digest'],
];
const mappingFields: Field[] = [
  ['V2 历史 ID', 'id'], ['源 V1 signed ID', 'source_id'], ['源模板 signed ID', 'template_source_id'], ['源类目 signed ID', 'category_source_id'], ['历史模板 ID', 'template_history_id'], ['历史类目 ID', 'category_history_id'], ['源问题 signed ID', 'question_source_id'], ['源选项 signed ID', 'option_source_id'], ['创建时间', 'created_at'], ['源键摘要', 'source_key_digest', 'digest'], ['源载荷摘要', 'source_payload_digest', 'digest'],
];
const signupRuleFields: Field[] = [
  ['V2 历史 ID', 'id'], ['源标签命名空间 ID', 'tag_source_id'], ['源标签名称', 'tag_name'], ['源报名状态', 'signup_status'], ['源 active（不生效）', 'original_active'], ['更新时间', 'updated_at'], ['源键摘要', 'source_key_digest', 'digest'], ['源载荷摘要', 'source_payload_digest', 'digest'],
];

function rows(kind: 'templates' | 'categories' | 'mappings' | 'signup_rules', items: object[], templateID?: number): string {
  return items.map((item) => {
    const row = item as Record<string, unknown>;
    if (kind === 'templates') return article(`<a style="color:#245BDB" href="${historyBase}&history_template_id=${requireProfileCatalogHistoryID(row.id as number)}">查看模板历史详情</a>${fields(item, templateFields)}`);
    if (kind === 'categories') return article(`<a style="color:#245BDB" href="${historyBase}&history_template_id=${templateID}&history_category_id=${requireProfileCatalogHistoryID(row.id as number)}">查看该类目的选项映射</a>${fields(item, categoryFields)}`);
    return article(fields(item, kind === 'mappings' ? mappingFields : signupRuleFields));
  }).join('');
}

async function section<T extends object>(host: HTMLElement, title: string, kind: 'templates' | 'categories' | 'mappings' | 'signup_rules', read: (limit: number, offset: number) => Promise<Page<T>>, templateID?: number): Promise<void> {
  const limit = 20;
  async function load(offset: number): Promise<void> {
    host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="status">正在读取历史记录…</p>`;
    try {
      const page = await read(limit, offset);
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><div data-history-rows>${page.items.length ? rows(kind, page.items, templateID) : '<p>暂无历史记录</p>'}</div><div style="display:flex;gap:10px;align-items:center"><button data-prev ${offset === 0 ? 'disabled' : ''}>上一页</button><span>共 ${page.total} 条 · offset=${page.offset} · 每页 ${page.limit} 条</span><button data-next ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button></div>`;
      host.querySelector('[data-prev]')?.addEventListener('click', () => void load(Math.max(0, offset - limit)));
      host.querySelector('[data-next]')?.addEventListener('click', () => void load(offset + limit));
    } catch {
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="alert">历史数据读取失败；未使用演示数据。</p><button data-retry>重试本页（offset=${offset}）</button>`;
      host.querySelector('[data-retry]')?.addEventListener('click', () => void load(offset));
    }
  }
  await load(0);
}

export async function mountProfileCatalogHistory(stage: HTMLElement, options: { templateID?: string; categoryID?: string; view?: string } = {}): Promise<void> {
  stage.innerHTML = `<div data-profile-catalog-history style="overflow:auto;padding:20px;display:grid;gap:16px"><header><h1 style="font-size:20px">Profile Catalog · V1 历史（只读）</h1><a href="config.html">返回当前配置</a> · <a href="${historyBase}">模板历史</a> · <a href="${historyBase}&history_view=signup_rules">报名标签规则历史</a><p style="padding:12px;background:#FFF9F0;border:1px solid #F5D6A7;border-radius:8px">仅展示迁移后的历史事实。源 V1 signed ID、false、NULL、0 和原时间保持原义；历史 enabled/active 不代表当前生效。本页不会保存、启用、执行规则或调用 Provider，actor 与原始载荷仅显示摘要。</p></header><div data-history-sections style="display:grid;gap:16px"></div></div>`;
  const body = stage.querySelector<HTMLElement>('[data-history-sections]')!;
  const makeHost = (kind: string): HTMLElement => {
    const host = document.createElement('section');
    host.dataset.historyKind = kind;
    host.setAttribute('style', cardStyle);
    body.append(host);
    return host;
  };
  if (options.view !== undefined && options.view !== 'templates' && options.view !== 'signup_rules') {
    body.innerHTML = '<p role="alert">历史分区无效；未读取任何记录。</p>';
    return;
  }
  if (options.categoryID !== undefined && options.templateID === undefined) {
    body.innerHTML = '<p role="alert">历史类目缺少模板关联；未读取任何记录。</p>';
    return;
  }
  let templateID: number | undefined;
  let categoryID: number | undefined;
  try {
    templateID = options.templateID === undefined ? undefined : requireProfileCatalogHistoryID(options.templateID);
    categoryID = options.categoryID === undefined ? undefined : requireProfileCatalogHistoryID(options.categoryID);
  } catch {
    body.innerHTML = '<p role="alert">历史 ID 无效；未读取任何记录。</p>';
    return;
  }
  if (options.view === 'signup_rules') {
    if (templateID !== undefined || categoryID !== undefined) body.innerHTML = '<p role="alert">历史分区参数冲突；未读取任何记录。</p>';
    else await section(makeHost('signup_rules'), '报名标签规则历史（不激活）', 'signup_rules', readSignupTagHistoryRules);
    return;
  }
  if (categoryID !== undefined) {
    await section(makeHost('mappings'), `历史选项映射 · 模板=${templateID} · 类目=${categoryID}`, 'mappings', (limit, offset) => readProfileHistoryOptionMappings(templateID!, categoryID!, limit, offset), templateID);
    return;
  }
  if (templateID !== undefined) {
    const detail = makeHost('template_detail');
    try {
      const value = await readProfileHistoryTemplate(templateID);
      detail.innerHTML = `<h2 style="font-size:16px">历史模板详情</h2>${fields(value.item, templateFields)}`;
      await section(makeHost('categories'), `历史类目 · 模板=${templateID}`, 'categories', (limit, offset) => readProfileHistoryCategories(templateID!, limit, offset), templateID);
    } catch {
      detail.innerHTML = '<p role="alert">历史详情读取失败；未使用演示数据。</p><button data-retry>重试读取详情</button>';
      detail.querySelector('[data-retry]')?.addEventListener('click', () => void mountProfileCatalogHistory(stage, options));
    }
    return;
  }
  await section(makeHost('templates'), '历史模板', 'templates', readProfileHistoryTemplates);
}
