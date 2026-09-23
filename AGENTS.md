# AGENTS.md

## Development window and release command center

每个开发任务使用新的 `codex/<work-item>` 分支/worktree 和新 PR。开始前阅读
`docs/development-before-start.md`，先完成业务判断、GitHub 参考检索、仓库复用评估
和经确认的 PRD，再记录 OneID、Persistence、External Effects 分类。开发完成状态按
`code_complete`、`staging_built`、`staging_self_accepted`、`handoff_ready` 四级记录；
未达到后一级时不得用前一级冒称完成。该四级链用于运行时变更；纯治理/文档变更在
`code_complete` 后以治理检查证据直接进入 `handoff_ready`，并将 `staging_built`、
`staging_self_accepted` 标为 N/A。

运行时 handoff 必须绑定准确 PR、base main、PR head、merge-preview、各自 tree、
package SHA-256、staging receipt 和受影响业务 readback。纯治理/文档变更必须标记
`change_class=governance_only`，明确 runtime package、staging app install 和业务运行时
读回均为 N/A，并提交治理检查证据，不得伪造运行时收据。

开发任务完成或收到返工后，必须先追加不可变持久事件，再通知发布指挥台。返工必须
生成新 commit、新 candidate、新证据和新事件；旧候选、旧 receipt 和旧事件不可覆盖。
指挥台只做只读判断、串行排队、合并、部署、观察和打回，不修改候选源码、PR、分支
或 worktree。公开仓库 `main` 的 GitHub 保护是合并门禁；串行发布仍由指挥台队列负责，
不假定 GitHub 原生 Merge Queue。

生产晋级只使用预发布验收的同一包。`outcome_unknown` 时停止重试并只读对账；用户明确
延期真实业务验收时，生产状态最多保持 `observing`，不得写成 `released`。

本文件适用于整个 `AI-CRM-v4` 仓库。

## 1. 仓库地位

- v4 是唯一新能力主线。
- 当前 AI-CRM-v4 仓库是唯一代码、测试、构建、发布和部署来源。禁止使用 AI-CRM-production、AI-CRM-v3、AI-CRM-v2、AI-CRM 或任何旧仓库 checkout、commit、donor SHA、donor manifest、运行时、数据库、接口和前端；旧系统缺失不得阻塞 v4。
- 新功能不得在 v4 和旧仓重复实现。
- 优先级：用户最新明确指令 > 本文件第 2 节“开发前最高优先级判断”与第 8 节“红线” > `docs/01-PRD-迁移范围与新仓库基线.md` > `docs/02-模块化开发与交付方案.md` > 本文件其他内容。

## 2. 开发前最高优先级判断

- 新功能、Bug 修复、调试、合并和上线前置流程统一先应用 `skills/aicrm-v3-development-frontdoor/SKILL.md`；新功能必须完成市场/GitHub 调研、复用评估和已确认 PRD，且合并前必须完成并行与发布快照。Skill 路径为兼容现有工具保留，所有证据只允许来自当前 v4 commit/tree。
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

- 一个 PR 交付一个用户可观察能力或一个明确缺陷。
- 不以目录存在、接口骨架、HTTP 200、Mock 或排队成功作为完成。
- 旧能力迁入前先冻结 Behavior Contract 和 Characterization/Journey 测试；禁止整目录复制后再清理。
- 临时兼容 Adapter 必须登记 Owner、替代路径和删除条件。

## 8. 红线

遇到以下情况立即停止该实现并报告：双主写、身份错误归属、支付/退款/Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域表写入、不可逆数据损坏、迁移静默丢数据。

## 9. 提交前验证顺序

- 首次推送和修复后再次推送前，先运行 `python3 scripts/dev_preflight.py fast`；Go 改动再运行 `python3 scripts/dev_preflight.py compile`，然后执行受影响领域的专项测试。编译成功不等于测试通过。
- `fast`、`compile` 和局部 `browser` 的证据只能汇报对应局部 claim，不能称为完整回归或可交付验证。需要本地完整证据时运行 `python3 scripts/dev_preflight.py full`；它要求开始、每个 lane 前后及结束时都是同一干净已提交树，并拒绝缺 PostgreSQL 16、Linux amd64 Chromium、固定工具或仓内已登记视图源的环境。具备预发布机时，完整本地证据随后必须在 `49.232.57.128` 做真实部署、健康检查和业务读回。运行时、测试/fixture、CI、部署、迁移、共享平台和发布状态机改动的 PR 必须在当前 head 跑完整 GitHub 代码 CI；PR check 不认证预发收据。运行时预发包、业务读回和 accepted receipt 必须在 `release_control handoff` 入队前独立核验。纯文档变更可使用轻门禁；`workflow_dispatch force_full=true` 只证明触发分支的代码树，不能冒充 PR required check 或预发验收。
- CI 失败先重现准确失败用例，修复后跑完整失败阶段，再提交全量 CI；不能通过删断言、接受 skip 或反复推送猜测修复。
- 新增真实 Host 浏览器旅程放在 `cmd/aicrm`，使用 `Test…ChromiumJourney` 命名，自动进入必跑集合；其他包或命名必须明确接入。运行 `python3 scripts/dev_preflight.py browser` 前准备最终 Host 产物和独立 PostgreSQL 16 测试库。
- 测试汇报附准确 HEAD、tree、工作区状态、命令及证据目录，区分编译、专项、本地完整、完整 CI、取消、跳过和未验证；修改代码后不能沿用旧 HEAD 绿灯。PR 首轮质量保留该 PR 最早 CI attempt 的原始 SHA；最终质量只对应当前 PR head 的最新 attempt。
- 共享 Composition、构建和工作流改动先核对并行任务，避免重复修复；不能恢复手工维护的 Chromium 用例正则。操作细则见 `docs/plans/2026-09-08-development-preflight.md`。
