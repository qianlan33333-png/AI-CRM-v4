// @ts-nocheck -- Ordinary questionnaire editor; assessment creation and builder retired.
import { request as apiRequest } from '../../api/transport';
import {
  deleteEditorQuestionnaire,
  duplicateEditorQuestionnaire,
  editorQuestionnaireExportUrl,
  getEditorQuestionnaire,
  listEditorQuestionnaires,
  listEditorTags,
  publishEditorQuestionnaire,
  saveEditorQuestionnaire,
  setEditorQuestionnaireDisabled,
} from '../../api/questionnaireEditorV3';
import './wecomTagPicker';

document.getElementById('open-assessment-settings')?.remove();
const editorConfigElement = document.getElementById('questionnaire-editor-config');
if (!editorConfigElement) throw new Error('questionnaire editor config is missing');
const editorConfig = JSON.parse(editorConfigElement.textContent || '{}');
const editorQuery = new URLSearchParams(window.location.search);
const requestedQuestionnaireId = Number(editorQuery.get('id') || editorConfig.initialQuestionnaireId || '');

const listEl = document.getElementById('questionnaire-list');
const listSearchEl = document.getElementById('list-search');
const statusFilterEl = document.getElementById('status-filter');
const previewHeadEl = document.getElementById('preview-head');
const previewQuestionsEl = document.getElementById('preview-questions');
const previewRulesWrapEl = document.getElementById('preview-rules-wrap');
const inspectorBodyEl = document.getElementById('inspector-body');
const inspectorTitleEl = document.getElementById('inspector-title');
const inspectorSubtitleEl = document.getElementById('inspector-subtitle');
const backLinkEl = document.getElementById('back-link');
const editorPageTitleEl = document.getElementById('editor-page-title');
const editorPageSubtitleEl = document.getElementById('editor-page-subtitle');
const editorSecondaryActionsEl = document.getElementById('editor-secondary-actions');
const topbarTitleEl = document.getElementById('topbar-title');
const draftIndicatorEl = document.getElementById('draft-indicator');
const tagCatalogMessageEl = document.getElementById('tag-catalog-message');
const drawerOverlayEl = document.getElementById('drawer-overlay');
const drawerTitleEl = document.getElementById('drawer-title');
const drawerBodyEl = document.getElementById('drawer-body');
const toastEl = document.getElementById('toast');

const state = {
  list: [],
  availableTags: [],
  availableTagMap: new Map(),
  questionnaire: null,
  currentId: null,
  editorMode: editorConfig.mode || 'new',
  selection: { kind: 'questionnaire' },
  ruleMode: false,
  lastRuleKey: '',
  selectedDimensionKey: '',
  selectedQuestionKey: '',
  selectedOverallLevelKey: '',
  initialSnapshot: '',
  persistedIsDisabled: false,
  persistedPublishedAndEnabled: false,
  pendingRepublish: false,
  listSearch: '',
  statusFilter: 'all',
  loadingList: false,
  tagModal: {
    open: false,
    search: '',
    selected: [],
    target: null,
  },
};
let localSeq = 0;
let toastTimer = null;
const SIDEBAR_PROFILE_FIELD_OPTIONS = [
  { value: '', label: '无，不映射到侧边栏' },
  { value: 'source', label: '用户来源' },
  { value: 'industry', label: '行业信息' },
  { value: 'industry_description', label: '行业具体描述' },
  { value: 'needs_blockers_followup', label: '需求、卡点、跟进状态' },
];

function nextLocalKey(prefix) {
  localSeq += 1;
  return `${prefix}_${Date.now()}_${localSeq}`;
}

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function normalizeTagIds(value) {
  if (Array.isArray(value)) {
    return [...new Set(value.map((item) => String(item || '').trim()).filter(Boolean))];
  }
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed = JSON.parse(value);
      if (Array.isArray(parsed)) return normalizeTagIds(parsed);
    } catch (error) {
      return [...new Set(value.split(',').map((item) => item.trim()).filter(Boolean))];
    }
  }
  return [];
}

function formatQuestionType(type) {
  if (type === 'single_choice') return '单选题';
  if (type === 'multi_choice') return '多选题';
  if (type === 'textarea') return '文本题';
  if (type === 'mobile') return '手机号题';
  return type || '题目';
}

function normalizeSidebarProfileField(value) {
  const normalized = String(value || '').trim();
  return SIDEBAR_PROFILE_FIELD_OPTIONS.some((item) => item.value === normalized) ? normalized : '';
}

function sidebarProfileFieldLabel(value) {
  const normalized = normalizeSidebarProfileField(value);
  if (!normalized) return '';
  return SIDEBAR_PROFILE_FIELD_OPTIONS.find((item) => item.value === normalized)?.label || '';
}

function sidebarProfileFieldOptionsHtml(value) {
  const normalized = normalizeSidebarProfileField(value);
  return SIDEBAR_PROFILE_FIELD_OPTIONS.map((item) => `
    <option value="${escapeHtml(item.value)}" ${item.value === normalized ? 'selected' : ''}>${escapeHtml(item.label)}</option>
  `).join('');
}

function sidebarProfileChipHtml(question) {
  const label = sidebarProfileFieldLabel(question.sidebar_profile_field);
  return label ? `<span class="profile-map-chip">${escapeHtml(label)}</span>` : '';
}

function buildUnknownTag(tagId) {
  return { tag_id: tagId, tag_name: '未匹配标签', group_name: '' };
}

function ensureTagKnown(tagId) {
  return state.availableTagMap.get(tagId) || buildUnknownTag(tagId);
}

function formatTagLabel(tag) {
  return `${tag.group_name ? `${tag.group_name} / ` : ''}${tag.tag_name || '未匹配标签'}`;
}

function buildTagBadges(tagIds) {
  const ids = normalizeTagIds(tagIds);
  if (!ids.length) return '<span class="tag-picker-note">未选择标签</span>';
  return ids.map((tagId) => {
    const tag = ensureTagKnown(tagId);
    const isUnknown = !state.availableTagMap.has(tagId);
    return `<span class="tag-badge${isUnknown ? ' unknown' : ''}">${escapeHtml(formatTagLabel(tag))}</span>`;
  }).join('');
}

function buildPublicUrl(questionnaire = state.questionnaire) {
  if (!questionnaire) return '';
  if (questionnaire.public_url || questionnaire.public_path) {
    return new URL(questionnaire.public_url || questionnaire.public_path, window.location.origin).toString();
  }
  const slug = String(questionnaire.slug || '').trim();
  return slug ? `${window.location.origin}/s/${slug}` : '';
}

function questionnaireDisplayName(item, fallback = '未命名问卷') {
  const title = String(item?.title || '').trim();
  const name = String(item?.name || '').trim();
  return title || name || fallback;
}

function fieldLabel(fieldName) {
  const labels = {
    name: '问卷名称',
    title: '问卷标题',
    description: '问卷说明',
    slug: '分享标识',
    min_score: '最低分',
    max_score: '最高分',
    sort_order: '排序',
    required: '必填',
    option_text: '选项文案',
    score: '分值',
    tag_codes: '标签',
    assessment_config: '测评结果规则',
    assessment_dimension_key: '测评维度',
    assessment_type_key: '测评分型',
  };
  return labels[fieldName] || fieldName;
}

function questionLabel(rawTitle) {
  const title = String(rawTitle || '').trim();
  return title ? `题目“${title}”` : '该题目';
}

function humanizeErrorMessage(rawMessage, fallback = '操作失败，请稍后重试') {
  const message = String(rawMessage || '').trim();
  if (!message) return fallback;
  if (/[一-龥]/.test(message) && !/ is required|must be|unknown_|already_submitted|wechat_oauth_not_configured/i.test(message)) {
    return message;
  }

  if (message === 'name is required') return '请输入问卷名称';
  if (message === 'title is required') return '请输入问卷标题';
  if (message === 'questions must be an array') return '题目数据格式不正确，请重新添加题目';
  if (message === 'score must be an integer') return '分值必须填写数字';
  if (message === 'tag_codes must be an array') return '标签数据格式不正确，请重新选择标签';
  if (message === 'answers is required') return '请先填写问卷内容再提交';
  if (message === 'unknown question_id') return '检测到异常题目数据，请刷新页面后重试';
  if (message === 'already_submitted') return '你已经提交过这份问卷';
  if (message === 'wechat_oauth_not_configured') return '当前未完成微信授权配置，暂时无法使用该功能';
  if (message === 'question type must be single_choice, multi_choice, textarea or mobile') return '题型不正确，请重新选择题型';
  if (message === 'min_score must be an integer') return '最低分必须填写数字';
  if (message === 'max_score must be an integer') return '最高分必须填写数字';
  if (message === 'option_text is required') return '请输入选项文案';
  if (message === 'score rule min_score cannot be greater than max_score') return '请检查分数规则：最低分不能大于最高分';
  if (message === 'score rule tag_codes must be an array') return '分数规则标签格式不正确，请重新选择标签';
  if (message === 'slug already exists') return '分享标识已存在，请修改右侧分享标识后再保存';
  if (message === 'assessment_config must be an object') return '测评结果规则必须是一个 JSON 对象';

  let matched = message.match(/^([a-z_]+) is required$/i);
  if (matched) return `请输入${fieldLabel(matched[1])}`;

  matched = message.match(/^([a-z_]+) must be an integer$/i);
  if (matched) return `${fieldLabel(matched[1])}必须填写数字`;

  matched = message.match(/^question ['"]?(.*?)['"]? is required$/i);
  if (matched) return `${questionLabel(matched[1])}还未填写，请补充后再保存`;

  matched = message.match(/^question ['"]?(.*?)['"]? must have options$/i);
  if (matched) return `${questionLabel(matched[1])}至少需要一个选项，请补充后再保存`;

  matched = message.match(/^question ['"]?(.*?)['"]? has an invalid option$/i);
  if (matched) return `${questionLabel(matched[1])}存在无效选项，请检查选项内容`;

  matched = message.match(/^question ['"]?(.*?)['"]? only allows one option$/i);
  if (matched) return `${questionLabel(matched[1])}只能选择一个选项，请检查当前配置`;

  matched = message.match(/^unknown question_id:?/i);
  if (matched) return '检测到异常题目数据，请刷新页面后重试';

  if (/Failed to fetch|NetworkError|Load failed/i.test(message)) return '网络连接异常，请稍后重试';
  if (/Unexpected token|JSON/i.test(message)) return fallback;

  return fallback;
}

function extractErrorMessage(data) {
  if (window.AdminApi && typeof window.AdminApi.formatErrorValue === 'function') {
    return window.AdminApi.formatErrorValue(data);
  }
  return '';
}

function showToast(message, isError = false) {
  if (!message) return;
  toastEl.textContent = message;
  toastEl.className = `toast${isError ? ' error' : ''}`;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    toastEl.className = 'toast hidden';
  }, isError ? 18000 : 12600);
}

function closeDrawer() {
  drawerOverlayEl.classList.add('hidden');
}

function applyTagSelection(target, tagIds) {
  const merged = normalizeTagIds(tagIds);
  if (target?.type === 'option') {
    const question = state.questionnaire.questions.find((item) => item.local_key === target.questionKey);
    const option = question?.options.find((item) => item.local_key === target.optionKey);
    if (option) option.tag_codes = merged;
  }
  if (target?.type === 'rule') {
    const rule = state.questionnaire.score_rules.find((item) => item.local_key === target.ruleKey);
    if (rule) rule.tag_codes = merged;
  }
  renderWorkspace();
}

function openTagModal(target, selectedTagIds = []) {
  if (!window.AICRMWeComTagPicker) {
    showToast('标签选择器加载失败，请刷新后重试', true);
    return;
  }
  const selected = normalizeTagIds(selectedTagIds).map((tagId) => ensureTagKnown(tagId));
  window.AICRMWeComTagPicker.open({
    title: '选择标签',
    mode: 'multiple',
    value: selected,
    catalog: { items: state.availableTags },
    onConfirm: (tags) => {
      applyTagSelection(target, normalizeTagIds((tags || []).map((tag) => tag.tag_id)));
    },
    onClear: () => {
      applyTagSelection(target, []);
    },
  });
}

function normalizeOtherMaxLength(value, fallback = 80) {
  if (value === '' || value === null || value === undefined) return fallback;
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function createOption(option = {}, index = 0) {
  return {
    id: option.id ?? null,
    local_key: option.local_key || nextLocalKey('option'),
    option_text: option.option_text || '',
    score: option.score ?? 0,

    tag_codes: normalizeTagIds(option.tag_codes || []),
    is_other: Boolean(option.is_other),
    other_placeholder: option.other_placeholder || '',
    other_max_length: normalizeOtherMaxLength(option.other_max_length),
    sort_order: option.sort_order ?? (index + 1),
  };
}

function createQuestion(type = 'single_choice', question = {}, index = 0) {
  const normalizedType = question.type || type;
  return {
    id: question.id ?? null,
    local_key: question.local_key || nextLocalKey('question'),
    type: normalizedType,
    title: question.title || '',
    placeholder_text: question.placeholder_text || '',

    sidebar_profile_field: normalizeSidebarProfileField(question.sidebar_profile_field),
    assessment_template_id: question.assessment_template_id || '',
    assessment_template_name: question.assessment_template_name || '',
    required: Boolean(question.required),
    sort_order: question.sort_order ?? (index + 1),
    options: ['textarea', 'mobile'].includes(normalizedType)
      ? []
      : (question.options || []).map((option, optionIndex) => createOption(option, optionIndex)).length
        ? (question.options || []).map((option, optionIndex) => createOption(option, optionIndex))
        : [createOption({}, 0)],
  };
}

function createRule(rule = {}, index = 0) {
  return {
    id: rule.id ?? null,
    local_key: rule.local_key || nextLocalKey('rule'),
    min_score: rule.min_score ?? '',
    max_score: rule.max_score ?? '',
    tag_codes: normalizeTagIds(rule.tag_codes || []),
    sort_order: rule.sort_order ?? (index + 1),
  };
}

function normalizeAnswerDisplayMode(value) {
  return ['all_in_one', 'one_by_one'].includes(value) ? value : 'all_in_one';
}

function blankQuestionnaire() {return {id:null,public_url:'',public_path:'',name:'',title:'',description:'',answer_display_mode:'all_in_one',assessment_enabled:false,assessment_config:{},slug:'',is_disabled:false,questions:[],score_rules:[]};}

function hydrateQuestionnaire(source = null) {
 const q=source||{};
 if(q.assessment_enabled) throw new Error('测评问卷已停用，仅保留历史答卷。');
 return {...blankQuestionnaire(),id:q.id??null,enabled:q.enabled===true,status:q.status||'',version:q.version??null,public_url:q.public_url||'',public_path:q.public_path||'',name:q.name||'',title:q.title||'',description:q.description||'',answer_display_mode:normalizeAnswerDisplayMode(q.answer_display_mode),slug:q.slug||'',is_disabled:Boolean(q.is_disabled),questions:(q.questions||[]).map((question,index)=>createQuestion(question.type||'single_choice',question,index)),score_rules:(q.score_rules||[]).map((rule,index)=>createRule(rule,index))};
}

function currentQuestion() {
  if (state.selection.kind !== 'question') return null;
  return state.questionnaire.questions.find((item) => item.local_key === state.selection.key) || null;
}

function currentRule() {
  if (state.selection.kind !== 'rule') return null;
  return state.questionnaire.score_rules.find((item) => item.local_key === state.selection.key) || null;
}

function selectQuestionnaire() {
  state.ruleMode = false;
  state.selection = { kind: 'questionnaire' };
  renderWorkspace();
}

function selectQuestion(key) {
  state.ruleMode = false;
  state.selection = { kind: 'question', key };
  renderWorkspace();
}

function selectRule(key) {
  state.ruleMode = true;
  state.lastRuleKey = key;
  state.selection = { kind: 'rule', key };
  renderWorkspace();
}

function enterRuleMode() {
  state.ruleMode = true;
  if (state.questionnaire.score_rules.length) {
    const remembered = state.questionnaire.score_rules.find((item) => item.local_key === state.lastRuleKey);
    const target = remembered || state.questionnaire.score_rules[0];
    state.lastRuleKey = target.local_key;
    state.selection = { kind: 'rule', key: target.local_key };
  } else {
    state.lastRuleKey = '';
    state.selection = { kind: 'questionnaire' };
  }
  renderWorkspace();
}

function resetDraft(data = null, options = {}) {
  state.questionnaire = hydrateQuestionnaire(data);
  state.currentId = state.questionnaire.id;
  if (!options.preservePendingRepublish) state.pendingRepublish = false;
  state.ruleMode = false;
  state.lastRuleKey = state.questionnaire.score_rules[0]?.local_key || '';
  state.selection = false && !data
    ? { kind: 'question', key: state.questionnaire.questions[0]?.local_key || '' }
    : { kind: 'questionnaire' };
  rememberDraftSnapshot();
  renderWorkspace();
}

function isArchivedQuestionnaire(questionnaire = state.questionnaire) {
  return String(questionnaire?.status || '').toLowerCase() === 'archived';
}

function applyArchivedQuestionnaireReadOnlyState() {
  if (!isArchivedQuestionnaire()) return;
  if (editorPageSubtitleEl) editorPageSubtitleEl.textContent = '问卷已归档，定义仅供历史查看。';
  const controls = document.querySelectorAll([
    '#inspector-body input',
    '#inspector-body textarea',
    '#inspector-body select',
    '#inspector-body button',
    '#preview-head button',
    '#preview-questions button',
    '#preview-rules-wrap button',
    '.phone-stage input',
    '.phone-stage textarea',
    '.phone-stage select',
    '.phone-stage button',
    '#save-btn',
    '#reset-btn',
    '#add-single',
    '#add-multi',
    '#add-textarea',
    '#add-mobile',
    '#add-rule',
    '#open-assessment-settings',
  ].join(','));
  controls.forEach((control) => {
    control.disabled = true;
    control.setAttribute('aria-disabled', 'true');
  });
}

function serializePayload(options = {}) {
  return {
    name: state.questionnaire.name,
    title: state.questionnaire.title,
    description: state.questionnaire.description,
    answer_display_mode: normalizeAnswerDisplayMode(state.questionnaire.answer_display_mode),
    assessment_enabled: false,
    assessment_config: {},
    slug: state.questionnaire.slug,
    is_disabled: state.questionnaire.is_disabled,
    questions: state.questionnaire.questions.map((question, index) => {
      const payload = {
        type: question.type,
        title: question.title,

        sidebar_profile_field: normalizeSidebarProfileField(question.sidebar_profile_field),
        required: Boolean(question.required),
        // V1 编辑器内部用 1-based 展示顺序；V2 Survey 写契约要求严格 0-based 连续序号。
        sort_order: index,
      };
      if (['textarea', 'mobile'].includes(question.type)) {
        payload.placeholder_text = question.placeholder_text || '';
      }
      if (!['textarea', 'mobile'].includes(question.type)) {
        payload.options = question.options.map((option, optionIndex) => ({
          option_text: option.option_text,
          score: Number(option.score || 0),

          tag_codes: normalizeTagIds(option.tag_codes),
          is_other: Boolean(option.is_other),
          other_placeholder: option.other_placeholder || '',
          other_max_length: normalizeOtherMaxLength(option.other_max_length),
          sort_order: optionIndex,
        }));
      }
      return payload;
    }),
    score_rules: state.questionnaire.score_rules.map((rule, index) => ({
      min_score: rule.min_score,
      max_score: rule.max_score,
      tag_codes: normalizeTagIds(rule.tag_codes),
      sort_order: Number(rule.sort_order || (index + 1)),
    })),
  };
}

function serializeDraftSnapshot() {
  return JSON.stringify(serializePayload({ validateCompletion: false }));
}

function isDraftDirty() {
  return Boolean(state.questionnaire) && state.initialSnapshot !== serializeDraftSnapshot();
}

function updateDraftIndicator() {
  draftIndicatorEl.classList.toggle('hidden', !isDraftDirty());
}

function rememberDraftSnapshot() {
  state.initialSnapshot = state.questionnaire ? serializeDraftSnapshot() : '';
  state.persistedIsDisabled = Boolean(state.currentId && state.questionnaire && state.questionnaire.is_disabled);
  state.persistedPublishedAndEnabled = Boolean(
    state.currentId
      && state.questionnaire
      && state.questionnaire.enabled === true
      && ['active', 'published'].includes(String(state.questionnaire.status || '').toLowerCase()),
  );
  updateDraftIndicator();
}

function confirmDiscardChanges() {
  if (!isDraftDirty()) return true;
  return window.confirm('当前有未保存修改，确认放弃并继续吗？');
}

function normalizeDateValue(value) {
  const raw = String(value || '').trim();
  if (!raw) return 0;
  const normalized = raw.includes('T') ? raw : raw.replace(' ', 'T');
  const timestamp = new Date(normalized).getTime();
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

function formatDateTime(value) {
  const raw = String(value || '').trim();
  if (!raw) return '-';
  const normalized = raw.includes('T') ? raw : raw.replace(' ', 'T');
  const date = new Date(normalized);
  if (Number.isNaN(date.getTime())) return raw;
  const pad = (number) => String(number).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function filteredQuestionnaires() {
  const keyword = state.listSearch.trim().toLowerCase();
  return [...state.list]
    .filter((item) => {
      if (state.statusFilter === 'enabled' && item.is_disabled) return false;
      if (state.statusFilter === 'disabled' && !item.is_disabled) return false;
      if (!keyword) return true;
      return String(item.name || '').toLowerCase().includes(keyword);
    })
    .sort((left, right) => {
      const statusDiff = Number(Boolean(left.is_disabled)) - Number(Boolean(right.is_disabled));
      if (statusDiff !== 0) return statusDiff;
      return normalizeDateValue(right.created_at) - normalizeDateValue(left.created_at);
    });
}

async function loadAvailableTags() {
  try {
    const data = await listEditorTags();
    state.availableTags = data.items || [];
    state.availableTagMap = new Map(state.availableTags.map((item) => [item.tag_id, item]));
    if (data.degraded || !state.availableTags.length) {
      tagCatalogMessageEl.textContent = data.page_error || '当前未获取到企微标签，可稍后重试';
      tagCatalogMessageEl.className = 'inline-alert warning';
    } else {
      tagCatalogMessageEl.textContent = '';
      tagCatalogMessageEl.className = 'inline-alert hidden';
    }
  } catch (error) {
    state.availableTags = [];
    state.availableTagMap = new Map();
    tagCatalogMessageEl.textContent = '企微标签加载失败，可稍后重试';
    tagCatalogMessageEl.className = 'inline-alert error';
  }
  renderInspector();
}

async function loadList() {
  state.loadingList = true;
  if (listEl) {
    listEl.innerHTML = '<div class="empty-state">问卷列表加载中...</div>';
  }
  try {
    const data = await listEditorQuestionnaires();
    state.list = data.questionnaires || [];
    if (listEl) {
      renderList();
    }
  } catch (error) {
    state.list = [];
    if (listEl) {
      listEl.innerHTML = `<div class="empty-state">${escapeHtml(error.message || '问卷列表加载失败，请稍后重试')}</div>`;
    }
  } finally {
    state.loadingList = false;
  }
}

async function loadQuestionnaire(questionnaireId, options = {}) {
  if (!options.skipConfirm && !confirmDiscardChanges()) return;
  const data = await getEditorQuestionnaire(questionnaireId);
  resetDraft(data.questionnaire);
  renderList();
}

async function toggleQuestionnaire(item) {
  if (isArchivedQuestionnaire(item)) throw new Error('问卷已归档，定义仅供历史查看');
  await setEditorQuestionnaireDisabled(item.id, !item.is_disabled);
  showToast(item.is_disabled ? '问卷已启用' : '问卷已停用');
  await loadList();
  if (state.currentId === item.id) {
    await loadQuestionnaire(item.id, { skipConfirm: true });
  }
}

async function deleteQuestionnaireItem(item) {
  const expectedVersion = Number(item?.version ?? state.questionnaire?.version);
  if (!window.confirm('删除后，问卷会从正常列表和新的填写入口移除；历史答卷、结果和导出记录会保留。确认删除该问卷吗？')) return;
  await deleteEditorQuestionnaire(item.id, expectedVersion);
  if (state.currentId === item.id) {
    window.location.assign(editorConfig.backHref);
    return;
  }
  showToast('问卷已删除');
  await loadList();
}

async function duplicateQuestionnaire(item) {
  if (!item?.id) return;
  if (isArchivedQuestionnaire(item)) throw new Error('问卷已归档，定义仅供历史查看');
  if (!confirmDiscardChanges()) return;
  if (!window.confirm(`复制问卷“${item.name || item.title || '未命名问卷'}”？复制后会生成一份默认停用的新问卷。`)) return;
  const data = await duplicateEditorQuestionnaire(item.id);
  const copied = data.questionnaire || {};
  await loadList();
  if (copied.id) {
    await loadQuestionnaire(copied.id, { skipConfirm: true });
  }
  showToast('问卷已复制，默认停用');
}

async function copyText(text, successMessage = '链接已复制') {
  if (!text) {
    showToast('当前还没有可复制的链接', true);
    return;
  }
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      window.prompt('请复制以下链接', text);
    }
    showToast(successMessage);
  } catch (error) {
    window.prompt('请手动复制以下链接', text);
  }
}

function downloadFilenameFromResponse(response, fallback) {
  const disposition = response.headers.get('Content-Disposition') || '';
  const utf8Match = disposition.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Match) return decodeURIComponent(utf8Match[1]);
  const asciiMatch = disposition.match(/filename="?([^";]+)"?/i);
  return asciiMatch ? asciiMatch[1] : fallback;
}

async function downloadQuestionnaireData(questionnaireId) {
  if (!questionnaireId) {
    showToast('保存问卷后才能下载数据', true);
    return;
  }
  const response = await apiRequest(editorQuestionnaireExportUrl(questionnaireId));
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(humanizeErrorMessage(extractErrorMessage(data), '下载失败，请稍后重试'));
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = downloadFilenameFromResponse(response, `questionnaire-${questionnaireId}-submissions.csv`);
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
  showToast('下载已开始');
}

function renderManagementTable() {
  if (!listEl) return;
  if (!state.list.length) {
    listEl.innerHTML = '<div class="empty-state">还没有问卷，点击「创建新问卷」开始搭建。</div>';
    return;
  }
  const items = filteredQuestionnaires();
  if (!items.length) {
    listEl.innerHTML = '<div class="empty-state">没有符合当前筛选条件的问卷。</div>';
    return;
  }
  listEl.innerHTML = `
    <table class="management-table">
      <thead>
        <tr>
          <th>问卷名称</th>
          <th>问卷创建时间</th>
          <th>提交数</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        ${items.map((item) => `
          <tr class="management-row${state.currentId === item.id ? ' active' : ''}${item.is_disabled ? ' disabled' : ''}" data-id="${escapeHtml(String(item.id))}">
            <td>
              <div class="name-line">
                <span class="name-main">${escapeHtml(item.name || '未命名问卷')}</span>

                <span class="status-badge${item.is_disabled ? ' disabled' : ''}">${item.is_disabled ? '已停用' : '启用中'}</span>
              </div>
              ${item.title ? `<div class="name-sub">${escapeHtml(item.title)}</div>` : ''}
            </td>
            <td><span class="table-text">${escapeHtml(formatDateTime(item.created_at))}</span></td>
            <td><span class="table-number">${escapeHtml(String(item.submission_count || 0))}</span></td>
            <td>
              <div class="row-actions">
                <button type="button" class="mini-btn edit" data-action="edit">编辑</button>
                <button type="button" class="mini-btn duplicate" data-action="duplicate">复制</button>
                <button type="button" class="mini-btn toggle" data-action="toggle">${item.is_disabled ? '启用' : '停用'}</button>
                <button type="button" class="mini-btn share" data-action="share">分享</button>
                <button type="button" class="mini-btn export" data-action="export">下载数据</button>
              </div>
            </td>
          </tr>
        `).join('')}
      </tbody>
    </table>
  `;
  listEl.querySelectorAll('.management-row').forEach((row) => {
    const item = state.list.find((entry) => String(entry.id) === row.dataset.id);
    if (!item) return;
    row.querySelector('[data-action="edit"]').addEventListener('click', () => {
      loadQuestionnaire(item.id).catch((error) => showToast(error.message || '问卷加载失败，请稍后重试', true));
    });
    row.querySelector('[data-action="toggle"]').addEventListener('click', () => {
      toggleQuestionnaire(item).catch((error) => showToast(error.message || '问卷状态更新失败，请稍后重试', true));
    });
    row.querySelector('[data-action="duplicate"]').addEventListener('click', () => {
      duplicateQuestionnaire(item).catch((error) => showToast(error.message || '问卷复制失败，请稍后重试', true));
    });
    row.querySelector('[data-action="share"]').addEventListener('click', () => {
      copyText(buildPublicUrl(item), '分享链接已复制');
    });
    row.querySelector('[data-action="export"]').addEventListener('click', () => {
      downloadQuestionnaireData(item.id).catch((error) => showToast(error.message || '下载失败，请稍后重试', true));
    });
  });
}

function renderList() {
  if (!listEl) return;
  renderManagementTable();
}

function renderEditorSecondaryActions() {
  if (!editorSecondaryActionsEl) return;

  editorSecondaryActionsEl.innerHTML = state.currentId ? `
      ${isArchivedQuestionnaire() ? '' : '<button id="editor-duplicate-btn" type="button" class="btn ghost">复制问卷</button>'}
      <button id="editor-export-btn" type="button" class="btn ghost">下载数据</button>
    ` : '';
  const duplicateBtn = document.getElementById('editor-duplicate-btn');
  const exportBtn = document.getElementById('editor-export-btn');
  duplicateBtn?.addEventListener('click', () => {
    duplicateQuestionnaire({ ...(state.questionnaire || {}), id: state.currentId }).catch((error) => showToast(error.message || '问卷复制失败，请稍后重试', true));
  });
  exportBtn?.addEventListener('click', () => {
    downloadQuestionnaireData(state.currentId).catch((error) => showToast(error.message || '下载失败，请稍后重试', true));
  });
}

function renderTopbar() {
  const title = questionnaireDisplayName(state.questionnaire, state.currentId ? '编辑问卷' : '新建问卷');
  const pageKind = state.currentId ? '编辑问卷' : ('新建问卷');
  topbarTitleEl.textContent = title;
  const topbarSubtitleEl = document.getElementById('topbar-subtitle');
  if (topbarSubtitleEl) {
    topbarSubtitleEl.textContent = isArchivedQuestionnaire()
      ? '问卷已归档，定义仅供历史查看。'
      : ('');
  }
  if (editorPageTitleEl) {
    editorPageTitleEl.textContent = pageKind;
  }
  document.title = `${pageKind} · ${title || '问卷编辑'}`;
  renderEditorSecondaryActions();
  updateDraftIndicator();
}

function renderPreview() {
  const questionnaire = state.questionnaire;
  const isQuestionnaireSelected = state.selection.kind === 'questionnaire';
  previewHeadEl.className = `preview-head${isQuestionnaireSelected ? ' active' : ''}`;
  {
    previewHeadEl.innerHTML = `
      <div class="question-type">问卷设置</div>
      <h2>${escapeHtml(questionnaire.title || '问卷标题')}</h2>
      <p>${escapeHtml(questionnaire.description || '在右侧编辑问卷信息')}</p>
    `;
  }
  previewHeadEl.onclick = () => selectQuestionnaire();

  previewQuestionsEl.innerHTML = '';
  if (!questionnaire.questions.length) {
    previewQuestionsEl.innerHTML = '<div class="empty-state">从左侧添加题目</div>';
  } else {
    const renderedTemplateIds = new Set();
    questionnaire.questions.forEach((question, questionIndex) => {

      const card = document.createElement('article');
      card.className = `preview-question${state.selection.kind === 'question' && state.selection.key === question.local_key ? ' active' : ''}`;
      {
        const optionPreview = question.type === 'textarea'
          ? `<div class="option-pill">${escapeHtml(question.placeholder_text || '多行文本输入')}</div>`
          : question.type === 'mobile'
            ? `<div class="option-pill">${escapeHtml(question.placeholder_text || '请输入手机号')}</div>`
            : (question.options || []).map((option) => `
                <div class="option-pill">
                  ${escapeHtml(option.option_text || '选项')}
                  ${''}
                </div>
              `).join('');
        card.innerHTML = `
          <span class="question-type">
            ${escapeHtml(formatQuestionType(question.type))}${question.required ? ' · 必填' : ''}${''}
          </span>
          ${sidebarProfileChipHtml(question)}
          <h4>${escapeHtml(question.title || '题目标题')}</h4>
          <div class="preview-options">${optionPreview}</div>
        `;
      }
      card.addEventListener('click', () => selectQuestion(question.local_key));
      previewQuestionsEl.appendChild(card);
    });
  }

  previewRulesWrapEl.classList.add('hidden');
  previewRulesWrapEl.innerHTML = '';
  updateDraftIndicator();
}

function mountTagPicker(host, selectedTagIds, onChange, target) {
  const normalizedSelected = normalizeTagIds(selectedTagIds);
  host.innerHTML = `
    <div class="tag-inline">
      <button type="button" class="btn ghost open-tag-modal">选择标签</button>
      <div class="tag-badges">${buildTagBadges(normalizedSelected)}</div>
    </div>
  `;
  const openButton = host.querySelector('.open-tag-modal');
  openButton.addEventListener('click', () => openTagModal(target, onChange.currentValue ? onChange.currentValue() : normalizedSelected));
}

function renderQuestionnaireInspector() {
  inspectorTitleEl.textContent = '问卷设置';
  const archived = isArchivedQuestionnaire();
  inspectorSubtitleEl.textContent = archived ? '问卷已归档，定义仅供历史查看。' : '编辑当前问卷的基础信息。';
  const deleteSection = state.currentId && !archived ? `
    <section class="config-group danger-zone">
      <button
        id="delete-questionnaire-btn"
        type="button"
        class="link-btn danger"
      >
        删除此问卷
      </button>
      <p>删除会停止新的填写入口，历史答卷和结果保留。</p>
    </section>
  ` : '';
  inspectorBodyEl.innerHTML = `
    <section class="config-group">
      <label class="field">问卷名称
        <input id="field-name" type="text" value="${escapeHtml(state.questionnaire.name)}">
      </label>
      <label class="field">问卷标题
        <input id="field-title" type="text" value="${escapeHtml(state.questionnaire.title)}">
      </label>
      <label class="field">问卷说明
        <textarea id="field-description">${escapeHtml(state.questionnaire.description)}</textarea>
      </label>
      <label class="field">答题展示方式
        <select id="field-answer-display-mode">
          <option value="all_in_one" ${normalizeAnswerDisplayMode(state.questionnaire.answer_display_mode) === 'all_in_one' ? 'selected' : ''}>整页答题</option>
          <option value="one_by_one" ${normalizeAnswerDisplayMode(state.questionnaire.answer_display_mode) === 'one_by_one' ? 'selected' : ''}>一题一页</option>
        </select>
      </label>
      <div class="field-grid compact">
        <label class="field">分享标识
          <input id="field-slug" type="text" value="${escapeHtml(state.questionnaire.slug)}">
        </label>
        <label class="field"><span style="display:block;margin-bottom:7px;">问卷状态</span>
          <label class="field" style="margin-bottom:0;font-weight:600;">
            <input id="field-is-disabled" type="checkbox" ${state.questionnaire.is_disabled ? 'checked' : ''}> 停用问卷
          </label>
        </label>
      </div>
    </section>
    ${deleteSection}
  `;
  inspectorBodyEl.querySelector('#field-name').addEventListener('input', (event) => {
    state.questionnaire.name = event.target.value;
    renderTopbar();
  });
  inspectorBodyEl.querySelector('#field-title').addEventListener('input', (event) => {
    state.questionnaire.title = event.target.value;
    renderTopbar();
    renderPreview();
  });
  inspectorBodyEl.querySelector('#field-description').addEventListener('input', (event) => {
    state.questionnaire.description = event.target.value;
    renderPreview();
  });
  inspectorBodyEl.querySelector('#field-answer-display-mode').addEventListener('change', (event) => {
    state.questionnaire.answer_display_mode = normalizeAnswerDisplayMode(event.target.value);
    updateDraftIndicator();
  });
  inspectorBodyEl.querySelector('#field-slug').addEventListener('input', (event) => {
    state.questionnaire.slug = event.target.value;
    renderTopbar();
  });
  inspectorBodyEl.querySelector('#field-is-disabled').addEventListener('change', (event) => {
    state.questionnaire.is_disabled = event.target.checked;
    updateDraftIndicator();
  });
  const deleteBtn = inspectorBodyEl.querySelector('#delete-questionnaire-btn');
  if (deleteBtn) {
    deleteBtn.addEventListener('click', () => {
      deleteQuestionnaireItem({
        id: state.currentId,
        name: questionnaireDisplayName(state.questionnaire),
        version: state.questionnaire.version,
      }).catch((error) => showToast(error.message || '删除失败，请稍后重试', true));
    });
  }
}

function renderQuestionInspector(question) {
  inspectorTitleEl.textContent = formatQuestionType(question.type);
  inspectorSubtitleEl.textContent = '编辑题目内容、选项与分值。';
  const optionsHtml = ['textarea', 'mobile'].includes(question.type)
    ? ''
    : (question.options || []).map((option, index) => {
        const otherMaxLength = normalizeOtherMaxLength(option.other_max_length);
        return `
          <div class="option-editor" data-option-key="${escapeHtml(option.local_key)}" draggable="true">
          <div class="option-editor-head">
            <span class="mini-label" title="拖拽调整顺序">⋮⋮ 选项 ${index + 1}</span>
            <button type="button" class="link-btn danger remove-option-btn" data-option-key="${escapeHtml(option.local_key)}">删除</button>
          </div>
          <div class="field-grid triple">
            <label class="field">选项文案
              <input data-option-field="option_text" data-option-key="${escapeHtml(option.local_key)}" type="text" value="${escapeHtml(option.option_text)}">
            </label>
            <label class="field">分值
              ${`<input data-option-field="score" data-option-key="${escapeHtml(option.local_key)}" type="text" value="${escapeHtml(String(option.score ?? 0))}">`}
            </label>

          </div>
          <label class="field" style="margin-bottom:8px;">
            <input
              data-option-field="is_other"
              data-option-key="${escapeHtml(option.local_key)}"
              type="checkbox"
              ${option.is_other ? 'checked' : ''}
            > 设为其它选项
          </label>
          <div class="option-other-config${option.is_other ? '' : ' hidden'}">
            <label class="field">输入框提示文案
              <input
                data-option-field="other_placeholder"
                data-option-key="${escapeHtml(option.local_key)}"
                type="text"
                value="${escapeHtml(option.other_placeholder || '')}"
                placeholder="请填写其它内容"
              >
            </label>
            <label class="field">最多输入字数
              <input
                data-option-field="other_max_length"
                data-option-key="${escapeHtml(option.local_key)}"
                type="number"
                min="1"
                max="200"
                step="1"
                value="${escapeHtml(String(otherMaxLength))}"
              >
            </label>
          </div>
          <div class="tag-picker-host" data-option-tag-host="${escapeHtml(option.local_key)}"></div>
        </div>
        `;
      }).join('');
  inspectorBodyEl.innerHTML = `
    <section class="config-group">
      <div class="config-head">
        <div><h3>题目</h3></div>
        <button id="remove-question-btn" type="button" class="link-btn danger">删除</button>
      </div>
      <label class="field">题型
        <select id="question-type">
          ${'<option value="single_choice">单选题</option><option value="multi_choice">多选题</option><option value="textarea">文本题</option><option value="mobile">手机号题</option>'}
        </select>
      </label>
      <label class="field">${'题目标题'}
        ${`<input id="question-title" type="text" value="${escapeHtml(question.title)}">`}
      </label>

      <label class="field${['textarea', 'mobile'].includes(question.type) ? '' : ' hidden'}" id="question-placeholder-field">提示文字
        <input
          id="question-placeholder-text"
          type="text"
          value="${escapeHtml(question.placeholder_text || '')}"
          placeholder="${escapeHtml(question.type === 'mobile' ? '例如：请输入手机号' : '例如：写下你的目标')}"
        >
      </label>
      <label class="field" style="margin-bottom:0;">
        <input id="question-required" type="checkbox" ${question.required ? 'checked' : ''}> 必填
      </label>
    </section>
    <section class="config-group">
      <div class="config-head">
        <div><h3>侧边栏核心画像映射</h3></div>
      </div>
      <label class="field">同步字段
        <select id="question-sidebar-profile-field">
          ${sidebarProfileFieldOptionsHtml(question.sidebar_profile_field)}
        </select>
      </label>
      <p>用户填什么就同步什么；多个题目映射同一字段时，后填写覆盖前填写。</p>
    </section>
    <section class="config-group${['textarea', 'mobile'].includes(question.type) ? ' hidden' : ''}" id="question-options-group">
      <div class="config-head">
        <div><h3>选项</h3></div>
        <button id="add-option-btn" type="button" class="btn secondary">添加选项</button>
      </div>
      ${optionsHtml || '<div class="empty-state">点击"添加选项"开始配置</div>'}
    </section>
  `;
  inspectorBodyEl.querySelector('#question-type').value = question.type;

  inspectorBodyEl.querySelector('#question-title').addEventListener('input', (event) => {
    question.title = event.target.value;
    renderPreview();
  });
  const questionPlaceholderInput = inspectorBodyEl.querySelector('#question-placeholder-text');
  if (questionPlaceholderInput) {
    questionPlaceholderInput.addEventListener('input', (event) => {
      question.placeholder_text = event.target.value;
      renderPreview();
    });
  }
  inspectorBodyEl.querySelector('#question-required').addEventListener('change', (event) => {
    question.required = event.target.checked;
    renderPreview();
  });
  inspectorBodyEl.querySelector('#question-sidebar-profile-field').addEventListener('change', (event) => {
    question.sidebar_profile_field = normalizeSidebarProfileField(event.target.value);
    renderPreview();
  });
  inspectorBodyEl.querySelector('#question-type').addEventListener('change', (event) => {
    question.type = event.target.value;
    if (['textarea', 'mobile'].includes(question.type)) {
      question.options = [];
    } else if (!question.options.length) {
      question.options = [createOption({}, 0)];
    }
    renderWorkspace();
  });
  inspectorBodyEl.querySelector('#remove-question-btn').addEventListener('click', () => {
    state.questionnaire.questions = state.questionnaire.questions.filter((item) => item.local_key !== question.local_key);
    state.selection = { kind: 'questionnaire' };
    renderWorkspace();
  });

  const addOptionBtn = inspectorBodyEl.querySelector('#add-option-btn');
  if (addOptionBtn) {
    addOptionBtn.addEventListener('click', () => {
      question.options.push(createOption({}, question.options.length));
      renderWorkspace();
    });
  }

  inspectorBodyEl.querySelectorAll('[data-option-field]').forEach((input) => {
    const updateOptionField = (event) => {
      const option = question.options.find((item) => item.local_key === event.target.dataset.optionKey);
      if (!option) return;
      const field = event.target.dataset.optionField;
      if (field === 'is_other') {
        option.is_other = event.target.checked;
        if (option.is_other) {
          question.options.forEach((item) => {
            if (item.local_key !== option.local_key) item.is_other = false;
          });
          if (!option.option_text) option.option_text = '其它';
          if (!option.other_max_length) option.other_max_length = 80;
        }
        renderWorkspace();
        return;
      }
      option[field] = field === 'other_max_length'
        ? (event.target.value === '' ? '' : Number(event.target.value))
        : event.target.value;
      renderPreview();
    };
    input.addEventListener('input', updateOptionField);
    input.addEventListener('change', updateOptionField);
  });
  inspectorBodyEl.querySelectorAll('.remove-option-btn').forEach((button) => {
    button.addEventListener('click', () => {
      question.options = question.options.filter((item) => item.local_key !== button.dataset.optionKey);
      if (!question.options.length && !['textarea', 'mobile'].includes(question.type)) {
        question.options = [createOption({}, 0)];
      }
      renderWorkspace();
    });
  });
  question.options.forEach((option) => {
    const host = inspectorBodyEl.querySelector(`[data-option-tag-host="${option.local_key}"]`);
    if (!host) return;
    const apply = (tagIds) => {
      option.tag_codes = tagIds;
    };
    apply.currentValue = () => option.tag_codes;
    mountTagPicker(host, option.tag_codes, apply, {
      type: 'option',
      questionKey: question.local_key,
      optionKey: option.local_key,
    });
  });

  let dragKey = null;
  inspectorBodyEl.querySelectorAll('.option-editor[draggable="true"]').forEach((node) => {
    node.addEventListener('dragstart', (event) => {
      dragKey = node.dataset.optionKey;
      node.classList.add('is-dragging');
      if (event.dataTransfer) {
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData('text/plain', dragKey || '');
      }
    });
    node.addEventListener('dragend', () => {
      dragKey = null;
      node.classList.remove('is-dragging');
    });
    node.addEventListener('dragover', (event) => {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
      node.classList.add('is-drop-target');
    });
    node.addEventListener('dragleave', () => {
      node.classList.remove('is-drop-target');
    });
    node.addEventListener('drop', (event) => {
      event.preventDefault();
      node.classList.remove('is-drop-target');
      const sourceKey = dragKey || (event.dataTransfer && event.dataTransfer.getData('text/plain'));
      const targetKey = node.dataset.optionKey;
      if (!sourceKey || !targetKey || sourceKey === targetKey) return;
      const items = [...question.options];
      const fromIdx = items.findIndex((item) => item.local_key === sourceKey);
      const toIdx = items.findIndex((item) => item.local_key === targetKey);
      if (fromIdx < 0 || toIdx < 0) return;
      const [moved] = items.splice(fromIdx, 1);
      items.splice(toIdx, 0, moved);
      question.options = items;
      renderWorkspace();
      updateDraftIndicator();
    });
  });
}

function renderRuleInspector(rule) {
  inspectorTitleEl.textContent = '分数规则';
  inspectorSubtitleEl.textContent = '按总分区间给提交者打标签。';
  const ruleListHtml = state.questionnaire.score_rules.length
    ? `
      <div class="rule-nav-list">
        ${state.questionnaire.score_rules.map((item, index) => `
          <button type="button" class="rule-nav-item${rule && item.local_key === rule.local_key ? ' active' : ''}" data-rule-key="${escapeHtml(item.local_key)}">
            <strong>规则 ${index + 1}</strong>
            <span>${escapeHtml(String(item.min_score ?? ''))} - ${escapeHtml(String(item.max_score ?? ''))}</span>
            <span>${normalizeTagIds(item.tag_codes).length ? normalizeTagIds(item.tag_codes).map((tagId) => formatTagLabel(ensureTagKnown(tagId))).join(' / ') : '未选择标签'}</span>
          </button>
        `).join('')}
      </div>
    `
    : `
      <div class="empty-state">
        <strong style="display:block;font-size:18px;margin-bottom:6px;">当前还没有分数规则</strong>
        <div class="muted" style="margin-bottom:14px;">点击下方按钮新增规则</div>
        <button id="empty-add-rule-btn" type="button" class="btn secondary">新增规则</button>
      </div>
    `;
  const editorHtml = rule ? `
    <section class="config-group">
      <div class="config-head">
        <div><h3>规则设置</h3></div>
        <button id="remove-rule-btn" type="button" class="link-btn danger">删除</button>
      </div>
      <div class="field-grid compact">
        <label class="field">最低分
          <input id="rule-min-score" type="text" value="${escapeHtml(String(rule.min_score ?? ''))}">
        </label>
        <label class="field">最高分
          <input id="rule-max-score" type="text" value="${escapeHtml(String(rule.max_score ?? ''))}">
        </label>
      </div>
      <div id="rule-tag-host"></div>
    </section>
  ` : `
    <section class="config-group">
      <div class="config-head"><div><h3>规则设置</h3></div></div>
      <div class="helper-note">从上方选择一条规则，或新增规则。</div>
    </section>
  `;
  inspectorBodyEl.innerHTML = `
    <section class="config-group">
      <div class="config-head">
        <div><h3>规则列表</h3></div>
        <button id="rule-list-add-btn" type="button" class="btn secondary">新增</button>
      </div>
      ${ruleListHtml}
    </section>
    ${editorHtml}
  `;
  inspectorBodyEl.querySelector('#rule-list-add-btn').addEventListener('click', () => addRule());
  inspectorBodyEl.querySelectorAll('[data-rule-key]').forEach((button) => {
    button.addEventListener('click', () => selectRule(button.dataset.ruleKey));
  });
  const emptyAddRuleBtn = inspectorBodyEl.querySelector('#empty-add-rule-btn');
  if (emptyAddRuleBtn) {
    emptyAddRuleBtn.addEventListener('click', () => addRule());
  }
  if (!rule) {
    return;
  }
  inspectorBodyEl.querySelector('#rule-min-score').addEventListener('input', (event) => {
    rule.min_score = event.target.value;
    renderPreview();
  });
  inspectorBodyEl.querySelector('#rule-max-score').addEventListener('input', (event) => {
    rule.max_score = event.target.value;
    renderPreview();
  });
  inspectorBodyEl.querySelector('#remove-rule-btn').addEventListener('click', () => {
    const currentIndex = state.questionnaire.score_rules.findIndex((item) => item.local_key === rule.local_key);
    state.questionnaire.score_rules = state.questionnaire.score_rules.filter((item) => item.local_key !== rule.local_key);
    state.ruleMode = true;
    if (state.questionnaire.score_rules.length) {
      const nextIndex = Math.min(currentIndex, state.questionnaire.score_rules.length - 1);
      state.lastRuleKey = state.questionnaire.score_rules[nextIndex].local_key;
      state.selection = { kind: 'rule', key: state.questionnaire.score_rules[nextIndex].local_key };
    } else {
      state.lastRuleKey = '';
      state.selection = { kind: 'questionnaire' };
    }
    renderWorkspace();
  });
  const apply = (tagIds) => {
    rule.tag_codes = tagIds;
    renderPreview();
  };
  apply.currentValue = () => rule.tag_codes;
  mountTagPicker(inspectorBodyEl.querySelector('#rule-tag-host'), rule.tag_codes, apply, {
    type: 'rule',
    ruleKey: rule.local_key,
  });
}

const ASSESSMENT_TEMPLATE_STEPS = [
  { key: 'basic', label: '基础信息' },
  { key: 'dimensions', label: '设置维度' },
  { key: 'results', label: '结果配置' },
  { key: 'preview', label: 'H5 预览 / 发布' },
];

function renderInspector() {

  if (state.ruleMode) {
    const rule = currentRule();
    renderRuleInspector(rule);
    return;
  }
  if (state.selection.kind === 'questionnaire') {
    renderQuestionnaireInspector();
    return;
  }


  if (state.selection.kind === 'question') {
    const question = currentQuestion();
    if (!question) {
      state.selection = { kind: 'questionnaire' };
      renderInspector();
      return;
    }
    renderQuestionInspector(question);
    return;
  }
  renderQuestionnaireInspector();
}

function renderWorkspace() {

  renderTopbar();
  renderPreview();
  renderInspector();
  renderList();
  applyArchivedQuestionnaireReadOnlyState();
}

function addQuestion(type) {
  const questionSeed = {};
  const question = createQuestion(type, questionSeed, state.questionnaire.questions.length);
  state.questionnaire.questions.push(question);
  state.ruleMode = false;
  state.selection = { kind: 'question', key: question.local_key };
  renderWorkspace();
}

function addRule() {
  const rule = createRule({}, state.questionnaire.score_rules.length);
  state.questionnaire.score_rules.push(rule);
  state.ruleMode = true;
  state.lastRuleKey = rule.local_key;
  state.selection = { kind: 'rule', key: rule.local_key };
  renderWorkspace();
}

function validateOtherOptionsBeforeSave() {
  (state.questionnaire?.questions || []).forEach((question, questionIndex) => {
    if (['textarea', 'mobile'].includes(question.type)) return;
    const title = question.title || `题目 ${questionIndex + 1}`;
    const otherOptions = (question.options || []).filter((option) => Boolean(option.is_other));
    if (otherOptions.length > 1) {
      throw new Error(`题目“${title}”最多只能设置一个其它选项`);
    }
    otherOptions.forEach((option) => {
      const maxLength = normalizeOtherMaxLength(option.other_max_length);
      if (!Number.isFinite(maxLength) || maxLength < 1 || maxLength > 200) {
        throw new Error(`题目“${title}”的其它选项最多输入字数必须在 1 到 200 之间`);
      }
    });
  });
}

async function saveQuestionnaire() {
  if (isArchivedQuestionnaire()) throw new Error('问卷已归档，定义仅供历史查看');

  validateOtherOptionsBeforeSave();
  const wasEditing = Boolean(state.currentId);
  const payload = serializePayload();
  // Updating an immutable published definition creates a draft. Preserve the
  // operator's published state only when the initial read confirmed it, and
  // never override an explicit request to stop the questionnaire.
  const shouldRepublish = !false && wasEditing && !payload.is_disabled
    && (state.persistedPublishedAndEnabled || state.pendingRepublish);
  const data = await saveEditorQuestionnaire(state.currentId, payload, { notifyLegacyPublish: false });
  let saved = data.questionnaire;
  if (shouldRepublish) {
    const questionnaireId = Number(saved?.id || state.currentId);
    const expectedVersion = Number(saved?.version);
    if (!Number.isSafeInteger(questionnaireId) || questionnaireId < 1 || !Number.isSafeInteger(expectedVersion) || expectedVersion < 1) {
      state.pendingRepublish = true;
      resetDraft(saved, { preservePendingRepublish: true });
      throw new Error('问卷已保存为草稿，但缺少准确版本，未执行发布');
    }
    let publishedVersion = 0;
    try {
      const published = await publishEditorQuestionnaire(questionnaireId, expectedVersion);
      const publishedQuestionnaire = published?.questionnaire;
      publishedVersion = Number(publishedQuestionnaire?.version);
      if (Number(publishedQuestionnaire?.id) !== questionnaireId || !Number.isSafeInteger(publishedVersion) || publishedVersion <= expectedVersion) {
        throw new Error('发布响应缺少准确问卷版本，未确认上线');
      }
      const readback = await getEditorQuestionnaire(questionnaireId);
      if (!(readback.questionnaire?.enabled === true
        && ['active', 'published'].includes(String(readback.questionnaire?.status || '').toLowerCase())
        && Number(readback.questionnaire?.version) === publishedVersion)) {
        throw new Error('发布回读未确认本次保存的上线版本');
      }
      saved = readback.questionnaire;
      state.pendingRepublish = false;
    } catch (error) {
      state.pendingRepublish = true;
      let readback;
      try {
        readback = await getEditorQuestionnaire(questionnaireId);
      } catch (readbackError) {
        resetDraft(saved, { preservePendingRepublish: true });
        throw new Error(`问卷已保存，发布状态未确认：${readbackError instanceof Error ? readbackError.message : '请查看当前状态'}`);
      }
      if (readback.questionnaire?.enabled === true
        && ['active', 'published'].includes(String(readback.questionnaire?.status || '').toLowerCase())
        && publishedVersion > expectedVersion
        && Number(readback.questionnaire?.version) === publishedVersion) {
        saved = readback.questionnaire;
        state.pendingRepublish = false;
      } else {
        resetDraft(readback.questionnaire, { preservePendingRepublish: true });
        throw new Error(`问卷已保存，但未确认本次版本上线；发布失败：${error instanceof Error ? error.message : '未知错误'}`);
      }
    }
  } else if (payload.is_disabled) {
    state.pendingRepublish = false;
  }
  resetDraft(saved, { preservePendingRepublish: true });
  state.editorMode = state.currentId ? 'edit' : 'new';
  if (!wasEditing && state.currentId) {
    window.history.replaceState({}, '', `questionnaireDetail.html?id=${state.currentId}`);
  }
  await loadList();
  showToast(wasEditing ? '问卷已更新' : '问卷已创建');
}

if (backLinkEl) {
  backLinkEl.addEventListener('click', (event) => {
    if (confirmDiscardChanges()) return;
    event.preventDefault();
  });
}
document.getElementById('reset-btn').addEventListener('click', () => {

  if (!confirmDiscardChanges()) return;
  if (state.currentId) {
    loadQuestionnaire(state.currentId, { skipConfirm: true }).catch((error) => showToast(error.message || '重置失败，请稍后重试', true));
    return;
  }
  resetDraft();
});
document.getElementById('save-btn').addEventListener('click', () => saveQuestionnaire().catch((error) => showToast(error.message || '保存失败，请检查当前配置后重试', true)));
if (document.getElementById('reload-list-btn')) {
  document.getElementById('reload-list-btn').addEventListener('click', () => loadList());
}
if (listSearchEl) {
  listSearchEl.addEventListener('input', (event) => {
    state.listSearch = event.target.value || '';
    renderList();
  });
}
if (statusFilterEl) {
  statusFilterEl.addEventListener('change', (event) => {
    state.statusFilter = event.target.value || 'all';
    renderList();
  });
}
document.getElementById('add-single')?.addEventListener('click', () => addQuestion('single_choice'));
document.getElementById('add-multi')?.addEventListener('click', () => addQuestion('multi_choice'));
document.getElementById('add-textarea')?.addEventListener('click', () => addQuestion('textarea'));
document.getElementById('add-mobile')?.addEventListener('click', () => {

  addQuestion('mobile');
});

document.getElementById('add-rule')?.addEventListener('click', () => {

  enterRuleMode();
});
document.getElementById('drawer-close').addEventListener('click', closeDrawer);
drawerOverlayEl.addEventListener('click', (event) => {
  if (event.target === drawerOverlayEl) closeDrawer();
});

if (editorConfig.initialQuestionnaire) {
  resetDraft(editorConfig.initialQuestionnaire);
} else {
  resetDraft();
}
const bootTasks = [loadAvailableTags(), loadList()];
Promise.all(bootTasks).then(async () => {
  if (Number.isSafeInteger(requestedQuestionnaireId) && requestedQuestionnaireId > 0 && !editorConfig.initialQuestionnaire) {
    await loadQuestionnaire(requestedQuestionnaireId, { skipConfirm: true });
    return;
  }
  renderWorkspace();
}).catch((error) => {
  showToast(error.message || '页面初始化失败，请刷新后重试', true);
  renderWorkspace();
});
