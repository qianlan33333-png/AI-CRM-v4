# AGENTS.md

## 日常开发与发布

使用 [开发入口](docs/development-before-start.md) 和 [日常国内发布](docs/operations/domestic-main-release.md)。用户已选择国内源码、GitHub 人工择机归档；GitHub PR/check、merge-preview、freshness、handoff ACK 与观察占位不是日常部署门禁。旧发布说明只作历史查询。

唯一发布工作台由 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json` 的 `thread_id` 指定，不在 skill 或旧对话中固定 ID。开发任务实际推送国内准确候选 ref 后，重新读取路由并通过 `send_message_to_thread` 投递原任务 ID、base/head/tree/ref、五项影响、测试收据、未验证项和已有授权范围；仅写“等待发布”不算交付。开发任务不自行安装共享预发或生产。工作台在开始、恢复和发布写操作前核对路由；不指向自己即停止发布写操作。更换对话只交接现有队列、锁、attempt 和收据，按发布文档执行，不初始化第二条发布路径。

每个开发任务使用独立 `codex/<work-item>` worktree/分支，一个能力只准备一次业务流程、参考和简短父 PRD。复用已授权 brief，相关测试随完整行为交付。UI 编码前使用 Product Design；新增限制先应用核心 skill 的必要性判断。

同一发布者持原串行锁，按本地 AI 在 `docs/release-checks.json` 给出的短清单检查改动包及直接业务消费者。普通发布取消未知/共享/政策改动的全量回退、依赖闭包证明和全仓 vet/race。成员先累计源码，下一候选从累计源码 HEAD 出发；最终只构建、安装预发一次并覆盖相关冒烟。有“部署到生产机”授权即内部绑定准确范围和同一构件，直接晋级并独立读回，不二次询问。需要代理时用一个 `gpt-6-luna` max agent，不按预发、交接和观察拆任务。

普通发布不备份或迁移数据库；仅生产迁移前备份。文档/skill 不安装应用。真实支付、扫码及 Provider 结果另行记录，不阻塞下次技术发布、不冒称业务成功。部署结果不明只读对账，不盲目重装。基线落后或旧发布器仍可写时先停止对账，不能凭修改文档假定自动流程已启用。预备机保持 2 核 2GB。

本文件适用于整个 `AI-CRM-v4` 仓库。

## 1. 仓库地位

- v4 是唯一新能力主线。
- 当前 AI-CRM-v4 代码线是唯一代码、测试、构建、发布和部署来源；激活后的权威 `main` 位于预备机国内裸仓库，GitHub 可落后。禁止使用 AI-CRM-production、AI-CRM-v3、AI-CRM-v2、AI-CRM 或任何旧仓库 checkout、commit、donor SHA、donor manifest、运行时、数据库、接口和前端；旧系统缺失不得阻塞 v4。
- 新功能不得在 v4 和旧仓重复实现。
- 优先级：用户最新明确指令 > 本文件第 2 节“开发前最高优先级判断”与第 8 节“红线” > `docs/01-PRD-迁移范围与新仓库基线.md` > `docs/02-模块化开发与交付方案.md` > 本文件其他内容。

## 2. 开发前最高优先级判断

- 新功能、Bug 修复和调试先应用 `skills/aicrm-v3-development-frontdoor/SKILL.md` 的业务判断、参考调研、复用评估和 PRD。旧 handoff、merge-preview 与合并前发布快照只作历史审计。新增限制先应用核心 skill 的必要性判断。Skill 路径为兼容现有工具保留，所有证据只允许来自当前 v4 commit/tree。
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

- 一个候选交付一个独立可上线的用户可观察行为或明确缺陷，相关测试与行为放在同一提交；不设行数或文件数配额。数据库迁移按前向兼容设计，不承诺逆向回滚。
- 涉及 UI 时，编码前使用 Product Design 插件/skill；父 brief 已授权的拆分候选不重复请求确认。
- 不以目录存在、接口骨架、HTTP 200、Mock 或排队成功作为完成。
- 旧能力迁入前先冻结 Behavior Contract 和 Characterization/Journey 测试；禁止整目录复制后再清理。
- 临时兼容 Adapter 必须登记 Owner、替代路径和删除条件。

## 8. 红线

遇到以下情况立即停止该实现并报告：双主写、身份错误归属、支付/退款/Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域表写入、不可逆数据损坏、迁移静默丢数据。

## 9. 极简验证与发布

- 一个能力复用一份父 brief。AI 读取准确 diff、改动函数直接调用方和字段直接消费者，列出包、具名用例和简短原因；无关模块允许漏测风险。
- 普通推送运行清单中的相关检查。小包整包测，大包具名 `-run`；相关 Go 编译由测试及最终应用构建完成，不再强制全仓 fast/compile/vet/race。
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` 查看计划；普通运行只执行清单。未登记或共享文件不转全量；AI 补最近的合同即可。全量由显式维护命令执行。
- 清单与准确源码一起提交；用例真实运行，无匹配、skip、缺环境、超时不得算通过。macOS 纯单元有效结果可复用，Linux/PG/浏览器相关检查在所需环境执行一次。
- 修改身份、鉴权、支付幂等、事务或迁移时，只加强该变动的相关拒绝/重放/并发/数据保留断言；第 8 节红线继续生效。
- 失败修复只重跑失败用例及修复影响范围，不重跑整个阶段。业务断言完成且进程停止后的临时目录清理失败记录警告；业务结果或安装状态不明停止写入并只读对账。
- 发布使用 [极简流程](docs/operations/domestic-main-release.md)：原队列/锁，源码累计，最终国内构建一次、预发一次、相关冒烟、已有授权同包生产和独立读回。源码-only 变动不安装业务应用。
- UI 编码前仍使用 Product Design。共享组件按本次直接使用方选检查，不自动全页面回归。普通发布不备份数据库，生产迁移前才备份。
- 汇报准确 head/tree 和相关命令、结果及未验证项；局部检查不称完整回归，技术部署不称真实 Provider 成功。不新增审批、影子对照、全量证明或定时发布通道。
