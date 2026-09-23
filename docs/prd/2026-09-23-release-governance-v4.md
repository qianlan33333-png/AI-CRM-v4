# AI-CRM-v4 发布治理基线 PRD

## 背景与目标

AI-CRM-v4 以当前生产版本的准确 Git tree 作为新仓库基线。该仓库是公开仓库，`main` 已启用 required check、严格同步、管理员约束、禁止 force push/删除和会话解决要求。发布仍由指挥台串行调度，不启用 GitHub 原生 Merge Queue。

本期建立可执行的开发交接、候选构建、同包晋级和观察治理，并解决预发布机访问 GitHub 偶发超时导致的 `main` 新鲜度核对问题。预发布不长期保存 GitHub 凭据或代理配置；指挥台从 GitHub 读取准确 `main` 后签发短时、内容绑定的新鲜度证明，预发布离线验签并核对候选、bundle 和包摘要。

## 业务判断逻辑

1. 每个工作项以不可变 handoff 进入指挥台，绑定仓库、PR、head、tree、基线、验证证据和受影响范围。
2. 指挥台一次只允许一个 Composition、迁移、公共组件、Provider、External Effects、CI 或部署脚本工作项进入共享窗口；普通领域可并行开发，合并与发布仍串行。
3. 候选必须由最新公开 `main` 与准确 PR head 生成 merge-preview。新鲜度证明必须包含仓库、分支、main/head/preview 的 SHA 与 tree、bundle/package 摘要、签发和过期时间、随机 nonce；字段使用规范 JSON 后由 Ed25519 SSH key 签名。
4. 预发布只信任固定 allowed-signers 公钥；验签、有效期、仓库/分支、候选 manifest、bundle/package 摘要和 tree 任一不匹配即失败。私钥和可选 GitHub token 不进入 bundle、receipt、日志或预发布机。
5. 预发布机是唯一 Linux amd64 构建节点。技术验收通过后，生产只使用同一已验收包；禁止本地重编译、旧 receipt、旧 head 或绕过 Host Key。
6. 若生产已有 observing candidate，必须先依据生产 receipt 完成观察；未知结果不得换幂等键重试或强行插队。
7. 用户可明确延期真实业务验收，但必须生成独立、可审计的 deferred acceptance，列出具体旅程、Owner、截止时间和技术收据；技术检查不能被延期。
8. PR 合并前和部署前都重新核对 GitHub 当前 head/main、tree、required check、队列所有权和证明有效性。GitHub 原生保护是门禁之一，不能替代预发布收据和业务读回。

## 公开方案与复用评估

- Git bundle 支持离线传输 Git 对象，并通过 `git bundle verify` 校验前置对象，适合在预发布直连不稳定时传递准确对象集：https://git-scm.com/docs/git-bundle
- GitHub REST Branches API 可读取公开仓库分支；公开读回无需在预发布保存令牌，指挥台可选使用最小权限令牌避免匿名限流：https://docs.github.com/en/rest/branches/branches
- GitHub Rulesets/branch protection 负责 required check、严格同步和禁止破坏性更新；本仓已启用可用的 `main` 保护：https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets
- GitHub Artifact Attestations 可作为云端制品证明参考，但本期构建发生在预发布机且需要离线核验，因此采用 Git/OpenSSH 自带的 `ssh-keygen -Y sign/verify`，不引入第二个发布服务：https://docs.github.com/en/actions/concepts/security/artifact-attestations

复用现有 `release_candidate.py`、安装器、固定 Host Key、Linux amd64 构建、release receipt 和生产观察逻辑。新增治理脚本只管理 release metadata，不修改业务表、运行时领域或 Provider。

## 数据与接口

- `handoff.schema.json`：工作项身份、准确 commit/tree、依赖、验证和交接状态。
- `release-event.schema.json`：持久事件、租约、重试、死信和接收方确认。
- `release-batch.schema.json`：一个串行窗口内的候选集合与聚合候选。
- `failure-envelope.schema.json`：失败类别、阶段、证据和可恢复动作。
- `deferred-business-acceptance.schema.json`：用户明确延期的真实业务旅程。
- `main-freshness-attestation.schema.json`：公开 `main` 读回和候选/bundle/package 摘要的短时签名证明。

所有本地状态采用原子文件替换和进程锁；其内容是发布控制面事实，不是业务数据。JSON schema 负责交换边界，Python 验证负责跨字段、Git lineage、摘要、签名和时效规则。

## 架构分类

- OneID：不涉及。发布元数据不解析、创建或合并客户身份。
- Persistence：本地文件持久化。队列、事件、handoff、receipt 和锁位于发布控制目录；不写业务 PostgreSQL，也不复用业务 jobqueue。
- External Effects：GitHub REST 是公开只读或最小权限只读；SSH 安装是由指挥台在已有固定 Host Key、生产锁和同包约束下执行的受控外部效果。业务 Provider 不涉及。

## 验收标准

- handoff、事件、批次、延期验收、失败信封均有 schema 和失败关闭的专项测试。
- 公共 `main` 读回必须与仓库、分支和 40 位 SHA 精确匹配；网络失败不能被解释为“没有更新”。
- 新鲜度证明在签名被改、过期、仓库/分支错误、main/head/preview/tree 错误、bundle/package 摘要错误时均拒绝。
- 私钥和 GitHub token 不写入证明、bundle、receipt 或测试夹具输出；预发布只需公钥/allowed signers。
- 队列拒绝缺少登记、跳跃状态、并发晋级、旧 head、非生产祖先、包摘要变化和 outcome unknown 重放。
- `python3 scripts/dev_preflight.py fast` 及发布治理专项 Python 测试通过，证据绑定准确 commit/tree。

## 上线、监控与回滚

该 PR 只新增治理能力，不部署业务运行时。合并后由指挥台开始使用新 schema 和命令；首次真实发布前先在临时控制目录演练完整 handoff → 候选 → 验签 → 技术收据 → 晋级准备流程。

监控队列租约、死信、过期证明、stale candidate、promotion reservation、outcome unknown 和观察中候选。回滚治理代码使用 Git revert；已签发证明保持不可变并按过期时间自然失效，不删除历史收据或篡改事件。
