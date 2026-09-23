# PRD：本地发布归档布局预检修复

- 状态：已确认冻结（范围按用户明确任务冻结）
- 负责人：本开发任务
- 板块：发布与部署工具
- 分支/worktree：`codex/v4-local-archive-layout-preflight` / `AI-CRM-v4-local-archive-layout-preflight`
- 预计上线窗口：不在本任务部署；后续由发布指挥台按串行队列处理

## 1. 业务判断

- 用户与场景：维护者用 `scripts/deploy-release-local.sh` 将一个已构建的 v4 发布归档上传并安装到目标环境。
- 要解决的问题：脚本的本地二进制预检把归档根误当成 `release/` 子目录。准确复现输入是 PR20 源码签名构建包 `aicrm-cefa98c4eeb109a6e126d926a902242b209d0f52.tar.gz`，SHA-256 为 `272dff10483999257671d8c0ebcb3dfe9647f5723f634ec997dafaa83444b38c`；用户提供的事实是归档根直接包含 `bin/`、`migrations/` 等目录。远端 `deploy/install-release.sh` 则将归档根解到 `release_dir` 并要求 `release_dir/bin/`。本地当前代码在 `release/bin` 不存在时解到临时根，却检查不存在的 `$tmp_check/release/bin`，使合法包在上传前失败；当工作区恰有 `release/bin` 时又可能检查到过期目录而不是输入包。
- 核心规则与边界：每次都从实际输入归档根预检；安全检查通过后才读取包内 `bin/` 做 ELF 检查。平铺根布局符合合同；额外的 `release/` 包装层、缺少 `bin/`、`migrations/` 或安装器要求的根 `release-files.sha256`、路径穿越、重复冲突路径、链接或其他非普通文件成员均失败关闭，且不得开始 SSH/SCP。预检失败不能被本地残留目录绕过。
- 成功标准：合法根布局可以定位真实归档的 `bin/` 并通过既有 Linux amd64 ELF 检查；错误布局或不安全成员在任何 SSH 前被拒绝；原有 commit SHA 文件名约束、迁移序列检查、上传前后包 SHA-256 对比、固定 Host Key、远端 installer 与持有 root 锁的 wrapper 均保留。
- 明确不做：不改 PR20、PR20 包、预发候选或生产；不上传、不安装、不部署、不合并；不调整生产锁、Host Key、发布队列或任何业务工作项。

## 2. 市场与 GitHub 调研

| 来源 | 类型 | 可借鉴能力 | 采用/舍弃 | 原因 |
| --- | --- | --- | --- | --- |
| [microsoft/apm-action `src/bundler.ts`](https://github.com/microsoft/apm-action/blob/main/src/bundler.ts) | GitHub 发布包处理方案 | 解包前枚举归档成员，以根标记识别布局；解包边界单独处理 | 采用“先校验归档目录表、后安全解包”的顺序；不采用其面向多格式 bundle 的标记发现逻辑 | v4 发布包有固定的平铺根合同，应直接验证 `bin/` 与 `migrations/`，不猜测格式 |
| [DeusData/codebase-memory-mcp `install.sh`](https://github.com/DeusData/codebase-memory-mcp/blob/main/install.sh) | GitHub 安装器 | 解包前验证完整归档命名空间，拒绝超出封闭布局的内容 | 采用路径、类型和预期根目录的失败关闭原则；不照搬其 Windows/ZIP 分支和产品专属文件清单 | 本脚本只处理当前 v4 `.tar.gz`，并继续复用本仓二进制与迁移合同 |
| [actions/upload-pages-artifact hosted-runner test](https://github.com/actions/upload-pages-artifact/blob/main/.github/workflows/test-hosted-runners.yml) | GitHub Actions 工件验证 | 真实解包后检查 symlink 并比较往返目录 | 采用真实归档夹具覆盖平铺根、错误包装层与不安全成员；不引入 artifact action 依赖 | 该缺陷在归档路径与解包后目录之间，测试必须覆盖真实 tar 内容 |

仓内复用结论：`scripts/create-release-archive.py` 以 `release` 目录内容作为归档根，并拒绝 symlink 与 AppleDouble；`scripts/check-release-binaries.py` 是现有 ELF 门禁；`scripts/check-migration-sequence.py` 是现有迁移序列门禁；`deploy/install-release.sh` 按已解出的 `release_dir/bin/` 安装。实现应连接这些既有合同，不复制或放宽它们。

## 3. 复用与架构分类

- 已有领域/模块：发布 shell 脚本、归档生成器、ELF 检查器、迁移序列检查器和远端 installer。
- 共享组件与标准组件：只复用以上发布工具；不引入新依赖或通用部署框架。
- OneID：不涉及；不读取客户或外部身份。
- Persistence：无业务持久化。仅读取归档、创建并清理私有临时目录。
- External Effects：修复后的预检本身无 Provider 或网络效果；其下游原有 SSH/SCP 部署效果不在本任务执行，部署协议和门禁保持不变。
- 数据 Owner、事务边界：无业务数据表、迁移、事务或业务数据 Owner。
- 复用、扩展和舍弃方案：扩展本地部署预检并为归档验证增加可测试逻辑；继续调用现有 ELF/迁移检查；不改远端安装流程，不依赖本地 `release/` 目录。
- 无重复能力：不新增身份匹配、持久队列、Worker、重试状态机或 Provider 调用。

## 4. 产品与技术边界

- 用户流程：输入归档和 40 位 commit SHA → 运行仓内迁移序列检查 → 枚举并验证输入 tar 成员 → 安全提取到私有临时目录 → 对实际归档根 `bin/` 运行现有 ELF 检查 → 仅预检成功后进入原有 SSH/SCP/远端部署流程。
- API/Port/事件：无业务 API、Port、数据库事件或 Provider 接口。
- 权限、审计和错误语义：临时目录只由当前执行用户访问；绝对路径、`..`、重复路径、符号/硬链接、设备/管道或错误根布局视为归档完整性失败并以非零码结束；不得继续到 SSH。新增预检器及其测试按现有 local-first gate 的 operator-only 分类处理，不要求应用 staging receipt；任何应用运行时路径仍触发 receipt 要求，回归测试固定这一边界。
- 并行依赖与共享入口：只改独立 worktree；PR20 仍保持自己的分支、包和预发状态。检查并发发布窗口，但本开发不占用部署槽位。

## 5. 测试与验收

| 测试层 | 是否适用 | 命令/旅程 | 证据位置 | 结果 |
| --- | --- | --- | --- | --- |
| 静态/架构/敏感信息 | 适用 | Shell/Python 静态检查及仓库发布合同检查 | 准确 head 的 CI | 待运行 |
| 编译/单元/race | 部分适用 | 归档验证器单元测试；不改 Go 运行时代码 | 准确 head 的 CI | 待运行 |
| PostgreSQL/迁移/事务 | 不适用 | 不改迁移或数据库；保留本地迁移序列检查 | PRD 与代码差异 | N/A |
| 集成/权限/真实读回 | 适用 | shell 集成夹具：合法平铺包走到 SSH 边界；错误布局和恶意成员在 SSH 前失败 | 准确 head 的 CI | 待运行 |
| 前端 build/组件合同 | 不适用 | 无前端变更 | PRD 与代码差异 | N/A |
| Chromium Journey | 不适用 | 无应用页面或业务旅程变更 | PRD 与代码差异 | N/A |
| OneID | 不适用 | 不处理客户或外部身份 | 分类结论 | N/A |
| External Effects | 不执行 | 测试伪造 SSH 边界，不连接任何目标；不触发 Provider | 准确 head 的 CI | 待运行 |
| 发布/部署后验收 | 不执行 | 本任务只验证本地预检和 GitHub CI；不上传/安装/部署 | 本 PR 与状态事件 | 明确不在范围 |

必要回归案例：正确平铺归档根（含 `bin/`、`migrations/`、`release-files.sha256`）通过；`release/bin/` 包装层、缺少必需根成员、空 bin、路径穿越、绝对路径、重复或冲突成员、symlink/hardlink/特殊文件失败；本地残留 `release/bin` 不能绕开对输入归档的检查；失败案例没有任何 SSH 调用。

发布脚本属于高风险门禁。提交后运行准确 PR head 的完整 CI（workflow dispatch `force_full=true`）和 `main` required check `check`；所有结论绑定同一 commit/tree。测试通过不等于 staging 或业务验收。

## 6. 并行与发布快照

```text
当前 GitHub main / 本地 HEAD：f07d76f2b1e3585bf930f5e9a2a37fe5bf87baaa
当前 main tree：61b37458a378572cc6d0bc454b42d4d329fe5c13
活跃分支与 PR：PR20 为 draft，分支 codex/v4-three-member-aggregate-candidate，head afb13625f6918ac723fb1c1ae5a2b942292865f3；另有若干开放 PR。
重叠文件/模块/迁移/API：PR20 修改 `.github/workflows/ci.yml` 与发布流程；本修复只增加 `scripts/ci/quality_lanes.py` 中的 preflight 测试项，并修改独立的本地归档预检、专项测试和 PRD。PR20 的文件清单不含这些路径；不修改 `.github/workflows/ci.yml`、迁移、API 或包内容。
其他已合并但未部署版本：以发布指挥台状态为准；本任务不晋级任何候选。
当前排队上线版本：state.json 中存在旧的 production observing 项；保持原状态，不重排或结束观察。
正在执行的部署或观察窗口：旧 production observing；本任务不执行部署，不占用该窗口。
本次合并需要重新运行的测试：准确 head 的完整 CI、required check `check` 与归档布局专项回归。
```

状态来源仅为 `/Users/qianlan/Downloads/新CRM/release-control/state.json`。任何 `release_events.py` 命令必须显式传入 `--state` 指向该文件。

## 7. 上线、监控与回滚

- 完整交付确认：代码、归档布局专项测试、准确 head 的 full CI/required check、独立 Draft PR 与持久 outbox 事件完成；这些不代表部署验收。
- GitHub PR 合并提交：本任务不合并。
- SSH 目标：本任务不连接任何目标。
- SSH 凭据：不读取、不更改。
- Host Key/known_hosts 校验：保留现有 `StrictHostKeyChecking=yes` 与显式 `UserKnownHostsFile` 约束。
- 部署后认证读回：本任务不部署；未来指挥台按现有合同完成。
- 监控与观察窗口：维持现有状态，不新增观察任务。
- 回滚触发条件与步骤：若预检拒绝合法包或未能拦截错误/不安全布局，修复后再更新 Draft PR；合并前可关闭 PR 或回退新提交。不会为本 PR 操作生产回滚。

## 8. 确认与变更

- 用户确认：用户明确授权本独立缺陷修复、测试、full CI/required check 和 Draft PR，并禁止影响 PR20、预发候选及生产；本 PRD 将该范围冻结。
- 确认时间：2026-09-23（任务指令）
- 重大变更及重新确认记录：无。
- 未验证项：用户给出的 PR20 包名与 SHA-256 作为复现事实记录；本任务不读取、修改、重新打包或部署该归档。
