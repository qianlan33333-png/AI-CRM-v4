---
name: aicrm-v3-development-frontdoor
description: "AI-CRM-v4 的开发前置门禁与并行交付流程（路径名为兼容保留）。用于新功能、Bug 修复、调试和发布交接。"
---

# AI-CRM-v4 开发前置门禁

## 开发窗口交付硬门槛

开发窗口用四级状态表达可核验进度：

1. `code_complete`：实现与适用本地验证完成，代码形成干净 commit。
2. `staging_built`：当前 `main + PR head` 的 merge-preview 已在唯一 Linux amd64
   预发布节点构建，package 与 built receipt 均绑定准确 SHA/tree。
3. `staging_self_accepted`：原开发任务已完成受影响业务旅程和技术读回，accepted
   receipt 与旅程证据可校验。
4. `handoff_ready`：PR、候选、包、receipt、风险、回滚点和持久事件均完整，可以
   交给发布指挥台。

这条四级链用于运行时变更。纯治理、Skill 和文档变更在 `code_complete` 后完成适用的
governance checks 即可进入 `handoff_ready`；`staging_built`、`staging_self_accepted`、
runtime package 和 staging app install 必须明确标记 N/A，不得伪造占位证据。

未达到后一级时不得用前一级冒称完成。用户明确延期真实业务验收时，必须生成独立
deferred acceptance；技术检查不可延期，生产状态最多保持 `observing`。返工产生新
commit、新候选、新 receipt 和新事件，不能覆盖已入队提交。

预发布使用虚拟 Provider 时，`staging_self_accepted` 只证明带 `effect_mode=virtual` 的
本地合同、状态转换和业务读回通过；不得写成真实 Provider 调用或 live 业务验收通过。

## 唯一代码来源

所有开发、测试、构建、发布和部署只允许使用当前 AI-CRM-v4 仓库的准确
commit 和 Git tree。禁止旧仓库 checkout、旧 commit、donor SHA、donor
manifest、旧运行时、旧数据库、旧接口或旧前端；缺失旧系统不得阻塞 v4。
每个发布包必须生成 provenance sidecar，记录 repository、commit SHA、tree
SHA、package SHA-256、构建环境和构建命令。

先阅读 `AGENTS.md`、`skills/aicrm-v3-development/SKILL.md`；涉及前端时再阅读 `skills/aicrm-v3-frontend-consistency/SKILL.md`。详细字段见 [开发前置流程与上线验收标准](../../docs/plans/开发前置流程与上线验收标准.md) 和 [PRD 前置模板](../../docs/prd/开发前置PRD模板.md)。

## 开始前分类

记录：

```text
OneID：不涉及 | 读取 canonical customer | 解析身份 | 建立客户 | 关联/合并身份
Persistence：stateless | 本地事务 | 内部持久任务 | Provider 读取 | Provider 写入/外部效果
```

不涉及的轴必须写明原因，不得制造虚假依赖。

## 新功能三步门禁

编码前必须完成：

1. 至少 2 个成熟产品/公开方案和至少 1 个高质量 GitHub 参考；找不到时记录搜索范围和结论。
2. 评估仓库已有领域、共享组件、标准组件、OneID、持久化和 External Effects，明确采用、扩展、舍弃。
3. 形成完整 PRD，包含业务逻辑、成功标准、接口/数据/权限边界、架构分类、测试、上线、监控、回滚和并行依赖。

PRD 经用户确认后冻结；重大范围、合同或风险变化必须重新确认。未完成三步不得正式编码。

Bug 修复也必须先写清业务判断和根因假设，检索 GitHub/公开实现用于验证标准做法，并
形成与影响面相称的修复 PRD 或缺陷合同；不得因范围较小而跳过复用评估和验收口径。

## 一次闭环交付

当所有假设和风险边界确认、用户明确开始开发后，开发任务持续推进到
`handoff_ready` 并发送持久事件；收到返工后由原开发任务修复并重新交接。发布指挥台
负责串行合并、正式部署和观察。只有新的业务决策、红线风险、凭据/权限缺失或证据
不一致才暂停并报告。

开发终点：实现 → `code_complete` → `staging_built` → `staging_self_accepted` →
准确 handoff 与持久事件 → `handoff_ready`。指挥台终点：GitHub 保护检查 → 串行合并 →
同一包晋级生产 → 认证和真实业务读回 → 观察窗口验收。本地不构建生产包。

生产部署完成且仅在版本、健康状态、认证读回、真实业务读回和观察窗口全部通过后，调用 Codex 的
`mcp__codex_app__fire_confetti` 一次庆祝本次上线。构建成功、PR 合并、部署开始、服务启动或仅有
`/readyz` 通过都不能触发；部署失败、回滚、结果未知或观察窗口未结束时不得触发。一次上线事件只触发一次，重复重试同一部署不得重复庆祝。

常规发布先在 `49.232.57.128` 预发布机从当前 v4 准确候选编译、安装并完成受影响板块验收，再合并 PR；receipt 必须绑定 repository、commit、tree、package SHA-256、capability 和 business readback。公开 `main` 的实时读回由指挥台完成并签发短时 freshness attestation；预发布只保存验签公钥，不保存 GitHub 凭据或长期代理。合并后由预发布机在发布锁下把同一已验收包串行晋级到 `124.220.53.183`，不重新编译和打包。

运行时 handoff 必须准确记录 GitHub PR URL、实时 base main SHA/tree、PR head SHA/tree、
双亲正确的 merge-preview SHA/tree、package SHA-256、staging built/accepted receipt 摘要，
以及每个受影响板块的业务旅程、预期、实际结果和证据摘要。任何字段过期或不一致都回到
新候选流程。纯治理、Skill 和文档变更使用 `change_class=governance_only`：runtime package、
staging app install、业务运行时读回均明确为 N/A，只提交与变更相称的治理检查证据；禁止
为通过 handoff 伪造 package 或 staging receipt。

发布包只允许在预发布机按 Linux amd64 目标编译，发布前执行 `scripts/check-release-binaries.py` 和 `scripts/check-migration-sequence.py`；安装器在切换 current 前再次拒绝非 Linux x86-64 ELF，避免 `status=126` 才发现架构错误。发布包统一由当前仓库 Python archiver 创建，拒绝 `._*` AppleDouble、symlink、未注册文件和非 Linux ELF；`release-files.sha256` 必须在预发布、生产安装器和 success observer 中通过。

当前已验证 SSH 账号为 `ubuntu`，通过 `deploy/run-release-as-root.sh` 持有 root fd 9 后执行 installer；禁止从普通 sudo 调用中传递失效的锁描述符。使用 `-i /Users/qianlan/Downloads/zhengshi.pem -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes`，显式选择已核验的 known_hosts；禁止尝试其他账号或绕过校验。上传前后独立比对 SHA-256，逐个检查 `bin/` 下文件为 Linux x86-64 ELF，纯 Go 默认 `CGO_ENABLED=0`，SDK runner 由现有脚本单独开启 cgo。构建必须来自准确候选 tree 的干净独立 checkout。

同一发布仅允许一个任务负责构建、上传、安装和观察，其他任务排队；不得共用可被另一任务改写的发布目录或归档路径。安装失败先保留证据并检查实际 active SHA；没有新证据不重复安装，不直接修改运营配置或跳过 bootstrap。失败候选需隔离时，先在发布锁下确认未运行、无成功收据和其他引用，不能删除正在使用的 release。

## 并行开发与发布队列

每个板块、Bug 或调试任务登记负责人、分支/worktree、状态、修改范围、共享入口、依赖项和预计上线窗口。开发与合并前盘点活跃分支、PR、Debug 任务、重叠文件/模块、迁移/API/组件合同、未部署提交、排队版本、部署和观察窗口。

- 每个板块使用独立 `codex/` 分支或 worktree。
- Composition、公共组件、迁移、CI、Provider 和部署脚本串行合并。
- 普通领域可并行，但合并前同步最新 `main` 并重跑受影响验证。
- Debug 分支不能作为稳定依赖；必须先形成可验证提交或明确临时接口。
- 一次只执行一个生产部署；部署后独立读回版本、迁移、健康、管理员页面和真实业务结果。

存在部署未完成、未知结果、回滚未完成、共享合同顺序依赖或过期 HEAD 时暂停合并并重新排队。

开发任务完成和返工都必须先把事件写入持久事件存储，再通过 Codex 消息通知指挥台；
消息送达只证明通知到达，不证明事件已处理。指挥台打回后，原开发任务负责修改源码并以
新 commit、新 candidate、新 receipt 和新事件重新交接；旧候选及其证据保持不可变。
指挥台只读核对 handoff、GitHub 和 receipt，负责串行队列、合并、部署、观察与打回，
绝不在指挥台任务中修改候选源码、PR、分支或 worktree。

## Bug 修复

保存真实失败证据，判断根因和影响面，先补回归测试或旅程，再做最小根因修复，执行原失败阶段和受影响全链路，并记录防复发措施。除非用户明确标注线上热修，不得走简化流程。

## 前端门禁

新增或修改前端必须使用 [@Product Design](plugin://product-design@openai-curated-remote) 和 [$artifact-template-crm](/Users/qianlan/.codex/skills/artifact-template-crm/SKILL.md)，优先复用管理端壳、标准选择器和共享组件。禁止单页私造标签、话术、群组、客服、商品等标准组件；仅文案、样式 token 或既有页面缺陷修复可走 audit/consistency 复核。

## 测试与合并

按影响范围先在本地执行适用的静态、编译/单元、PostgreSQL/迁移/事务、集成/权限、前端构建与真实浏览器、OneID、External Effects、发布包和回滚检查；涉及运行时代码的 PR 再在预发布机完成完整部署、合成数据业务旅程和读回。运行时、测试/fixture、CI、部署、迁移、共享平台与发布状态机改动须在当前 PR head 通过完整 GitHub 代码 CI；GitHub PR check 不认证预发收据。`release_control handoff` 核对本地包、receipt 与旅程摘要；指挥台必须在串行合并前另行核对签名来源与可信预发节点的包和读回，不能把开发者自备文件当来源证明。纯治理、Skill 和文档变更不要求无意义的应用部署 receipt；`workflow_dispatch force_full=true` 仅证明触发分支代码树，不替代 PR required check。

PR 保留准确 HEAD/tree、并行与发布快照、测试摘要、证据目录和未验证项。作者声明只作为上下文，不作为独立上线门禁；真正门禁是当前 head/tree、预发布 receipt、治理安全检查和部署后读回。编译通过、排队成功、Mock 或 HTTP 202 都不能单独称为完成。

## PR 证据留存

- PR 首轮 run/attempt/head/结果一旦产生不得覆盖；后续修复只追加事件。
- 当前 head 的最终结果只能由当前 head 的最新完整 required check 决定，旧 head 的绿灯不能替代。
- 固定分类为 `assertion_or_verification_failure`、`environment_setup_failure`、`cancelled`、`pending_or_incomplete`、`unknown_failure`；本地环境阻塞不能写成代码失败，替代环境通过不能冒称原环境通过。
- 每次修复必须记录修复提交、重跑阶段和最终结果；PR 正文只放轻量摘要与 artifact 链接，详细日志留在 artifact。
- 合并前必须有首轮和最终证据，未完成 lane、取消、skip、unknown 必须显式列出；合并后继续记录 merge SHA、部署 SHA、认证读回和观察窗口。
- 轻量 quality summary、首轮失败 manifest 和最终 check manifest 保留 90 天；详细 lane 日志、截图和大型测试包沿用 14/30 天策略。

### 国内预发布机与合并晋级

当前发布源是公开 AI-CRM-v4 仓库中实时 `main + PR head` 生成的准确 merge-preview。
指挥台只读 GitHub 当前 main/head，生成增量或完整 candidate bundle，并签发绑定
repository、main/head/preview/tree、bundle 摘要和短时有效期的 freshness attestation。
预发布机使用固定 allowed-signers 公钥离线验签并执行 `git bundle verify`；它不得依赖
GitHub 网络、保存 GitHub 凭据或默认远端 clone。预发布机是唯一 Linux amd64 构建节点，
必须完成二进制架构、包清单、安装、健康和受影响板块真实读回。

预发布构建必须持有 `/opt/aicrm/staging-build.lock` 单飞锁，锁覆盖构建目录清理、checkout、编译和 receipt 生成。并行构建不得共享目录或互相删除产物。`built` receipt 只能证明包来源，不能授权生产；完成安装和业务读回后才可写入 `accepted`。

受保护 `main` 只接受 PR merge commit。合并 SHA 可以与 staging 候选的 merge-preview
SHA 不同，但合并后的 tree 必须与已验收 candidate tree 完全一致，并验证 accepted
receipt 的 package SHA。生产使用 staging 已验收的同一包，不重新编译；任何 tree、
receipt、包摘要或发布队列不一致都停止晋级。若未来修改合并策略，必须先同步更新保护
规则、指挥台合同和验证脚本，并继续失败关闭地核对合并后 tree 与已验收 candidate tree。

### 预发布板块验收不可被无关 Provider 阻塞

每次 staging receipt 必须带能力声明：`affected_modules`、`required_routes`、`required_services`、`required_provider_dependencies` 和 `business_readback`。验收只执行声明的当前板块及明确共享依赖。未声明的 Provider 返回 `503 distribution_unavailable` 时必须用 `scripts/validate-staging-capability.py` 分类为 `external_config_unavailable` 并保留观察证据；不得把它写成代码失败，也不得阻塞当前板块。若该 Provider 在 `required_provider_dependencies` 中，则 503 仍是阻塞。Payment checkout 变更不自动要求 Distribution 商品链路；Distribution 变更才要求真实商品、资格和 Provider 读回。

验收辅助脚本不生成 accepted receipt，也不执行 HTTP 请求。required_routes 是非空路径字符串数组；observations 必须覆盖全部必需路由/Provider，并记录 status 与业务断言 business_verified。HTTP 200 本身不能替代业务验收。支付板块默认使用 acceptance_mode=virtual：在预发布用本地虚拟支付适配器或确定性数据库事实验证待支付订单重购、幂等、已支付拦截和状态回读，不连接真实支付、不扣款、不等待 Provider 回执。只有明确需要 Provider 集成的板块才使用 acceptance_mode=live。

支付板块在预发布默认使用虚拟验收，不要求真实商户配置、真实扣款或 Provider 回执。能力声明填写 `acceptance_mode: virtual`，并用本地虚拟适配器/确定性数据库事实完成 pending 重购、幂等回放、旧订单保留、已支付拦截和业务状态读回；`business_verified` 必须为真且观察项带 `effect_mode: virtual`。只有用户明确要求支付渠道联调时才使用 `acceptance_mode: live`，那是独立 Provider 验收，不得阻塞普通支付逻辑 PR。

预发布外部效果统一采用虚拟验收。预发布不连接真实支付、企微发送、群发、自动化 Provider、Webhook、分账或其他外部调度；相关板块只验证本地意图、幂等、队列接收、重放、状态转换、失败分类和业务读回，能力声明使用 `acceptance_mode: virtual`，观察项使用 `effect_mode: virtual`。未连接 Provider 的 503 归类 `external_config_unavailable`，不阻塞当前板块。只有用户明确授权渠道联调时才使用 `acceptance_mode: live`，并另行保留真实 Provider 证据。

### Merge preview candidate 与单一发布队列

发布候选必须由 `current main + PR` 生成 merge preview。预发布不得直接构建 PR 分支，也不得使用旧 head 的 receipt。候选 manifest 固定记录 `candidate_id`、`base_main_sha`、`pr_head_sha`、`merge_preview_sha`、`tree_sha`、`package_sha256`、影响板块和共享依赖；main 前进即使旧 receipt 为 accepted 也必须回到候选构建并重新验收。

预发布机维护非阻塞的 GitHub 源码镜像，仅用于增量 bundle 对象缓存。候选 bundle 优先以 `merge_preview_sha ^base_main_sha` 生成；缺少基线才传完整 bundle。Runner 不下载、不重新打包、不把发布包中转到生产。accepted 后由预发布机使用临时 0600 生产密钥、固定 known_hosts 和生产锁直推同一包，生产再次校验 tree、package SHA、active release、readyz 和板块读回。

生产晋级必须直接使用预发布机已验收的原始归档；不得回传本地、重新编译、重新打包或让 Runner 充当大包中转。预发布机只发送一次归档到生产，生产端使用既有 root wrapper、发布锁、installer、observer、回滚和 readiness 检查。预发布队列登记缺失时只能通过 `adopt-accepted` 形成显式 `waiting_merge` 事件，不能静默绕过单一队列。

生产网络或安装结果为 `outcome_unknown` 时，保留 attempt 与原幂等身份，只做版本、receipt
和 active release 的只读对账；禁止换 key、重传旧包或强行插队。用户明确延期真实业务
验收时，仍须完成同包、版本、健康、认证和指定技术读回，候选最多停留 `observing`；只有
延期旅程全部取得证据后才能进入 `released`。

客户同步/教研板块的预发布数据必须由 `deploy/seed-staging-business-fixtures.sh` 写入固定合成夹具；不得复制生产数据。夹具只提供可重复的同步记录、客户目录投影、教研课卡和映射事实，不能单独证明业务通过。必须再执行认证业务接口读回，并用 `scripts/validate-staging-fixture-readback.py` 核验 fixture 版本、数据摘要和 `business_verified=true`；无业务读回不得生成 accepted receipt。

发布队列由 `scripts/release_queue.py` 维护，一次只允许一个候选处于生产相关状态；普通领域可并行开发，Composition、迁移、公共组件、Provider、External Effects、部署脚本和 CI 变更串行合并。状态按 development、waiting_candidate、preview_building、staging_acceptance、frozen、waiting_merge、merged、production、observing、released、stale_candidate 记录。旧候选永不覆盖新候选。
