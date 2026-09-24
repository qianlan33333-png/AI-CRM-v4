# AI-CRM v4

AI-CRM v4 是当前唯一的代码、测试、构建、发布和部署仓库。首个提交的 Git tree 与当时生产运行版本一致；后续能力只在本仓演进。

历史来源仅用于说明基线，不是开发供体或运行时依赖：

- `AI-CRM-production@4af15e64fb7ebb311b52b17eaf5fc5ea5e8154c8`：生产行为与 OneID 参考；
- `AI-CRM@69c5282fb38058f2cc9872b6feb3f0f54bfad64b`：管理后台和企微侧边栏视觉壳。
- `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`：客户列表、游标分页和企微目录同步 Behavior Contract。

禁止从旧仓库 checkout、复制代码、构建或部署。

## 本期能力

- 渠道中立 `customers.id`、外部身份唯一约束、冲突、关联意图、合并候选、管理员确认归并和可审计撤销；
- 本地 Argon2id 密码、数据库 Session、CSRF、登录限流、固定三角色和 `session_version` 即时失效；
- 企微员工登录、侧边栏 OAuth、JSSDK 签名、客户上下文令牌、外部联系人加密回调和幂等 Inbox worker；
- 独立客户联系 Secret、可恢复企微全量同步、逐项收据、Outbox 目录投影和 02:30 外部 oneshot 对账 timer；
- `/admin/customers` 列表/详情、固定 watermark 游标、精确手机号筛选、脱敏与审计揭示；
- `cmd/migrate-phone-identities` 只接受已校验快照，支持 `inspect/dry-run/apply/reconcile/rollback`，不长期连接源生产环境；
- 后台完整 CRM 菜单、登录/首页/员工权限/OneID 查询页和侧边栏壳；尚未开发的业务统一显示“功能待接入”，不会调用旧 API；
- 支付宝已实现 WAP/网页支付、签名回调、交易查询、退款与对账 Adapter；Provider 默认
  关闭，启用真实网络调用必须提供部署侧商户凭据，并继续遵守幂等、回调重放和
  `outcome_unknown` 对账边界；
- `main` 受保护且必过准确提交的 `check`。GitHub Actions 只做 PR 检查；国内预备机按合并后的第一父链构建并通过内网晋级同一版本。

公开 HTTP 契约见 [OpenAPI](api/openapi.yaml)，数据迁移见 [migrations](migrations)，部署约束见 [部署说明](deploy/README.md)。

## 本地运行

需要 Go 1.26 和 PostgreSQL 16。先创建数据库并执行迁移：

```bash
export AICRM_DATABASE_URL='postgres://aicrm:password@127.0.0.1:5432/aicrm?sslmode=disable'
go run ./cmd/migrate-platform
```

首次启动可用环境变量幂等创建超级管理员：

```bash
export AICRM_BOOTSTRAP_USERNAME='admin'
export AICRM_BOOTSTRAP_PASSWORD='replace-with-a-strong-password'
export AICRM_BOOTSTRAP_DISPLAY_NAME='系统管理员'
make run
```

默认只监听 `127.0.0.1:8080`。公网流量由 Caddy 在 80/443 终止 HTTPS 后反向代理：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

`AICRM_WECOM_ENABLED=false` 和 `AICRM_WECOM_CUSTOMER_SYNC_ENABLED=false` 是安全默认值。客户同步还必须单独提供 `AICRM_WECOM_CONTACT_SECRET`；它不复用 OAuth 应用 Secret。

企微标签目录读取另有最窄的独立开关：`AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=false`。`id-dev` 保持关闭；只有同时明确配置 `catalog-read-authorized` 权限时才允许启用，只读取企业标签目录，不包含客户打标或去标。目录创建、改名和归档另需 `AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED=true`、`AICRM_OUTBOUND_PROVIDER_ENABLED=true`、可用企微通讯录凭据以及独立的 `catalog-write-authorized` 确认；读取授权永远不会开启写入。

## 验证

```bash
make check
go test -race ./cmd/aicrm ./internal/access/... ./internal/customer/... ./internal/identity/... ./internal/wecom/...
govulncheck ./...
```

## 开发与发布

每个 PR 交付一个独立可合并、可回退的用户可观察行为或明确缺陷，并在同一 PR 提交相关测试；按行为边界拆分，不设行数配额。整个实施留在同一个 Codex task，子 PR 复用已授权的父 brief。涉及侧栏、用户页或后台页时，编码前使用 Product Design 插件/skill。先阅读
[`docs/development-before-start.md`](docs/development-before-start.md)，按改动影响运行本地检查。

`python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` 显示候选影子计划与当前 enforced 选择；它不改变 GitHub 必需 `check`。普通本地运行仍执行 `fast`，Go 改动加 `compile`，只汇报本地范围。候选在 10 个有效 PR 中零已知漏选、配对中位耗时至少降低 30%、且每个能力的检查总耗时不增加后，按已授权标准启用；否则继续影子观察。未知、共享构建基础、可执行检查策略和迁移改动保持全量回退。禁止给必需工作流添加路径过滤。发布失败诊断和修复由单独的 `gpt-6-luna` max agent 执行；其他工作不受此模型限制。

PR 合并后，国内预备机按 `main` 第一父链顺序拉取准确提交、构建并使用合成数据做基础验证，
再通过内网晋级同一文件树。预备机数据可重建，无需备份；生产真实数据仅在数据库迁移前备份。
生产安装、健康读回和真实业务验收分开记录。日常操作与失败恢复见
[`docs/operations/domestic-release.md`](docs/operations/domestic-release.md)。旧 handoff、merge-preview
和手工发布入口保留为历史审计材料，不是新流程门禁。
