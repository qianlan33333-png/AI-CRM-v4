/**
 * Mock 种子数据 —— 与设计原型内嵌数据一致。
 * MockApi 首次加载时深拷贝进 sessionStorage，之后所有写操作持久化到会话级存储，
 * 跨页面跳转状态连续。接真实后端后本文件仅作为开发环境的 fixture。
 */
import type {
  AdminDb,
  AiPlan,
  AiRecipient,
  AudiencePackage,
  ConfigCategory,
  CouponClaim,
  CycleRun,
  CycleTask,
  FunnelGridRow,
  GroupChat,
  H5Data,
  QuestionnaireOps,
  RadarEvent,
  StaffMember,
  TagGroup,
  WecomTag,
} from './types';

/* ================= 内容雷达：访问明细造数（与方向稿一致，24 条） ================= */
const RADAR_UNION = [
  'oX9q****3kFm', 'oX9q****8tQw', 'oX9q****c2Lp', 'oX9q****77Hs', 'oX9q****mK41', 'oX9q****z09P',
  'oX9q****Qw8e', 'oX9q****pL3d', 'oX9q****66Ty', 'oX9q****nB52', 'oX9q****88Ua', 'oX9q****kE19',
];
const RADAR_EXT = [
  'wmXk3ABBAA_2f9Qn', 'wmXk3ABBAA_7hTzR', 'wmXk3ABBAA_1k0pM', 'wmXk3ABBAA_9qWe2',
  'wmXk3ABBAA_4rTy8', 'wmXk3ABBAA_6yUi3', 'wmXk3ABBAA_3eWd7', 'wmXk3ABBAA_5tYu1',
];

function makeRadarEvents(): RadarEvent[] {
  const out: RadarEvent[] = [];
  for (let i = 0; i < 24; i++) {
    const d = new Date(2026, 7, 5, 9, 41 - i * 37);
    const p = (n: number): string => String(n).padStart(2, '0');
    out.push({
      unionid_masked: RADAR_UNION[i % RADAR_UNION.length],
      external_userid: RADAR_EXT[i % RADAR_EXT.length],
      created_at: `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`,
    });
  }
  return out;
}

/* ================= AI 助手：目标人员造数（与方向稿 makeRecipients 一致） ================= */
const AI_PLANS: AiPlan[] = [
  { id: 1, name: 'Agent 生成待发送计划', code: 'wmbNXyCwAAVOJWev3kbMZkabwZuB2wmA', owner: 'HuangYouCan', creator: 'HuangYouCan', updated: '2026/8/5 20:27:44', target: 1, status: 'approved' },
  { id: 2, name: 'Agent 生成待发送计划', code: 'wmbNXyCwAAaKqerymAj9qOA7FJlxhFLQ', owner: 'HuangYouCan', creator: 'HuangYouCan', updated: '2026/8/5 10:53:46', target: 1, status: 'approved' },
  { id: 3, name: 'Agent 生成待发送计划', code: 'wmbNXyCwAAaKqerymAj9qOA7FJlxhFLQ', owner: 'ZhuQingHui', creator: 'ZhuQingHui', updated: '2026/8/5 09:18:20', target: 1, status: 'approved' },
  { id: 4, name: '2026-08-04 · HuangYouCan · ABCD v3 · 19:30 · AI助手审阅', code: 'plan_20260804_1930_abcd', owner: 'HuangYouCan', creator: 'HuangYouCan', updated: '2026/8/4 18:00:51', target: 801, status: 'rejected' },
  { id: 5, name: '【已替代】2026-08-04 · HuangYouCan · ABCD v3 · 19:30 · AI助手审阅', code: 'plan_20260804_1930_abcd_v2', owner: 'HuangYouCan', creator: 'HuangYouCan', updated: '2026/8/4 17:56:04', target: 497, status: 'rejected' },
  { id: 6, name: '2026-08-03 · WangWei · 沉默唤回 · 15:00 · AI助手审阅', code: 'plan_20260803_1500_recall', owner: 'WangWei', creator: 'WangWei', updated: '2026/8/3 15:57:41', target: 1, status: 'pending_review' },
  { id: 7, name: '2026-08-05 · LinKaiYan · 共学营开课提醒 · 09:00', code: 'plan_20260805_0900_camp', owner: 'LinKaiYan', creator: 'LinKaiYan', updated: '2026/8/5 08:42:10', target: 1632, status: 'pending_review' },
  { id: 8, name: '2026-08-05 · ZhangMin · 会员到期唤回 · 20:00', code: 'plan_20260805_2000_expire', owner: 'ZhangMin', creator: 'ZhangMin', updated: '2026/8/5 11:02:33', target: 286, status: 'active' },
];

const AI_NAMES = ['林晓', '陈默', '刘靖', '王恺', '张敏', '李由', '周航', '吴桐', '赵一', '孙可', '钱多多', '冯远', '褚夏', '卫东', '蒋晴', '沈括', '韩非', '杨柳', '朱琳', '秦朗', '尤佳', '许昌', '何欢', '吕蒙'];
const AI_EXT = ['wmXk3ABBAA_2f9Qn', 'wmXk3ABBAA_7hTzR', 'wmXk3ABBAA_1k0pM', 'wmXk3ABBAA_9qWe2', 'wmXk3ABBAA_4rTy8', 'wmXk3ABBAA_6yUi3'];

function makeRecipients(plan: AiPlan): AiRecipient[] {
  const n = Math.min(plan.target, 180);
  const arr: AiRecipient[] = [];
  for (let i = 0; i < n; i++) {
    const tn = 1 + (i % 3);
    const tasks = [];
    for (let t = 0; t < tn; t++) {
      tasks.push({
        no: t + 1,
        kind: t === 0 ? '首轮触达话术' : '跟进话术 · 第' + t + '次',
        text:
          '{{客户名}} 你好，' +
          (plan.name.includes('共学营')
            ? '今晚 20:00 共学营正式开营，课程表和听课入口已发你，记得准时来～'
            : plan.name.includes('到期')
              ? '你的会员还有 3 天到期，续费可保留全部咨询记录和课程进度。'
              : '上次你咨询的课程这周有新一期开班，给你留了一个试听名额。'),
        media: t === 0 ? ['小程序卡片：课程详情页', '图片：开营海报.png'] : ['无素材，纯文本'],
        note: '',
      });
    }
    arr.push({
      id: plan.id * 1000 + i,
      name: AI_NAMES[i % AI_NAMES.length] + (i >= AI_NAMES.length ? ' ' + (Math.floor(i / AI_NAMES.length) + 1) : ''),
      external_userid: AI_EXT[i % AI_EXT.length],
      owner: plan.owner,
      updated: plan.updated,
      taskCount: tn,
      status: plan.status === 'approved' ? 'approved' : plan.status === 'active' ? (i % 5 === 4 ? 'sent' : 'approved') : 'pending',
      tasks,
    });
  }
  return arr;
}

function makeAiRcs(): Record<number, AiRecipient[]> {
  const map: Record<number, AiRecipient[]> = {};
  for (const p of AI_PLANS) map[p.id] = makeRecipients(p);
  return map;
}

/* ================= 自动化运营 · 人群包种子 ================= */
const AUDIENCE_PACKAGES: AudiencePackage[] = [
  { id: 1, name: '未注册黄小璨用户', groupId: 0, count: 12896, lastRefresh: '2026-08-05 02:01', refreshMode: '每日 2:00', running: true, version: '历史配置', definition: '已加企微，但未注册黄小璨小程序的用户', incremental: 'off', daily: 'daily_0200', boundAutomation: '沙龙问卷 AI 跟进话术' },
  { id: 2, name: '黄小璨有效期内未进AI+OPC且未报名高客单', groupId: 0, count: 1068, lastRefresh: '2026-08-05 02:01', refreshMode: '每日 2:00', running: true, version: '历史配置', definition: '会员有效期内，未进入 AI+OPC 流程且未报名高客单课程', incremental: 'off', daily: 'daily_0200', boundAutomation: '' },
  { id: 3, name: '9.9 已支付未激活', groupId: 1, count: 342, lastRefresh: '2026-08-05 09:30', refreshMode: '每 3 分钟', running: true, version: 'v3', definition: '支付 9.9 体验课但 48 小时内未激活小程序', incremental: 'incremental_3m', daily: 'off', boundAutomation: '沙龙问卷 AI 跟进话术' },
  { id: 4, name: '9.9 已激活未进群', groupId: 1, count: 187, lastRefresh: '2026-08-05 09:30', refreshMode: '每 3 分钟', running: false, version: 'v3', definition: '已激活小程序但尚未进入共学营群', incremental: 'incremental_3m', daily: 'off', boundAutomation: '' },
];

/* ================= 运营闭环种子 ================= */
const CYCLE_TASKS: CycleTask[] = [
  { id: 1, name: '周一沙龙邀约闭环', cron: '每周一 09:00', dot: '#2EA121', steps: [ { label: '拉群', color: '#2EA121', dim: false }, { label: '开营', color: '#3370ff', dim: false }, { label: '复盘', color: '#C4C7CC', dim: true } ], action: '开始复盘', runId: 1 },
  { id: 2, name: '沉默用户唤回闭环', cron: '每周三 20:00', dot: '#3370ff', steps: [ { label: '筛人', color: '#2EA121', dim: false }, { label: '触达', color: '#2EA121', dim: false }, { label: '观察', color: '#3370ff', dim: false } ], action: '查看进度', runId: 2 },
  { id: 3, name: '会员到期续费闭环', cron: '每日 08:30', dot: '#D97917', steps: [ { label: '到期名单', color: '#2EA121', dim: false }, { label: '续费触达', color: '#2EA121', dim: false }, { label: '复盘', color: '#C4C7CC', dim: true } ], action: '开始复盘', runId: 3 },
];

const CYCLE_RUNS: Record<number, CycleRun> = {
  1: {
    id: 1,
    label: '周一沙龙邀约闭环 · 第 33 期',
    objective: '对本周新报名沙龙的 486 人完成拉群与开营触达，开营日到课率不低于 62%。',
    strategy: '周一沙龙邀约闭环',
    runKey: 'mock-cycle-33',
    snapshotRev: '18',
    audience: '本周报名沙龙且已加企微（486 人）',
    intendedSendAt: '2026-08-17 09:00',
    planScheduledFor: '2026-08-17 09:00',
    firstSentAt: '2026-08-17 09:00:12',
    lastSentAt: '2026-08-17 09:04:51',
    attempts: [
      { label: '首次执行', statusLabel: '成功', tone: 'ok', summary: '前置检查通过：人群包已刷新、话术已绑定、发送人在白名单内。', startedAt: '2026-08-17 09:00:03', finishedAt: '2026-08-17 09:04:51', stages: [ { label: '前置检查', status: 'ok' }, { label: '人群锁定', status: 'ok' }, { label: '人审通过', status: 'ok' }, { label: '批量发送', status: 'ok' } ] },
    ],
    funnel: [ { label: '基础人群', value: '486' }, { label: '去重后', value: '481' }, { label: '人审通过', value: '481' }, { label: '有效发送', value: '479' } ],
    audienceNote: '剔除 5 名 24 小时内已被其他计划触达的用户，避免过度打扰。',
    reviewStatus: '已通过',
    reviewTone: 'ok',
    planVersion: 'v18',
    planStatus: '已执行',
    planSource: '自动快照',
    targetCount: '481',
    delivery: { sent: '479', failed: '2', retryable: '2', rate: '99.6', statusLabel: '已完成', source: '企微发送回执', failureSummary: '2 条发送失败：对方已删除好友，已标记可重试（换绑后）。' },
    windows: [
      { label: '开营日 · 到课', statusLabel: '已上报', tone: 'ok', metrics: [ { label: '到课人数', value: '312', desc: '开营直播进入 ≥ 5 分钟' }, { label: '到课率', value: '65.1%', desc: '目标 ≥ 62%' } ], start: '2026-08-17 19:00', end: '2026-08-17 21:30', quality: '完整', limitation: '直播进入数据有 3 分钟延迟' },
      { label: 'T+1 · 问卷回收', statusLabel: '已上报', tone: 'blue', metrics: [ { label: '问卷提交', value: '204', desc: '42.5% 的触达用户完成' } ], start: '2026-08-18 00:00', end: '2026-08-18 23:59', quality: '完整', limitation: '未补充限制说明' },
    ],
    retro: { summary: '到课率达标，问卷转化高于近 6 期均值', detail: '到课率 65.1%（目标 62%），问卷回收率 42.5%（近 6 期均值 35.8%）。', findings: ['开营前 2 小时二次提醒的组别到课率高出 8.3 个百分点', '带问卷链接的话术版本回复率更高'], limitations: ['到课口径依赖直播进入日志，存在 3 分钟延迟', '未区分自然到课与提醒到课'] },
    next: { statusLabel: '已确认', tone: 'ok', summary: '下一期默认开启「开营前 2 小时二次提醒」', rationale: '提醒组别到课率提升显著，纳入标准动作。', confirmedAt: '2026-08-18 10:12', appliedVersion: 'v19', note: '由运营主管确认后进入策略版本 v19', changes: ['新增开营前 2 小时二次提醒步骤', '问卷链接前置到首条话术'] },
    references: [ { label: '发送任务 #SA-20260817-033', desc: '企微批量发送回执 · 479 条' }, { label: '人群包快照 rev18', desc: '481 人 · 去重后' } ],
  },
  2: {
    id: 2,
    label: '沉默用户唤回闭环 · 第 12 期',
    objective: '对沉默 30 天以上的 1,286 名会员进行唤回触达，目标重新打开率 8%。',
    strategy: '沉默用户唤回闭环',
    runKey: 'cycle_recall_2026w34_run012',
    snapshotRev: '7',
    audience: '沉默 ≥ 30 天的会员（1,286 人）',
    intendedSendAt: '2026-08-19 20:00',
    planScheduledFor: '2026-08-19 20:00',
    firstSentAt: '2026-08-19 20:00:08',
    lastSentAt: '2026-08-19 20:11:42',
    attempts: [
      { label: '首次执行', statusLabel: '部分失败', tone: 'warn', summary: '前置检查发现 2 名发送人不在白名单，切换备用发送人后完成。', startedAt: '2026-08-19 20:00:02', finishedAt: '2026-08-19 20:11:42', stages: [ { label: '前置检查', status: 'warn' }, { label: '人群锁定', status: 'ok' }, { label: '人审通过', status: 'ok' }, { label: '批量发送', status: 'ok' } ] },
    ],
    funnel: [ { label: '基础人群', value: '1,286' }, { label: '去重后', value: '1,240' }, { label: '人审通过', value: '1,240' }, { label: '有效发送', value: '1,219' } ],
    audienceNote: '剔除 46 名近 7 天已被唤回触达的用户。',
    reviewStatus: '已通过',
    reviewTone: 'ok',
    planVersion: 'v7',
    planStatus: '已执行',
    planSource: '自动快照',
    targetCount: '1,240',
    delivery: { sent: '1,219', failed: '21', retryable: '17', rate: '98.3', statusLabel: '已完成', source: '企微发送回执', failureSummary: '21 条失败：17 条可重试（限频），4 条对方已删除好友。' },
    windows: [
      { label: 'T+3 · 重新打开', statusLabel: '观察中', tone: 'blue', metrics: [ { label: '重新打开人数', value: '76', desc: '当前 6.2%，目标 8%' } ], start: '2026-08-19 20:00', end: '2026-08-22 20:00', quality: '进行中', limitation: '窗口未结束，不作为结论' },
    ],
    retro: { summary: '复盘尚未形成', detail: '待观察窗口成熟并核对数据口径后补充。', findings: [], limitations: ['观察窗口 T+3 尚未结束'] },
    next: { statusLabel: '尚未形成', tone: 'gray', summary: '等待复盘结论', rationale: '优化建议被确认后，才会进入下一策略版本。', confirmedAt: '未开始', appliedVersion: '未开始', note: '未记录', changes: [] },
    references: [ { label: '发送任务 #RC-20260819-012', desc: '企微批量发送回执 · 1,219 条' } ],
  },
  3: {
    id: 3,
    label: '会员到期续费闭环 · 第 58 期',
    objective: '对 7 天内到期的 286 名会员完成续费提醒，目标续费率 18%。',
    strategy: '会员到期续费闭环',
    runKey: 'cycle_renew_20260821_run058',
    snapshotRev: '22',
    audience: '7 天内到期会员（286 人）',
    intendedSendAt: '2026-08-21 08:30',
    planScheduledFor: '2026-08-21 08:30',
    firstSentAt: '2026-08-21 08:30:05',
    lastSentAt: '2026-08-21 08:33:19',
    attempts: [
      { label: '首次执行', statusLabel: '成功', tone: 'ok', summary: '全部前置检查通过，按计划完成发送。', startedAt: '2026-08-21 08:30:01', finishedAt: '2026-08-21 08:33:19', stages: [ { label: '前置检查', status: 'ok' }, { label: '人群锁定', status: 'ok' }, { label: '人审通过', status: 'ok' }, { label: '批量发送', status: 'ok' } ] },
    ],
    funnel: [ { label: '基础人群', value: '286' }, { label: '去重后', value: '286' }, { label: '人审通过', value: '284' }, { label: '有效发送', value: '284' } ],
    audienceNote: '2 名用户被人审标记为「暂不打扰」（投诉记录）。',
    reviewStatus: '已通过',
    reviewTone: 'ok',
    planVersion: 'v22',
    planStatus: '已执行',
    planSource: '自动快照',
    targetCount: '284',
    delivery: { sent: '284', failed: '0', retryable: '0', rate: '100', statusLabel: '已完成', source: '企微发送回执', failureSummary: '' },
    windows: [
      { label: 'T+7 · 续费转化', statusLabel: '未开始', tone: 'gray', metrics: [], start: '2026-08-21 08:30', end: '2026-08-28 08:30', quality: '未开始', limitation: '窗口未开始' },
    ],
    retro: { summary: '复盘尚未形成', detail: '待观察窗口成熟并核对数据口径后补充。', findings: [], limitations: ['续费窗口 T+7 尚未开始'] },
    next: { statusLabel: '尚未形成', tone: 'gray', summary: '等待复盘结论', rationale: '优化建议被确认后，才会进入下一策略版本。', confirmedAt: '未开始', appliedVersion: '未开始', note: '未记录', changes: [] },
    references: [ { label: '发送任务 #RN-20260821-058', desc: '企微批量发送回执 · 284 条' } ],
  },
};

/* ================= 问卷运营配置种子 ================= */
function makeQOps(): Record<number, QuestionnaireOps> {
  const base: QuestionnaireOps = {
    completionNavigationTargetId: 'completion.default',
    completionChannelId: '1',
    externalPushConfigurationReference: 'external-push.default',
    localOnly: true,
    postEnabled: true,
    postType: 'channel_qr',
    channelId: '加我企业微信，兑换课程',
    qrTitle: '扫码添加助教企微',
    qrSubtitle: '领取课程资料与答疑',
    redirectType: 'h5',
    redirectUrl: 'https://www.xinliushangye.com/s/thanks',
    pushEnabled: true,
    webhookUrl: 'https://open.example.com/webhook/questionnaire',
    subscribeType: '提交完成（submit.completed）',
    expiresAt: '2027-08-01 00:00',
    serviceCycle: '长期服务',
    frequency: '实时推送',
    remark: '推送到 CRM 线索池',
    customParams: [ { key: 'source', value: 'questionnaire' }, { key: 'scene', value: 'salon' } ],
  };
  const map: Record<number, QuestionnaireOps> = {};
  for (let i = 0; i < 6; i++) map[i] = JSON.parse(JSON.stringify(base)) as QuestionnaireOps;
  map[0].postType = 'redirect';
  map[0].redirectType = 'h5';
  map[0].redirectUrl = 'https://www.xinliushangye.com/report/diagnosis';
  map[1].redirectType = 'urllink';
  map[1].redirectUrl = 'https://api.example.com/urllink/survey-thanks';
  map[5].postEnabled = false;
  map[5].pushEnabled = false;
  return map;
}

/* ================= 企微标签种子 ================= */
const TAG_GROUPS: TagGroup[] = [
  { id: 1, name: '沙龙来源' },
  { id: 2, name: '意向等级' },
  { id: 3, name: '行业' },
  { id: 4, name: '课程偏好' },
  { id: 5, name: '运营动作' },
];

function makeWecomTags(): WecomTag[] {
  let id = 1;
  const add = (groupId: number, names: [string, number][]): WecomTag[] =>
    names.map(([name, users]) => ({ id: id++, groupId, name, users, syncedAt: '08-05 08:00' }));
  return [
    ...add(1, [ ['沙龙邀约', 412], ['共学营', 268], ['直播', 155], ['视频号', 96], ['朋友推荐', 43], ['线下活动', 21] ]),
    ...add(2, [ ['高意向', 231], ['中意向', 386], ['低意向', 502], ['待评估', 89] ]),
    ...add(3, [ ['教育培训', 312], ['大健康', 188], ['零售电商', 176], ['美容美发', 142], ['心理咨询', 98], ['保险', 66], ['高端餐饮', 54], ['珠宝', 31], ['瑜伽产康', 47], ['形体礼仪', 28], ['空间小院', 19], ['知识付费', 233], ['其他', 87] ]),
    ...add(4, [ ['增长陪跑', 486], ['共学营', 268], ['沙龙', 412], ['试听课', 1024], ['年卡', 87], ['单次诊断', 342], ['工作坊', 65], ['直播课', 155] ]),
    ...add(5, [ ['已首触', 1024], ['待跟进', 231], ['已拉群', 486], ['已续费提醒', 284], ['沉默唤回', 1219] ]),
  ];
}

/* ================= 配置中心种子（生产 category_registry 10 类目全字段） ================= */
type F = ConfigCategory['blocks'][number]['fields'][number];
const t = (key: string, value = '', kind: F['kind'] = 'text', extra?: Partial<F>): F => ({ key, label: key, kind, value, ...extra });
const sec = (key: string, configured = true): F => t(key, '', 'secret', { configured });
const sw = (key: string, on: boolean): F => t(key, '', 'switch', { on });
const num = (key: string, v: number): F => t(key, String(v), 'number');
const blk = (title: string, fields: F[]): ConfigCategory['blocks'][number] => ({ title, fields });

const CONFIG_CATEGORIES: ConfigCategory[] = [
  { key: 'wecom_base', label: '企业微信基础', group: '核心连接能力', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ t('WECOM_CORP_ID', 'ww8f2c1d0a9b7e4c'), t('WECOM_AGENT_ID', '1000047') ]),
    blk('密钥', [ sec('WECOM_SECRET'), sec('WECOM_CONTACT_SECRET', false) ]),
    blk('接口', [ t('WECOM_API_BASE', 'https://qyapi.weixin.qq.com'), t('WECOM_DEFAULT_OWNER_USERID', 'HuangYouCan') ]),
    blk('回调', [ t('WECOM_CALLBACK_TOKEN', 'Kq9XmZ2sVb'), sec('WECOM_CALLBACK_AES_KEY') ]),
    blk('会话存档', [ sec('WECOM_ARCHIVE_SECRET', false), t('WECOM_PRIVATE_KEY_PATH', '/data/keys/wecom_archive.pem'), t('WECOM_SDK_LIB_PATH', '/opt/wecom-sdk/lib'), num('WECOM_ARCHIVE_TIMEOUT', 30) ]),
    blk('限制', [ num('WECOM_CORP_TAG_LIMIT', 1000) ]),
  ] },
  { key: 'admin_access', label: '后台访问', group: '后台安全', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ t('AUTH_MODE', 'password'), t('LOGIN_REDIRECT_URI', 'https://crm.example.com/admin'), t('WECHAT_TRUSTED_DOMAIN', 'crm.example.com') ]),
  ] },
  { key: 'sidebar_identity', label: '侧边栏与身份', group: '后台安全', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ num('AICRM_SIDEBAR_TOKEN_TTL_SECONDS', 7200), num('AICRM_ADMIN_TOKEN_TTL_SECONDS', 86400) ]),
    blk('企微 JSSDK', [ t('AICRM_SIDEBAR_JSSDK_ADAPTER_MODE', 'auto'), sw('AICRM_SIDEBAR_JSSDK_REAL_ENABLED', true), sec('AICRM_SIDEBAR_JSSDK_SECRET'), num('AICRM_SIDEBAR_JSSDK_TIMEOUT_SECONDS', 8) ]),
    blk('图片素材', [ t('AICRM_SIDEBAR_IMAGE_QUICK_KEYWORDS', '欢迎语,海报,课程卡片') ]),
  ] },
  { key: 'ai_automation', label: 'AI 与自动化', group: '自动化能力', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('DeepSeek', [ sw('DEEPSEEK_ENABLED', true), sec('DEEPSEEK_API_KEY'), t('DEEPSEEK_BASE_URL', 'https://api.deepseek.com'), t('DEEPSEEK_MODEL', 'deepseek-chat'), num('DEEPSEEK_TIMEOUT_SECONDS', 60), num('DEEPSEEK_MAX_TOKENS', 4096), t('DEEPSEEK_EMBEDDING_MODEL', 'deepseek-embedding') ]),
    blk('统一授权平台', [ t('AICRM_AUTH_ISSUER', 'https://auth.example.com'), sec('AICRM_AUTH_SESSION_HASH_PEPPER'), sec('AICRM_AUTH_JWT_SIGNING_KEY'), t('AICRM_AUTH_TRUSTED_PROXY_ADDRESSES', '10.0.0.0/8') ]),
    blk('机器身份', [ t('AICRM_AUTH_CA_FILE', '/etc/ssl/auth-ca.pem'),
      t('AICRM_AUTH_AUTOMATION_WORKER_CLIENT_ID', 'automation-worker'), sec('AICRM_AUTH_AUTOMATION_WORKER_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_ARCHIVE_WORKER_CLIENT_ID', 'archive-worker'), sec('AICRM_AUTH_ARCHIVE_WORKER_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_CALLBACK_WORKER_CLIENT_ID', 'callback-worker'), sec('AICRM_AUTH_CALLBACK_WORKER_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_GROUP_BROADCAST_CLIENT_ID', 'group-broadcast'), sec('AICRM_AUTH_GROUP_BROADCAST_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_IDENTITY_CLIENT_ID', 'identity'), sec('AICRM_AUTH_IDENTITY_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_MCP_CLIENT_ID', 'mcp-tools'), sec('AICRM_AUTH_MCP_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_EXTERNAL_AGENT_CLIENT_ID', 'external-agent'), sec('AICRM_AUTH_EXTERNAL_AGENT_CLIENT_SECRET_REF', false),
      t('AICRM_AUTH_CAMPAIGN_AGENT_CLIENT_ID', 'campaign-agent'), sec('AICRM_AUTH_CAMPAIGN_AGENT_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_OPS_REPORTER_CLIENT_ID', 'ops-reporter'), sec('AICRM_AUTH_OPS_REPORTER_CLIENT_SECRET_REF'),
      t('AICRM_AUTH_OPERATION_RUNNER_CLIENT_ID', 'operation-runner'), sec('AICRM_AUTH_OPERATION_RUNNER_CLIENT_SECRET_REF') ]),
    blk('Webhook HMAC', [ t('AICRM_AUTH_OUTBOUND_WEBHOOK_CLIENT_ID', 'outbound-webhook') ]),
  ] },
  { key: 'webhooks_push', label: 'Webhook 与外推', group: '外部联通能力', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('OPENCLAW', [ t('OPENCLAW_WEBHOOK_URL', 'https://openclaw.example.com/hook'), num('OPENCLAW_TIMEOUT_SECONDS', 10) ]),
    blk('问卷提交', [ t('QUESTIONNAIRE_SUBMIT_WEBHOOK_URL', 'https://open.example.com/webhook/submit'), num('QUESTIONNAIRE_SUBMIT_WEBHOOK_TIMEOUT_SECONDS', 8) ]),
    blk('问卷外推', [ sw('QUESTIONNAIRE_EXTERNAL_PUSH_GLOBAL_ENABLED', true), num('QUESTIONNAIRE_EXTERNAL_PUSH_TIMEOUT_SECONDS', 8) ]),
    blk('统一队列', [ t('AICRM_EXTERNAL_EFFECT_ALLOWED_BASE_HOSTS', 'qyapi.weixin.qq.com,open.weixin.qq.com'), sw('AICRM_EXTERNAL_EFFECT_TEST_EXECUTION_ONLY', false), t('AICRM_EXTERNAL_EFFECT_ALLOWED_TYPES', 'wecom,webhook'), sw('AICRM_EXTERNAL_EFFECT_REALTIME_ENABLED', true), t('AICRM_EXTERNAL_EFFECT_REALTIME_ALLOWED_TYPES', 'webhook'), num('AICRM_EXTERNAL_EFFECT_QUEUE_BATCH', 50) ]),
    blk('Webhook 执行', [ sw('AICRM_WEBHOOK_EXECUTOR_ENABLED', true) ]),
    blk('企微执行', [ sw('AICRM_WECOM_EXEC_ENABLED', true), num('AICRM_WECOM_EXEC_TIMEOUT_SECONDS', 12), num('AICRM_WECOM_EXEC_BATCH_SIZE', 100), num('AICRM_WECOM_EXEC_RATE_LIMIT', 40), t('AICRM_WECOM_EXEC_CALLBACK_URL', 'https://crm.example.com/api/wecom/exec-callback'), t('AICRM_WECOM_EXEC_CALLBACK_TOKEN', 'tk_9f2m'), sec('AICRM_WECOM_EXEC_CALLBACK_AES_KEY'), num('AICRM_WECOM_EXEC_RETRY_MAX', 3), sw('AICRM_WECOM_EXEC_DEBUG', false) ]),
    blk('测试接收端', [ t('AICRM_WEBHOOK_TEST_RECEIVER_URL', 'https://webhook.site/aicrm-test') ]),
    blk('预留开关', [ sw('AICRM_PUSH_RESERVE_SWITCH_1', false), sw('AICRM_PUSH_RESERVE_SWITCH_2', false), sw('AICRM_PUSH_RESERVE_SWITCH_3', false), sw('AICRM_PUSH_RESERVE_SWITCH_4', false) ]),
    blk('重试', [ num('AICRM_PUSH_RETRY_MAX', 3), num('AICRM_PUSH_RETRY_INTERVAL_SECONDS', 30), t('AICRM_PUSH_RETRY_BACKOFF', '2x') ]),
  ] },
  { key: 'reliability', label: '稳定性', group: '平台治理', on: true, toggleable: true, checkSupported: false, blocks: [
    blk('HTTP', [ num('AICRM_HTTP_TIMEOUT_SECONDS', 30), num('AICRM_HTTP_RETRY_TIMES', 2), num('AICRM_HTTP_POOL_SIZE', 100) ]),
    blk('熔断', [ num('AICRM_CB_FAILURE_THRESHOLD', 5), num('AICRM_CB_RECOVERY_SECONDS', 60) ]),
    blk('队列', [ t('RQ_QUEUE_NAME', 'aicrm') ]),
    blk('OUTBOX', [ sw('AICRM_OUTBOX_ENABLED', true), num('AICRM_OUTBOX_BATCH', 100) ]),
    blk('REDIS', [ sec('REDIS_URL') ]),
  ] },
  { key: 'wechat_pay', label: '微信支付', group: '外部联通能力', on: true, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ sw('WECHAT_PAY_ENABLED', true), t('WECHAT_PAY_APP_ID', 'wx9f4c2a1b8e6d3012'), t('WECHAT_PAY_MCH_ID', '1688****21') ]),
    blk('密钥', [ sec('WECHAT_PAY_API_KEY'), sec('WECHAT_PAY_API_V3_KEY'), t('WECHAT_PAY_CERT_SERIAL_NO', '5A3F…C2'), t('WECHAT_PAY_PRIVATE_KEY_PATH', '/data/keys/apiclient_key.pem'), t('WECHAT_PAY_PLATFORM_CERT_PATH', '/data/keys/wechatpay_cert.pem') ]),
    blk('接口', [ t('WECHAT_PAY_NOTIFY_URL', 'https://crm.example.com/api/pay/wechat/notify'), sw('WECHAT_PAY_SANDBOX', false), num('WECHAT_PAY_TIMEOUT_SECONDS', 15) ]),
    blk('商品目录', [ t('WECHAT_PAY_CATALOG_JSON', '{\n  "SP-GROW-90": "增长陪跑 · 90 天",\n  "CITY-YEAR": "城市会员 · 年卡"\n}', 'textarea') ]),
  ] },
  { key: 'alipay', label: '支付宝支付', group: '外部联通能力', on: false, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ sw('ALIPAY_ENABLED', false), t('ALIPAY_APP_ID', ''), t('ALIPAY_SIGN_TYPE', 'RSA2') ]),
    blk('密钥', [ t('ALIPAY_PRIVATE_KEY_PATH', ''), sec('ALIPAY_PUBLIC_KEY', false) ]),
    blk('接口', [ t('ALIPAY_GATEWAY', 'https://openapi.alipay.com/gateway.do'), t('ALIPAY_NOTIFY_URL', ''), t('ALIPAY_RETURN_URL', ''), num('ALIPAY_TIMEOUT_SECONDS', 15) ]),
  ] },
  { key: 'wechat_shop', label: '微信小店', group: '外部联通能力', on: false, toggleable: true, checkSupported: true, blocks: [
    blk('基础信息', [ sw('WECHAT_SHOP_ENABLED', false), t('WECHAT_SHOP_APP_ID', ''), sec('WECHAT_SHOP_APP_SECRET', false) ]),
    blk('回调', [ t('WECHAT_SHOP_CALLBACK_TOKEN', ''), sec('WECHAT_SHOP_CALLBACK_AES_KEY', false) ]),
    blk('同步', [ sw('WECHAT_SHOP_SYNC_ENABLED', false) ]),
  ] },
  { key: 'wechat_oauth', label: '公众号授权', group: '外部联通能力', on: true, toggleable: true, checkSupported: false, blocks: [
    blk('基础信息', [ t('MP_APP_ID', 'wx4b1a8c9d2e7f6035'), sec('MP_APP_SECRET'), t('MP_OAUTH_SCOPE', 'snsapi_userinfo') ]),
  ] },
];

/* ================= 优惠券数据页种子 ================= */
function makeCouponClaims(): Record<number, CouponClaim[]> {
  const rows0: CouponClaim[] = [
    { user: '李思远', status: '已使用', tone: 'gray', claimedAt: '2026-08-03 09:12', validWindow: '08-03 – 08-31', product: '增长陪跑 · 90 天', orderNo: 'PAY20260803A19', usedAt: '2026-08-03 09:52' },
    { user: '陈曦', status: '已使用', tone: 'gray', claimedAt: '2026-08-02 14:05', validWindow: '08-02 – 08-31', product: '增长陪跑 · 90 天', orderNo: 'PAY20260802B44', usedAt: '2026-08-02 20:11' },
    { user: '周雨桐', status: '可用', tone: 'ok', claimedAt: '2026-08-05 08:41', validWindow: '08-05 – 08-31', product: '-', orderNo: '-', usedAt: '-' },
    { user: '王一鸣', status: '已预占', tone: 'blue', claimedAt: '2026-08-05 11:20', validWindow: '08-05 – 08-31', product: '增长陪跑 · 90 天', orderNo: 'PAY20260805B54（未支付）', usedAt: '-' },
    { user: '赵启明', status: '已过期', tone: 'red', claimedAt: '2026-07-28 19:33', validWindow: '07-28 – 08-04', product: '-', orderNo: '-', usedAt: '-' },
    { user: '孙婉宁', status: '已使用', tone: 'gray', claimedAt: '2026-08-01 10:02', validWindow: '08-01 – 08-31', product: '增长陪跑 · 30 天', orderNo: 'PAY20260801A31', usedAt: '2026-08-01 10:26' },
    { user: '吴桐', status: '可用', tone: 'ok', claimedAt: '2026-08-04 22:17', validWindow: '08-04 – 08-31', product: '-', orderNo: '-', usedAt: '-' },
    { user: '林晓', status: '已使用', tone: 'gray', claimedAt: '2026-08-02 09:48', validWindow: '08-02 – 08-31', product: '增长陪跑 · 90 天', orderNo: 'PAY20260802A02', usedAt: '2026-08-02 11:03' },
  ];
  const rows1: CouponClaim[] = [
    { user: '张敏', status: '已使用', tone: 'gray', claimedAt: '2026-07-20 09:15', validWindow: '07-20 – 08-15', product: '私域陪跑年卡', orderNo: 'PAY20260720A11', usedAt: '2026-07-20 09:41' },
    { user: '李由', status: '可用', tone: 'ok', claimedAt: '2026-08-10 15:26', validWindow: '08-10 – 08-15', product: '-', orderNo: '-', usedAt: '-' },
    { user: '周航', status: '已过期', tone: 'red', claimedAt: '2026-07-15 08:00', validWindow: '07-15 – 08-15', product: '-', orderNo: '-', usedAt: '-' },
  ];
  return { 0: rows0, 1: rows1, 2: [], 3: [], 4: [] };
}

/* ================= 通用选择器数据源（企微员工目录 / 客户群） ================= */
const STAFF_DIRECTORY: StaffMember[] = [
  { name: '林小楷', uid: 'LinKaiYan', dept: '增长顾问组' },
  { name: '张敏', uid: 'ZhangMin', dept: '增长顾问组' },
  { name: '黄友灿', uid: 'HuangYouCan', dept: '增长顾问组' },
  { name: '李由', uid: 'LiYou', dept: '社群运营组' },
  { name: '王恺', uid: 'WangKai', dept: '社群运营组' },
  { name: '周航', uid: 'ZhouHang', dept: '社群运营组' },
  { name: '陈静', uid: 'ChenJing', dept: '客户成功组' },
  { name: '刘靖', uid: 'LiuJing', dept: '客户成功组' },
];

const GROUP_CHATS: GroupChat[] = [
  { name: '5 天共学营 · 1 群', left: 96, size: 200 },
  { name: '5 天共学营 · 2 群', left: 34, size: 200 },
  { name: '5 天共学营 · 3 群', left: 118, size: 200 },
  { name: '沙龙邀约 · 8.6 期', left: 152, size: 200 },
  { name: '城市会员 · 北京站', left: 61, size: 200 },
  { name: '老客续费专属群', left: 187, size: 200 },
];

/* ================= 漏斗：120 行确定性造数（与方向稿一致，seed=42） ================= */
const FUNNEL_ENUMS = {
  funnel_label: ['已激活并打开', '仅激活未打开', '注册但无会员', '未激活'],
  owner_userid: ['LinKaiYan', 'ZhangMin', 'LiYou', 'HuangYouCan', '—'],
  class_term_label: ['8.6 期', '8.13 期', '8.20 期', '体验营', ''],
  first_entry_source: ['视频号', '公众号', '雷达链接', '好友推荐', '直播', '线下'],
  crm_hxc_state: ['已激活', '未激活', '疑似激活'],
  hxc_member_status: ['正常', '过期', '未开通', ''],
  membership_type: ['年卡', '季卡', '月卡', '体验卡', ''],
};

function makeFunnelRows(): FunnelGridRow[] {
  let seed = 42;
  const rnd = (): number => (seed = (seed * 1103515245 + 12345) >>> 0) / 4294967296;
  const SURN = '林陈刘王张李周吴赵孙钱冯褚卫蒋沈韩杨朱秦尤许何吕'.split('');
  const GIVN = ['晓', '默', '靖', '恺', '敏', '由', '航', '桐', '一', '可', '远', '夏', '东', '晴', '朗', '佳', '欢', '琳'];
  const pick = <T,>(a: T[]): T => a[Math.floor(rnd() * a.length)];
  const extId = (): string => {
    const cs = 'abcdefghijklmnopqrstuvwxyz0123456789';
    let s = '';
    for (let i = 0; i < 5; i++) s += cs[Math.floor(rnd() * cs.length)];
    return 'wmXk3ABBAA_' + s;
  };
  const fmtD = (d: Date): string =>
    d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
  const rows: FunnelGridRow[] = [];
  for (let i = 0; i < 120; i++) {
    const fs = pick(FUNNEL_ENUMS.funnel_label);
    const hasMember = fs === '已激活并打开' || fs === '仅激活未打开';
    const hasUser = fs === '已激活并打开' || fs === '注册但无会员';
    const d = new Date(2026, 7 - Math.floor(rnd() * 3), 1 + Math.floor(rnd() * 28));
    rows.push({
      mobile_masked: '1' + pick(['38', '39', '31', '37', '35']) + '****' + String(1000 + Math.floor(rnd() * 9000)),
      customer_name: pick(SURN) + pick(GIVN) + (rnd() > 0.7 ? pick(GIVN) : ''),
      funnel_label: fs,
      external_userid: hasMember && rnd() > 0.2 ? extId() : rnd() > 0.6 ? extId() : '',
      owner_userid: pick(FUNNEL_ENUMS.owner_userid),
      in_lead_pool: rnd() > 0.25 ? '✓' : '✗',
      in_questionnaire: rnd() > 0.5 ? '✓' : '✗',
      questionnaire_count: Math.floor(rnd() * 6),
      last_questionnaire_at: rnd() > 0.4 ? fmtD(d) : '',
      is_wecom_added: hasMember || rnd() > 0.4 ? '✓' : '✗',
      class_term_label: pick(FUNNEL_ENUMS.class_term_label),
      first_entry_source: pick(FUNNEL_ENUMS.first_entry_source),
      crm_hxc_state: hasMember ? '已激活' : pick(FUNNEL_ENUMS.crm_hxc_state),
      hxc_member_status: hasMember ? (rnd() > 0.85 ? '过期' : '正常') : hasUser ? '' : pick(['未开通', '']),
      membership_type: hasMember ? pick(['年卡', '季卡', '月卡', '体验卡']) : '',
      membership_days_left: hasMember ? Math.floor(rnd() * 360) : 0,
      hxc_silent_days: Math.floor(rnd() * 90),
      msg_user: Math.floor(rnd() * rnd() * 300),
      msg_ai: Math.floor(rnd() * rnd() * 400),
      last_msg_at: rnd() > 0.2 ? fmtD(d) : '',
    });
  }
  return rows;
}

export const SEED_DB: AdminDb = {
  radarLinks: [
    { id: 1, title: 'AI 增长陪跑详情页 · 追踪版', target_type: 'link', original_url: 'https://www.xinliushangye.com/peipao?utm=radar', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: 'HuangYouCan', code: 'a8f3k2', total_landings: 1284, authorized_users: 912, view_count: 412, last_viewed_at: '2026-08-05 09:41' },
    { id: 2, title: '共学营开营通知（可追踪）', target_type: 'link', original_url: 'https://mp.weixin.qq.com/s/gongxueying-kaiying', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: 'HuangYouCan', code: 'h5gx92', total_landings: 642, authorized_users: 508, view_count: 188, last_viewed_at: '2026-08-04 22:10' },
    { id: 3, title: '课程试听页', target_type: 'link', original_url: 'https://www.xinliushangye.com/trial', file_name_snapshot: '', media_item_id: '', enabled: false, auth_required: true, staff_id: 'WangWei', code: 'tri1x0', total_landings: 218, authorized_users: 196, view_count: 54, last_viewed_at: '2026-07-28 16:32' },
    { id: 4, title: '5天沙龙课程大纲.pdf', target_type: 'pdf', original_url: '', file_name_snapshot: '5天沙龙课程大纲.pdf', media_item_id: 'att_9021', enabled: true, auth_required: true, staff_id: 'LinKaiYan', code: 'pdf77a', total_landings: 396, authorized_users: 301, view_count: 276, last_viewed_at: '2026-08-05 08:15', pdf_processing_status: 'ready', pdf_page_count: 18 },
    { id: 5, title: '陪跑营学员案例长图', target_type: 'image', original_url: '', file_name_snapshot: 'case-poster.png', media_item_id: 'img_3382', enabled: true, auth_required: false, staff_id: 'ZhangMin', code: 'img55c', total_landings: 175, authorized_users: 0, view_count: 163, last_viewed_at: '2026-08-03 19:02' },
  ],
  radarEvents: makeRadarEvents(),
  aiPlans: AI_PLANS,
  aiRcs: makeAiRcs(),
  funnelRows: makeFunnelRows(),
  funnelViews: [
    { name: '全部人群', filters: [], group: '', sort: { field: 'msg_user', dir: 'desc' } },
    { name: '拉回重点 · 仅激活未打开', filters: [{ field: 'funnel_label', op: '等于', value: '仅激活未打开' }], group: 'owner_userid', sort: { field: 'msg_user', dir: 'desc' } },
    { name: '高价值会员', filters: [{ field: 'funnel_label', op: '等于', value: '已激活并打开' }, { field: 'msg_user', op: '≥', value: '50' }], group: 'membership_type', sort: { field: 'membership_days_left', dir: 'asc' } },
  ],
  audienceGroups: [{ id: 1, name: '黄小璨9.9运营' }],
  audiencePackages: AUDIENCE_PACKAGES,
  audienceMembers: {
    1: [0, 1, 2, 3, 4, 5].map((i) => ({ name: AI_NAMES[i], external_userid: AI_EXT[i % AI_EXT.length], joinedAt: '2026-08-0' + (1 + (i % 5)) + ' 09:1' + i })),
    2: [6, 7, 8, 9].map((i) => ({ name: AI_NAMES[i], external_userid: AI_EXT[i % AI_EXT.length], joinedAt: '2026-08-03 14:2' + i })),
    3: [10, 11, 12, 13, 14, 15, 16].map((i) => ({ name: AI_NAMES[i], external_userid: AI_EXT[i % AI_EXT.length], joinedAt: '2026-08-05 08:0' + (i % 10) })),
    4: [17, 18, 19].map((i) => ({ name: AI_NAMES[i], external_userid: AI_EXT[i % AI_EXT.length], joinedAt: '2026-08-04 20:3' + (i % 10) })),
  },
  audienceSenders: {
    1: [ { priority: 1, userid: 'HuangYouCan', rule: '默认发送人', status: '生效中' }, { priority: 2, userid: 'ZhangMin', rule: '备用 · 限频 40 条/分钟', status: '生效中' } ],
    2: [ { priority: 1, userid: 'HuangYouCan', rule: '默认发送人', status: '生效中' } ],
    3: [ { priority: 1, userid: 'LinKaiYan', rule: '默认发送人', status: '生效中' } ],
    4: [ { priority: 1, userid: 'LinKaiYan', rule: '默认发送人', status: '生效中' } ],
  },
  audienceRecords: {
    1: [
      { name: '林晓', external_userid: AI_EXT[0], source: '群发任务 #SA-0817', status: '已发送', tone: 'ok', sentAt: '2026-08-17 09:00:14', failReason: '-' },
      { name: '陈默', external_userid: AI_EXT[1], source: '群发任务 #SA-0817', status: '已发送', tone: 'ok', sentAt: '2026-08-17 09:00:22', failReason: '-' },
      { name: '刘靖', external_userid: AI_EXT[2], source: '群发任务 #SA-0817', status: '发送失败', tone: 'red', sentAt: '2026-08-17 09:00:31', failReason: '对方已删除好友' },
      { name: '王恺', external_userid: AI_EXT[3], source: '群发任务 #SA-0817', status: '已发送', tone: 'ok', sentAt: '2026-08-17 09:00:39', failReason: '-' },
      { name: '张敏', external_userid: AI_EXT[4], source: '手动补发', status: '待发送', tone: 'gray', sentAt: '-', failReason: '-' },
    ],
    2: [], 3: [], 4: [],
  },
  groupOpsPlans: [{ id: '1', name: '新客入群欢迎', status: 'paused', revision: 1, updatedAt: '2026-08-25 10:00' }],
  groupOpsDetail: { plan: { id: '1', name: '新客入群欢迎', status: 'paused', revision: 1, updatedAt: '2026-08-25 10:00' }, staffIds: [1], assets: [{ id: '1', reference: 'group:new-customers' }], nodes: [{ id: '1', position: 1, kind: 'message', messageText: '欢迎加入', materialReference: 'image:welcome' }], webhookReference: '', webhookUrl: '', previewLines: ['1. 欢迎加入'], previewIssues: [] },
  cycleTasks: CYCLE_TASKS,
  cycleRuns: CYCLE_RUNS,
  qOps: makeQOps(),
  tagGroups: TAG_GROUPS,
  wecomTags: makeWecomTags(),
  couponClaims: makeCouponClaims(),
  configCategories: CONFIG_CATEGORIES,
  staff: STAFF_DIRECTORY,
  groupChats: GROUP_CHATS,
  customerList: { total: 6, totalIsEstimate: false, nextCursor: null },
  orderList: { total: 0, hasMore: false },
  customerDetail: { status: 'not_found', context: null, survey: null, error: '' },
  hxcSenders: [],
  rows: {
    customers: [
      { name: '李思远', id: 'wmA8c3DQAA_x91', owner: '张敏', mobile: '138****4021' },
      { name: '陈曦', id: 'wmA8c3DQAA_k27', owner: '张敏', mobile: '159****7783' },
      { name: '周雨桐', id: 'wmA8c3DQAA_p04', owner: '李由', mobile: '186****2210' },
      { name: '王一鸣', id: 'wmA8c3DQAA_b58', owner: '未分配', mobile: '未填写' },
      { name: '赵启明', id: 'wmA8c3DQAA_h33', owner: '李由', mobile: '137****9046' },
      { name: '孙婉宁', id: 'wmA8c3DQAA_c12', owner: '王恺', mobile: '135****6672' },
    ],
    tags: [{ name: '高意向' }, { name: '已看直播' }, { name: '课程A-试听' }, { name: '南方区域' }, { name: '价格敏感' }, { name: '待跟进' }],
    qa: [
      { q: '你目前最想解决的问题是？', a: '获客成本太高' },
      { q: '团队规模', a: '10 – 30 人' },
      { q: '每月投放预算', a: '3 万以内' },
      { q: '手机号', a: '138****4021' },
    ],
    msgs: [
      { who: '客户', time: '08-01 10:22', text: '这个周期服务能开发票吗？', me: false },
      { who: '张敏', time: '08-01 10:24', text: '可以的，付款后在订单页申请，1 个工作日开具。', me: true },
      { who: '客户', time: '08-02 09:10', text: '好的，我先报名试试。', me: false },
      { who: '张敏', time: '08-02 09:12', text: '已经把报名链接发你，有问题随时找我。', me: true },
    ],
    qStats: [
      { label: '问卷总数', value: '18', unit: '份' },
      { label: '已发布', value: '11', unit: '份' },
      { label: '本周提交', value: '1,342', unit: '次' },
      { label: '外推失败', value: '3', unit: '条' },
    ],
    questionnaires: [
      { name: '增长诊断测评', assess: true, off: false, action: '跳转 H5 页面', created: '2026-05-12 10:04', count: '2318' },
      { name: '课程满意度回访', assess: false, off: false, action: '打开小程序 URL Link', created: '2026-04-28 16:31', count: '806' },
      { name: '私域用户画像', assess: true, off: false, action: '展示渠道二维码', created: '2026-04-02 09:12', count: '1455' },
      { name: '直播报名信息收集', assess: false, off: false, action: '打开微信小程序', created: '2026-03-19 14:47', count: '3902' },
      { name: '内容偏好调研', assess: false, off: false, action: '沿用原跳转', created: '2026-02-11 08:55', count: '624' },
      { name: '老客户续费意向', assess: false, off: true, action: '沿用原跳转', created: '2026-03-05 11:26', count: '0' },
    ],
    qSubs: [
      { time: '2026-08-03 09:41', uid: 'wmA8c3DQAA_x91', by: 'unionid', score: '82', tags: ['高意向', '课程A'] },
      { time: '2026-08-03 09:12', uid: 'wmA8c3DQAA_k27', by: 'external_userid', score: '64', tags: ['待跟进'] },
      { time: '2026-08-02 21:37', uid: 'wmA8c3DQAA_p04', by: 'mobile', score: '91', tags: ['高意向', '南方区域'] },
      { time: '2026-08-02 18:05', uid: '-', by: '未匹配', score: '48', tags: [] },
    ],
    qApply: [
      { time: '2026-08-03 09:41', sid: '#20481', uid: 'wmA8c3DQAA_x91', status: '已完成', tone: 'ok', err: '-' },
      { time: '2026-08-03 09:12', sid: '#20480', uid: 'wmA8c3DQAA_k27', status: '已完成', tone: 'ok', err: '-' },
      { time: '2026-08-02 18:05', sid: '#20478', uid: '-', status: '失败', tone: 'red', err: '外部推送超时（504）' },
    ],
    edTools: [
      { m: '单', t: '单选题', d: '只能选一个。' },
      { m: '多', t: '多选题', d: '可以选多个。' },
      { m: '文', t: '文本题', d: '开放回答。' },
      { m: '号', t: '手机号题', d: '收集联系方式。' },
      { m: '测', t: '添加多维测评模板', d: '从已创建模板中整组添加。' },
      { m: '规', t: '分数规则', d: '按总分区间打标签。' },
    ],
    edQs: [
      { tag: '文本题 · 必填', title: '你的微信昵称', ph: '多行文本输入', input: true, opts: [] },
      { tag: '手机号题 · 必填', title: '你的手机号', ph: '请输入手机号', input: true, opts: [] },
      { tag: '单选题 · 必填', title: '你目前所处的行业是?', ph: '', input: false, opts: ['美容 / 美发 / 美甲', '健康 / 养生 / 大健康', '教育培训 / 知识付费', '服装', '保险', '心理、疗愈', '零售 / 电商（含社交电商）', '高端餐饮', '珠宝', '形体礼仪', '空间、小院', '瑜伽、产康、普拉提', '其他'] },
    ],
    edAssignees: [{ name: '林小楷', uid: 'LinKaiYan', ratio: '100' }],
    chStats: [
      { label: '渠道总数', value: '14', unit: '独立获客资源' },
      { label: '渠道资产', value: '14', unit: '二维码 / 链接资源' },
      { label: '企微获客助手链接', value: '0', unit: '复制 / 分享链接' },
      { label: '渠道用户', value: '687', unit: '渠道进入记录' },
    ],
    channels: [
      { name: '扫码添加，邀请你进群共学', code: 'shalongyaoyue', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '下载二维码' },
      { name: '城市会员咨询', code: 'chengshi01', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '下载二维码' },
      { name: '城市会员咨询', code: 'chengshi02', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '生成二维码' },
      { name: '视频号8.8发布会', code: 'sph0808', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '1', qr: '下载二维码' },
      { name: '加我企业微信，兑换课程', code: 'duihuankecheng', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '19', qr: '下载二维码' },
      { name: '浅蓝测试渠道码', code: 'qianlan01', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '无标签', tagTone: 'gray', users: '1', qr: '下载二维码' },
      { name: '有赞店铺来源', code: 'youzan', type: '获客链接', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '8', qr: '下载二维码' },
      { name: '城市会员 3980', code: 'city3980', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '41', qr: '下载二维码' },
    ],
    orders: [
      { time: '2026-08-03 09:52', no: '4200002419202608', plat: 'PAY20260803A19', payer: '李思远', uid: 'wmA8c3DQAA_x91', product: '增长陪跑 · 90 天', amount: '¥2,980.00', status: '已支付', tone: 'ok', pay: '微信支付' },
      { time: '2026-08-03 08:14', no: '4200002419188473', plat: 'PAY20260803A02', payer: '陈曦', uid: 'wmA8c3DQAA_k27', product: '内容诊断单次', amount: '¥399.00', status: '已支付', tone: 'ok', pay: '微信支付' },
      { time: '2026-08-02 22:41', no: '4200002418902117', plat: 'PAY20260802B77', payer: '周雨桐', uid: 'wmA8c3DQAA_p04', product: '增长陪跑 · 30 天', amount: '¥1,280.00', status: '退款中', tone: 'warn', pay: '微信支付' },
      { time: '2026-08-02 20:03', no: '-', plat: 'PAY20260802B54', payer: '王一鸣', uid: '-', product: '增长陪跑 · 90 天', amount: '¥2,980.00', status: '未支付', tone: 'gray', pay: '微信支付' },
      { time: '2026-08-02 15:26', no: '4200002418655902', plat: 'PAY20260802A31', payer: '赵启明', uid: 'wmA8c3DQAA_h33', product: '私域陪跑年卡', amount: '¥9,800.00', status: '已退款', tone: 'red', pay: '支付宝' },
    ],
    orderKv: [
      { k: '微信单号', v: '4200002419202608', mono: true },
      { k: '商户单号', v: 'PAY20260803A19', mono: true },
      { k: '商品', v: '增长陪跑 · 90 天', mono: false },
      { k: '订单创建时间', v: '2026-08-03 09:52:14', mono: true },
      { k: '支付时间', v: '2026-08-03 09:52:41', mono: true },
      { k: '付款人', v: '李思远', mono: false },
      { k: '手机号', v: '138****4021', mono: true },
      { k: '客户身份', v: 'wmA8c3DQAA_x91', mono: true },
      { k: '金额', v: '¥2,980.00 CNY', mono: false },
      { k: '原始状态', v: 'SUCCESS / TRADE_SUCCESS', mono: true },
      { k: '已退款', v: '¥0.00', mono: false },
      { k: '退款处理中', v: '¥0.00', mono: false },
      { k: '可退款', v: '¥2,980.00', mono: false },
    ],
    orderEvents: [
      { time: '2026-08-03 09:52:14', ev: '创建订单', st: '成功', tone: 'ok' },
      { time: '2026-08-03 09:52:38', ev: '拉起支付', st: '成功', tone: 'ok' },
      { time: '2026-08-03 09:52:41', ev: '支付回调 payment.success', st: '成功', tone: 'ok' },
      { time: '2026-08-03 09:52:42', ev: '开通周期服务', st: '成功', tone: 'ok' },
      { time: '2026-08-03 09:52:43', ev: '外部推送 order.paid', st: '重试 1 次后成功', tone: 'warn' },
    ],
    spProducts: [
      { code: 'SP-GROW-90', name: '增长陪跑 · 90 天', price: '¥2,980.00', status: '已上架', tone: 'ok', sold: '412', updated: '2026-08-01 17:20' },
      { code: 'SP-GROW-30', name: '增长陪跑 · 30 天', price: '¥1,280.00', status: '已上架', tone: 'ok', sold: '1,036', updated: '2026-07-28 10:02' },
      { code: 'SP-VIP-365', name: '私域陪跑年卡', price: '¥9,800.00', status: '已上架', tone: 'ok', sold: '87', updated: '2026-07-19 09:41' },
      { code: 'SP-TRIAL-7', name: '体验期 7 天', price: '¥99.00', status: '未上架', tone: 'gray', sold: '2,204', updated: '2026-06-30 15:58' },
    ],
    coupons: [
      { name: '新客立减 100', code: 'xk100a', off: '¥100', scope: '增长陪跑 · 90 天 等 2 个', window: '08-01 00:00 – 08-31 23:59', issue: '1,200 / 486', status: '进行中', tone: 'ok' },
      { name: '老客续费 300', code: 'lk300b', off: '¥300', scope: '私域陪跑年卡', window: '07-15 00:00 – 08-15 23:59', issue: '300 / 212', status: '进行中', tone: 'ok' },
      { name: '直播专享 50', code: 'zb50c', off: '¥50', scope: '全部周期商品', window: '08-05 20:00 – 08-05 23:00', issue: '2,000 / 0', status: '未开始', tone: 'blue' },
      { name: '618 回馈券', code: 'c618d', off: '¥200', scope: '增长陪跑 · 30 天', window: '06-01 00:00 – 06-20 23:59', issue: '800 / 741', status: '已结束', tone: 'gray' },
      { name: '内测答谢券', code: 'nx150e', off: '¥150', scope: '体验期 7 天', window: '05-01 00:00 – 05-31 23:59', issue: '100 / 12', status: '已停用', tone: 'red' },
    ],
    images: [
      { resourceId: '1', name: '直播预告主视觉.png', size: '1080×1920 · 482 KB', tag: '直播', tone: 'blue', bg: 'linear-gradient(135deg,#DCE7FF,#B9CDFF)', desc: '每周三直播预告封面，含二维码区', tags: '直播,预告', enabled: true, uploadedAt: '2026-08-01 10:24', originalUrl: '/_test/materials/image/1' },
      { name: '课程卡片-增长.jpg', size: '750×1000 · 213 KB', tag: '课程', tone: 'purple', bg: 'linear-gradient(135deg,#EADCFF,#CFB6FF)', desc: '增长陪跑课程卡片竖版', tags: '课程,卡片', enabled: true, uploadedAt: '2026-07-30 16:02' },
      { name: '欢迎语配图.png', size: '900×600 · 156 KB', tag: '欢迎语', tone: 'ok', bg: 'linear-gradient(135deg,#D8F5DE,#AEE7BD)', desc: '新好友欢迎语横版配图', tags: '欢迎语', enabled: true, uploadedAt: '2026-07-28 09:41' },
      { name: '优惠券横幅.png', size: '1200×400 · 98 KB', tag: '活动', tone: 'warn', bg: 'linear-gradient(135deg,#FFE9CC,#FFD09B)', desc: '领券活动横幅', tags: '活动,优惠券', enabled: true, uploadedAt: '2026-07-26 20:15' },
      { name: '案例长图-01.jpg', size: '750×4200 · 1.2 MB', tag: '案例', tone: 'gray', bg: 'linear-gradient(135deg,#E9EBEF,#CFD3DA)', desc: '学员案例长图第一期', tags: '案例,长图', enabled: false, uploadedAt: '2026-07-18 14:33' },
      { name: '朋友圈九宫格-3.jpg', size: '1080×1080 · 340 KB', tag: '朋友圈', tone: 'blue', bg: 'linear-gradient(135deg,#DCE7FF,#AFC6FF)', desc: '九宫格第 3 张', tags: '朋友圈,九宫格', enabled: true, uploadedAt: '2026-07-15 11:08' },
      { name: '答疑海报.png', size: '1080×1440 · 512 KB', tag: '社群', tone: 'purple', bg: 'linear-gradient(135deg,#F0E2FF,#D6BBFF)', desc: '共学营答疑海报', tags: '社群,海报', enabled: true, uploadedAt: '2026-07-12 19:52' },
      { name: '门店物料码.png', size: '800×800 · 74 KB', tag: '渠道', tone: 'ok', bg: 'linear-gradient(135deg,#DAF3EC,#ADE0D2)', desc: '线下门店台卡二维码物料', tags: '渠道,门店', enabled: true, uploadedAt: '2026-07-08 08:47' },
    ],
    mpItems: [
      { name: 'AI 增长陪跑 · 报名', appid: 'wx8f2c1d0a9b7e4c12', pagepath: 'pages/enroll?from=crm', cardTitle: '点这里报名 AI 增长陪跑', thumbStatus: '企微缓存已就绪', thumbOk: true, enabled: true, bg: 'linear-gradient(135deg,#DCE7FF,#B9CDFF)' },
      { name: '共学营开营通知', appid: 'wx8f2c1d0a9b7e4c12', pagepath: 'pages/camp/open?term=8.6', cardTitle: '共学营今晚 20:00 开营', thumbStatus: '企微缓存已就绪', thumbOk: true, enabled: true, bg: 'linear-gradient(135deg,#D8F5DE,#AEE7BD)' },
      { name: '城市会员权益', appid: 'wx1a3b5c7d9e0f2a4b', pagepath: 'pages/vip/benefits', cardTitle: '查看你的城市会员权益', thumbStatus: '缩略图未上传企微', thumbOk: false, enabled: true, bg: 'linear-gradient(135deg,#FFE9CC,#FFD09B)' },
      { name: '试听课预约', appid: 'wx8f2c1d0a9b7e4c12', pagepath: 'pages/trial/booking', cardTitle: '点这里预约 1v1 试听课', thumbStatus: '企微缓存已就绪', thumbOk: true, enabled: false, bg: 'linear-gradient(135deg,#F0E2FF,#D6BBFF)' },
    ],
    attachItems: [
      { resourceId: '1', name: 'AI 增长陪跑服务说明.pdf', type: 'PDF', size: '2.4 MB', tags: '服务说明,陪跑', uploadedAt: '08-01 10:24', enabled: true },
      { name: '共学营课程表.xlsx', type: 'XLSX', size: '86 KB', tags: '课程表', uploadedAt: '07-30 16:02', enabled: true },
      { name: '开营致辞逐字稿.docx', type: 'DOCX', size: '132 KB', tags: '话术,开营', uploadedAt: '07-28 09:41', enabled: true },
      { name: '5天沙龙课程大纲.pdf', type: 'PDF', size: '1.1 MB', tags: '沙龙,大纲', uploadedAt: '07-22 15:37', enabled: true },
      { name: '会员权益手册.pdf', type: 'PDF', size: '3.8 MB', tags: '会员,权益', uploadedAt: '07-15 11:08', enabled: false },
    ],
    agents: [
      { name: '沙龙问卷 AI 跟进话术', code: 'salon_questionnaire_followup_agent', type: 'Agent 机器人', material: '0 图片 / 0 小程序 / 0 PDF / 0 群邀请', status: '启用中', tone: 'ok' },
      { name: '新建 Agent', code: 'new_agent_1784189809857', type: 'Agent 机器人', material: '0 图片 / 0 小程序 / 0 PDF / 0 群邀请', status: '启用中', tone: 'ok' },
      { name: '沙龙进群固定欢迎', code: 'salon_group_welcome_fixed', type: '固定话术', material: '1 图片 / 0 小程序 / 0 PDF / 1 群邀请', status: '已停止', tone: 'gray' },
    ],
    agentSlots: [
      { label: '插入 {{问卷信息}}' }, { label: '插入 {{最近20条聊天信息}} ' }, { label: '插入 {{用户标签}}' }, { label: '插入 {{激活信息}}' },
    ],
    agentDeps: [{ t: '问卷信息' }, { t: '最近20条聊天' }, { t: '用户标签' }, { t: '激活信息' }],
    products: [
      { code: 'AICRM-STD', name: 'AI 增长诊断单次', price: '299.00 CNY', status: '已上架', tone: 'ok', sold: '1,284', updated: '2026-08-03 09:41' },
      { code: 'CAMP-5D', name: '5 天共学营 · 单期', price: '199.00 CNY', status: '已上架', tone: 'ok', sold: '862', updated: '2026-08-01 10:24' },
      { code: 'WORKSHOP-1', name: '线下工作坊门票', price: '1980.00 CNY', status: '草稿', tone: 'gray', sold: '0', updated: '2026-07-28 16:32' },
      { code: 'TRIAL-1', name: '试听课', price: '0.00 CNY', status: '已下架', tone: 'gray', sold: '412', updated: '2026-06-11 11:20' },
    ],
  },
};

/** H5 用户端种子数据（问卷选项 / 测评维度） */
export const SEED_H5: H5Data = {
  single: [
    { text: '获客成本太高', on: true },
    { text: '转化率上不去', on: false },
    { text: '老客复购少', on: false },
    { text: '团队执行不下去', on: false },
  ],
  multi: [
    { text: '抖音 / 视频号', on: true, kind: 'box' },
    { text: '小红书', on: false, kind: 'box' },
    { text: '私域社群', on: true, kind: 'box' },
    { text: '线下活动', on: false, kind: 'box' },
    { text: '其它', on: true, kind: 'box' },
  ],
  step: [
    { text: '视频号直播', on: true },
    { text: '小红书内容', on: false },
    { text: '公众号文章', on: false },
    { text: '线下门店', on: false },
    { text: '其它', on: false },
  ],
  blank: [
    { text: '视频号直播', on: false },
    { text: '小红书内容', on: false },
    { text: '公众号文章', on: false },
  ],
  dims: [
    { name: '流量获取', score: 34, max: 40, desc: '渠道数量与内容频次都达标，直播是当前最稳的来源。', tone: 'ok' },
    { name: '线索承接', score: 16, max: 30, desc: '首触平均 4.2 小时，超过一半线索在 24 小时内没有被跟进。', tone: 'warn' },
    { name: '复购运营', score: 18, max: 30, desc: '到期提醒没有固定动作，续费主要靠客户主动询问。', tone: 'warn' },
  ],
};

export function deepCopy<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}
