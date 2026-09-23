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
