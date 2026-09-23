---
name: aicrm-v3-development-frontdoor
description: "AI-CRM-v3 的开发前置门禁与并行交付流程。用于新功能、Bug 修复、调试、合并和上线前规划；要求先完成市场/GitHub 调研、复用评估和冻结 PRD，并持续推进到 GitHub 合并、SSH 部署和上线验收。"
---

# AI-CRM-v3 开发前置门禁

## 唯一代码来源

所有开发、测试、构建、发布和部署只允许使用当前 AI-CRM-v3 仓库的准确
commit 和 Git tree。禁止旧仓库 checkout、旧 commit、donor SHA、donor
manifest、旧运行时、旧数据库、旧接口或旧前端；缺失旧系统不得阻塞 v3。
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

## 一次闭环交付

当所有假设和风险边界确认、用户明确开始开发后，持续推进到完整上线验收，不把常规实现选择逐步退回用户。只有新的业务决策、红线风险、凭据/权限缺失或部署证据不一致才暂停并报告。

完整终点：实现 → 预发布机按当前准确 commit 编译并验收 → GitHub 轻量一致性门禁与合并 → 预发布机将同一已验收包串行晋级生产 → 部署后认证读回 → 观察窗口验收 → 触发一次“彩纸礼炮”庆祝。本地只做快速反馈和发布脚本自检，不再重复上传本地构建包。

生产部署完成且仅在版本、健康状态、认证读回、真实业务读回和观察窗口全部通过后，调用 Codex 的
`mcp__codex_app__fire_confetti` 一次庆祝本次上线。构建成功、PR 合并、部署开始、服务启动或仅有
`/readyz` 通过都不能触发；部署失败、回滚、结果未知或观察窗口未结束时不得触发。一次上线事件只触发一次，重复重试同一部署不得重复庆祝。

常规发布先在 `49.232.57.128` 预发布机从当前 v3 准确 commit 编译、安装并完成受影响板块验收，再合并 PR；receipt 必须绑定 repository、commit、tree、package SHA-256、capability 和 business readback。PR 只验证 receipt 与当前 tree 一致、治理和冲突状态。合并后由预发布机在发布锁下把同一已验收包串行晋级到 `124.220.53.183`，不重新编译和打包。生产只做版本、健康、认证和本次板块真实读回。生产部署私钥固定使用 `/Users/qianlan/Downloads/zhengshi.pem`，必须保持 `0600`，不得复制到仓库、PR、日志或命令输出。预发布使用同一账号和密钥时也必须单独核验 Host Key。staging 与 production 使用独立 concurrency/release lock；无关 Provider 不可用时标记 `external_config_unavailable`，只有声明为当前板块依赖时才阻塞。

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

## Bug 修复

保存真实失败证据，判断根因和影响面，先补回归测试或旅程，再做最小根因修复，执行原失败阶段和受影响全链路，并记录防复发措施。除非用户明确标注线上热修，不得走简化流程。

## 前端门禁

新增或修改前端必须使用 [@Product Design](plugin://product-design@openai-curated-remote) 和 [$artifact-template-crm](/Users/qianlan/.codex/skills/artifact-template-crm/SKILL.md)，优先复用管理端壳、标准选择器和共享组件。禁止单页私造标签、话术、群组、客服、商品等标准组件；仅文案、样式 token 或既有页面缺陷修复可走 audit/consistency 复核。

## 测试与合并

按影响范围先在本地执行适用的静态、编译/单元、PostgreSQL/迁移/事务、集成/权限、前端构建与真实浏览器、OneID、External Effects、发布包和回滚检查；涉及运行时代码的 PR 再在预发布机完成完整部署、合成数据业务旅程和读回。GitHub PR 默认只执行当前 head/tree、治理、冲突、证据和敏感信息一致性检查；运行时代码还必须核对预发布 receipt。纯 CI、治理、Skill 和文档变更不要求无意义的应用部署 receipt，也不重复本地长测试。完整云端 CI 仅在维护者显式使用 `workflow_dispatch` 的 `force_full=true` 时执行。

PR 保留准确 HEAD/tree、并行与发布快照、测试摘要、证据目录和未验证项。作者声明只作为上下文，不作为独立上线门禁；真正门禁是当前 head/tree、预发布 receipt、治理安全检查和部署后读回。编译通过、排队成功、Mock 或 HTTP 202 都不能单独称为完成。

## PR 证据留存

- PR 首轮 run/attempt/head/结果一旦产生不得覆盖；后续修复只追加事件。
- 当前 head 的最终结果只能由当前 head 的最新完整 required check 决定，旧 head 的绿灯不能替代。
- 固定分类为 `assertion_or_verification_failure`、`environment_setup_failure`、`cancelled`、`pending_or_incomplete`、`unknown_failure`；本地环境阻塞不能写成代码失败，替代环境通过不能冒称原环境通过。
- 每次修复必须记录修复提交、重跑阶段和最终结果；PR 正文只放轻量摘要与 artifact 链接，详细日志留在 artifact。
- 合并前必须有首轮和最终证据，未完成 lane、取消、skip、unknown 必须显式列出；合并后继续记录 merge SHA、部署 SHA、认证读回和观察窗口。
- 轻量 quality summary、首轮失败 manifest 和最终 check manifest 保留 90 天；详细 lane 日志、截图和大型测试包沿用 14/30 天策略。

### 国内预发布机与合并晋级

当前默认发布源是本地 v3 Git bundle。先在本地确认准确 commit/tree 并生成 `git bundle verify` 通过的 bundle，再由 `deploy/build-release-on-staging.sh` 上传到 `49.232.57.128`；预发布机不得依赖 GitHub 网络，也不得默认执行远端 clone。预发布机是唯一 Linux amd64 构建节点，必须完成二进制架构、包清单、安装、健康和受影响板块真实读回。

预发布构建必须持有 `/opt/aicrm/staging-build.lock` 单飞锁，锁覆盖构建目录清理、checkout、编译和 receipt 生成。并行构建不得共享目录或互相删除产物。`built` receipt 只能证明包来源，不能授权生产；完成安装和业务读回后才可写入 `accepted`。

合并后的 squash/rebase commit 允许与 staging 构建 commit 不同，但生产晋级前必须比较两者 tree 完全一致，并验证 accepted receipt 的 package SHA。生产使用 staging 已验收的同一包，不重新编译；任何 tree、receipt、包摘要或发布队列不一致都停止晋级。

### 预发布板块验收不可被无关 Provider 阻塞

每次 staging receipt 必须带能力声明：`affected_modules`、`required_routes`、`required_services`、`required_provider_dependencies` 和 `business_readback`。验收只执行声明的当前板块及明确共享依赖。未声明的 Provider 返回 `503 distribution_unavailable` 时必须用 `scripts/validate-staging-capability.py` 分类为 `external_config_unavailable` 并保留观察证据；不得把它写成代码失败，也不得阻塞当前板块。若该 Provider 在 `required_provider_dependencies` 中，则 503 仍是阻塞。Payment checkout 变更不自动要求 Distribution 商品链路；Distribution 变更才要求真实商品、资格和 Provider 读回。

验收辅助脚本不生成 accepted receipt，也不执行 HTTP 请求。required_routes 是非空路径字符串数组；observations 必须覆盖全部必需路由/Provider，并记录 status 与业务断言 business_verified。HTTP 200 本身不能替代业务验收。支付板块默认使用 acceptance_mode=virtual：在预发布用本地虚拟支付适配器或确定性数据库事实验证待支付订单重购、幂等、已支付拦截和状态回读，不连接真实支付、不扣款、不等待 Provider 回执。只有明确需要 Provider 集成的板块才使用 acceptance_mode=live。

支付板块在预发布默认使用虚拟验收，不要求真实商户配置、真实扣款或 Provider 回执。能力声明填写 `acceptance_mode: virtual`，并用本地虚拟适配器/确定性数据库事实完成 pending 重购、幂等回放、旧订单保留、已支付拦截和业务状态读回；`business_verified` 必须为真且观察项带 `effect_mode: virtual`。只有用户明确要求支付渠道联调时才使用 `acceptance_mode: live`，那是独立 Provider 验收，不得阻塞普通支付逻辑 PR。

预发布外部效果统一采用虚拟验收。预发布不连接真实支付、企微发送、群发、自动化 Provider、Webhook、分账或其他外部调度；相关板块只验证本地意图、幂等、队列接收、重放、状态转换、失败分类和业务读回，能力声明使用 `acceptance_mode: virtual`，观察项使用 `effect_mode: virtual`。未连接 Provider 的 503 归类 `external_config_unavailable`，不阻塞当前板块。只有用户明确授权渠道联调时才使用 `acceptance_mode: live`，并另行保留真实 Provider 证据。

### Merge preview candidate 与单一发布队列

发布候选必须由 `current main + PR` 生成 merge preview。预发布不得直接构建 PR 分支，也不得使用旧 head 的 receipt。候选 manifest 固定记录 `candidate_id`、`base_main_sha`、`pr_head_sha`、`merge_preview_sha`、`tree_sha`、`package_sha256`、影响板块和共享依赖；main 前进即使旧 receipt 为 accepted 也必须回到候选构建并重新验收。

预发布机维护非阻塞的 GitHub 源码镜像，仅用于增量 bundle 对象缓存。候选 bundle 优先以 `merge_preview_sha ^base_main_sha` 生成；缺少基线才传完整 bundle。Runner 不下载、不重新打包、不把发布包中转到生产。accepted 后由预发布机使用临时 0600 生产密钥、固定 known_hosts 和生产锁直推同一包，生产再次校验 tree、package SHA、active release、readyz 和板块读回。

生产晋级必须直接使用预发布机已验收的原始归档；不得回传本地、重新编译、重新打包或让 Runner 充当大包中转。预发布机只发送一次归档到生产，生产端使用既有 root wrapper、发布锁、installer、observer、回滚和 readiness 检查。预发布队列登记缺失时只能通过 `adopt-accepted` 形成显式 `waiting_merge` 事件，不能静默绕过单一队列。

客户同步/教研板块的预发布数据必须由 `deploy/seed-staging-business-fixtures.sh` 写入固定合成夹具；不得复制生产数据。夹具只提供可重复的同步记录、客户目录投影、教研课卡和映射事实，不能单独证明业务通过。必须再执行认证业务接口读回，并用 `scripts/validate-staging-fixture-readback.py` 核验 fixture 版本、数据摘要和 `business_verified=true`；无业务读回不得生成 accepted receipt。

发布队列由 `scripts/release_queue.py` 维护，一次只允许一个候选处于生产相关状态；普通领域可并行开发，Composition、迁移、公共组件、Provider、External Effects、部署脚本和 CI 变更串行合并。状态按 development、waiting_candidate、preview_building、staging_acceptance、frozen、waiting_merge、merged、production、observing、released、stale_candidate 记录。旧候选永不覆盖新候选。
