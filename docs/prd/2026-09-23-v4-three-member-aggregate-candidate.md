# PRD：CRM v4 三项运行时能力聚合候选

状态：按用户明确范围冻结；实施中  
工作项：`v4-three-member-aggregate-candidate`  
负责人：Codex task `01a0cd14-23ae-7701-be85-d7873e586ea9`  
分支：`codex/v4-three-member-aggregate-candidate`  
Worktree：`/Users/qianlan/Downloads/新CRM/v4-three-member-aggregate-candidate`  
GitHub 仓库：`qianlan33333-png/AI-CRM-v4`

## 1. 业务判断

### 目标

用一个新的聚合 PR 把共享 CI 基线修复、支付宝用户入口、群邀请固定码升级纳入同一个受保护合并单元，消除三个源 PR 因旧 `main` 基线问题相互阻塞的死锁。PR #15、#3、#13 的源提交和原分支保持不变；用普通 `--no-ff` merge commit 保留每个来源的完整 SHA/tree 与可审计祖先关系，不 rebase、cherry-pick、amend 或改写源 PR。

PR #19 的最终桥接治理提交也必须以普通 merge commit 纳入聚合分支并成为聚合 head 的祖先，避免要求 PR #19 先在同一旧基线上独立全 CI 通过。它是治理来源，不是运行时批次成员，不承担支付或群邀请的 staging receipt。聚合 PR 是唯一受保护的合并对象；三个运行时成员及 PR #19 的精确来源各自保留记录。PR #19 owner 已冻结 head `c69f7ff20b5495f024330154ea5045dd6ab7ca81` / tree `3dac7f3fb3eac4041242ced5d1b8b9c5c63d9078`；合入前仍以 GitHub 实时 SHA/tree 做一致性校验。

### 成员能力与验收规则

| 成员 | 精确源 head / tree | 用户可见能力与组合验收 |
| --- | --- | --- |
| PR #15 共享基线 | `3320bf1c3bcb910c4b47789db91321798b896db9` / `f87181b0c81ee2c465da28dd1fd43d9ff7dbc93a` | 修复共享完整 CI 基线，纳入受信预发来源校验、自动化收据引用、配置迁移和产品信息 DOM 空值保护等变更。验收完整后台、前端与浏览器集合；PR #15 的旧 head 全 CI run `35822413949` 全 lane 成功，但对应 staging 包只证明已安装，尚无完整业务 accepted receipt。 |
| PR #3 支付宝入口 | `78d6606c51484474e70e4c2ec6fac2ef20a77c88` / `c97b9b79243b868f2d8d0b347ed40935604e4e05` | 配置中心可启用支付宝；商品/服务周期页按启动配置显示渠道；微信内选支付宝时保留原订单并展示系统浏览器付款链接；“我已支付”只查询原订单状态；遵循现有 Order/Payment Unit of Work。验收虚拟支付、重复请求幂等、原订单保留和已支付拦截。 |
| PR #13 群邀请固定码 | `2204cbca0dd98b5ccce8272d34988ceb2ce5620a` / `c6c4c39374410e7fdacfd9d63d87d7adb7fe5617` | 显式升级旧双群计划，下载企微官方固定 join-way 图片；切群时继续使用原 `config_id`，企微确认前关闭下载入口，旧 `/gi/` 页面保持当前已执行群的二维码。验收升级、下载来源约束、切群状态和二维码读回。 |
| PR #19 首包桥治理（非运行时成员） | `c69f7ff20b5495f024330154ea5045dd6ab7ca81` / `3dac7f3fb3eac4041242ced5d1b8b9c5c63d9078` | 让桥校验绑定聚合 PR URL/head/tree 与三个运行时成员，核对聚合 head 的 required `check` 和绑定该 head 的 force-full 全量 CI；桥接治理提交必须是最终 preview 祖先。PR #19 自身旧基线运行不能冒充通过，也不进入 `batch.members`。 |

### 不变量

- 汇总候选的 runtime member 清单只能是 PR #15、#3、#13，成员各自保留准确 PR URL、源提交 SHA、源 tree SHA、工作项和归属任务；不得将四个来源改写成一个虚构业务工作项。
- 保护门禁只看聚合 PR 当前 head 的 `check` 成功，以及绑定同一 head 的完整 CI；源 PR 的失败仍按失败记录，不能写成通过。
- `main` 必须是聚合分支的准确祖先，且 `main` 在合并审查/候选冻结时必须重新读取。严格分支保护要求最新 main、required `check`、管理员不豁免和会话解决。
- 只创建草稿 PR；本任务不合并、不安装预发包、不部署生产、不申请或消费首包桥，也不更改 `6088e57ccf63b62dff46b4e1` 的观察状态。
- PR #15 的旧预发包不是聚合 tree 的同包包，不能作为聚合候选的安装、accepted receipt 或生产包证明。

## 2. 根因与来源证据

实时 GitHub `main` 为 `f07d76f2b1e3585bf930f5e9a2a37fe5bf87baaa`，tree 为 `61b37458a378572cc6d0bc454b42d4d329fe5c13`。PR #15、#3、#13、#19 当前可见 head 都以该 commit 为 merge-base。PR #3 和 #13 的旧 main 完整 CI 分别在 run `35827751623`、`35826833868` 失败；它们的共享 baseline lane 失败，PR #3 自身浏览器几何修复和 PR #13 群邀请专项通过。PR #15 的完整 CI run `35822413949` 在 head `3320bf1...` 上各代码 lane 与 `check` 成功。

相对 f07d76f2，#15/#3/#13/#19 当前 head 的改动文件集合两两无交集。该结果只说明静态路径未重叠，不替代实际 merge、组合测试或浏览器验收；若之后任一源 head/tree 变化，必须重新计算并重新审查。

## 3. 业务与架构分类

- **OneID：** PR #3 读取既有微信 OAuth 付款会话及 canonical customer，不新建身份匹配；PR #13 不引入客户身份解析；PR #15 的共享修复不改变身份 Owner。
- **Persistence：** 支付继续使用现有 Order/Payment PostgreSQL Unit of Work；群邀请升级复用现有 Media/Group Ops 归属和表；迁移 `0205` 是向前放宽配置键约束。没有新队列、身份模型或第二套状态机。
- **External Effects：** 支付写入继续走现有支付宝 Adapter/External Effects；群邀请复用现有 Outbound/EER 企微写路径；固定码读取为受信来源的 Provider/图片读取。所有预发验收必须使用 `virtual` effects，不扣款、不群发、不联系真实用户；真实商户付款、回调/退款和企微扫码/切群是生产业务观察项，不在本任务通过 CI 声称完成。
- **数据 Owner/事务：** 保留 PR 内既有模块 Owner 与事务边界；不跨领域写表，不把 Provider 网络调用放进 DB 事务。

## 4. GitHub 参考与仓库复用

| 参考 | 可借鉴点 | 本候选采用 |
| --- | --- | --- |
| [GitHub 受保护分支与 required checks](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches) | strict required checks 让 PR 在最新 base 上接受同一检查；保护门禁由分支规则执行。 | 保留 `main` 当前 `check` context、strict、管理员约束与 PR-only 合并，不伪造源 PR 状态。 |
| [GitHub 合并 PR 指南](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/merging-a-pull-request) | Draft PR 不能合并；合并前需满足分支保护规定。 | 草稿仅供完整候选审查与待 staging；通过检查仍不授权合并。 |
| 仓库 PR #15：[当前共享基线合并与全量 CI](https://github.com/qianlan33333-png/AI-CRM-v4/pull/15) | 以一个集成树修复此前分散的 CI/运行时基线问题；run `35822413949` 证明 exact head `3320bf1...` 的全 lane 结果。 | 保留源提交并在组合候选上重跑完整矩阵，不复用旧 head 状态或包。 |
| 仓库 PR #19：[首包批次桥](https://github.com/qianlan33333-png/AI-CRM-v4/pull/19) | 通过精确成员、旧观察、签名 source、同包和单次 bridge admission 保护首包路径。 | 将最终桥治理提交作为聚合祖先；运行时成员与治理提交分开建模。 |

复用仓库现有 `.github/workflows/ci.yml` 的 `workflow_dispatch(force_full=true)`、`scripts/ci/verification.py`、质量 lane、`release_batch.py`/deferred batch schema、PR19 桥 manifest、签名 freshness attestation、staging receipt 和 release-control 队列；不新建 CI 或发布机制。桥接 JSON 精确包含 `governance_pr:{pr_url,head_sha,tree_sha}`（PR #19 最终提交）、`aggregate_pr:{pr_url,head_sha,tree_sha}`、`full_ci_run_id`；校验 PR19 SHA/tree 及 `git merge-base --is-ancestor governance_head batch.aggregate_head_sha`。`batch.members` 只含 #15/#3/#13，桥 JSON 的 `members` 按相同顺序锁定其 PR URL/head/tree；`batch.first_v4_batch_bridge` 指向桥 JSON，admit 从 `batch.worktree` 的准确 aggregate HEAD 执行。required `check` 和同 head 全量 workflow_dispatch 的 `plan`、`governance`、`preflight`、`backend`、`frontend`、`browser`、`archive-sdk`、`check` 必须全成功。

## 5. 候选实现与 Git 历史

1. 从实时 `main` 精确 SHA 创建隔离 worktree/`codex/` 分支。
2. 依次用普通 Git merge 纳入 PR #15、PR #3、PR #13、PR #19 的最终治理 head。每个来源 head 必须是最终 aggregate HEAD 的祖先，且 `git show <source>^{tree}` 与 GitHub 实时 tree 匹配。
3. 如 Git 出现冲突，只在新聚合分支解决并记录每个冲突路径、选取理由与组合回归；不能改写源 PR。初步文件重叠审计结果为 0，但仍以真实 merge 结果为准。
4. 增加本 PRD、来源锁定表和 CI/验收结果，不把临时工具、密钥或 staging 环境文件提交进仓库。
5. 推送新分支并创建 Draft PR；PR 正文列出四个来源 SHA/tree、PR19 治理来源字段、精确 CI run ID、保护检查结果、仍未完成的 staging/业务验收项。

## 6. 验收门槛

### 本开发任务必须通过

| Gate | 判定 |
| --- | --- |
| 源提交保留 | PR #15、#3、#13 和 PR #19 最终治理 head 均为聚合 HEAD 祖先；原分支的远端 ref 未改变。 |
| GitHub required check | 当前聚合 PR head 的分支保护 `check` 为 SUCCESS；规则当前为 `strict=true`、context=`check`、管理员强制、禁止 force push/deletion。 |
| 完整全量 CI | 对同一个聚合 head 显式运行 `gh workflow run ci.yml -f force_full=true`；`plan`、`preflight`、`backend`、`frontend`、`browser`、`archive-sdk`、`governance`、`check` 均为 success，且 run.head_sha 精确相同。任何 lane 的 skipped/cancelled/unknown 均不算绿。部署 job 因本任务不部署而 skipped，不能称作测试通过。 |
| PR 生命周期 | Draft PR 存在且指向 `main`，merge 未执行；聚合 PR 的最新 head/tree 与 CI、成员清单一致。 |
| 本地验证 | `python3 scripts/dev_preflight.py fast`、`git diff --check`，并按组合影响运行失败用例/专项；Go 源码有改动时再运行适用 compile 专项。 |

### 本任务明确不宣称完成

- 不持有此聚合 preview 的签名 staging built receipt / accepted receipt / package SHA；PR #15 旧包不能替代。
- 不完成聚合包预发安装、认证业务读回、支付宝虚拟支付验收、群邀请组合验收或真实 Provider 验收。
- 不执行 `release-control` submit/adopt/merge/promote/execute，不占用生产队列。
- 不将旧候选 6088 标记 released、结束 observation 或为其新建 exception。PR19 bridge admission 与首包审批仍需由指挥台按精确、已审的聚合 head/tree 执行。

## 7. 预发、生产与监控顺序

后续发布任务须先重新读取当前 main 和聚合 PR head/tree，签发绑定当前 source bundle、base/head/preview/tree 的 freshness attestation；仅由唯一 Linux amd64 预发节点生成聚合候选包。构建和业务回读必须绑定同一 merge-preview 和 package SHA-256。三个运行时成员逐项保留 work item、owner、journeys、evidence digest 和 virtual effect mode；PR #19 治理代码只做 bridge 校验，不生成运行时 receipt。

预发业务验收包括：

- 支付：支付宝配置启用/关闭、公开入口、微信内系统浏览器 handoff、原订单状态查询、幂等/已支付拦截；全部使用虚拟 Provider，无真实扣款。
- 群邀请：旧计划升级、可信 HTTPS 官方二维码下载、相同 config 轮换、企微确认前关闭下载和认证 readback；使用隔离合成数据和虚拟外部效果。
- 共享基线：自动化多收件人 receipt 引用稳定且互不冲突、Retention/readback、公开产品联系信息空值保护、受信来源与发布文件校验。

生产推进必须在旧 observing 锁允许后由指挥台决定；桥接只能作用于精确批次，不得静默清队列。合并后 main tree 必须与已 accepted candidate tree 完全相同；生产只能晋级预发验收的同一包。观察期间单独核对支付宝真实商户配置/回调/退款和企微官方二维码扫码/切群承接；只有真实业务验收及观察完成才能进入 released。

## 8. 回滚、失败处理与隐私

- required check 或任一 full CI lane 未成功：保持 Draft，不合并，不部署；保留 run/artifact 与首次失败记录，修复只在新聚合提交追加，重新对最终 head 全量验证。
- 源 PR head/tree、main、candidate tree、receipt 或 package 任一移动/不一致：当前证据作废，重新读取/构建/验收，不得覆盖旧候选或复用旧 receipt。
- 预发或生产业务读回失败、付款/企微效果 outcome_unknown、健康或迁移检查失败：停止后续晋级并只读对账。上线后的回滚由指挥台使用保留的已知包和原安装器/锁/observer 完成；禁止手工改数据库或重试外部效果。
- `0205` 只扩大运行时允许键集合，回滚应用不能删除已存配置值。所有支付宝密钥、OAuth 会话、手机号、企微身份和 Provider 响应不得进入提交、PR 文本或日志。
- 本任务的代码回滚边界为聚合 PR 未合并时关闭/废弃新候选；合并后的 revert/生产回滚必须由独立发布操作完成，不改写受保护历史，也不触碰旧 observing candidate。

## 9. 并行与发布快照

```text
main HEAD/tree：f07d76f2b1e3585bf930f5e9a2a37fe5bf87baaa / 61b37458a378572cc6d0bc454b42d4d329fe5c13（开始时快照）
活跃 PR：#3、#13、#15、#19；#17/#18 是治理 PR，本业务包不纳入
共享冲突审计：当前各来源相对 main 改动路径两两无交集；另验证组合构建与测试
源 PR 状态：#15 full CI green；#3/#13 exact-old-main full CI red；#19 full/required 结果不得从旧 head 复用
生产候选：6088e57ccf63b62dff46b4e1，observing，保持不变
当前预发：PR #15 曾安装的包，业务验收未完成；不可当作聚合包
生产队列：沿用 state.json，不提交人工队列变更；本任务不 submit/adopt/merge/deploy
```
