# CRM v4 国内构建与内网串行发布 PRD

日期：2026-09-23。基线：公开仓库 `AI-CRM-v4` 的 `main`。本 PRD 已由用户在本任务中确认。

## 业务判断

- 一个用户可观察能力对应一个 PR。不同板块可并行开发；生产按合并后 `main` 的第一父链逐个发布，后一个版本包含前一个版本。
- GitHub 是唯一主仓库与 PR 审核入口。GitHub 上只运行 PR/提交检查，不构建或传送生产安装包。预备机 `10.0.4.6` 拉取准确的已合并提交，在国内构建、安装和做基础验证；再通过内网向 `10.0.4.13` 晋级同一文件树。
- 正常技术发布自动晋级。真实支付、扫码等业务结果在生产部署后独立记录，不占用后续技术发布通道。生产安装与健康成功不能写成真实业务验收成功。
- 普通页面改动不备份数据库，也不运行迁移。只有涉及数据库的提交才自动备份和运行迁移专项检查；有破坏性或无法前向兼容的迁移在 PR 检查阶段拒绝。
- PR #22 继续暂停；本流程不会自动合并 PR。

## 参考与复用

- GitHub 保护分支与必需状态检查：https://docs.github.com/en/pull-requests/reference/status-checks
- GitHub 准确提交的检查结果查询：https://docs.github.com/en/rest/checks/runs
- Forgejo 的自托管 PR/Runner 方案：https://forgejo.org/docs/v15.0/user/collaboration/pull-requests-and-git-flow/ 和 https://forgejo.org/docs/v15.0/admin/actions/security/ 。本次选择保留现有 GitHub PR，避免再维护一套代码平台与 Runner。
- 复用当前 `main`、`scripts/run-donor-view-consumers.sh` 的构建输入、`release-files.sha256`、`/readyz`、systemd 服务与原子版本目录。以新的轻量发布路径取代 merge-preview、候选 handoff 和旧观察占位门禁。

## 边界

- OneID：不涉及；发布器不读取或分配客户身份。
- Persistence：只持久化最小技术发布游标、安装收据及锁。业务数据仍由原 PostgreSQL Owner 持有；预备机只用合成数据。
- External Effects：生产服务启停是运维效果，不新增支付或企微 Provider 调用。部署结果不明时只读对账，不换身份盲目重试。
- 仓库之外的商户平台配置不由代码发布器修改。运行所需密钥留在目标机，构建任务不能读取生产部署凭据。

## 验收与失败处理

1. 多个 PR 连续合并后，预备机按 `main` 第一父链顺序处理每个通过 `check` 的提交；失败则停在该提交，不跳到后一个。
2. 纯静态改动不编译 Go；Go 改动只重编依赖图涉及的命令；未知或构建基础改动完整构建。生产仅传输变化文件，安装后完整摘要与预备机一致。
3. 预备机基础旅程、服务和 `/readyz` 通过后自动晋级；生产准确 SHA、健康和服务读回成功才推进技术游标。失败自动回滚；结果不明停止并只读对账。
4. 不改数据库的页面版本没有数据库备份或迁移动作；数据库版本自动备份并验证完成，迁移检查失败阻断发布。
5. 首次启用前核对当前生产安装树与 GitHub `main`，建立一次性基线；新旧发布入口不能同时有生产写权限。
