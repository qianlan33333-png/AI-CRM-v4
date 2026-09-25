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
- 切换激活前，GitHub `main` 和准确 `check` 决定旧发布队列；激活后预备机国内裸仓为权威，候选经国内检查、同包内网晋级，GitHub 只由用户人工择机归档。

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

每个能力先做一次业务判断、参考检索和简短父 PRD；同一 Codex 任务中，每条开发线使用独立 `codex/<work-item>` worktree/分支。一个候选交付可单独上线的最小完整行为或明确缺陷，相关测试同行。涉及用户页或后台 UI 时先使用 Product Design。详见[开发入口](docs/development-before-start.md)。

国内主仓**仅在受锁激活收据存在且旧轮询器停用后**生效。此前继续使用 GitHub PR、必需 `check` 和[旧发布流程](docs/operations/domestic-release.md)。激活后开发者只推国内分支并提交准确 base/head；单一发布器在预备机检查、构建和合成数据验收，在生产机先保存可独立恢复的源码 bundle，再通过内网晋级同一安装包。生产准确版本、摘要、服务与健康读回通过才推进国内 `main`。日常操作见[国内主仓发布](docs/operations/domestic-main-release.md)。

普通发布不备份数据库；生产迁移前才备份。真实支付、扫码等业务验收在技术安装后独立记录。GitHub 不自动同步、没有同步周期；人工同步须先确认国内 `main` 与生产源码收据相同、GitHub 是其祖先，才普通快进推送并读回 SHA。若两台国内机器在归档前同时丢失，GitHub 可能缺少尚未同步的提交。
