# AGENTS.md

## Development and domestic serial release

每个开发任务使用新的 `codex/<work-item>` 分支/worktree 和 PR。开始前阅读
`docs/development-before-start.md`，完成业务判断、GitHub 参考与仓库复用评估，再形成一份简短父 PRD
并记录 OneID、Persistence、External Effects 分类。父 brief 一经授权，拆分出的 PR 复用该 brief，
只补充各自范围和验收，不重复请求确认；只有业务范围或外部合同发生实质变化时才重新确认。
每个 PR 交付一个独立可合并、可回退的用户可观察行为或明确缺陷，相关测试同 PR；按行为边界拆分，不设行数门槛。
涉及侧栏、用户展示页或后台页面时，编码前使用 Product Design 插件/skill。整个实施留在同一 Codex task。
发布失败诊断和修复由单独的 `gpt-6-luna` max agent 执行；其他工作不受此模型限制。

GitHub 是唯一主仓库和 PR 审核入口。受保护 `main` 只接受准确 head 的必需 `check`；
CI 工作流始终启动，最终 `check` 汇总实际需要的检查，不能用 `paths` 过滤掉必需工作流。
已登记的发布工具变更运行对应合同测试；可执行检查策略、未知、共享构建基础或迁移改动
保守运行完整检查。GitHub Actions 不构建或运输生产包。不同板块可并行提 PR，
合并后由预备机按 `main` 第一父链顺序逐个构建、基础验收并通过内网将同一版本晋级生产。
纯文档变更不安装；普通页面改动不备份数据库、不运行迁移；数据库变更必须经过
兼容性检查、自动备份和迁移专项检查。生产技术安装成功与真实业务验收分开记录，
真实业务验收未完成不占用后续技术发布通道。

旧 merge-preview、candidate handoff、release-control 观察占位只作为历史审计，
不再是新 PR 的合并或部署门禁。发布操作和结果不明处置见
`docs/operations/domestic-release.md`。
普通页面/程序发布不备份数据库。备份规则由受保护主机角色固定：预备机只用可重建的合成数据，
不做数据库备份；生产数据是真实数据，只有数据库迁移在运行迁移前备份。缺失或不符的角色配置必须停止，
PR 参数不能关闭生产备份。

本文件适用于整个 `AI-CRM-v4` 仓库。

## 1. 仓库地位

- v4 是唯一新能力主线。
- 当前 AI-CRM-v4 仓库是唯一代码、测试、构建、发布和部署来源。禁止使用 AI-CRM-production、AI-CRM-v3、AI-CRM-v2、AI-CRM 或任何旧仓库 checkout、commit、donor SHA、donor manifest、运行时、数据库、接口和前端；旧系统缺失不得阻塞 v4。
- 新功能不得在 v4 和旧仓重复实现。
- 优先级：用户最新明确指令 > 本文件第 2 节“开发前最高优先级判断”与第 8 节“红线” > `docs/01-PRD-迁移范围与新仓库基线.md` > `docs/02-模块化开发与交付方案.md` > 本文件其他内容。

## 2. 开发前最高优先级判断

- 新功能、Bug 修复和调试先应用 `skills/aicrm-v3-development-frontdoor/SKILL.md` 的业务判断、GitHub 调研、复用评估和 PRD。旧 handoff、merge-preview 与合并前发布快照指引已由本文件的新流程取代。Skill 路径为兼容现有工具保留，所有证据只允许来自当前 v4 commit/tree。
- 除用户最新明确指令与安全红线外，任何设计、实现、迁移或代码审查在开始编码前，都必须优先判断两件事：是否涉及 OneID/外部身份，以及是否涉及持久化、内部持久任务或外部效果。
- 开发者必须先阅读并应用项目核心 Skill：`skills/aicrm-v3-development/SKILL.md`，在计划或 PR 中留下简短分类结论。
- 这是一项优先设计检查，不是要求所有功能都接入 OneID 或 External Effects。确实不涉及时，应明确记录“不涉及”及理由，随后按本领域正常边界开发，禁止为了过门禁而制造虚假依赖。
- 涉及客户、渠道身份、外部用户标识或客户归属时，必须优先复用 OneID/Identity Port；不得自建第二套客户主键、身份匹配、隐式建客或自动合并机制。
- 涉及可恢复异步执行时，必须先区分内部持久任务、Provider 读取和 Provider 写入。内部持久任务复用 `internal/platform/jobqueue`；企微业务写统一经 `outbound` 并协调 `internal/externaleffects/port`，不得在业务模块自建队列、Worker、lease/fence、重试或对账状态机。
- 业务状态、幂等收据、审计、Outbox 与外部效果接受需要原子提交时，必须验证它们参与同一个 PostgreSQL Unit of Work；不得假设两个独立事务等价于原子提交。
- 任何新模块只允许通过稳定 Port 或版本化事件协调 OneID 与 External Effects，禁止跨领域访问它们的表，或 import 其 `app`、`store`、`http`、`worker`、`provider`。

## 3. 固定架构

- Go 模块化单体、PostgreSQL 16、单企业、单数据库。
- `cmd/aicrm` 是唯一 Composition Root；只负责加载配置、创建平台设施、注册模块和启动角色。
- 跨领域只允许 import `internal/<domain>/port`、稳定值对象或使用版本化领域事件。
- 禁止跨领域 import `app`、`store`、`http`、`worker`、`provider` 或生成物。
- `internal/platform` 不得 import 业务领域。
- 每张业务表只有一个 Owner；Store 只访问本领域拥有的表。
- 不自行引入微服务、多租户、Redis、Kafka、进程内 cron/ticker 或 Kubernetes 前置。

## 4. OneID

- `customers.id` 是唯一渠道中立业务主键。
- 外部身份归 `identity`，必须包含 kind、scope、value、assurance、source。
- OpenID 没有 App scope 时不得匹配；UnionID 没有开放平台 scope 时不得跨渠道关联。
- `verified` 只能由完成 Provider 验证的内部 Adapter 构造；HTTP 请求体不能自报升级。
- 无唯一可信证据时保持 pending/conflict，不猜测客户。
- `Resolve` 只解析，不隐式建客；建客必须走显式 `ProvisionCustomerFromVerifiedIdentity`。
- Identity 首版不做破坏性自动合并；跨 Customer 根连接形成可审计 merge candidate。

## 5. 数据与事务

- 业务状态、幂等收据、审计和 Outbox 必须在同一 PostgreSQL 事务提交或回滚。
- Provider 网络调用不得持有数据库事务。
- 并发更新使用显式锁、CAS 或版本号。
- v4 延续本仓已有迁移序列，不复制任何旧仓 migration 历史。
- 迁移工具放在 `cmd/migrate-*`，运行时包不得 import 迁移器。

## 6. 外部效果

- Provider 默认 disabled。
- 外部调用必须区分 accepted、queued、attempted、executed、outcome_unknown、reconciled。
- `outcome_unknown` 禁止盲目换幂等键重试。
- 支付、退款、企微写入和群发必须具备签名/验签、幂等、回调重放、对账和审计。
- Token、Cookie、OAuth code、Secret、私钥、openid、external_userid、手机号不得进入结构化日志。
- 只有 `outbound` 可以拥有企微业务写调用；其他模块只提交意图。

## 7. 开发单位

- 一个 PR 交付一个独立可合并、可回退的用户可观察行为或明确缺陷，相关测试与行为放在同一 PR；不设行数或文件数配额。
- 涉及 UI 时，编码前使用 Product Design 插件/skill；父 brief 已授权的拆分 PR 不重复请求确认。
- 不以目录存在、接口骨架、HTTP 200、Mock 或排队成功作为完成。
- 旧能力迁入前先冻结 Behavior Contract 和 Characterization/Journey 测试；禁止整目录复制后再清理。
- 临时兼容 Adapter 必须登记 Owner、替代路径和删除条件。

## 8. 红线

遇到以下情况立即停止该实现并报告：双主写、身份错误归属、支付/退款/Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域表写入、不可逆数据损坏、迁移静默丢数据。

## 9. 提交前验证顺序

- 首次推送和修复后再次推送前，先运行 `python3 scripts/dev_preflight.py fast`；Go 改动再运行 `python3 scripts/dev_preflight.py compile`，然后执行受影响领域的专项测试。编译成功不等于测试通过。
- `fast`、`compile` 和局部 `browser` 的证据只能汇报对应局部 claim，不能称为完整回归。GitHub 必需 `check` 按改动范围选测试；未知、共享基础设施、可执行检查策略和迁移变更运行完整检查，已登记的发布工具使用对应合同检查。合并后的准确 SHA 还须有成功的 `check`，预备机才构建并安装。预备机基础验收与生产安装读回是独立证据，不能用 CI 代替。真实支付、扫码验收只能在生产技术部署后由业务方记录。
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` 只显示候选影子计划。无 `--dry-run` 时仍执行本地 `fast`，Go 改动再执行 `compile`；输出只证明本地范围，GitHub 当前必需检查保持不变。候选须先积累 10 个有效 PR，零已知漏选、配对中位耗时至少降低 30%、且每个能力的检查总耗时不增加；达到已授权标准后才启用，否则继续影子观察。
- CI 失败先重现准确失败用例，修复后重跑完整失败阶段；提交前按更新后的影响计划重新计算适用检查范围。不能通过删断言、接受 skip 或反复推送猜测修复。
- 新增真实 Host 浏览器旅程放在 `cmd/aicrm`，使用 `Test…ChromiumJourney` 命名，自动进入必跑集合；其他包或命名必须明确接入。运行 `python3 scripts/dev_preflight.py browser` 前准备最终 Host 产物和独立 PostgreSQL 16 测试库。
- 测试汇报附准确 HEAD、tree、工作区状态、命令及证据目录，区分编译、专项、本地完整、完整 CI、取消、跳过和未验证；修改代码后不能沿用旧 HEAD 绿灯。PR 首轮质量保留该 PR 最早 CI attempt 的原始 SHA；最终质量只对应当前 PR head 的最新 attempt。
- 共享 Composition、构建和工作流改动先核对并行任务，避免重复修复；不能恢复手工维护的 Chromium 用例正则。操作细则见 `docs/plans/2026-09-08-development-preflight.md`。
