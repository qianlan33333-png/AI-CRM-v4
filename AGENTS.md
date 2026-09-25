# AGENTS.md

## Development and domestic serial release

**权威切换以受锁的国内主仓激活收据为界。** 激活前，继续使用
`docs/operations/domestic-release.md` 的 GitHub PR、准确 `check` 和旧发布器；
激活后，以 `docs/operations/domestic-main-release.md` 为日常入口，GitHub 只是人工择机同步的历史归档。
不要仅凭本文件合并或安装就假定切换完成。新旧发布器不得同时拥有生产写入口。

每个开发任务使用新的 `codex/<work-item>` 分支/worktree。开始前阅读
`docs/development-before-start.md`，完成业务判断、GitHub/成熟产品参考与仓库复用评估，再形成一份简短父 PRD，
记录 OneID、Persistence、External Effects 分类。父 brief 一经授权，拆出的候选复用它，
只补充本次范围和验收；业务范围或外部合同实质变化时才重做判断。每个候选交付可单独上线的最小完整行为或明确缺陷，相关测试同提交；不设行数门槛。
涉及侧栏、用户展示页或后台页面时，编码前使用 Product Design 插件/skill。发布失败由单独的
`gpt-6-luna` max agent 诊断和修复，其他工作不受此模型限制。

激活后开发者只推国内裸仓的 `codex/*` 分支，不得直接改国内 `main`、共享预发目录或生产。
单一发布器核对候选基线和准确 SHA/tree，按影响范围测试、预发安装与业务合同验收，
在生产机先保存可验证的完整源码 bundle，再将预发同一安装包通过内网晋级。生产版本、
文件摘要、服务和健康读回通过后才 CAS 推进国内 `main`。过期分支由原开发任务更新重验，
发布器不 rebase 或解决冲突。纯文档候选只更新源码备份和游标，不伪造应用安装。
未知、共享基础、迁移或检查策略变化保守运行全量检查。普通发布不备份数据库；仅生产迁移
在迁移前备份，预备机使用可重建的合成数据。真实业务验收独立于技术安装，不占后续技术通道。
结果不明时停队列，只读对账，不盲目重装。GitHub 凭据只留在开发者电脑；发布器不自动推送，
也不规定同步周期。

本文件适用于整个 `AI-CRM-v4` 仓库。

## 1. 仓库地位

- v4 是唯一新能力主线。
- 当前 AI-CRM-v4 代码线是唯一代码、测试、构建、发布和部署来源；激活后的权威 `main` 位于预备机国内裸仓库，GitHub 可落后。禁止使用 AI-CRM-production、AI-CRM-v3、AI-CRM-v2、AI-CRM 或任何旧仓库 checkout、commit、donor SHA、donor manifest、运行时、数据库、接口和前端；旧系统缺失不得阻塞 v4。
- 新功能不得在 v4 和旧仓重复实现。
- 优先级：用户最新明确指令 > 本文件第 2 节“开发前最高优先级判断”与第 8 节“红线” > `docs/01-PRD-迁移范围与新仓库基线.md` > `docs/02-模块化开发与交付方案.md` > 本文件其他内容。

## 2. 开发前最高优先级判断

- 新功能、Bug 修复和调试先应用 `skills/aicrm-v3-development-frontdoor/SKILL.md` 的业务判断、参考调研、复用评估和 PRD。旧 handoff、merge-preview 与合并前发布快照只作历史审计。Skill 路径为兼容现有工具保留，所有证据只允许来自当前 v4 commit/tree。
- 除用户最新明确指令与安全红线外，任何设计、实现、迁移或代码审查在开始编码前，都必须优先判断两件事：是否涉及 OneID/外部身份，以及是否涉及持久化、内部持久任务或外部效果。
- 开发者必须先阅读并应用项目核心 Skill：`skills/aicrm-v3-development/SKILL.md`，在计划或候选说明中留下简短分类结论。
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

- 一个候选交付一个独立可上线、可回退的用户可观察行为或明确缺陷，相关测试与行为放在同一提交；不设行数或文件数配额。
- 涉及 UI 时，编码前使用 Product Design 插件/skill；父 brief 已授权的拆分候选不重复请求确认。
- 不以目录存在、接口骨架、HTTP 200、Mock 或排队成功作为完成。
- 旧能力迁入前先冻结 Behavior Contract 和 Characterization/Journey 测试；禁止整目录复制后再清理。
- 临时兼容 Adapter 必须登记 Owner、替代路径和删除条件。

## 8. 红线

遇到以下情况立即停止该实现并报告：双主写、身份错误归属、支付/退款/Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域表写入、不可逆数据损坏、迁移静默丢数据。

## 9. 提交前验证顺序

- 首次推送和修复后再次推送前，先运行 `python3 scripts/dev_preflight.py fast`；Go 改动再运行 `python3 scripts/dev_preflight.py compile`，然后执行受影响领域的专项测试。编译成功不等于测试通过。
- `fast`、`compile` 和局部 `browser` 的证据只能汇报对应局部结论，不能称为完整回归。激活前继续执行 GitHub 准确提交的必需 `check`；激活后由预备机在准确候选 SHA/tree 上执行受信任的影响检查和已安装业务合同。未知、共享基础设施、可执行检查策略和迁移变更全量检查；发布工具运行对应合同。预备机基础验收与生产安装读回是独立证据，真实支付、扫码只由生产技术部署后的业务方验收。
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` 可显示候选计划。普通运行执行计划中的 lane、受影响 Go 包全集及登记检查；缺少环境、收据或执行结果时报不完整。macOS 缺 Linux 浏览器环境时，在预备机补齐，不能凭本地局部测试宣称候选通过。
- 旧 PR2 的十 PR 影子试验是 GitHub 门禁优化的历史记录；切换本身不证明快速检查安全。新国内发布器只能按已验证的影响规则收窄，否则回退全量；确认漏选或未知结果立即关闭相关快速路径。
- CI 失败先重现准确失败用例，修复后重跑完整失败阶段；提交前按更新后的影响计划重新计算适用检查范围。不能通过删断言、接受 skip 或反复推送猜测修复。
- 新增真实 Host 浏览器旅程放在 `cmd/aicrm`，使用 `Test…ChromiumJourney` 命名，自动进入必跑集合；其他包或命名必须明确接入。运行 `python3 scripts/dev_preflight.py browser` 前准备最终 Host 产物和独立 PostgreSQL 16 测试库。
- 测试汇报附准确 HEAD、tree、工作区状态、命令及证据目录，区分编译、专项、本地完整、预备机检查、取消、跳过和未验证；修改代码后不能沿用旧 HEAD 绿灯。激活前的 PR 质量保留最早与当前 head 的准确 CI attempt；激活后的候选以国内 SHA/tree 和检查收据为准。
- 共享 Composition、构建和工作流改动先核对并行任务，避免重复修复；不能恢复手工维护的 Chromium 用例正则。操作细则见 `docs/plans/2026-09-08-development-preflight.md`。
