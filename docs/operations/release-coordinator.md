# 发布指挥台运行合同

开发任务持有源码和 PR；指挥台只持有候选状态、证据、串行队列和部署操作。AI-CRM-v4 是唯一源码，生产基线提交不可改写。仓库公开且 `main` 已启用 required check、strict、管理员约束、禁止 force push/删除和会话解决；当前不使用 GitHub 原生 Merge Queue。

## 开发交接

开发任务按 `code_complete`、`staging_built`、`staging_self_accepted`、`handoff_ready`
四级记录进度。运行时 `handoff.json` 必须包含仓库、PR、独立 worktree、当前 GitHub
base main commit/tree、PR head commit/tree、merge preview SHA/tree、package SHA-256、
staging built/accepted receipt、受影响业务 readback、架构分类与回滚点。preview 以当前
main 和 PR head 为双亲。receipt 和每个证据文件都按 SHA-256 校验；HTTP 200、fixture、
`/readyz` 或布尔 `business_verified` 不能替代真实业务旅程。

纯治理/Skill/文档交接使用 `change_class=governance_only`，在 `code_complete` 后通过治理
检查进入 `handoff_ready`；`staging_built`、`staging_self_accepted`、runtime package、
staging app install 和业务运行时 readback 均明确为 N/A，并提供可校验的 governance
acceptance，不得创建虚假的 package 或 staging receipt。

```sh
python3 scripts/release_control.py handoff validate handoff.json
python3 scripts/release_control.py --state /secure/release/state.json \
  --coordinator-thread-id <thread-id> handoff submit handoff.json <candidate-id>
```

submit/resubmit 通过 GitHub API 核对 PR 目标仓库、实时 head/base 和 required checks；查询失败时失败关闭。开发任务必须先持久化交接或返工事件，再发送指挥台通知。相同候选重复交接幂等，不同源码不能复用 candidate ID。源码修复必须由原始开发任务提交新 commit、新 candidate、新 handoff、receipt 和事件，旧候选不可覆盖；指挥台不改源码、PR、cherry-pick、rebase 或解决冲突。

## 公开 main 新鲜度证明

预发布机访问 GitHub 可能超时。指挥台读取 `qianlan33333-png/AI-CRM-v4` 的公开分支 API，取得准确 main commit/tree，再签发短时证明：

```sh
python3 scripts/release_freshness.py issue \
  --repository qianlan33333-png/AI-CRM-v4 --branch main \
  --stage source --pr-head-sha <sha> --pr-head-tree <tree> \
  --merge-preview-sha <sha> --candidate-tree-sha <tree> \
  --bundle <candidate.bundle> --signing-key <ed25519-key> \
  --identity aicrm-release-command-center --out <attestation.json> \
  --signature-out <attestation.sig>
```

指挥台可通过 `GITHUB_TOKEN` 避免匿名限流，但 token 不写入证明、日志、bundle 或预发布机。规范 JSON 使用 OpenSSH `ssh-keygen -Y sign`，namespace 固定为 `aicrm-release-main-freshness-v1`。`source` 证明绑定 bundle；预发布构建完成后签发 `promotion` 证明，同时绑定同一 package SHA-256。

预发布使用固定 allowed-signers 文件验签，并核对有效期、仓库/分支、GitHub main SHA/tree、head/preview/tree、文件摘要和 `git bundle verify`：

```sh
python3 scripts/release_freshness.py verify \
  --attestation <attestation.json> --signature <attestation.sig> \
  --allowed-signers <allowed_signers> --identity aicrm-release-command-center \
  --repository qianlan33333-png/AI-CRM-v4 --branch main \
  --main-sha <expected> --main-tree <expected> \
  --pr-head-sha <expected> --pr-head-tree <expected> \
  --merge-preview-sha <expected> --candidate-tree-sha <expected> \
  --bundle <candidate.bundle> --git-repository <clean-repo>
```

签名、时效、字段、摘要或 bundle prerequisites 任一错误都拒绝。临时 SOCKS 只能用于一次性的只读 GitHub 核对，不能成为预发布长期依赖。

## 事件与通知

`release_events.py` 使用 flock、原子替换、lease、退避和 dead letter。指挥代理显式执行 `show` → `claim` → Codex 消息工具 → `finish sent|failed`；接收任务处理后再 `ack`。`sent` 只说明消息已投递，不说明接收方处理或生产效果成功。接收方按 event ID 去重。

## 串行发布队列

每次入队在最新 `main` 上重建 preview，核对 GitHub PR current head、required checks、签名证明、生产祖先和已验收 package。main、head 或 tree 前进即使旧测试为绿也必须生成新候选证据。

同一时间只允许一个候选占用合并/晋级/观察窗口。若已有 observing candidate，必须根据生产 receipt 完成观察后再处理下一项。队列冲突记录 `blocked_environment`；安装结果不明记录 `outcome_unknown` 并只读对账，不能换幂等键重试。

`release promote --prepare` 核对合并后 main tree、生产祖先、accepted 包和唯一队列占用。`--execute` 只允许预发布 Linux amd64 节点，以固定 Host Key、生产锁和现有部署脚本把同一包推送生产；本地 Mac、重新编译、旧 receipt、旧 head 或自动 adopt 都被禁止。网络尝试前写 attempt ID；成功只到 `observing`，生产版本、健康、认证、业务读回和观察完成后才到 `released`。

## 批次和延期验收

批次保留每个成员的 project/work item、origin task、commit/tree 和 receipt，不能把多个旅程改写成一个虚构工作项。成员数量和名称不硬编码；每个 project key 必须唯一，每个成员提交必须是 preview 祖先。

用户明确延期真实业务验收时，deferred manifest 必须包含唯一 exception ID、原始决定、具体 journey 列表、Owner、截止时间和后续动作。技术检查、包完整性、版本/健康/认证读回不能延期；生产状态最多为 `observing`，完成真实业务验收前不得写成 `released`。

## 失败分类

固定分类为 `assertion_or_verification_failure`、`environment_setup_failure`、`cancelled`、`pending_or_incomplete`、`unknown_failure`。failure envelope 记录候选、阶段、证据、要求动作和重新交接条件。环境失败不能冒称代码失败，替代环境通过不能冒称原环境通过。

## 首次 v4 运行时引导与支付宝入口修复

此段仅适用于 [一次性修复合同](../prd/2026-09-23-v4-bootstrap-alipay-repair-lane.md)。旧生产 SHA 不在 v4 Git 对象库；两棵树相同只是跨仓内容证据。普通候选继续要求生产 SHA 是 preview 祖先。**没有修复授权记录时，旧 `observing` 与祖先门禁均不变。**

指挥台保管仓外不可变 `bootstrap.json`，其 `schema=1`、`exception_id=first-v4-alipay-entry-repair-v1`，并含：

- `repository`；`legacy` 的旧 candidate/release/tree/package 和 `receipt: {path,sha256}`；`v4_root: {commit_sha,tree_sha}`。
- `candidate` 的 `pr_url`、`candidate_id`、`base_main_sha`、`head_sha`、`preview_sha`、`tree_sha`、`package_sha256`、`accepted_receipt_sha256`、`queue_owner_thread_id`、`origin_thread_id`。PR #3 当前 head 仅是候选快照；合并前必须重新取得当前 head、main 和双亲 preview，不复用旧 SHA。
- `production_readback: {path,sha256}`，文件记录十分钟内的旧 release/tree/package、`readyz.release_sha` 与 `ready`。生产晋级前仍由 `release_promote.py` 对真实生产再次执行 `/readyz` 读回。
- `queue_authorization` 和稍后的 `production_authorization` 分别含 `decision=approved`、`decision_id`、`user_thread_id`、`user_message_id`、`recorded_by_thread_id`、`decision_text`、`candidate_id`、`preview_sha`、`package_sha256`、`evidence: {path,sha256}`；生产决定另含 `old_release_sha`。证据 JSON 中的来源须为 `user_message`，并与决定字段逐项相同。操作者须直接核对 Codex 原始用户消息，再把记录写入受控状态。文件摘要能防事后篡改，但不能凭空证明消息真实性。

入队审批与生产安装审批互相独立。创建本合同、用户确认旧入口缺陷、PR/CI 通过均不是任一审批。PR #4 的构建来源签名修复是先行依赖，本合同不修改其脚本。PR #3 仍需当前 head 所需完整 CI、准确 merge-preview、Linux amd64 预发布构建/技术及业务旅程验收和 accepted package。修复通道必须由队列 Owner 使用 `--bootstrap` 明确选择；无参数的队列和祖先检查照旧拒绝。

```sh
python3 scripts/release_control.py release verify-lineage <worktree> <base-main> <active-production-sha> <preview-sha> --head-sha <pr3-head> --queue <queue.json> --bootstrap <bootstrap.json>
python3 scripts/release_queue.py --file <queue.json> adopt-accepted <accepted-receipt.json> --worktree <worktree> --bootstrap <bootstrap.json>
python3 scripts/release_control.py release promote --prepare --handoff <handoff.json> --queue <queue.json> --merged-main-sha <merged-sha> --production-sha <active-production-sha> --bootstrap <bootstrap.json>
```

如果使用 coordinator 状态机，`transition <candidate> preview_building --main-sha <main> --worktree <worktree> --bootstrap <bootstrap.json>` 是唯一修复入队入口；其余阶段延续原状态转换。两种队列文件不可同时作为同一生产队列使用。`repair_candidate_id` 锁定旧观察与准确新候选，`repairs_observation_id` 反向关联；旧观察继续 `observing`、支付宝 `not_accepted`，欢迎语/地址仍只是 `user_statement_only`。新的生产尝试另起 `observing`；未知结果时保留 reservation、attempt 和两条观察，先只读对账。回滚记录原活跃包、修复包及两条观察，不重置或改写旧观察。`release_queue.py` 禁止此候选直接转 `released`；真实商品页选择、实付、回调验签/幂等、查单、退款及状态读回由用户后测，并经独立收据审核后完成观察。
